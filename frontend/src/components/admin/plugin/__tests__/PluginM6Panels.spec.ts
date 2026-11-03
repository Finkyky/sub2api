import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive, nextTick, watch } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PluginTasksPanel from '../PluginTasksPanel.vue'
import PluginRunsPanel from '../PluginRunsPanel.vue'
import PluginGroupBindingsPanel from '../PluginGroupBindingsPanel.vue'
import { emptyScope, taskScope, taskValid } from '../taskForm'
const mocks = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), delete: vi.fn(), run: vi.fn(), runs: vi.fn(), runGet: vi.fn(), sensitive: vi.fn(), instances: vi.fn(), detail: vi.fn() }))
vi.mock('@/api/admin/pluginTasks', async original => ({ ...await original<typeof import('@/api/admin/pluginTasks')>(), pluginTasksAPI: mocks, pluginRunsAPI: { get: mocks.runGet, sensitive: mocks.sensitive } }))
vi.mock('@/api/admin', () => ({ adminAPI: { pluginInstances: { list: mocks.instances, get: mocks.detail } } }))
let auth: { permissions: string[]; hasPermission: (p: string) => boolean }
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('vue-i18n', async original => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (k: string) => k }) }))
const task = { id: 3, instance_id: 7, name: 'Sync', entry: 'sync', cron: '0 * * * *', enabled: true, timeout_seconds: 30, max_retries: 2, retry_backoff_seconds: 5, generation: 4, input: { token: 'INPUT_SECRET' }, scope: { channel_ids: [2], pricing_fields: ['price'], network_urls: ['https://host.test'], kv_keys: ['cursor'] }, next_run_at: null }
const run = { id: 9, task_id: 3, instance_id: 7, config_revision: 1, task_generation: 4, trigger: 'manual', idempotency_key: 'key', attempt: 1, fencing_token: 2, status: 'succeeded', changed: true, error_code: '', started_at: null, finished_at: null, duration_ms: 50, output: 'OUTPUT_SECRET' }
const all = ['plugin_tasks.read','plugin_tasks.create','plugin_tasks.update','plugin_tasks.delete','plugin_tasks.run','plugin_runs.read','plugin_runs.sensitive','plugin_instances.read','groups.read']
const wrappers: VueWrapper[] = []
const global = { stubs: { BaseDialog: { props: ['show','title'], template: '<section v-if="show"><h2>{{ title }}</h2><slot/><slot name="footer"/></section>' }, TotpStepUpDialog: true, PluginInstancesPanel: true, RouterLink: { template: '<a><slot/></a>' } } }
function panel(component: any, props: any = {}) { const w = mount(component, { props, global }); wrappers.push(w); return w }
async function edit(w: VueWrapper) { await flushPromises(); await w.get('[data-testid="edit-task"]').trigger('click'); await flushPromises() }
beforeEach(() => {
 vi.resetAllMocks(); auth = reactive({ permissions: [...all], hasPermission: (p: string) => auth.permissions.includes(p) })
 mocks.list.mockResolvedValue([{ ...task }]); mocks.get.mockResolvedValue({ ...task }); mocks.create.mockResolvedValue(task); mocks.update.mockResolvedValue({ ...task, generation: 5 }); mocks.delete.mockResolvedValue(undefined); mocks.run.mockResolvedValue(run)
 mocks.runs.mockResolvedValue([run]); mocks.runGet.mockResolvedValue(run); mocks.sensitive.mockResolvedValue({ run, result: { operations: [{ value: 'RAW_SECRET' }] } })
 mocks.instances.mockResolvedValue([{ id: 7, enabled: true, current_revision: 1 }, { id: 8, enabled: false, current_revision: 1 }]); mocks.detail.mockImplementation((id: number) => Promise.resolve({ instance: { id, name: 'Instance '+id, enabled: id === 7, current_revision: 1 }, bindings: [{ id, instance_id: id, group_id: 12, stage: 'input', protocol: 'openai.chat', entry: 'check', sort_order: 2, enabled: true, can_replace: false }, { id: 100 + id, instance_id: id, group_id: 12, stage: 'output', protocol: 'openai.chat', entry: 'redact', sort_order: 1, enabled: false, can_replace: true }] }))
})
afterEach(() => wrappers.splice(0).forEach(w => w.unmount()))
describe('M6 task panel', () => {
 it('supports readonly task metadata without exposing input or write controls', async () => {
  auth.permissions = ['plugin_tasks.read']; const w = panel(PluginTasksPanel, { instanceId: 7 }); await flushPromises()
  expect(mocks.list).toHaveBeenCalledWith(7); expect(w.text()).toContain('Sync'); expect(w.text()).not.toContain('INPUT_SECRET')
  for (const id of ['create-task','edit-task','delete-task','run-task']) expect(w.find(`[data-testid="${id}"]`).exists()).toBe(false)
 })
 it('does not read when only write permissions exist', async () => { auth.permissions = ['plugin_tasks.create']; panel(PluginTasksPanel); await flushPromises(); expect(mocks.list).not.toHaveBeenCalled() })
 it('fetches detail before edit and updates the exact generation without echoing errors', async () => {
  const w = panel(PluginTasksPanel, { instanceId: 7 }); await edit(w); expect(mocks.get).toHaveBeenCalledWith(3)
  await w.get('[data-testid="task-name"]').setValue('Changed'); await w.get('[data-testid="save-task"]').trigger('click'); await flushPromises()
  const body = mocks.update.mock.calls[0][1]; expect(body).toMatchObject({ name: 'Changed', instance_id: 7, expected_generation: 4, input: task.input, scope: task.scope }); expect(body).not.toHaveProperty('generation'); expect(body).not.toHaveProperty('id')
 })
 it('creates a full task, validates object input and scope, and excludes server fields', async () => {
  const w = panel(PluginTasksPanel, { instanceId: 7 }); await flushPromises(); await w.get('[data-testid="create-task"]').trigger('click')
  await w.get('[data-testid="task-name"]').setValue('New'); await w.get('[data-testid="task-entry"]').setValue('sync'); await w.get('[data-testid="task-cron"]').setValue('0 * * * *')
  await w.get('[data-testid="task-input"]').setValue('[]'); await w.get('[data-testid="save-task"]').trigger('click'); expect(mocks.create).not.toHaveBeenCalled()
  await w.get('[data-testid="task-input"]').setValue('{}'); await w.get('[data-testid="save-task"]').trigger('click'); await flushPromises()
  expect(mocks.create.mock.calls[0][0]).toMatchObject({ instance_id: 7, name: 'New', entry: 'sync', cron: '0 * * * *', input: {} }); expect(mocks.create.mock.calls[0][0]).not.toHaveProperty('generation')
 })
 it('deletes only after confirmation with expected_generation', async () => {
  const w = panel(PluginTasksPanel); await flushPromises(); await w.get('[data-testid="delete-task"]').trigger('click'); expect(mocks.delete).not.toHaveBeenCalled()
  await w.get('[data-testid="confirm-delete-task"]').trigger('click'); await flushPromises(); expect(mocks.delete).toHaveBeenCalledWith(3, 4)
 })
 it('refreshes 409 without retrying writes or rendering raw server errors', async () => {
  mocks.update.mockRejectedValue({ status: 409, message: 'RAW_SERVER_SECRET' }); const w = panel(PluginTasksPanel); await edit(w)
  await w.get('[data-testid="save-task"]').trigger('click'); await flushPromises()
  expect(mocks.update).toHaveBeenCalledTimes(1); expect(mocks.list).toHaveBeenCalledTimes(2); expect(w.text()).toContain('admin.pluginTasks.conflict'); expect(w.text()).not.toContain('RAW_SERVER_SECRET')
 })
 it('uses randomUUID for run and reuses its key across real step-up retry', async () => {
  mocks.run.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }).mockResolvedValueOnce(run)
  const w = panel(PluginTasksPanel); await flushPromises(); await w.get('[data-testid="run-task"]').trigger('click'); await flushPromises()
  const controller = w.findComponent({ name: 'TotpStepUpDialog' }).props('controller'); controller.onVerified(); await flushPromises()
  expect(mocks.run).toHaveBeenCalledTimes(2); const key = mocks.run.mock.calls[0][1]; expect(key).toMatch(/^[0-9a-f-]{36}$/i); expect(mocks.run.mock.calls[1][1]).toBe(key); expect(w.text()).not.toContain('OUTPUT_SECRET')
 })
 it('hides and clears a draft across revoke/regrant and rejects a late detail', async () => {
  const w = panel(PluginTasksPanel); await edit(w); await w.get('[data-testid="task-input"]').setValue('{"secret":"DRAFT_SECRET"}')
  auth.permissions = ['plugin_tasks.read']; await nextTick(); expect(w.find('[data-testid="task-input"]').exists()).toBe(false)
  auth.permissions = [...all]; await nextTick(); expect(w.find('[data-testid="task-input"]').exists()).toBe(false)
  let resolve!: (v: unknown) => void; mocks.get.mockImplementationOnce(() => new Promise(r => { resolve = r }))
  await w.get('[data-testid="edit-task"]').trigger('click'); auth.permissions = []; await nextTick(); auth.permissions = [...all]; await nextTick(); resolve(task); await flushPromises(); expect(w.find('[data-testid="task-input"]').exists()).toBe(false)
 })
 it('does not resurrect an in-flight form or step-up after write revocation', async () => {
  let reject!: (v: unknown) => void; mocks.update.mockImplementationOnce(() => new Promise((_,r) => { reject = r })); const w = panel(PluginTasksPanel); await edit(w); await w.get('[data-testid="save-task"]').trigger('click')
  auth.permissions = ['plugin_tasks.read']; await nextTick(); auth.permissions = [...all]; await nextTick(); reject({ code: 'STEP_UP_REQUIRED' }); await flushPromises()
  expect(w.findComponent({ name: 'TotpStepUpDialog' }).props('controller').visible.value).toBe(false); expect(mocks.update).toHaveBeenCalledTimes(1)
 })
 it('handles configuration/runtime failure with generic text and no raw data', async () => {
  mocks.run.mockRejectedValue({ status: 422, message: 'PRIVATE_CONFIG', metadata: { secret: true } }); const w = panel(PluginTasksPanel); await flushPromises(); await w.get('[data-testid="run-task"]').trigger('click'); await flushPromises()
  expect(w.text()).toContain('admin.pluginTasks.requestFailed'); expect(w.text()).not.toContain('PRIVATE_CONFIG'); expect(mocks.run).toHaveBeenCalledTimes(1)
 })
})
describe('M6 run metadata and explicit sensitive access', () => {
 it('requires runs.read, not tasks.read or sensitive alone', async () => { auth.permissions = ['plugin_runs.sensitive']; panel(PluginRunsPanel, { taskId: 3 }); await flushPromises(); expect(mocks.runs).not.toHaveBeenCalled() })
 it('reads metadata without output, opens detail, and only fetches raw result explicitly', async () => {
  const log = vi.spyOn(console, 'log'); const w = panel(PluginRunsPanel, { taskId: 3 }); await flushPromises(); expect(mocks.runs).toHaveBeenCalledWith(3); expect(mocks.sensitive).not.toHaveBeenCalled(); expect(w.text()).not.toContain('OUTPUT_SECRET')
  await w.get('[data-testid="run-details"]').trigger('click'); await flushPromises(); expect(mocks.runGet).toHaveBeenCalledWith(9)
  await w.get('[data-testid="show-sensitive"]').trigger('click'); await flushPromises(); expect(w.text()).toContain('RAW_SECRET'); expect(log).not.toHaveBeenCalled(); log.mockRestore()
  auth.permissions = ['plugin_runs.read']; await nextTick(); expect(w.text()).not.toContain('RAW_SECRET'); expect(w.find('[data-testid="show-sensitive"]').exists()).toBe(false)
 })
 it('rejects late sensitive results even across revoke/regrant', async () => {
  let resolve!: (v: unknown) => void; mocks.sensitive.mockImplementationOnce(() => new Promise(r => { resolve = r })); const w = panel(PluginRunsPanel, { taskId: 3 }); await flushPromises(); await w.get('[data-testid="run-details"]').trigger('click'); await flushPromises(); await w.get('[data-testid="show-sensitive"]').trigger('click')
  auth.permissions = ['plugin_runs.read']; await nextTick(); auth.permissions = [...all]; await nextTick(); resolve({ run, result: { secret: 'LATE_SECRET' } }); await flushPromises(); expect(w.text()).not.toContain('LATE_SECRET')
 })
 it('rejects sensitive results revoked in the final assignment microtask window', async () => {
  let resolve!: (value: unknown) => void
  mocks.sensitive.mockImplementationOnce(() => new Promise(r => { resolve = r }))
  const w = panel(PluginRunsPanel, { taskId: 3 })
  await flushPromises()
  await w.get('[data-testid="run-details"]').trigger('click')
  await flushPromises()
  const state = w.vm as unknown as { busy: boolean; rawResult: unknown }
  let revokedInWindow = false
  // execute() clears busy in finally, after its last valid() check. Queue the
  // revoke/regrant before the awaiting caller resumes, without counting ticks.
  const stop = watch(() => state.busy, (busy, wasBusy) => {
   if (wasBusy && !busy) queueMicrotask(() => {
    revokedInWindow = true
    auth.permissions = ['plugin_runs.read']
    auth.permissions = [...all]
   })
  }, { flush: 'sync' })
  try {
   await w.get('[data-testid="show-sensitive"]').trigger('click')
   resolve({ run, result: { secret: 'MICROTASK_SECRET' } })
   await flushPromises()
   expect(revokedInWindow).toBe(true)
   expect(mocks.sensitive).toHaveBeenCalledTimes(1)
   expect(state.rawResult).toBeNull()
   expect(w.find('[data-testid="sensitive-result"]').exists()).toBe(false)
   expect(w.text()).not.toContain('MICROTASK_SECRET')
  } finally { stop() }
 })
 it('clears run details on read revocation and task change', async () => { const w = panel(PluginRunsPanel, { taskId: 3 }); await flushPromises(); await w.get('[data-testid="run-details"]').trigger('click'); await flushPromises(); auth.permissions = []; await nextTick(); expect(w.text()).not.toContain('succeeded'); await w.setProps({ taskId: 4 }); expect(mocks.runs).toHaveBeenCalledTimes(1) })
})
describe('M6 business-group effective chains', () => {
 it('preserves enabled bindings including disabled blockers and separates phases/protocols', async () => {
  const w = panel(PluginGroupBindingsPanel, { groupId: 12 }); await flushPromises()
  expect(w.get('[data-testid="effective-chain"]').text()).toContain('check'); expect(w.get('[data-testid="effective-chain"]').text()).not.toContain('redact'); expect(w.get('[data-testid="effective-chain"]').text()).toContain('Instance 8'); expect(w.get('[data-testid="chain-blocked"]').text()).toContain('admin.pluginBindings.blocked'); expect(w.get('[data-testid="binding-blocker"]').text()).toContain('admin.pluginBindings.disabled'); expect(w.text()).toContain('admin.pluginBindings.output')
 })
 it('fails closed on partial detail read failure and never leaks raw failures', async () => {
  mocks.detail.mockRejectedValueOnce({ message: 'PRIVATE_CONFIG' }); const w = panel(PluginGroupBindingsPanel, { groupId: 12 }); await flushPromises(); expect(w.find('[data-testid="effective-chain"]').exists()).toBe(false); expect(w.text()).not.toContain('PRIVATE_CONFIG')
 })
 it('does not read for an unprivileged group reader and clears after revoke', async () => {
  auth.permissions = ['groups.read']; const w = panel(PluginGroupBindingsPanel, { groupId: 12 }); await flushPromises(); expect(mocks.instances).not.toHaveBeenCalled(); auth.permissions = [...all]; await nextTick(); await flushPromises(); auth.permissions = ['groups.read']; await nextTick(); expect(w.text()).not.toContain('check')
 })
})

