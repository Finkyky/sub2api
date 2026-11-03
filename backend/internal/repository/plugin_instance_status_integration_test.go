//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type piStatusIntegrationCipher struct{}

func (piStatusIntegrationCipher) Encrypt(string) (string, error) {
	panic("status encrypted configuration")
}
func (piStatusIntegrationCipher) Decrypt(string) (string, error) {
	panic("status decrypted configuration")
}

type piStatusIntegrationChecker struct {
	allow bool
	seen  []string
}

func (s *piStatusIntegrationChecker) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	s.seen = append(s.seen, code)
	return s.allow && code == "plugin_instances.read", nil
}

// This is real PostgreSQL + production routing/middleware/handler/service. Auth
// and audit are fixtures; no browser, real JWT login or candidate process is
// claimed. A nil plugin manager and panic cipher make accidental startup fail.
func TestPluginInstanceStatusHTTPIntegration(t *testing.T) {
	ctx := context.Background()
	plugin := piIntegrationInstallation(t)
	repo := NewPluginInstanceRepository(integrationDB)
	original, err := repo.Create(ctx, plugin.ID, "status instance", "ciphertext-status-secret")
	require.NoError(t, err)
	id := original.Instance.ID
	instances := service.NewPluginInstanceService(repo, piStatusIntegrationCipher{}, nil, time.Second)
	defer instances.Close()
	checker := &piStatusIntegrationChecker{}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepCalls := 0
	step := middleware.StepUpAuthMiddleware(func(c *gin.Context) { stepCalls++; c.AbortWithStatus(418) })
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{PluginInstance: adminhandler.NewPluginInstanceHandler(instances)}}
	routes.RegisterAdminRoutes(router.Group("/api/v1", nil), handlers, auth, audit, step, nil, nil, checker)
	path := "/api/v1/admin/plugin-instances/" + strconv.FormatInt(id, 10) + "/status"
	get := func(url string, code int) *httptest.ResponseRecorder {
		t.Helper()
		checker.seen = nil
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		require.Equal(t, code, w.Code, w.Body.String())
		require.Equal(t, []string{"plugin_instances.read"}, checker.seen)
		for _, secret := range []string{"ciphertext-status-secret", "config_encrypted", "install_path", "artifact_path", "artifact_data"} {
			require.NotContains(t, w.Body.String(), secret)
		}
		return w
	}
	get(path, http.StatusForbidden)
	checker.allow = true
	assertStatus := func(enabled bool, revision int64) {
		t.Helper()
		before, err := repo.Snapshot(ctx, id, 0)
		require.NoError(t, err)
		w := get(path, http.StatusOK)
		var envelope struct {
			Data service.PluginInstanceStatus `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		status := envelope.Data
		require.Equal(t, id, status.InstanceID)
		require.Equal(t, enabled, status.Enabled)
		require.Equal(t, revision, status.CurrentRevision)
		require.False(t, status.RuntimePresent)
		require.True(t, status.Closed)
		require.Zero(t, status.LeaseRefs)
		require.Empty(t, status.LastError)
		require.Empty(t, status.Runtimes)
		after, err := repo.Snapshot(ctx, id, 0)
		require.NoError(t, err)
		require.Equal(t, before, after, "GET must not mutate persistent publication/config/bindings")
	}
	assertStatus(false, 1)
	draft, err := repo.SaveRevision(ctx, id, original.Instance.Generation, "ciphertext-status-revision-2")
	require.NoError(t, err)
	// Emulate a publication made on another node: this node must only observe.
	published, err := repo.Publish(ctx, id, draft.Instance.Generation, 2, true)
	require.NoError(t, err)
	assertStatus(true, 2)
	_, err = repo.Publish(ctx, id, published.Instance.Generation, 2, false)
	require.NoError(t, err)
	assertStatus(false, 2)
	get("/api/v1/admin/plugin-instances/0/status", http.StatusBadRequest)
	get("/api/v1/admin/plugin-instances/9223372036854775807/status", http.StatusNotFound)
	instances.Close()
	get(path, http.StatusServiceUnavailable)
	require.Zero(t, stepCalls)
	t.Log("STATUS_HTTP_PG enabled/revision=authoritative node_runtime=absent writes=0 decrypted=0 step_up=0 exact_read_permission=true")
}
