package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pluginflow"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
)

func TestPluginInstanceStatusDoesNotStartOrDecrypt(t *testing.T) {
	repo := newPIMemoryRepo()
	repo.s.Installation.InstallPath = "private-install-path"
	s, started := piController(repo)
	defer s.Close()
	s.encryptor = piTestCipher{err: errors.New("secret decryption must not run")}
	before, err := repo.Snapshot(context.Background(), 11, 0)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		status, err := s.Status(context.Background(), 11)
		require.NoError(t, err)
		require.EqualValues(t, 11, status.InstanceID)
		require.True(t, status.Enabled)
		require.EqualValues(t, 1, status.CurrentRevision)
		require.False(t, status.RuntimePresent)
		require.Zero(t, status.LeaseRefs)
		require.False(t, status.Retired)
		require.True(t, status.Closed)
		require.Empty(t, status.LastError)
		require.NotNil(t, status.Runtimes)
		require.Empty(t, status.Runtimes)
		data, err := json.Marshal(status)
		require.NoError(t, err)
		for _, secret := range []string{"private-install-path", "alpha", "marker", "config_encrypted", "manifest"} {
			require.NotContains(t, string(data), secret)
		}
	}
	require.Empty(t, *started)
	after, err := repo.Snapshot(context.Background(), 11, 0)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestPluginInstanceStatusTracksCurrentAndDrainingLeases(t *testing.T) {
	ctx := context.Background()
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	old, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	defer old.Release()
	draft, err := s.SaveConfig(ctx, 11, 1, json.RawMessage(`{"marker":"beta"}`))
	require.NoError(t, err)
	published, err := s.Publish(ctx, 11, draft.Instance.Generation, 2)
	require.NoError(t, err)
	fresh, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	defer fresh.Release()
	status, err := s.Status(ctx, 11)
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.EqualValues(t, 2, status.CurrentRevision)
	require.Equal(t, 2, status.LeaseRefs)
	require.True(t, status.RuntimePresent)
	require.True(t, status.Retired)
	require.False(t, status.Closed)
	require.Equal(t, []PluginInstanceRuntimeStatus{
		{Revision: 1, LeaseRefs: 1, Retired: true},
		{Revision: 2, LeaseRefs: 1, LocalCurrent: true},
	}, status.Runtimes)
	old.Release()
	status, err = s.Status(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, 1, status.LeaseRefs)
	require.False(t, status.Retired)
	require.Len(t, status.Runtimes, 1)
	require.EqualValues(t, 1, (*started)[0].closed.Load())
	_, err = s.Disable(ctx, 11, published.Instance.Generation)
	require.NoError(t, err)
	status, err = s.Status(ctx, 11)
	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.True(t, status.Retired)
	require.False(t, status.Closed)
	require.Equal(t, 1, status.LeaseRefs)
	require.False(t, status.Runtimes[0].LocalCurrent)
	fresh.Release()
	status, err = s.Status(ctx, 11)
	require.NoError(t, err)
	require.False(t, status.RuntimePresent)
	require.True(t, status.Closed)
	require.Zero(t, status.LeaseRefs)
	require.Empty(t, status.Runtimes)
	require.EqualValues(t, 1, (*started)[1].closed.Load())
}

func TestPluginInstanceStatusObservesRemoteAuthorityWithoutReconciliation(t *testing.T) {
	ctx := context.Background()
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	lease, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	defer lease.Release()
	draft, err := repo.SaveRevision(ctx, 11, 1, `{"marker":"remote"}`)
	require.NoError(t, err)
	_, err = repo.Publish(ctx, 11, draft.Instance.Generation, 2, false)
	require.NoError(t, err)
	status, err := s.Status(ctx, 11)
	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.EqualValues(t, 2, status.CurrentRevision)
	require.EqualValues(t, 1, status.Runtimes[0].Revision)
	require.True(t, status.Runtimes[0].LocalCurrent)
	require.False(t, status.Retired)
	require.False(t, status.Closed)
	require.Len(t, *started, 1)
	require.EqualValues(t, 0, (*started)[0].closed.Load(), "status must not retire or close a process")
}

func TestPluginInstanceStatusRedactsFailedPublicationAndClearsOnRecovery(t *testing.T) {
	ctx := context.Background()
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	lease, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	defer lease.Release()
	draft, err := s.SaveConfig(ctx, 11, 1, json.RawMessage(`{"marker":"beta"}`))
	require.NoError(t, err)
	start := s.start
	s.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
		return nil, errors.New("credential=secret-token path=D:/private/plugin/config")
	}
	_, err = s.Publish(ctx, 11, draft.Instance.Generation, 2)
	require.Error(t, err)
	status, err := s.Status(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, "runtime_start_failed", status.LastError)
	require.EqualValues(t, 1, status.CurrentRevision)
	require.EqualValues(t, 0, (*started)[0].closed.Load())
	data, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-token")
	require.NotContains(t, string(data), "D:/private")
	s.start = start
	_, err = s.Publish(ctx, 11, draft.Instance.Generation, 2)
	require.NoError(t, err)
	status, err = s.Status(ctx, 11)
	require.NoError(t, err)
	require.Empty(t, status.LastError)
}

