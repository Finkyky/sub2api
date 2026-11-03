package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pluginflow"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

type pluginInstanceRuntime interface {
	Err() error
	Close()
	HookInvoker(string, string) pluginflow.Invoker
	RunTask(context.Context, string, string, json.RawMessage) (contract.TaskResult, error)
}
type pluginInstanceStart func(context.Context, *PluginInstanceSnapshot, json.RawMessage) (pluginInstanceRuntime, error)
type managedPluginInstance struct {
	runtime    pluginInstanceRuntime
	instanceID int64
	revision   int64
	generation int64
	refs       int
	retired    bool
	closed     bool
	superseded bool
}

// PluginInstanceService is an internal P2c API, not an HTTP authorization
// boundary. It is deliberately not wired into admin enable or gateway routes.
// Every new acquisition resolves the database authority; no cached state can
// silently turn an unavailable binding into an unbound group.
type PluginInstanceService struct {
	repo             PluginInstanceRepository
	encryptor        SecretEncryptor
	start            pluginInstanceStart
	gate             chan struct{}
	mu               sync.Mutex
	current          map[int64]*managedPluginInstance
	all              map[*managedPluginInstance]bool
	lastErrors       map[int64]string
	closed           bool
	reconcilePending map[int64]bool
	reconcileCursor  int64
	reconcileCancel  context.CancelFunc
	reconcileDone    chan struct{}
}

func newPluginInstanceService(repo PluginInstanceRepository, encryptor SecretEncryptor, start pluginInstanceStart) *PluginInstanceService {
	return &PluginInstanceService{repo: repo, encryptor: encryptor, start: start, gate: make(chan struct{}, 1), current: make(map[int64]*managedPluginInstance), all: make(map[*managedPluginInstance]bool), lastErrors: make(map[int64]string), reconcilePending: make(map[int64]bool)}
}

// The installed package is frozen while an instance references it. The legacy
// manager can therefore restore/reverify its exact artifact on another node.
func NewPluginInstanceService(repo PluginInstanceRepository, encryptor SecretEncryptor, manager *PluginManager, callTimeout time.Duration) *PluginInstanceService {
	return newPluginInstanceService(repo, encryptor, func(ctx context.Context, s *PluginInstanceSnapshot, config json.RawMessage) (pluginInstanceRuntime, error) {
		if manager == nil || manager.cfg == nil {
			return nil, ErrPluginInstanceUnavailable
		}
		local, e := manager.ensureLocalInstallation(ctx, &s.Installation)
		if e != nil {
			return nil, e
		}
		return startGeneralPluginRuntime(ctx, local, generalInstanceOptions{InstanceID: s.Instance.ID, ConfigRevision: s.Revision.Revision, Config: config, Host: manager.hostInfo, StartTimeout: time.Duration(manager.cfg.Plugins.StartTimeoutSeconds) * time.Second, CallTimeout: callTimeout, SocketDir: filepath.Join(manager.installer.RootDir(), "runtime")})
	})
}
func (s *PluginInstanceService) enter(ctx context.Context) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	done := func() { <-s.gate }
	if e := ctx.Err(); e != nil {
		done()
		return nil, e
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		done()
		return nil, ErrGeneralClosed
	}
	return done, nil
}
func (s *PluginInstanceService) encryptConfig(config json.RawMessage) (string, error) {
	if !generalObjectJSON(config) || s.encryptor == nil {
		return "", ErrPluginInstanceInvalid
	}
	encrypted, e := s.encryptor.Encrypt(string(config))
	if e != nil {
		return "", e
	}
	if encrypted == "" || len(encrypted) > 256*1024 {
		return "", ErrPluginInstanceInvalid
	}
	return encrypted, nil
}
func (s *PluginInstanceService) List(ctx context.Context, pluginID int64) ([]PluginInstance, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	if pluginID < 0 {
		return nil, ErrPluginInstanceInvalid
	}
	return s.repo.List(ctx, pluginID)
}

func (s *PluginInstanceService) Snapshot(ctx context.Context, id, revision int64) (*PluginInstanceSnapshot, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	if id <= 0 || revision < 0 {
		return nil, ErrPluginInstanceInvalid
	}
	return s.repo.Snapshot(ctx, id, revision)
}

