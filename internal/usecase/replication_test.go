package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// newReplicationFixture : un dépôt principal (id 1) et sa copie secondaire
// (id 2), tous deux vides.
func newReplicationFixture(t *testing.T) (*ReplicationService, *fakeEngine, *memStorages) {
	t.Helper()
	storages := newMemStorages()
	ctx := context.Background()
	if err := storages.Create(ctx, &domain.StorageConfig{
		Name: "primary", Type: domain.StorageLocal, Endpoint: "/srv/primary", ResticPassword: "pw1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := storages.Create(ctx, &domain.StorageConfig{
		Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", ResticPassword: "pw2",
		CopyOf: 1, AppendOnly: true,
	}); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{snapshotsByID: map[int64][]domain.Snapshot{}}
	svc := NewReplicationService(engine, storages, &capturePublisher{}, discardLogger())
	return svc, engine, storages
}

func snap(id string, at time.Time, path string) domain.Snapshot {
	return domain.Snapshot{ID: id, ShortID: id[:4], Time: at, Paths: []string{path}}
}

// Une passe de réconciliation recopie ce qui manque — c'est ce qui rattrape
// une destination injoignable au moment de la sauvegarde, ou un arrêt de SDB.
func TestReplicateCopiesMissingSnapshots(t *testing.T) {
	svc, engine, _ := newReplicationFixture(t)
	now := time.Now().UTC()
	engine.snapshotsByID[1] = []domain.Snapshot{
		snap("aaaa1111", now.Add(-2*time.Hour), "/sdb/data/pgdata"),
		snap("bbbb2222", now.Add(-time.Hour), "/sdb/data/pgdata"),
	}

	st, err := svc.Replicate(context.Background(), 2)
	if err != nil {
		t.Fatalf("Replicate() error: %v", err)
	}
	if st.Pending != 0 {
		t.Fatalf("Pending = %d after a full pass, want 0", st.Pending)
	}
	if st.CopiedSnapshots != 2 {
		t.Fatalf("CopiedSnapshots = %d, want 2", st.CopiedSnapshots)
	}
	if st.Lag() != 0 {
		t.Fatalf("Lag = %s once everything is copied, want 0", st.Lag())
	}
}

// LE piège de cette fonctionnalité : la copie ré-encrypte, donc les
// identifiants diffèrent d'un dépôt à l'autre. Comparer par identifiant ferait
// voir un retard permanent — et déclencherait une recopie complète à chaque
// passe.
func TestReplicationStatusMatchesSnapshotsAcrossDifferentIDs(t *testing.T) {
	svc, engine, _ := newReplicationFixture(t)
	at := time.Now().UTC().Add(-time.Hour)
	engine.snapshotsByID[1] = []domain.Snapshot{snap("aaaa1111", at, "/sdb/data/pgdata")}
	// même snapshot côté copie, identifiant différent
	engine.snapshotsByID[2] = []domain.Snapshot{snap("ffff9999", at, "/sdb/data/pgdata")}

	st, err := svc.Status(context.Background(), 2)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if st.Pending != 0 {
		t.Fatalf("Pending = %d, want 0: le snapshot EST copié, seul son identifiant change", st.Pending)
	}
}

// Le retard se mesure sur le PLUS ANCIEN snapshot manquant : prendre le plus
// récent afficherait quelques minutes de retard alors que des semaines de
// sauvegardes ne sont jamais parties.
func TestReplicationLagUsesOldestPendingSnapshot(t *testing.T) {
	svc, engine, _ := newReplicationFixture(t)
	now := time.Now().UTC()
	engine.snapshotsByID[1] = []domain.Snapshot{
		snap("aaaa1111", now.Add(-72*time.Hour), "/sdb/data/pgdata"),
		snap("bbbb2222", now.Add(-time.Minute), "/sdb/data/pgdata"),
	}

	st, err := svc.Status(context.Background(), 2)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if st.Pending != 2 {
		t.Fatalf("Pending = %d, want 2", st.Pending)
	}
	if st.Lag() < 71*time.Hour {
		t.Fatalf("Lag = %s, want the age of the oldest uncopied snapshot (~72h)", st.Lag())
	}
}

