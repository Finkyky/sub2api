import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { permissionDirective } from '@/directives/permission'
import type { User } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'
import PluginSectionsNav from '../PluginSectionsNav.vue'
const api = vi.hoisted(() => ({ groups: vi.fn(), instances: vi.fn(), detail: vi.fn(), allGroups: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { list: api.groups, getAll: api.allGroups, getUsageSummary: vi.fn().mockResolvedValue([]), getCapacitySummary: vi.fn().mockResolvedValue([]), getLiveCapability: vi.fn().mockResolvedValue({ supported: false }), getModelAllowlistCandidates: vi.fn().mockResolvedValue([]) }, pluginInstances: { list: api.instances, get: api.detail } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => ({ isCurrentStep: () => false, nextStep: vi.fn() }) }))
vi.mock('vue-i18n', async original => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (k: string) => k }) }))
const wrappers: VueWrapper[] = []
beforeEach(() => {
 setActivePinia(createPinia()); useAuthStore().user = { permissions: ['groups.read','plugin_instances.read'] } as User
 api.groups.mockResolvedValue({ items: [{ id: 12, name: 'Business group', platform: 'openai', status: 'active', subscription_type: 'standard', rate_multiplier: 1, supported_model_scopes: [] }], total: 1, pages: 1 }); api.allGroups.mockResolvedValue([])
 api.instances.mockResolvedValue([{ id: 7 }]); api.detail.mockResolvedValue({ instance: { id: 7, name: 'Published checker', enabled: true, current_revision: 2 }, bindings: [{ id: 4, instance_id: 7, group_id: 12, stage: 'input', protocol: 'openai.chat', entry: 'check', enabled: true, sort_order: 1 }] })
})
afterEach(() => wrappers.splice(0).forEach(w => w.unmount()))
function mountGroupsView() {
  const w = mount(GroupsView, { global: { directives: { permission: permissionDirective }, stubs: {
   AppLayout: { template: '<main><slot/></main>' }, TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
   DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row"/></div></div>' },
   BaseDialog: { props: ['show'], template: '<section v-if="show"><slot/><slot name="footer"/></section>' },
   Pagination: true, ConfirmDialog: true, EmptyState: true, Select: true, PlatformIcon: true, Icon: true, GroupCapacityBadge: true, GroupRateMultipliersModal: true, GroupRPMOverridesModal: true, PluginInstancesPanel: true, VueDraggable: true
  } } }); wrappers.push(w); return w
}
describe('actual M6 page/navigation integration with real permission store', () => {
 it('opens a real effective chain from the business GroupsView, then destroys it on revoke', async () => {
  const w = mountGroupsView(); await flushPromises(); expect(api.instances).not.toHaveBeenCalled()
  await w.get('[data-testid="group-plugin-chain"]').trigger('click'); await flushPromises()
  expect(w.get('[data-testid="effective-chain"]').text()).toContain('Published checker'); expect(w.get('[data-testid="effective-chain"]').text()).toContain('check'); expect(w.findAll('main')).toHaveLength(1)
  useAuthStore().user!.permissions = ['groups.read']; await nextTick(); expect(w.find('[data-testid="effective-chain"]').exists()).toBe(false); expect(w.find('[data-testid="group-plugin-chain"]').exists()).toBe(false)
 })
 it('exposes task/run sections independently and removes them on revoke', async () => {
  const auth = useAuthStore(); auth.user!.permissions = ['plugin_tasks.read']; const w = mount(PluginSectionsNav, { global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot/></a>' } } } }); wrappers.push(w)
  expect(w.find('a[href="/admin/plugin-tasks"]').exists()).toBe(true); expect(w.find('a[href="/admin/plugin-runs"]').exists()).toBe(false)
  auth.user!.permissions = ['plugin_runs.read']; await nextTick(); expect(w.find('a[href="/admin/plugin-tasks"]').exists()).toBe(false); expect(w.find('a[href="/admin/plugin-runs"]').exists()).toBe(true)
  auth.user!.permissions = []; await nextTick(); expect(w.findAll('a')).toHaveLength(0)
 })
})

describe('review actual GroupsView disabled live binding', () => {
 it('shows the disabled bound instance and fail-closed status without write controls for a group reader', async () => {
  api.detail.mockResolvedValue({ instance: { id: 7, name: 'Disabled bound checker', enabled: false, current_revision: 2 }, bindings: [{ id: 4, instance_id: 7, group_id: 12, stage: 'input', protocol: 'openai.chat', entry: 'check', enabled: true, sort_order: 1 }] })
  const w = mountGroupsView(); await flushPromises(); await w.get('[data-testid="group-plugin-chain"]').trigger('click'); await flushPromises()
  expect(w.get('[data-testid="effective-chain"]').text()).toContain('Disabled bound checker'); expect(w.get('[data-testid="chain-blocked"]').text()).toContain('admin.pluginBindings.blocked'); expect(w.get('[data-testid="binding-blocker"]').text()).toContain('admin.pluginBindings.disabled')
  expect(w.find('[data-testid="edit-binding-instance"]').exists()).toBe(false); expect(w.find('[data-testid="save-bindings"]').exists()).toBe(false); expect(w.find('[data-testid="save-config"]').exists()).toBe(false)
 })
})
