/** Explicit all-authorized fixture for existing business-action tests.
 * Permission enforcement is exercised separately with real stores/directives.
 */
import { vi } from 'vitest'
vi.mock('@/composables/usePermission', () => ({
  usePermission: () => ({ can: () => true, canOperation: () => true, requirePermission: () => {} })
}))
