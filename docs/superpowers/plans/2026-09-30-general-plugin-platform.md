# 通用插件平台 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement the detailed phase plans task-by-task. Independent modules may use superpowers:subagent-driven-development only after delegation is authorized. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有插件安装基础上交付支持独立任务、按业务分组绑定的输入／输出检查、插件自主决策及严格输出阻断的通用平台。

**Architecture:** 保留现有 OpenAI OAuth 传输插件，将通用契约、分组执行链、任务调度和宿主业务能力拆开。业务规则由插件执行，宿主负责鉴权、绑定匹配、资源控制、结果校验及协议输出。请求处理链与传输适配器具有不同的多实例／独占语义。

**Tech Stack:** 当前 Go 后端、Gin、PostgreSQL、Redis、现有 `.s2plugin` 包与 gRPC 进程基础、Vue/TypeScript；第一阶段仅依赖 Go 标准库，不引入 JavaScript/WASM 运行时。

---

## 0. 计划性质与工作区约定

- 用户已确认设计稿 `docs/superpowers/specs/2026-09-30-general-plugin-platform-design.md`，本文件将其拆分为阶段路线图。
- 这是总计划，不把后续大模块的简述冒充可直接执行的逐文件补丁。每个阶段实施前必须有自己的细化计划；阶段一已同时提供完整代码、测试及命令。
- 首个执行文件：`docs/superpowers/plans/2026-09-30-general-plugin-contracts-phase1.md`。
- 当前分支 `feature/rbac-user-system` 存在大量先前未提交改动。计划生成阶段仅新增文档，不改业务代码、不提交、不推送。
- 开发阶段先保留工作区基线和变更清单，不使用 `reset --hard`、`clean` 或基于 HEAD 的整目录覆盖。
- 以下“预期测试结果”均为将来执行的验收要求，不是本次已经运行或通过的测试。

## 1. 固定产品约束

1. 开发完成后上传插件安装包；后台在线编辑器不列为首版必需功能。
2. 独立任务模式是脱离业务请求执行，不要求独立容器／远程服务。
3. 请求链插件只作用于实际绑定的业务分组；未绑定时不调用插件、不传递请求正文。
4. 规则、置信度判断、是否处理和拦截均在插件内部；宿主不二次按分数改判。
5. 请求链统一结果为 `pass`、`replace`、`block`；执行异常另行表示。
6. 严格输出检查通过之前，客户端不得收到正文。超限或异常不能自动透传。
7. 价格插件可以自动更新实际计费配置，不以人工确认作为必经步骤。
8. 现有传输插件契约及能力独占保持兼容，不将其直接变成可串联处理器。

## 2. 阶段依赖

```text
P1 契约与纯执行核心
  ↓
P2 安装兼容、实例、分组绑定、通用运行调用
  ├── P3 输入／输出网关接入与严格缓冲
  └── P4 独立任务调度与宿主业务能力
          ↓
P5 管理界面、配置、绑定、任务与日志
          ↓
P6 示例安装包、兼容性与全链路验收
```

P3 与 P4 只有在 P2 的接口冻结后才具备独立实施条件。P5 可先按稳定 API 契约开发，但其真实联调依赖 P3/P4。不能把所有阶段同时启动后再拼接协议。

## 3. P1：契约与纯执行核心

### 交付

- 版本化领域契约：阶段、插件决策、内容、拒绝信息、请求元数据。
- 插件结果校验：非法结果、未经授权的替换、非法置信度与错误状态被拒绝；合法分数不影响插件决定。
- 仅匹配实际分组的稳定排序选择器。
- 串行执行核心：`pass` 保留内容、`replace` 传递新内容、`block` 短路、错误不返回部分内容。
- 调用边界复制正文，插件不能通过修改输入内存绕过 `replace` 契约。
- 独立任务使用单独成功／失败结果，不混用请求链三态。

### 文件边界

新增：

- `backend/pkg/pluginapi/contracts/v1/hook.go`
- `backend/pkg/pluginapi/contracts/v1/hook_test.go`
- `backend/pkg/pluginapi/contracts/v1/task.go`
- `backend/pkg/pluginapi/contracts/v1/task_test.go`
- `backend/internal/pluginflow/binding.go`
- `backend/internal/pluginflow/binding_test.go`
- `backend/internal/pluginflow/chain.go`
- `backend/internal/pluginflow/chain_test.go`

本阶段不连接网络、不读写数据库、不修改旧 protobuf、安装清单、网关或前端。契约文件是领域模型，不宣称已经冻结新 RPC 线格式。