describe('M6 conflict / revocation edge cases', () => {
 it.each(['create', 'delete', 'run'])('refreshes a %s 409 once and never automatically replays it', async action => {
  mocks[action].mockRejectedValue({ response: { status: 409 }, message: 'RAW_SECRET' }); const w = panel(PluginTasksPanel, { instanceId: 7 }); await flushPromises()
  if (action === 'create') { await w.get('[data-testid="create-task"]').trigger('click'); await w.get('[data-testid="task-name"]').setValue('New'); await w.get('[data-testid="task-entry"]').setValue('sync'); await w.get('[data-testid="task-cron"]').setValue('* * * * *'); await w.get('[data-testid="save-task"]').trigger('click') }
  else if (action === 'delete') { await w.get('[data-testid="delete-task"]').trigger('click'); await w.get('[data-testid="confirm-delete-task"]').trigger('click') }
  else await w.get('[data-testid="run-task"]').trigger('click')
  await flushPromises(); expect(mocks[action]).toHaveBeenCalledTimes(1); expect(mocks.list).toHaveBeenCalledTimes(2); expect(w.text()).not.toContain('RAW_SECRET')
 })
 it.each(['run', 'delete'])('cancels %s while the real OTP prompt is open, permanently across regrant', async action => {
  mocks[action].mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }); const w = panel(PluginTasksPanel); await flushPromises()
  if (action === 'delete') { await w.get('[data-testid="delete-task"]').trigger('click'); await w.get('[data-testid="confirm-delete-task"]').trigger('click') } else await w.get('[data-testid="run-task"]').trigger('click')
  await flushPromises(); const controller = w.findComponent({ name: 'TotpStepUpDialog' }).props('controller'); expect(controller.visible.value).toBe(true)
  auth.permissions = auth.permissions.filter(p => p !== `plugin_tasks.${action}`); await nextTick(); auth.permissions = [...all]; await nextTick(); controller.onVerified(); await flushPromises(); expect(mocks[action]).toHaveBeenCalledTimes(1); expect(controller.visible.value).toBe(false)
 })
 it('ignores a stale task list after revoke/regrant and an instance context change', async () => {
  let resolve!: (v: unknown) => void; mocks.list.mockImplementationOnce(() => new Promise(r => { resolve = r })).mockResolvedValue([]); const w = panel(PluginTasksPanel, { instanceId: 7 }); await flushPromises()
  auth.permissions = []; await nextTick(); auth.permissions = [...all]; await nextTick(); await w.setProps({ instanceId: 8 }); resolve([{ ...task, name: 'LATE_TASK_SECRET' }]); await flushPromises(); expect(w.text()).not.toContain('LATE_TASK_SECRET')
 })
 it('ignores a late run detail after read revoke/regrant', async () => {
  let resolve!: (v: unknown) => void; mocks.runGet.mockImplementationOnce(() => new Promise(r => { resolve = r })); const w = panel(PluginRunsPanel, { taskId: 3 }); await flushPromises(); await w.get('[data-testid="run-details"]').trigger('click')
  auth.permissions = []; await nextTick(); auth.permissions = [...all]; await nextTick(); resolve({ ...run, status: 'LATE_RUN_SECRET' }); await flushPromises(); expect(w.text()).not.toContain('LATE_RUN_SECRET')
 })
 it('shows no raw error or partial chain after configuration/detail failure', async () => {
  mocks.get.mockRejectedValue({ status: 500, message: 'PRIVATE_CONFIG' }); const w = panel(PluginTasksPanel); await flushPromises(); await w.get('[data-testid="edit-task"]').trigger('click'); await flushPromises(); expect(w.find('[data-testid="task-input"]').exists()).toBe(false); expect(w.text()).not.toContain('PRIVATE_CONFIG')
 })
 it.each(['null','[]','{"channel_ids":[0],"pricing_fields":[],"network_urls":[],"kv_keys":[]}','{"channel_ids":[],"pricing_fields":[2],"network_urls":[],"kv_keys":[]}'])('rejects invalid scope %s before sending any write', async scope => {
  const w = panel(PluginTasksPanel); await edit(w); await w.get('[data-testid="task-scope"]').setValue(scope); await w.get('[data-testid="save-task"]').trigger('click'); expect(mocks.update).not.toHaveBeenCalled()
 })
 it('clears explicit raw results on task context change without reading tasks', async () => {
  const w = panel(PluginRunsPanel, { taskId: 3 }); await flushPromises(); await w.get('[data-testid="run-details"]').trigger('click'); await flushPromises(); await w.get('[data-testid="show-sensitive"]').trigger('click'); await flushPromises(); expect(w.text()).toContain('RAW_SECRET'); await w.setProps({ taskId: 4 }); await flushPromises(); expect(w.text()).not.toContain('RAW_SECRET'); expect(mocks.get).not.toHaveBeenCalled()
 })
 it('sorts effective chain by order then binding ID, separately per protocol/stage', async () => {
  mocks.instances.mockResolvedValue([{ id: 7 }]); mocks.detail.mockResolvedValue({ instance: { id: 7, name: 'Checker', enabled: true, current_revision: 2 }, bindings: [
   { id: 5, instance_id: 7, group_id: 12, stage: 'input', protocol: 'openai.chat', entry: 'Last', sort_order: 3, enabled: true },
   { id: 3, instance_id: 7, group_id: 12, stage: 'input', protocol: 'openai.chat', entry: 'Second', sort_order: 1, enabled: true },
   { id: 2, instance_id: 7, group_id: 12, stage: 'input', protocol: 'openai.chat', entry: 'First', sort_order: 1, enabled: true },
   { id: 1, instance_id: 7, group_id: 12, stage: 'output', protocol: 'anthropic.messages', entry: 'Output', sort_order: 0, enabled: true }
  ] }); const w = panel(PluginGroupBindingsPanel, { groupId: 12 }); await flushPromises(); const text = w.get('[data-testid="effective-chain"]').text(); expect(text.indexOf('First')).toBeLessThan(text.indexOf('Second')); expect(text.indexOf('Second')).toBeLessThan(text.indexOf('Last')); expect(text).toContain('Output'); expect(text).toContain('anthropic.messages')
 })
})

