import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { installPermissionRefresh } from '../permissionRefresh'
import type { User } from '@/types'
import type { Router } from 'vue-router'

const harness = vi.hoisted(() => ({ guard: null as any, routes: [] as any[] }))
vi.mock('vue-router', () => ({
  createWebHistory: () => ({}),
  createRouter: (options: any) => { harness.routes = options.routes; return {
    beforeEach: (guard: any) => { harness.guard = guard }, afterEach: vi.fn(), onError: vi.fn(),
    resolve: (path: string) => ({ path, meta: harness.routes.find(r => r.path === path)?.meta ?? {} })
  } }
}))
vi.mock('@/api', () => ({ authAPI: {}, isTotp2FARequired: () => false }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ siteName: 'Test', backendModeEnabled: true, publicSettingsLoaded: true, cachedPublicSettings: {} }) }))
vi.mock('@/stores/adminSettings', () => ({ useAdminSettingsStore: () => ({ customMenuItems: [] }) }))
vi.mock('@/stores/adminCompliance', () => ({ useAdminComplianceStore: () => ({ initialized: true }) }))
vi.mock('@/composables/useRoutePrefetch', () => ({ useRoutePrefetch: () => ({ triggerPrefetch: vi.fn() }) }))
vi.mock('@/api/setup', () => ({ getSetupStatus: vi.fn() }))
import '../index'

beforeEach(() => { localStorage.clear(); setActivePinia(createPinia()) })
function authorize(permissions: string[]) {
  const auth = useAuthStore(); auth.token = 'live'; auth.user = { id: 1, role: 'user', permissions } as User
  vi.spyOn(auth, 'refreshUser').mockImplementation(async () => auth.user!)
  return auth
}
async function navigate(path: string) {
  const next = vi.fn()
  const meta = harness.routes.find(r => r.path === path)?.meta ?? {}
  await harness.guard({ path, fullPath: path, meta, params: {} }, {}, next)
  expect(next).toHaveBeenCalledOnce()
  return next.mock.calls[0][0]
}
describe('actual RBAC guard in backend mode', () => {
  it('allows permission-only operators without dashboard.read and terminates login/denial redirects', async () => {
    const auth = authorize(['proxies.read'])
    expect(await navigate('/login')).toBe('/admin/proxies')
    expect(await navigate('/admin/dashboard')).toBe('/admin/proxies')
    expect(await navigate('/admin/proxies')).toBeUndefined()
    expect(auth.refreshUser).toHaveBeenCalledTimes(3)
  })
  it('fails closed for unmapped routes, even super users', async () => {
    const auth = authorize([]); auth.user!.is_super_admin = true
    expect(await navigate('/admin/unregistered')).toBe('/admin/dashboard')
  })
  it('does not send an empty-role user around a backend-mode login loop', async () => {
    authorize([])
    expect(await navigate('/admin/users')).toBe('/access-denied')
    expect(await navigate('/access-denied')).toBeUndefined()
  })
  it('refreshes authority before checking a direct admin URL and handles refresh failure', async () => {
    const auth = authorize(['users.read'])
    vi.mocked(auth.refreshUser).mockImplementationOnce(async () => { auth.user!.permissions = []; return auth.user! })
    expect(await navigate('/admin/users')).toBe('/access-denied')
    auth.user!.permissions = ['users.read']
    vi.mocked(auth.refreshUser).mockRejectedValueOnce(new Error('offline'))
    expect(await navigate('/admin/users')).toBe('/access-denied')
  })
  it('moves the current page away when periodic refresh revokes its permission', async () => {
    const auth = authorize(['users.read'])
    const router = { currentRoute: ref({ path: '/admin/users' }), replace: vi.fn().mockResolvedValue(undefined) }
    const dispose = installPermissionRefresh(router as unknown as Router)
    auth.user!.permissions = ['permissions.read']; await nextTick()
    expect(router.replace).toHaveBeenCalledWith('/access-denied')
    dispose()
  })
})
