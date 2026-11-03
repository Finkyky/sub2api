package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type resultCachePolicyRepository struct{ db *sql.DB }

func NewResultCachePolicyRepository(db *sql.DB) service.ResultCachePolicyRepository {
	return &resultCachePolicyRepository{db: db}
}

// Derive both revision sources in one authoritative database snapshot. Timestamps
// alone miss content-only migrations and MAX(settings.updated_at) can hide edits
// or deletion of older settings. Hash inside PostgreSQL: credentials and setting
// values never leave this query, and Current still returns only config/status/hash.
// The complete settings set is intentionally conservative: any setting can affect
// gateway semantics. Unrelated setting changes can therefore invalidate results.
// Account updated_at also changes on quota/last-used writes, so it is not a
// semantic revision. Exclude only billing-owned runtime quota counters/windows
// from extra; limits, reset configuration and unknown extension keys stay hashed.
// Hash all remaining account columns rather than a partial allowlist: expiry,
// parent credential associations and future semantic columns must still invalidate.
// The v3 domain deliberately invalidates existing v2 keys once.
const resultCachePolicyCurrentSQL = `WITH policy_material AS (
 SELECT g.response_result_cache_config, g.status,
  jsonb_build_object(
   'group', to_jsonb(g),
   'accounts', COALESCE((
    SELECT jsonb_agg(jsonb_build_array(
     to_jsonb(a) - ARRAY['updated_at', 'last_used_at', 'extra']::text[],
     COALESCE(a.extra, '{}'::jsonb) - ARRAY[
      'quota_used', 'quota_daily_used', 'quota_weekly_used',
      'quota_daily_start', 'quota_weekly_start',
      'quota_daily_reset_at', 'quota_weekly_reset_at'
     ]::text[], ag.priority
    ) ORDER BY a.id)
    FROM accounts a JOIN account_groups ag ON ag.account_id=a.id
    WHERE ag.group_id=g.id AND a.deleted_at IS NULL
   ), '[]'::jsonb),
   'channels', COALESCE((
    SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id)
    FROM channels c JOIN channel_groups cg ON cg.channel_id=c.id
    WHERE cg.group_id=g.id
   ), '[]'::jsonb),
   'channel_model_pricing', COALESCE((
    SELECT jsonb_agg(to_jsonb(cp) ORDER BY cp.id)
    FROM channel_model_pricing cp
    JOIN channel_groups cg ON cg.channel_id=cp.channel_id
    WHERE cg.group_id=g.id
   ), '[]'::jsonb)
  ) AS effective_route_revision,
  COALESCE((
   SELECT jsonb_agg(jsonb_build_array(s.key, s.value) ORDER BY s.key)
   FROM settings s
  ), '[]'::jsonb) AS input_policy_revision
 FROM groups g WHERE g.id=$1 AND g.deleted_at IS NULL
)
SELECT response_result_cache_config, status,
 encode(sha256(convert_to(jsonb_build_array(
  'result-cache-policy-v3', response_result_cache_config,
  effective_route_revision, input_policy_revision
 )::text, 'UTF8')), 'hex')
FROM policy_material`

// Read authoritative policy, not the request's potentially stale auth snapshot.
func (r *resultCachePolicyRepository) Current(ctx context.Context, id int64) (service.ResultCachePolicy, error) {
	return readResultCachePolicy(ctx, r.db, id)
}

func readResultCachePolicy(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (service.ResultCachePolicy, error) {
	var raw []byte
	var status, version string
	err := db.QueryRowContext(ctx, resultCachePolicyCurrentSQL, id).Scan(&raw, &status, &version)
	if err != nil {
		return service.ResultCachePolicy{}, err
	}
	var cfg service.GroupResponseResultCacheConfig
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return service.ResultCachePolicy{}, err
	}
	cfg, err = service.NormalizeGroupResponseResultCacheConfig(cfg)
	return service.ResultCachePolicy{Config: cfg, Active: status == "active", Version: version}, err
}
