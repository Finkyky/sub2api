import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, it, expect } from 'vitest'
import { parse } from 'vue/compiler-sfc'
import { adminOperations } from '../adminOperations'
import { adminPermissionCatalog } from '../catalog'

const sourceRoot = resolve(process.cwd(), 'src')
describe('backend contract and rendered action coverage', () => {
  it('mirrors every exact registry operation, including POST reads and super-only capabilities', () => {
    const source = readFileSync(resolve(process.cwd(), '../backend/internal/server/middleware/admin_route_permission.go'), 'utf8')
    const expected = Object.fromEntries([...source.matchAll(/"((?:GET|POST|PUT|PATCH|DELETE) \/api\/v1\/admin\/[^"\n]+)":\s*\{(?:Permission: "([^"]+)"|(SuperAdminOnly: true))\}/g)].map(m => [m[1], m[3] ? { superOnly: true } : { permission: m[2] }]))
    expect(Object.keys(expected).length).toBeGreaterThanOrEqual(442)
    expect(adminOperations).toEqual(expected)
  })
  it('keeps the entry catalog in sync (empty and retired roles do not grant entry)', () => {
    const source = readFileSync(resolve(process.cwd(), '../backend/internal/service/permission_catalog.go'), 'utf8')
    expect([...adminPermissionCatalog].sort()).toEqual([...new Set([...source.matchAll(/Code: "([^"]+)"/g)].map(m => m[1]))].sort())
  })
  it('does not invent permission codes in action markers', () => {
    const files: string[] = []
    function scan(dir: string) { for (const e of readdirSync(dir, { withFileTypes: true })) { const p = resolve(dir, e.name); if (e.isDirectory()) scan(p); else if (p.endsWith('.vue')) files.push(p) } }
    scan(sourceRoot)
    let markers = 0
    for (const file of files) {
      const source = readFileSync(file, 'utf8')
      for (const match of source.matchAll(/v-permission="([^"]+)"/g)) {
        markers++
        for (const code of match[1].matchAll(/'([a-z_]+\.[a-z_.]+)'/g)) expect(adminPermissionCatalog.has(code[1]), `${file}: ${code[1]}`).toBe(true)
      }
    }
    expect(markers).toBeGreaterThan(280)
  })
  it.each([
    ['views/admin/ProxiesView.vue', 'copyProxyUrl(row)', "'proxies.export'"],
    ['views/admin/ProxiesView.vue', 'handleTestConnection(row)', "'proxies.update'"],
    ['views/admin/ProxiesView.vue', 'handleCreateProxy', "'proxies.create'"],
    ['views/admin/AccountsView.vue', 'handleDelete(row)', "'accounts.delete'"],
    ['views/admin/GroupsView.vue', 'handleDuplicate(row)', "'groups.create'"],
    ['components/admin/settings/AdminAccessKeysSettings.vue', 'edit()', "'@super'"],
    ['views/admin/BackupView.vue', 'downloadBackup(record.id)', "'@super'"],
    ['views/admin/PluginsView.vue', 'enablePlugin(plugin)', "'@super'"],
    ['views/admin/AuditLogView.vue', 'submitClear', "'audit_logs.delete'"],
    ['views/admin/RedeemView.vue', 'handleExportCodes', "'redeem_codes.export'"],
    ['views/admin/RiskControlView.vue', 'unbanUser(row)', "'risk_control.manage'"],
    ['features/prompt-audit/PromptAuditView.vue', 'saveConfig', "'prompt_audit.manage'"],
    ['features/prompt-audit/components/EventWorkspace.vue', "$emit('delete', event.id)", "'prompt_audit.delete'"],
    ['views/admin/ops/components/OpsSystemLogTable.vue', 'cleanupCurrentFilter', "'ops.manage'"],
    ['components/admin/account/ScheduledTestsPanel.vue', 'handleCreate', "'accounts.update'"],
    ['components/account/OllamaCloudUsageSettings.vue', 'deleteSession', "'accounts.update'"],
    ['components/admin/payment/AdminOrderTable.vue', "emit('refund', row)", "'payments.refund'"],
    ['components/admin/payment/AdminOrderTable.vue', "emit('retry', row)", "'payments.update'"],
  ])('%s gates %s with the reviewed operation permission', (file, handler, permission) => {
    const ast = parse(readFileSync(resolve(sourceRoot, file), 'utf8')).descriptor.template!.ast!
    const matches: string[] = []
    function visit(node: any) {
      if (node.type === 1 && node.props.some((p: any) => p.type === 7 && p.name === 'on' && p.exp?.content === handler)) {
        const gate = node.props.find((p: any) => p.type === 7 && p.name === 'permission')
        const condition = node.props.find((p: any) => p.type === 7 && p.name === 'if')
        matches.push(gate?.exp?.content ?? condition?.exp?.content ?? '')
      }
      for (const c of node.children ?? []) visit(c)
    }
    visit(ast)
    expect(matches.length).toBeGreaterThan(0)
    expect(matches.every(value => value.includes(permission))).toBe(true)
  })
})
