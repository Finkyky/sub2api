//go:build integration

package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// This SQL-only lane uses the same isolation contract as the repository harness.
// It deliberately fails without an explicit DSN; the supplied database is never
// migrated. Each test owns a fresh database, closed and dropped during cleanup.
func newRCRoutePostgres(t *testing.T) (*sql.DB, *dbent.Client) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("SUB2API_TEST_POSTGRES_DSN"))
	require.NotEmpty(t, raw, "set SUB2API_TEST_POSTGRES_DSN to run the PostgreSQL route lane")
	u, err := url.Parse(raw)
	require.NoError(t, err)
	require.True(t, (u.Scheme == "postgres" || u.Scheme == "postgresql") && u.Host != "", "PostgreSQL URL required")
	admin, err := sql.Open("postgres", raw)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, admin.PingContext(ctx))
	name := fmt.Sprintf("sub2api_it_route_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := admin.ExecContext(cleanup, "DROP DATABASE "+pq.QuoteIdentifier(name)+" WITH (FORCE)")
		require.NoError(t, err)
	})
	u.Path = "/" + name
	u.RawPath = ""
	query := u.Query()
	query.Del("dbname")
	query.Set("TimeZone", "UTC")
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return db, client
}

func rcPostgresRepositories(t *testing.T, db *sql.DB, client *dbent.Client) func(*rcRouteFixture) rcRouteRepositories {
	t.Helper()
	return func(f *rcRouteFixture) rcRouteRepositories {
		ctx := context.Background()
		users := repository.NewUserRepository(client, db)
		groups := repository.NewGroupRepository(client, db)
		keys := repository.NewAPIKeyRepository(client, db)
		accounts := repository.NewAccountRepository(client, db, nil)
		group := f.key.Group
		group.Name = "rc-pg-group"
		group.ResponseResultCacheConfig = service.GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 60, MaxResponseBytes: 2 << 20}
		require.NoError(t, groups.Create(ctx, group))
		user := f.key.User
		user.Email = "rc-pg@example.com"
		user.PasswordHash = "fixture-hash"
		user.Concurrency = 4
		require.NoError(t, users.Create(ctx, user))
		f.key.UserID = user.ID
		f.key.GroupID = &group.ID
		f.key.Name = "rc-pg-key"
		f.key.Quota = 100
		f.key.RateLimit5h = 100
		f.key.RateLimit1d = 100
		f.key.RateLimit7d = 100
		require.NoError(t, keys.Create(ctx, f.key))
		f.accounts.account.Concurrency = 4
		f.accounts.account.Extra["quota_daily_limit"] = 100.0
		f.accounts.account.Extra["quota_weekly_limit"] = 100.0
		f.accounts.account.Extra["quota_daily_reset_mode"] = "fixed"
		f.accounts.account.Extra["quota_weekly_reset_mode"] = "fixed"
		f.upstream.responseContent = "pg generation one"
		require.NoError(t, accounts.Create(ctx, &f.accounts.account))
		require.NoError(t, accounts.BindGroups(ctx, f.accounts.account.ID, []int64{group.ID}))
		return rcRouteRepositories{keys: keys, users: users, accounts: accounts, subscriptions: repository.NewUserSubscriptionRepository(client), billing: repository.NewUsageBillingRepository(client, db), usage: repository.NewUsageLogRepository(client, db), policy: repository.NewResultCachePolicyRepository(db), access: repository.NewResultCacheAccessRepository(db)}
	}
}

// Read persisted money and counters together, not a mocked billing command.
type rcPGSnapshot struct {
	Usage, Ledger, Access                             int
	CacheReadTokens                                   int64
	Balance, KeyQuota, KeyRate, AccountQuota          float64
	KeyRate1d, KeyRate7d, AccountDaily, AccountWeekly float64
	KeyWindow5h, KeyWindow1d, KeyWindow7d             sql.NullTime
	AccountExtra                                      string
}

