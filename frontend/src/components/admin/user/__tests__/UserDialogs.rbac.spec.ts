import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import UserCreateModal from '../UserCreateModal.vue'
import UserEditModal from '../UserEditModal.vue'
import UserApiKeysModal from '../UserApiKeysModal.vue'
import UserAllowedGroupsModal from '../UserAllowedGroupsModal.vue'
import UserPlatformQuotaModal from '../UserPlatformQuotaModal.vue'
import UserBalanceModal from '../UserBalanceModal.vue'
import UserBalanceHistoryModal from '../UserBalanceHistoryModal.vue'
import BulkEditUserModal from '../BulkEditUserModal.vue'
import GroupReplaceModal from '../GroupReplaceModal.vue'
import UserAttributeForm from '@/components/user/UserAttributeForm.vue'
import UserAttributesConfigModal from '@/components/user/UserAttributesConfigModal.vue'

const m = vi.hoisted(() => ({
  listRoles: vi.fn(), getUserRoles: vi.fn(), setUserRoles: vi.fn(),
  create: vi.fn(), update: vi.fn(), getUserApiKeys: vi.fn(), getAllGroups: vi.fn(), listGroups: vi.fn(), updateApiKeyGroup: vi.fn(),
  listEnabledDefinitions: vi.fn(), listDefinitions: vi.fn(), createDefinition: vi.fn(), updateDefinition: vi.fn(), deleteDefinition: vi.fn(), getUserAttributeValues: vi.fn(), updateUserAttributeValues: vi.fn(),
  getPlatformQuotas: vi.fn(), updatePlatformQuotas: vi.fn(), resetPlatformQuotaWindow: vi.fn(), updateBalance: vi.fn(),
  getUserBalanceHistory: vi.fn(), batchUpdateLimits: vi.fn(), replaceGroup: vi.fn(), showError: vi.fn(), showSuccess: vi.fn()
}))
let auth: any
vi.mock('@/api/admin/rbac', () => ({ rbacAPI: { listRoles: m.listRoles, getUserRoles: m.getUserRoles, setUserRoles: m.setUserRoles } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => m }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: (fn: () => Promise<unknown>) => fn() }),
  isStepUpBlocked: () => false, isStepUpCancelled: () => false, stepUpBlockReason: () => ''
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key })
}))
vi.mock('@/api/admin', () => ({ adminAPI: {
  users: { create: m.create, update: m.update, getUserApiKeys: m.getUserApiKeys, getPlatformQuotas: m.getPlatformQuotas,
    updatePlatformQuotas: m.updatePlatformQuotas, resetPlatformQuotaWindow: m.resetPlatformQuotaWindow,
    updateBalance: m.updateBalance, getUserBalanceHistory: m.getUserBalanceHistory, batchUpdateLimits: m.batchUpdateLimits, replaceGroup: m.replaceGroup },
  groups: { getAll: m.getAllGroups, list: m.listGroups },
  apiKeys: { updateApiKeyGroup: m.updateApiKeyGroup },
  userAttributes: { createDefinition: m.createDefinition, updateDefinition: m.updateDefinition, deleteDefinition: m.deleteDefinition, listEnabledDefinitions: m.listEnabledDefinitions, listDefinitions: m.listDefinitions,
    getUserAttributeValues: m.getUserAttributeValues, updateUserAttributeValues: m.updateUserAttributeValues }
} }))
const user = { id: 7, email: 'user@example.test', username: 'User', notes: '', role: 'user', concurrency: 0, rpm_limit: 0,
  balance: 20, allowed_groups: [4], group_rates: {}, subscriptions: [] }
