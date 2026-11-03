import { defineStore } from 'pinia'
import { ref, watch, onScopeDispose } from 'vue'
import { useAuthStore } from './auth'
import { adminAPI } from '@/api'
import type { CustomMenuItem } from '@/types'

export const useAdminSettingsStore = defineStore('adminSettings', () => {
  const auth = useAuthStore()
  const loaded = ref(false)
  const loading = ref(false)

  const readCachedBool = (key: string, defaultValue: boolean): boolean => {
    try {
      const raw = localStorage.getItem(key)
      if (raw === 'true') return true
      if (raw === 'false') return false
    } catch {
      // ignore localStorage failures
    }
    return defaultValue
  }

  const writeCachedBool = (key: string, value: boolean) => {
    try {
      localStorage.setItem(key, value ? 'true' : 'false')
    } catch {
      // ignore localStorage failures
    }
  }

  const readCachedString = (key: string, defaultValue: string): string => {
    try {
      const raw = localStorage.getItem(key)
      if (typeof raw === 'string' && raw.length > 0) return raw
    } catch {
      // ignore localStorage failures
    }
    return defaultValue
  }

  const writeCachedString = (key: string, value: string) => {
    try {
      localStorage.setItem(key, value)
    } catch {
      // ignore localStorage failures
    }
  }

  // Default open, but honor cached value to reduce UI flicker on first paint.
  const opsMonitoringEnabled = ref(readCachedBool('ops_monitoring_enabled_cached', true))
  const opsRealtimeMonitoringEnabled = ref(readCachedBool('ops_realtime_monitoring_enabled_cached', true))
  const opsQueryModeDefault = ref(readCachedString('ops_query_mode_default_cached', 'auto'))
  const paymentEnabled = ref(readCachedBool('payment_enabled_cached', false))
  const customMenuItems = ref<CustomMenuItem[]>([])

  let settingsLoaded = false
  let paymentLoaded = false
  let generation = 0
  let pending: Promise<void> | null = null
  const canReadSettings = () => auth.hasPermission('settings.read')
  const canReadPayments = () => auth.hasPermission('payments.read')

  function updateLoaded() {
    loaded.value = (!canReadSettings() || settingsLoaded) && (!canReadPayments() || paymentLoaded)
  }

  // Never retain private navigation across users, sessions or permission changes.
  // Generation checks also discard responses issued before a revoke/re-grant.
  watch([() => auth.user?.id, () => auth.token, canReadSettings, canReadPayments], () => {
    generation++
    pending = null
    settingsLoaded = false
    paymentLoaded = false
    loaded.value = false
    loading.value = false
    customMenuItems.value = []
    if (!canReadSettings()) {
      opsMonitoringEnabled.value = true
      opsRealtimeMonitoringEnabled.value = true
      opsQueryModeDefault.value = 'auto'
    }
    if (!canReadPayments()) paymentEnabled.value = false
  }, { flush: 'sync' })

  function fetch(force = false): Promise<void> {
    if (pending) return pending
    const requestGeneration = generation
    const tasks: Promise<void>[] = []
    if (canReadSettings() && (!settingsLoaded || force)) {
      tasks.push((async () => {
        try {
          const settings = await adminAPI.settings.getSettings()
          if (requestGeneration !== generation || !canReadSettings()) return
          opsMonitoringEnabled.value = settings.ops_monitoring_enabled ?? true
          writeCachedBool('ops_monitoring_enabled_cached', opsMonitoringEnabled.value)
          opsRealtimeMonitoringEnabled.value = settings.ops_realtime_monitoring_enabled ?? true
          writeCachedBool('ops_realtime_monitoring_enabled_cached', opsRealtimeMonitoringEnabled.value)
          opsQueryModeDefault.value = settings.ops_query_mode_default || 'auto'
          writeCachedString('ops_query_mode_default_cached', opsQueryModeDefault.value)
          customMenuItems.value = Array.isArray(settings.custom_menu_items) ? settings.custom_menu_items : []
          settingsLoaded = true
        } catch (err) {
          if (requestGeneration === generation) {
            settingsLoaded = false
            if ((err as { status?: number }).status === 403) customMenuItems.value = []
          }
          console.error('[adminSettings] Failed to fetch settings:', err)
        }
      })())
    }
    if (canReadPayments() && (!paymentLoaded || force)) {
      tasks.push((async () => {
        try {
          const response = await adminAPI.payment.getConfig()
          if (requestGeneration !== generation || !canReadPayments()) return
          paymentEnabled.value = response.data?.enabled ?? false
          writeCachedBool('payment_enabled_cached', paymentEnabled.value)
          paymentLoaded = true
        } catch (err) {
          if (requestGeneration === generation) paymentLoaded = false
          console.error('[adminSettings] Failed to fetch payment config:', err)
        }
      })())
    }
    if (!tasks.length) { updateLoaded(); return Promise.resolve() }
    loading.value = true
    const request = Promise.all(tasks).then(() => {}).finally(() => {
      if (requestGeneration !== generation) return
      loading.value = false
      pending = null
      updateLoaded()
    })
    pending = request
    return request
  }

  function setOpsMonitoringEnabledLocal(value: boolean) {
    opsMonitoringEnabled.value = value
    writeCachedBool('ops_monitoring_enabled_cached', value)
    settingsLoaded = true
    updateLoaded()
  }

  function setOpsRealtimeMonitoringEnabledLocal(value: boolean) {
    opsRealtimeMonitoringEnabled.value = value
    writeCachedBool('ops_realtime_monitoring_enabled_cached', value)
    settingsLoaded = true
    updateLoaded()
  }

  function setPaymentEnabledLocal(value: boolean) {
    paymentEnabled.value = value
    writeCachedBool('payment_enabled_cached', value)
    settingsLoaded = true
    updateLoaded()
  }

  function setOpsQueryModeDefaultLocal(value: string) {
    opsQueryModeDefault.value = value || 'auto'
    writeCachedString('ops_query_mode_default_cached', opsQueryModeDefault.value)
    settingsLoaded = true
    updateLoaded()
  }

  // Keep UI consistent if we learn that ops is disabled via feature-gated 404s.
  // (event is dispatched from the axios interceptor)
  let eventHandlerCleanup: (() => void) | null = null

  function initializeEventListeners() {
    if (eventHandlerCleanup) return

    try {
      const handler = () => {
        setOpsMonitoringEnabledLocal(false)
      }
      window.addEventListener('ops-monitoring-disabled', handler)
      eventHandlerCleanup = () => {
        window.removeEventListener('ops-monitoring-disabled', handler)
      }
    } catch {
      // ignore window access failures (SSR)
    }
  }

  if (typeof window !== 'undefined') {
    initializeEventListeners()
  }

  onScopeDispose(() => { generation++; eventHandlerCleanup?.() })

  return {
    loaded,
    loading,
    opsMonitoringEnabled,
    opsRealtimeMonitoringEnabled,
    opsQueryModeDefault,
    paymentEnabled,
    customMenuItems,
    fetch,
    setOpsMonitoringEnabledLocal,
    setOpsRealtimeMonitoringEnabledLocal,
    setPaymentEnabledLocal,
    setOpsQueryModeDefaultLocal
  }
})
