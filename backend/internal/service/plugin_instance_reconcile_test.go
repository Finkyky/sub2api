package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
)

type piReconciler interface {
	Reconcile(context.Context) error
	StartReconciler(context.Context, time.Duration, time.Duration) error
	StopReconciler()
}

func piReconcileAPI(t *testing.T, s *PluginInstanceService) piReconciler {
	t.Helper()
	r, ok := any(s).(piReconciler)
	require.True(t, ok, "PluginInstanceService must expose the generation reconciliation lifecycle")
	return r
}

type piReconcileRepo struct {
	*piMemoryRepo
	deleted      atomic.Bool
	failList     atomic.Bool
	failSnapshot atomic.Bool
	lists        atomic.Int32
	extra        []PluginInstance
}

func (r *piReconcileRepo) List(ctx context.Context, id int64) ([]PluginInstance, error) {
	r.lists.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.failList.Load() {
		return nil, errors.New("db read failure")
	}
	if r.deleted.Load() {
		return nil, nil
	}
	values, err := r.piMemoryRepo.List(ctx, id)
	return append(values, r.extra...), err
}
func (r *piReconcileRepo) Snapshot(ctx context.Context, id, revision int64) (*PluginInstanceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.deleted.Load() {
		return nil, ErrPluginInstanceNotFound
	}
	if r.failSnapshot.Load() {
		return nil, errors.New("snapshot read failure")
	}
	return r.piMemoryRepo.Snapshot(ctx, id, revision)
}

type piReconcileStarts struct {
	mu     sync.Mutex
	values []*piFakeRuntime
}

func (r *piReconcileStarts) at(index int) *piFakeRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.values[index]
}
func (r *piReconcileStarts) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.values) }
func piReconcileController() (*PluginInstanceService, *piReconcileRepo, *piReconcileStarts) {
	repo := &piReconcileRepo{piMemoryRepo: newPIMemoryRepo()}
	started := &piReconcileStarts{}
	s := newPluginInstanceService(repo, piTestCipher{}, func(ctx context.Context, snapshot *PluginInstanceSnapshot, config json.RawMessage) (pluginInstanceRuntime, error) {
		var value struct {
			Marker string `json:"marker"`
		}
		if err := json.Unmarshal(config, &value); err != nil {
			return nil, err
		}
		runtime := &piFakeRuntime{marker: value.Marker}
		started.mu.Lock()
		started.values = append(started.values, runtime)
		started.mu.Unlock()
		return runtime, nil
	})
	return s, repo, started
}
func piRemoteDraft(t *testing.T, r *piReconcileRepo) *PluginInstanceSnapshot {
	t.Helper()
	current, err := r.Snapshot(context.Background(), 11, 0)
	require.NoError(t, err)
	draft, err := r.SaveRevision(context.Background(), 11, current.Instance.Generation, `{"marker":"beta"}`)
	require.NoError(t, err)
	return draft
}
func piRemotePublish(t *testing.T, r *piReconcileRepo, revision int64, enabled bool) *PluginInstanceSnapshot {
	t.Helper()
	current, err := r.Snapshot(context.Background(), 11, 0)
	require.NoError(t, err)
	value, err := r.Publish(context.Background(), 11, current.Instance.Generation, revision, enabled)
	require.NoError(t, err)
	return value
}
func piHasCurrent(s *PluginInstanceService, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current[id] != nil
}

