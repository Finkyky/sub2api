import { apiClient } from '../client'

export interface PluginInstance {
  id: number
  plugin_id: number
  name: string
  generation: number
  current_revision: number
  latest_revision: number
  enabled: boolean
}

export interface PluginInstanceRevision {
  instance_id: number
  revision: number
  config_version: number
}

export interface PluginGroupBinding {
  id?: number
  instance_id: number
  group_id: number
  stage: 'input' | 'output'
  entry: string
  protocol: string
  sort_order: number
  enabled: boolean
  can_replace: boolean
}

export interface PluginInstanceRuntimeStatus {
  revision: number
  lease_refs: number
  local_current: boolean
  retired: boolean
  closed: boolean
}

// Publication is database-backed; runtime observations only describe this node.
export interface PluginInstanceStatus {
  instance_id: number
  enabled: boolean
  current_revision: number
  runtime_present: boolean
  lease_refs: number
  retired: boolean
  closed: boolean
  last_error: string
  runtimes: PluginInstanceRuntimeStatus[]
}

export interface PluginHookDeclaration {
  name: string
  stages: PluginGroupBinding['stage'][]
  protocols: string[]
  can_replace?: boolean
}

// schema2 currently declares hook options, but no configuration schema.
export interface PluginInstanceManifest {
  general?: {
    config_version: number
    hooks?: PluginHookDeclaration[]
  }
  [key: string]: unknown
}

export interface PluginInstanceDetail {
  instance: PluginInstance
  revision: PluginInstanceRevision
  manifest: PluginInstanceManifest
  bindings: PluginGroupBinding[]
}

export interface CreatePluginInstanceRequest {
  plugin_id: number
  name: string
  config: Record<string, unknown>
}

export interface UpdatePluginInstanceRequest {
  expected_generation: number
  config: Record<string, unknown>
}

export interface PublishPluginInstanceRequest {
  expected_generation: number
  revision: number
}

export interface BindingUpdateRequest {
  expected_generation: number
  bindings: PluginGroupBinding[]
}

async function unwrap<T>(promise: Promise<{ data: T }>): Promise<T> {
  const { data } = await promise
  return data
}

// Go's public projection represents an empty binding slice as null.
async function unwrapDetail(promise: Promise<{ data: Omit<PluginInstanceDetail, 'bindings'> & { bindings: PluginGroupBinding[] | null } }>): Promise<PluginInstanceDetail> {
  const { data } = await promise
  return { ...data, bindings: data.bindings ?? [] }
}

export const pluginInstancesAPI = {
  list(pluginId?: number) {
    return unwrap<PluginInstance[]>(apiClient.get('/admin/plugin-instances', { params: pluginId ? { plugin_id: pluginId } : undefined }))
  },
  get(id: number) {
    return unwrapDetail(apiClient.get(`/admin/plugin-instances/${id}`))
  },
  status(id: number) {
    return unwrap<PluginInstanceStatus>(apiClient.get(`/admin/plugin-instances/${id}/status`))
  },
  revisions(id: number) {
    return unwrap<PluginInstanceRevision[]>(apiClient.get(`/admin/plugin-instances/${id}/revisions`))
  },
  create(request: CreatePluginInstanceRequest) {
    return unwrapDetail(apiClient.post('/admin/plugin-instances', request))
  },
  saveConfig(id: number, request: UpdatePluginInstanceRequest) {
    return unwrapDetail(apiClient.put(`/admin/plugin-instances/${id}/config`, request))
  },
  publish(id: number, request: PublishPluginInstanceRequest) {
    return unwrapDetail(apiClient.post(`/admin/plugin-instances/${id}/publish`, request))
  },
  disable(id: number, expectedGeneration: number) {
    return unwrapDetail(apiClient.post(`/admin/plugin-instances/${id}/disable`, { expected_generation: expectedGeneration }))
  },
  delete(id: number, expectedGeneration: number) {
    return unwrap<{ deleted: boolean }>(apiClient.delete(`/admin/plugin-instances/${id}`, { data: { expected_generation: expectedGeneration } }))
  },
  replaceBindings(id: number, request: BindingUpdateRequest) {
    return unwrapDetail(apiClient.put(`/admin/plugin-instances/${id}/bindings`, request))
  }
}

export default pluginInstancesAPI
