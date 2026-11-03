package admin

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type deleteOnlyRepo struct {
	service.PluginInstanceRepository
	err     error
	calls   int
	id, gen int64
}

func (r *deleteOnlyRepo) Snapshot(context.Context, int64, int64) (*service.PluginInstanceSnapshot, error) {
	panic("delete handler must not add observation reads")
}
func (r *deleteOnlyRepo) Delete(_ context.Context, id, gen int64) error {
	r.calls++
	r.id = id
	r.gen = gen
	return r.err
}
func deleteHandlerFixture(t *testing.T) (*gin.Engine, *deleteOnlyRepo, *service.PluginInstanceService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &deleteOnlyRepo{}
	s := service.NewPluginInstanceService(repo, statusPanicCipher{}, nil, time.Second)
	t.Cleanup(s.Close)
	h := NewPluginInstanceHandler(s)
	api, ok := any(h).(interface{ Delete(*gin.Context) })
	require.True(t, ok, "handler must implement Delete")
	router := gin.New()
	router.DELETE("/admin/plugin-instances/:id", api.Delete)
	return router, repo, s
}
func TestPluginInstanceDeleteHandlerValidation(t *testing.T) {
	for _, tc := range []struct {
		id, body string
		want     int
	}{
		{"0", `{"expected_generation":1}`, 400}, {"bad", `{"expected_generation":1}`, 400},
		{"9223372036854775808", `{"expected_generation":1}`, 400},
		{"11", ``, 400}, {"11", `{}`, 400}, {"11", `{"expected_generation":0}`, 400},
		{"11", `{"expected_generation":-1}`, 400}, {"11", `{"expected_generation":"1"}`, 400},
		{"11", `{"expected_generation":1,"config":"private"}`, 400},
		{"11", `{"expected_generation":1} {}`, 400},
	} {
		t.Run(tc.id+tc.body, func(t *testing.T) {
			router, r, _ := deleteHandlerFixture(t)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("DELETE", "/admin/plugin-instances/"+tc.id, bytes.NewBufferString(tc.body)))
			require.Equal(t, tc.want, w.Code, w.Body.String())
			require.Zero(t, r.calls)
		})
	}
}
func TestPluginInstanceDeleteHandlerResultMatrix(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"success", nil, 200, `"deleted":true`},
		{"missing", service.ErrPluginInstanceNotFound, 404, "PLUGIN_INSTANCE_NOT_FOUND"},
		{"conflict", service.ErrPluginInstanceConflict, 409, "PLUGIN_INSTANCE_CONFLICT"},
		{"enabled", service.ErrPluginInstanceEnabled, 409, "PLUGIN_INSTANCE_ENABLED"},
		{"busy", service.ErrPluginInstanceBusy, 409, "PLUGIN_INSTANCE_BUSY"},
		{"invalid", service.ErrPluginInstanceInvalid, 400, "PLUGIN_INSTANCE_INVALID"},
		{"closed", service.ErrGeneralClosed, 503, "PLUGIN_INSTANCE_UNAVAILABLE"},
		{"private", errors.New("ciphertext-secret private-path postgres://private/password"), 503, "PLUGIN_INSTANCE_DELETE_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, r, _ := deleteHandlerFixture(t)
			r.err = tc.err
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("DELETE", "/admin/plugin-instances/11", bytes.NewBufferString(`{"expected_generation":3}`)))
			require.Equal(t, tc.want, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), tc.code)
			require.Equal(t, 1, r.calls)
			require.EqualValues(t, 11, r.id)
			require.EqualValues(t, 3, r.gen)
			for _, secret := range []string{"ciphertext-secret", "private-path", "postgres://private/password", "config_encrypted", "manifest", "bindings", "install_path"} {
				require.NotContains(t, w.Body.String(), secret)
				require.NotContains(t, logs.String(), secret)
			}
		})
	}
}
