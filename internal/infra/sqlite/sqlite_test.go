package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
	"github.com/standalone-docker-backup/sdb/internal/infra/crypto"
	"github.com/standalone-docker-backup/sdb/internal/infra/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}
	return db
}

func testCipher(t *testing.T) domain.Cipher {
	t.Helper()
	c, err := crypto.NewAESGCM("test-master-key-of-32-characters!")
	if err != nil {
		t.Fatalf("NewAESGCM() error: %v", err)
	}
	return c
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatalf("second Migrate() error: %v", err)
	}
}

func TestUserRepoCRUD(t *testing.T) {
	ctx := context.Background()
	repo := sqlite.NewUserRepo(openTestDB(t))

	u := &domain.User{Username: "alice", PasswordHash: "$argon2id$...", Role: domain.RoleAdmin}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("Create() did not set the ID")
	}

	dup := &domain.User{Username: "alice", PasswordHash: "x", Role: domain.RoleUser}
	if err := repo.Create(ctx, dup); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate username: err = %v, want ErrAlreadyExists", err)
	}

	got, err := repo.GetByUsername(ctx, "alice")
	if err != nil || got.Role != domain.RoleAdmin {
		t.Fatalf("GetByUsername() = %+v, %v", got, err)
	}

	got.Role = domain.RoleUser
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	again, _ := repo.GetByID(ctx, got.ID)
	if again.Role != domain.RoleUser {
		t.Fatalf("Update() not persisted, role = %s", again.Role)
	}

	n, err := repo.Count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Count() = %d, %v; want 1", n, err)
	}

	if err := repo.Delete(ctx, got.ID); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, err := repo.GetByID(ctx, got.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID(deleted): err = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, got.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete(deleted): err = %v, want ErrNotFound", err)
	}
}

func TestStorageRepoEncryptsSecretsAtRest(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := sqlite.NewStorageRepo(db, testCipher(t))

	cfg := &domain.StorageConfig{
		Name:           "offsite",
		Type:           domain.StorageS3,
		Endpoint:       "s3.example.com/backups",
		ResticPassword: "repo-password-plaintext",
		Credentials: map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIAEXAMPLE",
			"AWS_SECRET_ACCESS_KEY": "topsecretvalue",
		},
	}
	if err := repo.Create(ctx, cfg); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	got, err := repo.GetByID(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.ResticPassword != cfg.ResticPassword {
		t.Fatal("restic password did not round-trip")
	}
	if got.Credentials["AWS_SECRET_ACCESS_KEY"] != "topsecretvalue" {
		t.Fatal("credentials did not round-trip")
	}

	// les blobs bruts ne doivent contenir aucun secret en clair
	var creds, password []byte
	err = db.QueryRowContext(ctx,
		`SELECT credentials_enc, restic_password_enc FROM storage_configs WHERE id = ?`, cfg.ID).
		Scan(&creds, &password)
	if err != nil {
		t.Fatalf("raw select error: %v", err)
	}
	for _, secret := range []string{"topsecretvalue", "repo-password-plaintext"} {
		if bytes.Contains(creds, []byte(secret)) || bytes.Contains(password, []byte(secret)) {
			t.Fatalf("plaintext %q found in database blobs", secret)
		}
	}
}

func TestStorageRepoDeleteReferencedIsConflict(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	storages := sqlite.NewStorageRepo(db, testCipher(t))
	history := sqlite.NewHistoryRepo(db)

	cfg := &domain.StorageConfig{Name: "local", Type: domain.StorageLocal, Endpoint: "/backups", ResticPassword: "pw"}
	if err := storages.Create(ctx, cfg); err != nil {
		t.Fatalf("Create(storage) error: %v", err)
	}
	rec := &domain.BackupRecord{ContainerID: "c1", StorageID: cfg.ID, Status: domain.BackupSuccess}
	if err := history.Create(ctx, rec); err != nil {
		t.Fatalf("Create(record) error: %v", err)
	}

	if err := storages.Delete(ctx, cfg.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Delete(referenced storage): err = %v, want ErrConflict", err)
	}
}

