package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// All gateway stages are production implementations. Only repository boundaries,
// the audit engine, and the counted HTTP upstream are controlled fixtures.
// Standard mode intentionally exercises admission and the real RecordUsage path.
type rcRouteKeys struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *rcRouteKeys) GetByKeyForAuth(_ context.Context, token string) (*service.APIKey, error) {
	if token != r.key.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	k := *r.key
	u := *k.User
	g := *k.Group
	k.User = &u
	k.Group = &g
	return &k, nil
}
func (*rcRouteKeys) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

type rcRouteUsers struct {
	service.UserRepository
	user *service.User
}

func (r *rcRouteUsers) GetByID(context.Context, int64) (*service.User, error) {
	u := *r.user
	return &u, nil
}

type rcRouteAccounts struct {
	service.AccountRepository
	account service.Account
}

func (r *rcRouteAccounts) ListSchedulableByPlatform(_ context.Context, p string) ([]service.Account, error) {
	if p != r.account.Platform {
		return nil, nil
	}
	return []service.Account{r.account}, nil
}
func (r *rcRouteAccounts) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, p string) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, p)
}
func (r *rcRouteAccounts) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if id != r.account.ID {
		return nil, fmt.Errorf("unexpected account %d", id)
	}
	a := r.account
	return &a, nil
}

func (r *rcRouteAccounts) ListSchedulableByGroupIDAndPlatforms(_ context.Context, _ int64, platforms []string) ([]service.Account, error) {
	for _, p := range platforms {
		if p == r.account.Platform {
			return []service.Account{r.account}, nil
		}
	}
	return nil, nil
}

type rcRouteSubscriptions struct {
	service.UserSubscriptionRepository
	sub *service.UserSubscription
}

func (r *rcRouteSubscriptions) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*service.UserSubscription, error) {
	if r.sub == nil {
		return nil, service.ErrSubscriptionNotFound
	}
	s := *r.sub
	return &s, nil
}

type rcRouteLedger struct {
	service.UsageBillingRepository
	service.UsageLogRepository
	mu       sync.Mutex
	commands []*service.UsageBillingCommand
	logs     []*service.UsageLog
	quota    float64
}

func (r *rcRouteLedger) Apply(_ context.Context, c *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	if len(c.RequestID) > 64 {
		return nil, fmt.Errorf("billing request_id exceeds schema64")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *c
	r.commands = append(r.commands, &cp)
	r.quota += c.AccountQuotaCost
	return &service.UsageBillingApplyResult{Applied: true}, nil
}
func (r *rcRouteLedger) Create(_ context.Context, l *service.UsageLog) (bool, error) {
	if len(l.RequestID) > 64 {
		return false, fmt.Errorf("usage request_id exceeds schema64")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *l
	r.logs = append(r.logs, &cp)
	return true, nil
}
func (r *rcRouteLedger) snapshot() ([]*service.UsageBillingCommand, []*service.UsageLog, float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*service.UsageBillingCommand(nil), r.commands...), append([]*service.UsageLog(nil), r.logs...), r.quota
}

type rcRouteAccess struct {
	mu      sync.Mutex
	records []service.ResultCacheAccess
}

func (r *rcRouteAccess) Append(_ context.Context, a service.ResultCacheAccess) error {
	if len(a.RequestID) > 64 || len(a.SourceRequestID) > 64 {
		return fmt.Errorf("cache access/source exceeds schema64")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, a)
	return nil
}
func (*rcRouteAccess) Cleanup(context.Context, time.Time) error { return nil }
func (r *rcRouteAccess) snapshot() []service.ResultCacheAccess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]service.ResultCacheAccess(nil), r.records...)
}

type rcRoutePolicy struct{}

func (rcRoutePolicy) Current(context.Context, int64) (service.ResultCachePolicy, error) {
	return service.ResultCachePolicy{Active: true, Version: "route-policy-v1", Config: service.GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 60, MaxResponseBytes: 2 << 20, Revision: 1}}, nil
}

type rcRouteAudit struct {
	mu      sync.Mutex
	blocked bool
	calls   int
	onCheck func()
}

