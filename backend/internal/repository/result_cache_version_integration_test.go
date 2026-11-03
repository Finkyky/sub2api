//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type rcVersionFixture struct {
	group, account, channel int64
	policy                  service.ResultCachePolicyRepository
	cache                   *service.ResultCacheService
	store                   service.ResultCacheStore
}

func newRCVersionFixture(t *testing.T) *rcVersionFixture {
	t.Helper()
	ctx := context.Background()
	repo := newGroupRepositoryWithSQL(testEntClient(t), integrationDB)
	g := rcIntegrationGroup(t, repo)
	f := &rcVersionFixture{group: g.ID, policy: NewResultCachePolicyRepository(integrationDB)}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,extra,status,updated_at) VALUES($1,'openai','apikey','{"model_mapping":{"m":"alpha"},"api_key":"fixture-secret-canary"}','{}','active','2020-01-01') RETURNING id`, fmt.Sprintf("rc-version-account-%d", g.ID)).Scan(&f.account))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO channels(name,status,model_mapping,updated_at) VALUES($1,'active','{"openai":{"m":"alpha"}}','2020-01-01') RETURNING id`, fmt.Sprintf("rc-version-channel-%d", g.ID)).Scan(&f.channel))
	rcVersionExec(t, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,0)`, f.account, f.group)
	rcVersionExec(t, `INSERT INTO channel_groups(channel_id,group_id) VALUES($1,$2)`, f.channel, f.group)
	rcVersionExec(t, `INSERT INTO channel_model_pricing(channel_id,models,platform) VALUES($1,'["m"]','openai')`, f.channel)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM channels WHERE id=$1`, `DELETE FROM accounts WHERE id=$1`, `DELETE FROM groups WHERE id=$1`} {
			id := f.channel
			if q == `DELETE FROM accounts WHERE id=$1` {
				id = f.account
			}
			if q == `DELETE FROM groups WHERE id=$1` {
				id = f.group
			}
			_, err := integrationDB.ExecContext(context.Background(), q, id)
			require.NoError(t, err)
		}
	})
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	f.store = NewResultCacheStore(rdb)
	f.cache = service.NewResultCacheService(f.store, f.policy, nil, nil)
	t.Cleanup(f.cache.Stop)
	return f
}
func rcVersionExec(t *testing.T, q string, args ...any) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), q, args...)
	require.NoError(t, err)
}
func rcVersionSettings(t *testing.T) {
	t.Helper()
	for _, key := range []string{"content_moderation_config", "prompt_audit_config", "risk_control_enabled", "rc_version_max_blocker"} {
		var value string
		var updated time.Time
		err := integrationDB.QueryRowContext(context.Background(), `SELECT value,updated_at FROM settings WHERE key=$1`, key).Scan(&value, &updated)
		require.True(t, err == nil || err == sql.ErrNoRows)
		exists := err == nil
		t.Cleanup(func() {
			if exists {
				rcVersionExec(t, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,$3) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, key, value, updated)
			} else {
				rcVersionExec(t, `DELETE FROM settings WHERE key=$1`, key)
			}
		})
		initial := `{"revision":1,"enabled":true}`
		if key == "risk_control_enabled" {
			initial = "true"
		}
		timestamp := "2020-01-01"
		if key == "rc_version_max_blocker" {
			timestamp = "2099-01-01"
		}
		rcVersionExec(t, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,$3::timestamptz) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, key, initial, timestamp)
	}
}
func (f *rcVersionFixture) begin(t *testing.T, identity, mode string) *service.ResultCacheSession {
	t.Helper()
	q, err := f.cache.Begin(context.Background(), service.ResultCacheIntent{Declared: true, Mode: mode, Scope: "user"}, service.ResultCacheKeyMaterial{GroupID: f.group, ScopeIdentity: identity, Endpoint: "/v1/chat/completions", EffectiveModel: "m", EffectiveRequest: []byte(`{"model":"m","messages":[{"role":"user","content":"q"}]}`)}, service.ResultCacheAccess{RequestID: identity})
	require.NoError(t, err)
	require.NotNil(t, q)
	return q
}

var rcVersionBody = []byte(`{"id":"chatcmpl-source","model":"m","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"total_tokens":5}}`)

