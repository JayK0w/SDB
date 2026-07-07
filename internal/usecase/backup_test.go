package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newBackupFixture(t *testing.T) (*BackupService, *fakeRuntime, *fakeEngine, *memHistory, *capturePublisher) {
	t.Helper()
	runtime := &fakeRuntime{container: &domain.Container{
		ID:    "c1",
		Name:  "postgres",
		State: domain.ContainerRunning,
		Mounts: []domain.Mount{
			{Type: domain.MountVolume, Name: "pgdata", Source: "pgdata", Destination: "/var/lib/postgresql/data"},
		},
	}}
	storages := newMemStorages()
	if err := storages.Create(context.Background(), &domain.StorageConfig{
		Name: "local", Type: domain.StorageLocal, Endpoint: "/backups", ResticPassword: "pw",
	}); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{summary: &domain.BackupSummary{SnapshotID: "snap1", BytesProcessed: 12345}}
	history := newMemHistory()
	pub := &capturePublisher{}
	svc := NewBackupService(runtime, engine, storages, history, pub, discardLogger())
	return svc, runtime, engine, history, pub
}

func waitTerminal(t *testing.T, history *memHistory, id int64) domain.BackupRecord {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := history.GetByID(context.Background(), id)
		if err == nil && rec.Status.Terminal() {
			return *rec
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("backup %d did not reach a terminal status", id)
	return domain.BackupRecord{}
}

func TestBackupColdSuccessWithRetention(t *testing.T) {
	svc, runtime, engine, history, pub := newBackupFixture(t)

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID:   "c1",
		StorageID:     1,
		StopContainer: true,
		Retention:     &domain.RetentionPolicy{KeepLast: 3, Prune: true},
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupSuccess {
		t.Fatalf("Status = %s (%s), want success", final.Status, final.ErrorLog)
	}
	if final.SnapshotID != "snap1" || final.BytesProcessed != 12345 || final.EndTime == nil {
		t.Fatalf("record not filled from summary: %+v", final)
	}
	if got := runtime.stopped(); len(got) != 1 || got[0] != "c1" {
		t.Fatalf("stops = %v, want [c1]", got)
	}
	if got := runtime.started(); len(got) != 1 || got[0] != "c1" {
		t.Fatalf("starts = %v, want [c1] (container must be restarted)", got)
	}
	if got := engine.forgotten(); len(got) != 1 || got[0].KeepLast != 3 || !got[0].Prune {
		t.Fatalf("retention not applied: %v", got)
	}

	var sawTerminal bool
	for _, ev := range pub.all() {
		if ev.Type == domain.EventStatus && ev.Status == domain.BackupSuccess {
			sawTerminal = true
		}
	}
	if !sawTerminal {
		t.Fatal("no terminal status event published")
	}
}

func TestBackupEngineFailureStillRestartsContainer(t *testing.T) {
	svc, runtime, engine, history, _ := newBackupFixture(t)
	engine.backupErr = errors.New("network down")

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID: "c1", StorageID: 1, StopContainer: true,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupFailed {
		t.Fatalf("Status = %s, want failed", final.Status)
	}
	if !strings.Contains(final.ErrorLog, "network down") {
		t.Fatalf("ErrorLog = %q, want the engine error", final.ErrorLog)
	}
	if got := runtime.started(); len(got) != 1 {
		t.Fatalf("starts = %v: rollback must restart the container even on failure", got)
	}
}

func TestBackupPreHookAbortSkipsSnapshotAndStop(t *testing.T) {
	svc, runtime, engine, history, _ := newBackupFixture(t)
	runtime.execFn = func([]string) (*domain.ExecResult, error) {
		return &domain.ExecResult{ExitCode: 1, Stderr: "pg_dumpall: connection refused"}, nil
	}

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID:   "c1",
		StorageID:     1,
		StopContainer: true,
		PreHook:       &domain.Hook{Command: []string{"pg_dumpall"}},
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupFailed {
		t.Fatalf("Status = %s, want failed (abort policy)", final.Status)
	}
	if !strings.Contains(final.ErrorLog, "connection refused") {
		t.Fatalf("ErrorLog = %q, want hook stderr", final.ErrorLog)
	}
	if engine.backups() != 0 {
		t.Fatal("snapshot must not run after an aborting pre-hook failure")
	}
	if len(runtime.stopped()) != 0 {
		t.Fatal("container must not be stopped after an aborting pre-hook failure")
	}
}

func TestBackupPreHookContinueYieldsWarning(t *testing.T) {
	svc, runtime, engine, history, _ := newBackupFixture(t)
	runtime.execFn = func([]string) (*domain.ExecResult, error) {
		return &domain.ExecResult{ExitCode: 1, Stderr: "boom"}, nil
	}

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID: "c1",
		StorageID:   1,
		PreHook:     &domain.Hook{Command: []string{"true"}, OnFailure: domain.HookContinue},
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupWarning {
		t.Fatalf("Status = %s, want warning", final.Status)
	}
	if engine.backups() != 1 {
		t.Fatal("snapshot must still run with a continue pre-hook policy")
	}
}

func TestBackupPartialResultIsWarning(t *testing.T) {
	svc, _, engine, history, _ := newBackupFixture(t)
	engine.backupErr = fmt.Errorf("%w: some files unreadable", domain.ErrPartial)

	rec, err := svc.Start(context.Background(), domain.BackupRequest{ContainerID: "c1", StorageID: 1})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupWarning {
		t.Fatalf("Status = %s, want warning for a partial backup", final.Status)
	}
	if final.SnapshotID != "snap1" {
		t.Fatal("partial backups still produce a usable snapshot id")
	}
}

func TestBackupRestartFailureIsCritical(t *testing.T) {
	svc, runtime, _, history, _ := newBackupFixture(t)
	runtime.startErr = errors.New("no such container")

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID: "c1", StorageID: 1, StopContainer: true,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupFailed {
		t.Fatalf("Status = %s, want failed when the restart fails", final.Status)
	}
	if !strings.Contains(final.ErrorLog, "could not be restarted") {
		t.Fatalf("ErrorLog = %q, want the critical restart error", final.ErrorLog)
	}
}

func TestBackupConcurrentRunsOnSameContainerConflict(t *testing.T) {
	svc, _, engine, history, _ := newBackupFixture(t)
	release := make(chan struct{})
	engine.backupFn = func(context.Context) error {
		<-release
		return nil
	}

	first, err := svc.Start(context.Background(), domain.BackupRequest{ContainerID: "c1", StorageID: 1})
	if err != nil {
		t.Fatalf("first Start() error: %v", err)
	}
	_, err = svc.Start(context.Background(), domain.BackupRequest{ContainerID: "c1", StorageID: 1})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second Start() err = %v, want ErrConflict", err)
	}
	close(release)
	waitTerminal(t, history, first.ID)
}

