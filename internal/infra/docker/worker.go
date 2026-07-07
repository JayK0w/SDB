package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// RunWorker creates the ephemeral worker container, streams its
// (demultiplexed) output to stdout/stderr and blocks until it exits. The
// container is force-removed in every code path, including context
// cancellation, so no worker ever leaks.
func (r *Runtime) RunWorker(ctx context.Context, spec domain.WorkerSpec, stdout, stderr io.Writer) (int, error) {
	if err := r.ensureImage(ctx, spec.Image); err != nil {
		return -1, err
	}

	labels := map[string]string{workerLabel: "true"}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	mounts := make([]mount.Mount, 0, len(spec.Mounts))
	for _, m := range spec.Mounts {
		mounts = append(mounts, toDockerMount(m))
	}
	hostCfg := &container.HostConfig{Mounts: mounts}
	if spec.NetworkMode != "" {
		hostCfg.NetworkMode = container.NetworkMode(spec.NetworkMode)
	}

	created, err := r.cli.ContainerCreate(ctx,
		&container.Config{Image: spec.Image, Cmd: spec.Cmd, Env: spec.Env, Labels: labels},
		hostCfg, nil, nil, "")
	if err != nil {
		return -1, translate(err)
	}
	defer func() {
		// Removal must survive a canceled ctx (that is exactly when
		// cleanup matters most).
		rmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := r.cli.ContainerRemove(rmCtx, created.ID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
			r.logger.Warn("failed to remove worker container", "id", created.ID, "error", err)
		}
	}()

	if len(spec.Files) > 0 {
		if err := r.copyFiles(ctx, created.ID, spec.Files); err != nil {
			return -1, err
		}
	}

	attach, err := r.cli.ContainerAttach(ctx, created.ID, container.AttachOptions{
		Stream: true, Stdout: true, Stderr: true,
	})
	if err != nil {
		return -1, translate(err)
	}
	defer attach.Close()

	if err := r.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return -1, translate(err)
	}

	copied := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(stdout, stderr, attach.Reader)
		copied <- err
	}()

	waitCh, errCh := r.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		// Unblock the copy goroutine before returning so that no write to
		// stdout/stderr can happen after RunWorker has returned (callers
		// rely on this to close their event pipelines safely).
		attach.Close()
		<-copied
		return -1, translate(err)
	case res := <-waitCh:
		if err := <-copied; err != nil && !errors.Is(err, io.EOF) {
			r.logger.Warn("worker output stream ended abnormally", "id", created.ID, "error", err)
		}
		if res.Error != nil {
			return -1, fmt.Errorf("waiting for worker: %s", res.Error.Message)
		}
		return int(res.StatusCode), nil
	}
}

// copyFiles injects secret files into the created (not yet started)
// worker as a tar stream: keys and service accounts never touch the host
// filesystem or the container environment.
func (r *Runtime) copyFiles(ctx context.Context, id string, files map[string][]byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	paths := make([]string, 0, len(files))
	dirs := map[string]bool{}
	for p := range files {
		paths = append(paths, p)
		for d := path.Dir(p); d != "/" && d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	sort.Strings(paths)
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList) // parents before children

	for _, d := range dirList {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir,
			Name:     strings.TrimPrefix(d, "/") + "/",
			Mode:     0o700,
		}); err != nil {
			return fmt.Errorf("building secret archive: %w", err)
		}
	}
	for _, p := range paths {
		content := files[p]
		if err := tw.WriteHeader(&tar.Header{
			Name: strings.TrimPrefix(p, "/"),
			Mode: 0o600,
			Size: int64(len(content)),
		}); err != nil {
			return fmt.Errorf("building secret archive: %w", err)
		}
		if _, err := tw.Write(content); err != nil {
			return fmt.Errorf("building secret archive: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := r.cli.CopyToContainer(ctx, id, "/", &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("injecting worker files: %w", translate(err))
	}
	return nil
}

func toDockerMount(m domain.Mount) mount.Mount {
	src := m.Source
	if m.Type == domain.MountVolume && m.Name != "" {
		src = m.Name
	}
	out := mount.Mount{
		Type:     mount.Type(m.Type),
		Source:   src,
		Target:   m.Destination,
		ReadOnly: m.ReadOnly,
	}
	if m.Type == domain.MountBind {
		// Local repositories point at host paths that may not exist yet.
		out.BindOptions = &mount.BindOptions{CreateMountpoint: true}
	}
	return out
}

func (r *Runtime) ensureImage(ctx context.Context, ref string) error {
	if _, _, err := r.cli.ImageInspectWithRaw(ctx, ref); err == nil {
		return nil
	} else if !errdefs.IsNotFound(err) {
		return translate(err)
	}
	r.logger.Info("pulling worker image", "image", ref)
	rc, err := r.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return translate(err)
	}
	defer rc.Close()
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return fmt.Errorf("pulling image %s: %w", ref, err)
	}
	return nil
}