### 出口

详见阶段一计划。纯包测试及现有插件相关定向回归通过；旧文件哈希保持不变。单独完成本阶段不等于插件平台已能上线使用。

## 4. P2：插件实例、绑定与通用运行接口

### 需要细化的工作

- 保留现有 transport v1；为通用处理和任务入口增加版本化服务适配，握手区分能力。
- 用适配器将 RPC 输入／输出转换为 P1 领域契约，不直接把内部结构当成不受版本控制的网络协议。
- 清单声明执行模式、支持阶段、协议及配置版本、允许的宿主能力。
- 同一安装包支持不同配置实例；实例配置不允许在请求过程中被另一个组的配置覆盖。
- 新增分组绑定与配置版本，不删除旧传输绑定的独占约束。
- 实例发布、停用、升级和回退采用可固定的版本快照；已有请求持有旧运行句柄直到结束。
- 后端管理 API 接入已有 RBAC、step-up 和审计。

### 接入文件

既有：

- `backend/internal/service/plugin_manifest.go`
- `backend/internal/service/plugin_package.go`
- `backend/internal/service/plugin_manager.go`
- `backend/internal/service/plugin_runtime.go`
- `backend/internal/repository/plugin_repo.go`
- `backend/internal/handler/admin/plugin_handler.go`
- `backend/internal/server/routes/admin.go`
- `backend/internal/server/middleware/admin_route_permission.go`
- `backend/internal/service/permission_catalog.go`
- `backend/internal/service/wire.go`
- `backend/internal/handler/wire.go`
- `backend/pkg/pluginapi/v1/plugin.proto`

拟新增的功能单元：

- `backend/internal/service/plugin_instance.go`
- `backend/internal/service/plugin_group_binding.go`
- `backend/internal/service/plugin_hook_runtime.go`
- `backend/internal/repository/plugin_instance_repo.go`
- `backend/internal/repository/plugin_group_binding_repo.go`
- `backend/internal/handler/admin/plugin_instance_handler.go`
- `backend/internal/handler/admin/plugin_binding_handler.go`

迁移编号在执行时重新检查；当前最新为 `250_add_rbac_foundation.sql`，不得提前占用编号或修改既有迁移来代替新增迁移。

### 出口

旧 `.s2plugin` 包仍可按原契约运行；新实例互不污染；分组绑定授权、启停传播、并发配置更新、重启恢复及数据库回滚有独立测试。已绑定但不可用的插件不被伪装成未绑定。

## 5. P3：网关输入与严格输出检查

### 需要细化的工作

- 确认各平台鉴权后实际分组、请求正文读取、上游发送和客户端写出位置。
- 输入链在上游发送前执行；复合路由换组时重新确认适用绑定。
- 在真实响应写出点接入严格缓冲，不仅在请求结束后的日志钩子中审核。
- 普通 JSON 与 SSE 使用协议适配器形成完整检查内容；工具调用、模型声明和用量字段保留宿主语义。
- 明确正文上限、缓冲总预算、并发、超时、取消和临时资源回收。
- 输出审核结束前禁止正文写出或 flush 原始正文；阻断后由宿主生成协议兼容的客户端结果。
- 对未支持协议／内容类型显式拒绝启用严格绑定，或在请求进入时明确终止；不自动绕过。
- 将现有审核协调器适配到新链路时定义唯一执行归属，避免双重审核。

### 接入文件

- `backend/internal/server/routes/gateway.go`
- `backend/internal/handler/gateway_handler.go`
- `backend/internal/handler/openai_gateway_handler.go`
- `backend/internal/handler/wire.go`
- `backend/internal/service/openai_gateway_service.go`
- `backend/internal/securityaudit/coordinator.go`
- `backend/internal/securityaudit/prompt_types.go`
- `backend/internal/securityaudit/prompt_snapshot.go`
- 新增 `backend/internal/service/plugin_gateway_hooks.go`
- 新增 `backend/internal/service/plugin_response_buffer.go`
- 新增 `backend/internal/service/plugin_response_buffer_test.go`

各协议的完整文件写集需在 P3 详细计划中逐路由确认，不能因已有 OpenAI 入口就宣称其他平台已覆盖。

### 必需验收

使用可观测的 HTTP 服务与受控延迟插件验证：上游计数、审核完成前客户端正文为零、阻断后原正文不出现、SSE 跨事件敏感片段、超限、断开、超时及请求取消。只检查最终返回字符串不能证明未提前输出。

