//go:build integration

package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
)

func piAuditRealInstallation(t *testing.T, cfg *config.Config) *service.PluginInstallation {
	t.Helper()
	root := t.TempDir()
	cfg.Plugins.DataDir = filepath.Join(root, "private-audit-data")
	cfg.Plugins.StartTimeoutSeconds = 10
	installed := filepath.Join(cfg.Plugins.DataDir, "private-audit-install")
	binary := filepath.Join(installed, "bin", "plugin.exe")
	require.NoError(t, os.MkdirAll(filepath.Dir(binary), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(cfg.Plugins.DataDir, "runtime"), 0700))
	out, err := exec.Command("go", "build", "-o", binary, "../service/testdata/general-plugin").CombinedOutput()
	require.NoError(t, err, string(out))
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	manifest := service.PluginManifest{SchemaVersion: 2, ID: "com.example.general", Name: "audit fixture", Version: "0.1.0", Requires: service.PluginRequirements{Sub2API: ">=0.1.0", PluginProtocol: 1, GeneralAPI: 1}, Capabilities: []service.PluginCapability{{ID: service.PluginCapabilityRequestHook}, {ID: service.PluginCapabilityIndependentTask}}, Files: map[string]string{"bin/plugin.exe": hash}, General: &service.PluginGeneralManifest{ConfigVersion: 1, Hooks: []service.PluginHookEntry{{Name: "inspect", Stages: []contract.Stage{contract.Input, contract.Output}, Protocols: []string{"example.text.v1"}, CanReplace: true}}, Tasks: []service.PluginTaskEntry{{Name: "sync_prices"}}}}
	manifest.Runtimes = map[string]service.PluginRuntime{manifest.RuntimeKey(): {Path: "bin/plugin.exe"}}
	require.NoError(t, manifest.Validate())
	p, err := NewPluginRepository(integrationDB).Install(context.Background(), &service.PluginInstallation{PluginKey: manifest.ID, Name: manifest.Name, Version: manifest.Version, Manifest: manifest, BinarySHA256: hash, BinaryPath: binary, InstallPath: installed, ArtifactPath: filepath.Join(root, "private-artifact-path-canary"), SignatureStatus: service.PluginSignatureUnsigned, State: service.PluginStateIncompatible}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM sub2api_plugin_instances WHERE plugin_id=$1`, p.ID)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`DELETE FROM sub2api_plugin_installations WHERE id=$1`, p.ID)
		require.NoError(t, err)
	})
	return p
}

// Unlike the earlier HTTP fixtures, this exercises the actual audit queue and
// PostgreSQL audit repository. Auth/step-up remain fixtures, not real credentials.
// Publish and rollback start genuine GeneralPlugin gRPC child processes.
func TestPluginInstanceAuditHTTPPersistenceIntegration(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKey = strings.Repeat("b", 64)
	cipher, err := NewAESEncryptor(cfg)
	require.NoError(t, err)
	p := piAuditRealInstallation(t, cfg)
	repo := NewPluginInstanceRepository(integrationDB)
	manager := service.NewPluginManager(NewPluginRepository(integrationDB), cipher, cfg, service.PluginHostInfo{Version: "0.1.179"}, nil)
	instances := service.NewPluginInstanceService(repo, cipher, manager, time.Second)
	defer instances.Close()
	auditRepo := NewAuditLogRepository(integrationDB)
	audit := service.NewAuditLogService(auditRepo, nil)
	audit.Start()
	defer audit.Stop()
	checker := &piDeleteIntegrationChecker{permission: "none"}
	stepAllowed := false
	email := fmt.Sprintf("audit-%d@example.com", time.Now().UnixNano())
	prefix := fmt.Sprintf("pi-audit-%d", time.Now().UnixNano())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Set(middleware.ContextKeyAuthEmail, email)
		c.Next()
	})
	// All exact operation codes are granted by a fixture switch after denial.
	authz := piAuditIntegrationPermission{checker: checker}
	step := middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		if !stepAllowed {
			c.AbortWithStatus(428)
			return
		}
		c.Next()
	})
	h := &handler.Handlers{Admin: &handler.AdminHandlers{PluginInstance: adminhandler.NewPluginInstanceHandler(instances)}}
	routes.RegisterAdminRoutes(router.Group("/api/v1", nil), h, auth, middleware.NewAuditLogMiddleware(audit), step, nil, nil, authz)
	type expectedEntry struct {
		requestID, action string
		status            int
		fields            map[string]any
	}
	expected := []expectedEntry{}
	request := func(method, path, body, action string, status int, fields map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		id := prefix + "-" + strconv.Itoa(len(expected))
		req := httptest.NewRequest(method, "/api/v1/admin"+path+"?note=query-audit-canary", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.RequestID, id))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, w.Body.String())
		expected = append(expected, expectedEntry{id, action, status, fields})
		return w
	}
	request("DELETE", "/plugin-instances/999", `{"expected_generation":1,"secret":"plaintext-audit-canary"}`, middleware.AuditActionPluginInstanceDelete, 403, map[string]any{"instance_id": int64(999)})
	checker.permission = "all"
	createBody := fmt.Sprintf(`{"plugin_id":%d,"name":"name-audit-canary","config":{"marker":"plaintext-audit-canary"}}`, p.ID)
	request("POST", "/plugin-instances", createBody, middleware.AuditActionPluginInstanceCreate, 428, nil)
	decode := func(w *httptest.ResponseRecorder) service.PluginInstance {
		t.Helper()
		var envelope struct {
			Data struct {
				Instance service.PluginInstance `json:"instance"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		return envelope.Data.Instance
	}
	stepAllowed = true
	created := decode(request("POST", "/plugin-instances", createBody, middleware.AuditActionPluginInstanceCreate, 201, map[string]any{"plugin_id": p.ID, "new_generation": int64(1), "revision": int64(1)}))
	id := created.ID
	path := "/plugin-instances/" + strconv.FormatInt(id, 10)
	snap, err := repo.Snapshot(ctx, id, 0)
	require.NoError(t, err)
	ciphertexts := []string{snap.Revision.ConfigEncrypted}
	draft := decode(request("PUT", path+"/config", `{"expected_generation":1,"config":{"marker":"plaintext-audit-canary-beta"}}`, middleware.AuditActionPluginInstanceConfig, 200, map[string]any{"instance_id": id, "plugin_id": p.ID, "expected_generation": int64(1), "new_generation": int64(2), "revision": int64(2)}))
	snap, err = repo.Snapshot(ctx, id, 2)
	require.NoError(t, err)
	ciphertexts = append(ciphertexts, snap.Revision.ConfigEncrypted)
	published := decode(request("POST", path+"/publish", fmt.Sprintf(`{"expected_generation":%d,"revision":2}`, draft.Generation), middleware.AuditActionPluginInstancePublish, 200, map[string]any{"plugin_id": p.ID, "expected_generation": int64(2), "new_generation": int64(3), "revision": int64(2)}))
	old, err := instances.Acquire(ctx, id)
	require.NoError(t, err)
	defer old.Release()
	rolled := decode(request("POST", path+"/publish", fmt.Sprintf(`{"expected_generation":%d,"revision":1}`, published.Generation), middleware.AuditActionPluginInstancePublish, 200, map[string]any{"plugin_id": p.ID, "expected_generation": int64(3), "new_generation": int64(4), "revision": int64(1)}))
	disabled := decode(request("POST", path+"/disable", fmt.Sprintf(`{"expected_generation":%d}`, rolled.Generation), middleware.AuditActionPluginInstanceDisable, 200, map[string]any{"plugin_id": p.ID, "expected_generation": int64(4), "new_generation": int64(5), "revision": int64(1)}))
	request("DELETE", path, fmt.Sprintf(`{"expected_generation":%d}`, disabled.Generation), middleware.AuditActionPluginInstanceDelete, 409, map[string]any{"instance_id": id, "expected_generation": int64(5), "error_code": "PLUGIN_INSTANCE_BUSY", "conflict": false})
	old.Release()
	group := piIntegrationGroup(t)
	bindingBody := fmt.Sprintf(`{"expected_generation":5,"bindings":[{"instance_id":%d,"group_id":%d,"stage":"input","entry":"inspect","protocol":"example.text.v1","sort_order":0,"enabled":true,"can_replace":true}]}`, id, group)
	bound := decode(request("PUT", path+"/bindings", bindingBody, middleware.AuditActionPluginInstanceBindings, 200, map[string]any{"plugin_id": p.ID, "expected_generation": int64(5), "new_generation": int64(6), "revision": int64(1)}))
	request("PUT", path+"/config", `{"expected_generation":5,"config":{"marker":"plaintext-audit-canary"}}`, middleware.AuditActionPluginInstanceConfig, 409, map[string]any{"instance_id": id, "expected_generation": int64(5), "error_code": "PLUGIN_INSTANCE_CONFLICT", "conflict": true})
	// A read must not turn into an additional management write audit.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin"+path+"/status", nil))
	require.Equal(t, 200, w.Code)
	request("DELETE", path, fmt.Sprintf(`{"expected_generation":%d}`, bound.Generation), middleware.AuditActionPluginInstanceDelete, 200, map[string]any{"instance_id": id, "expected_generation": int64(6), "deleted": true})
	request("DELETE", path, `{"expected_generation":6}`, middleware.AuditActionPluginInstanceDelete, 404, map[string]any{"instance_id": id, "error_code": "PLUGIN_INSTANCE_NOT_FOUND"})
	request("DELETE", "/plugin-instances/raw-audit-id-canary", `{"expected_generation":1}`, middleware.AuditActionPluginInstanceDelete, 400, map[string]any{"error_code": "PLUGIN_INSTANCE_ID_INVALID"})
	instances.Close()
	request("POST", "/plugin-instances", createBody, middleware.AuditActionPluginInstanceCreate, 503, map[string]any{"plugin_id": p.ID, "error_code": "PLUGIN_INSTANCE_UNAVAILABLE"})
	audit.Stop() // Drains the real async queue through PostgreSQL BatchInsert/COPY.
	entries, err := auditRepo.List(ctx, &service.AuditLogFilter{ActorEmail: email, Page: 1, PageSize: 100})
	require.NoError(t, err)
	require.Equal(t, len(expected), entries.Total)
	require.Len(t, entries.Logs, len(expected))
	require.Len(t, expected, 14)
	actual := map[string]*service.AuditLog{}
	for _, listed := range entries.Logs {
		// List intentionally omits body; verify the actual persisted detail.
		require.Empty(t, listed.RequestBody)
		entry, err := auditRepo.GetByID(ctx, listed.ID)
		require.NoError(t, err)
		actual[entry.RequestID] = entry
	}
	for _, want := range expected {
		entry := actual[want.requestID]
		require.NotNil(t, entry)
		require.Equal(t, want.action, entry.Action)
		require.Equal(t, want.status, entry.StatusCode)
		require.EqualValues(t, 41, *entry.ActorUserID)
		require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
		require.EqualValues(t, want.status, entry.Extra["http_status"])
		for key, value := range want.fields {
			switch value.(type) {
			case int64:
				require.EqualValues(t, value, entry.Extra[key], key)
			default:
				require.Equal(t, value, entry.Extra[key], key)
			}
		}
		if want.status >= 400 {
			require.Equal(t, "failed", entry.Extra["result"])
			require.NotContains(t, entry.Extra, "new_generation")
			require.NotContains(t, entry.Extra, "deleted")
			if want.action != middleware.AuditActionPluginInstanceCreate {
				require.NotContains(t, entry.Extra, "plugin_id")
			}
		} else {
			require.Equal(t, "success", entry.Extra["result"])
			require.Equal(t, "OK", entry.Extra["error_code"])
		}
		if want.action == middleware.AuditActionPluginInstanceDelete {
			require.NotContains(t, entry.Extra, "new_generation")
			require.NotContains(t, entry.Extra, "plugin_id")
			require.NotContains(t, entry.Extra, "revision")
		}
		require.NotContains(t, entry.Extra, "observed_generation")
		require.NotContains(t, entry.Extra, "params")
		require.NotContains(t, entry.Extra, "query")
		encoded, err := json.Marshal(entry)
		require.NoError(t, err)
		for _, secret := range append(ciphertexts, "plaintext-audit-canary", "name-audit-canary", "query-audit-canary", "raw-audit-id-canary", p.InstallPath, p.ArtifactPath) {
			require.NotContains(t, string(encoded), secret)
		}
	}
	require.NoError(t, NewPluginRepository(integrationDB).Delete(ctx, p.ID, p.BinarySHA256))
	t.Log("AUDIT_PG_HTTP persisted_writes=14 real_candidate_publish=1 real_candidate_rollback=1 draining_delete=409 conflict=409 body_query_raw_params=omitted config_plaintext_ciphertext_private_paths=absent read_audits=0")
}

type piAuditIntegrationPermission struct{ checker *piDeleteIntegrationChecker }

func (s piAuditIntegrationPermission) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	return s.checker.permission == "all" && strings.HasPrefix(code, "plugin_instances."), nil
}
