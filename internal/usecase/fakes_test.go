package usecase

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// ---------------------------------------------------------------------------
// fake domain.ContainerRuntime
// ---------------------------------------------------------------------------

type fakeRuntime struct {
	mu        sync.Mutex
	container *domain.Container
	stopErr   error
	startErr  error
	stops     []string
	starts    []string
	execs     [][]string
	execFn    func(cmd []string) (*domain.ExecResult, error)

	removedVolumes []string
}

func (f *fakeRuntime) Ping(context.Context) error { return nil }

func (f *fakeRuntime) List(context.Context, bool) ([]domain.Container, error) {
	if f.container == nil {
		return nil, nil
	}
	return []domain.Container{*f.container}, nil
}

func (f *fakeRuntime) Get(_ context.Context, id string) (*domain.Container, error) {
	// docker inspect resout ID et nom : le fake fait pareil
	if f.container == nil || (f.container.ID != id && f.container.Name != id) {
		return nil, domain.ErrNotFound
	}
	c := *f.container
	return &c, nil
}

func (f *fakeRuntime) Stop(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stops = append(f.stops, id)
	return nil
}

func (f *fakeRuntime) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.starts = append(f.starts, id)
	return nil
}

func (f *fakeRuntime) Exec(_ context.Context, _ string, cmd []string, _ time.Duration) (*domain.ExecResult, error) {
	f.mu.Lock()
	f.execs = append(f.execs, cmd)
	fn := f.execFn
	f.mu.Unlock()
	if fn != nil {
		return fn(cmd)
	}
	return &domain.ExecResult{ExitCode: 0}, nil
}

func (f *fakeRuntime) RunWorker(context.Context, domain.WorkerSpec, io.Writer, io.Writer) (int, error) {
	return 0, nil
}

func (f *fakeRuntime) RemoveVolume(_ context.Context, name string) error {
	// meme garde-fou que l'implementation Docker : un test qui tenterait de
	// supprimer un volume de production doit echouer ici aussi
	if !domain.IsScratchVolume(name) {
		return domain.ErrForbidden
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedVolumes = append(f.removedVolumes, name)
	return nil
}

func (f *fakeRuntime) removed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removedVolumes...)
}

func (f *fakeRuntime) stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stops...)
}

func (f *fakeRuntime) started() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.starts...)
}

// ---------------------------------------------------------------------------
// fake domain.SnapshotEngine
// ---------------------------------------------------------------------------

type fakeEngine struct {
	mu          sync.Mutex
	ensureErr   error
	backupErr   error
	restoreErr  error
	checkErr    error
	summary     *domain.BackupSummary
	backupFn    func(ctx context.Context) error // blocage optionnel (tests de concurrence)
	restoreFn   func(ctx context.Context) error
	ensureCalls int
	backupCalls int
	checkCalls  int
	forgets      []domain.RetentionPolicy
	restores     []string
	snapshots    []domain.Snapshot
	snapshotsErr error
}

func (f *fakeEngine) EnsureRepository(context.Context, *domain.StorageConfig) error {
	f.mu.Lock()
	f.ensureCalls++
	f.mu.Unlock()
	return f.ensureErr
}

func (f *fakeEngine) Backup(ctx context.Context, _ *domain.StorageConfig, _ int64,
	_ []domain.Mount, _ []string, _ chan<- domain.ProgressEvent) (*domain.BackupSummary, error) {
	f.mu.Lock()
	f.backupCalls++
	fn := f.backupFn
	f.mu.Unlock()
	if fn != nil {
		if err := fn(ctx); err != nil {
			return f.summary, err
		}
	}
	return f.summary, f.backupErr
}

func (f *fakeEngine) Restore(ctx context.Context, _ *domain.StorageConfig, spec domain.RestoreSpec, _ chan<- domain.ProgressEvent) error {
	f.mu.Lock()
	// trace "snap->cible" en place, "snap:source->cible" en clonage : les
	// tests doivent voir que la source a bien ete transmise au moteur
	trace := spec.SnapshotID + "->" + spec.TargetVolume
	if spec.SourceVolume != "" && spec.SourceVolume != spec.TargetVolume {
		trace = spec.SnapshotID + ":" + spec.SourceVolume + "->" + spec.TargetVolume
	}
	if spec.Verify {
		trace += "#verify"
	}
	f.restores = append(f.restores, trace)
	fn := f.restoreFn
	f.mu.Unlock()
	if fn != nil {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	return f.restoreErr
}

func (f *fakeEngine) Snapshots(context.Context, *domain.StorageConfig, []string) ([]domain.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Snapshot(nil), f.snapshots...), f.snapshotsErr
}

