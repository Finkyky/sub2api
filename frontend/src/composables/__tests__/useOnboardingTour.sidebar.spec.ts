import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import type { Config, DriveStep } from 'driver.js'
import { useOnboardingTour } from '../useOnboardingTour'

const fixture = vi.hoisted(() => ({
  config: null as Config | null,
  reveal: vi.fn(),
  driver: { drive: vi.fn(), refresh: vi.fn(), destroy: vi.fn(), isActive: vi.fn(() => true) },
  onboarding: { getDriverInstance: vi.fn(() => null), setDriverInstance: vi.fn(),
    setControlMethods: vi.fn(), clearControlMethods: vi.fn(), isDriverActive: vi.fn(() => false) }
}))
vi.mock('driver.js', () => ({ driver: (config: Config) => { fixture.config = config; return fixture.driver } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1, role: 'admin' }, isSimpleMode: false }) }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => fixture.onboarding }))
vi.mock('@/utils/sidebarNavigation', () => ({ requestSidebarReveal: fixture.reveal }))
vi.mock('@/components/Guide/steps', () => ({
  getAdminSteps: () => [{ popover: { title: 'Welcome' } }], getUserSteps: () => []
}))
let wrapper: VueWrapper
let tour: ReturnType<typeof useOnboardingTour>
let target: HTMLAnchorElement
beforeEach(async () => {
  vi.clearAllMocks()
  target = document.createElement('a'); target.id = 'sidebar-wallet'; document.body.appendChild(target)
  let visible = false
  vi.spyOn(target, 'getBoundingClientRect').mockImplementation(() => ({ height: visible ? 32 : 0 }) as DOMRect)
  target.scrollIntoView = vi.fn()
  fixture.reveal.mockImplementation(() => { visible = true })
  wrapper = mount(defineComponent({ setup() { tour = useOnboardingTour({ autoStart: false }); return () => null } }))
  await tour.startTour()
})
afterEach(() => {
  fixture.config?.onDestroyed?.(undefined, {} as DriveStep, {} as never)
  wrapper.unmount(); target.remove(); vi.restoreAllMocks()
})
describe('onboarding hidden sidebar targets', () => {
  it('reveals, scrolls and then refreshes a hidden category child', async () => {
    const step = { element: '#sidebar-wallet', popover: { showButtons: ['next', 'previous'] } } as DriveStep
    await fixture.config!.onHighlightStarted!(target, step, {} as never)
    expect(fixture.reveal).toHaveBeenCalledWith('#sidebar-wallet')
    expect(target.scrollIntoView).toHaveBeenCalledWith({ block: 'nearest', inline: 'nearest', behavior: 'auto' })
    expect(fixture.driver.refresh).toHaveBeenCalledOnce()
    expect(vi.mocked(target.scrollIntoView).mock.invocationCallOrder[0]).toBeLessThan(fixture.driver.refresh.mock.invocationCallOrder[0])
  })
})
