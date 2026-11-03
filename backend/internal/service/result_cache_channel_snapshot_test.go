package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type rcChannelAuthorityRepo struct {
	ChannelRepository
	mu               sync.Mutex
	channels         []Channel
	policy           ResultCachePolicy
	err              error
	calls            int
	entered, release chan struct{}
	enterOnce        sync.Once
}

func (r *rcChannelAuthorityRepo) LoadResultCacheSnapshot(ctx context.Context, _ int64) ([]Channel, map[int64]string, ResultCachePolicy, error) {
	if r.entered != nil {
		r.enterOnce.Do(func() { close(r.entered) })
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, nil, ResultCachePolicy{}, ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	b, _ := json.Marshal(r.channels)
	var channels []Channel
	_ = json.Unmarshal(b, &channels)
	return channels, map[int64]string{1: PlatformOpenAI}, r.policy, r.err
}
func rcChannelAuthorityFixture(t *testing.T) (*ResultCacheService, *rcRequestPolicyRepo, *ChannelService, *rcChannelAuthorityRepo, *channelCache) {
	t.Helper()
	cache, policy, _ := rcRequestPolicyService(t)
	price := 2.0
	repo := &rcChannelAuthorityRepo{channels: []Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{1}, RestrictModels: true, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"m": "new"}}, ModelPricing: []ChannelModelPricing{{ID: 10, Platform: PlatformOpenAI, Models: []string{"new"}, InputPrice: &price}}}}, policy: policy.p}
	svc := NewChannelService(repo, nil, nil, nil, nil)
	old := populateChannelCache([]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{1}, ModelMapping: map[string]map[string]string{PlatformOpenAI: {"m": "old"}}}}, map[int64]string{1: PlatformOpenAI})
	svc.cache.Store(old)
	return cache, policy, svc, repo, old
}
func TestResultCacheChannelSnapshotReuse(t *testing.T) {
	s, _, channel, repo, old := rcChannelAuthorityFixture(t)
	ctx := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
	require.Equal(t, "new", channel.ResolveChannelMapping(ctx, 1, "m").MappedModel)
	require.False(t, channel.IsModelRestricted(ctx, 1, "new"))
	require.True(t, channel.IsModelRestricted(ctx, 1, "not-priced"))
	price := channel.GetChannelModelPricing(ctx, 1, "new")
	require.NotNil(t, price)
	require.Equal(t, 2.0, *price.InputPrice)
	ch, err := channel.GetChannelForGroup(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, BillingModelSourceChannelMapped, ch.BillingModelSource)
	// Returned clones cannot mutate the request snapshot.
	ch.ModelMapping[PlatformOpenAI]["m"] = "mutated"
	*price.InputPrice = 999
	require.Equal(t, "new", channel.ResolveChannelMapping(ctx, 1, "m").MappedModel)
	require.Equal(t, 2.0, *channel.GetChannelModelPricing(ctx, 1, "new").InputPrice)
	require.Same(t, old, channel.cache.Load())
	require.Equal(t, "old", channel.ResolveChannelMapping(context.Background(), 1, "m").MappedModel)
	repo.mu.Lock()
	require.Equal(t, 1, repo.calls)
	repo.mu.Unlock()
}
func TestResultCacheChannelSnapshotStickyFailure(t *testing.T) {
	for _, kind := range []string{"error", "version", "inactive", "disabled", "empty", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			s, _, ch, repo, old := rcChannelAuthorityFixture(t)
			reason := "policy_changed"
			switch kind {
			case "error":
				repo.err = errors.New("snapshot DB error")
				reason = "semantic_snapshot_error"
			case "version":
				repo.policy.Version = "request-v2"
			case "inactive":
				repo.policy.Active = false
			case "disabled":
				repo.policy.Config.Enabled = false
			case "empty":
				repo.policy.Version = ""
			case "unsupported":
				ch.repo = nil
				reason = "semantic_snapshot_unavailable"
			}
			ctx := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
			require.Equal(t, "old", ch.ResolveChannelMapping(ctx, 1, "m").MappedModel)
			repo.mu.Lock()
			repo.err = nil
			repo.policy.Version = "request-v1"
			repo.policy.Active = true
			repo.policy.Config.Enabled = true
			repo.mu.Unlock()
			if kind == "unsupported" {
				ch.repo = repo
			}
			// Recovery cannot replace the already-used failure within the same request.
			require.Equal(t, "old", ch.ResolveChannelMapping(ctx, 1, "m").MappedModel)
			q, err := s.Begin(ctx, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
			require.NoError(t, err)
			require.Equal(t, "BYPASS", q.Status)
			require.Equal(t, reason, q.Reason)
			require.Empty(t, q.Key)
			require.Empty(t, q.token)
			q.Complete(ctx, 200, nil)
			next := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
			require.Equal(t, "new", ch.ResolveChannelMapping(next, 1, "m").MappedModel)
			require.Same(t, old, ch.cache.Load())
			require.Empty(t, resultCacheChannelScope(next).failure())
		})
	}
}
func TestResultCacheChannelSnapshotOptOut(t *testing.T) {
	for _, intent := range []ResultCacheIntent{{}, rcRequestIntent("bypass")} {
		t.Run(intent.Mode, func(t *testing.T) {
			s, _, ch, repo, old := rcChannelAuthorityFixture(t)
			ctx := rcBind(t, s, context.Background(), intent)
			require.Equal(t, "old", ch.ResolveChannelMapping(ctx, 1, "m").MappedModel)
			require.Same(t, old, ch.cache.Load())
			repo.mu.Lock()
			require.Zero(t, repo.calls)
			repo.mu.Unlock()
		})
	}
}
func TestResultCacheChannelSnapshotConcurrentWaiters(t *testing.T) {
	s, _, ch, repo, old := rcChannelAuthorityFixture(t)
	repo.entered = make(chan struct{})
	repo.release = make(chan struct{})
	ctx := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
	results := make(chan string, 24)
	for i := 0; i < 24; i++ {
		go func() { results <- ch.ResolveChannelMapping(ctx, 1, "m").MappedModel }()
	}
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("snapshot reader not entered")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	start := time.Now()
	_, err := ch.loadCache(canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), time.Second)
	close(repo.release)
	for i := 0; i < 24; i++ {
		select {
		case value := <-results:
			require.Equal(t, "new", value)
		case <-time.After(time.Second):
			t.Fatal("healthy waiter stuck")
		}
	}
	repo.mu.Lock()
	require.Equal(t, 1, repo.calls)
	repo.mu.Unlock()
	require.Empty(t, resultCacheChannelScope(ctx).failure())
	require.Same(t, old, ch.cache.Load())
}
func TestResultCacheChannelSnapshotParentDeadline(t *testing.T) {
	s, _, ch, repo, _ := rcChannelAuthorityFixture(t)
	repo.entered = make(chan struct{})
	repo.release = make(chan struct{})
	parent, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	ctx := rcBind(t, s, parent, rcRequestIntent("read_write"))
	// A detached usage/forwarding caller cannot extend the captured parent DB budget.
	detached := context.WithoutCancel(ctx)
	require.Equal(t, "old", ch.ResolveChannelMapping(detached, 1, "m").MappedModel)
	require.Equal(t, "semantic_snapshot_error", resultCacheChannelScope(ctx).failure())
	q, err := s.Begin(detached, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
	require.NoError(t, err)
	require.Equal(t, "BYPASS", q.Status)
	q.Complete(detached, 200, nil)
}
func TestResultCacheChannelSnapshotSourcesAndLateFailure(t *testing.T) {
	s, _, good, _, old := rcChannelAuthorityFixture(t)
	_, _, bad, badRepo, _ := rcChannelAuthorityFixture(t)
	badRepo.err = errors.New("second source failed")
	ctx := rcBind(t, s, context.Background(), rcRequestIntent("write_only"))
	require.Equal(t, "new", good.ResolveChannelMapping(ctx, 1, "m").MappedModel)
	q, err := s.Begin(ctx, rcRequestIntent("write_only"), rcTestRequest(), ResultCacheAccess{})
	require.NoError(t, err)
	require.Equal(t, "MISS", q.Status)
	require.NotEmpty(t, q.token)
	require.Equal(t, "old", bad.ResolveChannelMapping(ctx, 1, "m").MappedModel)
	// Completion retains the scope even if its caller omits request context.
	q.NoteUpstream("m")
	q.Complete(context.Background(), 200, rcBody)
	require.Equal(t, "BYPASS", q.Status)
	require.Equal(t, "semantic_snapshot_error", q.Reason)
	store := s.store.(*rcFakeStore)
	store.mu.Lock()
	require.Nil(t, store.entry)
	require.Empty(t, store.token)
	store.mu.Unlock()
	require.Same(t, old, good.cache.Load())
}
func TestResultCacheChannelSnapshotVersionChangesAfterRead(t *testing.T) {
	s, p, ch, _, _ := rcChannelAuthorityFixture(t)
	ctx := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
	require.Equal(t, "new", ch.ResolveChannelMapping(ctx, 1, "m").MappedModel)
	p.update("request-v2", nil)
	q, err := s.Begin(ctx, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
	require.NoError(t, err)
	require.Equal(t, "policy_changed", q.Reason)
	require.Empty(t, q.Key)
	q.Complete(ctx, 200, nil)
}

func TestResultCacheChannelSnapshotGroupMismatch(t *testing.T) {
	for _, lookup := range []string{"channel", "platform", "mapping"} {
		t.Run(lookup, func(t *testing.T) {
			s, _, ch, _, _ := rcChannelAuthorityFixture(t)
			ctx := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
			switch lookup {
			case "channel":
				_, _ = ch.GetChannelForGroup(ctx, 2)
			case "platform":
				_ = ch.GetGroupPlatform(ctx, 2)
			case "mapping":
				_ = ch.ResolveChannelMapping(ctx, 2, "m")
			}
			q, err := s.Begin(ctx, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
			require.NoError(t, err)
			require.Equal(t, "policy_changed", q.Reason)
			require.Equal(t, "BYPASS", q.Status)
			q.Complete(ctx, 200, nil)
		})
	}
}
func TestResultCacheChannelSnapshotCloneAllPrices(t *testing.T) {
	p := ChannelModelPricing{Intervals: []PricingInterval{{}}}
	init := func(v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.Kind() == reflect.Pointer && (f.Type().Elem().Kind() == reflect.Float64 || (f.Type().Elem().Kind() == reflect.Int64 || f.Type().Elem().Kind() == reflect.Int)) {
				f.Set(reflect.New(f.Type().Elem()))
				if f.Type().Elem().Kind() == reflect.Float64 {
					f.Elem().SetFloat(2)
				} else {
					f.Elem().SetInt(2)
				}
			}
		}
	}
	init(reflect.ValueOf(&p).Elem())
	init(reflect.ValueOf(&p.Intervals[0]).Elem())
	clone := p.Clone()
	mutate := func(v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.Kind() == reflect.Pointer && f.Type().Elem().Kind() == reflect.Float64 {
				f.Elem().SetFloat(999)
			} else if f.Kind() == reflect.Pointer && (f.Type().Elem().Kind() == reflect.Int64 || f.Type().Elem().Kind() == reflect.Int) {
				f.Elem().SetInt(999)
			}
		}
	}
	mutate(reflect.ValueOf(&clone).Elem())
	mutate(reflect.ValueOf(&clone.Intervals[0]).Elem())
	check := func(v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.Kind() == reflect.Pointer && f.Type().Elem().Kind() == reflect.Float64 {
				require.Equal(t, 2.0, f.Elem().Float())
			} else if f.Kind() == reflect.Pointer && (f.Type().Elem().Kind() == reflect.Int64 || f.Type().Elem().Kind() == reflect.Int) {
				require.Equal(t, int64(2), f.Elem().Int())
			}
		}
	}
	check(reflect.ValueOf(p))
	check(reflect.ValueOf(p.Intervals[0]))
}