func (r *rcRouteAudit) Check(context.Context, securityaudit.Request) (*securityaudit.LegacyDecision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.onCheck != nil {
		r.onCheck()
	}
	return &securityaudit.LegacyDecision{Allowed: !r.blocked, Blocked: r.blocked, StatusCode: 403, ErrorCode: "content_policy_violation", Message: "fixture audit block"}, nil
}
func (r *rcRouteAudit) block() { r.mu.Lock(); defer r.mu.Unlock(); r.blocked = true }

type rcRouteHTTP struct {
	service.HTTPUpstream
	client *http.Client
}

func (u *rcRouteHTTP) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(r)
}

func (u *rcRouteHTTP) DoWithTLS(r *http.Request, p string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, concurrency)
}

type rcRouteUpstream struct {
	mu                   sync.Mutex
	bodies               [][]byte
	headers              []http.Header
	responseModel        string
	responseContent      string
	errorStatusByKey     map[string]int
	responseContentByKey map[string]string
	cacheReadTokens      int
	cacheCreationTokens  int
	conflictModel        string
	omitModel            bool
	overrideModel        bool
	modelValue           any
}

func (u *rcRouteUpstream) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	u.mu.Lock()
	u.bodies = append(u.bodies, body)
	u.headers = append(u.headers, r.Header.Clone())
	n := len(u.bodies)
	model := u.responseModel
	conflict := u.conflictModel
	omit, override, value := u.omitModel, u.overrideModel, u.modelValue
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.Header.Get("X-Api-Key")
	}
	errorStatus := u.errorStatusByKey[token]
	cacheRead, cacheCreation := u.cacheReadTokens, u.cacheCreationTokens
	content := u.responseContent
	if value, ok := u.responseContentByKey[token]; ok {
		content = value
	}
	if content == "" {
		content = "fixture answer"
	}
	u.mu.Unlock()
	if errorStatus >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", fmt.Sprintf("upstream-error-%d", n))
		w.WriteHeader(errorStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "overloaded_error", "message": "fixture primary unavailable"}})
		return
	}
	if model == "" {
		var request struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &request)
		model = request.Model
	}
	if !override {
		value = model
	}
	if r.URL.Path == "/v1/messages" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-ID", fmt.Sprintf("upstream-%d", n))
		writeStart := func(id string, model any, omit bool) {
			message := map[string]any{"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{}, "usage": map[string]any{"input_tokens": 100}}
			if cacheRead != 0 || cacheCreation != 0 {
				message["usage"] = map[string]any{"input_tokens": 100, "cache_read_input_tokens": cacheRead, "cache_creation_input_tokens": cacheCreation}
			}
			if omit {
				delete(message, "model")
			}
			payload, _ := json.Marshal(map[string]any{"type": "message_start", "message": message})
			fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", payload)
		}
		if conflict != "" {
			writeStart("conflict", conflict, false)
		}
		writeStart(fmt.Sprintf("msg_%d", n), value, omit)
		fmt.Fprintf(w, `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}

`, content)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", fmt.Sprintf("upstream-%d", n))
	response := map[string]any{"id": fmt.Sprintf("chatcmpl-upstream-%d", n), "model": model, "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}}
	if cacheRead != 0 || cacheCreation != 0 {
		response["usage"] = map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120, "prompt_tokens_details": map[string]any{"cached_tokens": cacheRead, "cache_write_tokens": cacheCreation}}
	}
	if omit {
		delete(response, "model")
	} else {
		response["model"] = value
	}
	_ = json.NewEncoder(w).Encode(response)
}
func (u *rcRouteUpstream) count() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.bodies) }

type rcRouteFixture struct {
	parent        context.Context
	afterRequest  func(*gin.Context)
	router        *gin.Engine
	model         string
	subscriptions *rcRouteSubscriptions
	key           *service.APIKey
	users         *rcRouteUsers
	accounts      *rcRouteAccounts
	ledger        *rcRouteLedger
	access        *rcRouteAccess
	audit         *rcRouteAudit
	upstream      *rcRouteUpstream
}