const key = { id: 5, name: 'API key', key: 'sk-test-1234567890123456789012345678', status: 'active', group_id: null, created_at: '2026-09-29T00:00:00Z' }
const stubs = {
  BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' },
  Select: { props: ['options', 'modelValue', 'disabled'], emits: ['update:modelValue'], template: '<select :disabled="disabled" :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option></select>' },
  Icon: true, TotpStepUpDialog: true, GroupBadge: true, GroupOptionItem: true, PlatformIcon: true, ConfirmDialog: true, teleport: true
}
const ui = (component: any, props = {}) => {
  const defaults = component === UserAttributeForm ? {} : { show: true }
  const target = [UserCreateModal, BulkEditUserModal, UserAttributeForm, UserAttributesConfigModal].includes(component) ? {} : { user }
  return mount(component, { props: { ...defaults, ...target, ...props }, global: { stubs } })
}
const state = (w: any) => w.vm.$.setupState
const permit = (...codes: string[]) => auth.permissions.push(...codes)
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.resetAllMocks()
  m.listRoles.mockResolvedValue([]); m.getUserRoles.mockResolvedValue([]); m.setUserRoles.mockResolvedValue(undefined)
  auth = reactive({ isSuperAdmin: false, permissions: ['users.read', 'users.update', 'users.create'],
    hasPermission: (code: string) => auth.isSuperAdmin || auth.permissions.includes(code) })
  m.create.mockResolvedValue(user); m.update.mockResolvedValue(user)
  m.getUserApiKeys.mockResolvedValue({ items: [key] })
  m.getAllGroups.mockResolvedValue([]); m.listGroups.mockResolvedValue({ items: [] })
  m.listEnabledDefinitions.mockResolvedValue([]); m.listDefinitions.mockResolvedValue([])
  m.getUserAttributeValues.mockResolvedValue([])
  m.getPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
  m.getUserBalanceHistory.mockResolvedValue({ items: [], total: 0 })
})

describe('user create/edit authority and legacy role', () => {
  it('hides legacy admin from ordinary create and scrubs a stale admin selection at submit', async () => {
    const w = ui(UserCreateModal)
    expect(w.find('option[value="admin"]').exists()).toBe(false)
    state(w).form.role = 'admin'
    state(w).form.email = user.email; state(w).form.password = 'long-test-password'
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.create).toHaveBeenCalledWith(expect.objectContaining({ role: 'user' }))
  })
  it('allows super creation but rechecks authority after revocation', async () => {
    auth.isSuperAdmin = true
    const w = ui(UserCreateModal)
    expect(w.find('option[value="admin"]').exists()).toBe(true)
    state(w).form.role = 'admin'; state(w).form.email = user.email; state(w).form.password = 'test-pass'
    auth.isSuperAdmin = false
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.create.mock.calls[0][0].role).toBe('user')
  })
  it('does not submit creation without users.create, even via synthetic submit', async () => {
    auth.permissions = []
    const w = ui(UserCreateModal)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.create).not.toHaveBeenCalled()
  })
  it('edits basic fields without group or attribute read access and never sends admin', async () => {
    const w = ui(UserEditModal)
    await flushPromises()
    expect(w.find('option[value="admin"]').exists()).toBe(false)
    expect(m.listEnabledDefinitions).not.toHaveBeenCalled()
    expect(m.getUserAttributeValues).not.toHaveBeenCalled()
    expect(m.getAllGroups).not.toHaveBeenCalled()
    state(w).form.role = 'admin'
    state(w).form.username = 'Updated'
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.update).toHaveBeenCalledWith(7, expect.objectContaining({ username: 'Updated', concurrency: 0 }))
    expect(m.update.mock.calls[0][1]).not.toHaveProperty('role')
    expect(w.emitted('success')).toBeTruthy()
  })
  it('allows protected-user basic edits but omits legacy role, email and password for ordinary operators', async () => {
    const w = ui(UserEditModal, { user: { ...user, role: 'admin' } })
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.update).toHaveBeenCalledWith(7, expect.objectContaining({ username: 'User', concurrency: 0 }))
    expect(m.update.mock.calls[0][1]).not.toHaveProperty('role')
    expect(m.update.mock.calls[0][1]).not.toHaveProperty('email')
    expect(m.update.mock.calls[0][1]).not.toHaveProperty('password')
    expect(w.text()).toContain('rbac.protectedUser')
  })
  it('honors the parent ceiling for sensitive fields without blocking basic edits', async () => {
    const w = ui(UserEditModal, { sensitiveAllowed: false })
    state(w).form.email = 'changed@example.test'; state(w).form.password = 'injected-password'
    state(w).form.username = 'Allowed name'
    await w.get('form').trigger('submit'); await flushPromises()
    const payload = m.update.mock.calls[0][1]
    expect(payload.username).toBe('Allowed name')
    expect(payload).not.toHaveProperty('email')
    expect(payload).not.toHaveProperty('password')
  })
  it('does not let even a super actor downgrade a legacy administrator', async () => {
    auth.isSuperAdmin = true
    const w = ui(UserEditModal, { user: { ...user, role: 'admin' } })
    expect(w.find('option[value="user"]').exists()).toBe(false)
    state(w).form.role = 'user'
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.update.mock.calls[0][1]).not.toHaveProperty('role', 'user')
  })
  it('keeps basic editing available when authorized attribute loading fails', async () => {
    permit('user_attributes.read')
    m.listEnabledDefinitions.mockRejectedValue(new Error('offline'))
    const w = ui(UserEditModal)
    await flushPromises()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.update).toHaveBeenCalledOnce()
    expect(m.updateUserAttributeValues).not.toHaveBeenCalled()
  })
  it('denies saving an open editor after update permission is revoked', async () => {
    const w = ui(UserEditModal)
    auth.permissions = ['users.read']
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.update).not.toHaveBeenCalled()
  })
})

