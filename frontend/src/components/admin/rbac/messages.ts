// Message functions also work with the application's CSP-compatible runtime-only build.
const en = {
  rolesPartialSave: 'Basic fields were saved, but saving roles failed. Retry keeps your role selection and does not repeat the basic update.',
  sensitiveLocked: 'Email and password changes are protected for this target. You may still update authorized basic fields; legacy role is not submitted.',
  attributesPartialSave: 'Basic fields were saved, but saving custom attributes failed. Retry saves the pending attributes; cancel keeps the saved basic fields.',
  directoryUnavailable: 'This directory requires read access. Other authorized user fields remain editable.',
  catalogDescription: 'Read-only system permission definitions, grouped by resource. Search codes, names and descriptions; this catalog comes directly from the server.',
  title: 'Roles & permissions', subtitle: 'Manage roles and grant only the authority you hold.',
  roles: 'Roles', catalog: 'Permission catalog', create: 'Create role', edit: 'Edit role',
  permissions: 'Permissions', delete: 'Delete', name: 'Name', code: 'Code', description: 'Description',
  actions: 'Actions', save: 'Save', saving: 'Saving…', cancel: 'Cancel', close: 'Close',
  loading: 'Loading…', retry: 'Retry', searchRoles: 'Search roles', searchPermissions: 'Search permissions by resource, code or description',
  noResults: 'No matching results', noPermission: 'You do not have permission to view this section.',
  readOnly: 'Read-only access', lockedRole: 'System and super-admin roles are protected from editing and deletion.',
  ceiling: 'Permissions exceed your authority. Only a super administrator can bypass this ceiling.',
  ceilingHint: 'You can only grant permissions you already hold.', emptyPermissions: 'No permissions selected. Saving sends [] and clears all role permissions.',
  emptyRoles: 'No roles selected. Saving sends [] and removes all assigned roles.',
  loadFailed: 'Loading failed. Retry before making changes.', saveFailed: 'Save failed. Your changes have been kept. Please retry.',
  saved: 'Changes saved', deleted: 'Role deleted', refreshFailed: 'Saved, but refreshing your current permissions failed. Reload before continuing.',
  deleteConfirm: 'Delete this role? Its user assignments and permissions will be removed.',
  deleteFailed: 'Deletion failed. Please retry.', invalid: 'Use a lowercase role code (letters, digits, _ or -, starting with a letter) and a name up to 150 characters.',
  assignRoles: 'Assign roles', userRoles: 'User roles', protectedUser: 'This super-admin user is protected. Role assignment, disabling and deletion are locked.',
  selection: 'Select multiple roles', selected: 'Selected', roleFilter: 'Role filter (current page)', allRoles: 'All roles on this page',
  pageFilterHint: 'Role filtering applies to the loaded page only.', rolesFailed: 'Some user roles failed to load. Retry to enable protected operations.',
  none: 'No roles', viewRoles: 'View roles', legacyHint: 'Legacy account type is separate from RBAC roles. Use Assign roles to manage permissions.'
}
const zh: Record<keyof typeof en, string> = {
  rolesPartialSave: '基本字段已保存，角色分配保存失败。已保留角色选择，重试不会重复更新基本字段。',
  sensitiveLocked: '\u6b64\u7528\u6237\u7684\u90ae\u7bb1\u548c\u5bc6\u7801\u4fee\u6539\u53d7\u4fdd\u62a4\u3002\u4ecd\u53ef\u4fee\u6539\u5df2\u6388\u6743\u7684\u57fa\u672c\u5b57\u6bb5\uff0c\u4e0d\u4f1a\u63d0\u4ea4\u65e7\u7248\u89d2\u8272\u3002',
  attributesPartialSave: '基本字段已保存，自定义属性保存失败。重试将保存待处理属性；取消会保留已保存的基本字段。',
  directoryUnavailable: '此目录需要读取权限，其他已获授权的用户基本字段仍可编辑。',
  catalogDescription: '只读系统权限目录，按资源分组。支持搜索标识、名称和说明；所有定义均直接来自后端。',
  title: '角色与权限', subtitle: '管理角色，仅授予自己拥有的权限。', roles: '角色', catalog: '权限目录', create: '创建角色', edit: '编辑角色',
  permissions: '权限', delete: '删除', name: '名称', code: '标识', description: '描述', actions: '操作', save: '保存', saving: '保存中…', cancel: '取消', close: '关闭',
  loading: '加载中…', retry: '重试', searchRoles: '搜索角色', searchPermissions: '按资源、标识或描述搜索权限', noResults: '无匹配结果',
  noPermission: '你没有查看此区域的权限。', readOnly: '只读访问', lockedRole: '系统角色和超级管理员角色受保护，禁止编辑或删除。',
  ceiling: '权限超过你的授权上限，仅超级管理员可绕过此上限。', ceilingHint: '你只能授予自身已经拥有的权限。',
  emptyPermissions: '尚未选择权限。保存将发送 [] 并清除角色的全部权限。', emptyRoles: '尚未选择角色。保存将发送 [] 并移除全部已分配角色。',
  loadFailed: '加载失败，请重试后再修改。', saveFailed: '保存失败，已保留你的修改，请重试。', saved: '修改已保存', deleted: '角色已删除',
  refreshFailed: '保存已成功，但当前用户权限刷新失败，请刷新页面后继续。', deleteConfirm: '确认删除此角色？相关用户关联和权限将一并移除。',
  deleteFailed: '删除失败，请重试。', invalid: '角色标识须以小写字母开头，仅含小写字母、数字、_ 或 -；名称最多 150 个字符。',
  assignRoles: '分配角色', userRoles: '用户角色', protectedUser: '此超级管理员用户受保护，角色分配、禁用和删除已锁定。',
  selection: '可勾选多个角色', selected: '已选', roleFilter: '角色筛选（当前页）', allRoles: '当前页全部角色',
  pageFilterHint: '角色筛选仅作用于已加载的当前页。', rolesFailed: '部分用户角色加载失败，请重试后再执行受保护操作。', none: '无角色', viewRoles: '查看角色',
  legacyHint: '旧版账户类型与 RBAC 角色相互独立，请通过“分配角色”管理权限。'
}
const messages = (values: Record<string, string>) => Object.fromEntries(Object.entries(values).map(([key, value]) => [key, () => value]))
export const rbacMessages = { en: { rbac: messages(en) }, zh: { rbac: messages(zh) } }
