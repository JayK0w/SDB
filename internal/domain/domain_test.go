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
		{"keep last seul", RetentionPolicy{KeepLast: 7}, false},
		{"politique complète", RetentionPolicy{KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, Prune: true}, false},
		{"vide refusée", RetentionPolicy{}, true},
		{"prune seul refusé", RetentionPolicy{Prune: true}, true},
		{"compte négatif refusé", RetentionPolicy{KeepDaily: -1, KeepLast: 3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() = %v, attendu ErrInvalidInput", err)
			}
		})
	}
}

func TestHookValidate(t *testing.T) {
	valid := Hook{Command: []string{"pg_dumpall", "-U", "postgres"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("hook valide rejeté : %v", err)
	}
	empty := Hook{}
	if err := empty.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("hook vide accepté, err = %v", err)
	}
	badPolicy := Hook{Command: []string{"true"}, OnFailure: "retry"}
	if err := badPolicy.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("politique inconnue acceptée, err = %v", err)
	}
}

func TestHookEffectiveDefaults(t *testing.T) {
	h := Hook{Command: []string{"true"}}
	if got := h.EffectiveTimeout(); got != DefaultHookTimeout {
		t.Fatalf("EffectiveTimeout() = %v, attendu %v", got, DefaultHookTimeout)
	}
	if got := h.EffectiveOnFailure(HookContinue); got != HookContinue {
		t.Fatalf("EffectiveOnFailure() = %v, attendu continue", got)
	}
	h.Timeout = time.Minute
	h.OnFailure = HookAbort
	if got := h.EffectiveTimeout(); got != time.Minute {
		t.Fatalf("EffectiveTimeout() = %v, attendu 1m", got)
	}
	if got := h.EffectiveOnFailure(HookContinue); got != HookAbort {
		t.Fatalf("EffectiveOnFailure() = %v, attendu abort", got)
	}
}

func TestBackupRequestValidate(t *testing.T) {
	base := BackupRequest{ContainerID: "abc123", StorageID: 1}
	if err := base.Validate(); err != nil {
		t.Fatalf("requête valide rejetée : %v", err)
	}
	noContainer := BackupRequest{StorageID: 1}
	if err := noContainer.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("requête sans conteneur acceptée, err = %v", err)
	}
	badHook := base
	badHook.PreHook = &Hook{}
	if err := badHook.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("pre-hook invalide accepté, err = %v", err)
	}
	badRetention := base
	badRetention.Retention = &RetentionPolicy{}
	if err := badRetention.Validate(); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("rétention vide acceptée, err = %v", err)
	}
}

func TestBackupStatusTerminal(t *testing.T) {
	for _, s := range []BackupStatus{BackupSuccess, BackupWarning, BackupFailed, BackupCanceled} {
		if !s.Terminal() {
			t.Errorf("%s devrait être terminal", s)
		}
	}
	for _, s := range []BackupStatus{BackupPending, BackupRunning} {
		if s.Terminal() {
			t.Errorf("%s ne devrait pas être terminal", s)
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
		t.Fatal("mot de passe restic exposé par Redacted()")
	}
	for k, v := range red.Credentials {
		if v != "" {
			t.Fatalf("credential %s exposé par Redacted()", k)
		}
	}
	if len(red.Credentials) != len(cfg.Credentials) {
		t.Fatal("Redacted() doit conserver les clés pour l'UI")
	}
	if cfg.Credentials["AWS_SECRET_ACCESS_KEY"] != "secret" {
		t.Fatal("Redacted() a modifié l'original")
	}
}

func TestContainerBackupableMounts(t *testing.T) {
	c := Container{Mounts: []Mount{
		{Type: MountVolume, Name: "pgdata"},
		{Type: MountBind, Source: "/srv/app"},
		{Type: "tmpfs", Destination: "/tmp"},
	}}
	if got := c.BackupableMounts(); len(got) != 2 {
		t.Fatalf("BackupableMounts() = %d montages, attendu 2", len(got))
	}
}

func TestScheduleToRequestTags(t *testing.T) {
	s := BackupSchedule{Name: "nightly", ContainerName: "db", StorageID: 2, Tags: []string{"prod"}}
	req := s.ToRequest()
	if req.ContainerID != "db" || req.StorageID != 2 {
		t.Fatalf("ToRequest() cible erronée : %+v", req)
	}
	if len(req.Tags) != 2 || req.Tags[0] != "scheduled:nightly" {
		t.Fatalf("ToRequest() tags = %v", req.Tags)
	}
}
