// Package docker implements domain.ContainerRuntime over the official
// Docker SDK. The client honours DOCKER_HOST and friends when no explicit
// host is configured; tcp:// endpoints require mTLS material (enforced
// upstream by the configuration layer).
package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// workerLabel marks containers spawned by SDB so they are excluded from
// listings and identifiable for cleanup.
const workerLabel = "sdb.worker"

type Options struct {
	Host        string // empty = SDK defaults (DOCKER_HOST or local socket)
	TLSCACert   string
	TLSCert     string
	TLSKey      string
	StopTimeout time.Duration // default grace period for container stops
}

type Runtime struct {
	cli         *client.Client
	stopTimeout time.Duration
	logger      *slog.Logger
}

var _ domain.ContainerRuntime = (*Runtime)(nil)

func New(opts Options, logger *slog.Logger) (*Runtime, error) {
	clientOpts := []client.Opt{client.WithAPIVersionNegotiation()}
	if opts.Host != "" {
		clientOpts = append(clientOpts, client.WithHost(opts.Host))
	} else {
		clientOpts = append(clientOpts, client.FromEnv)
	}
	if opts.TLSCACert != "" || opts.TLSCert != "" || opts.TLSKey != "" {
		clientOpts = append(clientOpts, client.WithTLSClientConfig(opts.TLSCACert, opts.TLSCert, opts.TLSKey))
	}
	cli, err := client.NewClientWithOpts(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	if opts.StopTimeout <= 0 {
		opts.StopTimeout = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{cli: cli, stopTimeout: opts.StopTimeout, logger: logger}, nil
}

func (r *Runtime) Ping(ctx context.Context) error {
	if _, err := r.cli.Ping(ctx); err != nil {
		return translate(err)
	}
	return nil
}

func (r *Runtime) Close() error { return r.cli.Close() }

// translate maps Docker SDK failures onto the domain sentinels so the
// usecase layer never imports SDK types.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errdefs.IsNotFound(err):
		return fmt.Errorf("%w: %v", domain.ErrNotFound, err)
	case errdefs.IsConflict(err):
		return fmt.Errorf("%w: %v", domain.ErrConflict, err)
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %v", domain.ErrCanceled, err)
	case client.IsErrConnectionFailed(err):
		return fmt.Errorf("%w: docker daemon unreachable: %v", domain.ErrUnavailable, err)
	default:
		return err
	}
}