func newRCRouteFixture(t *testing.T, platforms ...string) *rcRouteFixture {
	t.Helper()
	platform := service.PlatformOpenAI
	if len(platforms) > 0 {
		platform = platforms[0]
	}
	return newRCRouteFixtureWithPool(t, platform, nil)
}

// rcRouteRepositories replaces only storage boundaries for the PostgreSQL lane.
// Nil setup preserves the existing observable in-memory repository fixtures.
type rcRouteRepositories struct {
	pluginGateway          *handler.PluginGatewayHandler
	keys                   service.APIKeyRepository
	users                  service.UserRepository
	accounts               service.AccountRepository
	subscriptions          service.UserSubscriptionRepository
	billing                service.UsageBillingRepository
	usage                  service.UsageLogRepository
	channelService         *service.ChannelService
	channelPricingResolver bool
	compositeResolver      *service.CompositeRouteResolver
	policy                 service.ResultCachePolicyRepository
	access                 service.ResultCacheAccessRepository
}

func newRCRouteFixtureWithPool(t *testing.T, platform string, pool *service.UsageRecordWorkerPool) *rcRouteFixture {
	return newRCRouteFixtureWithRepositories(t, platform, pool, nil)
}

func newRCRouteFixtureWithRepositories(t *testing.T, platform string, pool *service.UsageRecordWorkerPool, setup func(*rcRouteFixture) rcRouteRepositories) *rcRouteFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	model := "gpt-5.2"
	if platform == service.PlatformAnthropic {
		model = "claude-sonnet-4.5"
	}
	f := &rcRouteFixture{model: model, subscriptions: &rcRouteSubscriptions{}, ledger: &rcRouteLedger{}, access: &rcRouteAccess{}, audit: &rcRouteAudit{}, upstream: &rcRouteUpstream{}}
	server := httptest.NewServer(http.HandlerFunc(f.upstream.serve))
	t.Cleanup(server.Close)
	group := &service.Group{ID: 42, Hydrated: true, Status: service.StatusActive, Platform: platform, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	user := &service.User{ID: 7, Status: service.StatusActive, Role: service.RoleUser, Balance: 100}
	f.key = &service.APIKey{ID: 100, UserID: user.ID, Key: "rc-route-test-key", Status: service.StatusActive, User: user, Group: group, GroupID: &group.ID}
	f.users = &rcRouteUsers{user: user}
	f.accounts = &rcRouteAccounts{account: service.Account{ID: 99, Name: "rc-upstream", Platform: platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "sk-fixture", "base_url": server.URL}, Extra: map[string]any{"openai_responses_supported": false, "quota_limit": 100.0}}}
	repos := rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: f.accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: rcRoutePolicy{}, access: f.access}
	if setup != nil {
		repos = setup(f)
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxBodySize = 1 << 20
	cfg.Gateway.TextMaxBodySize = 1 << 20
	rate := &keyBillingRouteRateRepo{}
	keys := service.NewAPIKeyService(repos.keys, repos.users, nil, nil, rate, nil, cfg)
	billingCache := service.NewBillingCacheService(nil, repos.users, repos.subscriptions, nil, nil, rate, cfg, nil)
	t.Cleanup(billingCache.Stop)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	var concurrencyCache service.ConcurrencyCache
	if setup != nil {
		concurrencyCache = repository.NewConcurrencyCache(rdb, 5, 30)
	}
	concurrency := service.NewConcurrencyService(concurrencyCache)
	billing := service.NewBillingService(cfg, nil)
	var pricingResolver *service.ModelPricingResolver
	if repos.channelPricingResolver {
		pricingResolver = service.NewModelPricingResolver(repos.channelService, billing)
	}
	deferred := service.NewDeferredService(repos.accounts, nil, time.Hour)
	gateway := service.NewOpenAIGatewayService(repos.accounts, repos.usage, repos.billing, repos.users, nil, rate, nil, cfg, nil, concurrency, billing, nil, billingCache, &rcRouteHTTP{client: server.Client()}, deferred, nil, nil, pricingResolver, repos.channelService, nil, nil, nil)
	cache := service.NewResultCacheService(repository.NewResultCacheStore(rdb), repos.policy, repos.access, cfg)
	subscriptions := service.NewSubscriptionService(nil, repos.subscriptions, billingCache, nil, cfg)
	t.Cleanup(subscriptions.Stop)
	t.Cleanup(cache.Stop)
	h := handler.ProvideOpenAIGatewayHandler(gateway, nil, concurrency, billingCache, keys, pool, nil, nil, nil, nil, cfg, cache, securityaudit.NewCoordinator(f.audit, nil), nil)
	generic := service.NewGatewayService(repos.accounts, nil, repos.usage, repos.billing, repos.users, repos.subscriptions, rate, nil, cfg, nil, concurrency, billing, nil, billingCache, nil, &rcRouteHTTP{client: server.Client()}, deferred, nil, nil, nil, nil, nil, nil, repos.channelService, pricingResolver, nil, nil, nil)
	genericHandler := handler.ProvideGatewayHandler(generic, gateway, nil, nil, nil, concurrency, billingCache, nil, keys, pool, nil, nil, nil, cfg, nil, cache, securityaudit.NewCoordinator(f.audit, nil), nil)
	f.router = gin.New()
	f.router.Use(func(c *gin.Context) {
		c.Next()
		if f.afterRequest != nil {
			f.afterRequest(c)
		}
	})
	RegisterGatewayRoutes(f.router, &handler.Handlers{Gateway: genericHandler, OpenAIGateway: h, PluginGateway: repos.pluginGateway}, middleware.NewAPIKeyAuthMiddleware(keys, subscriptions, cfg), keys, subscriptions, nil, nil, repos.compositeResolver, cfg)
	return f
}

