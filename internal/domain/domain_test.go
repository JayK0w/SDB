package domain

import (
	"errors"
	"testing"
	"time"
)

func TestRetentionPolicyValidate(t *testing.T) {
	tests := []struct {
		name    string
		policy  RetentionPolicy
		wantErr bool
	}{
		{"keep last only", RetentionPolicy{KeepLast: 7}, false},
		{"full policy", RetentionPolicy{KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, Prune: true}, false},
		{"zero policy refused", RetentionPolicy{}, true},
		{"prune alone refused", RetentionPolicy{Prune: true}, true},
		{"negative count refused", RetentionPolicy{KeepDaily: -1, KeepLast: 3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestHookValidate(t *testing.T) {
	valid := Hook{Command: []string{"pg_dumpall", "-U", "postgres"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid hook rejected: %v", err)
	}
	empty := Hook{}
	if err := empty.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty hook accepted, err = %v", err)
	}
	badPolicy := Hook{Command: []string{"true"}, OnFailure: "retry"}
	if err := badPolicy.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown failure policy accepted, err = %v", err)
	}
}

func TestHookEffectiveDefaults(t *testing.T) {
	h := Hook{Command: []string{"true"}}
	if got := h.EffectiveTimeout(); got != DefaultHookTimeout {
		t.Fatalf("EffectiveTimeout() = %v, want %v", got, DefaultHookTimeout)
	}
	if got := h.EffectiveOnFailure(HookContinue); got != HookContinue {
		t.Fatalf("EffectiveOnFailure() = %v, want %v", got, HookContinue)
	}
	h.Timeout = time.Minute
	h.OnFailure = HookAbort
	if got := h.EffectiveTimeout(); got != time.Minute {
		t.Fatalf("EffectiveTimeout() = %v, want 1m", got)
	}
	if got := h.EffectiveOnFailure(HookContinue); got != HookAbort {
		t.Fatalf("EffectiveOnFailure() = %v, want abort", got)
	}
}

func TestBackupRequestValidate(t *testing.T) {
	base := BackupRequest{ContainerID: "abc123", StorageID: 1}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	noContainer := BackupRequest{StorageID: 1}
	if err := noContainer.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("request without container accepted, err = %v", err)
	}
	badHook := base
	badHook.PreHook = &Hook{}
	if err := badHook.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("request with invalid pre-hook accepted, err = %v", err)
	}
	badRetention := base
	badRetention.Retention = &RetentionPolicy{}
	if err := badRetention.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("request with empty retention accepted, err = %v", err)
	}
}

func TestBackupStatusTerminal(t *testing.T) {
	terminal := []BackupStatus{BackupSuccess, BackupWarning, BackupFailed, BackupCanceled}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []BackupStatus{BackupPending, BackupRunning} {
		if s.Terminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestStorageConfigRedacted(t *testing.T) {
	cfg := StorageConfig{
		Name:           "offsite",
		Type:           StorageS3,
		Endpoint:       "s3:https://s3.example.com/backups",
		ResticPassword: "super-secret",
		Credentials: map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIA...",
			"AWS_SECRET_ACCESS_KEY": "secret",
		},
	}
	red := cfg.Redacted()
	if red.ResticPassword != "" {
		t.Fatal("restic password leaked through Redacted()")
	}
	for k, v := range red.Credentials {
		if v != "" {
			t.Fatalf("credential %s leaked through Redacted()", k)
		}
	}
	if len(red.Credentials) != len(cfg.Credentials) {
		t.Fatal("Redacted() must keep credential keys for the UI")
	}
	if cfg.Credentials["AWS_SECRET_ACCESS_KEY"] != "secret" {
		t.Fatal("Redacted() mutated the original config")
	}
}

func TestContainerBackupableMounts(t *testing.T) {
	c := Container{Mounts: []Mount{
		{Type: MountVolume, Name: "pgdata"},
		{Type: MountBind, Source: "/srv/app"},
		{Type: "tmpfs", Destination: "/tmp"},
	}}
	got := c.BackupableMounts()
	if len(got) != 2 {
		t.Fatalf("BackupableMounts() = %d mounts, want 2", len(got))
	}
}
