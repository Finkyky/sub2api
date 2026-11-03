-- RBAC foundation: roles, code-defined permissions, and assignments.
CREATE TABLE IF NOT EXISTS roles (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(100) NOT NULL UNIQUE,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    is_super_admin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_roles_is_system ON roles(is_system);
CREATE INDEX IF NOT EXISTS idx_roles_is_super_admin ON roles(is_super_admin);

CREATE TABLE IF NOT EXISTS permissions (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(150) NOT NULL UNIQUE,
    name VARCHAR(150) NOT NULL,
    resource VARCHAR(100) NOT NULL,
    action VARCHAR(100) NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS role_permissions (
    id BIGSERIAL PRIMARY KEY,
    role_id BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    UNIQUE (role_id, permission_id)
);

CREATE INDEX IF NOT EXISTS idx_role_permissions_permission_id
    ON role_permissions(permission_id);

CREATE TABLE IF NOT EXISTS user_roles (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    UNIQUE (user_id, role_id)
);

CREATE INDEX IF NOT EXISTS idx_user_roles_role_id ON user_roles(role_id);

INSERT INTO roles (code, name, description, is_system, is_super_admin)
VALUES ('super_admin', 'Super Administrator', 'Built-in role with all administration permissions', TRUE, TRUE)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    is_system = TRUE,
    is_super_admin = TRUE,
    updated_at = NOW();

INSERT INTO permissions (code, name, resource, action)
VALUES
    ('users.read', 'Read users', 'users', 'read'),
    ('users.create', 'Create users', 'users', 'create'),
    ('users.update', 'Update users', 'users', 'update'),
    ('users.delete', 'Delete users', 'users', 'delete'),
    ('users.roles.assign', 'Assign user roles', 'users', 'roles.assign'),
    ('roles.read', 'Read roles', 'roles', 'read'),
    ('roles.create', 'Create roles', 'roles', 'create'),
    ('roles.update', 'Update roles', 'roles', 'update'),
    ('roles.delete', 'Delete roles', 'roles', 'delete'),
    ('roles.permissions.assign', 'Assign role permissions', 'roles', 'permissions.assign'),
    ('permissions.read', 'Read permissions', 'permissions', 'read'),
    ('accounts.read', 'Read accounts', 'accounts', 'read'),
    ('accounts.create', 'Create accounts', 'accounts', 'create'),
    ('accounts.update', 'Update accounts', 'accounts', 'update'),
    ('accounts.delete', 'Delete accounts', 'accounts', 'delete'),
    ('channels.read', 'Read channels', 'channels', 'read'),
    ('channels.create', 'Create channels', 'channels', 'create'),
    ('channels.update', 'Update channels', 'channels', 'update'),
    ('channels.delete', 'Delete channels', 'channels', 'delete'),
    ('proxies.read', 'Read proxies', 'proxies', 'read'),
    ('proxies.create', 'Create proxies', 'proxies', 'create'),
    ('proxies.update', 'Update proxies', 'proxies', 'update'),
    ('proxies.delete', 'Delete proxies', 'proxies', 'delete'),
    ('groups.read', 'Read groups', 'groups', 'read'),
    ('groups.create', 'Create groups', 'groups', 'create'),
    ('groups.update', 'Update groups', 'groups', 'update'),
    ('groups.delete', 'Delete groups', 'groups', 'delete'),
    ('subscriptions.read', 'Read subscriptions', 'subscriptions', 'read'),
    ('subscriptions.update', 'Update subscriptions', 'subscriptions', 'update'),
    ('payments.read', 'Read payments', 'payments', 'read'),
    ('payments.refund', 'Refund payments', 'payments', 'refund'),
    ('settings.read', 'Read settings', 'settings', 'read'),
    ('settings.update', 'Update settings', 'settings', 'update'),
    ('audit_logs.read', 'Read audit logs', 'audit_logs', 'read'),
    ('ops.read', 'Read operations', 'ops', 'read'),
    ('ops.manage', 'Manage operations', 'ops', 'manage')
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    resource = EXCLUDED.resource,
    action = EXCLUDED.action,
    updated_at = NOW();

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r
 CROSS JOIN permissions p
 WHERE r.code = 'super_admin'
ON CONFLICT (role_id, permission_id) DO NOTHING;

INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
  FROM users u
  JOIN roles r ON r.code = 'super_admin'
 WHERE u.role = 'admin'
ON CONFLICT (user_id, role_id) DO NOTHING;