const rcRouteRequest = `{"model":"gpt-5.2","messages":[{"role":"user","content":"fixture question"}],"stream":false,"prompt_cache_key":"keep-native-key","cache_control":{"type":"ephemeral"},"gateway_cache":{"mode":"%s","scope":"user"}}`

func (f *rcRouteFixture) request(t *testing.T, mode string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.ReplaceAll(fmt.Sprintf(rcRouteRequest, mode), "gpt-5.2", f.model)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.key.Key)
	req.Header.Set("X-Gateway-Cache-Protocol", "result-cache")
	req.Header.Set("X-Gateway-Cache-Mode", mode)
	req.Header.Set("X-Gateway-Cache-Scope", "user")
	req.Header.Set("X-Gateway-Cache-Unknown", "strip-me")
	req.Header.Set("X-Client-Request-ID", "untrusted-repeat")
	req.Header.Set("X-Gateway-Request-ID", "untrusted-repeat")
	if f.parent != nil {
		req = req.WithContext(f.parent)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}
func requireRCRouteOK(t *testing.T, r *httptest.ResponseRecorder, status string) {
	t.Helper()
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	require.Equal(t, status, r.Header().Get("X-Gateway-Cache-Status"), r.Body.String())
}

func TestResultCacheGatewayRouteBillingCorrelation(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			f := newRCRouteFixture(t, platform)
			miss := f.request(t, "read_write")
			requireRCRouteOK(t, miss, "MISS")
			cmds, logs, quota := f.ledger.snapshot()
			access := f.access.snapshot()
			require.Len(t, cmds, 1)
			require.Len(t, logs, 1)
			require.Len(t, access, 1)
			require.Positive(t, cmds[0].BalanceCost)
			require.Positive(t, quota, "command=%+v account=%+v", cmds[0], f.accounts.account)
			require.Zero(t, logs[0].CacheReadTokens)
			require.Equal(t, cmds[0].RequestID, logs[0].RequestID)
			require.Equal(t, logs[0].RequestID, access[0].RequestID, "cache access must join the actual billed request")
			require.Equal(t, logs[0].RequestID, miss.Header().Get("X-Gateway-Request-ID"))
			require.Equal(t, "client:"+miss.Header().Get("X-Client-Request-ID"), logs[0].RequestID)
			require.Equal(t, 1, f.upstream.count())
			hit := f.request(t, "read_write")
			requireRCRouteOK(t, hit, "HIT")
			hitCommands, hitLogs, hitQuota := f.ledger.snapshot()
			require.Len(t, hitCommands, 1)
			require.Len(t, hitLogs, 1)
			require.Equal(t, quota, hitQuota)
			require.Equal(t, 1, f.upstream.count())
			access = f.access.snapshot()
			require.Len(t, access, 2)
			require.False(t, access[1].UpstreamCalled)
			require.NotEqual(t, access[0].RequestID, access[1].RequestID)
			require.Equal(t, access[1].RequestID, hit.Header().Get("X-Gateway-Request-ID"))
			require.Equal(t, logs[0].RequestID, access[1].SourceRequestID, "source must reference the original generation ledger, not the upstream id")
			var a, b map[string]any
			require.NoError(t, json.Unmarshal(miss.Body.Bytes(), &a))
			require.NoError(t, json.Unmarshal(hit.Body.Bytes(), &b))
			require.NotEqual(t, a["id"], b["id"])
			require.Equal(t, a["choices"], b["choices"])
			require.Equal(t, a["usage"], b["usage"])
			updated := f.request(t, "write_only")
			requireRCRouteOK(t, updated, "UPDATED")
			cmds, logs, updatedQuota := f.ledger.snapshot()
			access = f.access.snapshot()
			require.Len(t, cmds, 2)
			require.Len(t, logs, 2)
			require.Len(t, access, 3)
			require.Greater(t, updatedQuota, quota)
			require.Equal(t, 2, f.upstream.count())
			require.Equal(t, logs[1].RequestID, access[2].RequestID)
			require.Equal(t, logs[1].RequestID, cmds[1].RequestID)
			require.Equal(t, logs[1].RequestID, updated.Header().Get("X-Gateway-Request-ID"))
			require.Zero(t, logs[1].CacheReadTokens)

		})
	}
}