func rcPostgresSnapshot(t *testing.T, db *sql.DB, f *rcRouteFixture) rcPGSnapshot {
	t.Helper()
	var s rcPGSnapshot
	require.NoError(t, db.QueryRow(`SELECT
  (SELECT COUNT(*) FROM usage_logs WHERE api_key_id=$1),
  (SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id=$1),
  (SELECT COUNT(*) FROM result_cache_access_logs WHERE api_key_id=$1),
  (SELECT COALESCE(SUM(cache_read_tokens),0) FROM usage_logs WHERE api_key_id=$1),
  u.balance,k.quota_used,k.usage_5h,COALESCE((a.extra->>'quota_used')::numeric,0),
  k.usage_1d,k.usage_7d,k.window_5h_start,k.window_1d_start,k.window_7d_start,
  COALESCE((a.extra->>'quota_daily_used')::numeric,0),COALESCE((a.extra->>'quota_weekly_used')::numeric,0),a.extra::text
  FROM users u JOIN api_keys k ON k.user_id=u.id CROSS JOIN accounts a
  WHERE k.id=$1 AND a.id=$2`, f.key.ID, f.accounts.account.ID).Scan(&s.Usage, &s.Ledger, &s.Access, &s.CacheReadTokens, &s.Balance, &s.KeyQuota, &s.KeyRate, &s.AccountQuota, &s.KeyRate1d, &s.KeyRate7d, &s.KeyWindow5h, &s.KeyWindow1d, &s.KeyWindow7d, &s.AccountDaily, &s.AccountWeekly, &s.AccountExtra))
	return s
}
func rcPostgresDrain(t *testing.T, pool *service.UsageRecordWorkerPool) {
	t.Helper()
	done := make(chan struct{})
	require.False(t, pool.Submit(func(context.Context) { close(done) }).Dropped())
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("usage worker did not drain")
	}
}
func rcPostgresGeneration(t *testing.T, db *sql.DB, f *rcRouteFixture, id string, mode string) {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM result_cache_access_logs a
  JOIN usage_logs u ON u.request_id=a.request_id AND u.api_key_id=a.api_key_id
  JOIN usage_billing_dedup d ON d.request_id=u.request_id AND d.api_key_id=u.api_key_id
  WHERE a.request_id=$1 AND a.api_key_id=$2 AND a.status=CASE WHEN $3='write_only' THEN 'UPDATED' ELSE 'MISS' END AND a.mode=$3
  AND a.upstream_called AND a.upstream_cost IS NULL AND a.cache_charge=0
  AND u.actual_cost>0 AND u.total_cost>0 AND u.cache_read_tokens=0
  AND a.user_id=u.user_id AND a.group_id=u.group_id AND u.account_id=$4`, id, f.key.ID, mode, f.accounts.account.ID).Scan(&n))
	require.Equal(t, 1, n, "generation access/usage/ledger must join the same persisted identity")
}
func rcPostgresHit(t *testing.T, db *sql.DB, f *rcRouteFixture, id, source string) {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM result_cache_access_logs a
  JOIN usage_logs u ON u.request_id=a.source_request_id AND u.api_key_id=a.api_key_id
  JOIN usage_billing_dedup d ON d.request_id=u.request_id AND d.api_key_id=u.api_key_id
  WHERE a.request_id=$1 AND a.source_request_id=$2 AND a.api_key_id=$3
  AND a.status='HIT' AND NOT a.upstream_called AND a.upstream_cost=0 AND a.cache_charge=0
  AND NOT EXISTS(SELECT 1 FROM usage_logs own WHERE own.request_id=a.request_id AND own.api_key_id=a.api_key_id)
  AND NOT EXISTS(SELECT 1 FROM usage_billing_dedup own WHERE own.request_id=a.request_id AND own.api_key_id=a.api_key_id)`, id, source, f.key.ID).Scan(&n))
	require.Equal(t, 1, n, "HIT must reference the source generation without its own usage or bill")
}

