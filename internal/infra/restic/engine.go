package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

type Engine struct {
	runtime domain.ContainerRuntime
	image   string
}

var _ domain.SnapshotEngine = (*Engine)(nil)

// New builds the engine. workerImage must have restic as its entrypoint
// (the official restic/restic image does).
func New(runtime domain.ContainerRuntime, workerImage string) *Engine {
	return &Engine{runtime: runtime, image: workerImage}
}

// run executes one restic command in a fresh ephemeral worker and returns
// its exit code. Infrastructure failures (pull, create, attach) come back
// as the error; restic-level failures come back as a non-zero exit code.
func (e *Engine) run(ctx context.Context, storage *domain.StorageConfig, cmd []string,
	extraMounts []domain.Mount, labels map[string]string, stdout, stderr io.Writer) (int, error) {

	repo, err := repositoryFor(storage)
	if err != nil {
		return -1, err
	}
	spec := domain.WorkerSpec{
		Image: e.image,
		// Backend-specific flags (e.g. the sftp ssh command) ride along;
		// restic parses flags anywhere on the command line.
		Cmd:         append(append([]string{}, cmd...), repo.opts...),
		Env:         repo.env,
		Files:       repo.files,
		Mounts:      append(repo.mounts, extraMounts...),
		Labels:      labels,
		NetworkMode: repo.network,
	}
	return e.runtime.RunWorker(ctx, spec, stdout, stderr)
}

func (e *Engine) EnsureRepository(ctx context.Context, storage *domain.StorageConfig) error {
	// `cat config` is the cheapest authenticated way to probe a repo.
	exit, err := e.run(ctx, storage, []string{"cat", "config"}, nil, nil, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if exit == 0 {
		return nil
	}
	stderr := newBoundedBuffer(16 << 10)
	exit, err = e.run(ctx, storage, []string{"init"}, nil, nil, io.Discard, stderr)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("restic init failed (exit %d): %s", exit, stderr)
	}
	return nil
}

func (e *Engine) Snapshots(ctx context.Context, storage *domain.StorageConfig, tags []string) ([]domain.Snapshot, error) {
	cmd := []string{"snapshots", "--json"}
	for _, t := range tags {
		cmd = append(cmd, "--tag", t)
	}
	var stdout bytes.Buffer
	stderr := newBoundedBuffer(16 << 10)
	exit, err := e.run(ctx, storage, cmd, nil, nil, &stdout, stderr)
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		return nil, fmt.Errorf("restic snapshots failed (exit %d): %s", exit, stderr)
	}

	var raw []struct {
		ID       string    `json:"id"`
		ShortID  string    `json:"short_id"`
		Time     time.Time `json:"time"`
		Hostname string    `json:"hostname"`
		Paths    []string  `json:"paths"`
		Tags     []string  `json:"tags"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("parsing restic snapshots output: %w", err)
	}
	out := make([]domain.Snapshot, 0, len(raw))
	for _, s := range raw {
		out = append(out, domain.Snapshot{
			ID:       s.ID,
			ShortID:  s.ShortID,
			Time:     s.Time,
			Hostname: s.Hostname,
			Paths:    s.Paths,
			Tags:     s.Tags,
		})
	}
	return out, nil
}

func (e *Engine) Forget(ctx context.Context, storage *domain.StorageConfig, policy domain.RetentionPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	cmd := []string{"forget"}
	add := func(flag string, v int) {
		if v > 0 {
			cmd = append(cmd, flag, strconv.Itoa(v))
		}
	}
	add("--keep-last", policy.KeepLast)
	add("--keep-hourly", policy.KeepHourly)
	add("--keep-daily", policy.KeepDaily)
	add("--keep-weekly", policy.KeepWeekly)
	add("--keep-monthly", policy.KeepMonthly)
	add("--keep-yearly", policy.KeepYearly)
	if policy.Prune {
		cmd = append(cmd, "--prune")
	}

	stderr := newBoundedBuffer(16 << 10)
	exit, err := e.run(ctx, storage, cmd, nil, nil, io.Discard, stderr)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("restic forget failed (exit %d): %s", exit, stderr)
	}
	return nil
}

func (e *Engine) Check(ctx context.Context, storage *domain.StorageConfig) error {
	stderr := newBoundedBuffer(32 << 10)
	exit, err := e.run(ctx, storage, []string{"check"}, nil, nil, io.Discard, stderr)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("repository integrity check failed (exit %d): %s", exit, stderr)
	}
	return nil
}
