package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pluginflow"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type piMemoryRepo struct {
	mu          sync.Mutex
	s           PluginInstanceSnapshot
	revisions   map[int64]string
	failPublish error
}

func newPIMemoryRepo() *piMemoryRepo {
	m := testPluginGeneralManifest()
	m.General.HostCapabilities = nil
	return &piMemoryRepo{s: PluginInstanceSnapshot{Instance: PluginInstance{ID: 11, PluginID: 3, Name: "check", Generation: 1, LatestRevision: 1, CurrentRevision: 1, Enabled: true}, Installation: PluginInstallation{ID: 3, PluginKey: m.ID, Version: m.Version, Manifest: m}, Bindings: []PluginGroupBinding{{ID: 1, InstanceID: 11, GroupID: 7, Stage: contract.Input, Entry: "inspect", Protocol: "openai.chat_completions.v1", Enabled: true, CanReplace: true}}}, revisions: map[int64]string{1: `{"marker":"alpha"}`}}
}
func (r *piMemoryRepo) snapshot(rev int64) *PluginInstanceSnapshot {
	if rev == 0 {
		rev = r.s.Instance.CurrentRevision
	}
	s, _ := clonePluginInstanceSnapshot(&r.s)
	s.Revision = PluginInstanceRevision{InstanceID: 11, Revision: rev, ConfigVersion: 1, ConfigEncrypted: r.revisions[rev]}
	return s
}
func (r *piMemoryRepo) Snapshot(ctx context.Context, id, rev int64) (*PluginInstanceSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != 11 {
		return nil, ErrPluginInstanceNotFound
	}
	if rev != 0 && r.revisions[rev] == "" {
		return nil, ErrPluginInstanceNotFound
	}
	return r.snapshot(rev), nil
}
func (r *piMemoryRepo) Delete(context.Context, int64, int64) error {
	return errors.New("not used; delete tests provide a deletion-aware repository")
}
func (r *piMemoryRepo) Create(context.Context, int64, string, string) (*PluginInstanceSnapshot, error) {
	return nil, errors.New("not used")
}
func (r *piMemoryRepo) List(context.Context, int64) ([]PluginInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []PluginInstance{r.s.Instance}, nil
}
func (r *piMemoryRepo) RevisionSummaries(context.Context, int64) ([]PluginInstanceRevisionSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]PluginInstanceRevisionSummary, 0, len(r.revisions))
	for revision := range r.revisions {
		result = append(result, PluginInstanceRevisionSummary{InstanceID: 11, Revision: revision, ConfigVersion: 1})
	}
	return result, nil
}
func (r *piMemoryRepo) SaveRevision(ctx context.Context, id, gen int64, c string) (*PluginInstanceSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.s.Instance.Generation {
		return nil, ErrPluginInstanceConflict
	}
	r.s.Instance.Generation++
	r.s.Instance.LatestRevision++
	r.revisions[r.s.Instance.LatestRevision] = c
	return r.snapshot(r.s.Instance.LatestRevision), nil
}
func (r *piMemoryRepo) Publish(ctx context.Context, id, gen, rev int64, enabled bool) (*PluginInstanceSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failPublish != nil {
		return nil, r.failPublish
	}
	if gen != r.s.Instance.Generation {
		return nil, ErrPluginInstanceConflict
	}
	if r.revisions[rev] == "" {
		return nil, ErrPluginInstanceNotFound
	}
	r.s.Instance.Generation++
	r.s.Instance.CurrentRevision = rev
	r.s.Instance.Enabled = enabled
	return r.snapshot(rev), nil
}
func (r *piMemoryRepo) ReplaceBindings(ctx context.Context, id, gen int64, b []PluginGroupBinding) (*PluginInstanceSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.s.Instance.Generation {
		return nil, ErrPluginInstanceConflict
	}
	r.s.Instance.Generation++
	r.s.Bindings = append([]PluginGroupBinding(nil), b...)
	return r.snapshot(0), nil
}
func (r *piMemoryRepo) GroupSnapshots(context.Context, int64, contract.Stage, string) ([]PluginInstanceSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []PluginInstanceSnapshot{*r.snapshot(0)}, nil
}

type piTestCipher struct{ err error }

func (c piTestCipher) Encrypt(s string) (string, error) { return s, c.err }
func (c piTestCipher) Decrypt(s string) (string, error) { return s, c.err }

type piFakeRuntime struct {
	marker string
	closed atomic.Int32
}

