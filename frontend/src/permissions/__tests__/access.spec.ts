import { describe, expect, it, vi, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick, defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { useAuthStore } from '@/stores/auth'
import { allows, adminRoutePolicies, canAccessAdminPath, filterAdminNavigation, adminLandingPath } from '../policy'
import { permissionDirective } from '@/directives/permission'
import type { User } from '@/types'

beforeEach(() => { setActivePinia(createPinia()); localStorage.clear() })
const reader = { isSuperAdmin: false, hasPermission: (p: string) => p === 'users.read' }
describe('RBAC default deny and navigation', () => {
  it('rejects unclassified routes even for super admin', () => {
    expect(canAccessAdminPath(reader, '/admin/future')).toBe(false)
    expect(canAccessAdminPath({ ...reader, isSuperAdmin: true }, '/admin/future')).toBe(false)
    expect(allows(reader, undefined)).toBe(false)
    expect(allows(reader, { superOnly: true })).toBe(false)
  })
  it('lands on an accessible page, never forces dashboard.read', () => {
    expect(adminLandingPath(reader)).toBe('/admin/users')
    expect(adminLandingPath({ ...reader, hasPermission: () => false })).toBe('/access-denied')
    expect(adminRoutePolicies['/admin/roles']).toEqual({ permission: 'roles.read' })
  })
  it('filters individual children and hides empty parents', () => {
    const items = [{ path: '/admin/security', children: [{ path: '/admin/ops' }] },
      { path: '/admin/manage', children: [{ path: '/admin/users' }, { path: '/admin/roles' }] },
      { path: '/admin/settings' }]
    expect(filterAdminNavigation(items, reader)).toEqual([{ path: '/admin/manage', children: [{ path: '/admin/users' }], expandOnly: true }])
  })
  it('uses RBAC instead of the legacy role and reacts to revocation', () => {
    const auth = useAuthStore()
    auth.user = { role: 'admin', roles: [], permissions: [] } as unknown as User
    expect(auth.isAdmin).toBe(false)
    auth.user = { role: 'user', roles: [], permissions: ['users.read'] } as unknown as User
    expect(auth.isAdmin).toBe(true)
    auth.user.permissions = []
    expect(auth.isAdmin).toBe(false)
    auth.user.roles = [{ id: 1, name: 'operator' }] as User['roles']
    expect(auth.isAdmin).toBe(false)
    auth.user.permissions = ['retired.permission']
    expect(auth.isAdmin).toBe(false)
  })
})
describe('plugin instance permission entry and revocation', () => {
  it.each([
    'plugin_instances.read',
    'plugin_instances.create',
    'plugin_instances.update',
    'plugin_instances.publish',
    'plugin_instances.disable',
    'plugin_instances.delete',
    'plugin_instances.bindings.update'
  ])('recognizes %s as an administration permission', (permission) => {
    const auth = useAuthStore()
    auth.user = { role: 'user', roles: [], permissions: [permission] } as unknown as User
    expect(auth.isAdmin).toBe(true)
    auth.user.permissions = []
    expect(auth.isAdmin).toBe(false)
  })
  it('lands a plugin-only reader on instances and removes navigation after revocation', () => {
    const auth = useAuthStore()
    auth.user = { role: 'user', roles: [], permissions: ['plugin_instances.read'] } as unknown as User
    const items = [{ path: '/admin/dashboard' }, { path: '/admin/plugin-instances' }]
    expect(adminLandingPath(auth)).toBe('/admin/plugin-instances')
    expect(filterAdminNavigation(items, auth)).toEqual([{ path: '/admin/plugin-instances' }])
    auth.user.permissions = []
    expect(adminLandingPath(auth)).toBe('/access-denied')
    expect(filterAdminNavigation(items, auth)).toEqual([])
  })
})
describe('actual action controls', () => {
  it('hides and blocks write buttons and form submission for a read-only operator, including revocation', async () => {
    const auth = useAuthStore()
    auth.user = { permissions: ['users.read'] } as User
    const write = vi.fn()
    const wrapper = mount(defineComponent({ setup: () => ({ write }), template: `<div><button v-permission="'users.update'" @click="write">Save</button><form v-permission="'users.update'" @submit.prevent="write"><input/><button>Submit</button></form></div>` }), { attachTo: document.body, global: { directives: { permission: permissionDirective } } })
    expect(wrapper.get('button').isVisible()).toBe(false)
    await wrapper.get('button').trigger('click')
    await wrapper.get('form').trigger('submit')
    expect(write).not.toHaveBeenCalled()
    auth.user.permissions = ['users.read', 'users.update']
    await nextTick()
    expect(wrapper.get('button').isVisible()).toBe(true)
    await wrapper.get('button').trigger('click')
    expect(write).toHaveBeenCalledTimes(1)
    auth.user.permissions = ['users.read']
    await nextTick()
    expect(wrapper.get('button').isVisible()).toBe(false)
    await wrapper.get('button').trigger('click')
    expect(write).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
