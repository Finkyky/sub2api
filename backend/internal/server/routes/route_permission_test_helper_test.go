package routes

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// allowAllRoutePermissionChecker isolates route-behavior tests from RBAC policy
// evaluation. Policy coverage and denial paths use explicit checkers instead.
type allowAllRoutePermissionChecker struct{}

func (allowAllRoutePermissionChecker) HasPermission(context.Context, int64, string) (bool, error) {
	return true, nil
}

func (allowAllRoutePermissionChecker) Snapshot(context.Context, int64) (service.RBACSnapshot, error) {
	return service.RBACSnapshot{IsSuperAdmin: true}, nil
}
