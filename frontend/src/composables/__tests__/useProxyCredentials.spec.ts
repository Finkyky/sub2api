import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useProxyCredentials, credentialProxyUrl } from '../useProxyCredentials'
import type { Proxy, User, AdminDataPayload } from '@/types'

beforeEach(() => setActivePinia(createPinia()))
describe('proxy credentials from authorized export only', () => {
  const proxy = { id: 7, protocol: 'http', host: 'proxy.test', port: 8080, username: 'alice' } as Proxy
  const payload = { proxies: [{ ...proxy, password: 's:ecret' }] } as unknown as AdminDataPayload
  it('does not export or fabricate credentials for a read-only user', async () => {
    const auth = useAuthStore(); auth.user = { permissions: ['proxies.read'] } as User
    const exporter = vi.fn().mockResolvedValue(payload)
    const controller = useProxyCredentials(exporter)
    await expect(controller.reveal(proxy)).rejects.toThrow('FORBIDDEN')
    expect(exporter).not.toHaveBeenCalled()
  })
  it('uses step-up, retries export for the selected ID and clears secrets on revocation', async () => {
    const auth = useAuthStore(); auth.user = { permissions: ['proxies.export'] } as User
    const exporter = vi.fn().mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }).mockResolvedValue(payload)
    const controller = useProxyCredentials(exporter)
    const result = controller.reveal(proxy)
    await vi.waitFor(() => expect(controller.stepUp.visible.value).toBe(true))
    controller.stepUp.onVerified()
    expect(credentialProxyUrl(await result)).toBe('http://alice:s%3Aecret@proxy.test:8080')
    expect(exporter).toHaveBeenLastCalledWith({ ids: [7] })
    auth.user.permissions = ['proxies.read']; await nextTick()
    expect(controller.credentials.value[7]).toBeUndefined()
    controller.dispose()
  })
  it('checks revocation after an in-flight export', async () => {
    const auth = useAuthStore(); auth.user = { permissions: ['proxies.export'] } as User
    let resolve!: (value: AdminDataPayload) => void
    const controller = useProxyCredentials(() => new Promise(r => { resolve = r }))
    const result = controller.reveal(proxy)
    auth.user.permissions = []; resolve(payload)
    await expect(result).rejects.toThrow('FORBIDDEN')
    expect(controller.credentials.value[7]).toBeUndefined()
    controller.dispose()
  })
})
