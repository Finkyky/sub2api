# 通用插件 P2a 安装清单与兼容入口 Implementation Plan

> 按 executing-plans 与 test-driven-development 顺序实施。本轮不提交、不推送、不部署；保留当前工作区及 P1 补强。

**Goal:** 在现有 .s2plugin 上传安装入口接纳通用插件声明，保留旧传输插件兼容，并在通用运行时未完成时明确阻止错误启动。

**Architecture:** schema_version=1 保持旧传输协议。新增 schema_version=2、requires.general_api 和 general 声明，安装器复用签名／哈希／解包／持久化流程；schema2 不生成旧传输绑定。兼容性明确返回 incompatible + general_runtime_unavailable，旧运行时在创建进程前再次拒绝 schema2。没有数据库迁移，清单继续存储在现有 JSON 字段。

**Tech Stack:** Go、现有 .s2plugin ZIP、现有管理与存储接口；本轮不新增依赖或 RPC 代码。

## P2 切片

1. P2a（本轮）：清单与入库兼容；通用包可以上传存储，但不宣称能够启用或执行。
2. P2b：版本化 GeneralPlugin RPC、真实运行句柄、实例隔离、超时和资源约束；完成后才移除通用运行时兼容性门禁。
3. P2c：实例配置版本、分组绑定、发布／回滚／在途快照及数据库迁移。
4. P2d：管理 API、RBAC／step-up／审计、跨节点恢复与集成验收。

方案比较：直接扩展旧 TransportPlugin 会混淆传输独占与多实例检查语义；完全另建上传入口重复签名和存储逻辑。本轮采用共同包封装、版本化声明、分开运行入口。

## Schema 2

- requires.plugin_protocol=1、general_api=1、transport_api=0。
- 通用插件可无自定义 UI：ui.entrypoint 为空时 ui_bridge=0；有 UI 时保留 ui/ 路径、哈希声明与 ui_bridge=1 约束。
- capabilities 仅支持 request.hook.v1 与 independent.task.v1，不带 Platform/AccountType，不允许重复。它们描述执行方式，不是敏感词／价格等业务类型。
- general.config_version 是正整数。
- general.hooks 声明 name、stages（input/output）、protocols 和可选 can_replace。协议是命名空间标识，声明不等于宿主已经支持，绑定与网关接入时仍要检查协议支持。
- general.tasks 声明 name。定时计划和目标渠道属于实例／任务配置，不写死到插件包。
- general.host_capabilities 为请求的宿主能力标识；不是权限授予。P2b/P4 必须按实例授权，包声明本身不提供账号凭据或计费写权限。
- 每类入口最多 32 个，名称全局唯一，名称采用最多 64 字符的小写机器标识；每个 hook 支持 1–2 个不重复阶段及 1–16 个不重复协议标识；宿主能力最多 32 个，均为不重复命名空间标识。
- 声明能力与入口严格对应；未知能力、旧传输字段混入、未声明入口、空入口或重复字段值均拒绝。
- schema1 携带 general 或 general_api 不被悄悄忽略；未知 schema 仍拒绝。

示例核心字段（实际包还需 id、version、runtimes、files 等既有字段）：

```json
{
  "schema_version": 2,
  "requires": {"sub2api": ">=0.1.170 <0.2.0", "plugin_protocol": 1, "general_api": 1, "transport_api": 0, "ui_bridge": 0},
  "capabilities": [{"id": "request.hook.v1"}, {"id": "independent.task.v1"}],
  "general": {
    "config_version": 1,
    "hooks": [{"name": "inspect", "stages": ["input", "output"], "protocols": ["openai.chat_completions.v1"], "can_replace": false}],
    "tasks": [{"name": "sync_prices"}],
    "host_capabilities": ["channels.pricing.write"]
  }
}
```

## 写集与步骤

- [x] 保存基线 ZIP、哈希、Git 索引和插件回归结果。
- [x] 新增 plugin_general_manifest_test.go：合法输入／输出／独立任务／混合模式／可选 UI，版本、能力、阶段、协议、入口及声明数量边界；先观察 RED。
- [x] 新增 plugin_general_manifest.go；修改 plugin_manifest.go，将 schema1 原约束与 schema2 新声明分开，复用包文件校验。
- [x] 修改 plugin_compatibility.go：schema2 安装后兼容性给出明确 reason_code，不伪造运行支持；未知 schema 不通过。
- [x] 修改 plugin_manager.go：仅为已验证的旧传输清单生成旧绑定；通用安装零旧绑定。修改 plugin_runtime.go：进程启动前拒绝通用清单。
- [x] 新增 plugin_general_package_test.go：真实签名 ZIP 安装、headless／有 UI、哈希／签名失败、未声明字段拒绝、管理器入库零传输绑定及启用阻断。
- [x] 验证旧 schema1 安装与 capability 独占行为，执行 P1 核心、插件 service 定向测试、race、vet 和服务端构建。
- [x] 更新路线图与 .gitignore 的精确文档例外，生成增量与独立副本回滚证据。

## 验证命令

```powershell
go test ./internal/service -run 'PluginGeneral' -count=1 -v
go test ./internal/service -run Plugin -count=1 -timeout=180s
go test -race ./internal/service -run 'PluginGeneral|PluginPackage|EvaluatePluginCompatibility' -count=1 -timeout=180s
go test ./pkg/pluginapi/... ./internal/pluginflow -count=1
go vet ./internal/service ./pkg/pluginapi/... ./internal/pluginflow
go build -o D:/tmp/sub2api-plugin-p2a-20260930/server-check.exe ./cmd/server
```

旧真实子进程测试依赖 SUB2API_TEST_PLUGIN_PACKAGE；缺少测试包时应如实记录跳过，不将定向单元回归称为真实进程验收。数据库入库接口使用测试替身验证参数；本轮不声称已完成真实数据库集成。

## 交付边界

P2a 结束时只有上传／存储／声明校验能力；运行时、实例和分组绑定仍待 P2b–P2d。原 P1 串行检查及独立任务结果契约不改动。

## 实施与交接记录

- 新增 plugin_general_manifest.go、plugin_general_manifest_test.go、plugin_general_package_test.go。
- 修改清单类型／校验、兼容性、管理器与运行时入口；旧规范化配置回归用例补充真实 schema1 清单，原断言保持不变。
- 初始 RED 记录：新类型缺失；通用能力错误生成旧绑定；通用包进入二进制校验。
- 代码审查发现多节点更新后的缓存复用问题；四组 RED 复现了通用／已替换传输包的 SaveConfig/Test 仍调用旧运行时。修正后先检查清单，再核对缓存的包身份和安装 ID，四组均验证零 RPC、零配置写入。
- 通用清单、真实签名 ZIP 验证、管理器安装参数、启用／配置／测试门禁、旧插件定向回归、race、P1 核心、vet、服务端构建均通过。
- TestPluginRuntimeIntegration 因缺少 SUB2API_TEST_PLUGIN_PACKAGE 而跳过。签名 ZIP 测试的二进制是测试载荷；本轮没有完成真实子进程运行或真实数据库验收。
- 本轮未改数据库迁移、前端或生产网关。HostCapabilities 只是请求声明，未赋予渠道价格修改权限。
- 证据目录：D:\tmp\sub2api-plugin-p2a-20260930。增量 ZIP、差异、验证记录和回滚脚本均与本轮基线对应；回滚保留原 P1 与补强。

下一步：P2b 细化并实现 GeneralPlugin RPC、安装清单与握手交叉校验、独立实例配置及可固定运行句柄；在真实运行时通过验收前保留 general_runtime_unavailable 门禁。
