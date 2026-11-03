import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '../auth'
import { useAdminSettingsStore } from '../adminSettings'
import type { User } from '@/types'
const api = vi.hoisted(() => ({ settings: vi.fn(), payment: vi.fn() }))
vi.mock('@/api', () => ({ authAPI: {}, isTotp2FARequired: () => false, adminAPI: { settings: { getSettings: api.settings }, payment: { getConfig: api.payment } } }))
const menus = [{ id: 'private-menu', label: 'Private menu', visibility: 'admin', sort_order: 0 }]
function authority(permissions: string[]) { const auth = useAuthStore(); auth.token = 'session'; auth.user = { id: 1, permissions } as User; return auth }
beforeEach(() => { setActivePinia(createPinia()); localStorage.clear(); vi.clearAllMocks(); api.settings.mockResolvedValue({ custom_menu_items: menus, ops_monitoring_enabled: false }); api.payment.mockResolvedValue({ data: { enabled: true } }) })
describe('independently authorized admin settings resources', () => {
  it('loads settings without issuing an unauthorized payment request', async () => {
    authority(['settings.read']); const store = useAdminSettingsStore(); await store.fetch()
    expect(api.payment).not.toHaveBeenCalled(); expect(api.settings).toHaveBeenCalledOnce()
    expect(store.customMenuItems).toEqual(menus); expect(store.opsMonitoringEnabled).toBe(false)
  })
  it('loads payment config without settings.read', async () => {
    authority(['payments.read']); const store = useAdminSettingsStore(); await store.fetch()
    expect(api.settings).not.toHaveBeenCalled(); expect(api.payment).toHaveBeenCalledOnce()
    expect(store.paymentEnabled).toBe(true); expect(store.customMenuItems).toEqual([])
  })
  it('preserves successful settings when payment fails, and retries only the failed resource', async () => {
    authority(['settings.read', 'payments.read']); api.payment.mockRejectedValueOnce({ status: 403 })
    const store = useAdminSettingsStore(); await store.fetch()
    expect(store.customMenuItems).toEqual(menus); expect(store.loaded).toBe(false)
    await store.fetch()
    expect(api.settings).toHaveBeenCalledOnce(); expect(api.payment).toHaveBeenCalledTimes(2)
    expect(store.loaded).toBe(true)
  })
  it('immediately removes menus on revocation and ignores late settings responses', async () => {
    const auth = authority(['settings.read']); const store = useAdminSettingsStore(); await store.fetch()
    let resolve!: (value: any) => void; api.settings.mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const pending = store.fetch(true)
    auth.user!.permissions = ['payments.read']
    expect(store.customMenuItems).toEqual([])
    resolve({ custom_menu_items: menus }); await pending
    expect(store.customMenuItems).toEqual([])
    await store.fetch(); expect(store.paymentEnabled).toBe(true)
  })
  it('clears both resources on logout and coalesces concurrent fetches', async () => {
    const auth = authority(['settings.read', 'payments.read']); const store = useAdminSettingsStore()
    await Promise.all([store.fetch(), store.fetch()]); expect(api.settings).toHaveBeenCalledOnce(); expect(api.payment).toHaveBeenCalledOnce()
    auth.user = null; auth.token = null
    expect(store.customMenuItems).toEqual([]); expect(store.paymentEnabled).toBe(false)
    await store.fetch(); expect(api.settings).toHaveBeenCalledOnce(); expect(api.payment).toHaveBeenCalledOnce()
  })
  it('does not reuse admin menu state across different accounts', async () => {
    const auth = authority(['settings.read']); const store = useAdminSettingsStore(); await store.fetch()
    auth.user = { id: 2, permissions: ['settings.read'] } as User
    expect(store.customMenuItems).toEqual([])
    api.settings.mockResolvedValueOnce({ custom_menu_items: [] }); await store.fetch()
    expect(api.settings).toHaveBeenCalledTimes(2); expect(store.customMenuItems).toEqual([])
  })
})
