package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type RBACHandler struct {
	management *service.RBACManagementService
}

func NewRBACHandler(management *service.RBACManagementService) *RBACHandler {
	return &RBACHandler{management: management}
}

func (h *RBACHandler) ready(c *gin.Context) bool {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.ErrorFrom(c, service.ErrRBACUnauthenticated)
		return false
	}
	if h == nil || h.management == nil {
		response.Error(c, http.StatusServiceUnavailable, "RBAC management unavailable")
		return false
	}
	c.Request = c.Request.WithContext(service.WithRBACMutationAudit(c.Request.Context(), func(change service.RBACMutationAudit) { middleware.SetRBACAuditChange(c, change) }))
	return true
}
func rbacID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, service.ErrRBACInvalidInput)
		return 0, false
	}
	return id, true
}
func rbacActor(c *gin.Context) service.RBACActor {
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	// Authentication sets both fields; neither is derived from JSON or headers here.
	return service.RBACActor{UserID: subject.UserID, AdminAPIKey: c.GetString("auth_method") == service.AuditAuthMethodAdminAPIKey && c.GetBool(string(middleware.ContextKeySuperAdmin))}
}
func bindRBACJSON(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		response.BadRequest(c, "Invalid RBAC request body")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		response.BadRequest(c, "Expected one JSON object")
		return false
	}
	return true
}
func (h *RBACHandler) ListRoles(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	roles, err := h.management.ListRoles(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, roles)
}
func (h *RBACHandler) GetRole(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := rbacID(c)
	if !ok {
		return
	}
	role, err := h.management.GetRole(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, role)
}
func (h *RBACHandler) ListPermissions(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	type permissionDTO struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Resource    string `json:"resource"`
		Action      string `json:"action"`
		Description string `json:"description"`
	}
	catalog := h.management.ListPermissions()
	out := make([]permissionDTO, 0, len(catalog))
	for _, p := range catalog {
		out = append(out, permissionDTO{p.Code, p.Name, p.Resource, p.Action, p.Description})
	}
	response.Success(c, out)
}
func (h *RBACHandler) GetRolePermissions(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := rbacID(c)
	if !ok {
		return
	}
	role, err := h.management.GetRole(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, role.Permissions)
}
func (h *RBACHandler) GetUserRoles(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := rbacID(c)
	if !ok {
		return
	}
	roles, err := h.management.GetUserRoles(c.Request.Context(), id)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, roles)
}
func (h *RBACHandler) CreateRole(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var req struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !bindRBACJSON(c, &req) {
		return
	}
	role, err := h.management.CreateRole(c.Request.Context(), rbacActor(c), service.CreateRBACRoleInput{Code: req.Code, Name: req.Name, Description: req.Description})
	if response.ErrorFrom(c, err) {
		return
	}
	middleware.SetAuditAction(c, "role.created")
	response.Created(c, role)
}
func (h *RBACHandler) UpdateRole(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := rbacID(c)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !bindRBACJSON(c, &req) {
		return
	}
	role, err := h.management.UpdateRole(c.Request.Context(), rbacActor(c), id, service.UpdateRBACRoleInput{Name: req.Name, Description: req.Description})
	if response.ErrorFrom(c, err) {
		return
	}
	middleware.SetAuditAction(c, "role.updated")
	response.Success(c, role)
}
func (h *RBACHandler) DeleteRole(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := rbacID(c)
	if !ok {
		return
	}
	if response.ErrorFrom(c, h.management.DeleteRole(c.Request.Context(), rbacActor(c), id)) {
		return
	}
	middleware.SetAuditAction(c, "role.deleted")
	response.Success(c, gin.H{"deleted": true})
}
func (h *RBACHandler) SetRolePermissions(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := rbacID(c)
	if !ok {
		return
	}
	var req struct {
		Permissions *[]string `json:"permissions"`
	}
	if !bindRBACJSON(c, &req) {
		return
	}
	if req.Permissions == nil {
		response.BadRequest(c, "permissions array is required; use [] to clear")
		return
	}
	if response.ErrorFrom(c, h.management.SetRolePermissions(c.Request.Context(), rbacActor(c), id, *req.Permissions)) {
		return
	}
	middleware.SetAuditAction(c, "role.permissions_updated")
	response.Success(c, gin.H{"updated": true})
}
func (h *RBACHandler) SetUserRoles(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	id, ok := rbacID(c)
	if !ok {
		return
	}
	var req struct {
		RoleIDs *[]int64 `json:"role_ids"`
	}
	if !bindRBACJSON(c, &req) {
		return
	}
	if req.RoleIDs == nil {
		response.BadRequest(c, "role_ids array is required; use [] to clear")
		return
	}
	if response.ErrorFrom(c, h.management.SetUserRoles(c.Request.Context(), rbacActor(c), id, *req.RoleIDs)) {
		return
	}
	middleware.SetAuditAction(c, "user.roles_updated")
	response.Success(c, gin.H{"updated": true})
}
