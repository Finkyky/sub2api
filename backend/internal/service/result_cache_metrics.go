package service

import "sync/atomic"

type ResultCacheMetricsSnapshot struct{ HIT, MISS, UPDATED, BYPASS, UNCACHEABLE, AccessErrors, TotalLatencyMS uint64 }
type resultCacheMetrics struct{ hit, miss, updated, bypass, uncacheable, accessErrors, latencyMS atomic.Uint64 }

func (m *resultCacheMetrics) observe(status string, ms int64) {
	if ms > 0 {
		m.latencyMS.Add(uint64(ms))
	}
	switch status {
	case "HIT":
		m.hit.Add(1)
	case "MISS":
		m.miss.Add(1)
	case "UPDATED":
		m.updated.Add(1)
	case "BYPASS":
		m.bypass.Add(1)
	case "UNCACHEABLE":
		m.uncacheable.Add(1)
	}
}
func (s *ResultCacheService) Metrics() ResultCacheMetricsSnapshot {
	if s == nil {
		return ResultCacheMetricsSnapshot{}
	}
	m := &s.metrics
	return ResultCacheMetricsSnapshot{m.hit.Load(), m.miss.Load(), m.updated.Load(), m.bypass.Load(), m.uncacheable.Load(), m.accessErrors.Load(), m.latencyMS.Load()}
}
