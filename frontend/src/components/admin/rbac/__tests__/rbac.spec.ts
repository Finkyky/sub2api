import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { reactive } from 'vue'
import RolesView from '@/views/admin/RolesView.vue'
import PermissionsView from '@/views/admin/PermissionsView.vue'
import PermissionPicker from '../PermissionPicker.vue'
import RoleEditorDialog from '../RoleEditorDialog.vue'
import RolePermissionsDialog from '../RolePermissionsDialog.vue'
import UserRolesDialog from '../UserRolesDialog.vue'

const mocks = vi.hoisted(() => ({
  api: Object.fromEntries(['listRoles', 'getRole', 'createRole', 'updateRole', 'deleteRole', 'listPermissions', 'getRolePermissions', 'setRolePermissions', 'getUserRoles', 'setUserRoles'].map(k => [k, vi.fn()])),
  refreshUser: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))
enableAutoUnmount(afterEach)
let auth: any
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('@/api/admin/rbac', () => ({ rbacAPI: mocks.api }))
const role = (id = 1, extra = {}) => ({ id, code: `role_${id}`, name: `Role ${id}`, description: '', is_system: false, is_super_admin: false, permissions: ['users.read'], ...extra })
const permissions = [
  { code: 'users.read', resource: 'users', action: 'read', name: 'Read users', description: '' },
  { code: 'users.delete', resource: 'users', action: 'delete', name: 'Delete users', description: '' },
  { code: 'roles.read', resource: 'roles', action: 'read', name: 'Read roles', description: '' }
]
const mountUI = (component: any, props = {}, locale = 'en') => mount(component, {
  props,
  global: { plugins: [createI18n({ legacy: false, locale, messages: {} })], stubs: {
    teleport: true, AppLayout: { template: '<main><slot /></main>' },
    TablePageLayout: { template: '<div><slot name="actions"/><slot name="filters"/><slot name="table"/></div>' }
  } }
})
beforeEach(() => {
  vi.resetAllMocks()
  auth = reactive({ isSuperAdmin: false, user: { id: 7, permissions: ['roles.read', 'permissions.read', 'users.read'] },
    hasPermission: (p: string) => auth.isSuperAdmin || auth.user.permissions.includes(p), refreshUser: mocks.refreshUser })
  mocks.api.listRoles.mockResolvedValue([role()])
  mocks.api.getRole.mockImplementation(async (id: number) => role(id))
  mocks.api.listPermissions.mockResolvedValue(permissions)
  mocks.api.getRolePermissions.mockResolvedValue(['users.read'])
  mocks.api.getUserRoles.mockResolvedValue([role()])
  mocks.api.createRole.mockResolvedValue(role(3))
  mocks.refreshUser.mockResolvedValue(auth.user)
})
const grant = (...codes: string[]) => auth.user.permissions.push(...codes)

describe('RBAC permission picker', () => {
  it('groups and searches permissions, enforcing the authority ceiling', async () => {
    const w = mountUI(PermissionPicker, { permissions, modelValue: [], disabled: false })
    expect(w.find('input[value="users.read"]').attributes('disabled')).toBeUndefined()
    expect(w.find('input[value="users.delete"]').attributes('disabled')).toBeDefined()
    await w.get('input[type="search"]').setValue('roles.read')
    expect(w.find('input[value="users.read"]').exists()).toBe(false)
    expect(w.text()).toContain('roles')
    await w.get('input[value="roles.read"]').setValue(true)
    expect(w.emitted('update:modelValue')?.[0]).toEqual([['roles.read']])
  })
  it('supports super bypass and read-only mode', async () => {
    auth.isSuperAdmin = true
    const w = mountUI(PermissionPicker, { permissions, modelValue: [] })
    expect(w.find('input[value="users.delete"]').attributes('disabled')).toBeUndefined()
    await w.setProps({ disabled: true })
    expect(w.findAll('input[type="checkbox"]').every(c => c.attributes('disabled') !== undefined)).toBe(true)
  })
  it('renders Chinese local messages', () => {
    const w = mountUI(PermissionPicker, { permissions, modelValue: [] }, 'zh')
    expect(w.get('input[type="search"]').attributes('placeholder')).toContain('搜索')
  })
})

describe('roles page', () => {
  it('does not fetch protected APIs with no read permission', async () => {
    auth.user.permissions = []
    const w = mountUI(RolesView)
    await flushPromises()
    expect(mocks.api.listRoles).not.toHaveBeenCalled()
    expect(mocks.api.listPermissions).not.toHaveBeenCalled()
    expect(w.text()).toContain('permission')
  })
  it('renders read-only roles without mutation buttons', async () => {
    const w = mountUI(RolesView)
    await flushPromises()
    expect(w.text()).toContain('Role 1')
    expect(w.find('[data-testid="create-role"]').exists()).toBe(false)
    expect(w.find('[data-testid="edit-role-1"]').exists()).toBe(false)
    expect(w.find('[data-testid="delete-role-1"]').exists()).toBe(false)
  })
  it('locks system, super, and out-of-ceiling roles even with mutation permissions', async () => {
    grant('roles.update', 'roles.delete', 'roles.permissions.assign')
    mocks.api.listRoles.mockResolvedValue([role(1, { is_system: true }), role(2, { code: 'super_admin' }), role(3, { permissions: ['users.delete'] })])
    const w = mountUI(RolesView)
    await flushPromises()
    for (const id of [1, 2, 3]) {
      expect(w.find(`[data-testid="edit-role-${id}"]`).exists()).toBe(false)
      expect(w.find(`[data-testid="delete-role-${id}"]`).exists()).toBe(false)
    }
  })
  it('shows a retryable load error', async () => {
    mocks.api.listRoles.mockRejectedValueOnce(new Error('offline'))
    const w = mountUI(RolesView)
    await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(true)
    await w.get('[data-testid="retry-roles"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Role 1')
  })
})

describe('role metadata', () => {
  it('saves only metadata and immutable code is excluded on update', async () => {
    grant('roles.update')
    const w = mountUI(RoleEditorDialog, { role: role(), show: true })
    await w.get('[data-testid="role-name"]').setValue('Updated')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.api.updateRole).toHaveBeenCalledWith(1, { name: 'Updated', description: '' })
    expect(mocks.api.setRolePermissions).not.toHaveBeenCalled()
    expect(w.emitted('saved')).toBeTruthy()
  })
  it('creates a role using the exact API payload', async () => {
    grant('roles.create')
    const w = mountUI(RoleEditorDialog, { role: null, show: true })
    await w.get('[data-testid="role-code"]').setValue('auditor')
    await w.get('[data-testid="role-name"]').setValue('Auditor')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.api.createRole).toHaveBeenCalledWith({ code: 'auditor', name: 'Auditor', description: '' })
  })
  it('retains form on save failure and supports cancel', async () => {
    grant('roles.update')
    mocks.api.updateRole.mockRejectedValueOnce(new Error('offline'))
    const w = mountUI(RoleEditorDialog, { role: role(), show: true })
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(true)
    expect(w.emitted('saved')).toBeFalsy()
    await w.get('[data-testid="cancel"]').trigger('click')
    expect(w.emitted('close')).toBeTruthy()
  })
})

