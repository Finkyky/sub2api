//go:build unit

package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Each top-level PG test owns a fresh database. No repository integration helper,
// shared schema, scheduler migrations or live application tables are touched.
func capabilityPostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SUB2API_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SUB2API_TEST_POSTGRES_DSN for isolated PostgreSQL capability tests")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, admin.Ping())
	var random [12]byte
	_, err = rand.Read(random[:])
	require.NoError(t, err)
	name := "m5_cap_" + hex.EncodeToString(random[:])
	_, err = admin.Exec(`CREATE DATABASE "` + name + `"`)
	require.NoError(t, err)
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_, e := admin.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`)
		if e != nil {
			t.Errorf("drop owned test database: %v", e)
		}
		admin.Close()
	})
	_, err = db.Exec(`CREATE TABLE sub2api_plugin_instances(id BIGINT PRIMARY KEY);
 INSERT INTO sub2api_plugin_instances VALUES(7),(8);
 CREATE TABLE channel_model_pricing(id BIGINT PRIMARY KEY,channel_id BIGINT NOT NULL,input_price NUMERIC(20,12),output_price NUMERIC(20,12),cache_write_price NUMERIC(20,12),cache_read_price NUMERIC(20,12),per_request_price NUMERIC(20,10),updated_at TIMESTAMPTZ DEFAULT NOW());
 INSERT INTO channel_model_pricing(id,channel_id,input_price,output_price) VALUES(101,1,1,2),(102,1,3,4),(201,2,5,6);`)
	require.NoError(t, err)
	_, file, _, _ := runtime.Caller(0)
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", "255_plugin_task_capabilities.sql"))
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	// Re-application tests the migration's idempotent DDL too.
	tx, err = db.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return db
}
func preparedPricingBroker(t *testing.T, db *sql.DB) (*PluginTaskCapabilityBroker, *PluginTaskClaim, *PluginInstanceSnapshot) {
	t.Helper()
	c, s := capabilityFixture()
	c.Task.Scope = PluginTaskScope{ChannelIDs: []int64{1}, PricingFields: []string{"input_price", "output_price", "per_request_price"}, KVKeys: []string{"cursor", "remove"}}
	s.Installation.Manifest.General.HostCapabilities = []string{PluginHostPricingReadV1, PluginHostPricingWriteV1, PluginHostKVV1}
	b := NewPluginTaskCapabilityBroker(db, nil)
	_, err := b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	t.Cleanup(func() { b.Discard(c) })
	return b, c, s
}
func capabilityResult(raw string) contract.TaskResult {
	return contract.TaskResult{Status: contract.TaskSucceeded, Changed: true, Output: json.RawMessage(raw)}
}
func capabilityPrice(t *testing.T, db *sql.DB, id int64) float64 {
	t.Helper()
	var v float64
	require.NoError(t, db.QueryRow(`SELECT input_price FROM channel_model_pricing WHERE id=$1`, id).Scan(&v))
	return v
}
func TestPluginTaskCapabilityPGEnvelopeCommitHistoryIdempotency(t *testing.T) {
	db := capabilityPostgres(t)
	b, c, s := preparedPricingBroker(t, db)
	_, err := db.Exec(`INSERT INTO sub2api_plugin_task_kv(instance_id,key,value) VALUES(7,'cursor','{"v":1}'),(8,'cursor','{"private":true}'),(7,'other','"not authorized"'),(7,'remove','1')`)
	require.NoError(t, err)
	raw, err := b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	var envelope pluginTaskEnvelope
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.JSONEq(t, `{"v":1}`, string(envelope.Host.KV["cursor"]))
	require.NotContains(t, envelope.Host.KV, "other")
	require.Len(t, envelope.Host.Pricing, 2)
	require.Nil(t, envelope.Host.Pricing[0].Fields["per_request_price"])
	require.NotContains(t, envelope.Host.Pricing[0].Fields, "cache_read_price")
	snapshot := *envelope.Host.Pricing[0].Fields["input_price"]
	result := capabilityResult(`{"summary":"sensitive summary","price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9.25,"per_request_price":0.12345678901}},{"channel_id":1,"pricing_id":102,"fields":{"input_price":0}}],"kv_updates":[{"key":"cursor","value":{"next":2}},{"key":"remove","delete":true}]}`)
	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background(), tx, c, result))
	require.NoError(t, tx.Commit())
	require.Equal(t, 9.25, capabilityPrice(t, db, 101))
	require.Equal(t, 0.0, capabilityPrice(t, db, 102))
	require.Equal(t, 1.0, snapshot)
	require.Equal(t, 1.0, *envelope.Host.Pricing[0].Fields["input_price"])
	var value string
	require.NoError(t, db.QueryRow(`SELECT value::text FROM sub2api_plugin_task_kv WHERE instance_id=7 AND key='cursor'`).Scan(&value))
	require.JSONEq(t, `{"next":2}`, value)
	require.NoError(t, db.QueryRow(`SELECT value::text FROM sub2api_plugin_task_kv WHERE instance_id=8 AND key='cursor'`).Scan(&value))
	require.JSONEq(t, `{"private":true}`, value)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_plugin_task_kv WHERE instance_id=7 AND key='remove'`).Scan(&count))
	require.Zero(t, count)
	history, err := b.PricingHistory(context.Background(), 1, 101, 20)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, c.Run.ID, history[0].RunID)
	require.Equal(t, int64(7), history[0].InstanceID)
	require.Equal(t, "sample.price", history[0].SourcePlugin)
	require.Equal(t, "refresh", history[0].SourceEntry)
	require.Equal(t, 1.0, *history[0].OldValues["input_price"])
	require.Nil(t, history[0].OldValues["per_request_price"])
	require.Equal(t, 0.123456789, *history[0].NewValues["per_request_price"])
	// Retry/replay is one business mutation even across a new broker/run attempt.
	c.Run.ID++
	c.Run.Attempt++
	_, err = b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	tx, err = db.Begin()
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background(), tx, c, result))
	require.NoError(t, tx.Commit())
	history, err = b.PricingHistory(context.Background(), 1, 101, 20)
	require.NoError(t, err)
	require.Len(t, history, 1)
	tx, err = db.Begin()
	require.NoError(t, err)
	err = b.Commit(context.Background(), tx, c, capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":99}}]}`))
	require.ErrorIs(t, err, ErrPluginTaskConflict)
	require.NoError(t, tx.Commit())
	require.Equal(t, 9.25, capabilityPrice(t, db, 101))
}
func TestPluginTaskCapabilityPGRejectBatchPreservesEverything(t *testing.T) {
	db := capabilityPostgres(t)
	cases := []string{
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}},{"channel_id":1,"pricing_id":999,"fields":{"input_price":1}}],"kv_updates":[{"key":"cursor","value":1}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}},{"channel_id":1,"pricing_id":201,"fields":{"input_price":1}}]}`,
		`{"price_updates":[{"channel_id":2,"pricing_id":201,"fields":{"input_price":1}}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"image_output_price":1}}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"cache_read_price":1}}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":-1}}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":1e309}}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":null}}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":100000000}}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}}],"kv_updates":[{"key":"other","value":1}]}`,
		`{"kv_updates":[{"key":"cursor","value":1},{"key":"cursor","value":2}]}`,
		`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}},{"channel_id":1,"pricing_id":101,"fields":{"output_price":8}}]}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			b, c, _ := preparedPricingBroker(t, db)
			tx, err := db.Begin()
			require.NoError(t, err)
			require.Error(t, b.Commit(context.Background(), tx, c, capabilityResult(raw)))
			require.NoError(t, tx.Commit())
			require.Equal(t, 1.0, capabilityPrice(t, db, 101))
			var count int
			require.NoError(t, db.QueryRow(`SELECT (SELECT count(*) FROM sub2api_plugin_pricing_versions)+(SELECT count(*) FROM sub2api_plugin_task_kv)+(SELECT count(*) FROM sub2api_plugin_task_capability_commits)`).Scan(&count))
			require.Zero(t, count)
		})
	}
}
func TestPluginTaskCapabilityPGSavepointAndCallerRollback(t *testing.T) {
	db := capabilityPostgres(t)
	b, c, _ := preparedPricingBroker(t, db)
	result := capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}}],"kv_updates":[{"key":"cursor","value":1}]}`)
	// Force a SQL failure after a successful price+history write. Committing the
	// caller transaction despite broker error must preserve the old price/history.
	_, err := db.Exec(`ALTER TABLE sub2api_plugin_task_kv ADD CONSTRAINT fail_kv CHECK (key <> 'cursor')`)
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	require.Error(t, b.Commit(context.Background(), tx, c, result))
	require.NoError(t, tx.Commit())
	require.Equal(t, 1.0, capabilityPrice(t, db, 101))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_plugin_pricing_versions`).Scan(&count))
	require.Zero(t, count)
	_, err = db.Exec(`ALTER TABLE sub2api_plugin_task_kv DROP CONSTRAINT fail_kv`)
	require.NoError(t, err)
	tx, err = db.Begin()
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background(), tx, c, result))
	require.NoError(t, tx.Rollback())
	require.Equal(t, 1.0, capabilityPrice(t, db, 101))
	// Failed task output cannot mutate business data.
	tx, err = db.Begin()
	require.NoError(t, err)
	result.Status = contract.TaskFailed
	result.ErrorCode = "task_failed"
	require.NoError(t, b.Commit(context.Background(), tx, c, result))
	require.NoError(t, tx.Commit())
	require.Equal(t, 1.0, capabilityPrice(t, db, 101))
}
func TestPluginTaskCapabilityPGFrozenAndReadOnly(t *testing.T) {
	db := capabilityPostgres(t)
	b, c, s := preparedPricingBroker(t, db)
	result := capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}}]}`)
	c.Task.Scope.ChannelIDs = append(c.Task.Scope.ChannelIDs, 2)
	tx, err := db.Begin()
	require.NoError(t, err)
	require.ErrorIs(t, b.Commit(context.Background(), tx, c, result), ErrPluginTaskCapability)
	require.NoError(t, tx.Rollback())
	c.Task.Scope.ChannelIDs = c.Task.Scope.ChannelIDs[:1]
	s.Installation.Manifest.General.HostCapabilities = []string{PluginHostPricingReadV1, PluginHostKVV1}
	_, err = b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	tx, err = db.Begin()
	require.NoError(t, err)
	require.ErrorIs(t, b.Commit(context.Background(), tx, c, result), ErrPluginTaskCapability)
	require.NoError(t, tx.Rollback())
	require.Equal(t, 1.0, capabilityPrice(t, db, 101))
}
func TestPluginTaskCapabilityPGConcurrentReplay(t *testing.T) {
	db := capabilityPostgres(t)
	b, c, s := preparedPricingBroker(t, db)
	other := *c
	other.Run.ID++
	b2 := NewPluginTaskCapabilityBroker(db, nil)
	_, err := b2.Prepare(context.Background(), &other, s)
	require.NoError(t, err)
	defer b2.Discard(&other)
	result := capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}}],"kv_updates":[{"key":"cursor","value":1}]}`)
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for _, item := range []struct {
		b *PluginTaskCapabilityBroker
		c *PluginTaskClaim
	}{{b, c}, {b2, &other}} {
		wg.Add(1)
		go func(b *PluginTaskCapabilityBroker, c *PluginTaskClaim) {
			defer wg.Done()
			tx, e := db.Begin()
			if e == nil {
				e = b.Commit(context.Background(), tx, c, result)
				if e == nil {
					e = tx.Commit()
				} else {
					tx.Rollback()
				}
			}
			errors <- e
		}(item.b, item.c)
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		require.NoError(t, e)
	}
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_plugin_pricing_versions`).Scan(&count))
	require.Equal(t, 1, count)
}
func TestPluginTaskCapabilityInvalidFloatAndFrozenProjection(t *testing.T) {
	c, s := capabilityFixture()
	c.Task.Scope = PluginTaskScope{ChannelIDs: []int64{1}, PricingFields: []string{"input_price"}}
	s.Installation.Manifest.General.HostCapabilities = []string{PluginHostPricingWriteV1}
	grant, err := validatePluginTaskGrant(c, s)
	require.NoError(t, err)
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		require.Error(t, validatePluginTaskMutations(grant, PluginTaskCapabilityOutput{PriceUpdates: []PluginTaskPriceUpdate{{ChannelID: 1, PricingID: 101, Fields: map[string]float64{"input_price": value}}}}))
	}
	s.Installation.Manifest.General.HostCapabilities[0] = PluginHostPricingReadV1
	c.Task.Scope.PricingFields[0] = "output_price"
	require.Equal(t, PluginHostPricingWriteV1, grant.capabilities[0])
	require.Equal(t, "input_price", grant.scope.PricingFields[0])
	require.Error(t, validatePluginTaskMutations(grant, PluginTaskCapabilityOutput{KVUpdates: []PluginTaskKVUpdate{{Key: strings.Repeat("x", 161), Value: json.RawMessage(`1`)}}}))
}

type capabilityCacheRepo struct {
	ChannelRepository
	db *sql.DB
}

func (r capabilityCacheRepo) ListAll(ctx context.Context) ([]Channel, error) {
	p, err := scanPluginPricing(r.db.QueryRowContext(ctx, `SELECT id,channel_id,input_price,output_price,cache_write_price,cache_read_price,per_request_price FROM channel_model_pricing WHERE id=101`))
	if err != nil {
		return nil, err
	}
	return []Channel{{ID: 1, GroupIDs: []int64{1}, ModelPricing: []ChannelModelPricing{{ID: 101, ChannelID: 1, Platform: PlatformOpenAI, Models: []string{"model"}, InputPrice: p.Fields["input_price"]}}}}, nil
}
func (r capabilityCacheRepo) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{1: PlatformOpenAI}, nil
}
func TestPluginTaskCapabilityPGAfterCommitPreservesAcquiredCacheSnapshot(t *testing.T) {
	db := capabilityPostgres(t)
	b, c, _ := preparedPricingBroker(t, db)
	channels := NewChannelService(capabilityCacheRepo{db: db}, nil, nil, nil, nil)
	b.channels = channels
	cache, err := channels.loadCache(context.Background())
	require.NoError(t, err)
	key := channelModelKey{groupID: 1, platform: PlatformOpenAI, model: "model"}
	acquired := cache.pricingByGroupModel[key]
	require.NotNil(t, acquired)
	require.Equal(t, 1.0, *acquired.InputPrice)
	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background(), tx, c, capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}}]}`)))
	require.NoError(t, tx.Commit())
	b.AfterCommit()
	next, err := channels.loadCache(context.Background())
	require.NoError(t, err)
	require.Equal(t, 9.0, *next.pricingByGroupModel[key].InputPrice)
	require.Equal(t, 1.0, *acquired.InputPrice)
	require.NotSame(t, cache, next)
}