describe('ancillary user directories', () => {
  it('allows API key reads without fetching groups or exposing group mutations', async () => {
    permit('api_keys.read')
    const w = ui(UserApiKeysModal)
    await flushPromises()
    expect(m.getUserApiKeys).toHaveBeenCalledWith(7)
    expect(m.getAllGroups).not.toHaveBeenCalled()
    await state(w).changeGroup(key, 4)
    expect(m.updateApiKeyGroup).not.toHaveBeenCalled()
  })
  it('only fetches the group picker for actors with both read and key-update authority', async () => {
    permit('api_keys.read', 'api_keys.update', 'groups.read')
    const w = ui(UserApiKeysModal)
    await flushPromises()
    expect(m.getAllGroups).toHaveBeenCalledOnce()
    auth.permissions = ['api_keys.read', 'groups.read']
    await state(w).changeGroup(key, 4)
    expect(m.updateApiKeyGroup).not.toHaveBeenCalled()
  })
  it('does not fetch or overwrite groups without groups.read', async () => {
    const w = ui(UserAllowedGroupsModal)
    await flushPromises()
    expect(m.listGroups).not.toHaveBeenCalled()
    await state(w).handleSave()
    expect(m.update).not.toHaveBeenCalled()
  })
  it('discards an in-flight group directory response after read access is revoked', async () => {
    permit('groups.read')
    let finish!: (value: unknown) => void
    m.listGroups.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const w = ui(UserAllowedGroupsModal)
    auth.permissions = ['users.read', 'users.update']
    await flushPromises()
    finish({ items: [{ id: 4, name: 'Private', status: 'active', subscription_type: 'standard' }] })
    await flushPromises()
    expect(state(w).groups).toEqual([])
    expect(state(w).loaded).toBe(false)
  })
  it('does not clear user groups when the directory load failed', async () => {
    permit('groups.read')
    m.listGroups.mockRejectedValue(new Error('offline'))
    const w = ui(UserAllowedGroupsModal)
    await flushPromises()
    await state(w).handleSave()
    expect(m.update).not.toHaveBeenCalled()
    expect(w.find('[role="alert"]').exists()).toBe(true)
  })
  it('does not fetch attribute definitions/values when either read permission is absent', async () => {
    const w = ui(UserAttributeForm, { userId: 7, modelValue: {} })
    await flushPromises()
    expect(m.listEnabledDefinitions).not.toHaveBeenCalled()
    expect(m.getUserAttributeValues).not.toHaveBeenCalled()
    auth.permissions = ['user_attributes.read']
    await flushPromises()
    expect(m.getUserAttributeValues).not.toHaveBeenCalled()
    expect(w.find('input').exists()).toBe(false)
  })
  it('reads attribute-management catalog at mount but guards direct mutations', async () => {
    permit('user_attributes.read')
    const w = ui(UserAttributesConfigModal)
    await flushPromises()
    expect(m.listDefinitions).toHaveBeenCalledOnce()
    state(w).form.key = 'key'; state(w).form.name = 'Name'
    await state(w).handleSave()
    state(w).deletingAttribute = { id: 1 }
    await state(w).handleDelete()
    expect(m.createDefinition).not.toHaveBeenCalled()
    expect(m.deleteDefinition).not.toHaveBeenCalled()
  })
  it('tracks dirty attributes rather than resubmitting values read from the server', async () => {
    permit('user_attributes.read')
    m.listEnabledDefinitions.mockResolvedValue([{ id: 1, name: 'Team', type: 'text', enabled: true }])
    m.getUserAttributeValues.mockResolvedValue([{ attribute_id: 1, value: 'old' }])
    const w = ui(UserEditModal)
    await flushPromises()
    expect(state(w).attributesDirty).toBe(false)
    await w.findComponent(UserAttributeForm).get('input').setValue('new')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.updateUserAttributeValues).toHaveBeenCalledWith(7, { 1: 'new' })
  })
  it('reports partial attribute failure and retries without repeating the basic update', async () => {
    permit('user_attributes.read')
    m.listEnabledDefinitions.mockResolvedValue([{ id: 1, name: 'Team', type: 'text', enabled: true }])
    m.getUserAttributeValues.mockResolvedValue([{ attribute_id: 1, value: 'old' }])
    m.updateUserAttributeValues.mockRejectedValueOnce(new Error('offline'))
    const w = ui(UserEditModal)
    await flushPromises()
    await w.findComponent(UserAttributeForm).get('input').setValue('new')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.showError).toHaveBeenCalledWith('rbac.attributesPartialSave')
    expect(w.emitted('close')).toBeFalsy()
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.update).toHaveBeenCalledOnce()
    expect(m.updateUserAttributeValues).toHaveBeenCalledTimes(2)
    expect(w.emitted('close')).toBeTruthy()
  })
  it('only loads attribute-management catalog with user_attributes.read', async () => {
    permit('user_attributes.manage')
    const w = ui(UserAttributesConfigModal, { show: false })
    await w.setProps({ show: true }); await flushPromises()
    expect(m.listDefinitions).not.toHaveBeenCalled()
  })
})

