package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

// PluginTaskService owns task calls, not the shared instance controller. Close
// cancels all calls and drains their leases before returning; the owner closes
// PluginInstanceService after this service and the scheduler have stopped.
type PluginTaskService struct {
	repo      PluginTaskRepository
	instances *PluginInstanceService
	broker    PluginTaskBroker
	encryptor SecretEncryptor
	owner     string
	life      context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	wg        sync.WaitGroup
	closeDone chan struct{}
}

func NewPluginTaskService(repo PluginTaskRepository, instances *PluginInstanceService, broker PluginTaskBroker, encryptor SecretEncryptor) *PluginTaskService {
	life, cancel := context.WithCancel(context.Background())
	var token [16]byte
	if _, e := rand.Read(token[:]); e != nil {
		cancel()
	}
	return &PluginTaskService{repo: repo, instances: instances, broker: broker, encryptor: encryptor, owner: hex.EncodeToString(token[:]), life: life, cancel: cancel, closeDone: make(chan struct{})}
}
func (s *PluginTaskService) ready(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed || s.life.Err() != nil {
		return ErrGeneralClosed
	}
	if s.repo == nil {
		return ErrPluginTaskInvalid
	}
	return nil
}
func (s *PluginTaskService) work(ctx context.Context) (context.Context, func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	s.mu.Lock()
	if s.closed || s.life.Err() != nil {
		s.mu.Unlock()
		return nil, nil, ErrGeneralClosed
	}
	s.wg.Add(1)
	s.mu.Unlock()
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.life, cancel)
	return call, func() { stop(); cancel(); s.wg.Done() }, nil
}
func (s *PluginTaskService) List(ctx context.Context, instanceID int64) ([]PluginTask, error) {
	if e := s.ready(ctx); e != nil {
		return nil, e
	}
	return s.repo.List(ctx, instanceID)
}
func (s *PluginTaskService) Get(ctx context.Context, id int64) (*PluginTask, error) {
	if e := s.ready(ctx); e != nil {
		return nil, e
	}
	return s.repo.Get(ctx, id)
}
func (s *PluginTaskService) validateDefinition(ctx context.Context, task *PluginTask) (*PluginTask, error) {
	copy, e := pluginTaskClone(task)
	if e != nil {
		return nil, e
	}
	if s.instances == nil {
		return nil, ErrPluginTaskInvalid
	}
	if e = ValidatePluginTaskDefinition(*copy); e != nil {
		return nil, e
	}
	// A disabled instance can own drafts. Execution alone requires enabled.
	snapshot, e := s.instances.Snapshot(ctx, copy.InstanceID, 0)
	if e != nil {
		return nil, e
	}
	if e = ValidatePluginTask(*copy, snapshot.Installation.Manifest); e != nil {
		return nil, e
	}
	return copy, nil
}
func (s *PluginTaskService) Create(ctx context.Context, task *PluginTask) (*PluginTask, error) {
	if e := s.ready(ctx); e != nil {
		return nil, e
	}
	copy, e := s.validateDefinition(ctx, task)
	if e != nil {
		return nil, e
	}
	return s.repo.Create(ctx, copy)
}
func (s *PluginTaskService) Update(ctx context.Context, task *PluginTask, expected int64) (*PluginTask, error) {
	if e := s.ready(ctx); e != nil {
		return nil, e
	}
	copy, e := s.validateDefinition(ctx, task)
	if e != nil {
		return nil, e
	}
	return s.repo.Update(ctx, copy, expected)
}
func (s *PluginTaskService) Delete(ctx context.Context, id, expected int64) error {
	if e := s.ready(ctx); e != nil {
		return e
	}
	return s.repo.Delete(ctx, id, expected)
}
func pluginTaskPublicRun(run *PluginTaskRun) *PluginTaskRun {
	if run == nil {
		return nil
	}
	copy := *run
	copy.ResultEncrypted = ""
	return &copy
}
func (s *PluginTaskService) Runs(ctx context.Context, id int64, limit int) ([]PluginTaskRun, error) {
	if e := s.ready(ctx); e != nil {
		return nil, e
	}
	runs, e := s.repo.Runs(ctx, id, limit)
	if e != nil {
		return nil, e
	}
	public := make([]PluginTaskRun, len(runs))
	for i := range runs {
		public[i] = *pluginTaskPublicRun(&runs[i])
	}
	return public, nil
}
func (s *PluginTaskService) Run(ctx context.Context, id int64) (*PluginTaskRun, error) {
	if e := s.ready(ctx); e != nil {
		return nil, e
	}
	run, e := s.repo.Run(ctx, id)
	return pluginTaskPublicRun(run), e
}
func (s *PluginTaskService) RunSensitive(ctx context.Context, id int64) (*PluginTaskRun, json.RawMessage, error) {
	if e := s.ready(ctx); e != nil {
		return nil, nil, e
	}
	run, e := s.repo.Run(ctx, id)
	if e != nil {
		return nil, nil, e
	}
	public := pluginTaskPublicRun(run)
	if run.ResultEncrypted == "" {
		return public, nil, nil
	}
	if s.encryptor == nil {
		return nil, nil, ErrPluginTaskInvalid
	}
	plain, e := s.encryptor.Decrypt(run.ResultEncrypted)
	if e != nil || len(plain) > PluginTaskMaxResultBytes || !json.Valid([]byte(plain)) {
		return nil, nil, ErrPluginTaskInvalid
	}
	return public, json.RawMessage(plain), nil
}
func PluginTaskScheduledKey(task PluginTask) string {
	if task.NextRunAt == nil {
		return ""
	}
	return fmt.Sprintf("scheduled:%d:%s", task.ID, task.NextRunAt.UTC().Format(time.RFC3339Nano))
}
func (s *PluginTaskService) Execute(ctx context.Context, id int64, key string) (*PluginTaskRun, error) {
	return s.execute(ctx, id, "manual", key)
}
func (s *PluginTaskService) execute(ctx context.Context, id int64, trigger, key string) (*PluginTaskRun, error) {
	if id <= 0 || !pluginTaskKeyValid(key) {
		return nil, ErrPluginTaskInvalid
	}
	call, release, e := s.work(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	// Bound even the pre-claim repository calls. The task-specific deadline starts
	// before Claim and includes broker preparation, instance start, RPC and commit.
	call, limit := context.WithTimeout(call, PluginTaskMaxTimeout+10*time.Second)
	defer limit()
	if s.repo == nil || s.instances == nil || s.broker == nil || s.encryptor == nil {
		return nil, ErrPluginTaskInvalid
	}
	task, e := s.repo.Get(call, id)
	if e != nil {
		return nil, e
	}
	if !task.Enabled {
		return nil, ErrPluginTaskDisabled
	}
	if task.TimeoutSeconds < 1 || task.TimeoutSeconds > 300 {
		return nil, ErrPluginTaskInvalid
	}
	runCtx, stop := context.WithTimeout(call, time.Duration(task.TimeoutSeconds)*time.Second)
	defer stop()
	claim, e := s.repo.Claim(runCtx, id, s.owner, trigger, key, time.Duration(task.TimeoutSeconds)*time.Second+10*time.Second)
	if e != nil {
		// Claim never grants two live runs. A duplicate HTTP submission can still
		// replay the already running/terminal DTO without acquiring business rights.
		if errors.Is(e, ErrPluginTaskConflict) {
			runs, err := s.repo.Runs(runCtx, id, 100)
			if err == nil {
				for _, run := range runs {
					if run.IdempotencyKey == key && (run.Status == "running" || run.Status == "succeeded") {
						return pluginTaskPublicRun(&run), nil
					}
				}
			}
		}
		return nil, e
	}
	if claim == nil {
		return nil, ErrPluginTaskConflict
	}
	if claim.Run.Status != "running" || claim.Owner == "" {
		return pluginTaskPublicRun(&claim.Run), nil
	}
	// Optional broker cleanup keeps failed/cancelled/rejected grants bounded.
	if discard, ok := s.broker.(interface{ Discard(*PluginTaskClaim) }); ok {
		defer discard.Discard(claim)
	}
	outcome := claim.Run
	outcome.Status = "failed"
	outcome.Changed = false
	outcome.ErrorCode = "execution_failed"
	var result contract.TaskResult
	resultValid := false
	lease, e := s.instances.Acquire(runCtx, claim.Task.InstanceID)
	if e != nil {
		outcome.ErrorCode = "instance_unavailable"
	} else {
		defer lease.Release()
		snapshot, err := lease.Snapshot()
		if err != nil || snapshot.Instance.Generation != claim.InstanceGeneration || snapshot.Revision.Revision != claim.Run.ConfigRevision || !snapshot.Instance.Enabled {
			outcome.ErrorCode = "authority_changed"
		} else if ValidatePluginTask(claim.Task, snapshot.Installation.Manifest) != nil {
			outcome.ErrorCode = "invalid_definition"
		} else {
			input, err := s.broker.Prepare(runCtx, claim, snapshot)
			if err != nil {
				outcome.ErrorCode = "prepare_failed"
			} else if !generalObjectJSON(input) {
				outcome.ErrorCode = "invalid_input"
			} else {
				result, err = lease.RunTask(runCtx, claim.Task.Entry, key, input)
				if err != nil {
					outcome.ErrorCode = "execution_failed"
				} else if len(result.Output) > generalJSONMaxBytes {
					outcome.ErrorCode = "result_too_large"
				} else if contract.ValidateTaskResult(result) != nil {
					outcome.ErrorCode = "invalid_result"
				} else {
					raw, marshalErr := json.Marshal(result)
					if marshalErr != nil || len(raw) > PluginTaskMaxResultBytes {
						outcome.ErrorCode = "result_too_large"
					} else {
						encrypted, encryptErr := s.encryptor.Encrypt(string(raw))
						if encryptErr != nil || encrypted == "" || len(encrypted) > 2*PluginTaskMaxResultBytes {
							outcome.ErrorCode = "encryption_failed"
						} else {
							resultValid = true
							outcome.ResultEncrypted = encrypted
							if result.Status == contract.TaskSucceeded {
								outcome.Status = "succeeded"
								outcome.Changed = result.Changed
								outcome.ErrorCode = ""
							} else {
								outcome.ErrorCode = "task_failed"
							}
						}
					}
				}
			}
		}
	}
	if runCtx.Err() != nil {
		outcome.Status = "failed"
		outcome.Changed = false
		outcome.ErrorCode = "cancelled"
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			outcome.ErrorCode = "timeout"
		}
	}
	// Never store an invalid or oversized result, nor plaintext infrastructure errors.
	if !resultValid {
		outcome.ResultEncrypted = ""
	}
	var callback func(*sql.Tx) error
	if outcome.Status == "succeeded" {
		callback = func(tx *sql.Tx) error {
			if e := runCtx.Err(); e != nil {
				return e
			}
			return s.broker.Commit(runCtx, tx, claim, result)
		}
	}
	finishCtx := runCtx
	var finishCancel context.CancelFunc
	if outcome.Status != "succeeded" {
		finishCtx, finishCancel = context.WithTimeout(context.WithoutCancel(call), 5*time.Second)
		defer finishCancel()
	}
	e = s.repo.Finish(finishCtx, claim, outcome, callback)
	if e != nil && callback != nil && !errors.Is(e, ErrPluginTaskConflict) {
		// A broker/SQL failure rolled back all business writes. Record only generic
		// failure metadata in a fresh fenced transaction and make it retryable.
		outcome.Status = "failed"
		outcome.Changed = false
		outcome.ErrorCode = "commit_failed"
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(call), 5*time.Second)
		e = s.repo.Finish(cleanup, claim, outcome, nil)
		cancel()
	}
	if e != nil {
		return nil, e
	}
	if outcome.Status == "succeeded" {
		s.broker.AfterCommit()
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(call), 5*time.Second)
	defer cancel()
	saved, e := s.repo.Run(cleanup, outcome.ID)
	return pluginTaskPublicRun(saved), e
}
func (s *PluginTaskService) Close() {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		return
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
	close(s.closeDone)
}
