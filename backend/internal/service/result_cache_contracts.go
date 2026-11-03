package service

import (
	"context"
	"encoding/json"
	"time"
)

type ResultCacheEntry struct {
	Version         int             `json:"version"`
	CreatedAt       time.Time       `json:"created_at"`
	ExpiresAt       time.Time       `json:"expires_at"`
	PolicyVersion   string          `json:"policy_version"`
	Model           string          `json:"model"`
	Body            json.RawMessage `json:"body"`
	SourceRequestID string          `json:"source_request_id"`
}
type ResultCachePolicy struct {
	Config  GroupResponseResultCacheConfig
	Version string
	Active  bool
}
type ResultCachePolicyRepository interface {
	Current(context.Context, int64) (ResultCachePolicy, error)
}
type ResultCacheStore interface {
	Get(context.Context, string) (*ResultCacheEntry, error)
	Acquire(context.Context, string, string, time.Duration) (bool, error)
	Renew(context.Context, string, string, time.Duration) (bool, error)
	Publish(context.Context, string, string, *ResultCacheEntry) (bool, error)
	Release(context.Context, string, string) error
}
type ResultCacheAccess struct {
	RequestID                                           string
	UserID, APIKeyID, GroupID                           int64
	Model, Mode, Scope, Status, Reason, SourceRequestID string
	AgeSeconds, DurationMS                              int64
	UpstreamCalled                                      bool
}
type ResultCacheAccessRepository interface {
	Append(context.Context, ResultCacheAccess) error
	Cleanup(context.Context, time.Time) error
}