describe('user-only mutations fail closed', () => {
  it('does not save blank quotas after a failed read', async () => {
    m.getPlatformQuotas.mockRejectedValue(new Error('offline'))
    const w = ui(UserPlatformQuotaModal)
    await flushPromises()
    await state(w).onSave()
    expect(m.updatePlatformQuotas).not.toHaveBeenCalled()
  })
  it('keeps quotas read-only without users.update', async () => {
    auth.permissions = ['users.read']
    const w = ui(UserPlatformQuotaModal)
    await flushPromises()
    expect(m.getPlatformQuotas).toHaveBeenCalledOnce()
    await state(w).onSave(); await state(w).onReset('openai', 'daily')
    expect(m.updatePlatformQuotas).not.toHaveBeenCalled()
    expect(m.resetPlatformQuotaWindow).not.toHaveBeenCalled()
  })
  it('blocks balance changes without users.update', async () => {
    auth.permissions = ['users.read']
    const w = ui(UserBalanceModal, { operation: 'add' })
    state(w).form.amount = 1
    await w.get('form').trigger('submit'); await flushPromises()
    expect(m.updateBalance).not.toHaveBeenCalled()
  })
  it('does not read balance history or expose deposit actions without authority', async () => {
    auth.permissions = []
    const w = ui(UserBalanceHistoryModal)
    await state(w).loadHistory(1)
    expect(m.getUserBalanceHistory).not.toHaveBeenCalled()
    expect(w.text()).not.toContain('admin.users.deposit')
  })
  it('blocks bulk changes after users.update is revoked', async () => {
    const w = ui(BulkEditUserModal, { selectedIds: [7] })
    state(w).enableConcurrency = true; state(w).concurrencyValue = 3
    auth.permissions = ['users.read']
    await flushPromises()
    expect(w.get('[data-test="submit"]').attributes('disabled')).toBeDefined()
    await state(w).handleSubmit()
    expect(m.batchUpdateLimits).not.toHaveBeenCalled()
  })
  it('blocks replacing groups without reading the catalog', async () => {
    const w = ui(GroupReplaceModal, { oldGroup: { id: 4, name: 'A' }, allGroups: [] })
    state(w).selectedGroupId = 5
    await state(w).handleReplace()
    expect(m.replaceGroup).not.toHaveBeenCalled()
  })
})
