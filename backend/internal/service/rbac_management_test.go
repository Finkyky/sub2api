package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type rbacManagementMemory struct {
	roles               map[int64]RBACRole
	users               map[int64]RBACManagedUser
	assignments         map[int64][]int64
	inTx                bool
	transactions        int
	writeErr, commitErr error
}

func newRBACManagementMemory() *rbacManagementMemory {
	return &rbacManagementMemory{
		roles: map[int64]RBACRole{
			10: {ID: 10, Code: "manager", Name: "Manager", Permissions: []string{"roles.create", "roles.read", "roles.update", "roles.delete", "roles.permissions.assign", "users.roles.assign", "users.read"}},
			20: {ID: 20, Code: "reader", Name: "Reader", Permissions: []string{"users.read"}},
			30: {ID: 30, Code: "super_admin", Name: "Root", IsSystem: true, IsSuperAdmin: true, Permissions: []string{}},
			40: {ID: 40, Code: "billing", Name: "Billing", Permissions: []string{"payments.refund"}},
		},
		users:       map[int64]RBACManagedUser{1: {ID: 1, Status: StatusActive}, 2: {ID: 2, Status: StatusActive}, 3: {ID: 3, Status: StatusActive, LegacyAdmin: true}},
		assignments: map[int64][]int64{1: {10}, 2: {20}, 3: {30}},
	}
}
func (m *rbacManagementMemory) WithTransaction(ctx context.Context, fn func(RBACManagementTx) error) error {
	m.transactions++
	next := *m
	next.inTx = true
	next.roles = map[int64]RBACRole{}
	for id, r := range m.roles {
		r.Permissions = append([]string{}, r.Permissions...)
		next.roles[id] = r
	}
	next.assignments = map[int64][]int64{}
	for id, roles := range m.assignments {
		next.assignments[id] = append([]int64{}, roles...)
	}
	if err := fn(&next); err != nil {
		return err
	}
	if m.commitErr != nil {
		return m.commitErr
	}
	m.roles = next.roles
	m.assignments = next.assignments
	return nil
}
func (m *rbacManagementMemory) ListRoles(context.Context) ([]RBACRole, error) {
	out := make([]RBACRole, 0, len(m.roles))
	for _, r := range m.roles {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}
func (m *rbacManagementMemory) GetRole(_ context.Context, id int64) (*RBACRole, error) {
	r, ok := m.roles[id]
	if !ok {
		return nil, ErrRBACRoleNotFound
	}
	return &r, nil
}
func (m *rbacManagementMemory) GetUser(_ context.Context, id int64) (*RBACManagedUser, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return &u, nil
}
func (m *rbacManagementMemory) GetUserRoles(ctx context.Context, id int64) ([]RBACRole, error) {
	if _, err := m.GetUser(ctx, id); err != nil {
		return nil, err
	}
	out := make([]RBACRole, 0)
	for _, id := range m.assignments[id] {
		out = append(out, m.roles[id])
	}
	return out, nil
}
func (m *rbacManagementMemory) CreateRole(_ context.Context, r *RBACRole) error {
	if !m.inTx {
		panic("write outside transaction")
	}
	for _, existing := range m.roles {
		if existing.Code == r.Code {
			return ErrRBACRoleExists
		}
	}
	r.ID = 99
	m.roles[r.ID] = *r
	return m.writeErr
}
func (m *rbacManagementMemory) UpdateRole(_ context.Context, r *RBACRole) error {
	if !m.inTx {
		panic("write outside transaction")
	}
	m.roles[r.ID] = *r
	return m.writeErr
}
func (m *rbacManagementMemory) DeleteRole(_ context.Context, id int64) error {
	if !m.inTx {
		panic("write outside transaction")
	}
	delete(m.roles, id)
	return m.writeErr
}
func (m *rbacManagementMemory) SetRolePermissions(_ context.Context, id int64, codes []string) error {
	if !m.inTx {
		panic("write outside transaction")
	}
	r := m.roles[id]
	r.Permissions = codes
	m.roles[id] = r
	return m.writeErr
}
func (m *rbacManagementMemory) SetUserRoles(_ context.Context, id int64, roles []int64) error {
	if !m.inTx {
		panic("write outside transaction")
	}
	m.assignments[id] = roles
	return m.writeErr
}

func TestRBACManagementRoleLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newRBACManagementMemory()
	svc := NewRBACManagementService(repo)
	actor := RBACActor{UserID: 1}
	role, err := svc.CreateRole(ctx, actor, CreateRBACRoleInput{Code: " auditor ", Name: " Auditor ", Description: " Notes "})
	require.NoError(t, err)
	require.Equal(t, "auditor", role.Code)
	require.Equal(t, "Auditor", role.Name)
	require.Empty(t, role.Permissions)
	role, err = svc.UpdateRole(ctx, actor, role.ID, UpdateRBACRoleInput{Name: "Audit team", Description: "Updated"})
	require.NoError(t, err)
	require.Equal(t, "auditor", role.Code)
	require.Equal(t, "Audit team", role.Name)
	require.NoError(t, svc.SetRolePermissions(ctx, actor, role.ID, []string{"users.read", "roles.read", "users.read"}))
	role, err = svc.GetRole(ctx, role.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"roles.read", "users.read"}, role.Permissions)
	require.NoError(t, svc.SetRolePermissions(ctx, actor, role.ID, nil))
	require.Empty(t, repo.roles[role.ID].Permissions)
	require.NoError(t, svc.DeleteRole(ctx, actor, role.ID))
	_, err = svc.GetRole(ctx, role.ID)
	require.ErrorIs(t, err, ErrRBACRoleNotFound)
	require.Equal(t, 5, repo.transactions)
}