func (r *piFakeRuntime) Close() { r.closed.Add(1) }
func (r *piFakeRuntime) Err() error {
	if r.closed.Load() > 0 {
		return ErrGeneralClosed
	}
	return nil
}
func (r *piFakeRuntime) HookInvoker(entry, protocol string) pluginflow.Invoker {
	return func(context.Context, pluginflow.Binding, contract.HookRequest) (contract.HookResult, error) {
		return contract.HookResult{Decision: contract.Replace, Content: &contract.Content{MediaType: "text/plain", Data: []byte(r.marker)}}, nil
	}
}
func (r *piFakeRuntime) RunTask(context.Context, string, string, json.RawMessage) (contract.TaskResult, error) {
	return contract.TaskResult{Status: contract.TaskSucceeded}, nil
}
func piController(repo *piMemoryRepo) (*PluginInstanceService, *[]*piFakeRuntime) {
	started := []*piFakeRuntime{}
	s := newPluginInstanceService(repo, piTestCipher{}, func(ctx context.Context, p *PluginInstanceSnapshot, config json.RawMessage) (pluginInstanceRuntime, error) {
		var c struct {
			Marker string `json:"marker"`
		}
		if e := json.Unmarshal(config, &c); e != nil {
			return nil, e
		}
		r := &piFakeRuntime{marker: c.Marker}
		started = append(started, r)
		return r, nil
	})
	return s, &started
}
func TestPluginInstancePublicationDrainsOldReferences(t *testing.T) {
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	old, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	draft, e := s.SaveConfig(context.Background(), 11, 1, json.RawMessage(`{"marker":"beta"}`))
	require.NoError(t, e)
	published, e := s.Publish(context.Background(), 11, draft.Instance.Generation, draft.Revision.Revision)
	require.NoError(t, e)
	require.EqualValues(t, 2, published.Instance.CurrentRevision)
	require.EqualValues(t, 0, (*started)[0].closed.Load())
	fresh, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	require.Equal(t, "beta", (*started)[1].marker)
	fresh.Release()
	old.Release()
	old.Release()
	require.EqualValues(t, 1, (*started)[0].closed.Load())
	back, e := s.Publish(context.Background(), 11, published.Instance.Generation, 1)
	require.NoError(t, e)
	require.EqualValues(t, 1, back.Instance.CurrentRevision)
	require.Greater(t, back.Instance.Generation, published.Instance.Generation)
	again, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	require.Equal(t, "alpha", (*started)[2].marker)
	disabled, e := s.Disable(context.Background(), 11, back.Instance.Generation)
	require.NoError(t, e)
	require.False(t, disabled.Instance.Enabled)
	_, e = s.Acquire(context.Background(), 11)
	require.ErrorIs(t, e, ErrPluginInstanceUnavailable)
	require.EqualValues(t, 0, (*started)[2].closed.Load())
	again.Release()
	require.EqualValues(t, 1, (*started)[2].closed.Load())
	t.Log("PUBLICATION old=alpha new=beta rollback=alpha disabled_new_acquire=rejected")
}
func TestPluginInstancePublishFailurePreservesCurrent(t *testing.T) {
	for _, failure := range []string{"CAS", "startup"} {
		t.Run(failure, func(t *testing.T) {
			repo := newPIMemoryRepo()
			s, started := piController(repo)
			defer s.Close()
			old, e := s.Acquire(context.Background(), 11)
			require.NoError(t, e)
			defer old.Release()
			draft, e := s.SaveConfig(context.Background(), 11, 1, json.RawMessage(`{"marker":"beta"}`))
			require.NoError(t, e)
			if failure == "CAS" {
				repo.failPublish = ErrPluginInstanceConflict
			} else {
				s.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
					return nil, ErrGeneralHandshake
				}
			}
			_, e = s.Publish(context.Background(), 11, draft.Instance.Generation, 2)
			require.Error(t, e)
			require.EqualValues(t, 1, repo.s.Instance.CurrentRevision)
			require.EqualValues(t, 0, (*started)[0].closed.Load())
			if failure == "CAS" {
				require.EqualValues(t, 1, (*started)[1].closed.Load())
			}
		})
	}
}
func TestPluginInstanceRemoteDisableAndRestart(t *testing.T) {
	repo := newPIMemoryRepo()
	a, aa := piController(repo)
	b, bb := piController(repo)
	defer a.Close()
	defer b.Close()
	first, e := a.Acquire(context.Background(), 11)
	require.NoError(t, e)
	first.Release()
	second, e := b.Acquire(context.Background(), 11)
	require.NoError(t, e)
	second.Release()
	require.Len(t, *aa, 1)
	require.Len(t, *bb, 1)
	_, e = repo.Publish(context.Background(), 11, 1, 1, false)
	require.NoError(t, e)
	_, e = a.Acquire(context.Background(), 11)
	require.ErrorIs(t, e, ErrPluginInstanceUnavailable)
	require.EqualValues(t, 1, (*aa)[0].closed.Load())
	_, e = b.Acquire(context.Background(), 11)
	require.ErrorIs(t, e, ErrPluginInstanceUnavailable)
	require.EqualValues(t, 1, (*bb)[0].closed.Load())
}
func TestPluginInstanceInvalidConfigAndEncryption(t *testing.T) {
	repo := newPIMemoryRepo()
	s, _ := piController(repo)
	defer s.Close()
	for _, c := range []string{"null", "[]", "{", ""} {
		_, e := s.SaveConfig(context.Background(), 11, 1, json.RawMessage(c))
		require.ErrorIs(t, e, ErrPluginInstanceInvalid)
	}
	require.EqualValues(t, 1, repo.s.Instance.LatestRevision)
	s.encryptor = piTestCipher{err: errors.New("cipher failed")}
	_, e := s.SaveConfig(context.Background(), 11, 1, json.RawMessage(`{}`))
	require.Error(t, e)
	require.EqualValues(t, 1, repo.s.Instance.Generation)
}
func TestPluginInstanceCloseAndCancelledQueue(t *testing.T) {
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	a, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	s.Close()
	s.Close()
	a.Release()
	require.EqualValues(t, 1, (*started)[0].closed.Load())
	_, e = s.Acquire(context.Background(), 11)
	require.ErrorIs(t, e, ErrGeneralClosed)
	repo = newPIMemoryRepo()
	s, _ = piController(repo)
	defer s.Close()
	s.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = s.Acquire(ctx, 11)
	require.ErrorIs(t, e, context.Canceled)
	<-s.gate
}