func TestBackupCancel(t *testing.T) {
	svc, _, engine, history, _ := newBackupFixture(t)
	engine.backupFn = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	rec, err := svc.Start(context.Background(), domain.BackupRequest{ContainerID: "c1", StorageID: 1})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if err := svc.Cancel(rec.ID); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupCanceled {
		t.Fatalf("Status = %s, want canceled", final.Status)
	}
	if err := svc.Cancel(999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Cancel(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestSelectMounts(t *testing.T) {
	c := &domain.Container{Name: "web", Mounts: []domain.Mount{
		{Type: domain.MountVolume, Name: "data"},
		{Type: domain.MountVolume, Name: "cache"},
		{Type: "tmpfs", Destination: "/tmp"},
	}}
	all, err := selectMounts(c, nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("selectMounts(nil) = %d mounts, %v; want 2", len(all), err)
	}
	one, err := selectMounts(c, []string{"cache"})
	if err != nil || len(one) != 1 || one[0].Name != "cache" {
		t.Fatalf("selectMounts(cache) = %v, %v", one, err)
	}
	if _, err := selectMounts(c, []string{"missing"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("selectMounts(missing) err = %v, want ErrInvalidInput", err)
	}
	empty := &domain.Container{Name: "bare"}
	if _, err := selectMounts(empty, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("selectMounts(no mounts) err = %v, want ErrInvalidInput", err)
	}
}
