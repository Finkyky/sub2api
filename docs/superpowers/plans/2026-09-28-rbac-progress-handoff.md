# RBAC 开发进度记录（2026-09-28）

下次继续时间：2026-09-29。当前分支：`feature/rbac-user-system`，HEAD `45f2b28`（设计文档已推送）。**实现代码和本记录尚未提交、尚未推送**；不要清理当前工作区。

## 已确认范围

- 关系型原生 RBAC；用户可绑定多个角色，权限取角色并集，资源/操作粒度。
- 首版仅覆盖管理后台；Admin API Key 保留超级管理员能力。
- 超级管理员不可删除、不可限制；现有 `users.role = 'admin'` 兼容映射。
- 权限目录由代码定义并同步数据库；变更下一次请求生效。
- 管理员只能授予自身拥有的权限；用户计划从零部署。

设计和执行计划：
- `docs/superpowers/specs/2026-09-28-rbac-user-system-design.md`
- `docs/superpowers/plans/2026-09-28-rbac-user-system.md`

## 已在工作区实现

1. Ent 的 Role、Permission、RolePermission、UserRole 模型、生成代码，以及 `239_add_rbac_foundation.sql` 迁移。迁移创建基础表、权限目录，绑定既有管理员和初始超级管理员。
2. RBAC permission catalog、角色权限并集/上限校验、repository、`RequirePermission` 中间件、Admin API Key 超管标记；用户管理路由的列表/详情/创建/更新/删除已初步接入。
3. 首位管理员创建时，在同一事务绑定 `super_admin`。
4. 前端用户类型、auth store 权限辅助方法、`/admin/users` 路由权限检查及测试。
5. 本轮发现新部署迁移与 Ent 生成模型不一致：`role_permissions`、`user_roles` 缺少 Ent 所需的 `id` 主键。现已加入 `BIGSERIAL PRIMARY KEY`，关联列保留唯一约束，并新增回归测试。
6. 本轮新增服务层权限快照，`/auth/me` 返回 `roles`、`permissions`、`is_super_admin`，每次请求从数据库读取；新增服务层与 handler 单元测试。

## 已验证

- `backend`: `go test ./internal/repository -run '^TestRBACMigrationMatchesEntJoinPrimaryKeys$' -count=1` 通过。
- `backend`: `go test ./internal/service ./internal/setup ./internal/server/middleware ./internal/server/routes ./cmd/server -run 'RBAC|BootstrapAdmin|RequirePermission' -count=1` 通过。
- `backend`: `go test -tags unit ./internal/handler -run '^TestAuthHandlerGetCurrentUser' -count=1` 通过。
- `frontend`: RBAC 两个 Vitest 文件，共 3 个测试通过；`pnpm typecheck`、`pnpm build` 通过。
- 宽范围 Go 测试本轮两次超时；此前 repository 中 3 项 `pg_dump` 测试受 Windows 缺少 Unix `sh` 影响。不能将聚焦测试当作完整回归通过。
- 迁移修正的原始/修改文件、diff、验证记录和经另一份副本测试的回滚脚本位于 `D:\tmp\sub2api-rbac-20260928`；详情见其中 `VERIFICATION.txt`。

## 明天优先顺序

1. **先检查工作区**：`git status --short --branch`、`git diff --check`；保留所有未提交代码，核对迁移、Ent 生成代码和权限目录一致性。
2. 完成 RBAC service/repository：权限目录启动同步、角色 CRUD、角色权限修改、用户多角色绑定；事务保护超级管理员及权限上限，补单元/集成测试。
3. 增加 `/admin/roles`、`/admin/permissions`、`/admin/roles/:id/permissions`、`/admin/users/:id/roles` API 和审计事件。
4. **封闭权限边界**：目前 JWT 的 admin 门禁仍依赖旧 `user.role`；仅用户管理部分路由接入了 RBAC。接纳普通角色进入后台前，必须为其可访问的全部管理路由建立默认拒绝的权限映射，避免未保护路由被放行。
5. 前端实现角色/权限页面、用户多角色绑定、菜单与按钮级控制。不要仅依赖前端守卫。
6. 完整验证后再分阶段提交并推送该分支；记录 Windows 环境限制及任何测试失败。

当前状态是**基础设施阶段进行中**，并非完整 RBAC 功能交付。
