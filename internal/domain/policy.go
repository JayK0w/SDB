package domain

import (
	"fmt"
	"time"
)

// HookFailurePolicy decides what happens to the backup run when a hook fails.
type HookFailurePolicy string

const (
	// HookAbort fails the backup if the hook fails. Sensible default for
	// pre-hooks: snapshotting inconsistent data is worse than not
	// snapshotting at all.
	HookAbort HookFailurePolicy = "abort"
	// HookContinue records the failure but lets the run proceed; the run
	// then finishes in BackupWarning instead of BackupSuccess.
	HookContinue HookFailurePolicy = "continue"
)

// DefaultHookTimeout bounds hook execution when no timeout is configured,
// so a stuck pg_dumpall cannot block the backup pipeline forever.
const DefaultHookTimeout = 5 * time.Minute

// Hook is a command executed inside the target container before or after
// the snapshot, e.g. ["sh", "-c", "pg_dumpall -U postgres > /var/lib/postgresql/data/dump.sql"].
type Hook struct {
	Command   []string
	Timeout   time.Duration
	OnFailure HookFailurePolicy
}

func (h *Hook) Validate() error {
	if len(h.Command) == 0 {
		return fmt.Errorf("%w: hook command is empty", ErrInvalidInput)
	}
	if h.OnFailure != "" && h.OnFailure != HookAbort && h.OnFailure != HookContinue {
		return fmt.Errorf("%w: unknown hook failure policy %q", ErrInvalidInput, h.OnFailure)
	}
	if h.Timeout < 0 {
		return fmt.Errorf("%w: hook timeout must be positive", ErrInvalidInput)
	}
	return nil
}

func (h *Hook) EffectiveTimeout() time.Duration {
	if h.Timeout > 0 {
		return h.Timeout
	}
	return DefaultHookTimeout
}

// EffectiveOnFailure resolves the policy, falling back to the caller's
// default (abort for pre-hooks, continue for post-hooks).
func (h *Hook) EffectiveOnFailure(def HookFailurePolicy) HookFailurePolicy {
	if h.OnFailure != "" {
		return h.OnFailure
	}
	return def
}

// RetentionPolicy maps one-to-one to restic `forget --keep-*` flags.
type RetentionPolicy struct {
	KeepLast    int
	KeepHourly  int
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	Prune       bool // reclaim repository space right after forgetting
}

func (p RetentionPolicy) IsZero() bool {
	return p.KeepLast == 0 && p.KeepHourly == 0 && p.KeepDaily == 0 &&
		p.KeepWeekly == 0 && p.KeepMonthly == 0 && p.KeepYearly == 0
}

func (p RetentionPolicy) Validate() error {
	if p.IsZero() {
		// Refuse a policy that would delete every snapshot: retention is
		// opt-in per keep rule, deleting everything must be explicit
		// (dedicated snapshot deletion, not retention).
		return fmt.Errorf("%w: retention policy keeps no snapshots at all", ErrInvalidInput)
	}
	for _, v := range []int{p.KeepLast, p.KeepHourly, p.KeepDaily, p.KeepWeekly, p.KeepMonthly, p.KeepYearly} {
		if v < 0 {
			return fmt.Errorf("%w: retention keep counts must be positive", ErrInvalidInput)
		}
	}
	return nil
}
