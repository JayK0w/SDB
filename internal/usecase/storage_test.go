package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func newStorageFixture() (*StorageService, *memStorages, *fakeEngine) {
	storages := newMemStorages()
	engine := &fakeEngine{}
	return NewStorageService(storages, engine, discardLogger()), storages, engine
}

func TestStorageCreateGeneratesPasswordAndInitialisesRepo(t *testing.T) {
	ctx := context.Background()
	svc, storages, engine := newStorageFixture()

	cfg := &domain.StorageConfig{Name: "local", Type: domain.StorageLocal, Endpoint: "/backups"}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if len(cfg.ResticPassword) < 32 {
		t.Fatalf("generated restic password too short: %d chars", len(cfg.ResticPassword))
	}
	if engine.ensureCalls != 1 {
		t.Fatalf("EnsureRepository calls = %d, want 1", engine.ensureCalls)
	}
	stored, err := storages.GetByID(ctx, cfg.ID)
	if err != nil || stored.ResticPassword != cfg.ResticPassword {
		t.Fatalf("stored config mismatch: %+v, %v", stored, err)
	}
}

func TestStorageCreateRollsBackWhenRepoInitFails(t *testing.T) {
	ctx := context.Background()
	svc, storages, engine := newStorageFixture()
	engine.ensureErr = errors.New("bucket unreachable")

	cfg := &domain.StorageConfig{Name: "s3", Type: domain.StorageS3, Endpoint: "s3.example.com/b"}
	err := svc.Create(ctx, cfg)
	if !errors.Is(err, engine.ensureErr) {
		t.Fatalf("Create() must surface the repository error, got %v", err)
	}
	if _, err := storages.GetByID(ctx, cfg.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("config must be rolled back, GetByID err = %v", err)
	}
}

func TestStorageUpdatePasswordImmutable(t *testing.T) {
	ctx := context.Background()
	svc, storages, _ := newStorageFixture()

	cfg := &domain.StorageConfig{Name: "local", Type: domain.StorageLocal, Endpoint: "/backups"}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	original := cfg.ResticPassword

	// Empty password on update keeps the existing one.
	upd := &domain.StorageConfig{ID: cfg.ID, Name: "local-renamed", Type: domain.StorageLocal, Endpoint: "/backups"}
	if err := svc.Update(ctx, upd); err != nil {
		t.Fatalf("Update(keep password) error: %v", err)
	}
	stored, _ := storages.GetByID(ctx, cfg.ID)
	if stored.ResticPassword != original || stored.Name != "local-renamed" {
		t.Fatalf("update mishandled: %+v", stored)
	}

	// A different password is refused.
	bad := &domain.StorageConfig{ID: cfg.ID, Name: "local", Type: domain.StorageLocal, Endpoint: "/backups", ResticPassword: "new-password"}
	if err := svc.Update(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Update(change password) err = %v, want ErrInvalidInput", err)
	}
}

func TestMaintenanceRunChecksContinuesOnFailure(t *testing.T) {
	ctx := context.Background()
	storages := newMemStorages()
	for _, name := range []string{"a", "b"} {
		if err := storages.Create(ctx, &domain.StorageConfig{
			Name: name, Type: domain.StorageLocal, Endpoint: "/b", ResticPassword: "pw",
		}); err != nil {
			t.Fatal(err)
		}
	}
	engine := &fakeEngine{checkErr: errors.New("pack corrupted")}
	svc := NewMaintenanceService(storages, engine, discardLogger())

	err := svc.RunChecks(ctx)
	if err == nil {
		t.Fatal("RunChecks() must report failures")
	}
	if engine.checkCalls != 2 {
		t.Fatalf("check calls = %d, want 2 (one failure must not stop the sweep)", engine.checkCalls)
	}
}
