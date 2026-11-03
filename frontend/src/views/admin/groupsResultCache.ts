import type { ResponseResultCacheConfig, ResponseResultCacheInput } from '@/types'

export function createDefaultResultCacheInput(): ResponseResultCacheInput {
  return { enabled: false, ttl_seconds: 30, max_response_bytes: 2097152, allow_group_sharing: false }
}

// Read server metadata, but serialize only the four administrator-controlled fields.
export function resultCacheConfigToInput(config?: Partial<ResponseResultCacheConfig> | null): ResponseResultCacheInput {
  const defaults = createDefaultResultCacheInput()
  return {
    enabled: config?.enabled ?? defaults.enabled,
    ttl_seconds: config?.ttl_seconds === 0 ? defaults.ttl_seconds : config?.ttl_seconds ?? defaults.ttl_seconds,
    max_response_bytes: config?.max_response_bytes === 0 ? defaults.max_response_bytes : config?.max_response_bytes ?? defaults.max_response_bytes,
    allow_group_sharing: config?.allow_group_sharing ?? defaults.allow_group_sharing,
  }
}

export function isResultCacheInputValid(config: ResponseResultCacheInput): boolean {
  return Number.isInteger(config.ttl_seconds) && config.ttl_seconds >= 1 && config.ttl_seconds <= 86400
    && Number.isInteger(config.max_response_bytes) && config.max_response_bytes >= 1024 && config.max_response_bytes <= 2097152
}
