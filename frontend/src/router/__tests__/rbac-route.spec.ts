import { describe, expect, it } from 'vitest'
import router from '../index'
import { adminRoutePolicies } from '@/permissions/policy'

describe('RBAC route metadata', () => {
  it('explicitly classifies every admin route, including redirects', () => {
    for (const route of router.getRoutes().filter(r => r.path.startsWith('/admin'))) {
      expect(adminRoutePolicies[route.path], route.path).toBeDefined()
      expect(route.meta.requiresAdmin, route.path).toBe(true)
      expect(route.meta.requiredPermission || route.meta.superOnly, route.path).toBeTruthy()
    }
  })
  it('keeps permissions independent of roles', () => {
    expect(router.resolve('/admin/permissions').meta.requiredPermission).toBe('permissions.read')
  })
  it('includes the role management page', () => {
    expect(router.resolve('/admin/roles').meta.requiredPermission).toBe('roles.read')
  })
  it('includes the plugin instance management page', () => {
    expect(router.resolve('/admin/plugin-instances').meta.requiredPermission).toBe('plugin_instances.read')
  })
})
