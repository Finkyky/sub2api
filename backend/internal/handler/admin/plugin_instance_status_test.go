package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// All other repository methods deliberately remain nil. A status read must only
// query authority, never create a revision, publish, bind or acquire a runtime.
type statusOnlyRepo struct {
	service.PluginInstanceRepository
	snapshot *service.PluginInstanceSnapshot
	err      error
	calls    int
}

func (r *statusOnlyRepo) Snapshot(_ context.Context, id, rev int64) (*service.PluginInstanceSnapshot, error) {
	r.calls++
	if id != 11 || rev != 0 {
		return nil, service.ErrPluginInstanceNotFound
	}
	return r.snapshot, r.err
}

type statusPanicCipher struct{}

func (statusPanicCipher) Encrypt(string) (string, error) { panic("status must not encrypt") }
func (statusPanicCipher) Decrypt(string) (string, error) { panic("status must not decrypt") }

func statusHandlerFixture(t *testing.T) (*gin.Engine, *statusOnlyRepo, *service.PluginInstanceService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &statusOnlyRepo{snapshot: &service.PluginInstanceSnapshot{
		Instance:     service.PluginInstance{ID: 11, Enabled: true, CurrentRevision: 2},
		Revision:     service.PluginInstanceRevision{ConfigEncrypted: "ciphertext-secret"},
		Installation: service.PluginInstallation{InstallPath: "private-install-path", ArtifactPath: "private-artifact-path", ArtifactData: []byte("secret-artifact")},
	}}
	instances := service.NewPluginInstanceService(repo, statusPanicCipher{}, nil, time.Second)
	t.Cleanup(instances.Close)
	router := gin.New()
	router.GET("/admin/plugin-instances/:id/status", NewPluginInstanceHandler(instances).Status)
	return router, repo, instances
}

func TestPluginInstanceStatusHandlerReadOnlyProjection(t *testing.T) {
	router, repo, _ := statusHandlerFixture(t)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/plugin-instances/11/status", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, repo.calls)
	var envelope struct {
		Data service.PluginInstanceStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.EqualValues(t, 11, envelope.Data.InstanceID)
	require.True(t, envelope.Data.Enabled)
	require.EqualValues(t, 2, envelope.Data.CurrentRevision)
	require.True(t, envelope.Data.Closed)
	require.False(t, envelope.Data.RuntimePresent)
	require.Empty(t, envelope.Data.Runtimes)
	require.Contains(t, w.Body.String(), `"runtimes":[]`)
	for _, secret := range []string{"ciphertext-secret", "private-install-path", "private-artifact-path", "secret-artifact", "config_encrypted", "manifest", "bindings"} {
		require.NotContains(t, w.Body.String(), secret)
	}
}

func TestPluginInstanceStatusHandlerValidationAndMissingInstance(t *testing.T) {
	router, repo, _ := statusHandlerFixture(t)
	for _, id := range []string{"0", "-1", "bad", "9223372036854775808"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/plugin-instances/"+id+"/status", nil))
		require.Equal(t, http.StatusBadRequest, w.Code, id)
		require.Contains(t, w.Body.String(), "PLUGIN_INSTANCE_ID_INVALID")
	}
	require.Zero(t, repo.calls)
	repo.err = service.ErrPluginInstanceNotFound
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/plugin-instances/11/status", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "PLUGIN_INSTANCE_NOT_FOUND")
}

func TestPluginInstanceStatusHandlerClosedService(t *testing.T) {
	router, repo, instances := statusHandlerFixture(t)
	instances.Close()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/plugin-instances/11/status", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "PLUGIN_INSTANCE_UNAVAILABLE")
	require.Zero(t, repo.calls)
}

func TestPluginInstanceStatusHandlerRedactsAuthorityFailureFromResponseAndLog(t *testing.T) {
	router, repo, _ := statusHandlerFixture(t)
	repo.err = errors.New("postgres://credential-secret@host database error ciphertext-secret private-install-path")
	var logged bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(old)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/plugin-instances/11/status", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "PLUGIN_INSTANCE_STATUS_UNAVAILABLE")
	for _, secret := range []string{"postgres://", "credential-secret", "ciphertext-secret", "private-install-path"} {
		require.NotContains(t, w.Body.String(), secret)
		require.NotContains(t, logged.String(), secret)
	}
}
