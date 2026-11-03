package service

import (
	"context"
	"errors"
	"sort"
)

// PluginInstanceStatus combines database authority with a node-local observation.
// No runtime is started, decrypted, retired or closed while reading this DTO.
// Closed is true when there are no open local runtimes, including a cold node;
// RuntimePresent distinguishes an absent runtime from an observed closed one.
type PluginInstanceStatus struct {
	InstanceID      int64                         `json:"instance_id"`
	Enabled         bool                          `json:"enabled"`
	CurrentRevision int64                         `json:"current_revision"`
	RuntimePresent  bool                          `json:"runtime_present"`
	LeaseRefs       int                           `json:"lease_refs"`
	Retired         bool                          `json:"retired"`
	Closed          bool                          `json:"closed"`
	LastError       string                        `json:"last_error"`
	Runtimes        []PluginInstanceRuntimeStatus `json:"runtimes"`
}

// LocalCurrent is the node's active handle, not a claim that its revision still
// matches the database. Retired handles remain visible until their leases drain.
type PluginInstanceRuntimeStatus struct {
	Revision     int64 `json:"revision"`
	LeaseRefs    int   `json:"lease_refs"`
	LocalCurrent bool  `json:"local_current"`
	Retired      bool  `json:"retired"`
	Closed       bool  `json:"closed"`
}

func (s *PluginInstanceService) Status(ctx context.Context, id int64) (*PluginInstanceStatus, error) {
	done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	if id <= 0 {
		return nil, ErrPluginInstanceInvalid
	}
	snapshot, err := s.repo.Snapshot(ctx, id, 0)
	if err != nil {
		return nil, err
	}
	status := &PluginInstanceStatus{
		InstanceID: snapshot.Instance.ID, Enabled: snapshot.Instance.Enabled,
		CurrentRevision: snapshot.Instance.CurrentRevision, Closed: true,
		Runtimes: make([]PluginInstanceRuntimeStatus, 0),
	}
	type observation struct {
		public  PluginInstanceRuntimeStatus
		runtime pluginInstanceRuntime
		handle  *managedPluginInstance
	}
	// Release can run without the management gate, so freeze reference counts
	// under mu. Runtime.Err is called outside mu to avoid a foreign callback
	// holding the service lock. It is observational and already concurrency-safe
	// for Acquire/Check; the returned status is a point-in-time node snapshot.
	s.mu.Lock()
	status.LastError = s.lastErrors[id]
	observations := make([]observation, 0)
	for h := range s.all {
		if h.instanceID != id {
			continue
		}
		observations = append(observations, observation{
			public: PluginInstanceRuntimeStatus{Revision: h.revision, LeaseRefs: h.refs,
				LocalCurrent: s.current[id] == h, Retired: h.retired, Closed: h.closed},
			runtime: h.runtime, handle: h,
		})
	}
	s.mu.Unlock()
	observedFailure := false
	for _, observed := range observations {
		r := observed.public
		if !r.Closed && observed.runtime.Err() != nil {
			r.Closed = true
			// A final Release may normally close a retired runtime after the
			// reference-count snapshot. That is not an abnormal process exit.
			s.mu.Lock()
			normallyClosed := observed.handle.closed
			superseded := observed.handle.superseded
			s.mu.Unlock()
			if !normallyClosed && !superseded {
				observedFailure = true
			}
		}
		status.LeaseRefs += r.LeaseRefs
		status.Retired = status.Retired || r.Retired
		status.Closed = status.Closed && r.Closed
		status.Runtimes = append(status.Runtimes, r)
	}
	// Release can record a failure while the runtime is observed. Read the
	// final history once, so runtime iteration order cannot erase an error.
	s.mu.Lock()
	status.LastError = s.lastErrors[id]
	s.mu.Unlock()
	if observedFailure {
		status.LastError = "runtime_unavailable"
	}
	status.RuntimePresent = len(status.Runtimes) > 0
	sort.Slice(status.Runtimes, func(i, j int) bool {
		a, b := status.Runtimes[i], status.Runtimes[j]
		if a.Revision != b.Revision {
			return a.Revision < b.Revision
		}
		if a.LocalCurrent != b.LocalCurrent {
			return !a.LocalCurrent
		}
		if a.Retired != b.Retired {
			return a.Retired
		}
		if a.Closed != b.Closed {
			return a.Closed
		}
		return a.LeaseRefs < b.LeaseRefs
	})
	return status, nil
}

// Only host-defined codes are retained. Never retain err.Error(), config, plugin
// stderr, paths or credentials, even when a candidate startup failure has them.
func (s *PluginInstanceService) recordInstanceError(id int64, code string) {
	s.mu.Lock()
	s.lastErrors[id] = code
	s.mu.Unlock()
}

func (s *PluginInstanceService) recordInstancePublicationError(id int64, err error) {
	code := "publication_failed"
	if errors.Is(err, ErrPluginInstanceConflict) {
		code = "publication_conflict"
	}
	s.recordInstanceError(id, code)
}

// Err only inspects host-owned lifecycle state (no plugin IPC). Lifecycle
// mutations call it under mu before their own Close, so a normal drain cannot
// be mistaken for an abnormal exit. Successful installation clears the history;
// a later release of an older handle must not overwrite that recovery.
func (s *PluginInstanceService) recordRuntimeFailureLocked(h *managedPluginInstance) {
	if h.superseded {
		return
	}
	if current := s.current[h.instanceID]; current != nil && current != h {
		return
	}
	if !h.closed && h.runtime.Err() != nil {
		s.lastErrors[h.instanceID] = "runtime_unavailable"
	}
}

// Call failures are recorded by the lease's execution path, never by GET.
func (s *PluginInstanceService) recordRuntimeCallFailure(h *managedPluginInstance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h.closed || h.superseded {
		return
	}
	if current := s.current[h.instanceID]; current != nil && current != h {
		return
	}
	code := "runtime_call_failed"
	if h.runtime.Err() != nil {
		code = "runtime_unavailable"
	}
	s.lastErrors[h.instanceID] = code
}