func TestPluginInstanceStatusReportsPublicationCASFailure(t *testing.T) {
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	repo.failPublish = ErrPluginInstanceConflict
	_, err := s.Publish(context.Background(), 11, 1, 1)
	require.ErrorIs(t, err, ErrPluginInstanceConflict)
	status, err := s.Status(context.Background(), 11)
	require.NoError(t, err)
	require.Equal(t, "publication_conflict", status.LastError)
	require.False(t, status.RuntimePresent)
	require.EqualValues(t, 1, (*started)[0].closed.Load())
}

func TestPluginInstanceStatusObservesCrashedRuntimeWithoutRestart(t *testing.T) {
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	lease, err := s.Acquire(context.Background(), 11)
	require.NoError(t, err)
	defer lease.Release()
	(*started)[0].Close()
	status, err := s.Status(context.Background(), 11)
	require.NoError(t, err)
	require.Equal(t, "runtime_unavailable", status.LastError)
	require.True(t, status.RuntimePresent)
	require.True(t, status.Closed)
	require.True(t, status.Runtimes[0].Closed)
	require.Len(t, *started, 1)
	require.EqualValues(t, 1, (*started)[0].closed.Load())
}

func TestPluginInstanceStatusErrorsDoNotStartRuntime(t *testing.T) {
	s, started := piController(newPIMemoryRepo())
	defer s.Close()
	for _, id := range []int64{0, -1} {
		_, err := s.Status(context.Background(), id)
		require.ErrorIs(t, err, ErrPluginInstanceInvalid)
	}
	_, err := s.Status(context.Background(), 99)
	require.ErrorIs(t, err, ErrPluginInstanceNotFound)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Status(ctx, 11)
	require.ErrorIs(t, err, context.Canceled)
	s.Close()
	_, err = s.Status(context.Background(), 11)
	require.ErrorIs(t, err, ErrGeneralClosed)
	require.Empty(t, *started)
}

func TestPluginInstanceStatusConcurrentReadAndRelease(t *testing.T) {
	s, _ := piController(newPIMemoryRepo())
	defer s.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				lease, err := s.Acquire(context.Background(), 11)
				if err != nil {
					errs <- err
					return
				}
				status, err := s.Status(context.Background(), 11)
				lease.Release()
				if err != nil {
					errs <- err
					return
				}
				if status.LeaseRefs < 1 {
					errs <- errors.New("active lease absent from status")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	status, err := s.Status(context.Background(), 11)
	require.NoError(t, err)
	require.Zero(t, status.LeaseRefs)
}

// Freeze the status Err observation while a normal retired lease drains. The
// pause is consumed once so release's own lifecycle observation stays bounded.
type piStatusPausedRuntime struct {
	piFakeRuntime
	pause    atomic.Bool
	observed chan struct{}
	resume   chan struct{}
}

func (r *piStatusPausedRuntime) Err() error {
	if r.pause.CompareAndSwap(true, false) {
		close(r.observed)
		<-r.resume
	}
	return r.piFakeRuntime.Err()
}

func TestPluginInstanceStatusNormalDrainDoesNotReportRuntimeFailure(t *testing.T) {
	ctx := context.Background()
	repo := newPIMemoryRepo()
	oldRuntime := &piStatusPausedRuntime{observed: make(chan struct{}), resume: make(chan struct{})}
	s := newPluginInstanceService(repo, piTestCipher{}, func(_ context.Context, snap *PluginInstanceSnapshot, _ json.RawMessage) (pluginInstanceRuntime, error) {
		if snap.Revision.Revision == 1 {
			return oldRuntime, nil
		}
		return &piFakeRuntime{}, nil
	})
	defer s.Close()
	lease, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	defer lease.Release()
	draft, err := s.SaveConfig(ctx, 11, 1, json.RawMessage(`{"marker":"beta"}`))
	require.NoError(t, err)
	_, err = s.Publish(ctx, 11, draft.Instance.Generation, 2)
	require.NoError(t, err)
	oldRuntime.pause.Store(true)
	result := make(chan *PluginInstanceStatus, 1)
	failed := make(chan error, 1)
	go func() {
		status, err := s.Status(ctx, 11)
		if err != nil {
			failed <- err
			return
		}
		result <- status
	}()
	select {
	case <-oldRuntime.observed:
	case <-time.After(3 * time.Second):
		close(oldRuntime.resume)
		t.Fatal("status never observed retired runtime")
	}
	lease.Release()
	close(oldRuntime.resume)
	select {
	case status := <-result:
		require.Empty(t, status.LastError, "normal drain is not a crash")
	case err := <-failed:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("status did not finish after normal drain")
	}
}

func TestPluginInstanceStatusRetainsRuntimeFailureAfterDisableAndDrain(t *testing.T) {
	ctx := context.Background()
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	lease, err := s.Acquire(ctx, 11)
	require.NoError(t, err)
	defer lease.Release()
	(*started)[0].Close() // Simulate abnormal exit before any host Close.
	status, err := s.Status(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, "runtime_unavailable", status.LastError)
	_, err = s.Disable(ctx, 11, 1)
	require.NoError(t, err)
	lease.Release()
	status, err = s.Status(ctx, 11)
	require.NoError(t, err)
	require.False(t, status.RuntimePresent)
	require.Equal(t, "runtime_unavailable", status.LastError, "draining must not erase a known failure")
	require.Equal(t, "runtime_unavailable", s.lastErrors[11], "failure is recorded by lifecycle handling, not GET")
}

type piStatusFailingRuntime struct{ piFakeRuntime }

func (r *piStatusFailingRuntime) RunTask(context.Context, string, string, json.RawMessage) (contract.TaskResult, error) {
	return contract.TaskResult{}, errors.New("private-task-path token=secret-call-token")
}
func (r *piStatusFailingRuntime) HookInvoker(string, string) pluginflow.Invoker {
	return func(context.Context, pluginflow.Binding, contract.HookRequest) (contract.HookResult, error) {
		return contract.HookResult{}, errors.New("private-hook-path token=secret-call-token")
	}
}

func TestPluginInstanceStatusRetainsCallFailureUntilSuccessfulInstall(t *testing.T) {
	for _, method := range []string{"hook", "task"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			repo := newPIMemoryRepo()
			s := newPluginInstanceService(repo, piTestCipher{}, func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
				return &piStatusFailingRuntime{}, nil
			})
			defer s.Close()
			lease, err := s.Acquire(ctx, 11)
			require.NoError(t, err)
			defer lease.Release()
			if method == "task" {
				_, err = lease.RunTask(ctx, "sync", "job", json.RawMessage(`{}`))
			} else {
				_, err = lease.Check(ctx, 1, contract.HookRequest{GroupID: 7, Stage: contract.Input})
			}
			require.Error(t, err)
			status, err := s.Status(ctx, 11)
			require.NoError(t, err)
			require.Equal(t, "runtime_call_failed", status.LastError)
			data, err := json.Marshal(status)
			require.NoError(t, err)
			require.NotContains(t, string(data), "secret-call-token")
			require.NotContains(t, string(data), "private-")
			disabled, err := s.Disable(ctx, 11, 1)
			require.NoError(t, err)
			lease.Release()
			status, err = s.Status(ctx, 11)
			require.NoError(t, err)
			require.Equal(t, "runtime_call_failed", status.LastError)
			_, err = s.Publish(ctx, 11, disabled.Instance.Generation, 1)
			require.NoError(t, err)
			status, err = s.Status(ctx, 11)
			require.NoError(t, err)
			require.Empty(t, status.LastError)
		})
	}
}

