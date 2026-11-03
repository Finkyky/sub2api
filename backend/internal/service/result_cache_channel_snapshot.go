package service

import (
	"context"
	"fmt"
	"sync"
)

// ResultCacheChannelSnapshotRepository must return data and its authoritative
// policy from ONE database snapshot. Separate Current/ListAll reads are not a
// proof: even an ABA policy change can produce mixed relation/price data.
type ResultCacheChannelSnapshotRepository interface {
	LoadResultCacheSnapshot(context.Context, int64) ([]Channel, map[int64]string, ResultCachePolicy, error)
}

type resultCacheChannelSnapshot struct {
	done  chan struct{}
	cache *channelCache
	err   error
}

// The set is request-local. Published cells are immutable after done closes.
// A sticky failure only denies result caching; ChannelService falls back to its
// unchanged shared path for ordinary forwarding. No request cells are published
// to that shared cache or shared singleflight.
type resultCacheChannelSnapshots struct {
	parent  context.Context
	groupID int64
	version string
	mu      sync.Mutex
	cells   map[*ChannelService]*resultCacheChannelSnapshot
	reason  string
}

func (p *resultCacheChannelSnapshots) failure() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reason
}
func (p *resultCacheChannelSnapshots) fail(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reason == "" {
		p.reason = reason
	}
}
func resultCacheChannelScope(ctx context.Context) *resultCacheChannelSnapshots {
	pin, ok := ctx.Value(resultCacheRequestPolicyKey{}).(resultCacheRequestPolicy)
	if !ok {
		return nil
	}
	return pin.channels
}
func (p *resultCacheChannelSnapshots) load(ctx context.Context, s *ChannelService) (*channelCache, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	cell := p.cells[s]
	if cell == nil {
		cell = &resultCacheChannelSnapshot{done: make(chan struct{})}
		p.cells[s] = cell
		go p.build(s, cell)
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-cell.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return cell.cache, cell.err
	}
}
func (p *resultCacheChannelSnapshots) build(s *ChannelService, cell *resultCacheChannelSnapshot) {
	defer close(cell.done)
	loader, ok := s.repo.(ResultCacheChannelSnapshotRepository)
	if !ok {
		p.fail("semantic_snapshot_unavailable")
		cell.err = fmt.Errorf("channel repository has no authoritative snapshot reader")
		return
	}
	dbCtx, cancel := context.WithTimeout(p.parent, channelCacheDBTimeout)
	defer cancel()
	channels, platforms, policy, err := loader.LoadResultCacheSnapshot(dbCtx, p.groupID)
	if err != nil {
		p.fail("semantic_snapshot_error")
		cell.err = err
		return
	}
	if err = dbCtx.Err(); err != nil {
		p.fail("semantic_snapshot_error")
		cell.err = err
		return
	}
	if !policy.Active || !policy.Config.Enabled || policy.Version == "" || policy.Version != p.version {
		p.fail("policy_changed")
		cell.err = fmt.Errorf("channel snapshot authority differs from request pin")
		return
	}
	cell.cache = populateChannelCache(channels, platforms)
}
func (q *ResultCacheSession) channelSnapshotReason() string {
	if q == nil {
		return ""
	}
	return q.channelSnapshots.failure()
}

func (s *ChannelService) loadCacheForGroup(ctx context.Context, groupID int64) (*channelCache, error) {
	if scope := resultCacheChannelScope(ctx); scope != nil && scope.groupID != groupID {
		scope.fail("policy_changed")
		return s.loadSharedCache(ctx)
	}
	return s.loadCache(ctx)
}

// accept serializes a cache decision with source registration and sticky
// failures. Never wait for builds under mu: an unfinished cell denies caching,
// so builders can always acquire mu to report failure. Once all registered
// cells have completed, mu remains held through the bounded store publication
// (or HIT acceptance), defining the local semantic commit boundary. Lookups
// registered after this boundary belong to subsequent work, not this decision.
func (p *resultCacheChannelSnapshots) accept(commit func()) string {
	if p == nil {
		commit()
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reason != "" {
		return p.reason
	}
	for _, cell := range p.cells {
		select {
		case <-cell.done:
		default:
			return "semantic_snapshot_pending"
		}
	}
	commit()
	return ""
}
