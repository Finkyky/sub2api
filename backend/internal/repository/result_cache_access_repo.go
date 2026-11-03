package repository

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"time"
)

type resultCacheAccessRepository struct{ db *sql.DB }

func NewResultCacheAccessRepository(db *sql.DB) service.ResultCacheAccessRepository {
	return &resultCacheAccessRepository{db: db}
}
func (r *resultCacheAccessRepository) Append(ctx context.Context, a service.ResultCacheAccess) error {
	// Original generation cost remains in usage_logs. NULL for upstream-called
	// records prevents incorrectly inventing zero actual generation cost.
	var upstreamCost any
	if !a.UpstreamCalled {
		upstreamCost = 0.0
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO result_cache_access_logs(request_id,user_id,api_key_id,group_id,model,mode,scope,status,reason,source_request_id,age_seconds,duration_ms,upstream_called,upstream_cost,cache_charge)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,0) ON CONFLICT(request_id) DO NOTHING`, a.RequestID, a.UserID, a.APIKeyID, a.GroupID, a.Model, a.Mode, a.Scope, a.Status, a.Reason, a.SourceRequestID, a.AgeSeconds, a.DurationMS, a.UpstreamCalled, upstreamCost)
	return err
}
func (r *resultCacheAccessRepository) Cleanup(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM result_cache_access_logs WHERE id IN (SELECT id FROM result_cache_access_logs WHERE created_at<$1 ORDER BY created_at LIMIT 10000)`, before)
	return err
}