func TestResultCacheVersionSourcesIntegration(t *testing.T) {
	f := newRCVersionFixture(t)
	rcVersionSettings(t)
	cases := []struct {
		name, query string
		args        []any
	}{
		{"channel_model_mapping", `UPDATE channels SET model_mapping='{"openai":{"m":"beta"}}' WHERE id=$1`, []any{f.channel}},
		{"account_model_mapping", `UPDATE accounts SET credentials=jsonb_set(credentials,'{model_mapping}','{"m":"beta"}') WHERE id=$1`, []any{f.account}},
		{"group_model_routing", `UPDATE groups SET model_routing='{"m":[42]}',model_routing_enabled=true WHERE id=$1`, []any{f.group}},
		{"group_default_model", `UPDATE groups SET default_mapped_model='beta' WHERE id=$1`, []any{f.group}},
		{"channel_features", `UPDATE channels SET features_config='{"web_search":{"enabled":true}}' WHERE id=$1`, []any{f.channel}},
		{"channel_restriction", `UPDATE channels SET restrict_models=true WHERE id=$1`, []any{f.channel}},
		{"channel_pricing_models", `UPDATE channel_model_pricing SET models='["m","beta"]' WHERE channel_id=$1`, []any{f.channel}},
		{"account_extra", `UPDATE accounts SET extra='{"force_response_model":"beta"}' WHERE id=$1`, []any{f.account}},
		{"membership_priority", `UPDATE account_groups SET priority=17 WHERE account_id=$1 AND group_id=$2`, []any{f.account, f.group}},
		{"content_moderation_config", `UPDATE settings SET value='{"revision":2,"enabled":false}' WHERE key='content_moderation_config'`, nil},
		{"prompt_audit_config", `UPDATE settings SET value='{"config_version":2,"enabled":false}' WHERE key='prompt_audit_config'`, nil},
		{"risk_control_enabled", `UPDATE settings SET value='false' WHERE key='risk_control_enabled'`, nil},
		{"input_policy_delete", `DELETE FROM settings WHERE key='content_moderation_config'`, nil},
		{"input_policy_insert", `INSERT INTO settings(key,value,updated_at) VALUES('content_moderation_config','{"revision":3}','2020-01-01')`, nil},
		{"account_membership_delete", `DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, []any{f.account, f.group}},
		{"channel_membership_delete", `DELETE FROM channel_groups WHERE channel_id=$1 AND group_id=$2`, []any{f.channel, f.group}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := f.begin(t, tc.name, "read_write")
			require.Equal(t, "MISS", q.Status)
			q.NoteUpstream("m")
			q.Complete(context.Background(), 200, rcVersionBody)
			hit := f.begin(t, tc.name, "read_only")
			require.Equal(t, "HIT", hit.Status)
			hit.Complete(context.Background(), 200, nil)
			var beforeMax time.Time
			require.NoError(t, integrationDB.QueryRow(`SELECT MAX(updated_at) FROM settings`).Scan(&beforeMax))
			rcVersionExec(t, tc.query, tc.args...)
			var afterMax time.Time
			require.NoError(t, integrationDB.QueryRow(`SELECT MAX(updated_at) FROM settings`).Scan(&afterMax))
			require.Equal(t, beforeMax, afterMax, "the unrelated newest setting must not change")
			after := f.begin(t, tc.name, "read_only")
			defer after.Complete(context.Background(), 200, nil)
			require.Equal(t, "MISS", after.Status, "changed authoritative content must not HIT the old key")
			require.NotEqual(t, q.Key, after.Key)
			old, err := f.store.Get(context.Background(), q.Key)
			require.NoError(t, err)
			require.NotNil(t, old, "old entries are retained until TTL, not scan-deleted")
			t.Logf("VERSION_SOURCE changed=%s before_status=HIT after_status=MISS key_changed=true old_entry_retained=true", tc.name)
		})
	}
}
func TestResultCacheVersionInFlightIntegration(t *testing.T) {
	f := newRCVersionFixture(t)
	rcVersionSettings(t)
	for _, which := range []string{"route", "input_policy"} {
		t.Run(which, func(t *testing.T) {
			q := f.begin(t, which, "write_only")
			require.Equal(t, "MISS", q.Status)
			if which == "route" {
				rcVersionExec(t, `UPDATE channels SET model_mapping='{"openai":{"m":"late"}}' WHERE id=$1`, f.channel)
			} else {
				rcVersionExec(t, `UPDATE settings SET value='{"config_version":9}' WHERE key='prompt_audit_config'`)
			}
			q.NoteUpstream("m")
			q.Complete(context.Background(), 200, rcVersionBody)
			require.Equal(t, "BYPASS", q.Status)
			require.Equal(t, "policy_changed", q.Reason)
			entry, err := f.store.Get(context.Background(), q.Key)
			require.NoError(t, err)
			require.Nil(t, entry)
			t.Logf("IN_FLIGHT changed=%s status=BYPASS reason=policy_changed stale_entry_absent=true", which)
		})
	}
}
func TestResultCacheVersionDeterminismIntegration(t *testing.T) {
	f := newRCVersionFixture(t)
	before, err := f.policy.Current(context.Background(), f.group)
	require.NoError(t, err)
	require.Regexp(t, `^[a-f0-9]{64}$`, before.Version)
	repeated, err := f.policy.Current(context.Background(), f.group)
	require.NoError(t, err)
	require.Equal(t, before.Version, repeated.Version)
	rcVersionExec(t, `UPDATE accounts SET credentials='{"api_key":"fixture-secret-canary","model_mapping":{"m":"alpha"}}' WHERE id=$1`, f.account)
	after, err := f.policy.Current(context.Background(), f.group)
	require.NoError(t, err)
	require.Equal(t, before.Version, after.Version, "JSONB key order must not change the version")
	require.NotContains(t, after.Version, "fixture-secret-canary")
	require.Len(t, after.Version, 64)
	// Relation insertion order is not semantic. Recreate the same membership
	// with the same priority, without touching either account or group content.
	rcVersionExec(t, `DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, f.account, f.group)
	rcVersionExec(t, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,0)`, f.account, f.group)
	relinked, err := f.policy.Current(context.Background(), f.group)
	require.NoError(t, err)
	require.Equal(t, before.Version, relinked.Version)
	// Mutating a channel/account outside this group must not change its route.
	var outside int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES($1,'openai','apikey','{}') RETURNING id`, fmt.Sprintf("rc-outside-%d", f.group)).Scan(&outside))
	t.Cleanup(func() { rcVersionExec(t, `DELETE FROM accounts WHERE id=$1`, outside) })
	rcVersionExec(t, `UPDATE accounts SET credentials='{"model_mapping":{"m":"outside"}}' WHERE id=$1`, outside)
	unchanged, err := f.policy.Current(context.Background(), f.group)
	require.NoError(t, err)
	require.Equal(t, before.Version, unchanged.Version)
	// Committed route content is visible even without timestamp maintenance.
	var tx *sql.Tx
	tx, err = integrationDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = tx.Exec(`UPDATE channels SET model_mapping='{"openai":{"m":"transaction"}}' WHERE id=$1`, f.channel)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	changed, err := f.policy.Current(context.Background(), f.group)
	require.NoError(t, err)
	require.NotEqual(t, before.Version, changed.Version)
	t.Log("POLICY_VERSION sha256=true stable_snapshot=true jsonb_key_order_stable=true secrets_returned=false")
}