describe('role permissions', () => {
  it('explicitly sends [] to clear and refreshes current user permissions', async () => {
    grant('roles.permissions.assign')
    const w = mountUI(RolePermissionsDialog, { role: role(), show: true })
    await flushPromises()
    await w.get('input[value="users.read"]').setValue(false)
    await w.get('[data-testid="save-permissions"]').trigger('click')
    await flushPromises()
    expect(mocks.api.setRolePermissions).toHaveBeenCalledWith(1, [])
    expect(mocks.refreshUser).toHaveBeenCalledOnce()
  })
  it('never saves protected roles, including for a super actor', async () => {
    auth.isSuperAdmin = true
    const w = mountUI(RolePermissionsDialog, { role: role(1, { is_system: true }), show: true })
    await flushPromises()
    expect(w.find('[data-testid="save-permissions"]').exists()).toBe(false)
    expect(mocks.api.setRolePermissions).not.toHaveBeenCalled()
  })
  it('fails closed when initial permissions cannot be loaded', async () => {
    grant('roles.permissions.assign')
    mocks.api.getRolePermissions.mockRejectedValue(new Error('offline'))
    const w = mountUI(RolePermissionsDialog, { role: role(), show: true })
    await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(true)
    expect(w.find('[data-testid="save-permissions"]').attributes('disabled')).toBeDefined()
  })
})

