package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"log/slog"
	"sync"
	"time"
)

var ErrResultCacheScopeDenied = infraerrors.Forbidden("RESULT_CACHE_SCOPE_DENIED", "group sharing is not enabled")

type ResultCacheService struct {
	ioTimeout, leaseTTL, waitBudget, generationBudget time.Duration
	metrics                                           resultCacheMetrics
	store                                             ResultCacheStore
	policy                                            ResultCachePolicyRepository
	access                                            ResultCacheAccessRepository
	cleanupDone                                       chan struct{}
	fillSlots                                         chan struct{}
	stop                                              chan struct{}
	stopped                                           sync.Once
	lifecycleMu                                       sync.Mutex
	renewals                                          sync.WaitGroup
}

func NewResultCacheService(store ResultCacheStore, policy ResultCachePolicyRepository, access ResultCacheAccessRepository, cfg *config.Config) *ResultCacheService {
	options := config.DefaultResultCacheConfig()
	if cfg != nil && cfg.Gateway.ResultCache != (config.ResultCacheConfig{}) {
		options = cfg.Gateway.ResultCache
	}
	s := &ResultCacheService{ioTimeout: time.Duration(options.IOTimeoutMS) * time.Millisecond, leaseTTL: time.Duration(options.LeaseTTLMS) * time.Millisecond, waitBudget: time.Duration(options.WaitTimeoutMS) * time.Millisecond, generationBudget: time.Duration(options.GenerationTimeoutMS) * time.Millisecond, store: store, policy: policy, access: access, stop: make(chan struct{}), cleanupDone: make(chan struct{}), fillSlots: make(chan struct{}, options.MaxConcurrentFills)}
	if access != nil {
		go s.cleanupLoop()
	} else {
		close(s.cleanupDone)
	}
	return s
}
func (s *ResultCacheService) Stop() {
	if s != nil {
		s.stopped.Do(func() {
			// Serialize admission with shutdown so Add never races a zero-count Wait.
			s.lifecycleMu.Lock()
			close(s.stop)
			s.lifecycleMu.Unlock()
			<-s.cleanupDone
			s.renewals.Wait()
			if closer, ok := s.store.(interface{ Close() error }); ok {
				if err := closer.Close(); err != nil {
					slog.Warn("result cache store close failed")
				}
			}
		})
	}
}
func (s *ResultCacheService) isStopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *ResultCacheService) cleanupLoop() {
	defer close(s.cleanupDone)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := s.access.Cleanup(ctx, time.Now().Add(-7*24*time.Hour))
			cancel()
			if err != nil {
				slog.Warn("result cache access cleanup failed")
			}
		}
	}
}

type ResultCacheSession struct {
	service                *ResultCacheService
	channelSnapshots       *resultCacheChannelSnapshots
	intent                 ResultCacheIntent
	material               ResultCacheKeyMaterial
	policy                 ResultCachePolicy
	Key, Status, Reason    string
	Entry                  *ResultCacheEntry
	token                  string
	started                time.Time
	generationDeadline     time.Time
	access                 ResultCacheAccess
	stopRenew              chan struct{}
	stopOnce, completeOnce sync.Once
	modelMatches           bool
	fillSlot               bool
	slotOnce               sync.Once
}

