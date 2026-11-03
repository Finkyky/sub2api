package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// This repository exposes two real selection candidates. Production schedulers
// choose the primary first, exclude it after HTTP 503, then select the backup.
type rcFallbackAccounts struct {
	*rcRouteAccounts
	backup service.Account
}

func (r *rcFallbackAccounts) candidates(platforms []string) []service.Account {
	for _, platform := range platforms {
		if platform == r.account.Platform {
			return []service.Account{r.account, r.backup}
		}
	}
	return nil
}
func (r *rcFallbackAccounts) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.candidates([]string{platform}), nil
}
func (r *rcFallbackAccounts) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}
func (r *rcFallbackAccounts) ListSchedulableByGroupIDAndPlatforms(_ context.Context, _ int64, platforms []string) ([]service.Account, error) {
	return r.candidates(platforms), nil
}
func (r *rcFallbackAccounts) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	if id == r.backup.ID {
		a := r.backup
		return &a, nil
	}
	return r.rcRouteAccounts.GetByID(ctx, id)
}

func rcFallbackExpectedModel(platform string, sameModel bool) string {
	if platform == service.PlatformAnthropic {
		if sameModel {
			return "claude-sonnet-4.5"
		}
		return "claude-opus-4.5"
	}
	if sameModel {
		return "gpt-5.2"
	}
	return "gpt-5.4"
}

func rcFallbackModelObservation(t *testing.T, log *service.UsageLog, sent, observed string) {
	t.Helper()
	require.NotNil(t, log.UpstreamModel)
	require.Equal(t, sent, *log.UpstreamModel)
	require.NotNil(t, log.UpstreamResponseModel)
	require.Equal(t, observed, *log.UpstreamResponseModel)
	require.NotNil(t, log.UpstreamModelMismatch)
	require.Equal(t, sent != observed, *log.UpstreamModelMismatch)
}

