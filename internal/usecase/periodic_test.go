package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// memMaintenanceState : dates de dernier passage en mémoire.
type memMaintenanceState struct {
	mu   sync.Mutex
	runs map[string]time.Time
	err  error
}

func newMemMaintenanceState() *memMaintenanceState {
	return &memMaintenanceState{runs: map[string]time.Time{}}
}

func (m *memMaintenanceState) LastRun(_ context.Context, task string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return time.Time{}, m.err
	}
	return m.runs[task], nil
}

func (m *memMaintenanceState) MarkRun(_ context.Context, task string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[task] = at
	return nil
}

func (m *memMaintenanceState) get(task string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runs[task]
}

func (m *memMaintenanceState) set(task string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[task] = at
}

// runCounter : compte les exécutions et signale la première.
type runCounter struct {
	mu    sync.Mutex
	count int
	first chan struct{}
	err   error
}

func newRunCounter() *runCounter { return &runCounter{first: make(chan struct{}, 8)} }

func (r *runCounter) fn(context.Context) error {
	r.mu.Lock()
	r.count++
	r.mu.Unlock()
	r.first <- struct{}{}
	return r.err
}

func (r *runCounter) runs() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// LE bug : chaque redémarrage repartait d'un intervalle complet. Sur une
// instance redémarrée plus souvent que l'intervalle — une mise à jour par
// semaine avec SDB_VERIFY_INTERVAL=168h suffit — la passe ne s'exécutait
// JAMAIS, sans que rien ne le signale.
func TestPeriodicTaskSurvivesRestarts(t *testing.T) {
	state := newMemMaintenanceState()
	// tâche censée tourner toutes les heures, jamais exécutée jusqu'ici
	sched := NewMaintenanceScheduler(state, discardLogger(), WithStartupGrace(10*time.Millisecond))

	// premier "démarrage" : la tâche n'a jamais tourné, elle doit partir vite
	ctx, cancel := context.WithCancel(context.Background())
	counter := newRunCounter()
	go sched.Run(ctx, "verification", time.Hour, counter.fn)
	select {
	case <-counter.first:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("une tâche jamais exécutée doit partir peu après le démarrage")
	}
	cancel()

	firstRun := state.get("verification")
	if firstRun.IsZero() {
		t.Fatal("la date du passage n'a pas été persistée : le redémarrage suivant repartirait de zéro")
	}

	// second "démarrage", juste après : l'échéance est encore loin, la passe
	// ne doit PAS repartir
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	counter2 := newRunCounter()
	go sched.Run(ctx2, "verification", time.Hour, counter2.fn)
	select {
	case <-counter2.first:
		t.Fatal("la tâche est repartie alors qu'elle venait de tourner : l'échéance n'a pas survécu au redémarrage")
	case <-time.After(300 * time.Millisecond):
	}
}

// Une échéance dépassée pendant un arrêt prolongé doit être rattrapée peu
// après le démarrage, pas au bout d'un intervalle complet de plus.
func TestOverdueTaskRunsShortlyAfterStartup(t *testing.T) {
	state := newMemMaintenanceState()
	state.set("integrity-check", time.Now().Add(-72*time.Hour))
	sched := NewMaintenanceScheduler(state, discardLogger(), WithStartupGrace(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counter := newRunCounter()
	go sched.Run(ctx, "integrity-check", 24*time.Hour, counter.fn)

	select {
	case <-counter.first:
	case <-time.After(3 * time.Second):
		t.Fatal("une échéance dépassée doit être rattrapée au démarrage")
	}
	// la date est écrite APRÈS le retour de la passe : on l'attend au lieu de
	// la lire dans la foulée
	if got := waitFreshRun(t, state, "integrity-check"); time.Since(got) > time.Minute {
		t.Fatalf("la date n'a pas été rafraîchie après le rattrapage : %s", got)
	}
}

// waitFreshRun : attend que la date du passage soit postérieure au démarrage
// du test, ou échoue.
func waitFreshRun(t *testing.T, state *memMaintenanceState, task string) time.Time {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := state.get(task); time.Since(got) < time.Minute {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("la date du passage de %s n'a jamais été rafraîchie", task)
	return time.Time{}
}

// Une passe en échec compte quand même comme un passage : sinon la boucle
// retenterait sans fin une vérification qui relit un dépôt entier, et un
// incident deviendrait une panne d'exploitation.
func TestFailedPassIsStillRecorded(t *testing.T) {
	state := newMemMaintenanceState()
	sched := NewMaintenanceScheduler(state, discardLogger(), WithStartupGrace(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counter := newRunCounter()
	counter.err = errors.New("repository unreachable")
	go sched.Run(ctx, "replication", time.Hour, counter.fn)

	select {
	case <-counter.first:
	case <-time.After(3 * time.Second):
		t.Fatal("la passe n'a pas démarré")
	}
	deadline := time.Now().Add(2 * time.Second)
	for state.get("replication").IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if state.get("replication").IsZero() {
		t.Fatal("une passe en échec doit être enregistrée, sinon elle repart en boucle")
	}
	if n := counter.runs(); n != 1 {
		t.Fatalf("%d exécutions dans la foulée, want 1 : pas de reprise immédiate après échec", n)
	}
}

// Une base illisible ne doit pas déclencher une relecture de tous les dépôts
// au démarrage : on retombe sur l'ancien comportement, bruyamment.
func TestUnreadableStateFallsBackToAFullInterval(t *testing.T) {
	state := newMemMaintenanceState()
	state.err = errors.New("database is locked")
	sched := NewMaintenanceScheduler(state, discardLogger(), WithStartupGrace(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counter := newRunCounter()
	go sched.Run(ctx, "verification", 2*time.Second, counter.fn)

	select {
	case <-counter.first:
		t.Fatal("la passe est partie immédiatement malgré une base illisible")
	case <-time.After(300 * time.Millisecond):
	}
}

// Le délai de grâce ne doit jamais dépasser l'intervalle demandé : régler une
// passe toutes les minutes et attendre cinq minutes serait l'inverse de ce
// qu'on promet. Repéré en exécution réelle — un intervalle de 3 min annonçait
// un premier passage dans 5 min.
func TestStartupGraceNeverExceedsTheInterval(t *testing.T) {
	state := newMemMaintenanceState()
	sched := NewMaintenanceScheduler(state, discardLogger(), WithStartupGrace(5*time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counter := newRunCounter()
	go sched.Run(ctx, "verification", 200*time.Millisecond, counter.fn)

	select {
	case <-counter.first:
	case <-time.After(3 * time.Second):
		t.Fatal("la grâce de démarrage a écrasé un intervalle plus court qu'elle")
	}
}

// Intervalle nul = tâche désactivée : la boucle ne doit pas tourner à vide.
func TestZeroIntervalDisablesTheTask(t *testing.T) {
	state := newMemMaintenanceState()
	sched := NewMaintenanceScheduler(state, discardLogger(), WithStartupGrace(time.Millisecond))

	done := make(chan struct{})
	counter := newRunCounter()
	go func() {
		sched.Run(context.Background(), "replication", 0, counter.fn)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() doit rendre la main immédiatement quand l'intervalle est nul")
	}
	if counter.runs() != 0 {
		t.Fatal("une tâche désactivée ne doit pas s'exécuter")
	}
}
