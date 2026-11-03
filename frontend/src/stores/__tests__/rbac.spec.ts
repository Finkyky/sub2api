import { describe, expect, it, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '../auth'

describe('RBAC auth helpers', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('returns true when a permission is present', () => {
    localStorage.setItem('auth_token', 'token')
    localStorage.setItem('auth_user', JSON.stringify({ role: 'user', permissions: ['users.read'] }))
    const store = useAuthStore()
    store.checkAuth()
    expect(store.hasPermission('users.read')).toBe(true)
    expect(store.hasPermission('users.delete')).toBe(false)
  })

  it('treats super admin as having every permission', () => {
    localStorage.setItem('auth_token', 'token')
    localStorage.setItem('auth_user', JSON.stringify({ role: 'admin', permissions: [], is_super_admin: true }))
    const store = useAuthStore()
    store.checkAuth()
    expect(store.hasPermission('future.permission')).toBe(true)
  })
})
