import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

function questionMarkOnlyMessages(value: unknown, path = ''): string[] {
  if (typeof value === 'string') {
    return /^[?\uFF1F\s]+$/.test(value) ? [path] : []
  }
  if (!value || typeof value !== 'object') return []
  return Object.entries(value).flatMap(([key, child]) =>
    questionMarkOnlyMessages(child, path ? `${path}.${key}` : key)
  )
}

describe('locale text integrity', () => {
  it.each(Object.entries({ en, zh }))('%s has no question-mark-only translations', (_locale, messages) => {
    expect(questionMarkOnlyMessages(messages)).toEqual([])
  })

  it('provides readable Chinese RBAC navigation labels in every namespace', () => {
    expect(zh.nav.permissions).toBe('权限管理')
    expect(zh.nav.roles).toBe('角色管理')
    expect(zh.modelPlaza.nav.permissions).toBe('权限管理')
    expect(zh.modelPlaza.nav.roles).toBe('角色管理')
  })

  it('provides readable Chinese access-denied messages', () => {
    expect(zh.common.accessDenied).toBe('访问被拒绝')
    expect(zh.common.accessDeniedHint).toBe('您的账户没有访问此页面的权限，请联系管理员。')
  })
})
