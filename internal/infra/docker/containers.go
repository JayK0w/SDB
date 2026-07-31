package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/infra/streamio"
)

// plafond des sorties de hook (stockées dans error_log)
const maxHookOutput = 64 << 10

func (r *Runtime) List(ctx context.Context, all bool) ([]domain.Container, error) {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{All: all})
	if err != nil {
		return nil, translate(err)
	}
	out := make([]domain.Container, 0, len(list))
	for _, c := range list {
		if c.Labels[workerLabel] != "" {
			continue // workers SDB exclus
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
	}
	return c, nil
}

// RemoveVolume : suppression d'un volume jetable de vérification.
//
// Le refus hors préfixe est délibérément implémenté ICI, au plus près de
// l'appel destructeur, et non seulement chez l'appelant : une régression
// dans le usecase ne doit pas pouvoir se traduire par la destruction d'un
// volume de production.
func (r *Runtime) RemoveVolume(ctx context.Context, name string) error {
	if !domain.IsScratchVolume(name) {
		return fmt.Errorf("%w: refusing to remove volume %q, only %s* volumes are disposable",
			domain.ErrForbidden, name, domain.VerifyVolumePrefix)
	}
	if err := r.cli.VolumeRemove(ctx, name, false); err != nil {
		if errdefs.IsNotFound(err) {
			return nil // déjà parti : le nettoyage a atteint son but
		}
		return translate(err)
	}
	return nil
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

// Exec : hook dans un conteneur en marche. Attention : Docker n'a pas de
// timeout d'exec côté serveur — au timeout on coupe le flux mais le
// processus peut continuer ; la politique d'échec du hook tranche.
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

	stdout := streamio.NewBounded(maxHookOutput)
	stderr := streamio.NewBounded(maxHookOutput)
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
