import { mount } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import ResponseResultCacheField from '../ResponseResultCacheField.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (k: string) => k }) }))
const defaults = () => ({ enabled: false, ttl_seconds: 30, max_response_bytes: 2097152, allow_group_sharing: false })
describe('ResponseResultCacheField', () => {
 it('starts disabled and emits explicit enabling', async () => {
  const w = mount(ResponseResultCacheField, { props: { modelValue: defaults() } })
  expect(w.find('[data-testid="cache-ttl"]').exists()).toBe(false)
  await w.find('[data-testid="cache-enabled"]').setValue(true)
  expect(w.emitted('update:modelValue')?.[0]?.[0]).toEqual({ ...defaults(), enabled: true })
 })
 it('exposes bounds and sharing only when enabled', async () => {
  const w = mount(ResponseResultCacheField, { props: { modelValue: { ...defaults(), enabled: true } } })
  expect(w.find('[data-testid="cache-ttl"]').attributes('max')).toBe('86400')
  await w.find('[data-testid="cache-sharing"]').setValue(true)
  expect(w.emitted('update:modelValue')?.[0]?.[0]).toEqual({ ...defaults(), enabled: true, allow_group_sharing: true })
 })
 it('respects read-only permissions', () => {
  const w = mount(ResponseResultCacheField, { props: { modelValue: { ...defaults(), enabled: true }, disabled: true } })
  expect(w.findAll('input').every(e => e.attributes('disabled') !== undefined)).toBe(true)
 })
})

describe('ResponseResultCacheField write payload', () => {
 it('does not re-emit server revision or unrelated metadata', async () => {
  const value = { ...defaults(), revision: 99, internal_flag: true }
  const w = mount(ResponseResultCacheField, { props: { modelValue: value } })
  await w.find('[data-testid="cache-enabled"]').setValue(true)
  expect(w.emitted('update:modelValue')?.[0]?.[0]).toEqual({ ...defaults(), enabled: true })
 })
 it('emits TTL and size updates without changing sharing or enabling flags', async () => {
  const value = { ...defaults(), enabled: true, allow_group_sharing: true }
  const w = mount(ResponseResultCacheField, { props: { modelValue: value } })
  await w.find('[data-testid="cache-ttl"]').setValue('60')
  expect(w.emitted('update:modelValue')?.[0]?.[0]).toEqual({ ...value, ttl_seconds: 60 })
  await w.find('[data-testid="cache-size"]').setValue('4096')
  expect(w.emitted('update:modelValue')?.[1]?.[0]).toEqual({ ...value, max_response_bytes: 4096 })
 })
})