### 发布前业务确认

输出被阻断后是否向用户收费尚未决定。用量事实必须保留，收费／退款策略在接入真实结算前确认，不由任意检查插件直接更改余额。

## 6. P4：独立任务与宿主业务能力

### 需要细化的工作

- 手动和定时任务共享执行入口；任务有独立的执行状态、关联 ID、截止时间和结果。
- 单节点并发控制与多节点领取／租约分开设计；租约丢失后用 fencing 或业务版本校验阻止旧执行者提交。
- 调度不承诺“恰好一次”；通过业务幂等键与条件更新避免重试重复应用。
- 通用 KV、网络访问及数据查询按实例授权，不给检测插件自动继承传输插件的账号凭据权限。
- 价格插件使用宿主的专用更新入口，明确目标渠道和实际售价字段；映射／加价逻辑属于插件配置。
- 自动提交有效价格版本；获取或校验失败保留旧价格，已有请求沿用其快照。

### 接入文件

- `backend/internal/service/plugin_host_services.go`
- `backend/internal/service/upstream_billing_probe.go`
- `backend/internal/service/channel.go`
- `backend/internal/service/channel_service.go`
- 新增 `backend/internal/service/plugin_task_service.go`
- 新增 `backend/internal/service/plugin_task_scheduler.go`
- 新增 `backend/internal/service/plugin_pricing_capability.go`
- 新增 `backend/internal/repository/plugin_task_repo.go`
- 新增 `backend/internal/handler/admin/plugin_task_handler.go`

### 出口

没有网关流量也能触发任务；手动／定时结果一致；多节点竞争、重试、超时、停用及价格自动生效均有测试。任务不通过伪造一次业务请求执行。

## 7. P5：管理界面与可观测性

### 需要细化的工作

- 插件库、实例配置、分组绑定、独立任务、执行记录分开组织。
- 插件配置界面复用现有 UI Bridge 的适用部分；实例 ID 与授权上下文不能混用安装 ID。
- 分组页面展示实际生效链，插件页面展示绑定分组和阶段。
- 后台展示规则版本、插件决定、耗时和异常，默认仅展示脱敏信息。
- 管理能力复用已有权限指令和鉴权机制；API 授权不是只隐藏按钮。

### 接入文件

- `frontend/src/api/admin/plugins.ts`
- `frontend/src/views/admin/PluginsView.vue`
- `frontend/src/views/admin/__tests__/PluginsView.spec.ts`
- `frontend/src/views/admin/GroupsView.vue`
- 新增 `frontend/src/components/admin/plugin/PluginInstancesPanel.vue`
- 新增 `frontend/src/components/admin/plugin/PluginGroupBindingsPanel.vue`
- 新增 `frontend/src/components/admin/plugin/PluginTasksPanel.vue`
- 新增 `frontend/src/components/admin/plugin/PluginRunsPanel.vue`

### 出口

只读用户、授权变更、撤权刷新、配置失败、并发版本冲突、任务运行与日志脱敏均有组件/API 测试；再完成真实浏览器验收，不将组件测试替代视觉验收。

## 8. P6：SDK 示例与发布验收

交付三个真正可打包安装的不同用途示例：输入凭据检查、输出业务规则检查、独立价格同步。检查逻辑和阈值位于插件示例中，宿主不硬编码这些类别。

- 更新 `docs/PLUGIN_DEVELOPMENT.md` 和 `backend/pkg/pluginapi/docs/`。
- SDK 文档覆盖清单、配置、两类入口、宿主授权、错误与超时、脱敏、打包及兼容版本。
- 在开发环境执行上传→实例配置→绑定→请求／任务→结果审计的完整链路。
- 完成现有传输插件兼容回归、Go 测试与构建、前端测试／类型／lint／构建、数据库迁移及并发集成测试。
- Docker 构建、启动、持久化与重启恢复单独验收，不能以本地 Go/Vue 构建替代。
- 最终保留本轮原始文件、差异、验证命令和经独立副本验证的回退材料；数据库升级回退按备份／兼容方案处理。

## 9. 设计验收项覆盖矩阵

对应设计稿第 10 节：