func (f *fakeEngine) Forget(_ context.Context, _ *domain.StorageConfig, p domain.RetentionPolicy) error {
	f.mu.Lock()
	f.forgets = append(f.forgets, p)
	f.mu.Unlock()
	return nil
}

func (f *fakeEngine) Check(context.Context, *domain.StorageConfig) error {
	f.mu.Lock()
	f.checkCalls++
	f.mu.Unlock()
	return f.checkErr
}

func (f *fakeEngine) backups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.backupCalls
}

func (f *fakeEngine) forgotten() []domain.RetentionPolicy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.RetentionPolicy(nil), f.forgets...)
}

// ---------------------------------------------------------------------------
// fake domain.BackupHistoryRepository
// ---------------------------------------------------------------------------

type memHistory struct {
	mu   sync.Mutex
	seq  int64
	recs map[int64]domain.BackupRecord
}

func newMemHistory() *memHistory { return &memHistory{recs: map[int64]domain.BackupRecord{}} }

func (m *memHistory) Create(_ context.Context, rec *domain.BackupRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	rec.ID = m.seq
	m.recs[rec.ID] = *rec
	return nil
}

func (m *memHistory) Update(_ context.Context, rec *domain.BackupRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.recs[rec.ID]; !ok {
		return domain.ErrNotFound
	}
	m.recs[rec.ID] = *rec
	return nil
}

func (m *memHistory) GetByID(_ context.Context, id int64) (*domain.BackupRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.recs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := rec
	return &out, nil
}

func (m *memHistory) List(context.Context, domain.HistoryFilter) ([]domain.BackupRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.BackupRecord, 0, len(m.recs))
	for _, rec := range m.recs {
		out = append(out, rec)
	}
	return out, nil
}

func (m *memHistory) FailInterrupted(context.Context, string) (int64, error) { return 0, nil }

// ---------------------------------------------------------------------------
// fake domain.StorageRepository
// ---------------------------------------------------------------------------

type memStorages struct {
	mu   sync.Mutex
	seq  int64
	cfgs map[int64]domain.StorageConfig
}

func newMemStorages() *memStorages { return &memStorages{cfgs: map[int64]domain.StorageConfig{}} }

func (m *memStorages) Create(_ context.Context, cfg *domain.StorageConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	cfg.ID = m.seq
	m.cfgs[cfg.ID] = *cfg
	return nil
}

func (m *memStorages) GetByID(_ context.Context, id int64) (*domain.StorageConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.cfgs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := cfg
	return &out, nil
}

func (m *memStorages) List(context.Context) ([]domain.StorageConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.StorageConfig, 0, len(m.cfgs))
	for _, cfg := range m.cfgs {
		out = append(out, cfg)
	}
	return out, nil
}

func (m *memStorages) Update(_ context.Context, cfg *domain.StorageConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cfgs[cfg.ID]; !ok {
		return domain.ErrNotFound
	}
	m.cfgs[cfg.ID] = *cfg
	return nil
}

func (m *memStorages) Delete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cfgs[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.cfgs, id)
	return nil
}

// ---------------------------------------------------------------------------
// fake domain.RestoreHistoryRepository
// ---------------------------------------------------------------------------

type memRestores struct {
	mu   sync.Mutex
	seq  int64
	recs map[int64]domain.RestoreRecord
}

func newMemRestores() *memRestores { return &memRestores{recs: map[int64]domain.RestoreRecord{}} }

func (m *memRestores) Create(_ context.Context, rec *domain.RestoreRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	rec.ID = m.seq
	m.recs[rec.ID] = *rec
	return nil
}

