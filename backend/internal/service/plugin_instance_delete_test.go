package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type piDeleteAPI interface {
	Delete(context.Context, int64, int64) error
}
type piDeleteRepo struct {
	*piMemoryRepo
	deleted bool
	calls   atomic.Int32
	fail    error
	entered chan struct{}
	resume  chan struct{}
}

func (r *piDeleteRepo) Delete(ctx context.Context, id, expected int64) error {
	r.calls.Add(1)
	if r.entered != nil {
		close(r.entered)
		<-r.resume
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	if id != 11 || r.deleted {
		return ErrPluginInstanceNotFound
	}
	if expected != r.s.Instance.Generation {
		return ErrPluginInstanceConflict
	}
	if r.s.Instance.Enabled {
		return ErrPluginInstanceEnabled
	}
	r.deleted = true
	r.s.Bindings = nil
	r.revisions = nil
	return nil
}
func (r *piDeleteRepo) Snapshot(ctx context.Context, id, revision int64) (*PluginInstanceSnapshot, error) {
	r.mu.Lock()
	deleted := r.deleted
	r.mu.Unlock()
	if deleted {
		return nil, ErrPluginInstanceNotFound
	}
	return r.piMemoryRepo.Snapshot(ctx, id, revision)
}
func piDeleteFixture(t *testing.T) (*PluginInstanceService, *piDeleteRepo, *[]*piFakeRuntime, piDeleteAPI) {
	t.Helper()
	repo := &piDeleteRepo{piMemoryRepo: newPIMemoryRepo()}
	started := []*piFakeRuntime{}
	s := newPluginInstanceService(repo, piTestCipher{}, func(_ context.Context, _ *PluginInstanceSnapshot, _ json.RawMessage) (pluginInstanceRuntime, error) {
		r := &piFakeRuntime{}
		started = append(started, r)
		return r, nil
	})
	t.Cleanup(s.Close)
	api, ok := any(s).(piDeleteAPI)
	require.True(t, ok, "service must implement generation-guarded Delete")
	return s, repo, &started, api
}
func TestPluginInstanceDeleteGuardsAndCleanup(t *testing.T) {
	ctx := context.Background()
	s, r, started, api := piDeleteFixture(t)
	for _, args := range [][2]int64{{0, 1}, {11, 0}, {-1, 1}, {11, -1}} {
		require.ErrorIs(t, api.Delete(ctx, args[0], args[1]), ErrPluginInstanceInvalid)
	}
	require.Zero(t, r.calls.Load())
	require.EqualError(t, api.Delete(ctx, 11, 1), "plugin instance is enabled")
	lease, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	_, err = s.Disable(ctx, 11, 1)
	require.NoError(t, err)
	require.EqualError(t, api.Delete(ctx, 11, 2), "plugin instance has active local leases")
	require.Equal(t, int32(1), r.calls.Load(), "lease guard must run before repository delete")
	require.Zero(t, (*started)[0].closed.Load())
	lease.Release()
	require.Equal(t, int32(1), (*started)[0].closed.Load())
	s.recordInstanceError(11, "runtime_start_failed")
	require.ErrorIs(t, api.Delete(ctx, 11, 1), ErrPluginInstanceConflict)
	require.NoError(t, api.Delete(ctx, 11, 2))
	require.True(t, r.deleted)
	require.Nil(t, r.revisions)
	require.Nil(t, r.s.Bindings)
	s.mu.Lock()
	current, handles, history := s.current[11], len(s.all), len(s.lastErrors)
	s.mu.Unlock()
	require.Nil(t, current)
	require.Zero(t, handles)
	require.Zero(t, history)
	require.ErrorIs(t, api.Delete(ctx, 11, 2), ErrPluginInstanceNotFound)
	_, err = s.Acquire(ctx, 11)
	require.ErrorIs(t, err, ErrPluginInstanceNotFound)
}
func TestPluginInstanceDeleteChecksSupersededLease(t *testing.T) {
	ctx := context.Background()
	s, r, started, api := piDeleteFixture(t)
	lease, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	// Publishing the same revision supersedes the old leased handle.
	_, err = s.Publish(ctx, 11, 1, 1)
	require.NoError(t, err)
	_, err = s.Disable(ctx, 11, 2)
	require.NoError(t, err)
	require.Equal(t, int32(1), (*started)[1].closed.Load())
	require.EqualError(t, api.Delete(ctx, 11, 3), "plugin instance has active local leases")
	require.Zero(t, r.calls.Load())
	lease.Release()
	require.NoError(t, api.Delete(ctx, 11, 3))
	require.Equal(t, int32(1), (*started)[0].closed.Load())
}
func TestPluginInstanceDeleteFailurePreservesLocalState(t *testing.T) {
	ctx := context.Background()
	s, r, started, api := piDeleteFixture(t)
	lease, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	lease.Release()
	// Another node disabled the DB; local idle runtime still exists.
	r.mu.Lock()
	r.s.Instance.Enabled = false
	r.mu.Unlock()
	s.recordInstanceError(11, "runtime_call_failed")
	r.fail = errors.New("private repository failure")
	require.ErrorIs(t, api.Delete(ctx, 11, 1), r.fail)
	require.Zero(t, (*started)[0].closed.Load())
	require.NotNil(t, s.current[11])
	require.Equal(t, "runtime_call_failed", s.lastErrors[11])
	r.fail = nil
	require.NoError(t, api.Delete(ctx, 11, 1))
	require.Equal(t, int32(1), (*started)[0].closed.Load())
	require.Empty(t, s.lastErrors)
}
func TestPluginInstanceDeleteSerializesNewAcquisition(t *testing.T) {
	ctx := context.Background()
	s, r, started, api := piDeleteFixture(t)
	r.s.Instance.Enabled = false
	r.entered = make(chan struct{})
	r.resume = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- api.Delete(ctx, 11, 1) }()
	<-r.entered
	// While Delete holds the gate, neither a new acquisition nor publication
	// can even reach DB authority. A pre-cancelled context would not test this.
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(r.resume) }) }
	defer resume()
	for _, operation := range []string{"acquire", "publish"} {
		waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		if operation == "acquire" {
			_, err := s.Acquire(waiting, 11)
			require.ErrorIs(t, err, context.DeadlineExceeded)
		} else {
			_, err := s.Publish(waiting, 11, 1, 1)
			require.ErrorIs(t, err, context.DeadlineExceeded)
		}
		cancel()
	}
	resume()
	require.NoError(t, <-done)
	_, err := s.Acquire(ctx, 11)
	require.ErrorIs(t, err, ErrPluginInstanceNotFound)
	require.Empty(t, *started)
}
func TestPluginInstanceDeleteClosedAndCancelled(t *testing.T) {
	s, r, _, api := piDeleteFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, api.Delete(ctx, 11, 1), context.Canceled)
	s.Close()
	require.ErrorIs(t, api.Delete(context.Background(), 11, 1), ErrGeneralClosed)
	require.Zero(t, r.calls.Load())
}

func TestPluginInstanceDeleteDoesNotTouchOtherInstanceLeases(t *testing.T) {
	ctx := context.Background()
	s, r, _, api := piDeleteFixture(t)
	r.s.Instance.Enabled = false
	other := &piFakeRuntime{}
	handle := &managedPluginInstance{instanceID: 99, revision: 1, refs: 1, runtime: other}
	s.current[99] = handle
	s.all[handle] = true
	s.lastErrors[99] = "runtime_call_failed"
	require.NoError(t, api.Delete(ctx, 11, 1))
	require.Same(t, handle, s.current[99])
	require.True(t, s.all[handle])
	require.Equal(t, 1, handle.refs)
	require.Zero(t, other.closed.Load())
	require.Equal(t, "runtime_call_failed", s.lastErrors[99])
	s.release(handle)
}