func newRCFallbackFixture(t *testing.T, platform string, sameModel bool) (*rcRouteFixture, *rcFallbackAccounts, *service.UsageRecordWorkerPool) {
	t.Helper()
	pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 8, TaskTimeout: 5 * time.Second})
	t.Cleanup(pool.Stop)
	var accounts *rcFallbackAccounts
	f := newRCRouteFixtureWithRepositories(t, platform, pool, func(f *rcRouteFixture) rcRouteRepositories {
		target := "gpt-5.4"
		if platform == service.PlatformAnthropic {
			target = "claude-opus-4.5"
		}
		if sameModel {
			target = f.model
		}
		primary := &f.accounts.account
		primary.Credentials["api_key"] = "sk-primary"
		primary.Priority = 0
		backup := *primary
		backup.ID = primary.ID + 1
		backup.Name = "rc-backup"
		backup.Priority = 100
		backup.Credentials = map[string]any{"api_key": "sk-backup", "base_url": primary.Credentials["base_url"], "model_mapping": map[string]any{f.model: target}}
		backup.Extra = map[string]any{"openai_responses_supported": false, "quota_limit": 100.0}
		accounts = &rcFallbackAccounts{rcRouteAccounts: f.accounts, backup: backup}
		f.upstream.errorStatusByKey = map[string]int{"sk-primary": http.StatusServiceUnavailable}
		f.upstream.responseContentByKey = map[string]string{"sk-primary": "primary A answer", "sk-backup": "backup answer"}
		return rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: rcRoutePolicy{}, access: f.access}
	})
	// Stop before downstream cache/service cleanup; the earlier registration also
	// covers setup failures. Stop is idempotent and drains pending usage work.
	t.Cleanup(pool.Stop)
	return f, accounts, pool
}
func rcFallbackDrain(t *testing.T, pool *service.UsageRecordWorkerPool) {
	t.Helper()
	done := make(chan struct{})
	require.False(t, pool.Submit(func(context.Context) { close(done) }).Dropped())
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("fallback usage worker failed to drain")
	}
}
func rcFallbackAnswer(t *testing.T, body []byte) string {
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
func rcFallbackAttempts(t *testing.T, f *rcRouteFixture, wantModels, wantKeys []string) {
	t.Helper()
	f.upstream.mu.Lock()
	defer f.upstream.mu.Unlock()
	require.Len(t, f.upstream.bodies, len(wantModels))
	require.Len(t, wantKeys, len(wantModels))
	for i, body := range f.upstream.bodies {
		var request struct {
			Model        string          `json:"model"`
			GatewayCache json.RawMessage `json:"gateway_cache"`
		}
		require.NoError(t, json.Unmarshal(body, &request))
		require.Equal(t, wantModels[i], request.Model, "actual HTTP attempt %d", i)
		require.Empty(t, request.GatewayCache, "gateway cache declaration must not reach either upstream attempt")
		if f.accounts.account.Platform == service.PlatformOpenAI {
			var native map[string]any
			require.NoError(t, json.Unmarshal(body, &native))
			require.Equal(t, "keep-native-key", native["prompt_cache_key"])
			require.Equal(t, map[string]any{"type": "ephemeral"}, native["cache_control"])
		}
		key := strings.TrimPrefix(f.upstream.headers[i].Get("Authorization"), "Bearer ")
		if key == "" {
			key = f.upstream.headers[i].Get("X-Api-Key")
		}
		require.Equal(t, wantKeys[i], key, "actual HTTP credential %d", i)
		for name := range f.upstream.headers[i] {
			require.False(t, strings.HasPrefix(strings.ToLower(name), "x-gateway-cache-"))
		}
	}
}
func rcFallbackUsage(t *testing.T, f *rcRouteFixture, accountID int64, model string, n int, status string, wantRead, wantWrite int) {
	t.Helper()
	commands, logs, quota := f.ledger.snapshot()
	access := f.access.snapshot()
	require.Len(t, commands, n, "failed HTTP attempts must not generate bills")
	require.Len(t, logs, n, "one usage row per successful generation")
	require.Len(t, access, n)
	var expectedQuota float64
	ids := map[string]bool{}
	for i := range commands {
		require.NotEmpty(t, commands[i].RequestID)
		require.LessOrEqual(t, len(commands[i].RequestID), 64)
		require.False(t, ids[commands[i].RequestID], "successful requests need unique billing identities")
		ids[commands[i].RequestID] = true
		require.True(t, strings.HasPrefix(commands[i].RequestID, "client:"), "identity must not be a failed/successful upstream HTTP id")
		require.Equal(t, accountID, commands[i].AccountID)
		require.Equal(t, accountID, logs[i].AccountID)
		require.NotNil(t, logs[i].UpstreamModel)
		require.Equal(t, model, *logs[i].UpstreamModel)
		require.Equal(t, f.model, logs[i].RequestedModel)
		require.Equal(t, commands[i].RequestID, logs[i].RequestID)
		require.Equal(t, logs[i].RequestID, access[i].RequestID)
		require.Equal(t, logs[i].RequestID, access[i].SourceRequestID)
		require.True(t, access[i].UpstreamCalled)
		require.Equal(t, wantRead, commands[i].CacheReadTokens)
		require.Equal(t, wantWrite, commands[i].CacheCreationTokens)
		require.Equal(t, wantRead, logs[i].CacheReadTokens)
		require.Equal(t, wantWrite, logs[i].CacheCreationTokens)
		require.Positive(t, commands[i].BalanceCost)
		require.Positive(t, commands[i].AccountQuotaCost)
		require.Equal(t, status, access[i].Status)
		expectedQuota += commands[i].AccountQuotaCost
	}
	require.InDelta(t, expectedQuota, quota, 1e-12)
}

func TestResultCacheGatewayRouteCrossModelFallback(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, tc := range []struct {
				name                     string
				sameModel, claimOriginal bool
			}{
				{name: "different_model"}, {name: "different_model_claims_original", claimOriginal: true}, {name: "same_model_conservative", sameModel: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, mode := range []string{"read_write", "write_only", "read_only"} {
						t.Run(mode, func(t *testing.T) {
							f, accounts, pool := newRCFallbackFixture(t, platform, tc.sameModel)
							target := rcFallbackExpectedModel(platform, tc.sameModel)
							if tc.sameModel {
								require.Equal(t, f.model, target)
							} else {
								require.NotEqual(t, f.model, target)
							}
							if tc.claimOriginal {
								f.upstream.responseModel = f.model
							}
							status := "UNCACHEABLE"
							if mode == "read_only" {
								status = "MISS"
							}
							models, keys := []string{}, []string{}
							for i := 1; i <= 2; i++ {
								response := f.request(t, mode)
								requireRCRouteOK(t, response, status)
								require.Equal(t, "backup answer", rcFallbackAnswer(t, response.Body.Bytes()))
								rcFallbackDrain(t, pool)
								models = append(models, f.model, target)
								keys = append(keys, "sk-primary", "sk-backup")
								rcFallbackAttempts(t, f, models, keys)
								rcFallbackUsage(t, f, accounts.backup.ID, target, i, status, 0, 0)
								_, successLogs, _ := f.ledger.snapshot()
								observed := target
								if tc.claimOriginal {
									observed = f.model
								}
								rcFallbackModelObservation(t, successLogs[i-1], target, observed)
								records := f.access.snapshot()
								require.Equal(t, records[i-1].RequestID, response.Header().Get("X-Gateway-Request-ID"))
							}
							// A successful recovery may populate A normally; prior rejected B fills
							// must not poison the key or permanently disable later eligible fills.
							f.upstream.mu.Lock()
							delete(f.upstream.errorStatusByKey, "sk-primary")
							f.upstream.responseModel = ""
							f.upstream.mu.Unlock()
							recovered := f.request(t, "read_write")
							requireRCRouteOK(t, recovered, "MISS")
							rcFallbackDrain(t, pool)
							require.Equal(t, "primary A answer", rcFallbackAnswer(t, recovered.Body.Bytes()))
							models = append(models, f.model)
							keys = append(keys, "sk-primary")
							rcFallbackAttempts(t, f, models, keys)
							commands, logs, quota := f.ledger.snapshot()
							require.Len(t, commands, 3)
							require.Len(t, logs, 3)
							require.Equal(t, f.accounts.account.ID, commands[2].AccountID)
							require.Equal(t, f.accounts.account.ID, logs[2].AccountID)
							rcFallbackModelObservation(t, logs[2], f.model, f.model)
							require.Equal(t, commands[2].RequestID, logs[2].RequestID)
							require.Equal(t, logs[2].RequestID, recovered.Header().Get("X-Gateway-Request-ID"))
							recoveredAccess := f.access.snapshot()
							require.Len(t, recoveredAccess, 3)
							require.Equal(t, logs[2].RequestID, recoveredAccess[2].RequestID)
							require.Equal(t, logs[2].RequestID, recoveredAccess[2].SourceRequestID)
							require.NotEqual(t, logs[0].RequestID, logs[2].RequestID)
							require.NotEqual(t, logs[1].RequestID, logs[2].RequestID)
							hit := f.request(t, "read_write")
							requireRCRouteOK(t, hit, "HIT")
							rcFallbackDrain(t, pool)
							require.Equal(t, "primary A answer", rcFallbackAnswer(t, hit.Body.Bytes()))
							afterCommands, afterLogs, afterQuota := f.ledger.snapshot()
							require.Len(t, afterCommands, 3)
							require.Len(t, afterLogs, 3)
							require.Equal(t, quota, afterQuota)
							rcFallbackAttempts(t, f, models, keys)
							access := f.access.snapshot()
							require.Len(t, access, 4)
							require.Equal(t, logs[2].RequestID, access[3].SourceRequestID)
							t.Logf("FALLBACK platform=%s case=%s mode=%s attempts=5 generations=3 rejected_fallback=2 recovered_A_hit=true", platform, tc.name, mode)
						})
					}
				})
			}
		})
	}
}