func TestPluginInstanceReconcileColdAndUnchangedDoNotStart(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	require.NoError(t, r.Reconcile(context.Background()))
	require.Zero(t, repo.lists.Load())
	require.Zero(t, started.count())
	lease, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer lease.Release()
	require.NoError(t, r.Reconcile(context.Background()))
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 1, started.count())
}
func TestPluginInstanceReconcileRemotePublicationDrainsOldLease(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer old.Release()
	draft := piRemoteDraft(t, repo)
	published := piRemotePublish(t, repo, draft.Revision.Revision, true)
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 2, started.count())
	require.Zero(t, started.at(0).closed.Load())
	fresh, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer fresh.Release()
	snapshot, err := fresh.Snapshot()
	require.NoError(t, err)
	require.Equal(t, published.Instance.Generation, snapshot.Instance.Generation)
	require.EqualValues(t, 2, snapshot.Revision.Revision)
	result, err := old.Check(context.Background(), 1, contract.HookRequest{GroupID: 7, Stage: contract.Input})
	require.NoError(t, err)
	require.Equal(t, "alpha", string(result.Content.Data))
	old.Release()
	require.EqualValues(t, 1, started.at(0).closed.Load())
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 2, started.count())
}
func TestPluginInstanceReconcileSameRevisionGenerationAndDraftIsolation(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	old.Release()
	piRemoteDraft(t, repo)
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 2, started.count())
	require.Equal(t, "alpha", started.at(1).marker, "never start an unpublished draft")
	piRemotePublish(t, repo, 1, false)
	piRemotePublish(t, repo, 1, true)
	fresh, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	fresh.Release()
	require.Equal(t, 3, started.count(), "Acquire must observe same-revision generation changes too")
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 3, started.count())
}
func TestPluginInstanceReconcileRemoteDisableAndDelete(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer old.Release()
	piRemotePublish(t, repo, 1, false)
	require.NoError(t, r.Reconcile(context.Background()))
	require.False(t, piHasCurrent(s, 11))
	require.Zero(t, started.at(0).closed.Load())
	_, err = s.Acquire(context.Background(), 11)
	require.ErrorIs(t, err, ErrPluginInstanceUnavailable)
	s.recordInstanceError(11, "prior_error")
	repo.deleted.Store(true)
	require.NoError(t, r.Reconcile(context.Background()))
	s.mu.Lock()
	require.Empty(t, s.lastErrors)
	s.mu.Unlock()
	s.recordRuntimeCallFailure(old.handle)
	s.mu.Lock()
	require.Empty(t, s.lastErrors, "deleted history must not be restored by old leases")
	s.mu.Unlock()
	old.Release()
	require.EqualValues(t, 1, started.at(0).closed.Load())
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 1, started.count())
}
func TestPluginInstanceReconcileReadFailuresDoNotInventDeletion(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer old.Release()
	repo.failList.Store(true)
	require.Error(t, r.Reconcile(context.Background()))
	require.True(t, piHasCurrent(s, 11))
	require.Zero(t, started.at(0).closed.Load())
	repo.failList.Store(false)
	piRemoteDraft(t, repo)
	piRemotePublish(t, repo, 2, true)
	repo.failSnapshot.Store(true)
	require.Error(t, r.Reconcile(context.Background()))
	require.False(t, piHasCurrent(s, 11))
	require.Equal(t, 1, started.count())
	repo.failSnapshot.Store(false)
	old.Release()
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 2, started.count(), "pending intent survives release of the last stale lease")
}
func TestPluginInstanceReconcileFailedCandidateRetriesWithoutDBMutation(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer old.Release()
	piRemoteDraft(t, repo)
	published := piRemotePublish(t, repo, 2, true)
	start := s.start
	s.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
		return nil, ErrGeneralHandshake
	}
	require.Error(t, r.Reconcile(context.Background()))
	require.False(t, piHasCurrent(s, 11))
	snapshot, err := repo.Snapshot(context.Background(), 11, 0)
	require.NoError(t, err)
	require.Equal(t, published.Instance.Generation, snapshot.Instance.Generation)
	status, err := s.Status(context.Background(), 11)
	require.NoError(t, err)
	require.Equal(t, "runtime_start_failed", status.LastError)
	old.Release()
	s.start = start
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 2, started.count())
	require.True(t, piHasCurrent(s, 11))
}
func TestPluginInstanceReconcileRejectsCandidateChangedDuringStart(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer old.Release()
	piRemoteDraft(t, repo)
	piRemotePublish(t, repo, 2, true)
	start := s.start
	s.start = func(ctx context.Context, snap *PluginInstanceSnapshot, cfg json.RawMessage) (pluginInstanceRuntime, error) {
		candidate, err := start(ctx, snap, cfg)
		piRemotePublish(t, repo, 2, false)
		return candidate, err
	}
	require.Error(t, r.Reconcile(context.Background()))
	require.False(t, piHasCurrent(s, 11))
	require.EqualValues(t, 1, started.at(1).closed.Load())
	require.Zero(t, started.at(0).closed.Load())
	require.NoError(t, r.Reconcile(context.Background()))
	require.Equal(t, 2, started.count())
}
func TestPluginInstanceReconcileParentCancellationDoesNotInstallOrOverwriteHistory(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer old.Release()
	piRemoteDraft(t, repo)
	piRemotePublish(t, repo, 2, true)
	s.recordInstanceError(11, "prior_error")
	start := s.start
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.start = func(ctx context.Context, snap *PluginInstanceSnapshot, cfg json.RawMessage) (pluginInstanceRuntime, error) {
		candidate, err := start(ctx, snap, cfg)
		cancel()
		return candidate, err
	}
	require.ErrorIs(t, r.Reconcile(ctx), context.Canceled)
	require.False(t, piHasCurrent(s, 11))
	require.EqualValues(t, 1, started.at(1).closed.Load())
	s.mu.Lock()
	require.Equal(t, "prior_error", s.lastErrors[11])
	s.mu.Unlock()
}
func TestPluginInstanceReconcileDisablePhasePrecedesSlowCandidate(t *testing.T) {
	s, repo, _ := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer old.Release()
	disabledRuntime := &piFakeRuntime{}
	h := &managedPluginInstance{runtime: disabledRuntime, instanceID: 12, revision: 1}
	s.mu.Lock()
	s.current[12] = h
	s.all[h] = true
	s.mu.Unlock()
	repo.extra = []PluginInstance{{ID: 12, Generation: 2, CurrentRevision: 1, Enabled: false}}
	piRemoteDraft(t, repo)
	piRemotePublish(t, repo, 2, true)
	entered, release := make(chan struct{}), make(chan struct{})
	start := s.start
	s.start = func(ctx context.Context, snap *PluginInstanceSnapshot, cfg json.RawMessage) (pluginInstanceRuntime, error) {
		close(entered)
		select {
		case <-release:
			return start(ctx, snap, cfg)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- r.Reconcile(context.Background()) }()
	<-entered
	require.EqualValues(t, 1, disabledRuntime.closed.Load())
	close(release)
	require.NoError(t, <-done)
}
func TestPluginInstanceReconcileLoopPropagationStopRestartAndColdNode(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interval, budget := 10*time.Millisecond, 100*time.Millisecond
	require.NoError(t, r.StartReconciler(ctx, interval, budget))
	time.Sleep(25 * time.Millisecond)
	require.Zero(t, repo.lists.Load())
	old, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer old.Release()
	require.NoError(t, r.StartReconciler(ctx, interval, budget))
	piRemotePublish(t, repo, 1, false)
	began := time.Now()
	require.Eventually(t, func() bool { return !piHasCurrent(s, 11) }, interval+2*budget+100*time.Millisecond, time.Millisecond)
	t.Logf("STOP_PROPAGATION elapsed=%s bound=%s scheduler_allowance=100ms", time.Since(began), interval+2*budget)
	require.Zero(t, started.at(0).closed.Load())
	r.StopReconciler()
	r.StopReconciler()
	calls := repo.lists.Load()
	time.Sleep(25 * time.Millisecond)
	require.Equal(t, calls, repo.lists.Load())
	require.NoError(t, r.StartReconciler(ctx, interval, budget))
	r.StopReconciler()
	old.Release()
	require.EqualValues(t, 1, started.at(0).closed.Load())
}
func TestPluginInstanceReconcileCloseCancelsGateHoldingCandidate(t *testing.T) {
	s, repo, started := piReconcileController()
	r := piReconcileAPI(t, s)
	lease, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer lease.Release()
	piRemoteDraft(t, repo)
	piRemotePublish(t, repo, 2, true)
	entered := make(chan struct{})
	s.start = func(ctx context.Context, _ *PluginInstanceSnapshot, _ json.RawMessage) (pluginInstanceRuntime, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	require.NoError(t, r.StartReconciler(context.Background(), time.Millisecond, time.Hour))
	<-entered
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked with reconciliation gate")
	}
	require.EqualValues(t, 1, started.at(0).closed.Load())
	require.ErrorIs(t, r.Reconcile(context.Background()), ErrGeneralClosed)
	require.ErrorIs(t, r.StartReconciler(context.Background(), time.Millisecond, time.Second), ErrGeneralClosed)
	s.Close()
}
func TestPluginInstanceReconcileValidationAndProviderStartup(t *testing.T) {
	s, _, _ := piReconcileController()
	defer s.Close()
	r := piReconcileAPI(t, s)
	for _, durations := range [][2]time.Duration{{0, time.Second}, {time.Second, 0}, {-time.Second, time.Second}} {
		require.ErrorIs(t, r.StartReconciler(context.Background(), durations[0], durations[1]), ErrPluginInstanceInvalid)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, r.StartReconciler(ctx, time.Second, time.Second), context.Canceled)
	manager := NewPluginManager(nil, nil, &config.Config{}, PluginHostInfo{}, nil)
	provider := ProvidePluginInstanceService(newPIMemoryRepo(), piTestCipher{}, manager)
	defer provider.Close()
	field := reflect.ValueOf(provider).Elem().FieldByName("reconcileDone")
	require.True(t, field.IsValid())
	require.NotZero(t, field.Pointer(), "production provider must start the loop")
}

type piReconcilePairRepo struct {
	*piMemoryRepo
	second *piMemoryRepo
}

func (r *piReconcilePairRepo) List(ctx context.Context, id int64) ([]PluginInstance, error) {
	first, err := r.piMemoryRepo.List(ctx, id)
	if err != nil {
		return nil, err
	}
	next, err := r.second.List(ctx, id)
	return append(first, next...), err
}
func (r *piReconcilePairRepo) Snapshot(ctx context.Context, id, revision int64) (*PluginInstanceSnapshot, error) {
	if id != 12 {
		return r.piMemoryRepo.Snapshot(ctx, id, revision)
	}
	snap, err := r.second.Snapshot(ctx, 11, revision)
	if err != nil {
		return nil, err
	}
	snap.Instance.ID = 12
	snap.Revision.InstanceID = 12
	for i := range snap.Bindings {
		snap.Bindings[i].InstanceID = 12
	}
	return snap, nil
}
func TestPluginInstanceReconcileFairRotationAfterBudgetExhaustion(t *testing.T) {
	ctx := context.Background()
	a, b := newPIMemoryRepo(), newPIMemoryRepo()
	b.s.Instance.ID = 12
	repo := &piReconcilePairRepo{piMemoryRepo: a, second: b}
	slow := false
	s := newPluginInstanceService(repo, piTestCipher{}, func(ctx context.Context, snapshot *PluginInstanceSnapshot, _ json.RawMessage) (pluginInstanceRuntime, error) {
		if slow && snapshot.Instance.ID == 11 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &piFakeRuntime{}, nil
	})
	defer s.Close()
	first, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	defer first.Release()
	second, err := s.Acquire(ctx, 12)
	require.NoError(t, err)
	defer second.Release()
	a.s.Instance.Generation++
	b.s.Instance.Generation++
	slow = true
	for i := 0; i < 2; i++ {
		cycle, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		require.ErrorIs(t, s.Reconcile(cycle), context.DeadlineExceeded)
		cancel()
		if i == 0 {
			require.False(t, piHasCurrent(s, 12))
		}
	}
	require.True(t, piHasCurrent(s, 12), "deadline-consuming instance must not starve next candidate")
	require.False(t, piHasCurrent(s, 11))
}
func TestPluginInstanceReconcileCandidateSecondReadFailures(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "read_failure", true: "deleted"}[deleted], func(t *testing.T) {
			s, repo, started := piReconcileController()
			defer s.Close()
			lease, err := s.Acquire(context.Background(), 11)
			require.NoError(t, err)
			defer lease.Release()
			piRemotePublish(t, repo, 1, true)
			start := s.start
			s.start = func(ctx context.Context, snapshot *PluginInstanceSnapshot, cfg json.RawMessage) (pluginInstanceRuntime, error) {
				runtime, err := start(ctx, snapshot, cfg)
				if deleted {
					repo.deleted.Store(true)
				} else {
					repo.failSnapshot.Store(true)
				}
				return runtime, err
			}
			require.Error(t, s.Reconcile(context.Background()))
			require.False(t, piHasCurrent(s, 11))
			require.EqualValues(t, 1, started.at(1).closed.Load())
			s.mu.Lock()
			pending := s.reconcilePending[11]
			s.mu.Unlock()
			require.Equal(t, !deleted, pending)
			if !deleted {
				repo.failSnapshot.Store(false)
				s.start = start
				require.NoError(t, s.Reconcile(context.Background()))
				require.True(t, piHasCurrent(s, 11))
			}
		})
	}
}
func TestPluginInstanceReconcileRejectsMalformedListBeforeRetiring(t *testing.T) {
	for _, value := range []PluginInstance{{ID: 11, Generation: 2, CurrentRevision: 1}, {ID: 12, Generation: 0, CurrentRevision: 1}, {ID: 12, Generation: 1, CurrentRevision: 0}, {ID: -1, Generation: 1, CurrentRevision: 1}} {
		t.Run(fmt.Sprint(value.ID, "_", value.Generation, "_", value.CurrentRevision), func(t *testing.T) {
			s, repo, started := piReconcileController()
			defer s.Close()
			lease, err := s.Acquire(context.Background(), 11)
			require.NoError(t, err)
			defer lease.Release()
			repo.extra = []PluginInstance{value}
			require.ErrorIs(t, s.Reconcile(context.Background()), ErrPluginInstanceInvalid)
			require.True(t, piHasCurrent(s, 11))
			require.Equal(t, 1, started.count())
			require.Zero(t, started.at(0).closed.Load())
		})
	}
}
func TestPluginInstanceReconcileRuntimeAndErrorClosesCandidate(t *testing.T) {
	s, repo, _ := piReconcileController()
	defer s.Close()
	lease, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer lease.Release()
	piRemotePublish(t, repo, 1, true)
	candidate := &piFakeRuntime{}
	s.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
		return candidate, errors.New("failed handshake")
	}
	require.ErrorIs(t, s.Reconcile(context.Background()), ErrPluginInstanceUnavailable)
	require.EqualValues(t, 1, candidate.closed.Load())
}
func TestPluginInstanceReconcileConcurrentLifecycle(t *testing.T) {
	s, _, started := piReconcileController()
	lease, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 10; j++ {
				err := s.StartReconciler(context.Background(), time.Millisecond, 50*time.Millisecond)
				if err != nil && !errors.Is(err, ErrGeneralClosed) {
					t.Errorf("start: %v", err)
				}
				s.StopReconciler()
			}
		}()
	}
	workers.Add(2)
	go func() { defer workers.Done(); s.Close() }()
	go func() { defer workers.Done(); lease.Release() }()
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle deadlock")
	}
	s.Close()
	require.EqualValues(t, 1, started.at(0).closed.Load())
}