// Normal billing and last-used writes are not response semantics. Keep all
// unknown extra fields and quota configuration authoritative, but ignore only
// the explicit runtime bookkeeping keys written by the billing repository.
func TestResultCacheVersionAccountBookkeepingIntegration(t *testing.T) {
	f := newRCVersionFixture(t)
	for _, key := range []string{"quota_used", "quota_daily_used", "quota_weekly_used", "quota_daily_start", "quota_weekly_start", "quota_daily_reset_at", "quota_weekly_reset_at"} {
		t.Run(key, func(t *testing.T) {
			before, err := f.policy.Current(context.Background(), f.group)
			require.NoError(t, err)
			rcVersionExec(t, `UPDATE accounts SET extra=jsonb_set(extra,ARRAY[$2]::text[],'"runtime-value"'),updated_at=NOW() WHERE id=$1`, f.account, key)
			after, err := f.policy.Current(context.Background(), f.group)
			require.NoError(t, err)
			require.Equal(t, before.Version, after.Version, "runtime bookkeeping must not invalidate result keys")
		})
	}
	t.Run("last_used", func(t *testing.T) {
		before, err := f.policy.Current(context.Background(), f.group)
		require.NoError(t, err)
		rcVersionExec(t, `UPDATE accounts SET last_used_at=NOW(),updated_at=NOW() WHERE id=$1`, f.account)
		after, err := f.policy.Current(context.Background(), f.group)
		require.NoError(t, err)
		require.Equal(t, before.Version, after.Version)
	})
	for _, tc := range []struct{ key, first, second string }{
		{"quota_limit", "100", "200"}, {"quota_daily_limit", "100", "200"}, {"quota_weekly_limit", "100", "200"},
		{"quota_daily_reset_mode", `"rolling"`, `"fixed"`}, {"quota_daily_reset_hour", "0", "6"},
		{"quota_weekly_reset_mode", `"rolling"`, `"fixed"`}, {"quota_weekly_reset_day", "1", "2"}, {"quota_weekly_reset_hour", "0", "6"},
		{"quota_reset_timezone", `"UTC"`, `"Asia/Shanghai"`}, {"unknown_semantic_extension", `"alpha"`, `"beta"`},
	} {
		t.Run(tc.key, func(t *testing.T) {
			for _, value := range []string{tc.first, tc.second, ""} {
				before, err := f.policy.Current(context.Background(), f.group)
				require.NoError(t, err)
				if value == "" {
					rcVersionExec(t, `UPDATE accounts SET extra=extra-$2 WHERE id=$1`, f.account, tc.key)
				} else {
					rcVersionExec(t, `UPDATE accounts SET extra=jsonb_set(extra,ARRAY[$2]::text[],$3::jsonb) WHERE id=$1`, f.account, tc.key, value)
				}
				after, err := f.policy.Current(context.Background(), f.group)
				require.NoError(t, err)
				require.NotEqual(t, before.Version, after.Version, "quota config insert/update/delete must invalidate")
			}
		})
	}
}

