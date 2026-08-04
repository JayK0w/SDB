package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// captureSink : jauges de fraîcheur réamorcées, telles que les tests les
// relisent.
type captureSink struct {
	backups       map[string]time.Time
	verifications map[string]time.Time
}

func newCaptureSink() *captureSink {
	return &captureSink{backups: map[string]time.Time{}, verifications: map[string]time.Time{}}
}

func (s *captureSink) SeedLastBackupSuccess(container string, at time.Time) {
	s.backups[container] = at
}

func (s *captureSink) SeedLastVerificationSuccess(storage string, at time.Time) {
	s.verifications[storage] = at
}

// seedFixture : un dépôt, un historique de sauvegardes et de restaurations.
func seedFixture(t *testing.T) (*memHistory, *memRestores, *memStorages) {
	t.Helper()
	storages := newMemStorages()
	if err := storages.Create(context.Background(), &domain.StorageConfig{
		Name: "backup-local", Type: domain.StorageLocal, Endpoint: "/srv/b", ResticPassword: "pw",
	}); err != nil {
		t.Fatal(err)
	}
	return newMemHistory(), newMemRestores(), storages
}

// LE trou : les jauges de fraîcheur vivent en mémoire du processus. Après un
// redémarrage, « dernière sauvegarde réussie » et « dernière preuve de
// restaurabilité » disparaissent, et une alerte bâtie sur `absent(...)` se
// déclenche alors qu'il ne s'est rien passé. La base, elle, sait depuis quand.
func TestSeedFreshnessRestoresGaugesFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	history, restores, storages := seedFixture(t)
	now := time.Now().UTC()

	older := now.Add(-48 * time.Hour)
	recent := now.Add(-2 * time.Hour)
	for _, rec := range []*domain.BackupRecord{
		{ContainerName: "demo-web", StorageID: 1, Status: domain.BackupSuccess, StartTime: older},
		{ContainerName: "demo-web", StorageID: 1, Status: domain.BackupSuccess, StartTime: recent},
		{ContainerName: "pg", StorageID: 1, Status: domain.BackupFailed, StartTime: now},
	} {
		if err := history.Create(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := restores.Create(ctx, &domain.RestoreRecord{
		StorageID: 1, Status: domain.BackupSuccess, StartTime: recent,
		TriggeredBy: domain.Actor{Name: domain.VerificationActor},
	}); err != nil {
		t.Fatal(err)
	}

	sink := newCaptureSink()
	n, err := SeedFreshness(ctx, history, restores, storages, sink)
	if err != nil {
		t.Fatalf("SeedFreshness() error: %v", err)
	}
	if n != 2 {
		t.Fatalf("%d séries réamorcées, want 2", n)
	}
	if got := sink.backups["demo-web"]; !got.Equal(recent) {
		t.Fatalf("dernière sauvegarde de demo-web = %s, want la PLUS RÉCENTE (%s)", got, recent)
	}
	if _, ok := sink.backups["pg"]; ok {
		t.Fatal("un conteneur dont toutes les sauvegardes ont échoué ne doit pas passer pour sauvegardé")
	}
	// la jauge est étiquetée par NOM de dépôt, l'historique porte son id
	if got := sink.verifications["backup-local"]; !got.Equal(recent) {
		t.Fatalf("dernière vérification = %s, want %s", got, recent)
	}
}

// Un avertissement a bien produit un snapshot exploitable — l'exclure ferait
// passer un conteneur pour non sauvegardé alors qu'il l'est.
func TestSeedFreshnessCountsWarningsAsBackedUp(t *testing.T) {
	ctx := context.Background()
	history, restores, storages := seedFixture(t)
	at := time.Now().UTC().Add(-time.Hour)

	if err := history.Create(ctx, &domain.BackupRecord{
		ContainerName: "demo-web", StorageID: 1, Status: domain.BackupWarning, StartTime: at,
	}); err != nil {
		t.Fatal(err)
	}

	sink := newCaptureSink()
	if _, err := SeedFreshness(ctx, history, restores, storages, sink); err != nil {
		t.Fatalf("SeedFreshness() error: %v", err)
	}
	if got := sink.backups["demo-web"]; !got.Equal(at) {
		t.Fatalf("une sauvegarde en avertissement doit compter : got %s, want %s", got, at)
	}
}

// Une restauration lancée par un humain répond à un besoin ponctuel : elle ne
// prouve rien sur la fraîcheur du contrôle périodique.
func TestSeedFreshnessIgnoresManualRestores(t *testing.T) {
	ctx := context.Background()
	history, restores, storages := seedFixture(t)

	if err := restores.Create(ctx, &domain.RestoreRecord{
		StorageID: 1, Status: domain.BackupSuccess, StartTime: time.Now().UTC(),
		TriggeredBy: domain.Actor{UserID: 1, Name: "admin"},
	}); err != nil {
		t.Fatal(err)
	}

	sink := newCaptureSink()
	n, err := SeedFreshness(ctx, history, restores, storages, sink)
	if err != nil {
		t.Fatalf("SeedFreshness() error: %v", err)
	}
	if n != 0 || len(sink.verifications) != 0 {
		t.Fatalf("une restauration manuelle ne doit pas réamorcer la preuve de restaurabilité : %+v", sink.verifications)
	}
}

// Un dépôt supprimé depuis laisserait une série que plus rien ne met à jour.
func TestSeedFreshnessSkipsDeletedStorages(t *testing.T) {
	ctx := context.Background()
	history, restores, storages := seedFixture(t)
	if err := storages.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := restores.Create(ctx, &domain.RestoreRecord{
		StorageID: 1, Status: domain.BackupSuccess, StartTime: time.Now().UTC(),
		TriggeredBy: domain.Actor{Name: domain.VerificationActor},
	}); err != nil {
		t.Fatal(err)
	}

	sink := newCaptureSink()
	if _, err := SeedFreshness(ctx, history, restores, storages, sink); err != nil {
		t.Fatalf("SeedFreshness() error: %v", err)
	}
	if len(sink.verifications) != 0 {
		t.Fatalf("dépôt supprimé : aucune série ne doit être réamorcée, got %+v", sink.verifications)
	}
}