func (s *PluginInstanceService) RevisionSummaries(ctx context.Context, id int64) ([]PluginInstanceRevisionSummary, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	if id <= 0 {
		return nil, ErrPluginInstanceInvalid
	}
	return s.repo.RevisionSummaries(ctx, id)
}

func (s *PluginInstanceService) Create(ctx context.Context, pluginID int64, name string, config json.RawMessage) (*PluginInstanceSnapshot, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	encrypted, e := s.encryptConfig(config)
	if e != nil {
		return nil, e
	}
	return s.repo.Create(ctx, pluginID, name, encrypted)
}
func (s *PluginInstanceService) SaveConfig(ctx context.Context, id, expected int64, config json.RawMessage) (*PluginInstanceSnapshot, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	encrypted, e := s.encryptConfig(config)
	if e != nil {
		return nil, e
	}
	return s.repo.SaveRevision(ctx, id, expected, encrypted)
}
func (s *PluginInstanceService) ReplaceBindings(ctx context.Context, id, expected int64, bindings []PluginGroupBinding) (*PluginInstanceSnapshot, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	snapshot, e := s.repo.Snapshot(ctx, id, 0)
	if e != nil {
		return nil, e
	}
	if e = ValidatePluginGroupBindings(snapshot.Installation.Manifest, id, bindings); e != nil {
		return nil, e
	}
	return s.repo.ReplaceBindings(ctx, id, expected, append([]PluginGroupBinding(nil), bindings...))
}
func (s *PluginInstanceService) startSnapshot(ctx context.Context, snapshot *PluginInstanceSnapshot) (pluginInstanceRuntime, error) {
	if snapshot == nil || snapshot.Revision.InstanceID != snapshot.Instance.ID || snapshot.Revision.Revision <= 0 || snapshot.Installation.Manifest.General == nil || snapshot.Revision.ConfigVersion != snapshot.Installation.Manifest.General.ConfigVersion || s.encryptor == nil || s.start == nil {
		return nil, ErrPluginInstanceInvalid
	}
	if e := ValidatePluginGroupBindings(snapshot.Installation.Manifest, snapshot.Instance.ID, snapshot.Bindings); e != nil {
		return nil, e
	}
	plain, e := s.encryptor.Decrypt(snapshot.Revision.ConfigEncrypted)
	if e != nil {
		return nil, e
	}
	config := json.RawMessage(plain)
	if !generalObjectJSON(config) {
		return nil, ErrPluginInstanceInvalid
	}
	copy, e := clonePluginInstanceSnapshot(snapshot)
	if e != nil {
		return nil, e
	}
	runtime, e := s.start(ctx, copy, config)
	if e != nil {
		if runtime != nil {
			runtime.Close()
		}
		// Parent cancellation is not evidence that the runtime is unavailable.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Mark real candidate startup/handshake/Configure failures uniformly while
		// retaining the cause for internal callers. HTTP adapters never expose it.
		return nil, errors.Join(ErrPluginInstanceUnavailable, e)
	}
	if runtime == nil {
		return nil, ErrPluginInstanceUnavailable
	}
	return runtime, nil
}
func (s *PluginInstanceService) Publish(ctx context.Context, id, expected, revision int64) (*PluginInstanceSnapshot, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	if id <= 0 || expected <= 0 || revision <= 0 {
		return nil, ErrPluginInstanceInvalid
	}
	target, e := s.repo.Snapshot(ctx, id, revision)
	if e != nil {
		return nil, e
	}
	if target.Instance.Generation != expected {
		return nil, ErrPluginInstanceConflict
	}
	candidate, e := s.startSnapshot(ctx, target)
	if e != nil {
		if ctx.Err() == nil {
			s.recordInstanceError(id, "runtime_start_failed")
		}
		return nil, e
	}
	published, e := s.repo.Publish(ctx, id, expected, revision, true)
	if e != nil {
		s.recordInstancePublicationError(id, e)
		candidate.Close()
		return nil, e
	}
	s.install(id, revision, published.Instance.Generation, candidate)
	return published, nil
}
func (s *PluginInstanceService) Disable(ctx context.Context, id, expected int64) (*PluginInstanceSnapshot, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	snapshot, e := s.repo.Snapshot(ctx, id, 0)
	if e != nil {
		return nil, e
	}
	disabled, e := s.repo.Publish(ctx, id, expected, snapshot.Instance.CurrentRevision, false)
	if e != nil {
		return nil, e
	}
	s.retire(id)
	return disabled, nil
}
func (s *PluginInstanceService) install(id, revision, generation int64, runtime pluginInstanceRuntime) *managedPluginInstance {
	s.retire(id)
	h := &managedPluginInstance{runtime: runtime, instanceID: id, revision: revision, generation: generation}
	s.mu.Lock()
	// Remember replacement on each older handle, not merely in current[id].
	// Disable may remove current while an older lease still drains; even a
	// same-revision restart must permanently revoke that old error authority.
	for previous := range s.all {
		if previous.instanceID == id {
			previous.superseded = true
		}
	}
	s.current[id] = h
	delete(s.reconcilePending, id)
	s.all[h] = true
	delete(s.lastErrors, id)
	s.mu.Unlock()
	return h
}
func (s *PluginInstanceService) retire(id int64) {
	if runtime := s.retireDetached(id); runtime != nil {
		runtime.Close()
	}
}

