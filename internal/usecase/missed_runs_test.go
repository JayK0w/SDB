package usecase

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func ptrTime(t time.Time) *time.Time { return &t }

func TestMissedRunsCountsElapsedWindows(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		spec    string
		lastRun *time.Time
		want    int
	}{
		{"jamais lance", "0 * * * *", nil, 0},
		{"a jour", "0 * * * *", ptrTime(now.Add(-30 * time.Minute)), 0},
		// 09:00 -> 12:00 : 10:00 et 11:00 sont ratees, celle de 12:00 tombe
		// pile maintenant et sera tiree par le cron vivant
		{"trois heures d'arret, cron horaire", "0 * * * *", ptrTime(now.Add(-3 * time.Hour)), 2},
		{"deux jours d'arret, cron quotidien", "0 3 * * *", ptrTime(now.Add(-48 * time.Hour)), 2},
		{"arret tres long, comptage plafonne", "* * * * *", ptrTime(now.Add(-30 * 24 * time.Hour)), maxCountedMisses},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := missedRuns(tc.spec, tc.lastRun, now)
			if err != nil {
				t.Fatalf("missedRuns() error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("missedRuns() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMissedRunsRejectsInvalidCron(t *testing.T) {
	last := time.Now().Add(-time.Hour)
	if _, err := missedRuns("pas-du-cron", &last, time.Now()); err == nil {
		t.Fatal("an invalid cron expression must be reported")
	}
}

func schedulerWithSchedule(t *testing.T, sched *domain.BackupSchedule, opts ...SchedulerOption) (*SchedulerService, *memHistory) {
	t.Helper()
	runtime := &fakeRuntime{container: &domain.Container{
		ID: "c1", Name: "postgres", State: domain.ContainerRunning,
		Mounts: []domain.Mount{{Type: domain.MountVolume, Name: "pgdata", Destination: "/data"}},
	}}
	storages := newMemStorages()
	if err := storages.Create(context.Background(), &domain.StorageConfig{
		Name: "local", Type: domain.StorageLocal, Endpoint: "/b", ResticPassword: "pw",
	}); err != nil {
		t.Fatal(err)
	}
	history := newMemHistory()
	backups := NewBackupService(runtime, &fakeEngine{summary: &domain.BackupSummary{SnapshotID: "s"}},
		storages, history, &capturePublisher{}, discardLogger())

	repo := newMemSchedules()
	if err := repo.Create(context.Background(), sched); err != nil {
		t.Fatal(err)
	}
	return NewSchedulerService(repo, backups, discardLogger(), opts...), history
}

// Le trou doit être signalé même sans rattrapage : c'est ce qui empêche une
// coupure de passer inaperçue.
func TestMissedWindowIsReportedWithoutCatchUp(t *testing.T) {
	var mu sync.Mutex
	var seen int
	sched := &domain.BackupSchedule{
		Name: "nightly", Cron: "0 * * * *", Enabled: true,
		ContainerName: "postgres", StorageID: 1,
		LastRunAt: ptrTime(time.Now().Add(-5 * time.Hour)),
	}
	svc, history := schedulerWithSchedule(t, sched,
		WithMissedRunHandler(func(_ domain.BackupSchedule, missed int) {
			mu.Lock()
			seen = missed
			mu.Unlock()
		}))

	svc.reportMissed([]domain.BackupSchedule{*sched})

	mu.Lock()
	got := seen
	mu.Unlock()
	if got < 4 {
		t.Fatalf("missed runs reported = %d, want at least 4", got)
	}
	// sans rattrapage, aucune sauvegarde ne doit avoir démarré
	recs, _ := history.List(context.Background(), domain.HistoryFilter{})
	if len(recs) != 0 {
		t.Fatalf("catch-up is off: no backup should have started, got %d", len(recs))
	}
}

// Avec rattrapage : UNE seule reprise, pas l'arriéré complet.
func TestCatchUpFiresExactlyOnceRegardlessOfBacklog(t *testing.T) {
	sched := &domain.BackupSchedule{
		Name: "nightly", Cron: "* * * * *", Enabled: true,
		ContainerName: "postgres", StorageID: 1,
		LastRunAt: ptrTime(time.Now().Add(-6 * time.Hour)), // des centaines d'échéances
	}
	svc, history := schedulerWithSchedule(t, sched, WithCatchUp(true))

	svc.reportMissed([]domain.BackupSchedule{*sched})

	recs, err := history.List(context.Background(), domain.HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("catch-up started %d runs, want exactly 1", len(recs))
	}
	if recs[0].TriggeredBy.Name != "system:catchup:nightly" {
		t.Fatalf("TriggeredBy = %q, want the catch-up system actor", recs[0].TriggeredBy.Name)
	}
	if recs[0].TriggeredBy.UserID != 0 {
		t.Fatal("a catch-up run must not claim a user id")
	}
}

// Une planification désactivée n'a pas d'échéance : elle ne doit rien
// déclencher ni rien signaler.
func TestDisabledScheduleIsNeverCaughtUp(t *testing.T) {
	var called bool
	sched := &domain.BackupSchedule{
		Name: "off", Cron: "* * * * *", Enabled: false,
		ContainerName: "postgres", StorageID: 1,
		LastRunAt: ptrTime(time.Now().Add(-6 * time.Hour)),
	}
	svc, history := schedulerWithSchedule(t, sched,
		WithCatchUp(true),
		WithMissedRunHandler(func(domain.BackupSchedule, int) { called = true }))

	svc.reportMissed([]domain.BackupSchedule{*sched})

	if called {
		t.Fatal("a disabled schedule must not report missed runs")
	}
	recs, _ := history.List(context.Background(), domain.HistoryFilter{})
	if len(recs) != 0 {
		t.Fatalf("a disabled schedule must not be caught up, got %d runs", len(recs))
	}
}

// Une planification à jour ne doit rien déclencher.
func TestUpToDateScheduleReportsNothing(t *testing.T) {
	var called bool
	sched := &domain.BackupSchedule{
		Name: "fresh", Cron: "0 * * * *", Enabled: true,
		ContainerName: "postgres", StorageID: 1,
		LastRunAt: ptrTime(time.Now().Add(-2 * time.Minute)),
	}
	svc, history := schedulerWithSchedule(t, sched,
		WithCatchUp(true),
		WithMissedRunHandler(func(domain.BackupSchedule, int) { called = true }))

	svc.reportMissed([]domain.BackupSchedule{*sched})

	if called {
		t.Fatal("an up-to-date schedule must not be flagged")
	}
	recs, _ := history.List(context.Background(), domain.HistoryFilter{})
	if len(recs) != 0 {
		t.Fatalf("nothing to catch up, got %d runs", len(recs))
	}
}
