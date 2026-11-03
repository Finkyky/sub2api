//go:build integration

package repository

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	examples "github.com/Wei-Shaw/sub2api/examples/general-plugins"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type platformLifecycleChecker struct{ denySensitive bool }

func (p *platformLifecycleChecker) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	return !(p.denySensitive && code == "plugin_runs.sensitive"), nil
}
func (p *platformLifecycleChecker) Snapshot(context.Context, int64) (service.RBACSnapshot, error) {
	return service.RBACSnapshot{IsSuperAdmin: true}, nil
}

type platformLifecycleUsers struct {
	service.UserRepository
	id int64
}

func (p platformLifecycleUsers) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: p.id, TotpEnabled: true}, nil
}
func (p platformLifecycleUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

// Builds exactly the package that upload validates; CI need not have Python or a prebuilt artifact.
func platformExamplePackage(t *testing.T, name string) []byte {
	t.Helper()
	if dir := os.Getenv("SUB2API_PLUGIN_EXAMPLE_PACKAGES"); dir != "" {
		b, e := os.ReadFile(filepath.Join(dir, name+"-1.0.0.s2plugin"))
		require.NoError(t, e)
		return b
	}
	path := filepath.Join(t.TempDir(), "plugin")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	out, e := exec.Command("go", "build", "-ldflags", "-X main.plugin="+name, "-o", path, "../../examples/general-plugins/cmd/general-plugin").CombinedOutput()
	require.NoError(t, e, string(out))
	binary, e := os.ReadFile(path)
	require.NoError(t, e)
	raw, e := examples.Source(name, "manifest.source.json")
	require.NoError(t, e)
	var m service.PluginManifest
	require.NoError(t, json.Unmarshal(raw, &m))
	h := sha256.Sum256(binary)
	entry := "bin/plugin"
	if runtime.GOOS == "windows" {
		entry += ".exe"
	}
	m.Runtimes = map[string]service.PluginRuntime{m.RuntimeKey(): {Path: entry}}
	m.Files = map[string]string{entry: hex.EncodeToString(h[:])}
	require.NoError(t, m.Validate())
	raw, e = json.Marshal(m)
	require.NoError(t, e)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for key, data := range map[string][]byte{"manifest.json": raw, entry: binary} {
		w, e := z.Create(key)
		require.NoError(t, e)
		_, e = w.Write(data)
		require.NoError(t, e)
	}
	require.NoError(t, z.Close())
	return buf.Bytes()
}
func TestPluginPlatformRealExamplesHTTPTaskPricingAndRecoveryIntegration(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Plugins.DataDir = filepath.Join(t.TempDir(), "plugins")
	cfg.Plugins.AllowUnsigned = true
	cfg.Plugins.StartTimeoutSeconds = 10
	cfg.Plugins.MaxUploadBytes = 100 << 20
	cfg.Plugins.MaxUncompressedBytes = 200 << 20
	cfg.Totp.EncryptionKey = strings.Repeat("c", 64)
	cipher, e := NewAESEncryptor(cfg)
	require.NoError(t, e)
	email := fmt.Sprintf("m5-m7-%d@example.test", time.Now().UnixNano())
	var userID int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','admin','active',0,1) RETURNING id`, email).Scan(&userID))
	var channelID, pricingID int64
	groupID := piIntegrationGroup(t)
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO channels(name)VALUES($1)RETURNING id`, email).Scan(&channelID))
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO channel_model_pricing(channel_id,models,platform,input_price,output_price)VALUES($1,'["model-a"]','openai',1,2)RETURNING id`, channelID).Scan(&pricingID))
	_, e = integrationDB.Exec(`INSERT INTO channel_groups(channel_id,group_id)VALUES($1,$2)`, channelID, groupID)
	require.NoError(t, e)
	channels := service.NewChannelService(NewChannelRepository(integrationDB), nil, nil, nil, nil)
	before := channels.GetChannelModelPricing(ctx, groupID, "model-a")
	require.NotNil(t, before)
	require.Equal(t, 1.0, *before.InputPrice)
	manager := service.NewPluginManager(NewPluginRepository(integrationDB), cipher, cfg, service.PluginHostInfo{Version: "0.1.179"}, nil)
	defer manager.Stop()
	instanceRepo := NewPluginInstanceRepository(integrationDB)
	instances := service.NewPluginInstanceService(instanceRepo, cipher, manager, time.Second)
	defer func() { instances.Close() }()
	broker := service.NewPluginTaskCapabilityBroker(integrationDB, channels)
	taskRepo := NewPluginTaskRepository(integrationDB)
	tasks := service.NewPluginTaskService(taskRepo, instances, broker, cipher)
	defer func() { tasks.Close() }()
	auditRepo := NewAuditLogRepository(integrationDB)
	audit := service.NewAuditLogService(auditRepo, nil)
	audit.Start()
	defer audit.Stop()
	checker := &platformLifecycleChecker{}
	grants := &piHTTPIntegrationGrants{allowed: true}
	users := platformLifecycleUsers{id: userID}
	step := middleware.NewStepUpAuthMiddleware(service.NewTotpService(users, nil, grants, nil, nil, nil), service.NewUserService(users, nil, nil, nil), nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Set(middleware.ContextKeyAuthEmail, email)
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(middleware.ContextKeySessionID, "platform-session")
		c.Next()
	})
	h := &handler.Handlers{Admin: &handler.AdminHandlers{Plugin: adminhandler.NewPluginHandler(manager), PluginInstance: adminhandler.NewPluginInstanceHandler(instances), PluginTask: adminhandler.NewPluginTaskHandler(tasks)}}
	routes.RegisterAdminRoutes(router.Group("/api/v1", nil), h, auth, middleware.NewAuditLogMiddleware(audit), step, nil, nil, checker)
	sequence := 0
	request := func(method, path string, body []byte, ctype string, want int) *httptest.ResponseRecorder {
		t.Helper()
		sequence++
		req := httptest.NewRequest(method, "/api/v1/admin"+path+"?note=PRIVATE-QUERY", bytes.NewReader(body))
		req.Header.Set("Content-Type", ctype)
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.RequestID, email+"-"+strconv.Itoa(sequence)))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, want, w.Code, w.Body.String())
		return w
	}
	jsonRequest := func(method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		data, e := json.Marshal(body)
		require.NoError(t, e)
		return request(method, path, data, "application/json", want)
	}
	snapshots := map[string]service.PluginInstance{}
	for _, name := range examples.Names {
		var b bytes.Buffer
		form := multipart.NewWriter(&b)
		w, e := form.CreateFormFile("plugin", name+".s2plugin")
		require.NoError(t, e)
		_, e = w.Write(platformExamplePackage(t, name))
		require.NoError(t, e)
		require.NoError(t, form.Close())
		res := request("POST", "/plugins/upload", b.Bytes(), form.FormDataContentType(), 201)
		var installed struct {
			Data service.PluginInstallation `json:"data"`
		}
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &installed))
		require.True(t, installed.Data.Compatibility.Compatible)
		installationID := installed.Data.ID
		t.Cleanup(func() {
			// Delete only this test's installation after child runtimes stop.
			_, err := integrationDB.Exec(`DELETE FROM sub2api_plugin_instances WHERE plugin_id=$1`, installationID)
			require.NoError(t, err)
			_, err = integrationDB.Exec(`DELETE FROM sub2api_plugin_installations WHERE id=$1`, installationID)
			require.NoError(t, err)
		})
		raw, e := examples.Source(name, "config.example.json")
		require.NoError(t, e)
		var conf map[string]any
		require.NoError(t, json.Unmarshal(raw, &conf))
		if name == "pricing-sync" {
			conf["rules"] = []map[string]any{{"channel_id": channelID, "pricing_id": pricingID, "source_key": "model-a", "field": "input_price", "multiplier": 1, "max_price": 10, "max_change_ratio": 0.5}}
		}
		res = jsonRequest("POST", "/plugin-instances", map[string]any{"plugin_id": installed.Data.ID, "name": name, "config": conf}, 201)
		var detail struct {
			Data struct {
				Instance service.PluginInstance `json:"instance"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &detail))
		instance := detail.Data.Instance
		path := "/plugin-instances/" + strconv.FormatInt(instance.ID, 10)
		res = jsonRequest("POST", path+"/publish", map[string]any{"expected_generation": instance.Generation, "revision": 1}, 200)
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &detail))
		instance = detail.Data.Instance
		if name != "pricing-sync" {
			stage := contract.Input
			if name == "business-output" {
				stage = contract.Output
			}
			res = jsonRequest("PUT", path+"/bindings", map[string]any{"expected_generation": instance.Generation, "bindings": []service.PluginGroupBinding{{InstanceID: instance.ID, GroupID: groupID, Entry: name, Stage: stage, Protocol: service.PluginProtocolOpenAIChat, Enabled: true}}}, 200)
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &detail))
			instance = detail.Data.Instance
		}
		snapshots[name] = instance
	}
	// Production middleware, PostgreSQL snapshots, and actual child processes. The terminal upstream is counted.
	gateway := handler.NewPluginGatewayHandler(instances, nil, cfg)
	gatewayRouter := gin.New()
	gatewayRouter.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, Group: &service.Group{ID: groupID}})
		c.Next()
	})
	upstream := 0
	responseText := "approved answer"
	gatewayRouter.POST("/v1/chat/completions", gateway.Middleware, func(c *gin.Context) {
		upstream++
		c.JSON(200, gin.H{"id": "r", "model": "model-a", "choices": []any{gin.H{"message": gin.H{"role": "assistant", "content": responseText}, "finish_reason": "stop"}}, "usage": gin.H{"prompt_tokens": 2, "completion_tokens": 3}})
	})
	ask := func(text string, want int) {
		t.Helper()
		body, _ := json.Marshal(gin.H{"model": "model-a", "messages": []any{gin.H{"role": "user", "content": text}}})
		w := httptest.NewRecorder()
		gatewayRouter.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body)))
		require.Equal(t, want, w.Code, w.Body.String())
		if want != 200 {
			require.NotContains(t, w.Body.String(), text)
			require.NotContains(t, w.Body.String(), responseText)
		}
	}
	ask("sk-abcdefghijklmnopqrstuv", 403)
	require.Zero(t, upstream)
	responseText = "guaranteed profit"
	ask("ordinary question", 403)
	require.Equal(t, 1, upstream)
	responseText = "approved answer"
	ask("ordinary question", 200)
	require.Equal(t, 2, upstream)
	taskBody := map[string]any{"instance_id": snapshots["pricing-sync"].ID, "name": "price task", "entry": "pricing-sync", "cron": "@every 1m", "enabled": true, "timeout_seconds": 20, "max_retries": 0, "retry_backoff_seconds": 1, "input": map[string]any{"prices": map[string]any{"model-a": map[string]any{"input_price": 1.2}}, "note": "PRIVATE-INPUT"}, "scope": service.PluginTaskScope{ChannelIDs: []int64{channelID}, PricingIDs: []int64{pricingID}, PricingFields: []string{"input_price"}, NetworkURLs: []string{}, KVKeys: []string{}}}
	res := jsonRequest("POST", "/plugin-tasks", taskBody, 200)
	var taskResponse struct {
		Data struct {
			Task service.PluginTask `json:"task"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &taskResponse))
	task := taskResponse.Data.Task
	taskPath := "/plugin-tasks/" + strconv.FormatInt(task.ID, 10)
	res = jsonRequest("POST", taskPath+"/run", map[string]any{"idempotency_key": "manual-price"}, 200)
	var runResponse struct {
		Data struct {
			Run service.PluginTaskRun `json:"run"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &runResponse))
	run := runResponse.Data.Run
	require.Equal(t, "succeeded", run.Status)
	require.NotContains(t, res.Body.String(), "price_updates")
	after := channels.GetChannelModelPricing(ctx, groupID, "model-a")
	require.Equal(t, 1.2, *after.InputPrice)
	require.Equal(t, 1.0, *before.InputPrice, "in-flight snapshot changed")
	res = jsonRequest("POST", taskPath+"/run", map[string]any{"idempotency_key": "manual-price"}, 200)
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &runResponse))
	require.Equal(t, run.ID, runResponse.Data.Run.ID)
	var versions int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM sub2api_plugin_pricing_versions WHERE run_id=$1`, run.ID).Scan(&versions))
	require.Equal(t, 1, versions)
	stored, e := taskRepo.Run(ctx, run.ID)
	require.NoError(t, e)
	require.NotContains(t, stored.ResultEncrypted, "price_updates")
	require.NotEmpty(t, stored.ResultEncrypted)
	res = request("GET", taskPath+"/runs", nil, "application/json", 200)
	require.NotContains(t, res.Body.String(), "price_updates")
	checker.denySensitive = true
	request("GET", "/plugin-runs/"+strconv.FormatInt(run.ID, 10)+"/sensitive", nil, "application/json", 403)
	checker.denySensitive = false
	grants.allowed = false
	request("GET", "/plugin-runs/"+strconv.FormatInt(run.ID, 10)+"/sensitive", nil, "application/json", 403)
	grants.allowed = true
	res = request("GET", "/plugin-runs/"+strconv.FormatInt(run.ID, 10)+"/sensitive", nil, "application/json", 200)
	require.Contains(t, res.Body.String(), "price_updates")
	taskBody["input"] = map[string]any{"prices": map[string]any{"model-a": map[string]any{"input_price": 200}}}
	taskBody["expected_generation"] = task.Generation
	res = jsonRequest("PUT", taskPath, taskBody, 200)
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &taskResponse))
	task = taskResponse.Data.Task
	res = jsonRequest("POST", taskPath+"/run", map[string]any{"idempotency_key": "failed-price"}, 200)
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &runResponse))
	require.Equal(t, "failed", runResponse.Data.Run.Status)
	require.Equal(t, 1.2, *channels.GetChannelModelPricing(ctx, groupID, "model-a").InputPrice)
	taskBody["input"] = map[string]any{"prices": map[string]any{"model-a": map[string]any{"input_price": 1.3}}}
	taskBody["expected_generation"] = task.Generation
	res = jsonRequest("PUT", taskPath, taskBody, 200)
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &taskResponse))
	task = taskResponse.Data.Task
	_, e = integrationDB.Exec(`UPDATE sub2api_plugin_tasks SET next_run_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, task.ID)
	require.NoError(t, e)
	scheduler := service.NewPluginTaskScheduler(tasks)
	scheduler.Start()
	require.Eventually(t, func() bool { return *channels.GetChannelModelPricing(ctx, groupID, "model-a").InputPrice == 1.3 }, 15*time.Second, 100*time.Millisecond)
	scheduler.Stop()
	tasks.Close()
	instances.Close()
	instances = service.NewPluginInstanceService(instanceRepo, cipher, manager, time.Second)
	tasks = service.NewPluginTaskService(taskRepo, instances, service.NewPluginTaskCapabilityBroker(integrationDB, channels), cipher)
	recovered, e := tasks.Execute(ctx, task.ID, "after-restart")
	require.NoError(t, e)
	require.Equal(t, "succeeded", recovered.Status)
	audit.Stop()
	entries, e := auditRepo.List(ctx, &service.AuditLogFilter{ActorEmail: email, Page: 1, PageSize: 100})
	require.NoError(t, e)
	seen := map[string]bool{}
	for _, entry := range entries.Logs {
		if strings.HasPrefix(entry.Action, "admin.plugin_tasks.") || entry.Action == "admin.plugin_runs.sensitive" {
			seen[entry.Action] = true
			detail, e := auditRepo.GetByID(ctx, entry.ID)
			require.NoError(t, e)
			raw, e := json.Marshal(detail)
			require.NoError(t, e)
			require.NotContains(t, string(raw), "PRIVATE-INPUT")
			require.NotContains(t, string(raw), "PRIVATE-QUERY")
			require.Equal(t, "<credential-bearing body omitted>", detail.RequestBody)
		}
	}
	for _, action := range []string{"admin.plugin_tasks.create", "admin.plugin_tasks.update", "admin.plugin_tasks.run", "admin.plugin_runs.sensitive"} {
		require.True(t, seen[action], action)
	}
	t.Log("PLATFORM upload=3 configure_publish=3 bind=2 input_block_upstream=0 output_block_no_leak=true manual_and_scheduled_prices=1.2/1.3 failed_price_retained=true replay_same_run=true in_flight_snapshot=1 restart_recovery=true encrypted_result=true sensitive_step_up=true audit_persisted_redacted=true")
}