func TestRBACManagementRejectsInvalidRoleInput(t *testing.T) {
	for _, input := range []CreateRBACRoleInput{
		{Code: "super_admin", Name: "Root"}, {Code: "bad code", Name: "Bad"}, {Code: "reader", Name: " "},
		{Code: strings.Repeat("a", 101), Name: "Long"}, {Code: "valid", Name: strings.Repeat("x", 151)},
	} {
		repo := newRBACManagementMemory()
		svc := NewRBACManagementService(repo)
		_, err := svc.CreateRole(context.Background(), RBACActor{UserID: 1}, input)
		require.Error(t, err)
		require.NotContains(t, repo.roles, int64(99))
	}
}

func TestRBACManagementPermissionBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		actor RBACActor
		id    int64
		codes []string
		setup func(*rbacManagementMemory)
		want  error
	}{
		{name: "unauthenticated", id: 20, codes: []string{"users.read"}, want: ErrRBACUnauthenticated},
		{name: "inactive actor", actor: RBACActor{UserID: 1}, id: 20, setup: func(m *rbacManagementMemory) { u := m.users[1]; u.Status = "disabled"; m.users[1] = u }, want: ErrRBACForbidden},
		{name: "missing operation permission", actor: RBACActor{UserID: 2}, id: 20, codes: []string{"users.read"}, want: ErrRBACForbidden},
		{name: "unknown permission even for API key", actor: RBACActor{UserID: 1, AdminAPIKey: true}, id: 20, codes: []string{"unknown.read"}, want: ErrRBACUnknownPermission},
		{name: "requested ceiling", actor: RBACActor{UserID: 1}, id: 20, codes: []string{"payments.refund"}, want: ErrRBACPermissionCeiling},
		{name: "existing ceiling blocks stripping", actor: RBACActor{UserID: 1}, id: 40, codes: nil, want: ErrRBACPermissionCeiling},
		{name: "super admin immutable", actor: RBACActor{UserID: 3}, id: 30, codes: nil, want: ErrRBACProtectedRole},
		{name: "system role immutable", actor: RBACActor{UserID: 3}, id: 20, setup: func(m *rbacManagementMemory) { r := m.roles[20]; r.IsSystem = true; m.roles[20] = r }, want: ErrRBACProtectedRole},
		{name: "missing role", actor: RBACActor{UserID: 1}, id: 404, codes: nil, want: ErrRBACRoleNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRBACManagementMemory()
			if tc.setup != nil {
				tc.setup(repo)
			}
			before := repo.roles[tc.id]
			err := NewRBACManagementService(repo).SetRolePermissions(context.Background(), tc.actor, tc.id, tc.codes)
			require.ErrorIs(t, err, tc.want)
			require.Equal(t, before, repo.roles[tc.id])
		})
	}
}