// Detach all local state before foreign Close callbacks. The caller owns the
// returned runtime's exactly-once cleanup; the gate serializes that ownership.
func (s *PluginInstanceService) retireDetached(id int64) pluginInstanceRuntime {
	s.mu.Lock()
	h := s.current[id]
	delete(s.current, id)
	var closeRuntime pluginInstanceRuntime
	if h != nil {
		s.recordRuntimeFailureLocked(h)
		h.retired = true
		if h.refs == 0 && !h.closed {
			h.closed = true
			delete(s.all, h)
			closeRuntime = h.runtime
		}
	}
	s.mu.Unlock()
	return closeRuntime
}

// A warm replacement must retain intent even if an Acquire, rather than the
// worker, observes authority first and candidate startup fails or is canceled.
// Observe a real crash before revoking older handles' error-writing authority.
// A failed first acquisition of a cold instance does not create retry intent.
func (s *PluginInstanceService) retireForReplacement(id int64) pluginInstanceRuntime {
	s.mu.Lock()
	if h := s.current[id]; h != nil {
		s.recordRuntimeFailureLocked(h)
		s.reconcilePending[id] = true
	}
	for previous := range s.all {
		if previous.instanceID == id {
			previous.superseded = true
		}
	}
	s.mu.Unlock()
	return s.retireDetached(id)
}
func (s *PluginInstanceService) acquireSnapshot(ctx context.Context, snapshot *PluginInstanceSnapshot) (*PluginInstanceLease, error) {
	id := snapshot.Instance.ID
	if !snapshot.Instance.Enabled {
		s.retire(id)
		return nil, ErrPluginInstanceUnavailable
	}
	if snapshot.Revision.Revision != snapshot.Instance.CurrentRevision {
		return nil, ErrPluginInstanceInvalid
	}
	copy, e := clonePluginInstanceSnapshot(snapshot)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	h := s.current[id]
	s.mu.Unlock()
	if h == nil || h.revision != snapshot.Revision.Revision || h.generation != snapshot.Instance.Generation || h.runtime.Err() != nil {
		// Retire mismatched local state even if starting the authoritative revision fails.
		if retired := s.retireForReplacement(id); retired != nil {
			retired.Close()
		}
		runtime, e := s.startSnapshot(ctx, copy)
		if e != nil {
			if ctx.Err() == nil {
				s.recordInstanceError(id, "runtime_start_failed")
			}
			return nil, e
		}
		h = s.install(id, snapshot.Revision.Revision, snapshot.Instance.Generation, runtime)
	}
	s.mu.Lock()
	h.refs++
	s.mu.Unlock()
	return &PluginInstanceLease{owner: s, handle: h, snapshot: copy}, nil
}
func (s *PluginInstanceService) Acquire(ctx context.Context, id int64) (*PluginInstanceLease, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	snapshot, e := s.repo.Snapshot(ctx, id, 0)
	if e != nil {
		s.retire(id)
		return nil, e
	}
	return s.acquireSnapshot(ctx, snapshot)
}

