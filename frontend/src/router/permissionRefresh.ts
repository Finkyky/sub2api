import { watch } from 'vue'
import type { Router } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { canAccessAdminPath } from '@/permissions/policy'

/** Reactive revocation also covers the 60s snapshot refresh and cross-tab/focus updates. */
export function installPermissionRefresh(router: Router) {
  const auth = useAuthStore()
  const stop = watch(() => [auth.user?.permissions, auth.user?.roles, auth.isSuperAdmin, auth.isAuthenticated], () => {
    const path = router.currentRoute.value.path
    if (path.startsWith('/admin') && (!auth.isAdmin || !canAccessAdminPath(auth, path))) {
      void router.replace(auth.isAuthenticated ? '/access-denied' : '/login')
    }
  }, { deep: true, flush: 'post' })
  const refresh = () => { if (auth.isAuthenticated) void auth.refreshUser().catch(() => {
    if (router.currentRoute.value.path.startsWith('/admin')) void router.replace('/access-denied')
  }) }
  const onStorage = (event: StorageEvent) => { if (['auth_user', 'auth_token'].includes(event.key ?? '')) refresh() }
  window.addEventListener('focus', refresh)
  window.addEventListener('storage', onStorage)
  return () => { stop(); window.removeEventListener('focus', refresh); window.removeEventListener('storage', onStorage) }
}