func TestResultCacheGatewayRouteFallbackDoesNotOverwriteWarmModel(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			f, accounts, pool := newRCFallbackFixture(t, platform, false)
			f.upstream.mu.Lock()
			delete(f.upstream.errorStatusByKey, "sk-primary")
			f.upstream.mu.Unlock()
			warm := f.request(t, "read_write")
			requireRCRouteOK(t, warm, "MISS")
			rcFallbackDrain(t, pool)
			warmCommands, warmLogs, warmQuota := f.ledger.snapshot()
			require.Len(t, warmCommands, 1)
			require.Len(t, warmLogs, 1)
			require.Positive(t, warmCommands[0].BalanceCost)
			require.Positive(t, warmQuota)
			hit := f.request(t, "read_write")
			requireRCRouteOK(t, hit, "HIT")
			rcFallbackDrain(t, pool)
			require.Equal(t, "primary A answer", rcFallbackAnswer(t, hit.Body.Bytes()))
			hitCommands, hitLogs, hitQuota := f.ledger.snapshot()
			require.Equal(t, warmCommands, hitCommands)
			require.Equal(t, warmLogs, hitLogs)
			require.Equal(t, warmQuota, hitQuota)
			f.upstream.mu.Lock()
			f.upstream.errorStatusByKey["sk-primary"] = http.StatusServiceUnavailable
			f.upstream.mu.Unlock()
			fallback := f.request(t, "write_only")
			requireRCRouteOK(t, fallback, "UNCACHEABLE")
			rcFallbackDrain(t, pool)
			require.Equal(t, "backup answer", rcFallbackAnswer(t, fallback.Body.Bytes()))
			fallbackCommands, fallbackLogs, fallbackQuota := f.ledger.snapshot()
			require.Len(t, fallbackCommands, 2)
			require.Len(t, fallbackLogs, 2)
			require.Positive(t, fallbackCommands[1].BalanceCost)
			require.InDelta(t, warmQuota+fallbackCommands[1].AccountQuotaCost, fallbackQuota, 1e-12)
			retained := f.request(t, "read_write")
			requireRCRouteOK(t, retained, "HIT")
			rcFallbackDrain(t, pool)
			require.Equal(t, "primary A answer", rcFallbackAnswer(t, retained.Body.Bytes()), "B must not replace an existing A response")
			rcFallbackAttempts(t, f, []string{f.model, f.model, rcFallbackExpectedModel(platform, false)}, []string{"sk-primary", "sk-primary", "sk-backup"})
			commands, logs, quota := f.ledger.snapshot()
			require.Equal(t, fallbackCommands, commands)
			require.Equal(t, fallbackLogs, logs)
			require.Equal(t, fallbackQuota, quota)
			require.Len(t, commands, 2)
			require.Len(t, logs, 2)
			require.Equal(t, f.accounts.account.ID, commands[0].AccountID)
			require.Equal(t, accounts.backup.ID, commands[1].AccountID)
			require.Equal(t, f.accounts.account.ID, logs[0].AccountID)
			require.Equal(t, accounts.backup.ID, logs[1].AccountID)
			require.Equal(t, commands[0].RequestID, logs[0].RequestID)
			require.Equal(t, commands[1].RequestID, logs[1].RequestID)
			require.NotEmpty(t, commands[0].RequestID)
			require.NotEmpty(t, commands[1].RequestID)
			require.NotEqual(t, commands[0].RequestID, commands[1].RequestID)
			rcFallbackModelObservation(t, logs[0], f.model, f.model)
			rcFallbackModelObservation(t, logs[1], rcFallbackExpectedModel(platform, false), rcFallbackExpectedModel(platform, false))
			require.Equal(t, logs[0].RequestID, warm.Header().Get("X-Gateway-Request-ID"))
			require.Equal(t, logs[1].RequestID, fallback.Header().Get("X-Gateway-Request-ID"))
			access := f.access.snapshot()
			require.Len(t, access, 4)
			require.Equal(t, logs[0].RequestID, access[0].RequestID)
			require.Equal(t, logs[0].RequestID, access[0].SourceRequestID)
			require.Equal(t, logs[0].RequestID, access[1].SourceRequestID)
			require.Equal(t, logs[0].RequestID, access[3].SourceRequestID)
			require.Equal(t, logs[1].RequestID, access[2].RequestID)
			require.Equal(t, logs[1].RequestID, access[2].SourceRequestID)
			require.NotEqual(t, access[1].RequestID, access[3].RequestID)
			t.Logf("WARM_FALLBACK platform=%s attempts=3 generations=2 original_A_source_retained=true B_not_published=true", platform)
		})
	}
}