describe('multi-role users', () => {
  const user = { id: 7, email: 'user@example.com', role: 'user' }
  it('saves several role IDs then supports an explicit empty replacement', async () => {
    grant('users.roles.assign')
    mocks.api.listRoles.mockResolvedValue([role(1), role(2)])
    const w = mountUI(UserRolesDialog, { user, show: true })
    await flushPromises()
    await w.get('input[value="2"]').setValue(true)
    await w.get('[data-testid="save-user-roles"]').trigger('click')
    await flushPromises()
    expect(mocks.api.setUserRoles).toHaveBeenCalledWith(7, [1, 2])
    expect(mocks.refreshUser).toHaveBeenCalledOnce()
    const empty = mountUI(UserRolesDialog, { user, show: true })
    await flushPromises()
    await empty.get('input[value="1"]').setValue(false)
    await empty.get('[data-testid="save-user-roles"]').trigger('click')
    await flushPromises()
    expect(mocks.api.setUserRoles).toHaveBeenLastCalledWith(7, [])
  })
  it('disables roles above the ceiling and super roles for a normal actor', async () => {
    grant('users.roles.assign')
    mocks.api.listRoles.mockResolvedValue([role(), role(2, { permissions: ['users.delete'] }), role(3, { is_super_admin: true })])
    const w = mountUI(UserRolesDialog, { user, show: true })
    await flushPromises()
    expect(w.get('input[value="2"]').attributes('disabled')).toBeDefined()
    expect(w.get('input[value="3"]').attributes('disabled')).toBeDefined()
  })
  it('protects legacy and RBAC super users with a visible explanation', async () => {
    auth.isSuperAdmin = true
    for (const target of [{ ...user, role: 'admin' }, { ...user, is_super_admin: true }]) {
      const w = mountUI(UserRolesDialog, { user: target, show: true })
      await flushPromises()
      expect(w.text()).toContain('protected')
      expect(w.find('[data-testid="save-user-roles"]').exists()).toBe(false)
    }
  })
  it('locks assignments if an existing role exceeds authority', async () => {
    grant('users.roles.assign')
    mocks.api.getUserRoles.mockResolvedValue([role(9, { permissions: ['users.delete'] })])
    const w = mountUI(UserRolesDialog, { user, show: true })
    await flushPromises()
    expect(w.find('[data-testid="save-user-roles"]').exists()).toBe(false)
  })
  it('is read-only without assign permission', async () => {
    const w = mountUI(UserRolesDialog, { user, show: true })
    await flushPromises()
    expect(w.find('[data-testid="save-user-roles"]').exists()).toBe(false)
  })
  it('keeps selection and error visible on server rejection', async () => {
    grant('users.roles.assign')
    mocks.api.setUserRoles.mockRejectedValue(new Error('forbidden'))
    const w = mountUI(UserRolesDialog, { user, show: true })
    await flushPromises()
    await w.get('[data-testid="save-user-roles"]').trigger('click')
    await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(true)
    expect((w.get('input[value="1"]').element as HTMLInputElement).checked).toBe(true)
    expect(w.emitted('saved')).toBeFalsy()
  })
})