func TestPluginTaskCapabilityPGDifferentTasksSameKey(t *testing.T) {
	db := capabilityPostgres(t)
	b, c, s := preparedPricingBroker(t, db)
	first := capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}}],"kv_updates":[{"key":"cursor","value":1}]}`)
	tx, err := db.Begin()
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background(), tx, c, first))
	require.NoError(t, tx.Commit())
	other := *c
	other.Task.ID = 2
	other.Run.TaskID = 2
	other.Run.ID = 22
	b2 := NewPluginTaskCapabilityBroker(db, nil)
	_, err = b2.Prepare(context.Background(), &other, s)
	require.NoError(t, err)
	defer b2.Discard(&other)
	second := capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":11}}],"kv_updates":[{"key":"cursor","value":2}]}`)
	tx, err = db.Begin()
	require.NoError(t, err)
	err = b2.Commit(context.Background(), tx, &other, second)
	if err != nil {
		tx.Rollback()
	}
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.Equal(t, 11.0, capabilityPrice(t, db, 101))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_plugin_task_capability_commits`).Scan(&count))
	require.Equal(t, 2, count)
	history, err := b.PricingHistory(context.Background(), 1, 101, 20)
	require.NoError(t, err)
	require.Len(t, history, 2)
	// Retry of task #2 remains idempotent despite sharing task #1's logical key.
	tx, err = db.Begin()
	require.NoError(t, err)
	require.NoError(t, b2.Commit(context.Background(), tx, &other, second))
	require.NoError(t, tx.Commit())
	history, err = b.PricingHistory(context.Background(), 1, 101, 20)
	require.NoError(t, err)
	require.Len(t, history, 2)
}

func TestPluginTaskCapabilityPGExplicitPricingEntries(t *testing.T) {
	db := capabilityPostgres(t)
	b, c, s := preparedPricingBroker(t, db)
	c.Task.Scope.PricingIDs = []int64{101}
	raw, err := b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	var env pluginTaskEnvelope
	require.NoError(t, json.Unmarshal(raw, &env))
	require.Len(t, env.Host.Pricing, 1)
	require.Equal(t, int64(101), env.Host.Pricing[0].PricingID)
	tx, err := db.Begin()
	require.NoError(t, err)
	err = b.Commit(context.Background(), tx, c, capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}},{"channel_id":1,"pricing_id":102,"fields":{"input_price":8}}]}`))
	require.ErrorIs(t, err, ErrPluginTaskCapability)
	require.NoError(t, tx.Commit())
	require.Equal(t, 1.0, capabilityPrice(t, db, 101))
	require.Equal(t, 3.0, capabilityPrice(t, db, 102))
	tx, err = db.Begin()
	require.NoError(t, err)
	require.NoError(t, b.Commit(context.Background(), tx, c, capabilityResult(`{"price_updates":[{"channel_id":1,"pricing_id":101,"fields":{"input_price":9}}]}`)))
	require.NoError(t, tx.Commit())
	require.Equal(t, 9.0, capabilityPrice(t, db, 101))
	require.Equal(t, 3.0, capabilityPrice(t, db, 102))
	history, err := b.PricingHistory(context.Background(), 1, 101, 10)
	require.NoError(t, err)
	require.Equal(t, c.Task.ID, history[0].TaskID)
	for _, ids := range [][]int64{{101, 999}, {201}, {101, 201}} {
		c.Task.Scope.PricingIDs = ids
		_, err = b.Prepare(context.Background(), c, s)
		require.ErrorIs(t, err, ErrPluginTaskCapability)
		require.Empty(t, b.grants)
	}
	// Even write-only tasks must reject a mismatched or missing explicit entry.
	s.Installation.Manifest.General.HostCapabilities = []string{PluginHostPricingWriteV1, PluginHostKVV1}
	c.Task.Scope.PricingIDs = []int64{201}
	_, err = b.Prepare(context.Background(), c, s)
	require.ErrorIs(t, err, ErrPluginTaskCapability)
	c.Task.Scope.PricingIDs = []int64{101}
	raw, err = b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &env))
	require.Empty(t, env.Host.Pricing)
}
