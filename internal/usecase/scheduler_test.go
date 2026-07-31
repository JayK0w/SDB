package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

func newSchedulerFixture(t *testing.T) (*SchedulerService, *memSchedules, *memHistory) {
	t.Helper()
	backupSvc, _, _, history, _ := newBackupFixture(t)
	schedules := newMemSchedules()
	return NewSchedulerService(schedules, backupSvc, discardLogger()), schedules, history
}

func TestValidateCron(t *testing.T) {
	for _, ok := range []string{"* * * * *", "0 3 * * *", "@daily", "*/5 * * * *"} {
		if err := ValidateCron(ok); err != nil {
			t.Errorf("ValidateCron(%q) rejected a valid expression: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "not a cron", "61 * * * *", "* * * *"} {
		if err := ValidateCron(bad); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("ValidateCron(%q) err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestScheduleCreateValidates(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSchedulerFixture(t)

	bad := &domain.BackupSchedule{Name: "s", Cron: "junk", ContainerName: "postgres", StorageID: 1}
	if err := svc.Create(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Create(bad cron) err = %v, want ErrInvalidInput", err)
	}
	missing := &domain.BackupSchedule{Name: "s", Cron: "* * * * *", StorageID: 1}
	if err := svc.Create(ctx, missing); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Create(no container) err = %v, want ErrInvalidInput", err)
	}
	good := &domain.BackupSchedule{Name: "nightly", Cron: "0 3 * * *", ContainerName: "postgres", StorageID: 1, Enabled: true}
	if err := svc.Create(ctx, good); err != nil {
		t.Fatalf("Create(valid) error: %v", err)
	}
	if good.ID == 0 {
		t.Fatal("Create() did not assign an id")
	}
}

func TestScheduleRunNowFiresBackup(t *testing.T) {
	ctx := context.Background()
	svc, schedules, history := newSchedulerFixture(t)

	sched := &domain.BackupSchedule{
		Name: "nightly", Cron: "0 3 * * *", ContainerName: "postgres", StorageID: 1,
		Enabled: true, Retention: &domain.RetentionPolicy{KeepLast: 3},
	}
	if err := svc.Create(ctx, sched); err != nil {
		t.Fatal(err)
	}

	actor := domain.Actor{UserID: 7, Name: "alice"}
	rec, err := svc.RunNow(ctx, sched.ID, actor)
	if err != nil {
		t.Fatalf("RunNow() error: %v", err)
	}
	final := waitTerminal(t, history, rec.ID)
	if final.Status != domain.BackupSuccess {
		t.Fatalf("scheduled run status = %s (%s)", final.Status, final.ErrorLog)
	}
	// déclenchement manuel : l'humain doit être attribué, pas le planificateur
	if final.TriggeredBy != actor {
		t.Fatalf("TriggeredBy = %+v, want the human actor %+v", final.TriggeredBy, actor)
	}

	stored, _ := schedules.GetByID(ctx, sched.ID)
	if stored.LastRunAt == nil {
		t.Fatal("RunNow() must record last_run_at")
	}
	if final.ContainerName != "postgres" {
		t.Fatalf("resolved container = %q", final.ContainerName)
	}
}
