import { beforeEach, describe, it, expect, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '../auth'
import type { User } from '@/types'
const api = vi.hoisted(() => ({ getCurrentUser: vi.fn(), logout: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@/api', () => ({ authAPI: api, isTotp2FARequired: () => false }))
beforeEach(() => { setActivePinia(createPinia()); localStorage.clear(); vi.clearAllMocks() })
describe('live RBAC snapshot races', () => {
  it('joins initial, navigation and focus refreshes instead of overwriting newer authority', async () => {
    const auth = useAuthStore(); auth.token = 'session'; auth.user = { permissions: ['users.update'] } as User
    let resolve!: (value: any) => void
    api.getCurrentUser.mockImplementation(() => new Promise(r => { resolve = r }))
    const first = auth.refreshUser(); const second = auth.refreshUser()
    expect(api.getCurrentUser).toHaveBeenCalledTimes(1)
    resolve({ data: { permissions: ['users.read'] } })
    await Promise.all([first, second])
    expect(auth.hasPermission('users.update')).toBe(false)
  })
  it('does not restore an old user or persist permissions after logout', async () => {
    const auth = useAuthStore(); auth.token = 'session'; auth.user = { permissions: ['users.update'] } as User
    let resolve!: (value: any) => void
    api.getCurrentUser.mockImplementation(() => new Promise(r => { resolve = r }))
    const pending = auth.refreshUser()
    await auth.logout()
    resolve({ data: { permissions: ['users.update'] } })
    await expect(pending).rejects.toThrow('Auth session changed')
    expect(auth.user).toBeNull(); expect(localStorage.getItem('auth_user')).toBeNull()
  })
})
