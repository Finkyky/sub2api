package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"sync"
	"testing"
	"time"
)

type rcFakePolicy struct {
	mu sync.Mutex
	p  ResultCachePolicy
}

func (f *rcFakePolicy) Current(context.Context, int64) (ResultCachePolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.p, nil
}

type rcFakeStore struct {
	mu    sync.Mutex
	entry *ResultCacheEntry
	token string
	fail  bool
}

func (f *rcFakeStore) Get(context.Context, string) (*ResultCacheEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, errors.New("redis down")
	}
	return f.entry, nil
}
func (f *rcFakeStore) Acquire(_ context.Context, _ string, t string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" {
		return false, nil
	}
	f.token = t
	return true, nil
}
func (f *rcFakeStore) Renew(_ context.Context, _ string, t string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token == t, nil
}
func (f *rcFakeStore) Publish(_ context.Context, _ string, t string, e *ResultCacheEntry) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t != f.token {
		return false, nil
	}
	f.entry = e
	f.token = ""
	return true, nil
}
func (f *rcFakeStore) Release(_ context.Context, _ string, t string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t == f.token {
		f.token = ""
	}
	return nil
}
func rcTestRequest() ResultCacheKeyMaterial {
	return ResultCacheKeyMaterial{GroupID: 1, ScopeIdentity: "1/2", EffectiveModel: "m", EffectiveRequest: []byte(`{"model":"m","messages":[{"role":"user","content":"q"}]}`)}
}

var rcBody = []byte(`{"id":"chatcmpl-old","model":"m","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"total_tokens":5}}`)

func TestResultCacheServiceModes(t *testing.T) {
	p := &rcFakePolicy{p: ResultCachePolicy{Active: true, Version: "v1", Config: GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 30, MaxResponseBytes: 2 << 20, Revision: 1}}}
	store := &rcFakeStore{}
	s := NewResultCacheService(store, p, nil, nil)
	defer s.Stop()
	ctx := context.Background()
	session, e := s.Begin(ctx, ResultCacheIntent{Declared: true, Mode: "read_write", Scope: "user"}, rcTestRequest(), ResultCacheAccess{})
	if e != nil || session.Status != "MISS" {
		t.Fatal(session, e)
	}
	session.NoteUpstream("m")
	session.Complete(ctx, 200, rcBody)
	if store.entry == nil {
		t.Fatal("no publish")
	}
	hit, e := s.Begin(ctx, ResultCacheIntent{Declared: true, Mode: "read_write", Scope: "user"}, rcTestRequest(), ResultCacheAccess{})
	if e != nil || hit.Status != "HIT" {
		t.Fatal(hit, e)
	}
	hit.Complete(ctx, 200, nil)
	refresh, e := s.Begin(ctx, ResultCacheIntent{Declared: true, Mode: "write_only", Scope: "user"}, rcTestRequest(), ResultCacheAccess{})
	if e != nil || refresh.Status == "HIT" {
		t.Fatal("write_only hit", e)
	}
	refresh.NoteUpstream("m")
	refresh.Complete(ctx, 200, rcBody)
	store.entry = nil
	read, e := s.Begin(ctx, ResultCacheIntent{Declared: true, Mode: "read_only", Scope: "user"}, rcTestRequest(), ResultCacheAccess{})
	if e != nil {
		t.Fatal(e)
	}
	read.NoteUpstream("m")
	read.Complete(ctx, 200, rcBody)
	if store.entry != nil {
		t.Fatal("read_only wrote")
	}
	p.p.Config.Enabled = false
	b, e := s.Begin(ctx, ResultCacheIntent{Declared: true, Mode: "read_write", Scope: "user"}, rcTestRequest(), ResultCacheAccess{})
	if e != nil || b.Status != "BYPASS" {
		t.Fatal(b, e)
	}
	b.Complete(ctx, 200, nil)
}
func TestResultCacheServiceDisableInflight(t *testing.T) {
	p := &rcFakePolicy{p: ResultCachePolicy{Active: true, Version: "v1", Config: GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 30, MaxResponseBytes: 2 << 20, Revision: 1}}}
	store := &rcFakeStore{}
	s := NewResultCacheService(store, p, nil, nil)
	defer s.Stop()
	q, e := s.Begin(context.Background(), ResultCacheIntent{Declared: true, Mode: "read_write", Scope: "user"}, rcTestRequest(), ResultCacheAccess{})
	if e != nil {
		t.Fatal(e)
	}
	p.mu.Lock()
	p.p.Config.Enabled = false
	p.p.Version = "v2"
	p.mu.Unlock()
	q.NoteUpstream("m")
	q.Complete(context.Background(), 200, rcBody)
	if store.entry != nil {
		t.Fatal("disabled policy published")
	}
}
func TestResultCacheResponseComplete(t *testing.T) {
	if !ResultCacheResponseComplete(rcBody) {
		t.Fatal("complete response rejected")
	}
	for _, b := range []string{`{"choices":[]}`, `{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`, `{"choices":[{"finish_reason":"stop","message":{"content":"","tool_calls":[{}]}}]}`, `{"error":{}}`} {
		if ResultCacheResponseComplete([]byte(b)) {
			t.Fatal("bad cached", b)
		}
	}
}
func TestResultCacheCoalescing(t *testing.T) {
	p := &rcFakePolicy{p: ResultCachePolicy{Active: true, Version: "v1", Config: GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 30, MaxResponseBytes: 2 << 20, Revision: 1}}}
	store := &rcFakeStore{}
	s := NewResultCacheService(store, p, nil, nil)
	defer s.Stop()
	ctx := context.Background()
	first, e := s.Begin(ctx, ResultCacheIntent{Declared: true, Mode: "read_write", Scope: "user"}, rcTestRequest(), ResultCacheAccess{})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan *ResultCacheSession, 1)
	go func() {
		q, _ := s.Begin(ctx, ResultCacheIntent{Declared: true, Mode: "read_write", Scope: "user"}, rcTestRequest(), ResultCacheAccess{})
		done <- q
	}()
	first.NoteUpstream("m")
	first.Complete(ctx, 200, rcBody)
	select {
	case q := <-done:
		if q == nil || q.Status != "HIT" {
			t.Fatal("waiter generated twice", q)
		}
		q.Complete(ctx, 200, nil)
	case <-time.After(time.Second):
		t.Fatal("waiter hung")
	}
}

