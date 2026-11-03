package admin

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type taskHTTPFake struct {
	fail  error
	calls int
	key   string
}

func (f *taskHTTPFake) List(context.Context, int64) ([]service.PluginTask, error) {
	return []service.PluginTask{}, f.fail
}
func (f *taskHTTPFake) Get(context.Context, int64) (*service.PluginTask, error) {
	return &service.PluginTask{ID: 1, Generation: 2}, f.fail
}
func (f *taskHTTPFake) Create(_ context.Context, t *service.PluginTask) (*service.PluginTask, error) {
	f.calls++
	return t, f.fail
}
func (f *taskHTTPFake) Update(_ context.Context, t *service.PluginTask, _ int64) (*service.PluginTask, error) {
	f.calls++
	return t, f.fail
}
func (f *taskHTTPFake) Delete(context.Context, int64, int64) error { f.calls++; return f.fail }
func (f *taskHTTPFake) Execute(_ context.Context, _ int64, key string) (*service.PluginTaskRun, error) {
	f.calls++
	f.key = key
	return &service.PluginTaskRun{ID: 9, Status: "succeeded", ResultEncrypted: "SECRET-CIPHER"}, f.fail
}
func (f *taskHTTPFake) Runs(context.Context, int64, int) ([]service.PluginTaskRun, error) {
	return []service.PluginTaskRun{{ID: 9, ResultEncrypted: "SECRET-CIPHER"}}, f.fail
}
func (f *taskHTTPFake) Run(context.Context, int64) (*service.PluginTaskRun, error) {
	return &service.PluginTaskRun{ID: 9, ResultEncrypted: "SECRET-CIPHER"}, f.fail
}
func (f *taskHTTPFake) RunSensitive(context.Context, int64) (*service.PluginTaskRun, json.RawMessage, error) {
	return &service.PluginTaskRun{ID: 9}, json.RawMessage(`{"status":"succeeded","output":{"token":"SECRET-RESULT"}}`), f.fail
}
func taskHTTP(t *testing.T, f *taskHTTPFake, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newPluginTaskHandler(f)
	r.GET("/tasks", h.List)
	r.GET("/tasks/:id", h.Get)
	r.POST("/tasks", h.Create)
	r.PUT("/tasks/:id", h.Update)
	r.DELETE("/tasks/:id", h.Delete)
	r.POST("/tasks/:id/run", h.Execute)
	r.GET("/tasks/:id/runs", h.Runs)
	r.GET("/runs/:id", h.Run)
	r.GET("/runs/:id/sensitive", h.Sensitive)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}
func TestPluginTaskHTTPInputAndRedaction(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/tasks/1/run", `{}`}, {"POST", "/tasks/1/run", `{"idempotency_key":"x","secret":"bad"}`}, {"POST", "/tasks/1/run", `{"idempotency_key":"x"} {}`}, {"PUT", "/tasks/1", `{"expected_generation":0}`}, {"DELETE", "/tasks/1", `{"expected_generation":-1}`}, {"GET", "/tasks/0", ""}, {"GET", "/tasks?instance_id=bad", ""}, {"GET", "/tasks/1/runs?limit=5000", ""},
	} {
		f := &taskHTTPFake{}
		w := taskHTTP(t, f, tc.method, tc.path, tc.body)
		require.Equal(t, http.StatusBadRequest, w.Code, tc.path)
		require.Zero(t, f.calls)
	}
	f := &taskHTTPFake{}
	w := taskHTTP(t, f, "POST", "/tasks/1/run", `{"idempotency_key":"manual-123"}`)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "manual-123", f.key)
	require.NotContains(t, w.Body.String(), "SECRET")
	for _, path := range []string{"/tasks/1/runs", "/runs/9"} {
		w = taskHTTP(t, f, "GET", path, "")
		require.Equal(t, 200, w.Code)
		require.NotContains(t, w.Body.String(), "SECRET")
		require.NotContains(t, w.Body.String(), "result_encrypted")
	}
	w = taskHTTP(t, f, "GET", "/runs/9/sensitive", "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "SECRET-RESULT")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
func TestPluginTaskHTTPErrorProjection(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{service.ErrPluginTaskInvalid, 400}, {service.ErrPluginTaskNotFound, 404}, {service.ErrPluginTaskConflict, 409}, {service.ErrPluginTaskDisabled, 409}, {service.ErrPluginTaskCapability, 403}, {errors.New("token=SECRET-ERROR"), 503}} {
		w := taskHTTP(t, &taskHTTPFake{fail: tc.err}, "POST", "/tasks/1/run", `{"idempotency_key":"a"}`)
		require.Equal(t, tc.status, w.Code)
		require.NotContains(t, w.Body.String(), "SECRET-ERROR")
	}
}
