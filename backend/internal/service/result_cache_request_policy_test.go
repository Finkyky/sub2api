package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type rcRequestPolicyRepo struct {
	mu    sync.Mutex
	p     ResultCachePolicy
	err   error
	calls int
}

func (p *rcRequestPolicyRepo) Current(context.Context, int64) (ResultCachePolicy, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.p, p.err
}
func (p *rcRequestPolicyRepo) update(version string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.p.Version = version
	p.err = err
}
func (p *rcRequestPolicyRepo) count() int { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }
func rcRequestPolicyService(t *testing.T) (*ResultCacheService, *rcRequestPolicyRepo, *rcFakeStore) {
	t.Helper()
	p := &rcRequestPolicyRepo{p: ResultCachePolicy{Active: true, Version: "request-v1", Config: GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 60, MaxResponseBytes: 2 << 20}}}
	store := &rcFakeStore{}
	s := NewResultCacheService(store, p, nil, nil)
	t.Cleanup(s.Stop)
	return s, p, store
}
func rcBind(t *testing.T, s *ResultCacheService, ctx context.Context, intent ResultCacheIntent) context.Context {
	t.Helper()
	id := rcTestRequest().GroupID
	return s.BindRequestPolicy(ctx, intent, &id)
}
func rcRequestIntent(mode string) ResultCacheIntent {
	return ResultCacheIntent{Declared: true, Mode: mode, Scope: "user"}
}

func TestResultCacheRequestPolicyStable(t *testing.T) {
	for _, mode := range []string{"read_write", "read_only", "write_only"} {
		t.Run(mode, func(t *testing.T) {
			s, p, store := rcRequestPolicyService(t)
			intent := rcRequestIntent(mode)
			ctx := rcBind(t, s, context.Background(), intent)
			require.Equal(t, 1, p.count())
			q, err := s.Begin(ctx, intent, rcTestRequest(), ResultCacheAccess{})
			require.NoError(t, err)
			require.Equal(t, "MISS", q.Status)
			require.NotEmpty(t, q.Key)
			q.NoteUpstream("m")
			q.Complete(ctx, 200, rcBody)
			store.mu.Lock()
			entry := store.entry
			store.mu.Unlock()
			if mode == "read_only" {
				require.Nil(t, entry)
			} else {
				require.NotNil(t, entry)
				require.Equal(t, "request-v1", entry.PolicyVersion)
				next := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
				hit, err := s.Begin(next, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
				require.NoError(t, err)
				require.Equal(t, "HIT", hit.Status)
				hit.Complete(next, 200, nil)
			}
		})
	}
}

func TestResultCacheRequestPolicyBypass(t *testing.T) {
	cases := []string{"changed", "early_error_recovers", "early_empty_version", "early_disabled", "different_group", "different_service", "no_rebind", "nil_group"}
	for _, name := range cases {
		for _, mode := range []string{"read_write", "read_only", "write_only"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				s, p, store := rcRequestPolicyService(t)
				intent := rcRequestIntent(mode)
				reason := "policy_changed"
				switch name {
				case "early_error_recovers":
					p.update("request-v1", errors.New("authority unavailable"))
					reason = "policy_error"
				case "early_empty_version":
					p.update("", nil)
					reason = "policy_error"
				case "early_disabled":
					p.mu.Lock()
					p.p.Config.Enabled = false
					p.mu.Unlock()
					reason = "disabled"
				}
				ctx := rcBind(t, s, context.Background(), intent)
				m := rcTestRequest()
				switch name {
				case "changed":
					p.update("request-v2", nil)
				case "early_error_recovers", "early_empty_version":
					p.update("request-v1", nil)
				case "early_disabled":
					p.mu.Lock()
					p.p.Config.Enabled = true
					p.mu.Unlock()
				case "different_group":
					m.GroupID++
				case "different_service":
					other, _, _ := rcRequestPolicyService(t)
					ctx = rcBind(t, other, context.Background(), intent)
				case "no_rebind":
					p.update("request-v2", nil)
					ctx = rcBind(t, s, ctx, intent)
					require.Equal(t, 1, p.count())
				case "nil_group":
					ctx = s.BindRequestPolicy(context.Background(), intent, nil)
				}
				store.mu.Lock()
				store.entry = &ResultCacheEntry{PolicyVersion: p.p.Version, Model: "m", Body: rcBody, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
				original := store.entry
				store.mu.Unlock()
				q, err := s.Begin(ctx, intent, m, ResultCacheAccess{})
				require.NoError(t, err)
				require.Equal(t, "BYPASS", q.Status)
				require.Equal(t, reason, q.Reason)
				require.Empty(t, q.Key)
				require.Empty(t, q.token)
				require.Zero(t, q.CaptureLimit())
				require.Nil(t, q.Entry)
				q.NoteUpstream("m")
				q.Complete(ctx, 200, rcBody)
				store.mu.Lock()
				defer store.mu.Unlock()
				require.Same(t, original, store.entry)
				require.Empty(t, store.token)
			})
		}
	}
}

func TestResultCacheRequestPolicyNoExtraRead(t *testing.T) {
	for _, intent := range []ResultCacheIntent{{}, rcRequestIntent("bypass")} {
		t.Run(intent.Mode, func(t *testing.T) {
			s, p, _ := rcRequestPolicyService(t)
			parent := context.Background()
			ctx := rcBind(t, s, parent, intent)
			require.True(t, parent == ctx)
			require.Zero(t, p.count())
			q, err := s.Begin(ctx, intent, rcTestRequest(), ResultCacheAccess{})
			require.NoError(t, err)
			if intent.Declared {
				require.Equal(t, "client_bypass", q.Reason)
				require.Equal(t, 1, p.count())
				q.Complete(ctx, 200, nil)
			} else {
				require.Nil(t, q)
				require.Zero(t, p.count())
			}
		})
	}
}

