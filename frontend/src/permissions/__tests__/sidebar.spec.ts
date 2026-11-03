import { beforeEach, describe, it, expect, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/types'
import Sidebar from '@/components/layout/AppSidebar.vue'
import VersionBadge from '@/components/common/VersionBadge.vue'
import { useAppStore } from '@/stores/app'
import zh from '@/i18n/locales/zh'
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key.split('.').reduce<unknown>((value, segment) => (value as Record<string, unknown>)?.[segment], zh) ?? key }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ path: '/admin/permissions' }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/composables/useBatchImageAccess', () => ({ useBatchImageAccess: () => ({ canUseBatchImage: false, refreshBatchImageAccess: vi.fn() }) }))
vi.mock('@/stores/adminSettings', () => ({ useAdminSettingsStore: () => ({ customMenuItems: [], settings: null, fetch: vi.fn() }) }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => ({}) }))
beforeEach(() => { setActivePinia(createPinia()); useAuthStore().user = { permissions: ['permissions.read'] } as User })
describe('actual sidebar RBAC', () => {
  it('exposes permissions independently, removes empty groups, and refreshes on revocation', async () => {
    const wrapper = mount(Sidebar, { global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot/></a>' }, VersionBadge: true, Icon: true } } })
    await flushPromises()
    expect(wrapper.find('a[href="/admin/permissions"]').exists()).toBe(true)
    expect(wrapper.find('a[href="/admin/roles"]').exists()).toBe(false)
    expect(wrapper.find('a[href="/admin/dashboard"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(zh.nav.securityAudit)
    expect(wrapper.text()).not.toContain(zh.nav.orderManagement)
    const auth = useAuthStore(); auth.user!.permissions = ['roles.read']; await nextTick()
    expect(wrapper.find('a[href="/admin/permissions"]').exists()).toBe(false)
    expect(wrapper.find('a[href="/admin/roles"]').exists()).toBe(true)
    wrapper.unmount()
  })
})

describe('sidebar brand without a version upgrade entry', () => {
  it.each([false, true])('does not render VersionBadge when collapsed=%s', (collapsed) => {
    const app = useAppStore()
    app.sidebarCollapsed = collapsed
    app.siteVersion = '0.2.7'
    app.currentVersion = '0.2.7'
    app.hasUpdate = true
    const fetchVersion = vi.spyOn(app, 'fetchVersion')
    const wrapper = mount(Sidebar, { global: { stubs: {
      RouterLink: { props: ['to'], template: '<a :href="to"><slot/></a>' },
      VersionBadge: true,
      Icon: true
    } } })
    try {
      expect(wrapper.findComponent(VersionBadge).exists()).toBe(false)
      expect(wrapper.find('.sidebar-brand-title').text()).toBe('Sub2API')
      expect(wrapper.find('.sidebar-header').text()).not.toContain('0.2.7')
      expect(fetchVersion).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
    }
  })
})

describe('Chinese sidebar RBAC labels', () => {
  it('renders readable child labels and the collapsed category tooltip without changing access checks', async () => {
    useAuthStore().user = { permissions: ['permissions.read', 'roles.read'] } as User
    const wrapper = mount(Sidebar, { global: { stubs: {
      RouterLink: { props: ['to'], template: '<a :href="to"><slot/></a>' },
      Icon: true
    } } })
    try {
      expect(wrapper.find('nav a[href="/admin/permissions"]').text()).toBe('权限管理')
      expect(wrapper.find('nav a[href="/admin/roles"]').text()).toBe('角色管理')
      useAppStore().sidebarCollapsed = true
      await nextTick()
      const category = wrapper.get('nav [data-sidebar-group="/admin/users"]')
      expect(category.attributes('title')).toBe('用户管理')
      expect(category.attributes('aria-expanded')).toBe('false')
      const submenu = wrapper.get('#' + category.attributes('aria-controls'))
      expect(submenu.attributes('style')).toContain('display: none')
      await category.trigger('click')
      expect(useAppStore().sidebarCollapsed).toBe(false)
      expect(submenu.attributes('style')).not.toContain('display: none')
      expect(wrapper.find('nav a[href="/admin/permissions"]').text()).toBe('权限管理')
      expect(wrapper.find('nav a[href="/admin/roles"]').text()).toBe('角色管理')
    } finally {
      wrapper.unmount()
    }
  })
})
