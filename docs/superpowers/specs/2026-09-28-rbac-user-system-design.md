# RBAC User System Design

**Date:** 2026-09-28
**Branch:** `feature/rbac-user-system`

## Goal

Add a relational RBAC system for the administration backend. Roles are assignable to users, permissions are resource/action based, permission checks are enforced by the backend, and the frontend mirrors the same permissions for routes, menus, and buttons.

## Confirmed Decisions

- Use native relational RBAC integrated with Ent, repositories, services, Gin middleware, and existing audit logs.
- Preserve the existing `Admin API Key`; it always authenticates as the built-in super-admin capability.
- Create a non-deletable `super_admin` system role during initialization.
- A user may have multiple roles; effective permissions are the union of all assigned roles.
- Permissions are code-defined and idempotently synchronized to the database.
- First release controls the administration backend only; ordinary user features keep their current behavior.
- Permission changes become effective on the next request by invalidating affected user permission caches.
- No explicit deny rules, data scopes, departments, role inheritance, dynamic permission creation, or field-level policies in the first release.
- Existing `users.role` remains temporarily for compatibility. New authorization uses `user_roles`.
- Existing `admin` users are mapped to the built-in super-admin capability during compatibility synchronization; a fresh deployment binds the first administrator directly to `super_admin`.

## Data Model

Add four Ent entities and corresponding database tables:

- `roles`: `id`, unique `code`, `name`, `description`, `is_system`, `is_super_admin`, timestamps.
- `permissions`: `id`, unique `code`, `name`, `resource`, `action`, `description`, timestamp.
- `role_permissions`: composite unique `(role_id, permission_id)` with foreign keys to roles and permissions.
- `user_roles`: composite unique `(user_id, role_id)` with foreign keys to users and roles.

System roles cannot be deleted. `super_admin` cannot be renamed, downgraded, or have its effective permissions reduced. Relationship rows are removed when the owning user or role is deleted.

## Permission Catalog

Permissions are declared in Go and synchronized idempotently. Initial catalog:

```text
users.read               users.create              users.update
users.delete             users.roles.assign
roles.read               roles.create              roles.update
roles.delete             roles.permissions.assign
permissions.read
accounts.read            accounts.create            accounts.update
accounts.delete
channels.read            channels.create            channels.update
channels.delete
proxies.read              proxies.create            proxies.update
proxies.delete
groups.read              groups.create              groups.update
groups.delete
subscriptions.read       subscriptions.update
payments.read            payments.refund
settings.read            settings.update
audit_logs.read          ops.read                   ops.manage
```

The catalog is the source of truth; administrators may assign existing permissions but cannot create arbitrary permission identifiers.

## Authorization Flow

`AdminAuth` authenticates a JWT or the existing Admin API Key and places the authenticated subject in Gin context. `RequirePermission(code)` loads effective permissions and enforces a `403` for an authenticated subject without the required permission. Super-admin subjects bypass individual checks.

The effective permission set is the union of all assigned roles. A user with no admin role may authenticate for ordinary user endpoints but cannot enter protected administration routes.

RBAC cache keys use `rbac:user:{user_id}`. User-role changes invalidate one user. Role-permission changes enumerate users assigned to that role and invalidate each affected user. Cache invalidation failure is logged and does not roll back the database transaction; the next request can reload from the database.

## Route and API Changes

Existing `/api/v1/admin/**` routes retain their paths and authentication methods. They gain resource/action middleware at the route registration boundary. Examples:

```go
admin.GET("/users", requirePermission("users.read"), h.Admin.Users.List)
admin.POST("/users", requirePermission("users.create"), h.Admin.Users.Create)
admin.PUT("/users/:id", requirePermission("users.update"), h.Admin.Users.Update)
admin.DELETE("/users/:id", requirePermission("users.delete"), h.Admin.Users.Delete)
```

New endpoints:

```text
GET    /api/v1/admin/roles
POST   /api/v1/admin/roles
GET    /api/v1/admin/roles/:id
PUT    /api/v1/admin/roles/:id
DELETE /api/v1/admin/roles/:id
GET    /api/v1/admin/permissions
GET    /api/v1/admin/roles/:id/permissions
PUT    /api/v1/admin/roles/:id/permissions
GET    /api/v1/admin/users/:id/roles
PUT    /api/v1/admin/users/:id/roles
```

Role and assignment writes enforce the actor permission ceiling: a non-super-admin can only grant permissions already present in the actor's effective set, cannot manage the super-admin role, and cannot alter their own role assignment to obtain a permission they did not already possess.

Existing step-up 2FA remains required for sensitive administrator changes. RBAC answers whether the operation is authorized; step-up answers whether the recent second-factor proof is present.

## Frontend Changes

Extend the authenticated user shape with `roles`, `permissions`, and `is_super_admin`. Add `authStore.hasPermission(code)` and `authStore.hasAnyPermission(codes)` helpers.

Replace admin-only route checks with `requiredPermission` or `requiredPermissions` metadata. Sidebar entries and action buttons use the same helpers. Backend checks remain authoritative.

Add `/admin/roles` and `/admin/permissions` views. Extend `/admin/users` to list, filter, and assign multiple roles. System and super-admin roles are rendered as locked. The permission catalog is read-only.

## Initialization and Compatibility

Startup synchronization performs:

1. Create or update code-defined permissions.
2. Create or repair the `super_admin` system role.
3. For a new installation, bind the first administrator to `super_admin`.
4. For compatibility data, map `users.role = 'admin'` users to super-admin capability without promoting ordinary users.

All synchronization is idempotent and safe to rerun.

## Error and Audit Behavior

- Missing/invalid credentials: `401`.
- Authenticated subject without required permission: `403`.
- Unknown permission code in a role update: `400`.
- Attempt to modify system/super-admin role: `403`.
- Attempt to exceed actor permission ceiling: `403`.
- RBAC writes emit existing audit events: `role.created`, `role.updated`, `role.deleted`, `role.permissions_updated`, `user.roles_updated`, and `permission_catalog.synced`.
- Audit entries include actor, target, before/after values, request ID, and authentication method; secrets and tokens are excluded.

## Testing and Acceptance

Backend tests cover catalog synchronization, role-permission unions, super-admin bypass, `401`/`403` behavior, assignment ceilings, system-role protection, cascade behavior, cache invalidation, Admin API Key compatibility, and audit events. Route tests cover representative read/create/update/delete operations for users and RBAC resources.

Frontend tests cover route guards, sidebar visibility, action visibility, role CRUD, permission assignment, multi-role users, locked system roles, and permission refresh. Required regression commands are `go test ./...`, `pnpm test:run`, and `pnpm build`, with the existing Windows `sh`-dependent repository tests documented separately if they remain environment-limited.