func TestPluginInstanceStatusSupersededFailureCannotUndoRecovery(t *testing.T) {
	for _, replacement := range []string{"new_revision", "same_revision_restart"} {
		for _, action := range []string{"observe", "release", "hook", "task"} {
			t.Run(replacement+"/"+action, func(t *testing.T) {
				ctx := context.Background()
				repo := newPIMemoryRepo()
				s, started := piController(repo)
				defer s.Close()
				old, err := s.Acquire(ctx, 11)
				require.NoError(t, err)
				defer old.Release()
				(*started)[0].Close() // Fault occurred BEFORE successful recovery.
				generation := int64(1)
				if replacement == "new_revision" {
					draft, err := s.SaveConfig(ctx, 11, 1, json.RawMessage(`{"marker":"beta"}`))
					require.NoError(t, err)
					published, err := s.Publish(ctx, 11, draft.Instance.Generation, 2)
					require.NoError(t, err)
					generation = published.Instance.Generation
				} else {
					fresh, err := s.Acquire(ctx, 11)
					require.NoError(t, err)
					fresh.Release()
				}
				require.Empty(t, s.lastErrors[11], "successful install clears old failure history")
				if action == "observe" {
					status, err := s.Status(ctx, 11)
					require.NoError(t, err)
					require.Empty(t, status.LastError, "a superseded handle cannot override a recovered runtime")
					return
				}
				_, err = s.Disable(ctx, 11, generation)
				require.NoError(t, err)
				require.Empty(t, s.lastErrors[11], "healthy disable must preserve recovery")
				switch action {
				case "hook":
					_, err = old.Check(ctx, 1, contract.HookRequest{GroupID: 7, Stage: contract.Input})
					require.ErrorIs(t, err, ErrGeneralClosed)
				case "task":
					_, err = old.RunTask(ctx, "sync", "job", json.RawMessage(`{}`))
					require.ErrorIs(t, err, ErrGeneralClosed)
				}
				require.Empty(t, s.lastErrors[11], "late old calls must not resurrect old failures when current is nil")
				old.Release()
				status, err := s.Status(ctx, 11)
				require.NoError(t, err)
				require.False(t, status.RuntimePresent)
				require.True(t, status.Closed)
				require.Empty(t, status.LastError, "old drain must not undo recovery after a subsequent disable")
			})
		}
	}
}
