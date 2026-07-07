package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// maxHookOutput caps captured hook stdout/stderr; enough for a dump log,
// small enough to store in backups_history.error_log.
const maxHookOutput = 64 << 10

func (r *Runtime) List(ctx context.Context, all bool) ([]domain.Container, error) {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{All: all})
	if err != nil {
		return nil, translate(err)
	}
	out := make([]domain.Container, 0, len(list))
	for _, c := range list {
		if c.Labels[workerLabel] != "" {
			continue // SDB's own ephemeral workers
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		mounts := make([]domain.Mount, 0, len(c.Mounts))
		for _, m := range c.Mounts {
			mounts = append(mounts, domain.Mount{
				Type:        domain.MountType(m.Type),
				Name:        m.Name,
				Source:      m.Source,
				Destination: m.Destination,
				ReadOnly:    !m.RW,
			})
		}
		out = append(out, domain.Container{
			ID:      c.ID,
			Name:    name,
			Image:   c.Image,
			State:   domain.ContainerState(c.State),
			Labels:  c.Labels,
			Mounts:  mounts,
			Created: time.Unix(c.Created, 0).UTC(),
		})
	}
	return out, nil
}

func (r *Runtime) Get(ctx context.Context, id string) (*domain.Container, error) {
	info, err := r.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, translate(err)
	}
	created, _ := time.Parse(time.RFC3339Nano, info.Created)
	mounts := make([]domain.Mount, 0, len(info.Mounts))
	for _, m := range info.Mounts {
		mounts = append(mounts, domain.Mount{
			Type:        domain.MountType(m.Type),
			Name:        m.Name,
			Source:      m.Source,
			Destination: m.Destination,
			ReadOnly:    !m.RW,
		})
	}
	c := &domain.Container{
		ID:      info.ID,
		Name:    strings.TrimPrefix(info.Name, "/"),
		Mounts:  mounts,
		Created: created.UTC(),
	}
	if info.State != nil {
		c.State = domain.ContainerState(info.State.Status)
	}
	if info.Config != nil {
		c.Image = info.Config.Image
		c.Labels = info.Config.Labels
	}
	return c, nil
}

func (r *Runtime) Stop(ctx context.Context, id string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = r.stopTimeout
	}
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	if err := r.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &secs}); err != nil {
		return translate(err)
	}
	return nil
}

func (r *Runtime) Start(ctx context.Context, id string) error {
	if err := r.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return translate(err)
	}
	return nil
}

// Exec runs a hook command inside a running container. Note that Docker
// has no server-side exec timeout: when the deadline fires, the attach
// stream is closed and an error returned, but the process may keep
// running inside the container — the hook failure policy decides how the
// backup reacts.
func (r *Runtime) Exec(ctx context.Context, id string, cmd []string, timeout time.Duration) (*domain.ExecResult, error) {
	if timeout <= 0 {
		timeout = domain.DefaultHookTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	created, err := r.cli.ContainerExecCreate(execCtx, id, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, translate(err)
	}

	attach, err := r.cli.ContainerExecAttach(execCtx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, translate(err)
	}
	defer attach.Close()

	stdout := newBoundedBuffer(maxHookOutput)
	stderr := newBoundedBuffer(maxHookOutput)
	done := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(stdout, stderr, attach.Reader)
		done <- err
	}()

	select {
	case <-execCtx.Done():
		return nil, fmt.Errorf("hook did not finish within %s: %w", timeout, execCtx.Err())
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("reading hook output: %w", err)
		}
	}

	inspect, err := r.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return nil, translate(err)
	}
	return &domain.ExecResult{
		ExitCode: inspect.ExitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}
