package service

import "context"

// RBACMutationAudit contains database-derived authorization state only, never
// credentials or HTTP request bodies. Values are detached from ORM objects.
type RBACMutationAudit struct {
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   int64           `json:"target_id"`
	Before     *RBACAuditState `json:"before"`
	After      *RBACAuditState `json:"after"`
}
type RBACAuditState struct {
	Role  *RBACRole  `json:"role,omitempty"`
	Roles []RBACRole `json:"roles,omitempty"`
}
type rbacMutationAuditKey struct{}
type rbacMutationAuditSink func(RBACMutationAudit)

// WithRBACMutationAudit attaches a request-local sink. Mutations emit only after
// transaction commit; before/after reads are performed while its locks are held.
func WithRBACMutationAudit(ctx context.Context, sink func(RBACMutationAudit)) context.Context {
	return context.WithValue(ctx, rbacMutationAuditKey{}, rbacMutationAuditSink(sink))
}
func cloneRBACAuditRole(role *RBACRole) *RBACRole {
	if role == nil {
		return nil
	}
	out := *role
	out.Permissions = append([]string{}, role.Permissions...)
	return &out
}
func cloneRBACAuditRoles(roles []RBACRole) []RBACRole {
	out := make([]RBACRole, 0, len(roles))
	for i := range roles {
		out = append(out, *cloneRBACAuditRole(&roles[i]))
	}
	return out
}

type rbacAuditedTx struct {
	RBACManagementTx
	changes *[]RBACMutationAudit
}

func (t *rbacAuditedTx) recordRole(action string, id int64, before, after *RBACRole) {
	change := RBACMutationAudit{Action: action, TargetType: "role", TargetID: id}
	if before != nil {
		change.Before = &RBACAuditState{Role: cloneRBACAuditRole(before)}
	}
	if after != nil {
		change.After = &RBACAuditState{Role: cloneRBACAuditRole(after)}
	}
	*t.changes = append(*t.changes, change)
}
func (t *rbacAuditedTx) CreateRole(ctx context.Context, role *RBACRole) error {
	if err := t.RBACManagementTx.CreateRole(ctx, role); err != nil {
		return err
	}
	t.recordRole("role.created", role.ID, nil, role)
	return nil
}
func (t *rbacAuditedTx) UpdateRole(ctx context.Context, role *RBACRole) error {
	before, err := t.GetRole(ctx, role.ID)
	if err != nil {
		return err
	}
	before = cloneRBACAuditRole(before)
	if err := t.RBACManagementTx.UpdateRole(ctx, role); err != nil {
		return err
	}
	after, err := t.GetRole(ctx, role.ID)
	if err != nil {
		return err
	}
	t.recordRole("role.updated", role.ID, before, after)
	return nil
}
func (t *rbacAuditedTx) DeleteRole(ctx context.Context, id int64) error {
	before, err := t.GetRole(ctx, id)
	if err != nil {
		return err
	}
	before = cloneRBACAuditRole(before)
	if err := t.RBACManagementTx.DeleteRole(ctx, id); err != nil {
		return err
	}
	t.recordRole("role.deleted", id, before, nil)
	return nil
}
func (t *rbacAuditedTx) SetRolePermissions(ctx context.Context, id int64, codes []string) error {
	before, err := t.GetRole(ctx, id)
	if err != nil {
		return err
	}
	before = cloneRBACAuditRole(before)
	if err := t.RBACManagementTx.SetRolePermissions(ctx, id, codes); err != nil {
		return err
	}
	after, err := t.GetRole(ctx, id)
	if err != nil {
		return err
	}
	t.recordRole("role.permissions_updated", id, before, after)
	return nil
}
func (t *rbacAuditedTx) SetUserRoles(ctx context.Context, id int64, roleIDs []int64) error {
	before, err := t.GetUserRoles(ctx, id)
	if err != nil {
		return err
	}
	before = cloneRBACAuditRoles(before)
	if err := t.RBACManagementTx.SetUserRoles(ctx, id, roleIDs); err != nil {
		return err
	}
	after, err := t.GetUserRoles(ctx, id)
	if err != nil {
		return err
	}
	*t.changes = append(*t.changes, RBACMutationAudit{Action: "user.roles_updated", TargetType: "user", TargetID: id, Before: &RBACAuditState{Roles: before}, After: &RBACAuditState{Roles: cloneRBACAuditRoles(after)}})
	return nil
}
