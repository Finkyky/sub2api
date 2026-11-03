package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"
)

// Reconcile observes database authority without publishing or changing generation.
// Cold instances remain lazy. A failed replacement retains an explicit retry
// intent even after the last old lease is released.
func (s *PluginInstanceService) Reconcile(ctx context.Context) error {
	done, err := s.enter(ctx)
	if err != nil {
		return err
	}
	defer done()
	s.mu.Lock()
	ids := make(map[int64]bool, len(s.current)+len(s.reconcilePending))
	for id := range s.current {
		ids[id] = true
	}
	for id := range s.reconcilePending {
		ids[id] = true
	}
	for h := range s.all {
		ids[h.instanceID] = true
	}
	for id := range s.lastErrors {
		ids[id] = true
	}
	s.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	instances, err := s.repo.List(ctx, 0)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	authority := make(map[int64]PluginInstance, len(instances))
	for _, instance := range instances {
		if instance.ID <= 0 || instance.Generation <= 0 || instance.CurrentRevision <= 0 {
			return ErrPluginInstanceInvalid
		}
		if _, exists := authority[instance.ID]; exists {
			return ErrPluginInstanceInvalid
		}
		authority[instance.ID] = instance
	}
	tracked := make([]int64, 0, len(ids))
	for id := range ids {
		tracked = append(tracked, id)
	}
	sort.Slice(tracked, func(i, j int) bool { return tracked[i] < tracked[j] })
	candidates := make([]int64, 0)
	var cleanup []pluginInstanceRuntime
	collect := func(runtime pluginInstanceRuntime) {
		if runtime != nil {
			cleanup = append(cleanup, runtime)
		}
	}
	// Complete every authority revocation before invoking potentially slow Close.
	closeDetached := func() {
		for _, runtime := range cleanup {
			runtime.Close()
		}
		cleanup = nil
	}
	defer closeDetached()
	// Stop/delete propagation never waits for another instance's candidate startup.
	for _, id := range tracked {
		instance, exists := authority[id]
		if !exists {
			collect(s.forgetReconciledInstanceForCleanup(id))
			continue
		}
		if !instance.Enabled {
			collect(s.retireDetached(id))
			s.mu.Lock()
			delete(s.reconcilePending, id)
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		h, pending := s.current[id], s.reconcilePending[id]
		s.mu.Unlock()
		if h == nil && !pending {
			continue
		}
		if h != nil && h.generation == instance.Generation && h.revision == instance.CurrentRevision && h.runtime.Err() == nil {
			continue
		}
		collect(s.retireForReplacement(id))
		candidates = append(candidates, id)
	}
	closeDetached()
	// Rotate each attempted candidate so one deadline-consuming instance cannot
	// indefinitely starve later pending replacements.
	s.mu.Lock()
	cursor := s.reconcileCursor
	s.mu.Unlock()
	pivot := sort.Search(len(candidates), func(i int) bool { return candidates[i] > cursor })
	if pivot < len(candidates) {
		candidates = append(append([]int64{}, candidates[pivot:]...), candidates[:pivot]...)
	}
	var failures []error
	for _, id := range candidates {
		if err = ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		s.mu.Lock()
		s.reconcileCursor = id
		s.mu.Unlock()
		if err = s.reconcileCandidate(ctx, id); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (s *PluginInstanceService) forgetReconciledInstance(id int64) {
	if runtime := s.forgetReconciledInstanceForCleanup(id); runtime != nil {
		runtime.Close()
	}
}
func (s *PluginInstanceService) forgetReconciledInstanceForCleanup(id int64) pluginInstanceRuntime {
	s.mu.Lock()
	for h := range s.all {
		if h.instanceID == id {
			h.superseded = true
		}
	}
	s.mu.Unlock()
	runtime := s.retireDetached(id)
	s.mu.Lock()
	delete(s.reconcilePending, id)
	delete(s.lastErrors, id)
	s.mu.Unlock()
	return runtime
}
func (s *PluginInstanceService) reconcileCandidate(ctx context.Context, id int64) error {
	snapshot, err := s.repo.Snapshot(ctx, id, 0)
	if err != nil {
		if errors.Is(err, ErrPluginInstanceNotFound) {
			s.forgetReconciledInstance(id)
		}
		return err
	}
	if !snapshot.Instance.Enabled {
		s.retire(id)
		s.mu.Lock()
		delete(s.reconcilePending, id)
		s.mu.Unlock()
		return nil
	}
	if snapshot.Instance.ID != id || snapshot.Instance.Generation <= 0 || snapshot.Revision.Revision != snapshot.Instance.CurrentRevision {
		return ErrPluginInstanceInvalid
	}
	candidate, err := s.startSnapshot(ctx, snapshot)
	if err != nil {
		if ctx.Err() == nil {
			s.recordInstanceError(id, "runtime_start_failed")
		}
		return err
	}
	committed := false
	defer func() {
		if !committed {
			candidate.Close()
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	latest, err := s.repo.Snapshot(ctx, id, 0)
	if err != nil {
		if errors.Is(err, ErrPluginInstanceNotFound) {
			s.forgetReconciledInstance(id)
		}
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if latest.Instance.ID != id || !latest.Instance.Enabled || latest.Instance.Generation != snapshot.Instance.Generation || latest.Instance.CurrentRevision != snapshot.Instance.CurrentRevision || latest.Revision.Revision != snapshot.Revision.Revision {
		if !latest.Instance.Enabled {
			s.mu.Lock()
			delete(s.reconcilePending, id)
			s.mu.Unlock()
		}
		return ErrPluginInstanceConflict
	}
	s.install(id, snapshot.Revision.Revision, snapshot.Instance.Generation, candidate)
	committed = true
	return nil
}

// StartReconciler is idempotent for a live loop. The service lifetime is separate
// from each cycle budget, and successful runtimes retain their own lifetime.
func (s *PluginInstanceService) StartReconciler(parent context.Context, interval, budget time.Duration) error {
	if interval <= 0 || budget <= 0 {
		return ErrPluginInstanceInvalid
	}
	if err := parent.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrGeneralClosed
	}
	if s.reconcileDone != nil {
		select {
		case <-s.reconcileDone:
		default:
			return nil
		}
	}
	ctx, cancel := context.WithCancel(parent)
	stopped := make(chan struct{})
	s.reconcileCancel = cancel
	s.reconcileDone = stopped
	go s.reconcileLoop(ctx, interval, budget, stopped)
	return nil
}
func (s *PluginInstanceService) reconcileLoop(ctx context.Context, interval, budget time.Duration, stopped chan struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	cycle := func() {
		child, cancel := context.WithTimeout(ctx, budget)
		err := s.Reconcile(child)
		cancel()
		if err != nil && ctx.Err() == nil && !errors.Is(err, ErrGeneralClosed) {
			slog.Warn("plugin instance reconciliation cycle failed")
		}
	}
	if ctx.Err() != nil {
		return
	}
	cycle()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			cycle()
		}
	}
}

// StopReconciler waits without holding mu/gate. It does not close leased runtimes.
func (s *PluginInstanceService) StopReconciler() {
	s.mu.Lock()
	cancel, stopped := s.reconcileCancel, s.reconcileDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stopped != nil {
		<-stopped
	}
	s.mu.Lock()
	if s.reconcileDone == stopped {
		s.reconcileCancel = nil
		s.reconcileDone = nil
	}
	s.mu.Unlock()
}