func TestPluginInstanceRealProcessPublication(t *testing.T) {
	if testing.Short() {
		t.Skip("real subprocess test")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "general-fixture.exe")
	out, e := exec.Command("go", "build", "-o", binary, "./testdata/general-plugin").CombinedOutput()
	require.NoError(t, e, string(out))
	data, e := os.ReadFile(binary)
	require.NoError(t, e)
	digest := sha256.Sum256(data)
	repo := newPIMemoryRepo()
	repo.s.Installation.BinaryPath = binary
	repo.s.Installation.BinarySHA256 = hex.EncodeToString(digest[:])
	repo.s.Installation.Manifest.General.Hooks[0].Protocols = []string{"example.text.v1"}
	repo.s.Installation.Manifest.Files["bin/plugin"] = repo.s.Installation.BinarySHA256
	repo.s.Bindings[0].Protocol = "example.text.v1"
	running := []*generalPluginRuntime{}
	s := newPluginInstanceService(repo, piTestCipher{}, func(ctx context.Context, p *PluginInstanceSnapshot, c json.RawMessage) (pluginInstanceRuntime, error) {
		r, e := startGeneralPluginRuntime(ctx, &p.Installation, generalInstanceOptions{InstanceID: p.Instance.ID, ConfigRevision: p.Revision.Revision, Config: c, Host: PluginHostInfo{Version: "0.1.179"}, StartTimeout: 10 * time.Second, CallTimeout: time.Second, SocketDir: root})
		if e == nil {
			running = append(running, r)
		}
		return r, e
	})
	defer s.Close()
	req := contract.HookRequest{RequestID: "req", AttemptID: "attempt", GroupID: 7, Stage: contract.Input, Content: contract.Content{MediaType: "text/plain", Data: []byte("replace")}}
	check := func(l *PluginInstanceLease, marker string) {
		t.Helper()
		result, e := l.Check(context.Background(), 1, req)
		require.NoError(t, e)
		require.Equal(t, marker, string(result.Content.Data))
	}
	old, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	defer old.Release()
	check(old, "alpha")
	projection, e := old.Snapshot()
	require.NoError(t, e)
	projection.Bindings[0].GroupID = 99
	projection.Installation.Manifest.General.Hooks[0].Protocols[0] = "other"
	check(old, "alpha")
	draft, e := s.SaveConfig(context.Background(), 11, 1, json.RawMessage(`{"marker":"beta"}`))
	require.NoError(t, e)
	published, e := s.Publish(context.Background(), 11, draft.Instance.Generation, 2)
	require.NoError(t, e)
	fresh, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	defer fresh.Release()
	check(fresh, "beta")
	check(old, "alpha")
	require.NoError(t, running[0].life.Err())
	old.Release()
	require.ErrorIs(t, running[0].life.Err(), context.Canceled)
	back, e := s.Publish(context.Background(), 11, published.Instance.Generation, 1)
	require.NoError(t, e)
	rollback, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	defer rollback.Release()
	check(rollback, "alpha")
	check(fresh, "beta")
	task, e := rollback.RunTask(context.Background(), "sync_prices", "job", json.RawMessage(`{}`))
	require.NoError(t, e)
	require.JSONEq(t, `{"marker":"alpha"}`, string(task.Output))
	_, e = s.Disable(context.Background(), 11, back.Instance.Generation)
	require.NoError(t, e)
	_, e = s.Acquire(context.Background(), 11)
	require.ErrorIs(t, e, ErrPluginInstanceUnavailable)
	check(rollback, "alpha")
	rollback.Release()
	require.ErrorIs(t, running[2].life.Err(), context.Canceled)
	fresh.Release()
	require.ErrorIs(t, running[1].life.Err(), context.Canceled)
	t.Log("REAL_PROCESS old=alpha published=beta rollback=alpha disabled_new_acquire=rejected retired_processes=3")
}
func TestPluginInstanceGroupPartialFailureReleasesLeases(t *testing.T) {
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	repo.s.Instance.Enabled = false
	leases, e := s.AcquireGroup(context.Background(), 7, contract.Input, "openai.chat_completions.v1")
	require.ErrorIs(t, e, ErrPluginInstanceUnavailable)
	require.Empty(t, leases)
	require.Empty(t, *started)
}
func TestPluginInstanceConcurrentAcquireRelease(t *testing.T) {
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, e := s.Acquire(context.Background(), 11)
			if e != nil {
				t.Error(e)
				return
			}
			l.Release()
			l.Release()
		}()
	}
	wg.Wait()
	require.Len(t, *started, 1)
	_, e := s.Disable(context.Background(), 11, 1)
	require.NoError(t, e)
	require.EqualValues(t, 1, (*started)[0].closed.Load())
}

