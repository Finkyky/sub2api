package handler

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// requireBackendModeAdminAccess must use the authenticated user ID, never a role
// from the request or token claims. Every call re-evaluates the current grants.
func requireBackendModeAdminAccess(ctx context.Context, rbac *service.RBACService, userID int64) error {
	if userID <= 0 {
		return infraerrors.Unauthorized("INVALID_USER", "user not found")
	}
	if rbac == nil {
		return infraerrors.Forbidden("BACKEND_MODE_ADMIN_ONLY", "Backend mode is active. Admin access is required.")
	}
	snapshot, err := rbac.Snapshot(ctx, userID)
	if err != nil {
		return infraerrors.InternalServer("RBAC_UNAVAILABLE", "Failed to load permissions")
	}
	if !snapshot.HasAdminAccess() {
		return infraerrors.Forbidden("BACKEND_MODE_ADMIN_ONLY", "Backend mode is active. Admin access is required.")
	}
	return nil
}