// Rejouer une passe ne doit rien recopier : sans cette idempotence, chaque
// réconciliation re-téléverserait tout le dépôt.
func TestReplicateIsIdempotent(t *testing.T) {
	svc, engine, _ := newReplicationFixture(t)
	engine.snapshotsByID[1] = []domain.Snapshot{snap("aaaa1111", time.Now().UTC(), "/sdb/data/pgdata")}

	for i := 0; i < 2; i++ {
		if _, err := svc.Replicate(context.Background(), 2); err != nil {
			t.Fatalf("Replicate() #%d error: %v", i, err)
		}
	}
	st, err := svc.Status(context.Background(), 2)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if st.CopiedSnapshots != 1 {
		t.Fatalf("CopiedSnapshots = %d after two passes, want 1", st.CopiedSnapshots)
	}
}

// Un dépôt principal n'est la copie de rien : demander son état de réplication
// est une erreur d'appel, pas un état vide qui laisserait croire à une copie
// parfaitement à jour.
func TestReplicationStatusRefusesPrimaryStorage(t *testing.T) {
	svc, _, _ := newReplicationFixture(t)

	_, err := svc.Status(context.Background(), 1)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// Le verrou append-only interdit forget et prune, jamais l'écriture : une
// copie doit continuer d'alimenter un dépôt protégé, sinon la protection
// empêcherait la seconde copie qu'elle est censée servir.
func TestReplicateWritesToAppendOnlyCopy(t *testing.T) {
	svc, engine, storages := newReplicationFixture(t)
	cfg, err := storages.GetByID(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AppendOnly {
		t.Fatal("fixture: la copie doit être append-only")
	}
	engine.snapshotsByID[1] = []domain.Snapshot{snap("aaaa1111", time.Now().UTC(), "/sdb/data/pgdata")}

	st, err := svc.Replicate(context.Background(), 2)
	if err != nil {
		t.Fatalf("Replicate() vers un dépôt append-only : %v", err)
	}
	if st.CopiedSnapshots != 1 {
		t.Fatalf("CopiedSnapshots = %d, want 1", st.CopiedSnapshots)
	}
}

// ---------------------------------------------------------------------------
// Intégration avec le pipeline de sauvegarde
// ---------------------------------------------------------------------------

// newBackupWithCopyFixture : le pipeline complet, dépôt principal id 1 et copie
// secondaire id 2.
func newBackupWithCopyFixture(t *testing.T) (*BackupService, *fakeEngine, *memHistory, *memStorages) {
	t.Helper()
	runtime := &fakeRuntime{container: &domain.Container{
		ID: "c1", Name: "postgres", State: domain.ContainerRunning,
		Mounts: []domain.Mount{
			{Type: domain.MountVolume, Name: "pgdata", Source: "pgdata", Destination: "/var/lib/postgresql/data"},
		},
	}}
	storages := newMemStorages()
	ctx := context.Background()
	if err := storages.Create(ctx, &domain.StorageConfig{
		Name: "primary", Type: domain.StorageLocal, Endpoint: "/srv/primary", ResticPassword: "pw1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := storages.Create(ctx, &domain.StorageConfig{
		Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite", ResticPassword: "pw2", CopyOf: 1,
	}); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{
		summary:       &domain.BackupSummary{SnapshotID: "snap1", BytesProcessed: 12345},
		snapshotsByID: map[int64][]domain.Snapshot{},
	}
	history := newMemHistory()
	pub := &capturePublisher{}
	repl := NewReplicationService(engine, storages, pub, discardLogger())
	svc := NewBackupService(runtime, engine, storages, history, pub, discardLogger(), WithReplicator(repl))
	return svc, engine, history, storages
}

// Une sauvegarde réussie part immédiatement vers la copie : le délai avant
// d'exister à deux exemplaires se compte en minutes, pas en heures.
func TestBackupReplicatesSnapshotImmediately(t *testing.T) {
	svc, engine, history, _ := newBackupWithCopyFixture(t)

	rec, err := svc.Start(context.Background(), domain.BackupRequest{ContainerID: "c1", StorageID: 1})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupSuccess {
		t.Fatalf("Status = %s (%s), want success", final.Status, final.ErrorLog)
	}

	copies := engine.copies()
	if len(copies) != 1 {
		t.Fatalf("%d copies, want 1", len(copies))
	}
	if copies[0].dst != 2 || copies[0].src != 1 {
		t.Fatalf("copy %d -> %d, want 1 -> 2", copies[0].src, copies[0].dst)
	}
	if len(copies[0].snapshots) != 1 || copies[0].snapshots[0] != "snap1" {
		t.Fatalf("copied snapshots = %v, want [snap1]", copies[0].snapshots)
	}
}

// LE point de la décision : une copie ratée dégrade en avertissement.
// Annoncer `failed` ferait croire qu'il n'y a pas de sauvegarde alors que le
// snapshot existe et est restaurable — plus dangereux que le contraire. Le
// trou reste signalé (alerte sur avertissement) et rattrapable.
func TestBackupWarnsWhenSecondaryCopyFails(t *testing.T) {
	svc, engine, history, _ := newBackupWithCopyFixture(t)
	engine.copyErr = errors.New("offsite unreachable")

	rec, err := svc.Start(context.Background(), domain.BackupRequest{ContainerID: "c1", StorageID: 1})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)

	if final.Status != domain.BackupWarning {
		t.Fatalf("Status = %s, want warning: le snapshot principal est valide", final.Status)
	}
	if final.SnapshotID != "snap1" {
		t.Fatalf("SnapshotID = %q : la sauvegarde principale doit rester exploitable", final.SnapshotID)
	}
	if !strings.Contains(final.ErrorLog, "offsite unreachable") {
		t.Fatalf("ErrorLog = %q, want the copy failure", final.ErrorLog)
	}
}

// La rétention s'applique APRÈS la copie : purger le dépôt principal d'abord
// pourrait effacer un snapshot qui n'existe encore qu'à un seul exemplaire.
func TestRetentionRunsAfterSecondaryCopy(t *testing.T) {
	svc, engine, history, _ := newBackupWithCopyFixture(t)
	engine.copyFn = func(context.Context) error {
		if len(engine.forgotten()) > 0 {
			t.Error("retention ran BEFORE the secondary copy: a single-copy snapshot could be pruned away")
		}
		return nil
	}

	rec, err := svc.Start(context.Background(), domain.BackupRequest{
		ContainerID: "c1", StorageID: 1,
		Retention: &domain.RetentionPolicy{KeepLast: 1, Prune: true},
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupSuccess {
		t.Fatalf("Status = %s (%s), want success", final.Status, final.ErrorLog)
	}
	if len(engine.forgotten()) != 1 {
		t.Fatalf("retention not applied: %v", engine.forgotten())
	}
}

// Sauvegarder directement dans un dépôt de copie mélangerait des snapshots
// natifs et répliqués : l'écart de réplication ne voudrait plus rien dire, et
// l'alerte « la seconde copie a décroché » deviendrait fausse en silence.
func TestBackupIntoCopyTargetIsRefused(t *testing.T) {
	svc, _, _, _ := newBackupWithCopyFixture(t)

	_, err := svc.Start(context.Background(), domain.BackupRequest{ContainerID: "c1", StorageID: 2})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// Sans copie configurée, le pipeline ne doit pas changer de comportement.
func TestBackupWithoutSecondaryCopyStaysSuccessful(t *testing.T) {
	svc, engine, history, storages := newBackupWithCopyFixture(t)
	if err := storages.Delete(context.Background(), 2); err != nil {
		t.Fatal(err)
	}

	rec, err := svc.Start(context.Background(), domain.BackupRequest{ContainerID: "c1", StorageID: 1})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupSuccess {
		t.Fatalf("Status = %s (%s), want success", final.Status, final.ErrorLog)
	}
	if len(engine.copies()) != 0 {
		t.Fatalf("copies = %v, want none", engine.copies())
	}
}