// Le rattachement d'une copie secondaire doit survivre au round-trip, et la
// source ne doit pas pouvoir disparaître sous elle : une copie orpheline
// laisserait croire à une réplication qui n'a plus lieu.
func TestStorageRepoCopyOfRoundTripsAndProtectsItsSource(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	storages := sqlite.NewStorageRepo(db, testCipher(t))

	primary := &domain.StorageConfig{Name: "primary", Type: domain.StorageLocal, Endpoint: "/srv/primary", ResticPassword: "pw1"}
	if err := storages.Create(ctx, primary); err != nil {
		t.Fatalf("Create(primary) error: %v", err)
	}
	copyCfg := &domain.StorageConfig{
		Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", ResticPassword: "pw2",
		CopyOf: primary.ID,
	}
	if err := storages.Create(ctx, copyCfg); err != nil {
		t.Fatalf("Create(copy) error: %v", err)
	}

	got, err := storages.GetByID(ctx, copyCfg.ID)
	if err != nil || got.CopyOf != primary.ID {
		t.Fatalf("GetByID() = %+v, %v; want CopyOf = %d", got, err, primary.ID)
	}
	// un dépôt principal reste à zéro, pas à NULL scanné n'importe comment
	if p, err := storages.GetByID(ctx, primary.ID); err != nil || p.CopyOf != 0 {
		t.Fatalf("primary CopyOf = %d, want 0 (%v)", p.CopyOf, err)
	}

	if err := storages.Delete(ctx, primary.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Delete(source of a copy): err = %v, want ErrConflict", err)
	}
	if err := storages.Delete(ctx, copyCfg.ID); err != nil {
		t.Fatalf("Delete(copy) error: %v", err)
	}
	if err := storages.Delete(ctx, primary.ID); err != nil {
		t.Fatalf("Delete(source) once the copy is gone: %v", err)
	}
}

func TestStorageRepoRejectsCopyOfMissingStorage(t *testing.T) {
	ctx := context.Background()
	storages := sqlite.NewStorageRepo(openTestDB(t), testCipher(t))

	cfg := &domain.StorageConfig{
		Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", ResticPassword: "pw",
		CopyOf: 404,
	}
	if err := storages.Create(ctx, cfg); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Create(copy of a missing storage): err = %v, want ErrNotFound", err)
	}
}

func TestHistoryRepoListAndFailInterrupted(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	storages := sqlite.NewStorageRepo(db, testCipher(t))
	repo := sqlite.NewHistoryRepo(db)

	cfg := &domain.StorageConfig{Name: "local", Type: domain.StorageLocal, Endpoint: "/backups", ResticPassword: "pw"}
	if err := storages.Create(ctx, cfg); err != nil {
		t.Fatalf("Create(storage) error: %v", err)
	}

	mk := func(container string, status domain.BackupStatus, start time.Time) *domain.BackupRecord {
		rec := &domain.BackupRecord{ContainerID: container, StorageID: cfg.ID, Status: status, StartTime: start}
		if err := repo.Create(ctx, rec); err != nil {
			t.Fatalf("Create(record) error: %v", err)
		}
		return rec
	}
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	mk("web", domain.BackupSuccess, base)
	running := mk("db", domain.BackupRunning, base.Add(time.Hour))
	mk("db", domain.BackupFailed, base.Add(2*time.Hour))

	all, err := repo.List(ctx, domain.HistoryFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("List(all) = %d records, %v; want 3", len(all), err)
	}
	if all[0].ContainerID != "db" || all[0].Status != domain.BackupFailed {
		t.Fatalf("List() not ordered by start_time DESC: first = %+v", all[0])
	}

	onlyDB, err := repo.List(ctx, domain.HistoryFilter{ContainerID: "db"})
	if err != nil || len(onlyDB) != 2 {
		t.Fatalf("List(container=db) = %d records, %v; want 2", len(onlyDB), err)
	}
	onlyRunning, err := repo.List(ctx, domain.HistoryFilter{Status: domain.BackupRunning})
	if err != nil || len(onlyRunning) != 1 {
		t.Fatalf("List(status=running) = %d records, %v; want 1", len(onlyRunning), err)
	}

	n, err := repo.FailInterrupted(ctx, "interrupted by restart")
	if err != nil || n != 1 {
		t.Fatalf("FailInterrupted() = %d, %v; want 1", n, err)
	}
	got, _ := repo.GetByID(ctx, running.ID)
	if got.Status != domain.BackupFailed || got.EndTime == nil || got.ErrorLog == "" {
		t.Fatalf("FailInterrupted() left record inconsistent: %+v", got)
	}
}
