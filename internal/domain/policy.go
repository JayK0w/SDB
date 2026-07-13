package domain

import (
	"fmt"
	"time"
)

type HookFailurePolicy string

const (
	HookAbort    HookFailurePolicy = "abort"    // échec du hook = échec du run
	HookContinue HookFailurePolicy = "continue" // échec toléré → warning
)

// Borne un hook sans timeout : un pg_dumpall bloqué ne gèle pas le pipeline.
const DefaultHookTimeout = 5 * time.Minute

// Hook : commande exécutée dans le conteneur cible avant/après snapshot.
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

// def : abort pour un pre-hook, continue pour un post-hook.
func (h *Hook) EffectiveOnFailure(def HookFailurePolicy) HookFailurePolicy {
	if h.OnFailure != "" {
		return h.OnFailure
	}
	return def
}

// RetentionPolicy : correspondance directe avec restic forget --keep-*.
type RetentionPolicy struct {
	KeepLast    int
	KeepHourly  int
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
	Prune       bool // récupère l'espace juste après le forget
}

func (p RetentionPolicy) IsZero() bool {
	return p.KeepLast == 0 && p.KeepHourly == 0 && p.KeepDaily == 0 &&
		p.KeepWeekly == 0 && p.KeepMonthly == 0 && p.KeepYearly == 0
}

func (p RetentionPolicy) Validate() error {
	// refuse une politique qui supprimerait tous les snapshots
	if p.IsZero() {
		return fmt.Errorf("%w: retention policy keeps no snapshots at all", ErrInvalidInput)
	}
	for _, v := range []int{p.KeepLast, p.KeepHourly, p.KeepDaily, p.KeepWeekly, p.KeepMonthly, p.KeepYearly} {
		if v < 0 {
			return fmt.Errorf("%w: retention keep counts must be positive", ErrInvalidInput)
		}
	}
	return nil
}