func TestRBACManagementMetadataAndDeleteRespectBoundaries(t *testing.T) {
	for _, op := range []string{"update", "delete"} {
		for _, id := range []int64{30, 40} {
			t.Run(op, func(t *testing.T) {
				repo := newRBACManagementMemory()
				svc := NewRBACManagementService(repo)
				var err error
				if op == "update" {
					_, err = svc.UpdateRole(context.Background(), RBACActor{UserID: 1}, id, UpdateRBACRoleInput{Name: "Changed"})
				} else {
					err = svc.DeleteRole(context.Background(), RBACActor{UserID: 1}, id)
				}
				require.Error(t, err)
				require.Contains(t, repo.roles, id)
				require.NotEqual(t, "Changed", repo.roles[id].Name)
			})
		}
	}
}

func TestRBACManagementUserRoleAssignments(t *testing.T) {
	ctx := context.Background()
	repo := newRBACManagementMemory()
	svc := NewRBACManagementService(repo)
	actor := RBACActor{UserID: 1}
	require.NoError(t, svc.SetUserRoles(ctx, actor, 2, []int64{20, 10, 20}))
	require.Equal(t, []int64{10, 20}, repo.assignments[2])
	roles, err := svc.GetUserRoles(ctx, 2)
	require.NoError(t, err)
	require.Len(t, roles, 2)
	require.NoError(t, svc.SetUserRoles(ctx, actor, 2, nil))
	require.Empty(t, repo.assignments[2])
}

func TestRBACManagementUserRoleAssignmentBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		actor  RBACActor
		target int64
		ids    []int64
		setup  func(*rbacManagementMemory)
		want   error
	}{
		{name: "missing permission", actor: RBACActor{UserID: 2}, target: 2, ids: nil, want: ErrRBACForbidden},
		{name: "super role escalation", actor: RBACActor{UserID: 1}, target: 2, ids: []int64{30}, want: ErrRBACPermissionCeiling},
		{name: "ordinary role ceiling", actor: RBACActor{UserID: 1}, target: 2, ids: []int64{40}, want: ErrRBACPermissionCeiling},
		{name: "protect existing superior role", actor: RBACActor{UserID: 1}, target: 2, ids: nil, setup: func(m *rbacManagementMemory) { m.assignments[2] = []int64{40} }, want: ErrRBACPermissionCeiling},
		{name: "protect super user", actor: RBACActor{UserID: 1, AdminAPIKey: true}, target: 3, ids: nil, want: ErrRBACProtectedUser},
		{name: "protect inconsistent legacy admin", actor: RBACActor{UserID: 1, AdminAPIKey: true}, target: 3, ids: nil, setup: func(m *rbacManagementMemory) { m.assignments[3] = nil }, want: ErrRBACProtectedUser},
		{name: "missing target", actor: RBACActor{UserID: 1}, target: 404, ids: nil, want: ErrUserNotFound},
		{name: "missing role", actor: RBACActor{UserID: 1}, target: 2, ids: []int64{404}, want: ErrRBACRoleNotFound},
		{name: "invalid role ID", actor: RBACActor{UserID: 1}, target: 2, ids: []int64{-1}, want: ErrRBACInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRBACManagementMemory()
			if tc.setup != nil {
				tc.setup(repo)
			}
			before := append([]int64(nil), repo.assignments[tc.target]...)
			err := NewRBACManagementService(repo).SetUserRoles(context.Background(), tc.actor, tc.target, tc.ids)
			require.ErrorIs(t, err, tc.want)
			require.Equal(t, before, repo.assignments[tc.target])
		})
	}
}

func TestRBACManagementSuperAdminAndAPIKeyCanGrant(t *testing.T) {
	for _, actor := range []RBACActor{{UserID: 3}, {UserID: 1, AdminAPIKey: true}} {
		repo := newRBACManagementMemory()
		svc := NewRBACManagementService(repo)
		require.NoError(t, svc.SetRolePermissions(context.Background(), actor, 20, []string{"payments.refund"}))
		require.NoError(t, svc.SetUserRoles(context.Background(), actor, 2, []int64{30}))
		require.Equal(t, []int64{30}, repo.assignments[2])
		require.False(t, repo.users[2].LegacyAdmin)
	}
}

func TestRBACManagementReloadsActorPermissionsAndUnion(t *testing.T) {
	repo := newRBACManagementMemory()
	svc := NewRBACManagementService(repo)
	ctx := context.Background()
	repo.assignments[1] = []int64{10, 40}
	require.NoError(t, svc.SetRolePermissions(ctx, RBACActor{UserID: 1}, 20, []string{"payments.refund"}))
	repo.assignments[1] = []int64{40}
	require.ErrorIs(t, svc.SetRolePermissions(ctx, RBACActor{UserID: 1}, 20, nil), ErrRBACForbidden)
	require.Equal(t, []string{"payments.refund"}, repo.roles[20].Permissions)
}

