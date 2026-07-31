package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func escrowFixture() (*StorageService, *memStorages) {
	storages := newMemStorages()
	return NewStorageService(storages, &fakeEngine{}, discardLogger()), storages
}

// Sans mot de passe fourni, SDB en génère un : le comportement par défaut ne
// doit pas régresser.
func TestCreateGeneratesPasswordWhenNoneSupplied(t *testing.T) {
	svc, storages := escrowFixture()
	cfg := &domain.StorageConfig{
		Name: "auto", Type: domain.StorageLocal, Endpoint: "/b",
	}
	if err := svc.Create(context.Background(), cfg); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if len(cfg.ResticPassword) < 40 {
		t.Fatalf("generated password looks too short: %d chars", len(cfg.ResticPassword))
	}
	stored, _ := storages.GetByID(context.Background(), cfg.ID)
	if stored.ResticPassword != cfg.ResticPassword {
		t.Fatal("the generated password must be the one persisted")
	}
}

// Fournir son propre mot de passe est ce qui rend la perte de sdb.db
// survivable : il peut être séquestré hors de SDB.
func TestCreateHonoursSuppliedPassword(t *testing.T) {
	svc, storages := escrowFixture()
	const supplied = "correct-horse-battery-staple-42"

	cfg := &domain.StorageConfig{
		Name: "escrowed", Type: domain.StorageLocal, Endpoint: "/b",
		ResticPassword: supplied,
	}
	if err := svc.Create(context.Background(), cfg); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	stored, _ := storages.GetByID(context.Background(), cfg.ID)
	if stored.ResticPassword != supplied {
		t.Fatalf("stored password = %q, want the supplied one", stored.ResticPassword)
	}
}

// Un mot de passe manuel trop court affaiblirait un dépôt volé face à une
// attaque hors ligne.
func TestCreateRejectsWeakSuppliedPassword(t *testing.T) {
	svc, _ := escrowFixture()
	cfg := &domain.StorageConfig{
		Name: "weak", Type: domain.StorageLocal, Endpoint: "/b",
		ResticPassword: "short",
	}
	err := svc.Create(context.Background(), cfg)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Create() err = %v, want ErrInvalidInput", err)
	}
}

// Le mot de passe reste immuable : le changer côté SDB rendrait le dépôt
// existant inaccessible.
func TestPasswordStaysImmutableAfterCreation(t *testing.T) {
	svc, _ := escrowFixture()
	ctx := context.Background()
	cfg := &domain.StorageConfig{
		Name: "fixed", Type: domain.StorageLocal, Endpoint: "/b",
		ResticPassword: "correct-horse-battery-staple-42",
	}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	err := svc.Update(ctx, &domain.StorageConfig{
		ID: cfg.ID, Name: "fixed", Type: domain.StorageLocal, Endpoint: "/b",
		ResticPassword: "a-completely-different-password-99",
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Update() err = %v, want ErrInvalidInput", err)
	}
}

// Redacted() est ce qui empêche le mot de passe de fuir par les routes de
// lecture : seule la réponse de création le porte.
func TestRedactedNeverCarriesThePassword(t *testing.T) {
	cfg := domain.StorageConfig{
		Name: "x", ResticPassword: "super-secret-repository-password",
		Credentials: map[string]string{"AWS_SECRET_ACCESS_KEY": "shhh"},
	}
	red := cfg.Redacted()

	if red.ResticPassword != "" {
		t.Fatal("Redacted() must strip the repository password")
	}
	for k, v := range red.Credentials {
		if v != "" {
			t.Fatalf("Redacted() leaked credential %s", k)
		}
	}
	if _, ok := red.Credentials["AWS_SECRET_ACCESS_KEY"]; !ok {
		t.Fatal("Redacted() should keep the credential KEYS visible")
	}
	if strings.Contains(red.Name, "secret") {
		t.Fatal("sanity")
	}
}
