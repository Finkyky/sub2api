import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { adminOperations } from '@/permissions/adminOperations'
import { adminPermissionCatalog } from '@/permissions/catalog'
import { adminRoutePolicies } from '@/permissions/policy'
const read = (p: string) => readFileSync(resolve(process.cwd(), 'src', p), 'utf8')
describe('M6 sections and exact permission wiring', () => {
 it.each([['GET','plugin-tasks','plugin_tasks.read'], ['GET','plugin-tasks/:id','plugin_tasks.read'], ['POST','plugin-tasks','plugin_tasks.create'], ['PUT','plugin-tasks/:id','plugin_tasks.update'], ['DELETE','plugin-tasks/:id','plugin_tasks.delete'], ['POST','plugin-tasks/:id/run','plugin_tasks.run'], ['GET','plugin-tasks/:id/runs','plugin_runs.read'], ['GET','plugin-runs/:id','plugin_runs.read'], ['GET','plugin-runs/:id/sensitive','plugin_runs.sensitive']])('maps %s %s exactly to %s', (method,path,permission) => {
  expect(adminOperations[`${method} /api/v1/admin/${path}`]).toEqual({ permission }); expect(adminPermissionCatalog.has(permission)).toBe(true)
 })
 it('reuses the extracted instances panel without nested AppLayout', () => {
  expect(read('views/admin/PluginInstancesView.vue')).toContain('<PluginInstancesPanel'); expect(read('components/admin/plugin/PluginInstancesPanel.vue')).not.toContain('AppLayout')
  for (const panel of ['PluginTasksPanel','PluginRunsPanel','PluginGroupBindingsPanel']) expect(read(`components/admin/plugin/${panel}.vue`)).not.toContain('AppLayout')
 })
 it('shows effective chain on the actual GroupsView and stages on PluginsView', () => { expect(read('views/admin/GroupsView.vue')).toContain('PluginGroupBindingsPanel'); expect(read('views/admin/PluginsView.vue')).toContain('PluginGroupBindingsPanel') })
 it('has separately authorized task and run routes and menu entries', () => {
  for (const [path,permission] of [['plugin-tasks','plugin_tasks.read'], ['plugin-runs','plugin_runs.read']]) { expect(adminRoutePolicies[`/admin/${path}`]).toEqual({ permission }); expect(read('router/index.ts')).toContain(`/admin/${path}`); expect(read('components/layout/AppSidebar.vue')).toContain(`/admin/${path}`) }
 })
})

describe('M6 localized display text', () => {
  it('contains real Chinese translations rather than encoding placeholders', () => {
    const locale = read('i18n/locales/zh/admin/pluginTasks.ts')
    expect(locale).toContain('插件任务'); expect(locale).toContain('宿主在任务运行前准备并读取'); expect(locale).not.toContain('???')
    expect(read('i18n/locales/zh/common.ts')).toContain("pluginTasks: '插件任务'")
  })
})