func (m *memRestores) Update(_ context.Context, rec *domain.RestoreRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.recs[rec.ID]; !ok {
		return domain.ErrNotFound
	}
	m.recs[rec.ID] = *rec
	return nil
}

func (m *memRestores) GetByID(_ context.Context, id int64) (*domain.RestoreRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.recs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := rec
	return &out, nil
}

func (m *memRestores) List(context.Context, domain.RestoreFilter) ([]domain.RestoreRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.RestoreRecord, 0, len(m.recs))
	for _, rec := range m.recs {
		out = append(out, rec)
	}
	return out, nil
}

func (m *memRestores) FailInterrupted(context.Context, string) (int64, error) { return 0, nil }

// ---------------------------------------------------------------------------
// fake domain.ScheduleRepository
// ---------------------------------------------------------------------------

type memSchedules struct {
	mu   sync.Mutex
	seq  int64
	byID map[int64]domain.BackupSchedule
}

func newMemSchedules() *memSchedules { return &memSchedules{byID: map[int64]domain.BackupSchedule{}} }

func (m *memSchedules) Create(_ context.Context, s *domain.BackupSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	s.ID = m.seq
	m.byID[s.ID] = *s
	return nil
}

func (m *memSchedules) GetByID(_ context.Context, id int64) (*domain.BackupSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := s
	return &out, nil
}

func (m *memSchedules) List(context.Context) ([]domain.BackupSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.BackupSchedule, 0, len(m.byID))
	for _, s := range m.byID {
		out = append(out, s)
	}
	return out, nil
}

func (m *memSchedules) Update(_ context.Context, s *domain.BackupSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[s.ID]; !ok {
		return domain.ErrNotFound
	}
	m.byID[s.ID] = *s
	return nil
}

func (m *memSchedules) Delete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.byID, id)
	return nil
}

func (m *memSchedules) TouchLastRun(_ context.Context, id int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	s.LastRunAt = &at
	m.byID[id] = s
	return nil
}

// ---------------------------------------------------------------------------
// fake domain.UserRepository
// ---------------------------------------------------------------------------

type memUsers struct {
	mu   sync.Mutex
	seq  int64
	byID map[int64]domain.User
}

func newMemUsers() *memUsers { return &memUsers{byID: map[int64]domain.User{}} }

func (m *memUsers) Create(_ context.Context, u *domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.byID {
		if existing.Username == u.Username {
			return fmt.Errorf("%w: username %q", domain.ErrAlreadyExists, u.Username)
		}
	}
	m.seq++
	u.ID = m.seq
	m.byID[u.ID] = *u
	return nil
}

func (m *memUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := u
	return &out, nil
}

func (m *memUsers) GetByUsername(_ context.Context, username string) (*domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.byID {
		if u.Username == username {
			out := u
			return &out, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *memUsers) List(context.Context) ([]domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.User, 0, len(m.byID))
	for _, u := range m.byID {
		out = append(out, u)
	}
	return out, nil
}

func (m *memUsers) Update(_ context.Context, u *domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[u.ID]; !ok {
		return domain.ErrNotFound
	}
	m.byID[u.ID] = *u
	return nil
}

func (m *memUsers) Delete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.byID, id)
	return nil
}

func (m *memUsers) TokenVersion(_ context.Context, id int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return 0, domain.ErrNotFound
	}
	return u.TokenVersion, nil
}

func (m *memUsers) Count(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.byID)), nil
}

// ---------------------------------------------------------------------------
// fake domain.EventPublisher et helpers partages
// ---------------------------------------------------------------------------

type capturePublisher struct {
	mu     sync.Mutex
	events []domain.ProgressEvent
}

func (p *capturePublisher) Publish(ev domain.ProgressEvent) {
	p.mu.Lock()
	p.events = append(p.events, ev)
	p.mu.Unlock()
}

func (p *capturePublisher) all() []domain.ProgressEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.ProgressEvent(nil), p.events...)
}

// fakeHasher : evite le cout Argon2id dans les tests.
type fakeHasher struct{}

func (fakeHasher) Hash(password string) (string, error) { return "hash:" + password, nil }

func (fakeHasher) Verify(password, encoded string) (bool, error) {
	return encoded == "hash:"+password, nil
}
