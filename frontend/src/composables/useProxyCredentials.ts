import { ref, watch, onScopeDispose, getCurrentScope } from 'vue'
import { usePermission } from './usePermission'
import { useStepUp } from './useStepUp'
import type { Proxy, AdminDataProxy, AdminDataPayload } from '@/types'

// Never pass the redacted list DTO here. Only an export payload contains credentials.
export function credentialProxyUrl(proxy: AdminDataProxy): string {
  const user = encodeURIComponent(proxy.username ?? '')
  const pass = encodeURIComponent(proxy.password ?? '')
  const auth = user || pass ? `${user}:${pass}@` : ''
  const host = proxy.host.includes(':') && !proxy.host.startsWith('[') ? `[${proxy.host}]` : proxy.host
  return `${proxy.protocol}://${auth}${host}:${proxy.port}`
}
export function useProxyCredentials(exporter: (options: { ids: number[] }) => Promise<AdminDataPayload>) {
  const { can, requirePermission } = usePermission()
  const stepUp = useStepUp()
  const credentials = ref<Record<number, AdminDataProxy>>({})
  let disposed = false
  const stop = watch(() => can('proxies.export'), allowed => {
    if (!allowed) { credentials.value = {}; stepUp.onCancel() }
  }, { flush: 'sync' })
  async function reveal(proxy: Proxy): Promise<AdminDataProxy> {
    requirePermission('proxies.export')
    const payload = await stepUp.run(() => {
      requirePermission('proxies.export')
      return exporter({ ids: [proxy.id] })
    })
    requirePermission('proxies.export')
    const entry = payload.proxies?.[0]
    if (disposed || payload.proxies?.length !== 1 || !entry || entry.host !== proxy.host || entry.port !== proxy.port || entry.protocol !== proxy.protocol) {
      throw new Error('Invalid proxy export')
    }
    credentials.value[proxy.id] = entry
    return entry
  }
  function dispose() { disposed = true; stop(); credentials.value = {}; stepUp.onCancel() }
  if (getCurrentScope()) onScopeDispose(dispose)
  return { credentials, reveal, stepUp, dispose }
}
