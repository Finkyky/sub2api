import { useI18n } from 'vue-i18n'
import type { RBACPermission } from '@/api/admin/rbac'
import type { RoleSummary } from '@/types'
import { adminPermissionCatalog } from '@/permissions/catalog'

const resources: Record<string, string> = {
  accounts: '账号', affiliates: '邀请返利', announcements: '公告', api_keys: 'API 密钥',
  audit_logs: '操作日志', backups: '备份', channels: '渠道', compliance: '合规协议',
  dashboard: '仪表盘', groups: '分组', ops: '运维监控', pages: '自定义页面',
  payments: '支付', permissions: '权限目录', plugin_instances: '插件实例', plugins: '插件',
  plugin_runs: '插件运行记录', plugin_tasks: '插件任务', promo_codes: '优惠码',
  prompt_audit: '提示词审计', proxies: '代理', redeem_codes: '兑换码', risk_control: '风控',
  roles: '角色', settings: '系统设置', subscriptions: '订阅', system: '系统',
  usage: '使用记录', user_attributes: '用户属性', users: '用户'
}
const actions: Record<string, string> = {
  read: '查看', create: '创建', update: '编辑', delete: '删除', import: '导入', export: '导出',
  manage: '管理', accept: '确认', configure: '配置', refund: '退款', run: '执行',
  sensitive: '查看敏感信息', publish: '发布', disable: '停用'
}
const permissionNames: Record<string, string> = {
  'users.roles.assign': '分配用户角色',
  'roles.permissions.assign': '分配角色权限',
  'plugin_instances.bindings.update': '编辑插件实例绑定',
  'payments.refund': '发起支付退款',
  'payments.configure': '配置支付渠道',
  'plugin_runs.sensitive': '查看插件运行敏感信息',
  'compliance.accept': '确认部署与运营合规'
}
const isChinese = (locale: string) => locale.toLowerCase().startsWith('zh')
type DisplayRole = Pick<RoleSummary, 'code' | 'name' | 'is_system' | 'is_super_admin'> & { description?: string }

// Translate display metadata only. Immutable role/permission codes and API payloads stay unchanged.
export function getPermissionLabels(permission: RBACPermission, locale: string) {
  const original = { name: permission.name || permission.code, description: permission.description, resource: permission.resource }
  if (!isChinese(locale) || !adminPermissionCatalog.has(permission.code)) return original
  const [resource, ...suffix] = permission.code.split('.')
  const action = suffix.join('.')
  const name = permissionNames[permission.code] || (resources[resource] && actions[action] ? `${actions[action]}${resources[resource]}` : original.name)
  const description = original.description && !/[\u4e00-\u9fff]/.test(original.description)
    ? `允许${name}。` : original.description
  return { name, description, resource: resources[resource] || original.resource }
}

export function getRoleLabels(role: DisplayRole | null | undefined, locale: string) {
  if (!role) return { name: '', description: '' }
  if (isChinese(locale) && role.code === 'super_admin' && (role.is_system || role.is_super_admin)) {
    return { name: '超级管理员', description: '内置角色，拥有全部后台管理权限' }
  }
  return { name: role.name, description: role.description || '' }
}

export function useRBACLabels() {
  const { locale } = useI18n({ useScope: 'global' })
  return {
    resourceName: (resource: string) => isChinese(locale.value) ? resources[resource] || resource : resource,
    permissionName: (permission: RBACPermission) => getPermissionLabels(permission, locale.value).name,
    permissionDescription: (permission: RBACPermission) => getPermissionLabels(permission, locale.value).description,
    roleName: (role: DisplayRole | null | undefined) => getRoleLabels(role, locale.value).name,
    roleDescription: (role: DisplayRole | null | undefined) => getRoleLabels(role, locale.value).description
  }
}