describe('task read details and execution confirmation', () => {
 it('lets a task reader explicitly inspect task details without showing edit forms', async () => {
  auth.permissions = ['plugin_tasks.read']; const w = panel(PluginTasksPanel); await flushPromises(); await w.get('[data-testid="task-details"]').trigger('click'); await flushPromises()
  expect(mocks.get).toHaveBeenCalledWith(3); expect(w.get('[data-testid="task-read-detail"]').text()).toContain('sync'); expect(w.get('[data-testid="task-read-detail"]').text()).toContain('channel_ids'); expect(w.find('[data-testid="task-input"]').exists()).toBe(false)
  auth.permissions = []; await nextTick(); expect(w.find('[data-testid="task-read-detail"]').exists()).toBe(false)
 })
 it('confirms an accepted run using metadata only and hides it on run revocation', async () => {
  const w = panel(PluginTasksPanel); await flushPromises(); await w.get('[data-testid="run-task"]').trigger('click'); await flushPromises()
  expect(w.get('[data-testid="accepted-run"]').text()).toContain('9'); expect(w.get('[data-testid="accepted-run"]').text()).toContain('succeeded'); expect(w.text()).not.toContain('OUTPUT_SECRET')
  auth.permissions = auth.permissions.filter(p => p !== 'plugin_tasks.run'); await nextTick(); expect(w.find('[data-testid="accepted-run"]').exists()).toBe(false)
 })
})