type piReconcileBudgetObservation struct {
	at       time.Time
	deadline time.Time
}
type piReconcileProviderRepo struct {
	*piMemoryRepo
	seen chan piReconcileBudgetObservation
}

func (r *piReconcileProviderRepo) List(ctx context.Context, id int64) ([]PluginInstance, error) {
	deadline, _ := ctx.Deadline()
	select {
	case r.seen <- piReconcileBudgetObservation{time.Now(), deadline}:
	default:
	}
	return nil, errors.New("database observation only")
}
func TestPluginInstanceReconcileProviderUsesConfiguredIntervalAndBudget(t *testing.T) {
	repo := &piReconcileProviderRepo{piMemoryRepo: newPIMemoryRepo(), seen: make(chan piReconcileBudgetObservation, 5)}
	cfg := &config.Config{Plugins: config.PluginConfig{InstanceReconcileIntervalSeconds: 1, InstanceReconcileTimeoutSeconds: 2}}
	manager := NewPluginManager(nil, nil, cfg, PluginHostInfo{}, nil)
	s := ProvidePluginInstanceService(repo, piTestCipher{}, manager)
	defer s.Close()
	s.install(11, 1, 1, &piFakeRuntime{})
	receive := func() piReconcileBudgetObservation {
		t.Helper()
		select {
		case value := <-repo.seen:
			return value
		case <-time.After(2500 * time.Millisecond):
			t.Fatal("provider did not use configured one-second interval")
			return piReconcileBudgetObservation{}
		}
	}
	first, second := receive(), receive()
	require.InDelta(t, 2*time.Second, first.deadline.Sub(first.at), float64(100*time.Millisecond))
	require.InDelta(t, time.Second, second.at.Sub(first.at), float64(300*time.Millisecond))
}
func TestPluginInstanceReconcileSupersededLeaseCannotOverwriteReplacementFailure(t *testing.T) {
	repo := &piReconcileRepo{piMemoryRepo: newPIMemoryRepo()}
	s := newPluginInstanceService(repo, piTestCipher{}, func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
		return &piStatusFailingRuntime{}, nil
	})
	defer s.Close()
	lease, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer lease.Release()
	piRemotePublish(t, repo, 1, true)
	s.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
		return nil, errors.New("replacement failed")
	}
	require.Error(t, s.Reconcile(context.Background()))
	_, err = lease.RunTask(context.Background(), "sync_prices", "job", json.RawMessage(`{}`))
	require.Error(t, err)
	lease.Release()
	status, err := s.Status(context.Background(), 11)
	require.NoError(t, err)
	require.Equal(t, "runtime_start_failed", status.LastError)
}
func TestPluginInstanceReconcileRetriesWarmAcquireReplacementFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint("cancelled_", cancelled), func(t *testing.T) {
			s, repo, started := piReconcileController()
			defer s.Close()
			lease, err := s.Acquire(context.Background(), 11)
			require.NoError(t, err)
			defer lease.Release()
			piRemotePublish(t, repo, 1, true)
			start := s.start
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
				if cancelled {
					cancel()
					return nil, ctx.Err()
				}
				return nil, errors.New("node temporarily unavailable")
			}
			_, err = s.Acquire(ctx, 11)
			require.Error(t, err)
			lease.Release()
			s.start = start
			require.NoError(t, s.Reconcile(context.Background()))
			require.True(t, piHasCurrent(s, 11), "warm acquisition failure must preserve retry intent without a lease")
			require.Equal(t, 2, started.count())
		})
	}
	t.Run("cold_failed_acquisition_stays_lazy", func(t *testing.T) {
		s, _, _ := piReconcileController()
		defer s.Close()
		attempts := 0
		s.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
			attempts++
			return nil, errors.New("startup failure")
		}
		_, err := s.Acquire(context.Background(), 11)
		require.Error(t, err)
		require.NoError(t, s.Reconcile(context.Background()))
		require.Equal(t, 1, attempts)
	})
}
func TestPluginInstanceReconcileRetainsObservedCrashOnCandidateReadFailure(t *testing.T) {
	s, repo, started := piReconcileController()
	defer s.Close()
	lease, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer lease.Release()
	started.at(0).closed.Store(1) // Simulates an exited runtime, not service cleanup.
	repo.failSnapshot.Store(true)
	require.Error(t, s.Reconcile(context.Background()))
	lease.Release()
	s.mu.Lock()
	history := s.lastErrors[11]
	s.mu.Unlock()
	require.Equal(t, "runtime_unavailable", history, "same-generation crash must be observed before revoking old error authority")
	repo.failSnapshot.Store(false)
	require.NoError(t, s.Reconcile(context.Background()))
	status, err := s.Status(context.Background(), 11)
	require.NoError(t, err)
	require.Empty(t, status.LastError)
}

