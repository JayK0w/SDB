package usecase

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// syncBuffer : slog ecrit depuis la goroutine du test, mais un handler slog
// peut etre appele en concurrence — on protege pour ne pas dependre de ca.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// checkFixture : service de stockage dont on peut RELIRE les journaux.
func checkFixture(t *testing.T, checkErr error) (*StorageService, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	storages := newMemStorages()
	engine := &fakeEngine{checkErr: checkErr}
	svc := NewStorageService(storages, engine, slog.New(slog.NewTextHandler(logs, nil)))

	cfg := &domain.StorageConfig{
		Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite",
		ResticPassword: "pw",
	}
	if err := storages.Create(context.Background(), cfg); err != nil {
		t.Fatalf("seeding storage: %v", err)
	}
	return svc, logs
}

// Le succes doit LAISSER UNE TRACE. Sans elle, « pas de nouvelles » veut dire
// aussi bien « a reussi » que « tourne encore », et l'operateur qui vient de
// declencher un controle ne peut pas trancher — constate en production : deux
// controles lances, zero ligne, aucun moyen de savoir s'ils avaient abouti.
func TestOnDemandIntegrityCheckLogsItsSuccess(t *testing.T) {
	svc, logs := checkFixture(t, nil)

	if err := svc.CheckIntegrity(context.Background(), 1); err != nil {
		t.Fatalf("CheckIntegrity() error: %v", err)
	}

	out := logs.String()
	for _, want := range []string{"integrity check started", "integrity check passed", "storage=offsite"} {
		if !strings.Contains(out, want) {
			t.Fatalf("logs missing %q:\n%s", want, out)
		}
	}
	// La duree est ce qui rend la ligne exploitable : un check qui passe de
	// 3 s a 4 min sur le meme depot dit quelque chose sur le support.
	if !strings.Contains(out, "duration=") {
		t.Fatalf("the success line must carry the duration:\n%s", out)
	}
}

// L'echec reste journalise par le SERVICE : le handler HTTP ne le fait plus,
// et le perdre rendrait un controle rate parfaitement silencieux.
func TestOnDemandIntegrityCheckLogsItsFailure(t *testing.T) {
	svc, logs := checkFixture(t, errors.New("pack 4a2f is unreadable"))

	if err := svc.CheckIntegrity(context.Background(), 1); err == nil {
		t.Fatal("CheckIntegrity() returned nil on a failing repository")
	}

	out := logs.String()
	if !strings.Contains(out, "integrity check failed") {
		t.Fatalf("logs missing the failure line:\n%s", out)
	}
	// La cause doit voyager jusqu'au journal, pas seulement le fait qu'il y a
	// eu un echec.
	if !strings.Contains(out, "pack 4a2f is unreadable") {
		t.Fatalf("logs must carry the underlying restic error:\n%s", out)
	}
	if strings.Contains(out, "integrity check passed") {
		t.Fatalf("a failed check must not also report success:\n%s", out)
	}
}

// Les deux chemins produisent les MEMES messages : seul `trigger` les separe.
// Sans lui, le RUNBOOK ne pourrait plus faire distinguer une passe periodique
// qui s'arme d'un operateur qui a clique.
func TestIntegrityCheckLogsDistinguishOnDemandFromPeriodic(t *testing.T) {
	svc, onDemand := checkFixture(t, nil)
	if err := svc.CheckIntegrity(context.Background(), 1); err != nil {
		t.Fatalf("CheckIntegrity() error: %v", err)
	}
	if !strings.Contains(onDemand.String(), "trigger=on-demand") {
		t.Fatalf("on-demand check must be tagged as such:\n%s", onDemand.String())
	}

	periodic := &syncBuffer{}
	storages := newMemStorages()
	cfg := &domain.StorageConfig{
		Name: "offsite", Type: domain.StorageLocal, Endpoint: "/srv/offsite",
		ResticPassword: "pw",
	}
	if err := storages.Create(context.Background(), cfg); err != nil {
		t.Fatalf("seeding storage: %v", err)
	}
	maint := NewMaintenanceService(storages, &fakeEngine{}, slog.New(slog.NewTextHandler(periodic, nil)))
	if err := maint.RunChecks(context.Background()); err != nil {
		t.Fatalf("RunChecks() error: %v", err)
	}
	if !strings.Contains(periodic.String(), "trigger=periodic") {
		t.Fatalf("periodic pass must be tagged as such:\n%s", periodic.String())
	}
}