func TestRBACManagementWriteAndCommitFailureRollBack(t *testing.T) {
	for _, stage := range []string{"write", "commit"} {
		for _, op := range []string{"create", "update", "delete", "permissions", "user_roles"} {
			t.Run(stage+"/"+op, func(t *testing.T) {
				repo := newRBACManagementMemory()
				cause := errors.New("injected failure")
				if stage == "write" {
					repo.writeErr = cause
				} else {
					repo.commitErr = cause
				}
				svc := NewRBACManagementService(repo)
				actor := RBACActor{UserID: 1}
				ctx := context.Background()
				var err error
				switch op {
				case "create":
					var got *RBACRole
					got, err = svc.CreateRole(ctx, actor, CreateRBACRoleInput{Code: "new", Name: "New"})
					require.Nil(t, got)
				case "update":
					var got *RBACRole
					got, err = svc.UpdateRole(ctx, actor, 20, UpdateRBACRoleInput{Name: "Changed"})
					require.Nil(t, got)
				case "delete":
					err = svc.DeleteRole(ctx, actor, 20)
				case "permissions":
					err = svc.SetRolePermissions(ctx, actor, 20, nil)
				case "user_roles":
					err = svc.SetUserRoles(ctx, actor, 2, nil)
				}
				require.ErrorIs(t, err, cause)
				require.NotContains(t, repo.roles, int64(99))
				require.Equal(t, "Reader", repo.roles[20].Name)
				require.Equal(t, []string{"users.read"}, repo.roles[20].Permissions)
				require.Equal(t, []int64{20}, repo.assignments[2])
			})
		}
	}
}

func TestRBACManagementAdminAPIKeyRetainsCapabilityForInactiveBackingUser(t *testing.T) {
	repo := newRBACManagementMemory()
	u := repo.users[1]
	u.Status = "disabled"
	repo.users[1] = u
	// Existing Admin API Key authentication is independent of backing-user JWT status.
	err := NewRBACManagementService(repo).SetRolePermissions(context.Background(), RBACActor{UserID: 1, AdminAPIKey: true}, 20, []string{"payments.refund"})
	require.NoError(t, err)
}

func TestRBACManagementReadModelsAndCatalog(t *testing.T) {
	svc := NewRBACManagementService(newRBACManagementMemory())
	ctx := context.Background()
	roles, err := svc.ListRoles(ctx)
	require.NoError(t, err)
	require.Len(t, roles, 4)
	catalog := svc.ListPermissions()
	require.Len(t, catalog, len(PermissionCatalog()))
	for i := 1; i < len(catalog); i++ {
		require.Less(t, catalog[i-1].Code, catalog[i].Code)
	}
	_, err = svc.GetRole(ctx, 0)
	require.ErrorIs(t, err, ErrRBACInvalidInput)
	_, err = svc.GetUserRoles(ctx, -1)
	require.ErrorIs(t, err, ErrRBACInvalidInput)
	_, err = svc.GetUserRoles(ctx, 404)
	require.ErrorIs(t, err, ErrUserNotFound)
}
func TestRBACManagementRoleCreateDuplicateAndOperationDenial(t *testing.T) {
	repo := newRBACManagementMemory()
	svc := NewRBACManagementService(repo)
	ctx := context.Background()
	created, err := svc.CreateRole(ctx, RBACActor{UserID: 1}, CreateRBACRoleInput{Code: "reader", Name: "Duplicate"})
	require.Nil(t, created)
	require.ErrorIs(t, err, ErrRBACRoleExists)
	created, err = svc.CreateRole(ctx, RBACActor{UserID: 2}, CreateRBACRoleInput{Code: "new", Name: "New"})
	require.Nil(t, created)
	require.ErrorIs(t, err, ErrRBACForbidden)
	updated, err := svc.UpdateRole(ctx, RBACActor{UserID: 2}, 20, UpdateRBACRoleInput{Name: "Denied"})
	require.Nil(t, updated)
	require.ErrorIs(t, err, ErrRBACForbidden)
	require.ErrorIs(t, svc.DeleteRole(ctx, RBACActor{UserID: 2}, 20), ErrRBACForbidden)
	require.Equal(t, "Reader", repo.roles[20].Name)
}