func TestResultCacheVersionAccountSemanticFieldsIntegration(t *testing.T) {
	cases := []struct{ name, query string }{
		{"expires_at", `UPDATE accounts SET expires_at='2029-01-01' WHERE id=$1`},
		{"auto_pause_on_expired", `UPDATE accounts SET auto_pause_on_expired=NOT auto_pause_on_expired WHERE id=$1`},
		{"parent_account_id", `UPDATE accounts SET parent_account_id=$2 WHERE id=$1`},
		{"quota_dimension", `UPDATE accounts SET quota_dimension='global',parent_account_id=NULL WHERE id=$1`},
		{"concurrency", `UPDATE accounts SET concurrency=concurrency+1 WHERE id=$1`},
		{"rate_multiplier", `UPDATE accounts SET rate_multiplier=2 WHERE id=$1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRCVersionFixture(t)
			args := []any{f.account}
			if tc.name == "parent_account_id" || tc.name == "quota_dimension" {
				parents := make([]int64, 2)
				for i := range parents {
					require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra,status) VALUES($1,'openai','apikey','{}','{}','active') RETURNING id`, fmt.Sprintf("rc-parent-%d-%d", f.group, i)).Scan(&parents[i]))
				}
				t.Cleanup(func() {
					rcVersionExec(t, `UPDATE accounts SET parent_account_id=NULL,quota_dimension='global' WHERE id=$1`, f.account)
					for _, parent := range parents {
						rcVersionExec(t, `DELETE FROM accounts WHERE id=$1`, parent)
					}
				})
				// Respect the schema's parent/dimension coupling; parent case changes only
				// the association between two valid parents after warming the old key.
				rcVersionExec(t, `UPDATE accounts SET parent_account_id=$2,quota_dimension='spark' WHERE id=$1`, f.account, parents[0])
				if tc.name == "parent_account_id" {
					args = append(args, parents[1])
				}
			}
			warm := f.begin(t, tc.name, "read_write")
			warm.NoteUpstream("m")
			warm.Complete(context.Background(), 200, rcVersionBody)
			hit := f.begin(t, tc.name, "read_only")
			require.Equal(t, "HIT", hit.Status)
			hit.Complete(context.Background(), 200, nil)
			inFlight := f.begin(t, tc.name, "write_only")
			rcVersionExec(t, tc.query, args...)
			inFlight.NoteUpstream("m")
			inFlight.Complete(context.Background(), 200, rcVersionBody)
			require.Equal(t, "BYPASS", inFlight.Status, "semantic field changes must reject in-flight publication")
			require.Equal(t, "policy_changed", inFlight.Reason)
			after := f.begin(t, tc.name, "read_only")
			defer after.Complete(context.Background(), 200, nil)
			require.Equal(t, "MISS", after.Status)
			require.NotEqual(t, warm.Key, after.Key)
		})
	}
}