| 验收编号 | 覆盖阶段 |
| --- | --- |
| 1 安装与版本校验 | P2、P6 |
| 2 分组隔离 | P1、P2、P3 |
| 3 不同实例规则 | P2、P5、P6 |
| 4 输入阻断不上游 | P3、P6 |
| 5 插件自主决定 | P1、P3、P6 |
| 6 审核前零正文 | P3、P6 |
| 7 输出阻断无原文 | P3、P6 |
| 8 替换授权与校验 | P1、P2、P3 |
| 9 顺序与短路 | P1、P3 |
| 10 异常不透传 | P1、P2、P3、P6 |
| 11 SSE 完整语义 | P3、P6 |
| 12 无请求独立任务 | P4、P6 |
| 13 自动计费更新 | P4、P6 |
| 14 在途版本固定 | P2、P3、P4 |
| 15 日志脱敏 | P2、P3、P4、P5、P6 |
| 16 旧插件兼容 | P2、P3、P6 |

## 10. 执行门禁

- [x] 执行 P1 详细计划，保留红／绿测试证据，不连接业务流量。
- [ ] P1 接口审核后编写 P2 详细计划，并明确通用 RPC、实例发布与迁移设计。
- [ ] P2 通过后细化 P3/P4，确认协议覆盖、资源限制及阻断后的计费策略。
- [ ] 按稳定 API 完成 P5 和 P6，并进行真实环境验收。
- [ ] 用户另行确认后再提交、推送或部署；本计划中的阶段完成不自动授权这些操作。


P1 实施与交接记录：2026-09-30-general-plugin-phase1-handoff.md。P2 尚未实施。

## P2 增量进度（2026-09-30）

- P2a：清单与安装兼容层已实现，详见 2026-09-30-general-plugin-p2a-manifest.md。
- schema2 可声明输入／输出 hook、独立任务、配置版本与请求的宿主能力；签名／哈希包验证及现有入库接口复用。
- 通用包不生成旧传输绑定，当前状态为 incompatible，reason_code=general_runtime_unavailable；并不表示已能启动运行。
- P2b 通用 RPC、P2c 实例／分组绑定及 P2d 管理与跨节点验收尚未完成。P2 出口条件尚未全部满足。
- 审查补充了多节点更新后的缓存门禁：SaveConfig/Test 在读取安装记录后检查清单类型，复用缓存前核对包身份，避免用旧运行时操作新安装记录。

## P2b 增量进度（2026-10-01）

- 通用 GeneralPlugin v1 RPC、go-plugin SDK、清单握手校验与独立进程实例句柄已实现；旧 TransportPlugin 协议保持字节不变。
- 真实子进程测试覆盖 Hook/Task 调用、实例配置隔离、声明快照、替换双重许可、排队／启动／执行超时和关闭；不配合取消的进程会被终止且不重试。
- 管理入口仍保留 general_runtime_unavailable。下一步是 P2c 数据库实例、分组绑定和发布／回退／在途生命周期管理，之后接入管理启用流程。
- 细节与证据见 2026-10-01-general-plugin-p2b-runtime.md；本轮没有真实业务流量、调度或计费接入。

## 2026-10-01 暂停记录：外部上游适配服务与插件平台

用户要求：先记录本次讨论，2026-10-02 继续。本节记录源码分析与待讨论设计，不代表新增功能已经实现，也不自动扩大下一阶段实施范围。

### 分析对象与结论

- 分析文件：`D:\document\xwechat_files\wxid_eg3u5cpargtx22_e384\msg\file\2026-10\free-astra-main.tar(2).gz`。
- 该包通过独立 Python/Docker 服务提供兼容 API，由 sub2api 使用渠道 Base URL 和 API Key 调用；并非由 sub2api 原生插件管理器加载。
- 调用链：客户端 → sub2api → prism2api → freeastra 上游任务启动／状态轮询 → 兼容格式响应 → sub2api → 客户端。
- `Dockerfile:22` 启动 prism2api.py；compose.yaml 暴露 8319 端口并挂载会话目录；prism2api.py 提供 `/v1/models`、`/v1/responses`、`/v1/chat/completions`，处理密钥校验及会话重读。
- freeastra.py 将请求转换后调用上游，再组织 Responses／Chat Completions 响应；scripts/build_manifest.py 生成模型目录，不是 sub2api 安装清单。
- 该源码归档未实现 sub2api 的 TransportPlugin／GeneralPlugin 协议，改扩展名不构成可安装的 .s2plugin 包。
- 本次只核对本地源码，未启动该服务，未验证上游在线可用性、模型名称真实性或端到端接入。

### 两个必须分开的维度

1. 运行方式：嵌入宿主、独立进程、外部服务。
2. 触发方式：请求前、响应时、定时、手动、事件触发。

