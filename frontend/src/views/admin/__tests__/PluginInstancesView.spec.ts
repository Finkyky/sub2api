import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive, nextTick } from 'vue'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import PluginInstancesView from '../PluginInstancesView.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), get: vi.fn(), revisions: vi.fn(), status: vi.fn(), create: vi.fn(),
  saveConfig: vi.fn(), publish: vi.fn(), disable: vi.fn(), delete: vi.fn(), replaceBindings: vi.fn(),
  groups: vi.fn(), run: vi.fn(), success: vi.fn(), realStepUp: false,
  controller: null as ReturnType<typeof import('@/composables/useStepUp')['useStepUp']> | null
}))
let permissions: string[]
let auth: { permissions: string[]; hasPermission: (key: string) => boolean }
vi.mock('@/api/admin', () => ({ adminAPI: { pluginInstances: mocks, groups: { getAllIncludingInactive: mocks.groups } } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.success }) }))
vi.mock('@/composables/useStepUp', async importOriginal => {
  const actual = await importOriginal<typeof import('@/composables/useStepUp')>()
  return {
    ...actual,
    useStepUp: () => {
      if (mocks.realStepUp) { mocks.controller = actual.useStepUp(); return mocks.controller }
      return { run: mocks.run, onCancel: vi.fn() }
    },
    isStepUpCancelled: (e: unknown) => actual.isStepUpCancelled(e) || (e as { cancelled?: boolean })?.cancelled === true,
    isStepUpBlocked: (e: unknown) => (e as { code?: string })?.code === 'STEP_UP_TOTP_NOT_ENABLED',
    stepUpBlockReason: () => 'STEP_UP_TOTP_NOT_ENABLED'
  }
})
vi.mock('vue-i18n', async (importOriginal) => ({ ...(await importOriginal<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))
const instance = { id: 7, plugin_id: 3, name: 'Checker', generation: 4, current_revision: 1, latest_revision: 2, enabled: false }
const manifest = { general: { config_version: 1, hooks: [
  { name: 'check', stages: ['input'], protocols: ['openai.chat'], can_replace: false },
  { name: 'redact', stages: ['output'], protocols: ['openai.chat', 'anthropic.messages'], can_replace: true }
] } }
const binding = { instance_id: 7, group_id: 12, stage: 'input', entry: 'check', protocol: 'openai.chat', sort_order: 0, enabled: true, can_replace: false }
function detail(generation = 4) { return { instance: { ...instance, generation }, revision: { instance_id: 7, revision: 2, config_version: 1 }, manifest, bindings: [{ ...binding }] } }
const status = { instance_id: 7, enabled: false, current_revision: 1, runtime_present: true, lease_refs: 2, retired: true, closed: false, last_error: '', runtimes: [{ revision: 1, lease_refs: 2, local_current: false, retired: true, closed: false }] }
const wrappers: VueWrapper[] = []
function mountView() {
  const wrapper = mount(PluginInstancesView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show', 'title'], template: '<section v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>' },
    PluginSectionsNav: true,
    TotpStepUpDialog: true
  } } }); wrappers.push(wrapper); return wrapper
}
async function open(wrapper: VueWrapper) {
  await flushPromises()
  const button = wrapper.findAll('button').find(b => b.text() === 'Details' || b.text() === 'admin.pluginInstances.details')
  expect(button).toBeDefined(); await button!.trigger('click'); await flushPromises()
}
beforeEach(() => {
  vi.resetAllMocks(); mocks.realStepUp = false; mocks.controller = null
  permissions = ['read', 'create', 'update', 'publish', 'disable', 'delete', 'bindings.update'].map(p => `plugin_instances.${p}`)
  auth = reactive({ permissions, hasPermission: (key: string) => auth.permissions.includes(key) })
  mocks.run.mockImplementation((action: () => Promise<unknown>) => action())
  mocks.list.mockResolvedValue([{ ...instance }]); mocks.get.mockResolvedValue(detail())
  mocks.revisions.mockResolvedValue([{ instance_id: 7, revision: 1, config_version: 1 }, { instance_id: 7, revision: 2, config_version: 1 }])
  mocks.status.mockResolvedValue(status); mocks.groups.mockResolvedValue([{ id: 12, name: 'Group twelve' }])
  mocks.saveConfig.mockResolvedValue(detail(5)); mocks.replaceBindings.mockResolvedValue(detail(5))
  mocks.publish.mockResolvedValue(detail(5)); mocks.disable.mockResolvedValue(detail(5))
  mocks.create.mockResolvedValue(detail()); mocks.delete.mockResolvedValue({ deleted: true })
})
afterEach(() => wrappers.splice(0).forEach(w => w.unmount()))