func TestResultCacheChannelSnapshotLateFailureRejectsRead(t *testing.T) {
	s, _, good, _, _ := rcChannelAuthorityFixture(t)
	_, _, bad, repo, _ := rcChannelAuthorityFixture(t)
	repo.err = errors.New("late source error")
	ctx := rcBind(t, s, context.Background(), rcRequestIntent("write_only"))
	require.Equal(t, "new", good.ResolveChannelMapping(ctx, 1, "m").MappedModel)
	q, err := s.Begin(ctx, rcRequestIntent("write_only"), rcTestRequest(), ResultCacheAccess{})
	require.NoError(t, err)
	store := s.store.(*rcFakeStore)
	store.mu.Lock()
	store.entry = &ResultCacheEntry{PolicyVersion: "request-v1", Model: "m", Body: rcBody, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	store.mu.Unlock()
	require.Equal(t, "old", bad.ResolveChannelMapping(ctx, 1, "m").MappedModel)
	require.False(t, q.read(context.Background()))
	require.Nil(t, q.Entry)
	require.Equal(t, "semantic_snapshot_error", q.Reason)
	q.Complete(context.Background(), 200, nil)
}

type rcChannelBarrierPolicy struct {
	ResultCachePolicyRepository
	entered, release chan struct{}
}

func (p *rcChannelBarrierPolicy) Current(ctx context.Context, id int64) (ResultCachePolicy, error) {
	close(p.entered)
	select {
	case <-p.release:
		return p.ResultCachePolicyRepository.Current(ctx, id)
	case <-ctx.Done():
		return ResultCachePolicy{}, ctx.Err()
	}
}
func TestResultCacheChannelSnapshotFailureDuringAuthorityCheck(t *testing.T) {
	for _, kind := range []string{"source_error", "group_mismatch", "pending_error", "pending_success"} {
		t.Run(kind, func(t *testing.T) {
			s, policy, good, _, _ := rcChannelAuthorityFixture(t)
			_, _, bad, repo, _ := rcChannelAuthorityFixture(t)
			repo.err = errors.New("late snapshot failure")
			ctx := rcBind(t, s, context.Background(), rcRequestIntent("write_only"))
			require.Equal(t, "new", good.ResolveChannelMapping(ctx, 1, "m").MappedModel)
			q, err := s.Begin(ctx, rcRequestIntent("write_only"), rcTestRequest(), ResultCacheAccess{})
			require.NoError(t, err)
			require.NotEmpty(t, q.token)
			q.NoteUpstream("m")
			barrier := &rcChannelBarrierPolicy{ResultCachePolicyRepository: policy, entered: make(chan struct{}), release: make(chan struct{})}
			s.policy = barrier
			done := make(chan struct{})
			go func() { q.Complete(context.Background(), 200, rcBody); close(done) }()
			<-barrier.entered
			reason := "semantic_snapshot_error"
			if kind == "group_mismatch" {
				good.ResolveChannelMapping(ctx, 2, "m")
				reason = "policy_changed"
			} else if kind == "source_error" {
				bad.ResolveChannelMapping(ctx, 1, "m")
			} else {
				repo.entered = make(chan struct{})
				repo.release = make(chan struct{})
				if kind == "pending_success" {
					repo.err = nil
				}
				lookupDone := make(chan struct{})
				go func() { bad.ResolveChannelMapping(ctx, 1, "m"); close(lookupDone) }()
				<-repo.entered
				close(barrier.release)
				<-done
				// An in-flight source is not proof of a healthy, complete semantic set.
				require.Equal(t, "BYPASS", q.Status)
				require.Equal(t, "semantic_snapshot_pending", q.Reason)
				close(repo.release)
				<-lookupDone
			}
			if kind == "source_error" || kind == "group_mismatch" {
				close(barrier.release)
				<-done
				require.Equal(t, "BYPASS", q.Status)
				require.Equal(t, reason, q.Reason)
			}
			store := s.store.(*rcFakeStore)
			store.mu.Lock()
			require.Nil(t, store.entry)
			require.Empty(t, store.token)
			store.mu.Unlock()
		})
	}
}
func TestResultCacheChannelSnapshotClonePreservesNil(t *testing.T) {
	p := ChannelModelPricing{Intervals: []PricingInterval{{}}}
	clone := p.Clone()
	require.Nil(t, clone.InputPrice)
	require.Nil(t, clone.Intervals[0].MaxTokens)
	require.Nil(t, clone.Intervals[0].InputPrice)
}

type rcChannelGuardStore struct {
	*rcFakeStore
	scope  *resultCacheChannelSnapshots
	locked bool
}

func (s *rcChannelGuardStore) Publish(ctx context.Context, key, token string, e *ResultCacheEntry) (bool, error) {
	if s.scope.mu.TryLock() {
		s.scope.mu.Unlock()
	} else {
		s.locked = true
	}
	return s.rcFakeStore.Publish(ctx, key, token, e)
}
func TestResultCacheChannelSnapshotPublicationCommitBoundary(t *testing.T) {
	s, _, ch, _, _ := rcChannelAuthorityFixture(t)
	ctx := rcBind(t, s, context.Background(), rcRequestIntent("write_only"))
	require.Equal(t, "new", ch.ResolveChannelMapping(ctx, 1, "m").MappedModel)
	store := &rcChannelGuardStore{rcFakeStore: s.store.(*rcFakeStore), scope: resultCacheChannelScope(ctx)}
	s.store = store
	q, err := s.Begin(ctx, rcRequestIntent("write_only"), rcTestRequest(), ResultCacheAccess{})
	require.NoError(t, err)
	q.NoteUpstream("m")
	q.Complete(context.Background(), 200, rcBody)
	require.Equal(t, "UPDATED", q.Status)
	require.True(t, store.locked, "Publish must serialize scope failure/source registration through its local commit")
	require.True(t, store.scope.mu.TryLock(), "scope lock must be released after publication")
	store.scope.mu.Unlock()
}
