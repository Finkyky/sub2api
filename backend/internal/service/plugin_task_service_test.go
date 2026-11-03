package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pluginflow"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
)

type ptMemoryRepo struct {
	mu        sync.Mutex
	task      PluginTask
	run       PluginTaskRun
	active    bool
	finishErr error
	calls     int
}

func ptTestTask() PluginTask {
	return PluginTask{ID: 1, InstanceID: 11, Name: "task", Entry: "sync_prices", Enabled: true, TimeoutSeconds: 30, RetryBackoffSeconds: 1, Generation: 1, Input: json.RawMessage(`{"business":"public"}`)}
}
func (r *ptMemoryRepo) List(context.Context, int64) ([]PluginTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []PluginTask{r.task}, nil
}
func (r *ptMemoryRepo) Get(context.Context, int64) (*PluginTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.task
	return &t, nil
}
func (r *ptMemoryRepo) Create(ctx context.Context, t *PluginTask) (*PluginTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.task = *t
	return t, nil
}
func (r *ptMemoryRepo) Update(ctx context.Context, t *PluginTask, g int64) (*PluginTask, error) {
	return r.Create(ctx, t)
}
func (r *ptMemoryRepo) Delete(context.Context, int64, int64) error { return nil }
func (r *ptMemoryRepo) Due(context.Context, int) ([]int64, error)  { return nil, nil }
func (r *ptMemoryRepo) Claim(ctx context.Context, id int64, owner, trigger, key string, ttl time.Duration) (*PluginTaskClaim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if !r.task.Enabled {
		return nil, ErrPluginTaskDisabled
	}
	if r.active {
		return nil, ErrPluginTaskConflict
	}
	if r.run.ID > 0 && r.run.IdempotencyKey == key {
		return &PluginTaskClaim{Task: r.task, Run: r.run}, nil
	}
	r.active = true
	r.run = PluginTaskRun{ID: 1, TaskID: id, InstanceID: 11, ConfigRevision: 1, TaskGeneration: 1, Trigger: trigger, IdempotencyKey: key, Attempt: 1, FencingToken: 1, Status: "running", StartedAt: time.Now()}
	return &PluginTaskClaim{Task: r.task, Run: r.run, Owner: owner, LeaseUntil: time.Now().Add(ttl), InstanceGeneration: 1}, nil
}
func (r *ptMemoryRepo) Finish(ctx context.Context, c *PluginTaskClaim, run PluginTaskRun, callback func(*sql.Tx) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finishErr != nil {
		return r.finishErr
	}
	if callback != nil {
		if e := callback(nil); e != nil {
			return e
		}
	}
	r.run = run
	r.active = false
	now := time.Now()
	r.run.FinishedAt = &now
	return nil
}
func (r *ptMemoryRepo) Runs(context.Context, int64, int) ([]PluginTaskRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run.ID == 0 {
		return []PluginTaskRun{}, nil
	}
	return []PluginTaskRun{r.run}, nil
}
func (r *ptMemoryRepo) Run(context.Context, int64) (*PluginTaskRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r2 := r.run
	return &r2, nil
}

type ptCipher struct{ fail bool }

