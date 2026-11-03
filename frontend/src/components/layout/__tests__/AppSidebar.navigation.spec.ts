import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh/common'
import AppSidebar from '../AppSidebar.vue'

const m = vi.hoisted(() => ({ fetch: vi.fn(), nextStep: vi.fn(), currentStep: vi.fn() }))
let auth: any, app: any, settings: any
vi.mock('@/stores', () => ({
  useAuthStore: () => auth, useAppStore: () => app, useAdminSettingsStore: () => settings,
  useOnboardingStore: () => ({ nextStep: m.nextStep, isCurrentStep: m.currentStep })
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => app }))
vi.mock('@/composables/useBatchImageAccess', () => ({ useBatchImageAccess: () => ({ canUseBatchImage: ref(false), refreshBatchImageAccess: vi.fn().mockResolvedValue(undefined) }) }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks(); localStorage.clear()
  auth = reactive({ isAdmin: true, isSuperAdmin: true, isSimpleMode: false, permissions: [] as string[], user: { id: 1 }, token: 'fixture',
    hasPermission: (permission: string) => auth.isSuperAdmin || auth.permissions.includes(permission) })
  app = reactive({ sidebarCollapsed: false, mobileOpen: false, sidebarScrollTop: 0, publicSettingsLoaded: true, siteName: 'Sidebar fixture', siteLogo: '/logo.svg',
    cachedPublicSettings: { plugin_management_enabled: true, channel_monitor_enabled: true, subscription_enabled: true, payment_enabled: true, affiliate_enabled: true, risk_control_enabled: true, custom_menu_items: [] },
    toggleSidebar: vi.fn(() => { app.sidebarCollapsed = !app.sidebarCollapsed }),
    setSidebarCollapsed: vi.fn((value: boolean) => { app.sidebarCollapsed = value }),
    setMobileOpen: vi.fn((value: boolean) => { app.mobileOpen = value }) })
  settings = reactive({ opsMonitoringEnabled: true, paymentEnabled: true, customMenuItems: [], fetch: m.fetch })
  m.currentStep.mockReturnValue(false)
})
afterEach(() => vi.useRealTimers())
async function render(path = '/admin/dashboard') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  await router.push(path); await router.isReady()
  const wrapper = mount(AppSidebar, { attachTo: document.body, global: { plugins: [router, createI18n({ legacy: false, locale: 'zh', messages: { zh: { nav: Object.fromEntries(Object.entries(zh.nav).map(([key, value]) => [key, () => value])) } }, missingWarn: false, fallbackWarn: false })] } })
  await flushPromises()
  return { wrapper, router }
}
const group = (w: any, path: string) => w.get(`[data-sidebar-group="${path}"]`)
const entry = (w: any, path: string) => w.get(`a[href="${path}"]`)

