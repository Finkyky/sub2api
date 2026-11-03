import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { createI18n } from 'vue-i18n'
import UserEditModal from '../UserEditModal.vue'
import type { AdminUser } from '@/types'

const m = vi.hoisted(() => ({ update: vi.fn(), listRoles: vi.fn(), getUserRoles: vi.fn(), setUserRoles: vi.fn(), showSuccess: vi.fn(), showError: vi.fn(), refreshUser: vi.fn() }))
let auth: any
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => m }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: (fn: () => Promise<unknown>) => fn() }), isStepUpBlocked: () => false, isStepUpCancelled: () => false, stepUpBlockReason: () => '' }))
vi.mock('@/api/admin', () => ({ adminAPI: { users: { update: m.update } } }))
vi.mock('@/api/admin/rbac', () => ({ rbacAPI: { listRoles: m.listRoles, getUserRoles: m.getUserRoles, setUserRoles: m.setUserRoles } }))
const role = (id: number, name: string, permissions = ['users.read'], extra = {}) => ({ id, name, code: `role_${id}`, description: '', permissions, is_system: false, is_super_admin: false, ...extra })
const roles = [role(11, '运营人员'), role(12, '审计人员', ['audit_logs.read']), role(13, '高级管理员', ['roles.delete'])]
const target = (id = 7, extra = {}) => ({ id, email: 'user@example.test', username: 'User', notes: '', role: 'user', concurrency: 0, rpm_limit: 0, roles: [], ...extra } as unknown as AdminUser)
const render = (props = {}) => mount(UserEditModal, {
  props: { show: true, user: target(), ...props },
  global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: {}, missingWarn: false, fallbackWarn: false })], stubs: {
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' },
    UserAttributeForm: true, TotpStepUpDialog: true, Icon: true
  } }
})
const selectRole = async (w: any, id: number, selected: boolean) => w.get(`[data-test="user-role-${id}"]`).setValue(selected)
const submit = async (w: any) => { await w.get('form').trigger('submit'); await flushPromises() }
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.resetAllMocks()
  auth = reactive({ isSuperAdmin: false, user: { id: 99 }, permissions: ['users.read', 'users.update', 'roles.read', 'users.roles.assign', 'audit_logs.read'],
    hasPermission: (code: string) => auth.isSuperAdmin || auth.permissions.includes(code), refreshUser: m.refreshUser })
  m.update.mockResolvedValue({})
  m.listRoles.mockResolvedValue(roles)
  m.getUserRoles.mockResolvedValue([roles[0]])
  m.setUserRoles.mockResolvedValue(undefined)
})