func (s *ResultCacheService) current(ctx context.Context, id int64) (ResultCachePolicy, error) {
	c, cancel := context.WithTimeout(ctx, s.ioTimeout)
	defer cancel()
	return s.policy.Current(c, id)
}
func (s *ResultCacheService) Begin(ctx context.Context, intent ResultCacheIntent, m ResultCacheKeyMaterial, a ResultCacheAccess) (*ResultCacheSession, error) {
	if !intent.Declared {
		return nil, nil
	}
	q := &ResultCacheSession{service: s, channelSnapshots: resultCacheChannelScope(ctx), intent: intent, material: m, Status: "BYPASS", started: time.Now(), access: a, modelMatches: true, stopRenew: make(chan struct{})}
	q.access.Mode = intent.Mode
	q.access.Scope = intent.Scope
	q.access.Model = m.EffectiveModel
	if s == nil || s.store == nil || s.policy == nil {
		q.Reason = "unavailable"
		return q, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isStopped() {
		q.Reason = "service_stopped"
		return q, nil
	}
	p, err := s.current(ctx, m.GroupID)
	if err != nil {
		q.Reason = "policy_error"
		return q, nil
	}
	q.policy = p
	if !p.Active || !p.Config.Enabled {
		q.Reason = "disabled"
		return q, nil
	}
	if intent.Scope == "group" && !p.Config.AllowGroupSharing {
		return nil, ErrResultCacheScopeDenied
	}
	if intent.Mode == "bypass" {
		q.Reason = "client_bypass"
		return q, nil
	}
	if reason := s.requestPolicyReason(ctx, m.GroupID, p.Version); reason != "" {
		q.Reason = reason
		return q, nil
	}
	if !ResultCacheRequestEligible(m.EffectiveRequest) {
		q.Reason = "unsupported"
		return q, nil
	}
	m.PolicyVersion = p.Version
	m.Scope = intent.Scope
	if intent.Scope == "group" {
		m.ScopeIdentity = fmt.Sprintf("group:%d", m.GroupID)
	}
	key, err := BuildResultCacheKey(m)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_CACHE_REQUEST", err.Error())
	}
	q.Key = key
	q.material = m
	q.Status = "MISS"
	if intent.Mode != "write_only" {
		if q.read(ctx) {
			return q, nil
		}
		if q.Reason != "" {
			q.Status = "BYPASS"
			return q, nil
		}
	}
	if intent.Mode == "read_only" {
		return q, nil
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		q.Status = "BYPASS"
		q.Reason = "entropy_error"
		return q, nil
	}
	q.token = hex.EncodeToString(token)
	deadline := time.Now().Add(s.waitBudget)
	for {
		c, cancel := context.WithTimeout(ctx, s.ioTimeout)
		ok, e := s.store.Acquire(c, key, q.token, s.leaseTTL)
		cancel()
		if e != nil {
			q.token = ""
			q.Status = "BYPASS"
			q.Reason = "storage_error"
			return q, nil
		}
		if ok {
			if s.isStopped() {
				q.release()
				q.token = ""
				q.Status = "BYPASS"
				q.Reason = "service_stopped"
				return q, nil
			}
			// Close the gap between the initial read and acquiring a released lease.
			if intent.Mode == "read_write" && q.read(ctx) {
				q.release()
				return q, nil
			}
			if q.Reason != "" {
				q.release()
				q.Status = "BYPASS"
				return q, nil
			}
			select {
			case s.fillSlots <- struct{}{}:
				q.fillSlot = true
			default:
				q.release()
				q.token = ""
				q.Status = "BYPASS"
				q.Reason = "fill_capacity"
				return q, nil
			}
			s.lifecycleMu.Lock()
			if s.isStopped() {
				s.lifecycleMu.Unlock()
				q.release()
				q.token = ""
				q.Status = "BYPASS"
				q.Reason = "service_stopped"
				return q, nil
			}
			q.generationDeadline = time.Now().Add(s.generationBudget)
			s.renewals.Add(1)
			go func() {
				defer s.renewals.Done()
				q.renewLoop(ctx)
			}()
			s.lifecycleMu.Unlock()
			return q, nil
		}
		if intent.Mode == "read_write" && q.read(ctx) {
			q.token = ""
			return q, nil
		}
		if q.Reason != "" || time.Now().After(deadline) {
			q.token = ""
			q.Status = "BYPASS"
			if q.Reason == "" {
				q.Reason = "wait_timeout"
			}
			return q, nil
		}
		select {
		case <-ctx.Done():
			q.token = ""
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (q *ResultCacheSession) read(ctx context.Context) bool {
	c, cancel := context.WithTimeout(ctx, q.service.ioTimeout)
	e, err := q.service.store.Get(c, q.Key)
	cancel()
	if err != nil {
		q.Reason = "storage_error"
		return false
	}
	if e == nil {
		return false
	}
	if e.PolicyVersion != q.policy.Version || e.Model != q.material.EffectiveModel || !e.ExpiresAt.After(time.Now()) || len(e.Body) > q.policy.Config.MaxResponseBytes || !ResultCacheResponseComplete(e.Body) {
		return false
	}
	p, err := q.service.current(ctx, q.material.GroupID)
	if err != nil || !p.Active || !p.Config.Enabled || p.Version != q.policy.Version {
		q.Reason = "policy_changed"
		return false
	}
	if reason := q.channelSnapshots.accept(func() {
		q.Entry = e
		q.Status = "HIT"
		q.access.SourceRequestID = e.SourceRequestID
	}); reason != "" {
		q.Reason = reason
		return false
	}
	q.access.AgeSeconds = int64(time.Since(e.CreatedAt) / time.Second)
	return true
}
func (q *ResultCacheSession) renewLoop(ctx context.Context) {
	// Completion is not guaranteed when the client leaves or a handler stalls.
	// Release both the distributed lease and the local capacity on every exit.
	defer q.release()
	ticker := time.NewTicker(q.service.leaseTTL / 3)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Until(q.generationDeadline))
	defer deadline.Stop()
	for {
		select {
		case <-q.stopRenew:
			return
		case <-ctx.Done():
			return
		case <-q.service.stop:
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			c, cancel := context.WithTimeout(ctx, q.service.ioTimeout)
			ok, err := q.service.store.Renew(c, q.Key, q.token, q.service.leaseTTL)
			cancel()
			if err != nil || !ok {
				return
			}
		}
	}
}
func (q *ResultCacheSession) release() {
	q.slotOnce.Do(func() {
		if q.fillSlot {
			<-q.service.fillSlots
		}
	})
	if q.token == "" {
		return
	}
	c, cancel := context.WithTimeout(context.Background(), q.service.ioTimeout)
	defer cancel()
	_ = q.service.store.Release(c, q.Key, q.token)
}
func (q *ResultCacheSession) CaptureLimit() int {
	if q == nil || q.token == "" || q.Status == "HIT" {
		return 0
	}
	return q.policy.Config.MaxResponseBytes
}
func (q *ResultCacheSession) NoteUpstream(model string) {
	if q == nil {
		return
	}
	q.access.UpstreamCalled = true
	if model != "" && model != q.material.EffectiveModel {
		q.modelMatches = false
	}
}
func (q *ResultCacheSession) Complete(ctx context.Context, status int, body []byte) {
	if q == nil {
		return
	}
	q.completeOnce.Do(func() {
		defer q.stopOnce.Do(func() { close(q.stopRenew) })
		defer q.release()
		if q.token != "" && q.Status != "HIT" {
			if q.service.isStopped() {
				q.Status = "BYPASS"
				q.Reason = "service_stopped"
			} else if !time.Now().Before(q.generationDeadline) {
				q.Status = "BYPASS"
				q.Reason = "generation_timeout"
			} else if ctx.Err() != nil || !q.access.UpstreamCalled || status != 200 || !q.modelMatches || len(body) > q.policy.Config.MaxResponseBytes || !ResultCacheResponseComplete(body) {
				q.Status = "UNCACHEABLE"
				q.Reason = "response_ineligible"
			} else if reason := q.channelSnapshotReason(); reason != "" {
				q.Status = "BYPASS"
				q.Reason = reason
			} else {
				p, err := q.service.current(ctx, q.material.GroupID)
				if err != nil || !p.Active || !p.Config.Enabled || p.Version != q.policy.Version {
					q.Status = "BYPASS"
					q.Reason = "policy_changed"
				} else {
					if reason := q.channelSnapshots.accept(func() {
						now := time.Now().UTC()
						e := &ResultCacheEntry{Version: 1, CreatedAt: now, ExpiresAt: now.Add(time.Duration(p.Config.TTLSeconds) * time.Second), PolicyVersion: p.Version, Model: q.material.EffectiveModel, Body: append(json.RawMessage(nil), body...), SourceRequestID: q.sourceRequestID()}
						c, cancel := context.WithTimeout(ctx, q.service.ioTimeout)
						ok, err := q.service.store.Publish(c, q.Key, q.token, e)
						cancel()
						if err != nil || !ok {
							q.Status = "BYPASS"
							q.Reason = "publish_failed"
						} else if q.intent.Mode == "write_only" {
							q.Status = "UPDATED"
						}
					}); reason != "" {
						q.Status = "BYPASS"
						q.Reason = reason
					}
				}
			}
		}
		q.access.Status = q.Status
		q.access.Reason = q.Reason
		q.access.DurationMS = time.Since(q.started).Milliseconds()
		if q.service != nil {
			q.service.metrics.observe(q.Status, q.access.DurationMS)
		}
		if q.service != nil && q.service.access != nil {
			c, cancel := context.WithTimeout(context.Background(), q.service.ioTimeout)
			err := q.service.access.Append(c, q.access)
			cancel()
			if err != nil {
				q.service.metrics.accessErrors.Add(1)
				slog.Warn("result cache access record failed")
			}
		}
	})
}
func ResultCacheResponseComplete(body []byte) bool {
	var f struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Role         string          `json:"role"`
				Content      *string         `json:"content"`
				ToolCalls    json.RawMessage `json:"tool_calls"`
				FunctionCall json.RawMessage `json:"function_call"`
				Refusal      json.RawMessage `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &f) != nil || len(f.Error) > 0 || len(f.Choices) == 0 {
		return false
	}
	for _, c := range f.Choices {
		if c.Finish != "stop" || c.Message.Role != "assistant" || c.Message.Content == nil || len(c.Message.ToolCalls) > 0 || len(c.Message.FunctionCall) > 0 || (len(c.Message.Refusal) > 0 && string(c.Message.Refusal) != "null") {
			return false
		}
	}
	return true
}

func (q *ResultCacheSession) NoteSource(id string) {
	if q != nil && len(id) <= 64 {
		q.access.SourceRequestID = id
	}
}
func (q *ResultCacheSession) sourceRequestID() string {
	if q.access.SourceRequestID != "" {
		return q.access.SourceRequestID
	}
	return q.access.RequestID
}

func (q *ResultCacheSession) Reject() {
	if q != nil {
		q.modelMatches = false
	}
}
