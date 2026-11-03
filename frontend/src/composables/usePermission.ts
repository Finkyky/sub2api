import { useAuthStore } from '@/stores/auth'
import { allows, type PermissionBinding } from '@/permissions/policy'
import { adminOperations } from '@/permissions/adminOperations'
export function usePermission() {
  const auth = useAuthStore()
  const can = (policy: PermissionBinding) => allows(auth, policy)
  const canOperation = (operation: string) => allows(auth, adminOperations[operation])
  const requirePermission = (policy: PermissionBinding) => {
    if (!can(policy)) throw new Error('FORBIDDEN')
  }
  return { can, canOperation, requirePermission }
}