describe('plugin instance management', () => {
  it('loads read-only node status without exposing arbitrary metadata', async () => {
    mocks.get.mockResolvedValue({ ...detail(), config: { password: 'SERVER_SECRET' } })
    const wrapper = mountView(); await open(wrapper)
    expect(mocks.status).toHaveBeenCalledWith(7)
    expect(wrapper.find('[data-testid="status-panel"]').text()).toContain('2')
    expect(wrapper.text()).not.toContain('SERVER_SECRET')
  })
  it('only reads with read permission and hides every write editor', async () => {
    auth.permissions = ['plugin_instances.read']
    const wrapper = mountView(); await open(wrapper)
    for (const key of ['create-instance', 'save-config', 'save-bindings', 'delete-instance', 'publish-revision']) expect(wrapper.find(`[data-testid="${key}"]`).exists()).toBe(false)
    expect(wrapper.find('[data-testid="config-json"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="binding-group"]').attributes('disabled')).toBeDefined()
    expect(mocks.groups).not.toHaveBeenCalled()
  })
  it('saves a complete write-only configuration as a new revision with step-up and generation', async () => {
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="config-json"]').setValue('{"token":"NEW_SECRET"}')
    await wrapper.get('[data-testid="save-config"]').trigger('click'); await flushPromises()
    expect(mocks.run).toHaveBeenCalledTimes(1)
    expect(mocks.saveConfig).toHaveBeenCalledWith(7, { expected_generation: 4, config: { token: 'NEW_SECRET' } })
    expect((wrapper.get('[data-testid="config-json"]').element as HTMLTextAreaElement).value).toBe('')
    expect(wrapper.text()).not.toContain('NEW_SECRET')
    expect(mocks.revisions).toHaveBeenCalledTimes(2)
  })
  it.each(['[]', 'null', '{bad', '', '"hello"'])('rejects invalid configuration %j locally', async value => {
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="config-json"]').setValue(value)
    await wrapper.get('[data-testid="save-config"]').trigger('click'); await flushPromises()
    expect(mocks.saveConfig).not.toHaveBeenCalled()
    expect(wrapper.findAll('[role="alert"]').map(e => e.text()).join()).toContain('admin.pluginInstances.configInvalid')
  })
  it('uses manifest stage/protocol/replacement declarations in structured bindings', async () => {
    const wrapper = mountView(); await open(wrapper)
    expect(wrapper.find('[data-testid="bindings-json"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="binding-stage"]').findAll('option').map(o => o.attributes('value'))).toEqual(['input'])
    expect(wrapper.get('[data-testid="binding-replace"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="binding-entry"]').setValue('redact')
    expect(wrapper.get('[data-testid="binding-stage"]').findAll('option').map(o => o.attributes('value'))).toEqual(['output'])
    await wrapper.get('[data-testid="binding-protocol"]').setValue('anthropic.messages')
    await wrapper.get('[data-testid="binding-replace"]').setValue(true)
    await wrapper.get('[data-testid="binding-order"]').setValue(8)
    await wrapper.get('[data-testid="save-bindings"]').trigger('click'); await flushPromises()
    expect(mocks.replaceBindings).toHaveBeenCalledWith(7, { expected_generation: 4, bindings: [{ ...binding, stage: 'output', entry: 'redact', protocol: 'anthropic.messages', can_replace: true, sort_order: 8 }] })
    expect(mocks.run).toHaveBeenCalledTimes(1)
  })
  it('loads group choices only with groups.read', async () => {
    auth.permissions.push('groups.read'); const wrapper = mountView(); await open(wrapper)
    expect(mocks.groups).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="binding-group"]').text()).toContain('Group twelve')
  })
  it('preserves conflict drafts and only refreshes metadata on explicit refresh', async () => {
    mocks.saveConfig.mockRejectedValue({ status: 409, message: 'PRIVATE_CONFIG' })
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="config-json"]').setValue('{"a":1}')
    await wrapper.get('[data-testid="save-config"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('admin.pluginInstances.conflict')
    expect(wrapper.text()).not.toContain('PRIVATE_CONFIG')
    expect(mocks.get).toHaveBeenCalledTimes(1)
    expect((wrapper.get('[data-testid="config-json"]').element as HTMLTextAreaElement).value).toBe('{"a":1}')
    mocks.get.mockResolvedValue(detail(9))
    await wrapper.get('[data-testid="refresh-detail"]').trigger('click'); await flushPromises()
    expect(mocks.get).toHaveBeenCalledTimes(2)
    expect(mocks.saveConfig).toHaveBeenCalledTimes(1)
    expect((wrapper.get('[data-testid="config-json"]').element as HTMLTextAreaElement).value).toBe('')
  })
  it('confirms deletion and sends generation through step-up', async () => {
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="delete-instance"]').trigger('click'); await flushPromises()
    expect(mocks.delete).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm-delete"]').trigger('click'); await flushPromises()
    expect(mocks.delete).toHaveBeenCalledWith(7, 4)
    expect(mocks.run).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="status-panel"]').exists()).toBe(false)
  })
  it('keeps busy-delete conflicts visible without retry', async () => {
    mocks.delete.mockRejectedValue({ status: 409 })
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="delete-instance"]').trigger('click')
    await wrapper.get('[data-testid="confirm-delete"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('admin.pluginInstances.conflict')
    expect(mocks.delete).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="status-panel"]').exists()).toBe(true)
  })
  it('checks permissions again inside step-up retry', async () => {
    mocks.run.mockImplementation(async (action: () => Promise<unknown>) => { auth.permissions = ['plugin_instances.read']; return action() })
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="config-json"]').setValue('{"a":1}')
    await wrapper.get('[data-testid="save-config"]').trigger('click'); await flushPromises()
    expect(mocks.saveConfig).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="config-json"]').exists()).toBe(false)
  })
  it('drops selected metadata and late responses when read permission is revoked', async () => {
    let resolve!: (value: unknown) => void
    mocks.get.mockImplementation(() => new Promise(r => { resolve = r }))
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="instance-details"]').trigger('click')
    auth.permissions = []; await nextTick(); resolve(detail()); await flushPromises()
    expect(wrapper.find('[data-testid="status-panel"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Checker')
  })
  it('clears write-only drafts on close/reopen', async () => {
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="config-json"]').setValue('{"password":"DRAFT_SECRET"}')
    await wrapper.get('[data-testid="close-detail"]').trigger('click'); await open(wrapper)
    expect((wrapper.get('[data-testid="config-json"]').element as HTMLTextAreaElement).value).toBe('')
  })
  it('publishes through step-up and does not republish an already enabled current revision', async () => {
    mocks.get.mockResolvedValue({ ...detail(), instance: { ...instance, enabled: true } })
    const wrapper = mountView(); await open(wrapper)
    const buttons = wrapper.findAll('[data-testid="publish-revision"]')
    expect(buttons[0].attributes('disabled')).toBeDefined()
    await buttons[1].trigger('click'); await flushPromises()
    expect(mocks.publish).toHaveBeenCalledWith(7, { expected_generation: 4, revision: 2 })
    expect(mocks.run).toHaveBeenCalledTimes(1)
  })
  it('shows row-disable failures even when no detail dialog is open', async () => {
    mocks.list.mockResolvedValue([{ ...instance, enabled: true }]); mocks.disable.mockRejectedValue(new Error('LEAKED_TOKEN'))
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="disable-instance"]').trigger('click'); await flushPromises()
    expect(wrapper.findAll('[role="alert"]').map(e => e.text()).join()).toContain('admin.pluginInstances.requestFailed')
    expect(wrapper.text()).not.toContain('LEAKED_TOKEN')
    expect(mocks.run).toHaveBeenCalledTimes(1)
  })
  it('creates through step-up, validates the installation ID/name and clears its configuration', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="create-instance"]').trigger('click')
    await wrapper.get('[data-testid="submit-create"]').trigger('click'); await flushPromises()
    expect(mocks.create).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="create-plugin-id"]').setValue(3)
    await wrapper.get('[data-testid="create-name"]').setValue('  New checker  ')
    await wrapper.get('[data-testid="create-config"] [data-testid="config-json"]').setValue('{"a":1}')
    await wrapper.get('[data-testid="submit-create"]').trigger('click'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith({ plugin_id: 3, name: 'New checker', config: { a: 1 } })
    expect(mocks.run).toHaveBeenCalledTimes(1)
  })
  it('renders a localized step-up blocked error and treats cancellation as no mutation', async () => {
    mocks.run.mockRejectedValue({ code: 'STEP_UP_TOTP_NOT_ENABLED' })
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="save-bindings"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('stepUp.notEnabled')
    expect(mocks.replaceBindings).not.toHaveBeenCalled()
    mocks.run.mockRejectedValue({ cancelled: true })
    await wrapper.get('[data-testid="save-bindings"]').trigger('click'); await flushPromises()
    expect(mocks.replaceBindings).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('admin.pluginInstances.requestFailed')
  })
  it('allows republishing the current revision to re-enable a disabled instance', async () => {
    const wrapper = mountView(); await open(wrapper)
    const button = wrapper.findAll('[data-testid="publish-revision"]')[0]
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click'); await flushPromises()
    expect(mocks.publish).toHaveBeenCalledWith(7, { expected_generation: 4, revision: 1 })
  })
  it('hides writes until refreshed metadata is loaded and prevents writes against the old generation', async () => {
    const wrapper = mountView(); await open(wrapper)
    let resolve!: (value: unknown) => void
    mocks.get.mockImplementation(() => new Promise(r => { resolve = r }))
    await wrapper.get('[data-testid="refresh-detail"]').trigger('click')
    expect(wrapper.get('[data-testid="save-bindings"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="save-bindings"]').trigger('click'); await flushPromises()
    expect(mocks.replaceBindings).not.toHaveBeenCalled()
    resolve(detail(9)); await flushPromises()
  })
  it('does not revive an old runtime observation after the detail dialog is closed', async () => {
    let resolve!: (value: unknown) => void
    mocks.status.mockImplementation(() => new Promise(r => { resolve = r }))
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="close-detail"]').trigger('click')
    resolve(status); await flushPromises()
    expect(wrapper.find('[data-testid="status-panel"]').exists()).toBe(false)
  })
  it('handles read errors independently and localizes known last-error codes without leaking unknown messages', async () => {
    mocks.status.mockRejectedValue({ message: 'SECRET_STATUS' })
    const wrapper = mountView(); await open(wrapper)
    expect(wrapper.find('[data-testid="status-panel"]').text()).toContain('admin.pluginInstances.requestFailed')
    expect(wrapper.findAll('[data-testid="publish-revision"]')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('SECRET_STATUS')
    mocks.status.mockResolvedValue({ ...status, last_error: 'runtime_start_failed' })
    await wrapper.get('[data-testid="refresh-detail"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="status-panel"]').text()).toContain('admin.pluginInstances.runtimeStartFailed')
  })
  it('does not call read endpoints when initially unauthorized', async () => {
    auth.permissions = []; const wrapper = mountView(); await flushPromises()
    expect(mocks.list).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.pluginInstances.readDenied')
  })
  it('resets binding drafts and clears configuration when write permissions are revoked', async () => {
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="config-json"]').setValue('{"password":"secret"}')
    await wrapper.get('[data-testid="binding-order"]').setValue(8)
    auth.permissions = ['plugin_instances.read']; await nextTick()
    expect(wrapper.find('[data-testid="config-json"]').exists()).toBe(false)
    expect((wrapper.get('[data-testid="binding-order"]').element as HTMLInputElement).value).toBe('0')
    auth.permissions.push('plugin_instances.update'); await nextTick()
    expect((wrapper.get('[data-testid="config-json"]').element as HTMLTextAreaElement).value).toBe('')
  })
  it('reports invalid structured bindings locally without a write request', async () => {
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="binding-group"]').setValue(-1)
    await wrapper.get('[data-testid="save-bindings"]').trigger('click'); await flushPromises()
    expect(mocks.replaceBindings).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.pluginInstances.bindingsInvalid')
  })
  it('reports group lookup failures with the manual ID fallback', async () => {
    auth.permissions.push('groups.read'); mocks.groups.mockRejectedValue(new Error('SECRET'))
    const wrapper = mountView(); await open(wrapper)
    expect(wrapper.get('[data-testid="binding-group"]').attributes('type')).toBe('number')
    expect(wrapper.text()).toContain('admin.pluginInstances.groupsFailed')
    expect(wrapper.text()).not.toContain('SECRET')
  })

  it.each(['update', 'bindings.update'])('does not revive a pending %s step-up after revoke and regrant', async permission => {
    let resume!: () => void
    const gate = new Promise<void>(resolve => { resume = resolve })
    const api = permission === 'update' ? mocks.saveConfig : mocks.replaceBindings
    api.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    mocks.run.mockImplementation(async (action: () => Promise<unknown>) => {
      try { return await action() } catch { await gate; return action() }
    })
    const wrapper = mountView(); await open(wrapper)
    if (permission === 'update') await wrapper.get('[data-testid="config-json"]').setValue('{"password":"CLEARED_DRAFT"}')
    else await wrapper.get('[data-testid="binding-order"]').setValue(8)
    await wrapper.get(permission === 'update' ? '[data-testid="save-config"]' : '[data-testid="save-bindings"]').trigger('click'); await flushPromises()
    expect(api).toHaveBeenCalledTimes(1)
    auth.permissions = auth.permissions.filter(p => p !== `plugin_instances.${permission}`); await nextTick()
    auth.permissions.push(`plugin_instances.${permission}`); await nextTick()
    resume(); await flushPromises()
    expect(api).toHaveBeenCalledTimes(1)
  })
  it('invalidates the create continuation across read revocation and regrant', async () => {
    let resolve!: (value: unknown) => void
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="create-instance"]').trigger('click')
    await wrapper.get('[data-testid="create-plugin-id"]').setValue(3)
    await wrapper.get('[data-testid="create-name"]').setValue('New checker')
    await wrapper.get('[data-testid="create-config"] [data-testid="config-json"]').setValue('{}')
    mocks.list.mockImplementationOnce(() => new Promise(r => { resolve = r }))
    await wrapper.get('[data-testid="submit-create"]').trigger('click'); await flushPromises()
    auth.permissions = []; await nextTick()
    auth.permissions = permissions; await nextTick(); await flushPromises()
    resolve([instance]); await flushPromises()
    expect(mocks.get).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="status-panel"]').exists()).toBe(false)
  })

  it.each(['update', 'bindings.update', 'create', 'delete'])('cancels a late STEP_UP_REQUIRED for an invalidated %s request using the real controller', async permission => {
    mocks.realStepUp = true
    let reject!: (value: unknown) => void
    const api = permission === 'update' ? mocks.saveConfig : permission === 'bindings.update' ? mocks.replaceBindings : permission === 'create' ? mocks.create : mocks.delete
    api.mockImplementationOnce(() => new Promise((_, r) => { reject = r }))
    const wrapper = mountView(); await open(wrapper)
    if (permission === 'create') {
      await wrapper.get('[data-testid="create-instance"]').trigger('click')
      await wrapper.get('[data-testid="create-plugin-id"]').setValue(3)
      await wrapper.get('[data-testid="create-name"]').setValue('New instance')
      await wrapper.get('[data-testid="create-config"] [data-testid="config-json"]').setValue('{}')
      await wrapper.get('[data-testid="submit-create"]').trigger('click')
    } else if (permission === 'delete') {
      await wrapper.get('[data-testid="delete-instance"]').trigger('click')
      await wrapper.get('[data-testid="confirm-delete"]').trigger('click')
    } else {
      if (permission === 'update') await wrapper.get('[data-testid="config-json"]').setValue('{}')
      await wrapper.get(permission === 'update' ? '[data-testid="save-config"]' : '[data-testid="save-bindings"]').trigger('click')
    }
    expect(api).toHaveBeenCalledTimes(1)
    auth.permissions = auth.permissions.filter(p => p !== `plugin_instances.${permission}`)
    auth.permissions.push(`plugin_instances.${permission}`)
    reject({ code: 'STEP_UP_REQUIRED' }); await flushPromises()
    expect(mocks.controller!.visible.value).toBe(false)
    expect(wrapper.get('[data-testid="refresh-detail"]').attributes('disabled')).toBeUndefined()
    expect(api).toHaveBeenCalledTimes(1)
  })

  it('still prompts and retries a valid sensitive action with the real step-up controller', async () => {
    mocks.realStepUp = true
    mocks.saveConfig.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }).mockResolvedValueOnce(detail(5))
    const wrapper = mountView(); await open(wrapper)
    await wrapper.get('[data-testid="config-json"]').setValue('{}')
    await wrapper.get('[data-testid="save-config"]').trigger('click'); await flushPromises()
    expect(mocks.controller!.visible.value).toBe(true)
    expect(mocks.saveConfig).toHaveBeenCalledTimes(1)
    mocks.controller!.onVerified(); await flushPromises()
    expect(mocks.controller!.visible.value).toBe(false)
    expect(mocks.saveConfig).toHaveBeenCalledTimes(2)
    expect(mocks.saveConfig).toHaveBeenLastCalledWith(7, { expected_generation: 4, config: {} })
    expect(wrapper.get('[data-testid="refresh-detail"]').attributes('disabled')).toBeUndefined()
  })

})
