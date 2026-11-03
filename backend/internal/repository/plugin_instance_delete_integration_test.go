//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func piDeleteAssertCounts(t *testing.T, id int64, instances, revisions, bindings int) {
	t.Helper()
	for _, tc := range []struct {
		table, field string
		want         int
	}{
		{"sub2api_plugin_instances", "id", instances},
		{"sub2api_plugin_instance_revisions", "instance_id", revisions},
		{"sub2api_plugin_group_bindings", "instance_id", bindings},
	} {
		var n int
		err := integrationDB.QueryRow("SELECT count(*) FROM "+tc.table+" WHERE "+tc.field+"=$1", id).Scan(&n)
		require.NoError(t, err)
		require.Equal(t, tc.want, n, tc.table)
	}
}
func TestPluginInstanceDeleteCascadeAndUnfreezeIntegration(t *testing.T) {
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	repo := NewPluginInstanceRepository(integrationDB)
	packages := NewPluginRepository(integrationDB)
	s, err := repo.Create(ctx, p.ID, "delete target", "ciphertext-a")
	require.NoError(t, err)
	id := s.Instance.ID
	other, err := repo.Create(ctx, p.ID, "other instance", "ciphertext-other")
	require.NoError(t, err)
	s, err = repo.SaveRevision(ctx, id, 1, "ciphertext-b")
	require.NoError(t, err)
	group := piIntegrationGroup(t)
	s, err = repo.ReplaceBindings(ctx, id, s.Instance.Generation, []service.PluginGroupBinding{{InstanceID: id, GroupID: group, Stage: contract.Input, Entry: "inspect", Protocol: "example.text.v1", Enabled: true}})
	require.NoError(t, err)
	gen := s.Instance.Generation
	require.ErrorIs(t, repo.Delete(ctx, id, gen-1), service.ErrPluginInstanceConflict)
	piDeleteAssertCounts(t, id, 1, 2, 1)
	s, err = repo.Publish(ctx, id, gen, 1, true)
	require.NoError(t, err)
	require.ErrorIs(t, repo.Delete(ctx, id, s.Instance.Generation), service.ErrPluginInstanceEnabled)
	s, err = repo.Publish(ctx, id, s.Instance.Generation, 1, false)
	require.NoError(t, err)
	require.ErrorIs(t, packages.Delete(ctx, p.ID, p.BinarySHA256), service.ErrPluginStateChanged)
	candidate := *p
	candidate.Version = "0.2.0"
	candidate.Manifest.Version = "0.2.0"
	_, err = packages.Install(ctx, &candidate, nil)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	require.NoError(t, repo.Delete(ctx, id, s.Instance.Generation))
	piDeleteAssertCounts(t, id, 0, 0, 0)
	require.ErrorIs(t, repo.Delete(ctx, id, s.Instance.Generation), service.ErrPluginInstanceNotFound)
	_, err = repo.Snapshot(ctx, id, 0)
	require.ErrorIs(t, err, service.ErrPluginInstanceNotFound)
	_, err = repo.RevisionSummaries(ctx, id)
	require.ErrorIs(t, err, service.ErrPluginInstanceNotFound)
	selected, err := repo.GroupSnapshots(ctx, group, contract.Input, "example.text.v1")
	require.NoError(t, err)
	require.Empty(t, selected)
	list, err := repo.List(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, []service.PluginInstance{other.Instance}, list)
	require.ErrorIs(t, packages.Delete(ctx, p.ID, p.BinarySHA256), service.ErrPluginStateChanged, "another instance must retain the freeze")
	_, err = packages.Install(ctx, &candidate, nil)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	require.NoError(t, repo.Delete(ctx, other.Instance.ID, other.Instance.Generation))
	updated, err := packages.Install(ctx, &candidate, nil)
	require.NoError(t, err)
	require.Equal(t, p.ID, updated.ID)
	require.Equal(t, "0.2.0", updated.Version)
	require.NoError(t, packages.Delete(ctx, p.ID, p.BinarySHA256))
}
func TestPluginInstanceDeletePublicationRaceIntegration(t *testing.T) {
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	repo := NewPluginInstanceRepository(integrationDB)
	for i := 0; i < 5; i++ {
		s, err := repo.Create(ctx, p.ID, fmt.Sprintf("race-%d", i), "ciphertext-race")
		require.NoError(t, err)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var delErr, pubErr error
		go func() { defer wg.Done(); <-start; delErr = repo.Delete(ctx, s.Instance.ID, 1) }()
		go func() { defer wg.Done(); <-start; _, pubErr = repo.Publish(ctx, s.Instance.ID, 1, 1, true) }()
		close(start)
		wg.Wait()
		require.NotEqual(t, delErr == nil, pubErr == nil, "exactly one same-generation write must commit")
		if delErr == nil {
			require.ErrorIs(t, pubErr, service.ErrPluginInstanceNotFound)
			piDeleteAssertCounts(t, s.Instance.ID, 0, 0, 0)
		} else {
			require.ErrorIs(t, delErr, service.ErrPluginInstanceConflict)
			snapshot, err := repo.Snapshot(ctx, s.Instance.ID, 0)
			require.NoError(t, err)
			require.True(t, snapshot.Instance.Enabled)
			require.EqualValues(t, 2, snapshot.Instance.Generation)
			disabled, err := repo.Publish(ctx, s.Instance.ID, 2, 1, false)
			require.NoError(t, err)
			require.NoError(t, repo.Delete(ctx, s.Instance.ID, disabled.Instance.Generation))
		}
	}
}
func TestPluginInstanceDeleteTransactionRollbackIntegration(t *testing.T) {
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	repo := NewPluginInstanceRepository(integrationDB)
	s, err := repo.Create(ctx, p.ID, "rollback target", "ciphertext-rollback")
	require.NoError(t, err)
	id := s.Instance.ID
	group := piIntegrationGroup(t)
	s, err = repo.ReplaceBindings(ctx, id, 1, []service.PluginGroupBinding{{InstanceID: id, GroupID: group, Stage: contract.Input, Entry: "inspect", Protocol: "example.text.v1", Enabled: true}})
	require.NoError(t, err)
	name := fmt.Sprintf("pi_delete_fail_%d", id)
	// Force failure during the child cascade, not before transaction validation.
	_, err = integrationDB.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.instance_id = %d THEN RAISE EXCEPTION 'fixture child delete failed'; END IF; RETURN OLD; END $$`, name, id))
	require.NoError(t, err)
	_, err = integrationDB.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON sub2api_plugin_instance_revisions FOR EACH ROW EXECUTE FUNCTION %s()`, name, name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := integrationDB.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON sub2api_plugin_instance_revisions; DROP FUNCTION IF EXISTS %s()`, name, name))
		require.NoError(t, e)
	})
	require.ErrorContains(t, repo.Delete(ctx, id, s.Instance.Generation), "fixture child delete failed")
	piDeleteAssertCounts(t, id, 1, 1, 1)
	snapshot, err := repo.Snapshot(ctx, id, 0)
	require.NoError(t, err)
	require.Equal(t, s.Instance, snapshot.Instance)
	_, err = integrationDB.Exec(fmt.Sprintf(`DROP TRIGGER %s ON sub2api_plugin_instance_revisions`, name))
	require.NoError(t, err)
	require.NoError(t, repo.Delete(ctx, id, s.Instance.Generation))
	piDeleteAssertCounts(t, id, 0, 0, 0)
}

type piDeleteIntegrationChecker struct {
	permission string
	seen       []string
}

func (s *piDeleteIntegrationChecker) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	s.seen = append(s.seen, code)
	return code == s.permission, nil
}
func TestPluginInstanceDeleteHTTPIntegration(t *testing.T) {
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	repo := NewPluginInstanceRepository(integrationDB)
	snapshot, err := repo.Create(ctx, p.ID, "HTTP delete", "ciphertext-http-secret")
	require.NoError(t, err)
	id := snapshot.Instance.ID
	s := service.NewPluginInstanceService(repo, piStatusIntegrationCipher{}, nil, time.Second)
	defer s.Close()
	checker := &piDeleteIntegrationChecker{permission: "plugin_instances.read"}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepAllowed := false
	stepCalls := 0
	step := middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		stepCalls++
		if !stepAllowed {
			c.AbortWithStatus(418)
			return
		}
		c.Next()
	})
	h := &handler.Handlers{Admin: &handler.AdminHandlers{PluginInstance: adminhandler.NewPluginInstanceHandler(s)}}
	routes.RegisterAdminRoutes(router.Group("/api/v1", nil), h, auth, audit, step, nil, nil, checker)
	path := "/api/v1/admin/plugin-instances/" + strconv.FormatInt(id, 10)
	request := func(url, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		checker.seen = nil
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("DELETE", url, bytes.NewBufferString(body)))
		require.Equal(t, want, w.Code, w.Body.String())
		require.Equal(t, []string{"plugin_instances.delete"}, checker.seen)
		for _, secret := range []string{"ciphertext-http-secret", "config_encrypted", "install_path", "artifact_path", "manifest", "bindings"} {
			require.NotContains(t, w.Body.String(), secret)
		}
		return w
	}
	body := `{"expected_generation":1}`
	request(path, body, 403)
	require.Zero(t, stepCalls)
	checker.permission = "plugin_instances.delete"
	request(path, body, 418)
	piDeleteAssertCounts(t, id, 1, 1, 0)
	stepAllowed = true
	request(path, `{}`, 400)
	request(path, `{"expected_generation":2}`, 409)
	enabled, err := repo.Publish(ctx, id, 1, 1, true)
	require.NoError(t, err)
	w := request(path, `{"expected_generation":2}`, 409)
	require.Contains(t, w.Body.String(), "PLUGIN_INSTANCE_ENABLED")
	disabled, err := repo.Publish(ctx, id, enabled.Instance.Generation, 1, false)
	require.NoError(t, err)
	w = request(path, fmt.Sprintf(`{"expected_generation":%d}`, disabled.Instance.Generation), 200)
	var receipt struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &receipt))
	require.Equal(t, map[string]any{"deleted": true}, receipt.Data)
	piDeleteAssertCounts(t, id, 0, 0, 0)
	w = request(path, body, 404)
	require.Contains(t, w.Body.String(), "PLUGIN_INSTANCE_NOT_FOUND")
	_, err = s.Status(ctx, id)
	require.True(t, errors.Is(err, service.ErrPluginInstanceNotFound))
	require.Equal(t, 6, stepCalls, "only authorized delete requests reach existing step-up")
}