func TestResultCacheGatewayPostgresCorrelationIntegration(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			db, client := newRCRoutePostgres(t)
			pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 8, TaskTimeout: 15 * time.Second})
			// Register immediately so setup failures do not leak a worker.
			t.Cleanup(pool.Stop)
			f := newRCRouteFixtureWithRepositories(t, platform, pool, rcPostgresRepositories(t, db, client))
			// Drain the worker before gateway/cache/DB cleanup (Stop is idempotent).
			t.Cleanup(pool.Stop)
			initial := rcPostgresSnapshot(t, db, f)
			policy := repository.NewResultCachePolicyRepository(db)
			before, err := policy.Current(context.Background(), f.key.Group.ID)
			require.NoError(t, err)
			miss := f.request(t, "read_write")
			requireRCRouteOK(t, miss, "MISS")
			rcPostgresDrain(t, pool)
			rcPostgresGeneration(t, db, f, miss.Header().Get("X-Gateway-Request-ID"), "read_write")
			afterMiss := rcPostgresSnapshot(t, db, f)
			require.Equal(t, 1, afterMiss.Usage)
			require.Equal(t, 1, afterMiss.Ledger)
			require.Equal(t, 1, afterMiss.Access)
			require.Less(t, afterMiss.Balance, initial.Balance)
			require.Positive(t, afterMiss.KeyQuota)
			require.Positive(t, afterMiss.KeyRate)
			require.Positive(t, afterMiss.AccountQuota)
			require.Positive(t, afterMiss.AccountDaily)
			require.Positive(t, afterMiss.AccountWeekly)
			require.Positive(t, afterMiss.KeyRate1d)
			require.Positive(t, afterMiss.KeyRate7d)
			require.True(t, afterMiss.KeyWindow5h.Valid && afterMiss.KeyWindow1d.Valid && afterMiss.KeyWindow7d.Valid)
			require.Contains(t, afterMiss.AccountExtra, "quota_daily_reset_at")
			require.Contains(t, afterMiss.AccountExtra, "quota_weekly_reset_at")
			require.Equal(t, "pg generation one", rcPostgresAnswer(t, miss.Body.Bytes()))
			after, err := policy.Current(context.Background(), f.key.Group.ID)
			require.NoError(t, err)
			hit := f.request(t, "read_write")
			requireRCRouteOK(t, hit, "HIT")
			require.Equal(t, before.Version, after.Version, "ordinary persisted billing/last-used must not invalidate the route version")
			rcPostgresDrain(t, pool)
			require.NotEqual(t, miss.Header().Get("X-Gateway-Request-ID"), hit.Header().Get("X-Gateway-Request-ID"))
			rcPostgresHit(t, db, f, hit.Header().Get("X-Gateway-Request-ID"), miss.Header().Get("X-Gateway-Request-ID"))
			afterHit := rcPostgresSnapshot(t, db, f)
			afterMiss.Access++
			require.Equal(t, afterMiss, afterHit, "HIT adds exactly one free access row and changes no money/usage counters")
			require.Equal(t, "pg generation one", rcPostgresAnswer(t, hit.Body.Bytes()))
			f.upstream.mu.Lock()
			f.upstream.responseContent = "pg generation two"
			f.upstream.mu.Unlock()
			write := f.request(t, "write_only")
			requireRCRouteOK(t, write, "UPDATED")
			rcPostgresDrain(t, pool)
			rcPostgresGeneration(t, db, f, write.Header().Get("X-Gateway-Request-ID"), "write_only")
			require.Equal(t, "pg generation two", rcPostgresAnswer(t, write.Body.Bytes()))
			afterWrite := rcPostgresSnapshot(t, db, f)
			require.Equal(t, 2, afterWrite.Usage)
			require.Equal(t, 2, afterWrite.Ledger)
			require.Equal(t, 3, afterWrite.Access)
			require.Less(t, afterWrite.Balance, afterHit.Balance)
			require.Greater(t, afterWrite.KeyQuota, afterHit.KeyQuota)
			require.Greater(t, afterWrite.AccountQuota, afterHit.AccountQuota)
			hit2 := f.request(t, "read_write")
			requireRCRouteOK(t, hit2, "HIT")
			rcPostgresDrain(t, pool)
			rcPostgresHit(t, db, f, hit2.Header().Get("X-Gateway-Request-ID"), write.Header().Get("X-Gateway-Request-ID"))
			final := rcPostgresSnapshot(t, db, f)
			afterWrite.Access++
			require.Equal(t, afterWrite, final)
			require.Equal(t, "pg generation two", rcPostgresAnswer(t, hit2.Body.Bytes()))
			require.Zero(t, final.CacheReadTokens)
			require.Equal(t, 2, f.upstream.count())
			t.Logf("PG_CHAIN platform=%s usage=%d ledger=%d access=%d upstream=%d hit_money_unchanged=true source_join=true cache_read_tokens=%d", platform, final.Usage, final.Ledger, final.Access, f.upstream.count(), final.CacheReadTokens)
		})
	}
}

func rcPostgresAnswer(t *testing.T, body []byte) string {
	t.Helper()
	var reply struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	require.NoError(t, json.Unmarshal(body, &reply))
	require.Len(t, reply.Choices, 1)
	return reply.Choices[0].Message.Content
}