func TestPluginInstanceExpiredRuntimeIsNotReused(t *testing.T) {
	repo := newPIMemoryRepo()
	s, started := piController(repo)
	defer s.Close()
	old, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	// A runtime can close itself on RPC timeout/error while the service still owns a handle.
	(*started)[0].Close()
	fresh, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	defer fresh.Release()
	defer old.Release()
	require.Len(t, *started, 2, "a later request must get a new process, never retry the old call")
	require.Equal(t, "alpha", (*started)[1].marker)
}

func TestPluginInstanceRealProcessRecoversAfterRPCFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("real subprocess test")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "general-fixture.exe")
	out, e := exec.Command("go", "build", "-o", binary, "./testdata/general-plugin").CombinedOutput()
	require.NoError(t, e, string(out))
	data, e := os.ReadFile(binary)
	require.NoError(t, e)
	digest := sha256.Sum256(data)
	repo := newPIMemoryRepo()
	repo.s.Installation.BinaryPath = binary
	repo.s.Installation.BinarySHA256 = hex.EncodeToString(digest[:])
	repo.s.Installation.Manifest.Files["bin/plugin"] = repo.s.Installation.BinarySHA256
	repo.s.Installation.Manifest.General.Hooks[0].Protocols = []string{"example.text.v1"}
	repo.s.Bindings[0].Protocol = "example.text.v1"
	starts := 0
	s := newPluginInstanceService(repo, piTestCipher{}, func(ctx context.Context, p *PluginInstanceSnapshot, c json.RawMessage) (pluginInstanceRuntime, error) {
		r, e := startGeneralPluginRuntime(ctx, &p.Installation, generalInstanceOptions{InstanceID: p.Instance.ID, ConfigRevision: p.Revision.Revision, Config: c, Host: PluginHostInfo{Version: "0.1.179"}, StartTimeout: 10 * time.Second, CallTimeout: time.Second, SocketDir: root})
		if e == nil {
			starts++
		}
		return r, e
	})
	defer s.Close()
	failed, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	req := contract.HookRequest{RequestID: "req", AttemptID: "attempt", GroupID: 7, Stage: contract.Input, Content: contract.Content{MediaType: "text/plain", Data: []byte("error")}}
	_, e = failed.Check(context.Background(), 1, req)
	require.ErrorIs(t, e, ErrGeneralInvocation)
	require.Equal(t, 1, starts, "failed invocation is not retried")
	recovered, e := s.Acquire(context.Background(), 11)
	require.NoError(t, e)
	defer recovered.Release()
	failed.Release()
	require.Equal(t, 2, starts)
	count, e := recovered.RunTask(context.Background(), "sync_prices", "count", json.RawMessage(`{}`))
	require.NoError(t, e)
	require.JSONEq(t, `{"calls":0}`, string(count.Output))
	req.Content.Data = []byte("replace")
	result, e := recovered.Check(context.Background(), 1, req)
	require.NoError(t, e)
	require.Equal(t, "alpha", string(result.Content.Data))
	t.Log("REAL_RECOVERY failed_call_retries=0 next_acquire_new_process=true marker=alpha")
}