type piReconcileSlowCloseRuntime struct {
	piFakeRuntime
	entered chan struct{}
	release chan struct{}
}

func (r *piReconcileSlowCloseRuntime) Close() { close(r.entered); <-r.release; r.piFakeRuntime.Close() }
func TestPluginInstanceReconcileRevokesAllBeforeSlowRuntimeClose(t *testing.T) {
	s, repo, _ := piReconcileController()
	slow := &piReconcileSlowCloseRuntime{entered: make(chan struct{}), release: make(chan struct{})}
	next := &piFakeRuntime{}
	s.install(11, 1, 1, slow)
	s.install(12, 1, 1, next)
	piRemotePublish(t, repo, 1, false)
	repo.extra = []PluginInstance{{ID: 12, Generation: 2, CurrentRevision: 1, Enabled: false}}
	require.NoError(t, s.StartReconciler(context.Background(), time.Millisecond, time.Second))
	<-slow.entered
	stopDone, closeDone := make(chan struct{}), make(chan struct{})
	go func() { s.StopReconciler(); close(stopDone) }()
	go func() { s.Close(); close(closeDone) }()
	defer func() {
		close(slow.release)
		<-stopDone
		<-closeDone
		s.Close()
		require.EqualValues(t, 1, slow.closed.Load())
		require.EqualValues(t, 1, next.closed.Load())
	}()
	require.Eventually(t, func() bool { return !piHasCurrent(s, 12) }, 100*time.Millisecond, time.Millisecond, "all state must revoke before any synchronous Close")
	select {
	case <-stopDone:
		t.Fatal("Stop returned before cleanup finished")
	default:
	}
	select {
	case <-closeDone:
		t.Fatal("Close returned before cleanup finished")
	default:
	}
}
