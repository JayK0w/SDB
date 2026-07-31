package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func newVerifyFixture(t *testing.T) (*VerificationService, *fakeRuntime, *fakeEngine, *memRestores) {
	t.Helper()
	runtime := &fakeRuntime{}
	storages := newMemStorages()
	if err := storages.Create(context.Background(), &domain.StorageConfig{
		Name: "local", Type: domain.StorageLocal, Endpoint: "/backups", ResticPassword: "pw",
	}); err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{snapshots: []domain.Snapshot{
		{ID: "old", ShortID: "old", Time: time.Now().Add(-48 * time.Hour), Paths: []string{"/sdb/data/pgdata"}},
		{ID: "new", ShortID: "new", Time: time.Now().Add(-1 * time.Hour), Paths: []string{"/sdb/data/pgdata"}},
	}}
	history := newMemRestores()
	svc := NewVerificationService(runtime, engine, storages, history, &capturePublisher{}, discardLogger())
	return svc, runtime, engine, history
}

// Le cœur du chantier : la vérification restaure RÉELLEMENT le dernier
// snapshot, dans un volume jetable, avec contrôle des empreintes.
func TestVerifyRestoresLatestSnapshotIntoScratchVolumeWithVerify(t *testing.T) {
	svc, runtime, engine, history := newVerifyFixture(t)

	rec, err := svc.VerifyStorage(context.Background(), 1)
	if err != nil {
		t.Fatalf("VerifyStorage() error: %v", err)
	}
	if rec == nil {
		t.Fatal("a verification run must be recorded")
	}

	if len(engine.restores) != 1 {
		t.Fatalf("expected exactly one restore, got %v", engine.restores)
	}
	trace := engine.restores[0]
	if !strings.HasPrefix(trace, "new:pgdata->"+domain.VerifyVolumePrefix) {
		t.Fatalf("trace = %q; want the LATEST snapshot restored into a scratch volume", trace)
	}
	if !strings.HasSuffix(trace, "#verify") {
		t.Fatalf("trace = %q; verification must pass --verify", trace)
	}

	// le jetable ne doit pas survivre au contrôle
	if got := runtime.removed(); len(got) != 1 || !domain.IsScratchVolume(got[0]) {
		t.Fatalf("scratch volume was not cleaned up: %v", got)
	}

	final, err := history.GetByID(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != domain.BackupSuccess {
		t.Fatalf("Status = %s, want success", final.Status)
	}
	if final.TriggeredBy.Name != "system:verification" {
		t.Fatalf("TriggeredBy = %q, want system:verification", final.TriggeredBy.Name)
	}
}

// Un échec doit être enregistré ET remonter : c'est tout l'intérêt, sinon
// on découvre le problème le jour de la vraie restauration.
func TestVerifyFailureIsRecordedAndReturned(t *testing.T) {
	svc, runtime, engine, history := newVerifyFixture(t)
	engine.restoreErr = errors.New("hash mismatch on /sdb/data/pgdata/base/1")

	rec, err := svc.VerifyStorage(context.Background(), 1)
	if err == nil {
		t.Fatal("a failed verification must return an error")
	}
	if rec == nil {
		t.Fatal("the failed run must still be recorded")
	}
	final, gerr := history.GetByID(context.Background(), rec.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if final.Status != domain.BackupFailed {
		t.Fatalf("Status = %s, want failed", final.Status)
	}
	if final.ErrorLog == "" {
		t.Fatal("a failed verification must say why")
	}
	// le nettoyage a lieu même quand la vérification échoue
	if got := runtime.removed(); len(got) != 1 {
		t.Fatalf("scratch volume must be removed even on failure, got %v", got)
	}
}

// Un dépôt vide n'est pas un échec : il n'y a simplement rien à prouver.
func TestVerifySkipsEmptyRepository(t *testing.T) {
	svc, runtime, engine, _ := newVerifyFixture(t)
	engine.snapshots = nil

	rec, err := svc.VerifyStorage(context.Background(), 1)
	if err != nil {
		t.Fatalf("an empty repository must not be an error, got %v", err)
	}
	if rec != nil {
		t.Fatal("nothing should be recorded for an empty repository")
	}
	if got := runtime.removed(); len(got) != 0 {
		t.Fatalf("nothing to clean up, got %v", got)
	}
}

// Garde-fou : la cible d'une vérification est toujours un jetable. Le runtime
// refuse tout autre nom, donc une régression qui viserait un volume de
// production se solderait par une erreur, pas par une destruction.
func TestScratchVolumeGuardRejectsProductionVolumes(t *testing.T) {
	rt := &fakeRuntime{}
	if err := rt.RemoveVolume(context.Background(), "pgdata"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("RemoveVolume(pgdata) = %v, want ErrForbidden", err)
	}
	if err := rt.RemoveVolume(context.Background(), domain.VerifyVolumePrefix+"abc"); err != nil {
		t.Fatalf("RemoveVolume on a scratch volume: %v", err)
	}
	if domain.IsScratchVolume(domain.VerifyVolumePrefix) {
		t.Fatal("the bare prefix is not a scratch volume name")
	}
}

// Un dépôt en échec ne doit pas masquer l'état des autres.
func TestVerifyAllContinuesAcrossStorages(t *testing.T) {
	runtime := &fakeRuntime{}
	storages := newMemStorages()
	ctx := context.Background()
	for _, name := range []string{"a", "b"} {
		if err := storages.Create(ctx, &domain.StorageConfig{
			Name: name, Type: domain.StorageLocal, Endpoint: "/b", ResticPassword: "pw",
		}); err != nil {
			t.Fatal(err)
		}
	}
	engine := &fakeEngine{
		snapshots:  []domain.Snapshot{{ID: "s", ShortID: "s", Time: time.Now(), Paths: []string{"/sdb/data/v"}}},
		restoreErr: errors.New("boom"),
	}
	svc := NewVerificationService(runtime, engine, storages, newMemRestores(), &capturePublisher{}, discardLogger())

	err := svc.VerifyAll(ctx)
	if err == nil {
		t.Fatal("VerifyAll must report the failures")
	}
	// les DEUX dépôts ont été tentés malgré l'échec du premier
	if len(engine.restores) != 2 {
		t.Fatalf("expected both storages to be attempted, got %v", engine.restores)
	}
}

// Un snapshot dont les chemins ne sont pas exploitables ne doit pas produire
// une restauration vers une cible arbitraire.
func TestVerifyRejectsSnapshotWithoutDataPath(t *testing.T) {
	svc, _, engine, _ := newVerifyFixture(t)
	engine.snapshots = []domain.Snapshot{
		{ID: "weird", ShortID: "weird", Time: time.Now(), Paths: []string{"/etc/passwd"}},
	}

	if _, err := svc.VerifyStorage(context.Background(), 1); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if len(engine.restores) != 0 {
		t.Fatalf("no restore should have been attempted, got %v", engine.restores)
	}
}
