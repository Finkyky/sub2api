package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// PluginInstanceHandler exposes the P2c management projection. It deliberately
// never returns encrypted or decrypted instance configuration.
type PluginInstanceHandler struct {
	instances *service.PluginInstanceService
}

func NewPluginInstanceHandler(instances *service.PluginInstanceService) *PluginInstanceHandler {
	return &PluginInstanceHandler{instances: instances}
}

type pluginInstanceResponse struct {
	Instance service.PluginInstance                `json:"instance"`
	Revision service.PluginInstanceRevisionSummary `json:"revision"`
	Manifest service.PluginManifest                `json:"manifest"`
	Bindings []service.PluginGroupBinding          `json:"bindings"`
}

func projectPluginInstance(s *service.PluginInstanceSnapshot) pluginInstanceResponse {
	return pluginInstanceResponse{
		Instance: s.Instance,
		Revision: service.PluginInstanceRevisionSummary{InstanceID: s.Revision.InstanceID, Revision: s.Revision.Revision, ConfigVersion: s.Revision.ConfigVersion},
		Manifest: s.Installation.Manifest,
		Bindings: append([]service.PluginGroupBinding(nil), s.Bindings...),
	}
}

type pluginInstanceCreateRequest struct {
	PluginID int64           `json:"plugin_id"`
	Name     string          `json:"name"`
	Config   json.RawMessage `json:"config"`
}
type pluginInstanceGenerationRequest struct {
	ExpectedGeneration int64 `json:"expected_generation"`
}
type pluginInstanceConfigRequest struct {
	ExpectedGeneration int64           `json:"expected_generation"`
	Config             json.RawMessage `json:"config"`
}
type pluginInstancePublishRequest struct {
	ExpectedGeneration int64 `json:"expected_generation"`
	Revision           int64 `json:"revision"`
}
type pluginInstanceBindingsRequest struct {
	ExpectedGeneration int64                        `json:"expected_generation"`
	Bindings           []service.PluginGroupBinding `json:"bindings"`
}

func (h *PluginInstanceHandler) List(c *gin.Context) {
	pluginID := int64(0)
	if raw := c.Query("plugin_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			response.ErrorFrom(c, infraerrors.BadRequest("PLUGIN_INSTANCE_PLUGIN_ID_INVALID", "plugin_id is invalid"))
			return
		}
		pluginID = parsed
	}
	items, err := h.instances.List(c.Request.Context(), pluginID)
	if err != nil {
		pluginInstanceError(c, err)
		return
	}
	response.Success(c, items)
}

func (h *PluginInstanceHandler) Get(c *gin.Context) {
	id, ok := pluginInstanceIDParam(c)
	if !ok {
		return
	}
	snapshot, err := h.instances.Snapshot(c.Request.Context(), id, 0)
	if err != nil {
		pluginInstanceError(c, err)
		return
	}
	response.Success(c, projectPluginInstance(snapshot))
}

// Status observes database publication and local leases without runtime acquisition.
func (h *PluginInstanceHandler) Status(c *gin.Context) {
	id, ok := pluginInstanceIDParam(c)
	if !ok {
		return
	}
	status, err := h.instances.Status(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPluginInstanceInvalid), errors.Is(err, service.ErrPluginInstanceNotFound), errors.Is(err, service.ErrGeneralClosed):
			pluginInstanceError(c, err)
		default:
			// Repository failures may contain DSNs, paths or ciphertext. Do not
			// forward or log the raw error through the HTTP response helper.
			response.ErrorFrom(c, infraerrors.ServiceUnavailable("PLUGIN_INSTANCE_STATUS_UNAVAILABLE", "plugin instance status is unavailable"))
		}
		return
	}
	response.Success(c, status)
}

func (h *PluginInstanceHandler) Revisions(c *gin.Context) {
	id, ok := pluginInstanceIDParam(c)
	if !ok {
		return
	}
	revisions, err := h.instances.RevisionSummaries(c.Request.Context(), id)
	if err != nil {
		pluginInstanceError(c, err)
		return
	}
	response.Success(c, revisions)
}

func (h *PluginInstanceHandler) Create(c *gin.Context) {
	audit := beginPluginInstanceAudit(c, middleware.AuditActionPluginInstanceCreate)
	var req pluginInstanceCreateRequest
	if !decodePluginInstanceJSON(c, &req) {
		return
	}
	audit.attempt(0, req.PluginID, 0, 0)
	if len(req.Config) == 0 {
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_REQUEST_INVALID")
		response.ErrorFrom(c, infraerrors.BadRequest("PLUGIN_INSTANCE_REQUEST_INVALID", "plugin instance configuration is required"))
		return
	}
	snapshot, err := h.instances.Create(c.Request.Context(), req.PluginID, req.Name, req.Config)
	if err != nil {
		pluginInstanceWriteError(c, err)
		return
	}
	audit.success(snapshot)
	response.Created(c, projectPluginInstance(snapshot))
}