独立进程不等于独立任务。该适配服务部署独立，但参与用户请求并影响请求完成；用户此前定义的独立任务（如同步上游价格）应在没有用户请求时也可触发。以上是概念分类，不表示当前实现已经支持全部组合；P2b 当前落地的是独立进程运行时与 Hook／Task 调用原语。

### 插件能力分类与待确认设计

- 内容检查：插件内部规则与置信度决定 pass／replace／block，按业务分组绑定生效。
- 后台任务：价格同步、探测、维护；价格同步最终需要自动影响实际计费价格。
- 上游适配：协议转换、会话维护、请求转发。若后续统一管理此类项目，应明确原生适配插件与外部服务注册两种接入边界。
- 上游适配不硬塞进 Check 或 RunTask；转发契约需独立考虑流式、取消、超时、用量、模型映射以及与旧 TransportPlugin 的关系。
- 现有 TransportPlugin 支持指定类型的传输扩展，不将其直接等同于任意上游提供方扩展。
- 保留上传开发完成的插件安装包这一既定要求。此归档可走兼容 API 渠道；若纳入安装／生命周期管理，还需适配层或外部服务管理设计。
- 尚未确认具体新增契约或扩展实施范围，继续时先评估再细化计划。

### 源码中发现的语义差异

- README_SUB2.md 同时出现“估算 usage”和“usage 为 0”的矛盾说明；源码调用 estimated_usage() 并返回估算用量，非上游精确账单。
- 代码先等待 call_prism() 完成，再分块组织 SSE；兼容流式格式不等于实时逐 Token 上游流。
- 后续若做上游适配管理，需要显式标记用量来源与流式能力，避免把估算用量或模拟流式当作原生能力。

### 2026-10-02 续接位置

1. 先读取本节及 `2026-10-01-general-plugin-p2b-runtime.md`，保留 P1／P2a／P2b 已完成状态。
2. 结合本例澄清“内容检查／后台任务／上游适配”的能力边界，确认上游适配是否纳入后续原生插件管理计划。
3. 主线下一阶段仍为 P2c：实例、配置版本、分组绑定持久化与发布生命周期；本次未开始实施 P2c。
4. `general_runtime_unavailable` 管理门禁仍保留；真实请求链路、调度、计费写入仍待后续接入。
5. P2b 验收与回滚证据位于 `D:\tmp\sub2api-plugin-p2b-20261001`。本次仅追加交接文档，不修改运行时代码，不提交、推送或部署。


## P2c 基础增量（2026-10-08）

- 新增 242 迁移：通用插件实例、追加式加密配置版本、分组/阶段/入口/协议绑定；旧 transport bindings 保持独立。
- 仓储实现创建、保存草稿、generation CAS 发布/停用、回退旧配置版本、绑定替换和 repeatable-read 分组快照。
- 新增内部 PluginInstanceService：先启动候选后提交发布；候选启动或 CAS 失败保留旧版本；已租用句柄保持配置版本，引用归零才关闭。关闭/崩溃句柄在下次租用时重建，不自动重试失败调用。
- 已绑定但停用或不支持协议的执行步骤明确失败；同入口多协议行作为替代选项，另一实例的协议匹配不能掩盖缺失步骤。
- 安装记录有实例引用时冻结升级/删除。同 plugin_key 安装先获取事务级 advisory lock，再锁安装行，覆盖首次安装和实例创建并发窗口。
- 真实 PostgreSQL 6 项集成测试连续 3 轮通过（迁移幂等、配置回退、并发 CAS、绑定/停用、安装冻结、AES-GCM 加密、协议逐步骤覆盖）；真实子进程验证 alpha→beta→alpha、旧租用排空及错误后新租用恢复。
- 插件回归、repository/migrations 全量单元、race、vet、构建通过。repository 首次运行缺少 sh，追加现有 Git usr/bin 到测试进程 PATH 后原测试全量通过，未改备份功能。
- 证据目录：D:\tmp\sub2api-plugin-p2c-20261008；本轮配置/绑定/生命周期基础不等于整个 P2c 完成。

### 接续点

1. 补实例查询/删除等管理操作和发布状态 API，接入既有 RBAC、step-up、审计及 Wire/启动关闭装配。
2. 增加跨节点后台协调、状态可观测性、停用传播时间与多节点包恢复验收；当前跨节点状态在下一次权威数据库读取时生效。
3. 管理入口仍保留 general_runtime_unavailable，随后再接入真实网关 Hook、严格 HTTP/SSE 缓冲、任务调度和宿主计费能力。
4. 本轮仅使用独立临时 PostgreSQL，未迁移生产库，未提交或推送。
