package admin

import (
	"context"
	"encoding/json"
	"errors"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"regexp"
	"strconv"
)

// Explicit HTTP projection keeps raw plugin output out of ordinary run/history responses.
type PluginTaskAdminService interface {
	List(context.Context, int64) ([]service.PluginTask, error)
	Get(context.Context, int64) (*service.PluginTask, error)
	Create(context.Context, *service.PluginTask) (*service.PluginTask, error)
	Update(context.Context, *service.PluginTask, int64) (*service.PluginTask, error)
	Delete(context.Context, int64, int64) error
	Execute(context.Context, int64, string) (*service.PluginTaskRun, error)
	Runs(context.Context, int64, int) ([]service.PluginTaskRun, error)
	Run(context.Context, int64) (*service.PluginTaskRun, error)
	RunSensitive(context.Context, int64) (*service.PluginTaskRun, json.RawMessage, error)
}
type PluginTaskHandler struct{ tasks PluginTaskAdminService }

func NewPluginTaskHandler(tasks *service.PluginTaskService) *PluginTaskHandler {
	return newPluginTaskHandler(tasks)
}
func newPluginTaskHandler(tasks PluginTaskAdminService) *PluginTaskHandler {
	return &PluginTaskHandler{tasks: tasks}
}

type pluginTaskRequest struct {
	InstanceID          int64                   `json:"instance_id"`
	Name                string                  `json:"name"`
	Entry               string                  `json:"entry"`
	Cron                string                  `json:"cron"`
	Enabled             bool                    `json:"enabled"`
	TimeoutSeconds      int                     `json:"timeout_seconds"`
	MaxRetries          int                     `json:"max_retries"`
	RetryBackoffSeconds int                     `json:"retry_backoff_seconds"`
	Input               json.RawMessage         `json:"input"`
	Scope               service.PluginTaskScope `json:"scope"`
}

func (q pluginTaskRequest) task(id int64) *service.PluginTask {
	return &service.PluginTask{ID: id, InstanceID: q.InstanceID, Name: q.Name, Entry: q.Entry, Cron: q.Cron, Enabled: q.Enabled, TimeoutSeconds: q.TimeoutSeconds, MaxRetries: q.MaxRetries, RetryBackoffSeconds: q.RetryBackoffSeconds, Input: q.Input, Scope: q.Scope}
}
func pluginTaskAudit(c *gin.Context, action string, id int64) {
	middleware.SetAuditAction(c, action)
	middleware.SetAuditExtra(c, map[string]any{"result": "failed", "task_id": id})
}
func pluginTaskSuccess(c *gin.Context, fields map[string]any) {
	fields["result"] = "success"
	middleware.SetAuditExtra(c, fields)
}
func pluginTaskError(c *gin.Context, err error) {
	status := http.StatusServiceUnavailable
	code := "PLUGIN_TASK_UNAVAILABLE"
	msg := "plugin task operation unavailable"
	switch {
	case errors.Is(err, service.ErrPluginTaskInvalid), errors.Is(err, service.ErrPluginInstanceInvalid):
		status = 400
		code = "PLUGIN_TASK_INVALID"
		msg = "plugin task request is invalid"
	case errors.Is(err, service.ErrPluginTaskNotFound), errors.Is(err, service.ErrPluginInstanceNotFound):
		status = 404
		code = "PLUGIN_TASK_NOT_FOUND"
		msg = "plugin task was not found"
	case errors.Is(err, service.ErrPluginTaskConflict):
		status = 409
		code = "PLUGIN_TASK_CONFLICT"
		msg = "plugin task generation or lease changed; refresh and retry"
	case errors.Is(err, service.ErrPluginTaskDisabled):
		status = 409
		code = "PLUGIN_TASK_DISABLED"
		msg = "plugin task or instance is disabled"
	case errors.Is(err, service.ErrPluginTaskCapability):
		status = 403
		code = "PLUGIN_TASK_CAPABILITY_DENIED"
		msg = "plugin task capability is not granted"
	}
	middleware.SetAuditExtra(c, map[string]any{"result": "failed", "error_code": code})
	response.ErrorFrom(c, infraerrors.New(status, code, msg))
}
func pluginTaskJSON(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		pluginTaskError(c, service.ErrPluginTaskInvalid)
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		pluginTaskError(c, service.ErrPluginTaskInvalid)
		return false
	}
	return true
}
func pluginTaskID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		pluginTaskError(c, service.ErrPluginTaskInvalid)
		return 0, false
	}
	return id, true
}
func (h *PluginTaskHandler) ready(c *gin.Context) bool {
	if h == nil || h.tasks == nil {
		pluginTaskError(c, errors.New("unavailable"))
		return false
	}
	c.Header("Cache-Control", "no-store")
	return true
}
func (h *PluginTaskHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var id int64
	if raw := c.Query("instance_id"); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			pluginTaskError(c, service.ErrPluginTaskInvalid)
			return
		}
	}
	items, err := h.tasks.List(c.Request.Context(), id)
	if err != nil {
		pluginTaskError(c, err)
		return
	}
	if items == nil {
		items = []service.PluginTask{}
	}
	response.Success(c, gin.H{"tasks": items})
}
func (h *PluginTaskHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := pluginTaskID(c)
	if !ok {
		return
	}
	item, err := h.tasks.Get(c.Request.Context(), id)
	if err != nil {
		pluginTaskError(c, err)
		return
	}
	response.Success(c, gin.H{"task": item})
}
func (h *PluginTaskHandler) Create(c *gin.Context) {
	pluginTaskAudit(c, "plugin_tasks.create", 0)
	if !h.ready(c) {
		return
	}
	var q pluginTaskRequest
	if !pluginTaskJSON(c, &q) {
		return
	}
	item, err := h.tasks.Create(c.Request.Context(), q.task(0))
	if err != nil {
		pluginTaskError(c, err)
		return
	}
	pluginTaskSuccess(c, map[string]any{"task_id": item.ID, "instance_id": item.InstanceID, "generation": item.Generation})
	response.Success(c, gin.H{"task": item})
}
func (h *PluginTaskHandler) Update(c *gin.Context) {
	pluginTaskAudit(c, "plugin_tasks.update", 0)
	if !h.ready(c) {
		return
	}
	id, ok := pluginTaskID(c)
	if !ok {
		return
	}
	var q struct {
		pluginTaskRequest
		ExpectedGeneration int64 `json:"expected_generation"`
	}
	if !pluginTaskJSON(c, &q) {
		return
	}
	if q.ExpectedGeneration <= 0 {
		pluginTaskError(c, service.ErrPluginTaskInvalid)
		return
	}
	item, err := h.tasks.Update(c.Request.Context(), q.task(id), q.ExpectedGeneration)
	if err != nil {
		pluginTaskError(c, err)
		return
	}
	pluginTaskSuccess(c, map[string]any{"task_id": id, "generation": item.Generation})
	response.Success(c, gin.H{"task": item})
}
func (h *PluginTaskHandler) Delete(c *gin.Context) {
	pluginTaskAudit(c, "plugin_tasks.delete", 0)
	if !h.ready(c) {
		return
	}
	id, ok := pluginTaskID(c)
	if !ok {
		return
	}
	var q struct {
		ExpectedGeneration int64 `json:"expected_generation"`
	}
	if !pluginTaskJSON(c, &q) {
		return
	}
	if q.ExpectedGeneration <= 0 {
		pluginTaskError(c, service.ErrPluginTaskInvalid)
		return
	}
	if err := h.tasks.Delete(c.Request.Context(), id, q.ExpectedGeneration); err != nil {
		pluginTaskError(c, err)
		return
	}
	pluginTaskSuccess(c, map[string]any{"task_id": id, "deleted": true})
	response.Success(c, gin.H{"deleted": true})
}

var pluginTaskKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func (h *PluginTaskHandler) Execute(c *gin.Context) {
	pluginTaskAudit(c, "plugin_tasks.run", 0)
	if !h.ready(c) {
		return
	}
	id, ok := pluginTaskID(c)
	if !ok {
		return
	}
	var q struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !pluginTaskJSON(c, &q) {
		return
	}
	if !pluginTaskKeyPattern.MatchString(q.IdempotencyKey) {
		pluginTaskError(c, service.ErrPluginTaskInvalid)
		return
	}
	run, err := h.tasks.Execute(c.Request.Context(), id, q.IdempotencyKey)
	if err != nil {
		pluginTaskError(c, err)
		return
	}
	pluginTaskSuccess(c, map[string]any{"task_id": id, "run_id": run.ID, "status": run.Status})
	response.Success(c, gin.H{"run": run})
}
func (h *PluginTaskHandler) Runs(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := pluginTaskID(c)
	if !ok {
		return
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			pluginTaskError(c, service.ErrPluginTaskInvalid)
			return
		}
		limit = n
	}
	runs, err := h.tasks.Runs(c.Request.Context(), id, limit)
	if err != nil {
		pluginTaskError(c, err)
		return
	}
	if runs == nil {
		runs = []service.PluginTaskRun{}
	}
	response.Success(c, gin.H{"runs": runs})
}
func (h *PluginTaskHandler) Run(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := pluginTaskID(c)
	if !ok {
		return
	}
	run, err := h.tasks.Run(c.Request.Context(), id)
	if err != nil {
		pluginTaskError(c, err)
		return
	}
	response.Success(c, gin.H{"run": run})
}
func (h *PluginTaskHandler) Sensitive(c *gin.Context) {
	pluginTaskAudit(c, "plugin_runs.sensitive", 0)
	if !h.ready(c) {
		return
	}
	id, ok := pluginTaskID(c)
	if !ok {
		return
	}
	run, result, err := h.tasks.RunSensitive(c.Request.Context(), id)
	if err != nil {
		pluginTaskError(c, err)
		return
	}
	pluginTaskSuccess(c, map[string]any{"run_id": id, "task_id": run.TaskID})
	response.Success(c, gin.H{"run": run, "result": result})
}