func (h *PluginInstanceHandler) SaveConfig(c *gin.Context) {
	audit := beginPluginInstanceAudit(c, middleware.AuditActionPluginInstanceConfig)
	id, ok := pluginInstanceIDParam(c)
	if !ok {
		return
	}
	var req pluginInstanceConfigRequest
	if !decodePluginInstanceJSON(c, &req) {
		return
	}
	audit.attempt(id, 0, req.ExpectedGeneration, 0)
	if len(req.Config) == 0 {
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_REQUEST_INVALID")
		response.ErrorFrom(c, infraerrors.BadRequest("PLUGIN_INSTANCE_REQUEST_INVALID", "plugin instance configuration is required"))
		return
	}
	snapshot, err := h.instances.SaveConfig(c.Request.Context(), id, req.ExpectedGeneration, req.Config)
	if err != nil {
		pluginInstanceWriteError(c, err)
		return
	}
	audit.success(snapshot)
	response.Success(c, projectPluginInstance(snapshot))
}

func (h *PluginInstanceHandler) Publish(c *gin.Context) {
	audit := beginPluginInstanceAudit(c, middleware.AuditActionPluginInstancePublish)
	id, ok := pluginInstanceIDParam(c)
	if !ok {
		return
	}
	var req pluginInstancePublishRequest
	if !decodePluginInstanceJSON(c, &req) {
		return
	}
	audit.attempt(id, 0, req.ExpectedGeneration, req.Revision)
	snapshot, err := h.instances.Publish(c.Request.Context(), id, req.ExpectedGeneration, req.Revision)
	if err != nil {
		pluginInstanceWriteError(c, err)
		return
	}
	audit.success(snapshot)
	response.Success(c, projectPluginInstance(snapshot))
}

func (h *PluginInstanceHandler) Disable(c *gin.Context) {
	audit := beginPluginInstanceAudit(c, middleware.AuditActionPluginInstanceDisable)
	id, ok := pluginInstanceIDParam(c)
	if !ok {
		return
	}
	var req pluginInstanceGenerationRequest
	if !decodePluginInstanceJSON(c, &req) {
		return
	}
	audit.attempt(id, 0, req.ExpectedGeneration, 0)
	snapshot, err := h.instances.Disable(c.Request.Context(), id, req.ExpectedGeneration)
	if err != nil {
		pluginInstanceWriteError(c, err)
		return
	}
	audit.success(snapshot)
	response.Success(c, projectPluginInstance(snapshot))
}

// Delete returns only a receipt, never a snapshot containing private fields.
func (h *PluginInstanceHandler) Delete(c *gin.Context) {
	audit := beginPluginInstanceAudit(c, middleware.AuditActionPluginInstanceDelete)
	id, ok := pluginInstanceIDParam(c)
	if !ok {
		return
	}
	var req pluginInstanceGenerationRequest
	if !decodePluginInstanceJSON(c, &req) {
		return
	}
	audit.attempt(id, 0, req.ExpectedGeneration, 0)
	if err := h.instances.Delete(c.Request.Context(), id, req.ExpectedGeneration); err != nil {
		switch {
		case errors.Is(err, service.ErrPluginInstanceInvalid), errors.Is(err, service.ErrPluginInstanceNotFound), errors.Is(err, service.ErrPluginInstanceConflict), errors.Is(err, service.ErrPluginInstanceEnabled), errors.Is(err, service.ErrPluginInstanceBusy), errors.Is(err, service.ErrGeneralClosed):
			pluginInstanceWriteError(c, err)
		default:
			pluginInstanceAuditError(c, "PLUGIN_INSTANCE_DELETE_UNAVAILABLE")
			response.ErrorFrom(c, infraerrors.ServiceUnavailable("PLUGIN_INSTANCE_DELETE_UNAVAILABLE", "plugin instance deletion is unavailable"))
		}
		return
	}
	audit.deleted()
	response.Success(c, gin.H{"deleted": true})
}

func (h *PluginInstanceHandler) ReplaceBindings(c *gin.Context) {
	audit := beginPluginInstanceAudit(c, middleware.AuditActionPluginInstanceBindings)
	id, ok := pluginInstanceIDParam(c)
	if !ok {
		return
	}
	var req pluginInstanceBindingsRequest
	if !decodePluginInstanceJSON(c, &req) {
		return
	}
	audit.attempt(id, 0, req.ExpectedGeneration, 0)
	snapshot, err := h.instances.ReplaceBindings(c.Request.Context(), id, req.ExpectedGeneration, req.Bindings)
	if err != nil {
		pluginInstanceWriteError(c, err)
		return
	}
	audit.success(snapshot)
	response.Success(c, projectPluginInstance(snapshot))
}