// AcquireGroup freezes all matching bindings/configs in one database snapshot.
// A partial failure releases every already acquired lease before returning.
func (s *PluginInstanceService) AcquireGroup(ctx context.Context, groupID int64, stage contract.Stage, protocol string) ([]*PluginInstanceLease, error) {
	done, e := s.enter(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	snapshots, e := s.repo.GroupSnapshots(ctx, groupID, stage, protocol)
	if e != nil {
		return nil, e
	}
	leases := make([]*PluginInstanceLease, 0, len(snapshots))
	for i := range snapshots {
		lease, e := s.acquireSnapshot(ctx, &snapshots[i])
		if e != nil {
			for _, l := range leases {
				l.Release()
			}
			return nil, e
		}
		leases = append(leases, lease)
	}
	return leases, nil
}
func (s *PluginInstanceService) release(h *managedPluginInstance) {
	s.mu.Lock()
	h.refs--
	closeIt := h.retired && h.refs == 0 && !h.closed
	if closeIt {
		s.recordRuntimeFailureLocked(h)
		h.closed = true
		delete(s.all, h)
	}
	s.mu.Unlock()
	if closeIt {
		h.runtime.Close()
	}
}

// Close is node shutdown, not a durable instance disable. It terminates active
// and retired processes exactly once; other nodes retain the database state.
func (s *PluginInstanceService) Close() {
	// Prevent new loops/operations, cancel the gate holder, then wait outside the gate.
	s.mu.Lock()
	s.closed = true
	cancel, stopped := s.reconcileCancel, s.reconcileDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stopped != nil {
		<-stopped
	}
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	s.mu.Lock()
	s.closed = true
	runtimes := make([]pluginInstanceRuntime, 0, len(s.all))
	for h := range s.all {
		if !h.closed {
			h.closed = true
			runtimes = append(runtimes, h.runtime)
		}
	}
	s.current = make(map[int64]*managedPluginInstance)
	s.all = make(map[*managedPluginInstance]bool)
	s.reconcilePending = make(map[int64]bool)
	s.mu.Unlock()
	for _, r := range runtimes {
		r.Close()
	}
}

// Lease owns the old immutable publication until Release. Check/Task serialize
// with Release, so a concurrent Release cannot close a process mid-invocation.
type PluginInstanceLease struct {
	mu       sync.Mutex
	released bool
	owner    *PluginInstanceService
	handle   *managedPluginInstance
	snapshot *PluginInstanceSnapshot
}

func (l *PluginInstanceLease) Snapshot() (*PluginInstanceSnapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil, ErrGeneralClosed
	}
	return clonePluginInstanceSnapshot(l.snapshot)
}
func (l *PluginInstanceLease) Release() {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	l.mu.Unlock()
	l.owner.release(l.handle)
}
func (l *PluginInstanceLease) Check(ctx context.Context, bindingID int64, req contract.HookRequest) (contract.HookResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return contract.HookResult{}, ErrGeneralClosed
	}
	if l.handle.runtime.Err() != nil {
		l.owner.recordRuntimeCallFailure(l.handle)
		return contract.HookResult{}, ErrGeneralClosed
	}
	for _, b := range l.snapshot.Bindings {
		if b.ID == bindingID && b.Enabled && b.GroupID == req.GroupID && b.Stage == req.Stage {
			flow := pluginflow.Binding{ID: b.ID, InstanceID: b.InstanceID, GroupID: b.GroupID, Stage: b.Stage, Order: b.Order, Enabled: true, Ready: true, Version: l.snapshot.Installation.Version, ConfigRevision: l.snapshot.Revision.Revision, CanReplace: b.CanReplace}
			result, err := l.handle.runtime.HookInvoker(b.Entry, b.Protocol)(ctx, flow, req)
			if err != nil {
				l.owner.recordRuntimeCallFailure(l.handle)
			}
			return result, err
		}
	}
	return contract.HookResult{}, ErrGeneralBinding
}
func (l *PluginInstanceLease) RunTask(ctx context.Context, entry, jobID string, input json.RawMessage) (contract.TaskResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return contract.TaskResult{}, ErrGeneralClosed
	}
	if l.handle.runtime.Err() != nil {
		l.owner.recordRuntimeCallFailure(l.handle)
		return contract.TaskResult{}, ErrGeneralClosed
	}
	result, err := l.handle.runtime.RunTask(ctx, entry, jobID, input)
	if err != nil {
		l.owner.recordRuntimeCallFailure(l.handle)
	}
	return result, err
}

// Err makes self-closed or crashed P2b processes visible to the lease cache.
func (r *generalPluginRuntime) Err() error {
	if r.life.Err() != nil || r.client.Exited() {
		return ErrGeneralClosed
	}
	return nil
}