describe('M6 review: manual tasks, exact pricing scope and fail-closed chain', () => {
 it.each(['', '   '])('allows manual-only cron %j in task validation', cron => { const { id: _id, generation: _gen, next_run_at: _next, ...body } = task; expect(taskValid({ ...body, cron })).toBe(true) })
 it.each(['create', 'update'])('saves a %s task with empty cron and optional pricing IDs', async action => {
  const w = panel(PluginTasksPanel, { instanceId: 7 }); await flushPromises()
  if (action === 'create') { await w.get('[data-testid="create-task"]').trigger('click'); await w.get('[data-testid="task-name"]').setValue('Manual'); await w.get('[data-testid="task-entry"]').setValue('sync') } else await edit(w)
  await w.get('[data-testid="task-cron"]').setValue(''); await w.get('[data-testid="task-scope"]').setValue(JSON.stringify({ ...emptyScope(), pricing_ids: [11, 12] }))
  await w.get('[data-testid="save-task"]').trigger('click'); await flushPromises()
  expect(mocks[action]).toHaveBeenCalledTimes(1); expect(mocks[action].mock.calls[0][action === 'update' ? 1 : 0]).toMatchObject({ cron: '', scope: { pricing_ids: [11, 12] } })
 })
 it.each([undefined, [], [11, 12]])('accepts compatible optional pricing_ids %j without changing grant representation', ids => {
  const scope = ids === undefined ? emptyScope() : { ...emptyScope(), pricing_ids: ids }
  expect(taskScope(JSON.stringify(scope))).toEqual(scope)
 })
 it.each([null, '11', [0], [-1], [1.5], [1, 1], ['11'], [9007199254740992], {}, true])('rejects non-positive/non-unique pricing_ids %j', ids => { expect(taskScope(JSON.stringify({ ...emptyScope(), pricing_ids: ids }))).toBeNull() })
 it('passes omitted pricing_ids through an edited manual task for channel-wide compatibility', async () => {
  const w = panel(PluginTasksPanel); await edit(w); await w.get('[data-testid="task-cron"]').setValue(''); await w.get('[data-testid="save-task"]').trigger('click'); await flushPromises()
  expect(mocks.update).toHaveBeenCalledTimes(1); expect(mocks.update.mock.calls[0][1].scope).not.toHaveProperty('pricing_ids')
 })
 it('marks both phases unavailable for one disabled live binding without hiding the enabled companion', async () => {
  auth.permissions = ['groups.read', 'plugin_instances.read']
  mocks.instances.mockResolvedValue([{ id: 7 }, { id: 8 }]); mocks.detail.mockImplementation(id => Promise.resolve({ instance: { id, name: id === 7 ? 'Disabled bound' : 'Ready output', enabled: id !== 7, current_revision: 2 }, bindings: [{ id, instance_id: id, group_id: 12, stage: id === 7 ? 'input' : 'output', protocol: 'openai.chat', entry: 'check', enabled: true, sort_order: 1 }] }))
  const w = panel(PluginGroupBindingsPanel, { groupId: 12 }); await flushPromises()
  const chain = w.get('[data-testid="effective-chain"]'); expect(chain.text()).toContain('Disabled bound'); expect(chain.text()).toContain('Ready output'); expect(chain.findAll('[data-testid="chain-status"]').every(status => status.text().includes('admin.pluginBindings.blocked'))).toBe(true)
  expect(w.get('[data-testid="binding-blocker"]').text()).toContain('admin.pluginBindings.disabled'); expect(w.find('[data-testid="edit-binding-instance"]').exists()).toBe(false)
 })
 it.each([{ enabled: true, current_revision: 0, bindingId: 7, owner: 7, reason: 'unpublished' }, { enabled: true, current_revision: 2, bindingId: 0, owner: 7, reason: 'unavailable' }, { enabled: true, current_revision: 2, bindingId: 7, owner: 8, reason: 'unavailable' }])('retains enabled unavailable binding with $reason blocker', async state => {
  mocks.instances.mockResolvedValue([{ id: 7 }]); mocks.detail.mockResolvedValue({ instance: { id: 7, name: 'Unavailable bound', enabled: state.enabled, current_revision: state.current_revision }, bindings: [{ id: state.bindingId, instance_id: state.owner, group_id: 12, stage: 'input', protocol: 'openai.chat', entry: 'check', enabled: true, sort_order: 0 }] })
  const w = panel(PluginGroupBindingsPanel, { groupId: 12 }); await flushPromises(); expect(w.get('[data-testid="effective-chain"]').text()).toContain('Unavailable bound'); expect(w.get('[data-testid="binding-blocker"]').text()).toContain('admin.pluginBindings.' + state.reason)
 })
})
