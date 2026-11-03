package service

import "context"

// Delete only guarantees zero active leases on this node. Cross-node draining
// belongs to the generation reconciler; the DB remains authoritative for all
// future acquisitions and management writes.
func (s *PluginInstanceService) Delete(ctx context.Context, id, expected int64) error {
	done, err := s.enter(ctx)
	if err != nil {
		return err
	}
	defer done()
	if id <= 0 || expected <= 0 {
		return ErrPluginInstanceInvalid
	}
	// The gate excludes Acquire/AcquireGroup/Publish until the transaction and
	// cleanup finish. Release can decrease refs, but no operation can add a ref.
	s.mu.Lock()
	for h := range s.all {
		if h.instanceID == id && h.refs > 0 {
			s.mu.Unlock()
			return ErrPluginInstanceBusy
		}
	}
	s.mu.Unlock()
	if err = s.repo.Delete(ctx, id, expected); err != nil {
		return err
	}
	// Do not mutate local lifecycle/history on any repository failure. A stale
	// idle current may still exist if another node disabled this instance.
	s.mu.Lock()
	runtimes := make([]pluginInstanceRuntime, 0)
	for h := range s.all {
		if h.instanceID != id {
			continue
		}
		h.retired = true
		h.superseded = true
		if !h.closed {
			h.closed = true
			runtimes = append(runtimes, h.runtime)
		}
		delete(s.all, h)
	}
	delete(s.current, id)
	delete(s.lastErrors, id)
	s.mu.Unlock()
	for _, runtime := range runtimes {
		runtime.Close()
	}
	return nil
}