func TestResultCacheGatewayRouteWarmHitAdmission(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			cases := []struct {
				name   string
				change func(*rcRouteFixture)
				status int
				code   string
			}{
				{"disabled key", func(f *rcRouteFixture) { f.key.Status = service.StatusDisabled }, 401, "API_KEY_DISABLED"},
				{"expired key", func(f *rcRouteFixture) { v := time.Now().Add(-time.Hour); f.key.ExpiresAt = &v }, 403, "API_KEY_EXPIRED"},
				{"forbidden model", func(f *rcRouteFixture) {
					f.key.Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"other-model"}}
				}, 404, "model_not_found"},
				{"auth balance", func(f *rcRouteFixture) { f.key.User.Balance = 0 }, 403, "INSUFFICIENT_BALANCE"},
				{"handler balance", func(f *rcRouteFixture) { u := *f.users.user; u.Balance = 0; f.users.user = &u }, 403, "insufficient balance"},
				{"audit block", func(f *rcRouteFixture) { f.audit.block() }, 403, "content_policy_violation"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					f := newRCRouteFixture(t, platform)
					requireRCRouteOK(t, f.request(t, "read_write"), "MISS")
					requireRCRouteOK(t, f.request(t, "read_write"), "HIT")
					_, _, before := f.ledger.snapshot()
					tc.change(f)
					r := f.request(t, "read_write")
					require.Equal(t, tc.status, r.Code, r.Body.String())
					require.Contains(t, r.Body.String(), tc.code)
					require.NotEqual(t, "HIT", r.Header().Get("X-Gateway-Cache-Status"))
					require.Equal(t, 1, f.upstream.count())
					cmds, logs, after := f.ledger.snapshot()
					require.Len(t, cmds, 1)
					require.Len(t, logs, 1)
					require.Equal(t, before, after)
					require.Len(t, f.access.snapshot(), 2)
				})
			}

		})
	}
}