// Compare the exact successful-generation billing inputs and amounts with a
// bypass of the same real primary-503/backup-success path. Cache opt-in must not
// alter native prompt-cache token accounting or bill the failed HTTP attempt.
func TestResultCacheGatewayRouteFallbackBillingMatchesBypass(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			var baselineCommand *service.UsageBillingCommand
			var baselineLog *service.UsageLog
			var baselineQuota float64
			for _, mode := range []string{"bypass", "read_write", "write_only", "read_only"} {
				t.Run(mode, func(t *testing.T) {
					f, accounts, pool := newRCFallbackFixture(t, platform, false)
					target := rcFallbackExpectedModel(platform, false)
					require.NotEqual(t, f.model, target)
					f.upstream.mu.Lock()
					f.upstream.cacheReadTokens, f.upstream.cacheCreationTokens = 13, 7
					f.upstream.mu.Unlock()
					response := f.request(t, mode)
					status := "UNCACHEABLE"
					if mode == "bypass" {
						status = "BYPASS"
					}
					if mode == "read_only" {
						status = "MISS"
					}
					requireRCRouteOK(t, response, status)
					rcFallbackDrain(t, pool)
					rcFallbackAttempts(t, f, []string{f.model, target}, []string{"sk-primary", "sk-backup"})
					rcFallbackUsage(t, f, accounts.backup.ID, target, 1, status, 13, 7)
					commands, logs, quota := f.ledger.snapshot()
					rcFallbackModelObservation(t, logs[0], target, target)
					require.Equal(t, logs[0].RequestID, response.Header().Get("X-Gateway-Request-ID"))
					if mode == "bypass" {
						baselineCommand, baselineLog, baselineQuota = commands[0], logs[0], quota
						return
					}
					require.NotNil(t, baselineCommand)
					// Only correlation IDs/fingerprints may vary; every billing field
					// in the command must otherwise match the normal bypass.
					actual, expected := *commands[0], *baselineCommand
					actual.RequestID, expected.RequestID = "", ""
					actual.RequestFingerprint, expected.RequestFingerprint = "", ""
					actual.RequestPayloadHash, expected.RequestPayloadHash = "", ""
					require.Equal(t, expected, actual)
					require.Equal(t, baselineQuota, quota)
					require.Equal(t, baselineLog.TotalCost, logs[0].TotalCost)
					require.Equal(t, baselineLog.ActualCost, logs[0].ActualCost)
					require.Equal(t, baselineLog.CacheReadTokens, logs[0].CacheReadTokens)
					require.Equal(t, baselineLog.CacheCreationTokens, logs[0].CacheCreationTokens)
					t.Logf("FALLBACK_BILLING platform=%s mode=%s successful_bills=1 failed_attempt_bills=0 bypass_amounts_equal=true native_read=13 native_create=7", platform, mode)
				})
			}
		})
	}
}

var _ service.AccountRepository = (*rcFallbackAccounts)(nil)
