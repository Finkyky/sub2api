import { describe, expect, it } from 'vitest'
import { rbacMessages } from '../messages'

// The repository's global locale scanner only validates the global `t` namespace.
// These component-local `rt` calls intentionally use an independent bilingual schema.
const localComponents = import.meta.glob('../*.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
const localPages = import.meta.glob('../../../../views/admin/{Users,Roles,Permissions}View.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

describe('component-local RBAC locale completeness', () => {
  it('keeps both schemas identical and every message nonempty', () => {
    expect(Object.keys(rbacMessages.en.rbac).sort()).toEqual(Object.keys(rbacMessages.zh.rbac).sort())
    for (const locale of Object.values(rbacMessages)) {
      for (const message of Object.values(locale.rbac)) expect(message().trim()).not.toBe('')
    }
  })
  it('contains every RBAC key referenced by pages and components', () => {
    const sources = [...Object.values(localComponents), ...Object.values(localPages)]
    expect(sources.length).toBeGreaterThanOrEqual(7)
    for (const source of sources) {
      for (const match of source.matchAll(/['"]rbac\.([a-zA-Z]+)['"]/g)) {
        expect(rbacMessages.en.rbac, `English: ${match[1]}`).toHaveProperty(match[1])
        expect(rbacMessages.zh.rbac, `Chinese: ${match[1]}`).toHaveProperty(match[1])
      }
    }
  })
})