func (c ptCipher) Encrypt(s string) (string, error) {
	if c.fail {
		return "", errors.New("secret encryption")
	}
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (c ptCipher) Decrypt(s string) (string, error) {
	b, e := base64.StdEncoding.DecodeString(s)
	return string(b), e
}

type ptRuntime struct {
	call  func(context.Context, string, string, json.RawMessage) (contract.TaskResult, error)
	calls atomic.Int32
}

func (r *ptRuntime) Close()                                        {}
func (r *ptRuntime) Err() error                                    { return nil }
func (r *ptRuntime) HookInvoker(string, string) pluginflow.Invoker { return nil }
func (r *ptRuntime) RunTask(ctx context.Context, entry, key string, input json.RawMessage) (contract.TaskResult, error) {
	r.calls.Add(1)
	return r.call(ctx, entry, key, input)
}

type ptBroker struct {
	prepareErr, commitErr      error
	prepared, committed, after atomic.Int32
	input                      json.RawMessage
	commit                     func()
}

func (b *ptBroker) Prepare(ctx context.Context, c *PluginTaskClaim, s *PluginInstanceSnapshot) (json.RawMessage, error) {
	b.prepared.Add(1)
	if b.input != nil {
		return b.input, b.prepareErr
	}
	return json.RawMessage(`{"input":{"business":"public"},"host":{}}`), b.prepareErr
}
func (b *ptBroker) Commit(ctx context.Context, tx *sql.Tx, c *PluginTaskClaim, result contract.TaskResult) error {
	b.committed.Add(1)
	if b.commit != nil {
		b.commit()
	}
	return b.commitErr
}
func (b *ptBroker) AfterCommit() { b.after.Add(1) }
func ptService(t *testing.T, call func(context.Context, string, string, json.RawMessage) (contract.TaskResult, error)) (*PluginTaskService, *ptMemoryRepo, *ptBroker, *ptRuntime) {
	t.Helper()
	repo := &ptMemoryRepo{task: ptTestTask()}
	runtime := &ptRuntime{call: call}
	ir := newPIMemoryRepo()
	instances := newPluginInstanceService(ir, piTestCipher{}, func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error) {
		return runtime, nil
	})
	broker := &ptBroker{}
	s := NewPluginTaskService(repo, instances, broker, ptCipher{})
	t.Cleanup(instances.Close)
	t.Cleanup(s.Close)
	return s, repo, broker, runtime
}
func ptOK(context.Context, string, string, json.RawMessage) (contract.TaskResult, error) {
	return contract.TaskResult{Status: contract.TaskSucceeded, Changed: true, Output: json.RawMessage(`{"secret":"result-canary"}`)}, nil
}
func TestPluginTaskServiceSuccessEncryptedReplay(t *testing.T) {
	s, repo, b, runtime := ptService(t, ptOK)
	ctx := context.Background()
	run, e := s.Execute(ctx, 1, "caller-key")
	require.NoError(t, e)
	require.Equal(t, "succeeded", run.Status)
	require.Empty(t, run.ResultEncrypted)
	require.EqualValues(t, 1, b.after.Load())
	encoded, _ := json.Marshal(run)
	require.NotContains(t, string(encoded), "canary")
	require.NotContains(t, repo.run.ResultEncrypted, "canary")
	plainRun, raw, e := s.RunSensitive(ctx, 1)
	require.NoError(t, e)
	require.Empty(t, plainRun.ResultEncrypted)
	require.Contains(t, string(raw), "result-canary")
	require.Contains(t, string(raw), `"status":"succeeded"`)
	replay, e := s.Execute(ctx, 1, "caller-key")
	require.NoError(t, e)
	require.Equal(t, run.ID, replay.ID)
	require.EqualValues(t, 1, runtime.calls.Load())
	require.EqualValues(t, 1, b.committed.Load())
	runs, e := s.Runs(ctx, 1, 10)
	require.NoError(t, e)
	require.Empty(t, runs[0].ResultEncrypted)
	normal, e := s.Run(ctx, 1)
	require.NoError(t, e)
	require.Empty(t, normal.ResultEncrypted)
}
func TestPluginTaskServiceFailureNeverCommits(t *testing.T) {
	for _, kind := range []string{"runtime", "failed", "invalid", "oversize", "prepare", "encrypt", "commit", "fence"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, b, runtime := ptService(t, func(ctx context.Context, entry, key string, input json.RawMessage) (contract.TaskResult, error) {
				switch kind {
				case "runtime":
					return contract.TaskResult{}, errors.New("secret runtime")
				case "failed":
					return contract.TaskResult{Status: contract.TaskFailed, Changed: true, ErrorCode: "plugin_secret", Output: json.RawMessage(`{"canary":"sensitive"}`)}, nil
				case "invalid":
					return contract.TaskResult{Status: "wrong"}, nil
				case "oversize":
					return contract.TaskResult{Status: contract.TaskSucceeded, Output: json.RawMessage(`"` + strings.Repeat("x", PluginTaskMaxResultBytes) + `"`)}, nil
				}
				return ptOK(ctx, entry, key, input)
			})
			if kind == "prepare" {
				b.prepareErr = errors.New("secret prepare")
			}
			if kind == "encrypt" {
				s.encryptor = ptCipher{fail: true}
			}
			if kind == "commit" {
				b.commitErr = errors.New("secret commit")
			}
			if kind == "fence" {
				repo.finishErr = ErrPluginTaskConflict
			}
			run, e := s.Execute(context.Background(), 1, "required")
			if kind == "fence" {
				require.ErrorIs(t, e, ErrPluginTaskConflict)
			} else {
				require.NoError(t, e)
				require.Equal(t, "failed", run.Status)
				require.False(t, run.Changed)
				require.NotContains(t, run.ErrorCode, "secret")
			}
			require.Zero(t, b.after.Load())
			if kind != "commit" {
				require.Zero(t, b.committed.Load())
			}
			if kind == "prepare" {
				require.Zero(t, runtime.calls.Load())
			}
		})
	}
}
func TestPluginTaskServiceRequiresKeyAndDisabled(t *testing.T) {
	s, repo, b, runtime := ptService(t, ptOK)
	_, e := s.Execute(context.Background(), 1, "")
	require.ErrorIs(t, e, ErrPluginTaskInvalid)
	require.Zero(t, repo.calls)
	repo.task.Enabled = false
	_, e = s.Execute(context.Background(), 1, "valid")
	require.ErrorIs(t, e, ErrPluginTaskDisabled)
	require.Zero(t, runtime.calls.Load())
	require.Zero(t, b.prepared.Load())
	task := ptTestTask()
	task.Entry = "undeclared"
	_, e = s.Create(context.Background(), &task)
	require.ErrorIs(t, e, ErrPluginTaskInvalid)
}
func TestPluginTaskServiceCloseCancelsAndWaitsWithoutLock(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	s, _, b, _ := ptService(t, func(ctx context.Context, _, _ string, _ json.RawMessage) (contract.TaskResult, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return contract.TaskResult{}, ctx.Err()
	})
	finished := make(chan struct{})
	go func() { defer close(finished); _, _ = s.Execute(context.Background(), 1, "slow") }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, e := s.List(ctx, 11)
	require.NoError(t, e)
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not drain")
	}
	<-finished
	<-exited
	require.Zero(t, b.after.Load())
	_, e = s.Execute(context.Background(), 1, "after")
	require.ErrorIs(t, e, ErrGeneralClosed)
}
func TestPluginTaskRuntimeDeadlineNotHookTimeout(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &generalPluginRuntime{life: life, callTimeout: 10 * time.Millisecond, slot: make(chan struct{}, 1)}
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	call, done, e := r.beginTask(ctx)
	require.NoError(t, e)
	defer done()
	deadline, _ := call.Deadline()
	require.Greater(t, time.Until(deadline), 100*time.Millisecond)
}
func TestPluginTaskSchedulerStopIdempotent(t *testing.T) {
	s, _, _, _ := ptService(t, ptOK)
	scheduler := NewPluginTaskScheduler(s)
	scheduler.Start()
	scheduler.Start()
	scheduler.Stop()
	scheduler.Stop()
	_, e := s.Execute(context.Background(), 1, "stopped")
	require.ErrorIs(t, e, ErrGeneralClosed)
}