// rcRuntimeStore keeps independent lease owners and exposes release events so
// lifecycle tests wait for observable state changes rather than fixed sleeps.
type rcRuntimeStore struct {
	mu          sync.Mutex
	tokens      map[string]string
	released    chan string
	renewFails  bool
	published   int
	acquiredTTL time.Duration
}

func newRCRuntimeStore() *rcRuntimeStore {
	return &rcRuntimeStore{tokens: map[string]string{}, released: make(chan string, 16)}
}
func (f *rcRuntimeStore) Get(context.Context, string) (*ResultCacheEntry, error) { return nil, nil }
func (f *rcRuntimeStore) Acquire(ctx context.Context, k, t string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.tokens[k] != "" {
		return false, nil
	}
	f.tokens[k] = t
	f.acquiredTTL = ttl
	return true, nil
}
func (f *rcRuntimeStore) Renew(_ context.Context, k, t string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renewFails {
		return false, errors.New("renew failed")
	}
	return f.tokens[k] == t, nil
}
func (f *rcRuntimeStore) Publish(_ context.Context, k, t string, _ *ResultCacheEntry) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens[k] != t {
		return false, nil
	}
	f.published++
	delete(f.tokens, k)
	return true, nil
}
func (f *rcRuntimeStore) Release(_ context.Context, k, t string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens[k] == t {
		delete(f.tokens, k)
		f.released <- k
	}
	return nil
}
func rcRuntimeConfig() *config.Config {
	c := &config.Config{}
	c.Gateway.ResultCache = config.DefaultResultCacheConfig()
	c.Gateway.ResultCache.MaxConcurrentFills = 1
	c.Gateway.ResultCache.IOTimeoutMS = 10
	c.Gateway.ResultCache.LeaseTTLMS = 300
	return c
}
func rcRuntimeService(t *testing.T, store *rcRuntimeStore, cfg *config.Config) *ResultCacheService {
	t.Helper()
	p := &rcFakePolicy{p: ResultCachePolicy{Active: true, Version: "v1", Config: GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 30, MaxResponseBytes: 2 << 20, Revision: 1}}}
	s := NewResultCacheService(store, p, nil, cfg)
	t.Cleanup(s.Stop)
	return s
}
func rcRuntimeBegin(t *testing.T, s *ResultCacheService, ctx context.Context, identity string) *ResultCacheSession {
	t.Helper()
	m := rcTestRequest()
	m.ScopeIdentity = identity
	q, err := s.Begin(ctx, ResultCacheIntent{Declared: true, Mode: "read_write", Scope: "user"}, m, ResultCacheAccess{})
	if err != nil || q == nil {
		t.Fatalf("begin: %v, %v", q, err)
	}
	t.Cleanup(func() { q.Complete(context.Background(), 500, nil) })
	return q
}
func rcAwaitRelease(t *testing.T, store *rcRuntimeStore) {
	t.Helper()
	select {
	case <-store.released:
	case <-time.After(2 * time.Second):
		t.Fatal("lease was not released")
	}
}
func TestResultCacheRuntimeCapacity(t *testing.T) {
	store := newRCRuntimeStore()
	s := rcRuntimeService(t, store, rcRuntimeConfig())
	first := rcRuntimeBegin(t, s, context.Background(), "first")
	if first.Status != "MISS" || store.acquiredTTL != 300*time.Millisecond {
		t.Fatal("configured lease not applied", first.Status, store.acquiredTTL)
	}
	second := rcRuntimeBegin(t, s, context.Background(), "second")
	if second.Status != "BYPASS" || second.Reason != "fill_capacity" {
		t.Fatal("capacity not enforced", second.Status, second.Reason)
	}
	first.Complete(context.Background(), 500, nil)
	third := rcRuntimeBegin(t, s, context.Background(), "third")
	if third.Status != "MISS" {
		t.Fatal("slot not reusable", third.Status, third.Reason)
	}
}
func TestResultCacheRuntimeGenerationBudget(t *testing.T) {
	store := newRCRuntimeStore()
	cfg := rcRuntimeConfig()
	cfg.Gateway.ResultCache.GenerationTimeoutMS = 30
	s := rcRuntimeService(t, store, cfg)
	q := rcRuntimeBegin(t, s, context.Background(), "first")
	q.NoteUpstream("m")
	rcAwaitRelease(t, store)
	q.Complete(context.Background(), 200, rcBody)
	if q.Status != "BYPASS" || q.Reason != "generation_timeout" || store.published != 0 {
		t.Fatal("late producer published", q.Status, q.Reason, store.published)
	}
	next := rcRuntimeBegin(t, s, context.Background(), "second")
	if next.Status != "MISS" {
		t.Fatal("budget leaked capacity", next.Status, next.Reason)
	}
}
func TestResultCacheRuntimeCancellation(t *testing.T) {
	store := newRCRuntimeStore()
	s := rcRuntimeService(t, store, rcRuntimeConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := rcRuntimeBegin(t, s, ctx, "first")
	q.NoteUpstream("m")
	cancel()
	rcAwaitRelease(t, store)
	next := rcRuntimeBegin(t, s, context.Background(), "second")
	if next.Status != "MISS" {
		t.Fatal("cancel leaked capacity", next.Status, next.Reason)
	}
	q.Complete(ctx, 200, rcBody)
	if store.published != 0 {
		t.Fatal("cancelled producer published")
	}
}
func TestResultCacheRuntimeRenewFailure(t *testing.T) {
	store := newRCRuntimeStore()
	store.renewFails = true
	s := rcRuntimeService(t, store, rcRuntimeConfig())
	q := rcRuntimeBegin(t, s, context.Background(), "first")
	q.NoteUpstream("m")
	rcAwaitRelease(t, store)
	q.Complete(context.Background(), 200, rcBody)
	if store.published != 0 || q.Status != "BYPASS" {
		t.Fatal("lost lease published", q.Status, store.published)
	}
	next := rcRuntimeBegin(t, s, context.Background(), "second")
	if next.Status != "MISS" {
		t.Fatal("renew failure leaked capacity", next.Status, next.Reason)
	}
}
func TestResultCacheRuntimeShutdown(t *testing.T) {
	store := newRCRuntimeStore()
	s := rcRuntimeService(t, store, rcRuntimeConfig())
	q := rcRuntimeBegin(t, s, context.Background(), "first")
	q.NoteUpstream("m")
	s.Stop()
	rcAwaitRelease(t, store)
	q.Complete(context.Background(), 200, rcBody)
	if store.published != 0 || q.Reason != "service_stopped" {
		t.Fatal("stopped service published", q.Status, q.Reason, store.published)
	}
	next := rcRuntimeBegin(t, s, context.Background(), "second")
	if next.Status != "BYPASS" || next.Reason != "service_stopped" {
		t.Fatal("stopped service acquired", next.Status, next.Reason)
	}
}
func TestResultCacheRuntimeNoWait(t *testing.T) {
	store := newRCRuntimeStore()
	cfg := rcRuntimeConfig()
	cfg.Gateway.ResultCache.WaitTimeoutMS = 0
	s := rcRuntimeService(t, store, cfg)
	_ = rcRuntimeBegin(t, s, context.Background(), "first")
	second := rcRuntimeBegin(t, s, context.Background(), "first")
	if second.Status != "BYPASS" || second.Reason != "wait_timeout" {
		t.Fatal("no-wait budget ignored", second.Status, second.Reason)
	}
}

type rcDeadlinePolicy struct {
	rcFakePolicy
	observed chan time.Duration
}

func (f *rcDeadlinePolicy) Current(ctx context.Context, id int64) (ResultCachePolicy, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return ResultCachePolicy{}, errors.New("missing I/O deadline")
	}
	f.observed <- time.Until(deadline)
	return f.rcFakePolicy.Current(ctx, id)
}
func TestResultCacheRuntimeIODeadline(t *testing.T) {
	cfg := rcRuntimeConfig()
	p := &rcDeadlinePolicy{rcFakePolicy: rcFakePolicy{p: ResultCachePolicy{Active: true, Version: "v1", Config: GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 30, MaxResponseBytes: 2 << 20, Revision: 1}}}, observed: make(chan time.Duration, 8)}
	store := newRCRuntimeStore()
	s := NewResultCacheService(store, p, nil, cfg)
	defer s.Stop()
	q := rcRuntimeBegin(t, s, context.Background(), "first")
	q.NoteUpstream("m")
	q.Complete(context.Background(), 200, rcBody)
	for i := 0; i < 2; i++ {
		select {
		case remaining := <-p.observed:
			if remaining <= 0 || remaining > 10*time.Millisecond {
				t.Fatalf("I/O timeout not applied: %s", remaining)
			}
		default:
			t.Fatal("policy read was not observed")
		}
	}
	if store.published != 1 {
		t.Fatal("valid completion lost lease before publish")
	}
}