func TestPluginInstanceStartupErrorsClassifyRuntimeFailureWithoutPublishing(t *testing.T) {
	for _, cause := range []error{ErrGeneralHandshake, ErrGeneralConfig, errors.New("private-startup-config-path-cipher-canary"), context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			repo := newPIMemoryRepo()
			api, started := piController(repo)
			defer api.Close()
			old, err := api.Acquire(context.Background(), 11)
			require.NoError(t, err)
			defer old.Release()
			api.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
				return nil, cause
			}
			_, err = api.Publish(context.Background(), 11, 1, 1)
			require.ErrorIs(t, err, ErrPluginInstanceUnavailable)
			require.ErrorIs(t, err, cause)
			require.EqualValues(t, 1, repo.s.Instance.Generation)
			require.EqualValues(t, 1, repo.s.Instance.CurrentRevision)
			require.Zero(t, (*started)[0].closed.Load())
		})
	}
	t.Run("parent-cancellation-is-not-runtime-failure", func(t *testing.T) {
		repo := newPIMemoryRepo()
		api, _ := piController(repo)
		defer api.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		api.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
			cancel()
			return nil, context.Canceled
		}
		_, err := api.Publish(ctx, 11, 1, 1)
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ErrPluginInstanceUnavailable)
	})
}

func TestPluginInstancePublishRejectsInvalidArgumentsBeforeRuntimeStart(t *testing.T) {
	for _, args := range [][3]int64{{0, 1, 1}, {11, 0, 1}, {11, -1, 1}, {11, 1, 0}, {11, 1, -1}} {
		repo := newPIMemoryRepo()
		api, started := piController(repo)
		_, err := api.Publish(context.Background(), args[0], args[1], args[2])
		api.Close()
		require.ErrorIs(t, err, ErrPluginInstanceInvalid)
		require.Empty(t, *started)
		require.EqualValues(t, 1, repo.s.Instance.Generation)
	}
}

func TestPluginInstanceParentCancellationPreservesRuntimeErrorHistory(t *testing.T) {
	for _, operation := range []string{"publish", "acquire"} {
		for _, mode := range []string{"cancel", "parent-deadline", "candidate-timeout"} {
			for _, prior := range []string{"", "runtime_unavailable"} {
				t.Run(operation+"/"+mode+"/prior="+prior, func(t *testing.T) {
					repo := newPIMemoryRepo()
					api, started := piController(repo)
					defer api.Close()
					if operation == "publish" {
						old, err := api.Acquire(context.Background(), 11)
						require.NoError(t, err)
						defer old.Release()
					}
					api.recordInstanceError(11, prior)
					ctx, cancel := context.WithCancel(context.Background())
					if mode == "parent-deadline" {
						cancel()
						ctx, cancel = context.WithTimeout(context.Background(), 2*time.Millisecond)
					}
					defer cancel()
					api.start = func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
						if mode == "candidate-timeout" {
							return nil, context.DeadlineExceeded
						}
						if mode == "cancel" {
							cancel()
						} else {
							<-ctx.Done()
						}
						return nil, ctx.Err()
					}
					var err error
					if operation == "publish" {
						_, err = api.Publish(ctx, 11, 1, 1)
					} else {
						_, err = api.Acquire(ctx, 11)
					}
					state, stateErr := api.Status(context.Background(), 11)
					require.NoError(t, stateErr)
					if mode == "candidate-timeout" {
						require.ErrorIs(t, err, ErrPluginInstanceUnavailable)
						require.Equal(t, "runtime_start_failed", state.LastError)
					} else {
						require.ErrorIs(t, err, ctx.Err())
						require.NotErrorIs(t, err, ErrPluginInstanceUnavailable)
						require.Equal(t, prior, state.LastError)
					}
					require.EqualValues(t, 1, repo.s.Instance.Generation)
					require.EqualValues(t, 1, repo.s.Instance.CurrentRevision)
					if operation == "publish" {
						require.Zero(t, (*started)[0].closed.Load())
					}
				})
			}
		}
	}
}