describe('standalone read-only permission catalog', () => {
  it('fetches only the catalog and renders grouped searchable backend definitions', async () => {
    auth.user.permissions = ['permissions.read']
    const w = mountUI(PermissionsView)
    await flushPromises()
    expect(mocks.api.listPermissions).toHaveBeenCalledOnce()
    expect(mocks.api.listRoles).not.toHaveBeenCalled()
    expect(w.findAll('legend').map(node => node.text())).toEqual(['roles', 'users'])
    expect(w.findAll('input[type="checkbox"]').every(node => node.attributes('disabled') !== undefined)).toBe(true)
    await w.get('input[type="search"]').setValue('Delete users')
    expect(w.find('input[value="users.delete"]').exists()).toBe(true)
    expect(w.find('input[value="users.read"]').exists()).toBe(false)
    expect(mocks.api.setRolePermissions).not.toHaveBeenCalled()
  })
  it('makes no request when permission is absent', async () => {
    auth.user.permissions = ['roles.read']
    const w = mountUI(PermissionsView)
    await flushPromises()
    expect(mocks.api.listPermissions).not.toHaveBeenCalled()
    expect(w.text()).toContain('permission')
  })
  it('shows loading, recoverable errors and Chinese labels', async () => {
    mocks.api.listPermissions.mockRejectedValueOnce(new Error('offline'))
    const w = mountUI(PermissionsView, {}, 'zh')
    expect(w.text()).toContain('加载中')
    await flushPromises()
    expect(w.find('[role="alert"]').text()).toContain('加载失败')
    await w.get('[data-testid="retry-catalog"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('权限目录')
    expect(w.get('input[type="search"]').attributes('placeholder')).toContain('搜索')
  })
  it('clears cached permissions immediately when read access is revoked', async () => {
    const w = mountUI(PermissionsView)
    await flushPromises()
    auth.user.permissions = []
    await flushPromises()
    expect(w.find('input[value="users.read"]').exists()).toBe(false)
    expect(mocks.api.listPermissions).toHaveBeenCalledOnce()
  })
})


describe('RBAC mutation boundaries', () => {
  it('rejects direct metadata submit on system roles and without update permission', async () => {
    for (const target of [role(), role(2, { is_system: true })]) {
      const w = mountUI(RoleEditorDialog, { role: target, show: true })
      await w.get('form').trigger('submit')
      expect(mocks.api.updateRole).not.toHaveBeenCalled()
    }
  })
  it('asks for delete confirmation, keeps errors, and refreshes after retry', async () => {
    grant('roles.delete')
    mocks.api.deleteRole.mockRejectedValueOnce({ status: 409, message: 'Role conflict' })
    const w = mountUI(RolesView)
    await flushPromises()
    await w.get('[data-testid="delete-role-1"]').trigger('click')
    expect(mocks.api.deleteRole).not.toHaveBeenCalled()
    await w.get('[data-testid="confirm-delete-role"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Role conflict')
    await w.get('[data-testid="confirm-delete-role"]').trigger('click')
    await flushPromises()
    expect(mocks.api.deleteRole).toHaveBeenLastCalledWith(1)
    expect(mocks.refreshUser).toHaveBeenCalledOnce()
    expect(mocks.api.listRoles).toHaveBeenCalledTimes(2)
  })
  it('does not fetch the catalog for a role reader lacking permissions.read', async () => {
    auth.user.permissions = ['roles.read']
    const w = mountUI(RolePermissionsDialog, { role: role(), show: true })
    await flushPromises()
    expect(mocks.api.listPermissions).not.toHaveBeenCalled()
    expect(w.text()).toContain('users.read')
    expect(w.find('[data-testid="save-permissions"]').exists()).toBe(false)
  })
  it('preserves a normalized API error on permission save', async () => {
    grant('roles.permissions.assign')
    mocks.api.setRolePermissions.mockRejectedValue({ status: 403, message: 'Permission revoked' })
    const w = mountUI(RolePermissionsDialog, { role: role(), show: true })
    await flushPromises()
    await w.get('[data-testid="save-permissions"]').trigger('click')
    await flushPromises()
    expect(w.find('[role="alert"]').text()).toContain('Permission revoked')
    expect(w.emitted('saved')).toBeFalsy()
  })
  it('stops duplicate saves and does not describe refresh failure as a failed write', async () => {
    grant('users.roles.assign')
    let finish!: () => void
    mocks.api.setUserRoles.mockImplementation(() => new Promise<void>(resolve => { finish = resolve }))
    mocks.refreshUser.mockRejectedValue(new Error('offline'))
    const w = mountUI(UserRolesDialog, { user: { id: 7, email: 'me@example.com', role: 'user' }, show: true })
    await flushPromises()
    await w.get('[data-testid="save-user-roles"]').trigger('click')
    await w.get('[data-testid="save-user-roles"]').trigger('click')
    expect(mocks.api.setUserRoles).toHaveBeenCalledOnce()
    expect(w.get('[data-testid="cancel"]').attributes('disabled')).toBeDefined()
    finish(); await flushPromises()
    expect(mocks.showError).toHaveBeenCalledWith(expect.stringContaining('Saved, but'))
    expect(w.emitted('saved')).toBeTruthy()
  })
  it('ignores stale role loads when the target user changes', async () => {
    grant('users.roles.assign')
    let finish!: (roles: any[]) => void
    mocks.api.getUserRoles.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const w = mountUI(UserRolesDialog, { user: { id: 7, email: 'first@example.com', role: 'user' }, show: true })
    await w.setProps({ user: { id: 8, email: 'second@example.com', role: 'user' } })
    await flushPromises()
    finish([role(99, { is_super_admin: true })]); await flushPromises()
    expect(w.find('input[value="99"]').exists()).toBe(false)
    await w.get('[data-testid="save-user-roles"]').trigger('click'); await flushPromises()
    expect(mocks.api.setUserRoles).toHaveBeenCalledWith(8, [1])
    expect(mocks.refreshUser).not.toHaveBeenCalled()
  })
  it('keeps role assignments locked after initial load failure', async () => {
    grant('users.roles.assign')
    mocks.api.getUserRoles.mockRejectedValue(new Error('offline'))
    const w = mountUI(UserRolesDialog, { user: { id: 7, email: 'me@example.com', role: 'user' }, show: true })
    await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(true)
    expect(w.get('[data-testid="save-user-roles"]').attributes('disabled')).toBeDefined()
    expect(mocks.api.setUserRoles).not.toHaveBeenCalled()
  })
})


describe('RBAC dialog read-access lifecycle', () => {
  const cases = [
    { label: 'role permissions', component: RolePermissionsDialog, props: { role: role(), show: true }, read: 'roles.read', api: 'getRolePermissions', save: 'save-permissions', value: 'users.read', data: ['users.read'] },
    { label: 'user roles', component: UserRolesDialog, props: { user: { id: 9, email: 'target@example.com', role: 'user' }, show: true }, read: 'users.read', api: 'getUserRoles', save: 'save-user-roles', value: '1', data: [role()] }
  ]
  beforeEach(() => grant('roles.permissions.assign', 'users.roles.assign'))

  for (const scenario of cases) {
    it(`${scenario.label}: clears loaded data and blocks writes on read revocation`, async () => {
      const w = mountUI(scenario.component, scenario.props)
      await flushPromises()
      expect(w.find(`input[value="${scenario.value}"]`).exists()).toBe(true)
      auth.user.permissions = auth.user.permissions.filter((p: string) => p !== scenario.read)
      await flushPromises()
      expect(w.find(`input[value="${scenario.value}"]`).exists()).toBe(false)
      expect(w.find(`[data-testid="${scenario.save}"]`).exists()).toBe(false)
      expect(mocks.api[scenario.api]).toHaveBeenCalledOnce()
      expect(w.text()).toContain('permission')
      grant(scenario.read)
      await flushPromises()
      expect(mocks.api[scenario.api]).toHaveBeenCalledTimes(2)
      expect(w.find(`input[value="${scenario.value}"]`).exists()).toBe(true)
    })

    it(`${scenario.label}: discards a pending response after read revocation`, async () => {
      let finish!: (data: any) => void
      mocks.api[scenario.api].mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
      const w = mountUI(scenario.component, scenario.props)
      auth.user.permissions = auth.user.permissions.filter((p: string) => p !== scenario.read)
      await flushPromises()
      finish(scenario.data)
      await flushPromises()
      expect(w.find(`input[value="${scenario.value}"]`).exists()).toBe(false)
      expect(w.find(`[data-testid="${scenario.save}"]`).exists()).toBe(false)
      expect(w.text()).not.toContain('Loading')
    })

    it(`${scenario.label}: discards a pending failure after read revocation`, async () => {
      let fail!: (error: unknown) => void
      mocks.api[scenario.api].mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject }))
      const w = mountUI(scenario.component, scenario.props)
      auth.user.permissions = auth.user.permissions.filter((p: string) => p !== scenario.read)
      await flushPromises()
      fail({ status: 403, message: 'Stale private response' })
      await flushPromises()
      expect(w.find('[role="alert"]').exists()).toBe(false)
      expect(w.text()).not.toContain('Stale private response')
    })

    it(`${scenario.label}: reloads on account switch and ignores the previous account response`, async () => {
      let finish!: (data: any) => void
      mocks.api[scenario.api].mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
      mocks.api[scenario.api].mockResolvedValueOnce([])
      const w = mountUI(scenario.component, scenario.props)
      auth.user = { ...auth.user, id: 8 }
      await flushPromises()
      expect(mocks.api[scenario.api]).toHaveBeenCalledTimes(2)
      finish(scenario.data)
      await flushPromises()
      const checkbox = w.find(`input[value="${scenario.value}"]`)
      expect(checkbox.exists() && (checkbox.element as HTMLInputElement).checked).toBe(false)
    })

    it(`${scenario.label}: retains the draft across same-account permission snapshot refresh`, async () => {
      const w = mountUI(scenario.component, scenario.props)
      await flushPromises()
      await w.get(`input[value="${scenario.value}"]`).setValue(false)
      auth.user = { ...auth.user, permissions: [...auth.user.permissions] }
      await flushPromises()
      expect(mocks.api[scenario.api]).toHaveBeenCalledOnce()
      expect((w.get(`input[value="${scenario.value}"]`).element as HTMLInputElement).checked).toBe(false)
    })
  }

  it('drops catalog definitions when catalog read is revoked but retains assigned codes', async () => {
    const w = mountUI(RolePermissionsDialog, { role: role(), show: true })
    await flushPromises()
    expect(w.text()).toContain('Read users')
    auth.user.permissions = auth.user.permissions.filter((p: string) => p !== 'permissions.read')
    await flushPromises()
    expect(w.text()).not.toContain('Read users')
    expect(w.find('input[value="users.delete"]').exists()).toBe(false)
    expect(w.find('input[value="users.read"]').exists()).toBe(true)
    expect(w.find('[data-testid="save-permissions"]').exists()).toBe(false)
    expect(mocks.api.listPermissions).toHaveBeenCalledOnce()
  })

  it('drops unassigned role choices when role read is revoked but retains user assignments', async () => {
    mocks.api.listRoles.mockResolvedValue([role(), role(2)])
    const w = mountUI(UserRolesDialog, { user: { id: 9, email: 'target@example.com', role: 'user' }, show: true })
    await flushPromises()
    expect(w.find('input[value="2"]').exists()).toBe(true)
    auth.user.permissions = auth.user.permissions.filter((p: string) => p !== 'roles.read')
    await flushPromises()
    expect(w.find('input[value="2"]').exists()).toBe(false)
    expect(w.find('input[value="1"]').exists()).toBe(true)
    expect(w.find('[data-testid="save-user-roles"]').exists()).toBe(false)
    expect(mocks.api.listRoles).toHaveBeenCalledOnce()
  })

  const catalogs = [
    { label: 'permission catalog', component: RolePermissionsDialog, props: { role: role(), show: true }, read: 'permissions.read', api: 'listPermissions', data: permissions, value: 'users.delete', assigned: 'users.read' },
    { label: 'role catalog', component: UserRolesDialog, props: { user: { id: 9, email: 'target@example.com', role: 'user' }, show: true }, read: 'roles.read', api: 'listRoles', data: [role(), role(2)], value: '2', assigned: '1' }
  ]
  for (const scenario of catalogs) {
    for (const outcome of ['success', 'failure']) {
      it(`${scenario.label}: ignores late ${outcome} after revocation and reloads on restoration`, async () => {
        let finish!: (data: any) => void
        let fail!: (error: unknown) => void
        mocks.api[scenario.api].mockImplementationOnce(() => new Promise((resolve, reject) => { finish = resolve; fail = reject }))
        mocks.api[scenario.api].mockResolvedValueOnce(scenario.data)
        const w = mountUI(scenario.component, scenario.props)
        auth.user.permissions = auth.user.permissions.filter((p: string) => p !== scenario.read)
        await flushPromises()
        if (outcome === 'success') finish(scenario.data)
        else fail({ status: 403, message: 'Stale catalog response' })
        await flushPromises()
        expect(w.find(`input[value="${scenario.value}"]`).exists()).toBe(false)
        expect(w.find(`input[value="${scenario.assigned}"]`).exists()).toBe(true)
        expect(w.find('[role="alert"]').exists()).toBe(false)
        expect(mocks.api[scenario.api]).toHaveBeenCalledOnce()
        grant(scenario.read)
        await flushPromises()
        expect(mocks.api[scenario.api]).toHaveBeenCalledTimes(2)
        expect(w.find(`input[value="${scenario.value}"]`).exists()).toBe(true)
        expect(w.find('[role="alert"]').exists()).toBe(false)
      })
    }
  }
})


