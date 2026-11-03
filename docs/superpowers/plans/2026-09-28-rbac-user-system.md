# RBAC User System Implementation Plan

> **For agentic workers:** execute task-by-task with TDD and keep each step independently testable.

**Goal:** Replace the admin-only boolean authorization model with relational RBAC while preserving existing user features and Admin API Key compatibility.

**Architecture:** Add Ent entities and SQL migrations for roles, permissions, role-permissions, and user-roles. Introduce a permission catalog and authorization service behind Gin middleware, then migrate admin routes and frontend guards to resource/action permissions.

**Tech Stack:** Go 1.21, Gin, Ent, PostgreSQL migrations, Redis cache, Vue 3, Pinia, TypeScript, Vitest.

---

### Task 1: Add failing RBAC domain tests

**Files:**
- Create: `backend/internal/service/rbac_service_test.go`
- Create: `backend/internal/server/middleware/permission_test.go`

- [ ] Define tests for permission union, super-admin bypass, missing permission denial, actor ceiling, and unknown permission rejection.
- [ ] Run `go test ./internal/service ./internal/server/middleware -run RBAC -count=1`; verify failures because the service and middleware do not exist.

### Task 2: Add Ent RBAC schemas and SQL migration

**Files:**
- Create: `backend/ent/schema/role.go`
- Create: `backend/ent/schema/permission.go`
- Create: `backend/ent/schema/role_permission.go`
- Create: `backend/ent/schema/user_role.go`
- Create: `backend/migrations/239_add_rbac.sql`
- Modify: `backend/ent/schema/user.go`
- Modify: `backend/ent/schema/role.go` edges after generation

- [ ] Add fields, indexes, composite uniqueness, foreign-key behavior, and user/role edges.
- [ ] Add idempotent SQL tables and indexes.
- [ ] Run `go generate ./ent`; inspect generated code and run schema-focused tests.

### Task 3: Implement permission catalog and RBAC service

**Files:**
- Create: `backend/internal/service/rbac.go`
- Create: `backend/internal/service/permission_catalog.go`
- Create: `backend/internal/service/rbac_service_test.go` updates
- Modify: dependency wiring files under `backend/internal/server` or `backend/cmd/server`

- [ ] Define typed permission metadata and idempotent synchronization.
- [ ] Implement effective permission union, role CRUD, role-permission assignment, user-role assignment, ceiling validation, and cache invalidation.
- [ ] Run focused service tests until green.

### Task 4: Add authorization middleware

**Files:**
- Create: `backend/internal/server/middleware/permission.go`
- Create: `backend/internal/server/middleware/permission_test.go`
- Modify: `backend/internal/server/middleware/admin_auth.go` only where context data is needed
- Modify: router dependency wiring

- [ ] Add `RequirePermission` middleware using the RBAC service.
- [ ] Return `401` for missing identity and `403` for insufficient permission.
- [ ] Preserve Admin API Key as super-admin.
- [ ] Run middleware tests and existing admin-auth tests.

### Task 5: Add RBAC handlers and routes

**Files:**
- Create: `backend/internal/handler/admin/rbac_handler.go`
- Create: `backend/internal/handler/admin/rbac_handler_test.go`
- Modify: `backend/internal/server/routes/admin.go`
- Modify: handler dependency structs and wiring

- [ ] Implement role, permission catalog, role-permission, and user-role endpoints.
- [ ] Validate IDs, unknown permissions, protected roles, and ceiling violations.
- [ ] Emit audit events.
- [ ] Run focused handler and route contract tests.

### Task 6: Migrate existing admin routes to permissions

**Files:**
- Modify: `backend/internal/server/routes/admin.go`
- Modify: other route registration files under `backend/internal/server/routes` as needed
- Create: `backend/internal/server/routes/rbac_route_contract_test.go`

- [ ] Add `RequirePermission` at route boundaries using the catalog mapping.
- [ ] Keep step-up middleware for sensitive actions.
- [ ] Verify representative users/accounts/settings/payment/audit routes for allow/deny behavior.

### Task 7: Add initialization and compatibility synchronization

**Files:**
- Modify: `backend/internal/setup/setup.go`
- Create: `backend/internal/setup/rbac_setup_test.go`
- Modify: startup wiring

- [ ] Sync permissions and repair `super_admin` idempotently.
- [ ] Bind the first administrator on fresh setup.
- [ ] Map legacy `users.role = 'admin'` users without promoting ordinary users.
- [ ] Run setup tests twice and verify no duplicate rows.

### Task 8: Add frontend auth permission state

**Files:**
- Modify: `frontend/src/types/index.ts`
- Modify: `frontend/src/stores/auth.ts`
- Modify: `frontend/src/api/auth.ts`
- Create: `frontend/src/stores/__tests__/rbac.spec.ts`

- [ ] Extend user payload types and implement permission helpers.
- [ ] Ensure refresh replaces permissions immediately.
- [ ] Write failing Vitest tests first, then implement and run them.

### Task 9: Add frontend route/menu/button guards

**Files:**
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Create/modify: admin views and shared permission helpers
- Create: route/sidebar regression tests

- [ ] Replace binary admin checks with permission metadata.
- [ ] Hide inaccessible modules and action buttons.
- [ ] Add 403 handling for direct navigation.

### Task 10: Add roles and permissions admin UI

**Files:**
- Create: `frontend/src/views/admin/RolesView.vue`
- Create: `frontend/src/views/admin/PermissionsView.vue`
- Modify: `frontend/src/views/admin/UsersView.vue`
- Create: API modules and Vitest tests
- Modify: locale files for all supported languages

- [ ] Implement role CRUD, permission assignment, read-only permission catalog, and multi-role user assignment.
- [ ] Lock system/super-admin roles in the UI.
- [ ] Enforce permission-ceiling errors returned by the backend.

### Task 11: Full verification and documentation

**Files:**
- Modify: `docs/superpowers/specs/2026-09-28-rbac-user-system-design.md` only if verification details need correction
- Create: migration verification notes if required

- [ ] Run backend focused tests, frontend tests, build, and portable full backend tests.
- [ ] Run `git diff --check`, inspect generated code, and verify migration idempotence.
- [ ] Commit each completed task or coherent batch with descriptive messages.