func TestResultCacheGatewayRouteUpstreamControls(t *testing.T) {
	f := newRCRouteFixture(t)
	requireRCRouteOK(t, f.request(t, "read_write"), "MISS")
	f.upstream.mu.Lock()
	defer f.upstream.mu.Unlock()
	require.Len(t, f.upstream.bodies, 1)
	var sent map[string]any
	require.NoError(t, json.Unmarshal(f.upstream.bodies[0], &sent))
	require.NotContains(t, sent, "gateway_cache")
	require.Equal(t, "keep-native-key", sent["prompt_cache_key"])
	require.Equal(t, map[string]any{"type": "ephemeral"}, sent["cache_control"])
	for name := range f.upstream.headers[0] {
		require.False(t, strings.HasPrefix(strings.ToLower(name), "x-gateway-cache-"), name)
	}
}
func TestResultCacheGatewayRouteModelMismatch(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, name := range []string{"account mapping", "response model"} {
				t.Run(name, func(t *testing.T) {
					f := newRCRouteFixture(t, platform)
					other := "gpt-5.4"
					if platform == service.PlatformAnthropic {
						other = "claude-opus-4.5"
					}
					if name == "account mapping" {
						f.accounts.account.Credentials["model_mapping"] = map[string]any{f.model: other}
					} else {
						f.upstream.responseModel = other
					}
					for i := 0; i < 2; i++ {
						requireRCRouteOK(t, f.request(t, "read_write"), "UNCACHEABLE")
					}
					require.Equal(t, 2, f.upstream.count())
					cmds, logs, quota := f.ledger.snapshot()
					require.Len(t, cmds, 2)
					require.Len(t, logs, 2)
					require.Positive(t, quota)
				})
			}
		})
	}
}
func TestResultCacheGatewayRouteSubscriptionAdmission(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, kind := range []string{"missing", "expired", "daily exhausted"} {
				t.Run(kind, func(t *testing.T) {
					f := newRCRouteFixture(t, platform)
					requireRCRouteOK(t, f.request(t, "read_write"), "MISS")
					requireRCRouteOK(t, f.request(t, "read_write"), "HIT")
					f.key.Group.SubscriptionType = service.SubscriptionTypeSubscription
					status, code := 403, "SUBSCRIPTION_NOT_FOUND"
					if kind != "missing" {
						now := time.Now()
						f.subscriptions.sub = &service.UserSubscription{ID: 1, UserID: f.key.UserID, GroupID: f.key.Group.ID, Status: service.SubscriptionStatusActive, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(48 * time.Hour), DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now}
						code = "SUBSCRIPTION_INVALID"
						if kind == "expired" {
							f.subscriptions.sub.ExpiresAt = now.Add(-time.Hour)
						} else {
							limit := 1.0
							f.key.Group.DailyLimitUSD = &limit
							f.subscriptions.sub.DailyUsageUSD = limit + 0.01
							status = 429
							code = "USAGE_LIMIT_EXCEEDED"
						}
					}
					r := f.request(t, "read_write")
					require.Equal(t, status, r.Code, r.Body.String())
					require.Contains(t, r.Body.String(), code)
					require.NotEqual(t, "HIT", r.Header().Get("X-Gateway-Cache-Status"))
					require.Equal(t, 1, f.upstream.count())
					cmds, logs, _ := f.ledger.snapshot()
					require.Len(t, cmds, 1)
					require.Len(t, logs, 1)
					require.Len(t, f.access.snapshot(), 2)
				})
			}
		})
	}
}

func TestResultCacheGatewayRouteResponseModelConflict(t *testing.T) {
	f := newRCRouteFixture(t, service.PlatformAnthropic)
	f.upstream.conflictModel = "claude-opus-4.5"
	for i := 0; i < 2; i++ {
		requireRCRouteOK(t, f.request(t, "read_write"), "UNCACHEABLE")
	}
	require.Equal(t, 2, f.upstream.count())
	cmds, logs, _ := f.ledger.snapshot()
	require.Len(t, cmds, 2)
	require.Len(t, logs, 2)
}

