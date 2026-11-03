import { describe, expect, it } from 'vitest'
import { createDefaultResultCacheInput, resultCacheConfigToInput, isResultCacheInputValid } from '../groupsResultCache'

describe('group result cache form contract', () => {
 it('defaults old and new groups to disabled without a client revision', () => {
  const expected = { enabled: false, ttl_seconds: 30, max_response_bytes: 2097152, allow_group_sharing: false }
  expect(createDefaultResultCacheInput()).toEqual(expected)
  expect(resultCacheConfigToInput(undefined)).toEqual(expected)
  expect(resultCacheConfigToInput(null)).toEqual(expected)
 })
 it('hydrates an edit or copied configuration and strips server metadata', () => {
  expect(resultCacheConfigToInput({ enabled: true, ttl_seconds: 120, max_response_bytes: 4096, allow_group_sharing: true, revision: 999 })).toEqual({ enabled: true, ttl_seconds: 120, max_response_bytes: 4096, allow_group_sharing: true })
 })
 it('normalizes incomplete legacy projections without losing explicit false', () => {
  expect(resultCacheConfigToInput({ enabled: true, ttl_seconds: 0 })).toEqual({ enabled: true, ttl_seconds: 30, max_response_bytes: 2097152, allow_group_sharing: false })
 })
 it.each([1, 86400])('accepts TTL boundary %s', ttl_seconds => {
  expect(isResultCacheInputValid({ ...createDefaultResultCacheInput(), ttl_seconds })).toBe(true)
 })
 it.each([1024, 2097152])('accepts size boundary %s', max_response_bytes => {
  expect(isResultCacheInputValid({ ...createDefaultResultCacheInput(), max_response_bytes })).toBe(true)
 })
 it.each([0, -1, 86401, 1.5, NaN, Infinity])('rejects invalid TTL %s', ttl_seconds => {
  expect(isResultCacheInputValid({ ...createDefaultResultCacheInput(), ttl_seconds })).toBe(false)
 })
 it.each([0, 1023, 2097153, 1024.5, NaN, Infinity])('rejects invalid size %s', max_response_bytes => {
  expect(isResultCacheInputValid({ ...createDefaultResultCacheInput(), max_response_bytes })).toBe(false)
 })
})