describe('edit user dynamic RBAC roles', () => {
  it('loads actual roles, checks current assignments, and replaces the two legacy choices', async () => {
    const w = render(); await flushPromises()
    expect(m.listRoles).toHaveBeenCalledOnce()
    expect(m.getUserRoles).toHaveBeenCalledWith(7)
    expect(w.findAll('[data-test^="user-role-"][type="checkbox"]')).toHaveLength(3)
    expect(w.get<HTMLInputElement>('[data-test="user-role-11"]').element.checked).toBe(true)
    expect(w.text()).toContain('运营人员')
    expect(w.text()).toContain('审计人员')
    expect(w.get<HTMLInputElement>('[data-test="user-role-13"]').element.disabled).toBe(true)
    expect(w.find('option[value="user"]').exists()).toBe(false)
    expect(w.find('option[value="admin"]').exists()).toBe(false)
    await w.get('[data-test="user-role-selector"] input[type="search"]').setValue('审计')
    expect(w.find('[data-test="user-role-12"]').exists()).toBe(true)
    expect(w.find('[data-test="user-role-11"]').exists()).toBe(false)
  })

  it('saves multiple role IDs without submitting a legacy role string', async () => {
    const w = render(); await flushPromises()
    await selectRole(w, 12, true); await submit(w)
    expect(m.setUserRoles).toHaveBeenCalledWith(7, [11, 12])
    expect(m.update.mock.calls[0][1]).not.toHaveProperty('role')
    expect(w.emitted('close')).toBeTruthy()
  })

  it('explicitly clears assignments with an empty role ID array', async () => {
    const w = render(); await flushPromises()
    await selectRole(w, 11, false); await submit(w)
    expect(m.setUserRoles).toHaveBeenCalledWith(7, [])
  })

  it('does not repeat an unchanged role assignment when editing basic fields', async () => {
    const w = render(); await flushPromises(); await submit(w)
    expect(m.update).toHaveBeenCalledOnce()
    expect(m.setUserRoles).not.toHaveBeenCalled()
  })

  it('shows assigned roles read-only without assignment permission', async () => {
    auth.permissions = auth.permissions.filter((p: string) => p !== 'users.roles.assign')
    const w = render(); await flushPromises()
    expect(w.get<HTMLInputElement>('[data-test="user-role-11"]').element.disabled).toBe(true)
    await submit(w)
    expect(m.update).toHaveBeenCalledOnce()
    expect(m.setUserRoles).not.toHaveBeenCalled()
  })

  it('does not fetch the role catalog without roles.read', async () => {
    auth.permissions = ['users.read', 'users.update']
    const w = render(); await flushPromises()
    expect(m.listRoles).not.toHaveBeenCalled()
    expect(m.getUserRoles).toHaveBeenCalledWith(7)
    expect(w.text()).toContain('运营人员')
    expect(w.find('[data-test="user-role-12"]').exists()).toBe(false)
  })

  it('protects administrator targets even when the editor is a super administrator', async () => {
    auth.isSuperAdmin = true
    const w = render({ user: target(7, { role: 'admin' }) }); await flushPromises()
    expect(w.get<HTMLInputElement>('[data-test="user-role-11"]').element.disabled).toBe(true)
    await submit(w)
    expect(m.setUserRoles).not.toHaveBeenCalled()
    expect(m.update.mock.calls[0][1]).not.toHaveProperty('role')
  })

  it('keeps basic editing available when role loading fails and offers retry', async () => {
    m.getUserRoles.mockRejectedValueOnce(new Error('offline'))
    const w = render(); await flushPromises()
    expect(w.find('[data-test="retry-user-roles"]').exists()).toBe(true)
    await submit(w)
    expect(m.update).toHaveBeenCalledOnce()
    expect(m.setUserRoles).not.toHaveBeenCalled()
  })

  it('retains role changes after partial save failure and retries without repeating basic updates', async () => {
    m.setUserRoles.mockRejectedValueOnce(new Error('offline'))
    const w = render(); await flushPromises()
    await selectRole(w, 12, true); await submit(w)
    expect(w.emitted('close')).toBeFalsy()
    expect(w.get<HTMLInputElement>('[data-test="user-role-12"]').element.checked).toBe(true)
    expect(m.showError).toHaveBeenCalled()
    await submit(w)
    expect(m.update).toHaveBeenCalledOnce()
    expect(m.setUserRoles).toHaveBeenCalledTimes(2)
    expect(w.emitted('close')).toBeTruthy()
  })

  it('rechecks assignment authority before saving a draft', async () => {
    const w = render(); await flushPromises(); await selectRole(w, 12, true)
    auth.permissions = auth.permissions.filter((p: string) => p !== 'users.roles.assign')
    await submit(w)
    expect(m.setUserRoles).not.toHaveBeenCalled()
    expect(m.update).not.toHaveBeenCalled()
    expect(w.emitted('close')).toBeFalsy()
  })

  it('keeps selected roles across an equivalent current-user refresh', async () => {
    const w = render(); await flushPromises(); await selectRole(w, 12, true)
    auth.user = { ...auth.user }; await flushPromises()
    expect(w.get<HTMLInputElement>('[data-test="user-role-12"]').element.checked).toBe(true)
    expect(m.getUserRoles).toHaveBeenCalledOnce()
  })

  it('ignores late role data from a previous edit target', async () => {
    let resolveOld!: (value: typeof roles) => void
    m.getUserRoles.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    m.getUserRoles.mockResolvedValueOnce([roles[1]])
    const w = render(); await flushPromises()
    await w.setProps({ user: target(8) }); await flushPromises()
    expect(resolveOld).toBeTypeOf('function')
    resolveOld([roles[0]]); await flushPromises()
    expect(w.get<HTMLInputElement>('[data-test="user-role-12"]').element.checked).toBe(true)
    expect(w.get<HTMLInputElement>('[data-test="user-role-11"]').element.checked).toBe(false)
  })

  it('does not save roles or mark a new target as saved after an old basic update completes', async () => {
    let finishUpdate!: (value: object) => void
    m.update.mockImplementationOnce(() => new Promise(resolve => { finishUpdate = resolve }))
    const w = render(); await flushPromises(); await selectRole(w, 12, true)
    await w.get('form').trigger('submit'); await flushPromises()
    await w.setProps({ user: target(8) }); await flushPromises()
    finishUpdate({}); await flushPromises()
    expect(m.setUserRoles).not.toHaveBeenCalled()
    expect(w.emitted('close')).toBeFalsy()
    await submit(w)
    expect(m.update).toHaveBeenCalledTimes(2)
    expect(m.update.mock.calls[1][0]).toBe(8)
  })

})
