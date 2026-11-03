import { beforeEach, describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/types'
import { permissionDirective } from '@/directives/permission'
import AccountActions from '@/components/admin/account/AccountTableActions.vue'
import MonitorActions from '@/components/admin/monitor/MonitorActionsCell.vue'
import AccountBulkActions from '@/components/admin/account/AccountBulkActionsBar.vue'

vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
beforeEach(() => { setActivePinia(createPinia()); useAuthStore().user = { permissions: ['accounts.read', 'channels.read'] } as User })
const global = { directives: { permission: permissionDirective }, stubs: { Icon: true } }
describe('production action components under real RBAC', () => {
  it('leaves refresh usable but hides and blocks account creation, then reacts to grant/revoke', async () => {
    const wrapper = mount(AccountActions, { attachTo: document.body, props: { loading: false }, global })
    const [refresh, create] = wrapper.findAll('button')
    expect(refresh.isVisible()).toBe(true)
    expect(create.isVisible()).toBe(false)
    await create.trigger('click'); expect(wrapper.emitted('create')).toBeUndefined()
    const auth = useAuthStore(); auth.user!.permissions = ['accounts.read', 'accounts.create']; await nextTick()
    expect(create.isVisible()).toBe(true)
    await create.trigger('click'); expect(wrapper.emitted('create')).toHaveLength(1)
    auth.user!.permissions = ['accounts.read']; await nextTick()
    expect(create.isVisible()).toBe(false)
    await create.trigger('click'); expect(wrapper.emitted('create')).toHaveLength(1)
    wrapper.unmount()
  })
  it('hides and blocks test, duplicate, edit and delete monitor actions from a reader', async () => {
    const wrapper = mount(MonitorActions, { attachTo: document.body, props: { row: { id: 1 } as any, running: false, duplicating: false }, global })
    for (const button of wrapper.findAll('button')) { expect(button.isVisible()).toBe(false); await button.trigger('click') }
    for (const event of ['run', 'duplicate', 'edit', 'delete']) expect(wrapper.emitted(event)).toBeUndefined()
    wrapper.unmount()
  })
  it('blocks bulk writes even when rows are selected', async () => {
    const wrapper = mount(AccountBulkActions, { attachTo: document.body, props: { selectedIds: [1], totalResults: 1 } as any, global })
    for (const button of wrapper.findAll('button').filter(b => !b.isVisible())) await button.trigger('click')
    for (const event of ['delete', 'reset-status', 'refresh-token', 'probe-upstream-billing', 'toggle-schedulable', 'edit-selected', 'edit-filtered']) expect(wrapper.emitted(event)).toBeUndefined()
    expect(wrapper.findAll('button').filter(b => !b.isVisible()).length).toBeGreaterThan(4)
    wrapper.unmount()
  })
})