describe('Chinese RBAC display and authorization', () => {
  const superRole = () => role(10, { code: 'super_admin', name: 'Super Administrator', description: 'Built-in role with all administration permissions', is_system: true, is_super_admin: true })

  it('shows Chinese resource groups and permission names while retaining immutable codes', () => {
    const w = mountUI(PermissionPicker, { permissions, modelValue: [] }, 'zh')
    expect(w.findAll('legend').map(legend => legend.text())).toContain('用户')
    expect(w.text()).toContain('查看用户')
    expect(w.text()).toContain('删除用户')
    expect(w.text()).not.toContain('Read users')
    expect(w.get('input[value="users.read"]').attributes('value')).toBe('users.read')
  })

  it('searches Chinese names and still accepts English permission codes', async () => {
    const w = mountUI(PermissionPicker, { permissions, modelValue: [] }, 'zh')
    await w.get('input[type="search"]').setValue('查看用户')
    expect(w.find('input[value="users.read"]').exists()).toBe(true)
    expect(w.find('input[value="roles.read"]').exists()).toBe(false)
    await w.get('input[type="search"]').setValue('roles.read')
    expect(w.find('input[value="roles.read"]').exists()).toBe(true)
  })

  it('updates permission labels when the interface language changes', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: {} })
    const w = mount(PermissionPicker, { props: { permissions, modelValue: [] }, global: { plugins: [i18n] } })
    expect(w.text()).toContain('Read users')
    i18n.global.locale.value = 'zh'
    await flushPromises()
    expect(w.text()).toContain('查看用户')
    expect(w.text()).not.toContain('Read users')
  })

  it('localizes the built-in role name and description in the roles table', async () => {
    mocks.api.listRoles.mockResolvedValue([superRole()])
    const w = mountUI(RolesView, {}, 'zh')
    await flushPromises()
    expect(w.text()).toContain('超级管理员')
    expect(w.text()).toContain('内置角色，拥有全部后台管理权限')
    expect(w.text()).not.toContain('Super Administrator')
    await w.get('input[type="search"]').setValue('超级管理员')
    expect(w.text()).toContain('超级管理员')
    expect(w.text()).toContain('super_admin')
  })

  it('localizes role names and supports Chinese search in user role assignment', async () => {
    mocks.api.listRoles.mockResolvedValue([superRole(), role(2, { name: 'Support Team' })])
    mocks.api.getUserRoles.mockResolvedValue([])
    const w = mountUI(UserRolesDialog, { show: true, user: { id: 9, email: 'target@example.com' } }, 'zh')
    await flushPromises()
    expect(w.text()).toContain('超级管理员')
    expect(w.text()).toContain('Support Team')
    await w.get('input[type="search"]').setValue('超级管理员')
    expect(w.find('input[value="10"]').exists()).toBe(true)
    expect(w.find('input[value="2"]').exists()).toBe(false)
  })

  it('localizes the role-permissions dialog title', async () => {
    const w = mountUI(RolePermissionsDialog, { show: true, role: superRole() }, 'zh')
    await flushPromises()
    expect(w.get('.modal-title').text()).toContain('超级管理员')
    expect(w.get('.modal-title').text()).not.toContain('Super Administrator')
  })

  it('submits the same permission codes after selecting Chinese-labelled permissions', async () => {
    grant('roles.permissions.assign')
    const w = mountUI(RolePermissionsDialog, { show: true, role: role() }, 'zh')
    await flushPromises()
    expect(w.text()).toContain('查看用户')
    await w.get('input[value="users.read"]').setValue(false)
    await w.get('input[value="roles.read"]').setValue(true)
    await w.get('[data-testid="save-permissions"]').trigger('click')
    await flushPromises()
    expect(mocks.api.setRolePermissions).toHaveBeenCalledWith(1, ['roles.read'])
  })
})
