import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { permissionDirective } from '@/directives/permission'
import type { User } from '@/types'
import ProxiesView from '@/views/admin/ProxiesView.vue'
const mocks = vi.hoisted(() => ({ list: vi.fn(), exportData: vi.fn(), copy: vi.fn(), error: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { list: mocks.list, getAllWithCount: vi.fn().mockResolvedValue([]), exportData: mocks.exportData } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: vi.fn() }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const row = { id: 7, name: 'proxy', protocol: 'http', host: 'proxy.test', port: 8080, username: 'alice', status: 'active' }
const mountView = () => shallowMount(ProxiesView, { attachTo: document.body, global: { directives: { permission: permissionDirective }, stubs: {
  AppLayout: { template: '<div><slot/></div>' },
  TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/></div>' },
  DataTable: { props: ['data'], template: '<div v-for="row in data"><slot name="cell-address" :row="row"/><slot name="cell-auth" :row="row"/><slot name="cell-actions" :row="row"/></div>' },
  BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' },
} } })
let wrapper: ReturnType<typeof mountView> | undefined
beforeEach(() => {
  setActivePinia(createPinia()); useAuthStore().user = { permissions: ['proxies.read'] } as User
  vi.clearAllMocks(); mocks.list.mockResolvedValue({ items: [row], total: 1, pages: 1 })
  mocks.exportData.mockResolvedValue({ proxies: [{ ...row, password: 'secret' }] })
})
afterEach(() => wrapper?.unmount())
describe('proxy page actual read-only controls and export copy', () => {
  it('does not show or dispatch write/test/export/copy actions for a reader', async () => {
    wrapper = mountView(); await flushPromises()
    const writeButtons = wrapper.findAll('button').filter(b => /common.edit|common.delete|createProxy|dataImport|dataExport|qualityCheck|testConnection|batchDelete/i.test(b.text()) || b.attributes('title') === 'admin.proxies.copyProxyUrl')
    expect(writeButtons.length).toBeGreaterThan(7)
    for (const button of writeButtons) { expect(button.isVisible(), button.text()).toBe(false); await button.trigger('click') }
    expect(mocks.copy).not.toHaveBeenCalled(); expect(mocks.exportData).not.toHaveBeenCalled()
  })
  it('copies only after an authorized export, never assembles credentials from the redacted row', async () => {
    useAuthStore().user!.permissions = ['proxies.read', 'proxies.export']
    wrapper = mountView(); await flushPromises()
    await wrapper.get('button[title="admin.proxies.copyProxyUrl"]').trigger('click'); await flushPromises()
    expect(mocks.exportData).toHaveBeenCalledWith({ ids: [7] })
    expect(mocks.copy).toHaveBeenCalledWith('http://alice:secret@proxy.test:8080', 'admin.proxies.urlCopied')
  })
})