func TestResultCacheGatewayRouteUnknownResponseModel(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, kind := range []string{"missing", "null", "empty", "whitespace"} {
				t.Run(kind, func(t *testing.T) {
					f := newRCRouteFixture(t, platform)
					f.upstream.overrideModel = true
					switch kind {
					case "missing":
						f.upstream.omitModel = true
					case "empty":
						f.upstream.modelValue = ""
					case "whitespace":
						f.upstream.modelValue = "   "
					}
					for i := 0; i < 2; i++ {
						requireRCRouteOK(t, f.request(t, "read_write"), "UNCACHEABLE")
					}
					require.Equal(t, 2, f.upstream.count())
					cmds, logs, _ := f.ledger.snapshot()
					require.Len(t, cmds, 2)
					require.Len(t, logs, 2)
				})
			}
		})
	}
}
func TestResultCacheGatewayRouteAsyncUsageSnapshot(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 8, TaskTimeout: 10 * time.Second, OverflowPolicy: config.UsageRecordOverflowPolicySync})
			t.Cleanup(pool.Stop)
			block := make(chan struct{})
			started := make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(block) }) }
			t.Cleanup(release)
			require.Equal(t, service.UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) { close(started); <-block }))
			<-started
			f := newRCRouteFixtureWithPool(t, platform, pool)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			f.parent = ctx
			f.afterRequest = func(c *gin.Context) {
				c.Request = c.Request.WithContext(context.WithValue(context.Background(), ctxkey.RequestedPublicModel, "model-from-reused-context"))
			}
			miss := f.request(t, "read_write")
			requireRCRouteOK(t, miss, "MISS")
			hit := f.request(t, "read_write")
			requireRCRouteOK(t, hit, "HIT")
			updated := f.request(t, "write_only")
			requireRCRouteOK(t, updated, "UPDATED")
			cmds, logs, _ := f.ledger.snapshot()
			require.Empty(t, cmds)
			require.Empty(t, logs)
			cancel()
			release()
			require.Eventually(t, func() bool { _, ls, _ := f.ledger.snapshot(); return len(ls) == 2 }, 5*time.Second, 10*time.Millisecond)
			cmds, logs, quota := f.ledger.snapshot()
			require.Len(t, cmds, 2)
			require.Len(t, logs, 2)
			require.Positive(t, quota)
			access := f.access.snapshot()
			require.Len(t, access, 3)
			for i, j := range []int{0, 2} {
				require.Equal(t, access[j].RequestID, logs[i].RequestID)
				require.Equal(t, cmds[i].RequestID, logs[i].RequestID)
				require.Equal(t, f.model, logs[i].RequestedModel)
				require.Zero(t, logs[i].CacheReadTokens)
			}
			require.Equal(t, logs[0].RequestID, access[1].SourceRequestID)
			require.Equal(t, uint64(3), pool.Stats().SubmittedTasks)
			require.Zero(t, pool.Stats().DroppedTasks)
		})
	}
}

func TestResultCacheGatewayRouteLongCorrelationSource(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			f := newRCRouteFixture(t, platform)
			f.parent = context.WithValue(context.Background(), ctxkey.ClientRequestID, strings.Repeat("x", 64))
			miss := f.request(t, "read_write")
			requireRCRouteOK(t, miss, "MISS")
			f.parent = context.WithValue(context.Background(), ctxkey.ClientRequestID, strings.Repeat("y", 64))
			hit := f.request(t, "read_write")
			requireRCRouteOK(t, hit, "HIT")
			cmds, logs, _ := f.ledger.snapshot()
			access := f.access.snapshot()
			require.Len(t, cmds, 1)
			require.Len(t, logs, 1)
			require.Len(t, access, 2)
			require.LessOrEqual(t, len(access[0].RequestID), 64)
			require.Equal(t, logs[0].RequestID, access[0].RequestID)
			require.Equal(t, logs[0].RequestID, access[1].SourceRequestID)
			require.NotEqual(t, access[0].RequestID, access[1].RequestID)
			require.Equal(t, access[0].RequestID, miss.Header().Get("X-Gateway-Request-ID"))
			require.Equal(t, access[1].RequestID, hit.Header().Get("X-Gateway-Request-ID"))
		})
	}
}
func TestResultCacheGatewayRouteCustomModelIdentity(t *testing.T) {
	f := newRCRouteFixture(t)
	f.model = "custom-fixture-model-without-pricing"
	requireRCRouteOK(t, f.request(t, "read_write"), "MISS")
	requireRCRouteOK(t, f.request(t, "read_write"), "HIT")
	require.Equal(t, 1, f.upstream.count())
}