describe('admin sidebar categories', () => {
  it('puts users, roles and permissions below one Chinese user category', async () => {
    const { wrapper: w } = await render()
    expect(group(w, '/admin/users').text()).toContain('用户管理')
    expect(entry(w, '/admin/users').isVisible()).toBe(false)
    await group(w, '/admin/users').trigger('click')
    expect(entry(w, '/admin/users').text()).toBe('用户列表')
    expect(entry(w, '/admin/roles').isVisible()).toBe(true)
    expect(entry(w, '/admin/permissions').isVisible()).toBe(true)
  })
  it('puts all five plugin pages below a single plugin category', async () => {
    const { wrapper: w } = await render()
    expect(group(w, '/admin/plugins').text()).toContain('插件管理')
    await group(w, '/admin/plugins').trigger('click')
    for (const path of ['/admin/plugins', '/admin/plugin-instances', '/admin/plugin-bindings', '/admin/plugin-tasks', '/admin/plugin-runs']) expect(entry(w, path).isVisible()).toBe(true)
    expect(entry(w, '/admin/plugins').text()).toBe('插件包')
  })
  it('reduces the full admin section to ten top-level entries', async () => {
    const { wrapper: w } = await render()
    const top = [...w.get('.sidebar-section').element.children].filter(el => ['BUTTON', 'A'].includes(el.tagName))
    expect(top).toHaveLength(10)
    expect(top.filter(el => el.tagName === 'BUTTON')).toHaveLength(8)
  })
  it('only toggles the category and does not navigate on a category click', async () => {
    const { wrapper: w, router } = await render()
    const parent = group(w, '/admin/users')
    expect(parent.attributes('type')).toBe('button')
    expect(parent.attributes('aria-expanded')).toBe('false')
    await parent.trigger('click')
    expect(parent.attributes('aria-expanded')).toBe('true')
    expect(w.get('#' + parent.attributes('aria-controls')).isVisible()).toBe(true)
    expect(router.currentRoute.value.path).toBe('/admin/dashboard')
    await parent.trigger('click')
    expect(entry(w, '/admin/roles').isVisible()).toBe(false)
  })
  it('automatically expands the active category and permits manual collapse', async () => {
    const { wrapper: w } = await render('/admin/roles')
    expect(group(w, '/admin/users').attributes('aria-expanded')).toBe('true')
    expect(entry(w, '/admin/roles').classes()).toContain('sidebar-link-active')
    await group(w, '/admin/users').trigger('click')
    expect(entry(w, '/admin/roles').isVisible()).toBe(false)
    expect(group(w, '/admin/users').classes()).toContain('sidebar-link-active')
  })
  it('highlights the most specific child for nested routes, not the orders root too', async () => {
    const { wrapper: w } = await render('/admin/orders/dashboard')
    expect(entry(w, '/admin/orders/dashboard').classes()).toContain('sidebar-link-active')
    expect(entry(w, '/admin/orders').classes()).not.toContain('sidebar-link-active')
  })
  it('opens and highlights a category child for a detail route', async () => {
    const { wrapper: w } = await render('/admin/roles/7')
    expect(group(w, '/admin/users').attributes('aria-expanded')).toBe('true')
    expect(entry(w, '/admin/roles').classes()).toContain('sidebar-link-active')
  })
  it('keeps a plugin instance reader entry without requiring plugins.read', async () => {
    auth.isSuperAdmin = false; auth.permissions = ['plugin_instances.read']
    const { wrapper: w } = await render('/admin/plugin-instances')
    expect(group(w, '/admin/plugins').exists()).toBe(true)
    expect(entry(w, '/admin/plugin-instances').isVisible()).toBe(true)
    expect(w.find('a[href="/admin/plugins"]').exists()).toBe(false)
    expect(w.find('[data-sidebar-group="/admin/users"]').exists()).toBe(false)
    expect(m.fetch).not.toHaveBeenCalled()
  })
  it('reactively removes unauthorized children and then an empty category', async () => {
    auth.isSuperAdmin = false; auth.permissions = ['users.read', 'roles.read']
    const { wrapper: w } = await render('/admin/users')
    auth.permissions = ['users.read']; await flushPromises()
    expect(w.find('a[href="/admin/roles"]').exists()).toBe(false)
    auth.permissions = []; await flushPromises()
    expect(w.find('[data-sidebar-group="/admin/users"]').exists()).toBe(false)
  })
  it('removes the complete plugin category when its feature is disabled', async () => {
    const { wrapper: w } = await render()
    app.cachedPublicSettings.plugin_management_enabled = false; await flushPromises()
    expect(w.find('[data-sidebar-group="/admin/plugins"]').exists()).toBe(false)
    expect(w.find('a[href="/admin/plugin-instances"]').exists()).toBe(false)
  })
  it('filters disabled feature children without hiding unrelated operations', async () => {
    const { wrapper: w } = await render()
    app.cachedPublicSettings.subscription_enabled = false; await flushPromises()
    expect(w.find('a[href="/admin/subscriptions"]').exists()).toBe(false)
    await group(w, '/admin/operations').trigger('click')
    expect(entry(w, '/admin/announcements').isVisible()).toBe(true)
  })
  it('retains audit logs without the optional risk-control feature', async () => {
    app.cachedPublicSettings.risk_control_enabled = false
    const { wrapper: w } = await render('/admin/audit-logs')
    expect(entry(w, '/admin/audit-logs').isVisible()).toBe(true)
    expect(w.find('a[href="/admin/prompt-audit"]').exists()).toBe(false)
  })
  it('preserves simple-mode restrictions at child level', async () => {
    auth.isSimpleMode = true
    const { wrapper: w } = await render('/admin/accounts')
    for (const path of ['/admin/users', '/admin/subscriptions', '/admin/redeem', '/admin/promo-codes', '/admin/channels/pricing', '/admin/channels/monitor', '/admin/audit-logs']) expect(w.find(`a[href="${path}"]`).exists()).toBe(false)
    expect(entry(w, '/admin/accounts').isVisible()).toBe(true)
    expect(entry(w, '/admin/groups').isVisible()).toBe(true)
    expect(w.find('[data-sidebar-group="/admin/orders"]').exists()).toBe(false)
    expect(w.find('[data-sidebar-group="/admin/affiliates"]').exists()).toBe(false)
  })
  it('expands the icon-only sidebar when a category is clicked', async () => {
    app.sidebarCollapsed = true
    const { wrapper: w, router } = await render()
    await group(w, '/admin/plugins').trigger('click')
    expect(app.sidebarCollapsed).toBe(false)
    expect(entry(w, '/admin/plugin-tasks').isVisible()).toBe(true)
    expect(router.currentRoute.value.path).toBe('/admin/dashboard')
  })
  it('preserves tour anchors and reveals their category on a tour request', async () => {
    app.sidebarCollapsed = true
    const { wrapper: w } = await render()
    window.dispatchEvent(new CustomEvent('sub2api:sidebar-reveal', { detail: '#sidebar-group-manage' }))
    await flushPromises()
    expect(app.sidebarCollapsed).toBe(false)
    expect(app.mobileOpen).toBe(true)
    expect(w.get('#sidebar-group-manage').isVisible()).toBe(true)
    expect(w.get('#sidebar-channel-manage').isVisible()).toBe(true)
    expect(w.get('#sidebar-wallet').exists()).toBe(true)
  })
  it('removes the tour reveal listener when the sidebar unmounts', async () => {
    const { wrapper: w } = await render(); w.unmount(); vi.clearAllMocks()
    window.dispatchEvent(new CustomEvent('sub2api:sidebar-reveal', { detail: '#sidebar-group-manage' }))
    expect(app.setSidebarCollapsed).not.toHaveBeenCalled()
  })
  it('navigates child links and closes the mobile sidebar', async () => {
    const { wrapper: w, router } = await render()
    vi.useFakeTimers(); app.mobileOpen = true
    await group(w, '/admin/users').trigger('click')
    await entry(w, '/admin/users').trigger('click'); await vi.advanceTimersByTimeAsync(200); await flushPromises()
    expect(router.currentRoute.value.path).toBe('/admin/users')
    expect(app.mobileOpen).toBe(false)
  })
  it('retains custom admin entries outside the category collection', async () => {
    settings.customMenuItems = [{ id: 'fixture', label: '自定义页面', visibility: 'admin', sort_order: 0, icon_svg: '' }]
    const { wrapper: w } = await render()
    expect(entry(w, '/custom/fixture').text()).toBe('自定义页面')
  })
  it('reopens a manually collapsed category when navigating to its child', async () => {
    const { wrapper: w, router } = await render('/admin/roles')
    await group(w, '/admin/users').trigger('click')
    expect(entry(w, '/admin/permissions').isVisible()).toBe(false)
    await router.push('/admin/permissions'); await flushPromises()
    expect(entry(w, '/admin/permissions').isVisible()).toBe(true)
    expect(entry(w, '/admin/permissions').classes()).toContain('sidebar-link-active')
  })
  it('ignores unknown or unauthorized onboarding reveal requests', async () => {
    auth.isSuperAdmin = false; auth.permissions = ['roles.read']; app.sidebarCollapsed = true
    await render()
    for (const detail of ['#sidebar-group-manage', '#unknown', { selector: '#sidebar-group-manage' }, null]) {
      window.dispatchEvent(new CustomEvent('sub2api:sidebar-reveal', { detail }))
    }
    await flushPromises()
    expect(app.setSidebarCollapsed).not.toHaveBeenCalled()
    expect(app.setMobileOpen).not.toHaveBeenCalled()
  })
  it('reveals the wallet anchor inside operations for the onboarding tour', async () => {
    const { wrapper: w } = await render()
    expect(w.get('#sidebar-wallet').isVisible()).toBe(false)
    window.dispatchEvent(new CustomEvent('sub2api:sidebar-reveal', { detail: '#sidebar-wallet' }))
    await flushPromises()
    expect(w.get('#sidebar-wallet').isVisible()).toBe(true)
    expect(group(w, '/admin/operations').attributes('aria-expanded')).toBe('true')
  })
  it('does not change regular-user navigation', async () => {
    auth.isAdmin = false; auth.isSuperAdmin = false
    const { wrapper: w } = await render('/keys')
    expect(entry(w, '/keys').isVisible()).toBe(true)
    expect(w.find('[data-sidebar-group]').exists()).toBe(false)
  })
})
