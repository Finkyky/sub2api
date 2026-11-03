import { describe, expect, it } from 'vitest'
import { bindingsValid, parseConfig } from '../bindings'
import en from '@/i18n/locales/en/admin/pluginInstances'
import zh from '@/i18n/locales/zh/admin/pluginInstances'
import type { PluginGroupBinding, PluginHookDeclaration } from '@/api/admin/pluginInstances'
const hook: PluginHookDeclaration = { name: 'check', stages: ['input'], protocols: ['openai.chat'], can_replace: false }
const binding: PluginGroupBinding = { instance_id: 7, group_id: 12, stage: 'input', entry: 'check', protocol: 'openai.chat', sort_order: 0, enabled: true, can_replace: false }
describe('configuration and Manifest binding validation', () => {
  it('accepts only complete JSON objects without altering their values', () => {
    expect(parseConfig('{"nested":{"list":[1,null]},"token":"value"}')).toEqual({ nested: { list: [1, null] }, token: 'value' })
    expect(parseConfig('{}')).toEqual({})
  })
  it.each(['', '{', 'null', '[]', '1', '"text"', 'true'])('rejects %j', value => expect(parseConfig(value)).toBeNull())
  it('accepts declared bindings and an empty set', () => {
    expect(bindingsValid([binding], [hook], 7)).toBe(true)
    expect(bindingsValid([], [], 7)).toBe(true)
  })
  it.each([
    { instance_id: 8 }, { group_id: 0 }, { group_id: -1 }, { group_id: 1.5 }, { group_id: Number.MAX_SAFE_INTEGER + 1 },
    { entry: 'unknown' }, { stage: 'output' }, { protocol: 'unknown' }, { sort_order: 0.5 }, { sort_order: Infinity }, { can_replace: true }
  ])('rejects invalid binding %j', patch => expect(bindingsValid([{ ...binding, ...patch } as PluginGroupBinding], [hook], 7)).toBe(false))
  it('rejects duplicate keys but permits the same entry in different groups', () => {
    expect(bindingsValid([binding, { ...binding, sort_order: 1 }], [hook], 7)).toBe(false)
    expect(bindingsValid([binding, { ...binding, group_id: 13 }], [hook], 7)).toBe(true)
  })
  it('accepts replacement only when explicitly declared', () => expect(bindingsValid([{ ...binding, can_replace: true }], [{ ...hook, can_replace: true }], 7)).toBe(true))
  it('matches the server count and sort-order bounds', () => {
    for (const sort_order of [-100001, 100001]) expect(bindingsValid([{ ...binding, sort_order }], [hook], 7)).toBe(false)
    for (const sort_order of [-100000, 100000]) expect(bindingsValid([{ ...binding, sort_order }], [hook], 7)).toBe(true)
    expect(bindingsValid(Array.from({ length: 129 }, (_, i) => ({ ...binding, group_id: i + 1 })), [hook], 7)).toBe(false)
    expect(bindingsValid(Array.from({ length: 128 }, (_, i) => ({ ...binding, group_id: i + 1 })), [hook], 7)).toBe(true)
  })
  it('contains readable Chinese text and equivalent interpolation placeholders', () => {
    for (const [key, english] of Object.entries(en.pluginInstances)) {
      const chinese = zh.pluginInstances[key as keyof typeof zh.pluginInstances]
      expect(chinese, key).toMatch(/[\u4e00-\u9fff]/)
      expect(chinese, key).not.toContain('??')
      expect(chinese.match(/\{\w+\}/g)?.sort() || [], key).toEqual(english.match(/\{\w+\}/g)?.sort() || [])
    }
  })

})