type ptDiscardBroker struct {
	*ptBroker
	discarded atomic.Int32
}

func (b *ptDiscardBroker) Discard(*PluginTaskClaim) { b.discarded.Add(1) }
func TestPluginTaskServiceAlwaysDiscardsPreparedGrant(t *testing.T) {
	for _, kind := range []string{"success", "failure", "fence"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, b, _ := ptService(t, ptOK)
			wrapped := &ptDiscardBroker{ptBroker: b}
			s.broker = wrapped
			if kind == "failure" {
				b.prepareErr = errors.New("secret")
			}
			if kind == "fence" {
				repo.finishErr = ErrPluginTaskConflict
			}
			_, _ = s.Execute(context.Background(), 1, "discard")
			require.EqualValues(t, 1, wrapped.discarded.Load())
			if kind == "success" {
				_, e := s.Execute(context.Background(), 1, "discard")
				require.NoError(t, e)
				require.EqualValues(t, 1, wrapped.discarded.Load())
				require.EqualValues(t, 1, b.prepared.Load())
			}
		})
	}
}

func TestPluginTaskSchedulerDoesNotLetObsoleteRetryBlockCron(t *testing.T) {
	s, repo, b, runtime := ptService(t, ptOK)
	repo.task.MaxRetries = 1
	repo.task.Cron = "@every 1m"
	due := time.Now().Add(-time.Minute)
	repo.task.NextRunAt = &due
	repo.run = PluginTaskRun{ID: 1, TaskID: 1, InstanceID: 11, TaskGeneration: 1, ConfigRevision: 1, IdempotencyKey: "obsolete-retry", Attempt: 1, Status: "failed"}
	scheduler := NewPluginTaskScheduler(s)
	scheduler.dispatch(context.Background(), 1)
	require.Equal(t, 2, repo.calls)
	require.EqualValues(t, 1, runtime.calls.Load())
	require.EqualValues(t, 1, b.committed.Load())
}

type ptRetryKeyRepo struct {
	*ptMemoryRepo
	historyCalls atomic.Int32
	keyCalls     atomic.Int32
}

func (r *ptRetryKeyRepo) RetryKeys(context.Context, int64, int) ([]string, error) {
	r.keyCalls.Add(1)
	return []string{"sql-key"}, nil
}
func (r *ptRetryKeyRepo) Runs(context.Context, int64, int) ([]PluginTaskRun, error) {
	r.historyCalls.Add(1)
	return nil, errors.New("production discovery must not scan recent history")
}
func TestPluginTaskSchedulerPrefersSQLRetryKeys(t *testing.T) {
	s, repo, b, runtime := ptService(t, ptOK)
	sqlKeys := &ptRetryKeyRepo{ptMemoryRepo: repo}
	s.repo = sqlKeys
	scheduler := NewPluginTaskScheduler(s)
	scheduler.dispatch(context.Background(), 1)
	require.EqualValues(t, 1, sqlKeys.keyCalls.Load())
	require.Zero(t, sqlKeys.historyCalls.Load())
	require.EqualValues(t, 1, runtime.calls.Load())
	require.EqualValues(t, 1, b.committed.Load())
}