func TestResultCacheRequestPolicyContext(t *testing.T) {
	type key struct{}
	s, _, _ := rcRequestPolicyService(t)
	parent, cancel := context.WithDeadline(context.WithValue(context.Background(), key{}, "preserved"), time.Now().Add(time.Minute))
	ctx := rcBind(t, s, parent, rcRequestIntent("read_write"))
	require.Equal(t, "preserved", ctx.Value(key{}))
	a, ok := parent.Deadline()
	b, ok2 := ctx.Deadline()
	require.True(t, ok)
	require.True(t, ok2)
	require.Equal(t, a, b)
	cancel()
	q, err := s.Begin(ctx, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
	require.Nil(t, q)
	require.ErrorIs(t, err, context.Canceled)
}

func TestResultCacheRequestPolicyUnavailable(t *testing.T) {
	for _, name := range []string{"nil", "stopped"} {
		t.Run(name, func(t *testing.T) {
			var s *ResultCacheService
			reason := "unavailable"
			if name == "stopped" {
				s, _, _ = rcRequestPolicyService(t)
				s.Stop()
				reason = "service_stopped"
			}
			ctx := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
			q, err := s.Begin(ctx, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
			require.NoError(t, err)
			require.Equal(t, reason, q.Reason)
			q.Complete(ctx, 200, nil)
		})
	}
}

func TestResultCacheRequestPolicyScopeDenied(t *testing.T) {
	s, p, _ := rcRequestPolicyService(t)
	intent := rcRequestIntent("read_write")
	intent.Scope = "group"
	ctx := rcBind(t, s, context.Background(), intent)
	p.update("request-v2", nil)
	q, err := s.Begin(ctx, intent, rcTestRequest(), ResultCacheAccess{})
	require.Nil(t, q)
	require.ErrorIs(t, err, ErrResultCacheScopeDenied)
}

func TestResultCacheRequestPolicyPublicationRecheck(t *testing.T) {
	s, p, store := rcRequestPolicyService(t)
	intent := rcRequestIntent("read_write")
	ctx := rcBind(t, s, context.Background(), intent)
	q, err := s.Begin(ctx, intent, rcTestRequest(), ResultCacheAccess{})
	require.NoError(t, err)
	require.Equal(t, "MISS", q.Status)
	p.update("request-v2", nil)
	q.NoteUpstream("m")
	q.Complete(ctx, 200, rcBody)
	require.Equal(t, "BYPASS", q.Status)
	require.Equal(t, "policy_changed", q.Reason)
	store.mu.Lock()
	defer store.mu.Unlock()
	require.Nil(t, store.entry)
	require.Empty(t, store.token)
}

type rcRequestBlockingPolicy struct {
	started chan struct{}
	done    chan error
}

func (p *rcRequestBlockingPolicy) Current(ctx context.Context, _ int64) (ResultCachePolicy, error) {
	close(p.started)
	<-ctx.Done()
	p.done <- ctx.Err()
	return ResultCachePolicy{}, ctx.Err()
}
func TestResultCacheRequestPolicyEarlyCancellation(t *testing.T) {
	for _, name := range []string{"already_canceled", "cancel_during_read", "io_timeout"} {
		t.Run(name, func(t *testing.T) {
			p := &rcRequestBlockingPolicy{started: make(chan struct{}), done: make(chan error, 1)}
			s := NewResultCacheService(&rcFakeStore{}, p, nil, nil)
			s.ioTimeout = 5 * time.Second
			if name == "io_timeout" {
				s.ioTimeout = 20 * time.Millisecond
			}
			t.Cleanup(s.Stop)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "already_canceled" {
				cancel()
			}
			result := make(chan context.Context, 1)
			go func() { id := int64(1); result <- s.BindRequestPolicy(parent, rcRequestIntent("read_write"), &id) }()
			if name != "already_canceled" {
				select {
				case <-p.started:
				case <-time.After(time.Second):
					t.Fatal("authority read did not start")
				}
				if name == "cancel_during_read" {
					cancel()
				}
			}
			var ctx context.Context
			select {
			case ctx = <-result:
			case <-time.After(time.Second):
				t.Fatal("early binding did not finish within timeout")
			}
			pin, ok := ctx.Value(resultCacheRequestPolicyKey{}).(resultCacheRequestPolicy)
			require.True(t, ok)
			require.Equal(t, "policy_error", pin.reason)
			if name == "already_canceled" {
				select {
				case <-p.started:
					t.Fatal("already canceled request queried authority")
				default:
				}
			}
			if name == "io_timeout" {
				require.NoError(t, parent.Err())
				require.NoError(t, ctx.Err())
				require.ErrorIs(t, <-p.done, context.DeadlineExceeded)
				// The timeout belongs only to the authority read, not normal forwarding.
				s.policy = &rcRequestPolicyRepo{p: ResultCachePolicy{Active: true, Version: "request-v1", Config: GroupResponseResultCacheConfig{Enabled: true}}}
				q, err := s.Begin(ctx, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
				require.NoError(t, err)
				require.Equal(t, "policy_error", q.Reason)
				require.Equal(t, "BYPASS", q.Status)
				q.Complete(ctx, 200, nil)
			} else {
				q, err := s.Begin(ctx, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
				require.Nil(t, q)
				require.ErrorIs(t, err, context.Canceled)
				if name == "cancel_during_read" {
					require.ErrorIs(t, <-p.done, context.Canceled)
				}
			}
		})
	}
}