func decodePluginInstanceJSON(c *gin.Context, dst any) bool {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 4*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_REQUEST_INVALID")
		response.ErrorFrom(c, infraerrors.BadRequest("PLUGIN_INSTANCE_REQUEST_INVALID", "plugin instance request is invalid"))
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_REQUEST_INVALID")
		response.ErrorFrom(c, infraerrors.BadRequest("PLUGIN_INSTANCE_REQUEST_INVALID", "plugin instance request must contain one JSON document"))
		return false
	}
	return true
}

func pluginInstanceIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_ID_INVALID")
		response.ErrorFrom(c, infraerrors.BadRequest("PLUGIN_INSTANCE_ID_INVALID", "plugin instance id is invalid"))
		return 0, false
	}
	return id, true
}

func pluginInstanceWriteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPluginInstanceInvalid):
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_INVALID")
		response.ErrorFrom(c, infraerrors.BadRequest("PLUGIN_INSTANCE_INVALID", "plugin instance request is invalid"))
	case errors.Is(err, service.ErrPluginInstanceNotFound):
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_NOT_FOUND")
		response.ErrorFrom(c, infraerrors.NotFound("PLUGIN_INSTANCE_NOT_FOUND", "plugin instance was not found"))
	case errors.Is(err, service.ErrPluginInstanceEnabled):
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_ENABLED")
		response.ErrorFrom(c, infraerrors.Conflict("PLUGIN_INSTANCE_ENABLED", "disable the plugin instance before deleting it"))
	case errors.Is(err, service.ErrPluginInstanceBusy):
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_BUSY")
		response.ErrorFrom(c, infraerrors.Conflict("PLUGIN_INSTANCE_BUSY", "plugin instance still has active local leases"))
	case errors.Is(err, service.ErrPluginInstanceConflict):
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_CONFLICT")
		response.ErrorFrom(c, infraerrors.Conflict("PLUGIN_INSTANCE_CONFLICT", "plugin instance changed; reload and retry"))
	case errors.Is(err, service.ErrGeneralClosed):
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_UNAVAILABLE")
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("PLUGIN_INSTANCE_UNAVAILABLE", "plugin instance service is closed"))
	case errors.Is(err, service.ErrPluginInstanceUnavailable):
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_UNAVAILABLE")
		response.ErrorFrom(c, infraerrors.New(http.StatusUnprocessableEntity, "PLUGIN_INSTANCE_UNAVAILABLE", "plugin instance runtime is unavailable"))
	default:
		pluginInstanceAuditError(c, "PLUGIN_INSTANCE_OPERATION_UNAVAILABLE")
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("PLUGIN_INSTANCE_OPERATION_UNAVAILABLE", "plugin instance operation is unavailable"))
	}
}

func pluginInstanceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPluginInstanceInvalid):
		response.ErrorFrom(c, infraerrors.BadRequest("PLUGIN_INSTANCE_INVALID", "plugin instance request is invalid"))
	case errors.Is(err, service.ErrPluginInstanceNotFound):
		response.ErrorFrom(c, infraerrors.NotFound("PLUGIN_INSTANCE_NOT_FOUND", "plugin instance was not found"))
	case errors.Is(err, service.ErrPluginInstanceEnabled):
		response.ErrorFrom(c, infraerrors.Conflict("PLUGIN_INSTANCE_ENABLED", "disable the plugin instance before deleting it"))
	case errors.Is(err, service.ErrPluginInstanceBusy):
		response.ErrorFrom(c, infraerrors.Conflict("PLUGIN_INSTANCE_BUSY", "plugin instance still has active local leases"))
	case errors.Is(err, service.ErrPluginInstanceConflict):
		response.ErrorFrom(c, infraerrors.Conflict("PLUGIN_INSTANCE_CONFLICT", "plugin instance changed; reload and retry"))
	case errors.Is(err, service.ErrGeneralClosed):
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("PLUGIN_INSTANCE_UNAVAILABLE", "plugin instance service is closed"))
	case errors.Is(err, service.ErrPluginInstanceUnavailable):
		response.ErrorFrom(c, infraerrors.New(http.StatusUnprocessableEntity, "PLUGIN_INSTANCE_UNAVAILABLE", "plugin instance runtime is unavailable"))
	default:
		// Preserve an ApplicationError's HTTP status, not its arbitrary text,
		// metadata or cause. Passing err through ErrorFrom would also log it.
		status := infraerrors.Code(err)
		if status < 400 || status > 599 {
			status = http.StatusInternalServerError
		}
		response.ErrorFrom(c, infraerrors.New(status, "PLUGIN_INSTANCE_READ_FAILED", "plugin instance read failed"))
	}
}
