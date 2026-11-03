import { describe, expect, it } from 'vitest'
import { adminPermissionCatalog } from '@/permissions/catalog'
import type { RBACPermission } from '@/api/admin/rbac'
import { getPermissionLabels, getRoleLabels } from '../labels'

const definition = (code: string): RBACPermission => ({
  code, resource: code.split('.')[0], action: code.split('.').slice(1).join('.'), name: `English ${code}`, description: ''
})

describe('Chinese permission metadata', () => {
  it.each([...adminPermissionCatalog])('translates the code-owned permission %s', code => {
    const source = Object.freeze(definition(code))
    const labels = getPermissionLabels(source, 'zh')
    expect(labels.name).toMatch(/[\u4e00-\u9fff]/)
    expect(labels.resource).toMatch(/[\u4e00-\u9fff]/)
    expect(labels.name).not.toContain('undefined')
    expect(source.code).toBe(code)
    expect(source.name).toBe(`English ${code}`)
  })

  it('keeps English and unknown metadata intact', () => {
    const source = definition('users.read')
    expect(getPermissionLabels(source, 'en').name).toBe(source.name)
    const unknown = { ...definition('future.inspect'), description: 'Custom description' }
    expect(getPermissionLabels(unknown, 'zh')).toEqual({ name: unknown.name, resource: 'future', description: 'Custom description' })
  })

  it('localizes hierarchical actions and English descriptions', () => {
    expect(getPermissionLabels(definition('roles.permissions.assign'), 'zh').name).toBe('分配角色权限')
    expect(getPermissionLabels(definition('users.roles.assign'), 'zh').name).toBe('分配用户角色')
    expect(getPermissionLabels(definition('plugin_instances.bindings.update'), 'zh').name).toBe('编辑插件实例绑定')
    expect(getPermissionLabels({ ...definition('users.read'), description: 'Read-only users' }, 'zh').description).toBe('允许查看用户。')
  })
})

describe('Chinese role metadata', () => {
  const builtin = { code: 'super_admin', name: 'Super Administrator', description: 'Built-in role with all administration permissions', is_system: true, is_super_admin: true }
  it('localizes built-in role summaries without mutating stored data', () => {
    const source = Object.freeze({ ...builtin })
    expect(getRoleLabels(source, 'zh')).toEqual({ name: '超级管理员', description: '内置角色，拥有全部后台管理权限' })
    expect(getRoleLabels(source, 'en')).toEqual({ name: source.name, description: source.description })
    expect(source.name).toBe('Super Administrator')
    expect(getRoleLabels({ code: 'super_admin', name: 'Super Administrator', is_super_admin: true }, 'zh').name).toBe('超级管理员')
  })
  it('preserves custom role names and descriptions', () => {
    expect(getRoleLabels({ code: 'support', name: 'Support Team', description: 'Custom role' }, 'zh')).toEqual({ name: 'Support Team', description: 'Custom role' })
    expect(getRoleLabels(null, 'zh')).toEqual({ name: '', description: '' })
  })
})
