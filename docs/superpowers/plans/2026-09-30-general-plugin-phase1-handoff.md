# 通用插件平台 P1 实施与交接

日期：2026-09-30。工作分支：feature/rbac-user-system。本轮只实现已批准的第一阶段，不提交、不推送、不部署。

## 已实现

新增八个文件：

- backend/pkg/pluginapi/contracts/v1/hook.go
- backend/pkg/pluginapi/contracts/v1/hook_test.go
- backend/pkg/pluginapi/contracts/v1/task.go
- backend/pkg/pluginapi/contracts/v1/task_test.go
- backend/internal/pluginflow/binding.go
- backend/internal/pluginflow/binding_test.go
- backend/internal/pluginflow/chain.go
- backend/internal/pluginflow/chain_test.go

### 请求链契约

`HookRequest` 携带请求、尝试、业务分组、input/output 阶段及内容；`HookResult` 表达 pass / replace / block。规则和阈值由插件决定，宿主校验置信度数值合法性，但不据此覆盖插件决策。

替换必须由绑定的 CanReplace 授权，并通过 MIME 及载荷互斥校验。阻断结果必须只有结构化拒绝信息，不携带被检查的正文。拒绝文案的业务脱敏、协议编码、正文结构与大小限制由后续适配层承担。

### 分组选择与执行

只选择对应 GroupID、Stage 且 Enabled 的绑定；未绑定的插件不接收内容。任何匹配但不可用的绑定使整个选择步骤失败，健康绑定也不会提前收到内容。稳定排序使用 Order、ID，入链使用绑定值快照。

逐个串行执行，替换结果成为下一插件输入。block 立即短路；错误、panic、非法结果、取消均返回空 Outcome，不返回已处理的部分正文，也不透传插件原始错误。每个内容边界使用独立字节副本。最终 Pass 表示整条链获准继续，不保证正文未经替换。

绑定的版本／配置值快照不等于真实运行时已固定；P2 仍需固定运行句柄及生命周期。Invoker 必须遵守 context 并实现资源约束；纯 Go 函数执行器不负责强行终止不配合取消的函数。

### 独立任务契约

`TaskResult` 与请求链决策独立，包含 Status、Changed、ErrorCode、Output。failed + Changed=true 表示失败前可能已有业务提交，不表示事务自动回滚。本阶段未实现调度、价格抓取或实际计费更新。

## 验证

在 backend 执行：

```powershell
go test ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=1 -v
go test -race ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=1
go test ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=20
go vet ./pkg/pluginapi/... ./internal/pluginflow
go test ./pkg/pluginapi/v1 -count=1
go test ./internal/service -run Plugin -count=1 -timeout=180s
go build -o D:/tmp/sub2api-plugin-phase1-20260930/sub2api-phase1-check.exe ./cmd/server
```

全部退出 0。覆盖率：contracts/v1 100.0%，pluginflow 98.0%。race 使用 CGO_ENABLED=1；验证使用 GOMAXPROCS=4。格式检查无输出；git diff --check 退出 0，既有工作区文件存在 CRLF 转换提示。本轮新增文件另行检查。

代码审查未发现阻塞问题；已追加替换后取消／非法结果，以及健康／不可用绑定两种排列的零调用预检测试。完整命令、环境、原始输出、原始哈希、增量差异及独立副本回滚记录保存在：

`D:\tmp\sub2api-plugin-phase1-20260930`

既有源文件保持基线字节内容；本轮只新增八个 Go 文件、一份交接文档，更新两份计划的进度记录。回滚只针对本轮写集；实际工作区保留新实现。

## 尚未上线的能力

上传安装通用插件、RPC 适配、实例配置／发布、分组绑定持久化、HTTP/SSE 严格审核前缓冲、定时／手动任务、自动计费更新、管理界面与端到端验收均属于后续阶段。当前纯执行器测试不等于生产 HTTP 正文已经实现审核前零输出。

## P2 交接

下一步先细化 P2 计划：安装包兼容、通用运行时协议与能力声明、实例及配置版本、分组绑定持久化、真实运行句柄固定、取消／超时／资源限制及迁移策略。保留现有 OpenAI OAuth transport 插件兼容性。

后续阶段继续遵循已确认需求：开发后上传 .s2plugin；独立任务在请求外运行；请求插件仅影响绑定业务分组；插件内部规则决定放行／处理／拦截；上游价格同步最终自动影响用户有效计费价格。

## P1 补强实施记录（2026-09-30，第二轮）

本节是第一轮记录之后的增量，不修改第一轮验证包。本轮计划见 2026-09-30-general-plugin-phase1-hardening.md。

### 审计出口

- 原 Run 接口保持不变；新增 RunWithTrace 返回独立 ExecutionTrace。失败时 Outcome 仍为空，Trace 保留此前已执行步骤与失败步骤。
- 记录绑定／实例／分组／阶段、插件版本、配置版本、决策、规则编号／可选规则版本／类别／置信度和调用耗时。
- 未绑定、预检失败或缺少 Invoker 时不产生调用记录；缺少 Invoker 是宿主初始化失败，Trace 仅记录总体 Failure。真正进入插件回调的调用才产生记录；错误结果不被记录成插件 pass 或 block。
- 审计记录不携带正文、替换内容、拒绝文案和原始插件错误。Findings 的切片和置信度指针采用独立副本。
- 插件标识字段只保留 128 字节内 ASCII 机器标识；超长或格式不合规的字段置空，MetadataOmitted=true。此筛选不改变插件业务决策，也不等于业务脱敏；审计持久化之前仍需执行宿主的披露策略。
- Finding.RuleVersion 是可选字段，旧 JSON 缺省时仍有效。领域契约没有因此自动成为新的 RPC 线格式。
- 保留单插件 context.Canceled / DeadlineExceeded 的错误身份及 ErrInvocation 分类；父请求取消优先，不透传运行时原始错误。

### 文档跟踪

.gitignore 精确放开原四份插件设计／计划／交接文档及新增补强计划；已用 git check-ignore 验证五份均可跟踪，四个无关路径探针仍被忽略。当前没有执行 git add、commit 或 push。

### 测试与交接

- 新包覆盖率：contracts/v1 100.0%，pluginflow 99.0%。
- race、20 次重复测试、vet、旧 v1 全包回归、service 插件定向回归、服务端构建均通过。
- 两组 fuzz 各执行 5 秒，覆盖 HookResult 和 TaskResult，均通过；完整执行次数及种子输出见本轮证据包。
- 新增父请求实际超时、单插件局部超时、取消、错误／panic／非法结果留存、标识限制与所有权隔离测试。
- 第二轮证据目录：D:\tmp\sub2api-plugin-phase1-hardening-20260930。
- 第二轮回滚基线为已经实现原 P1 的工作区：撤销补强后仍保留原 P1；实际工作区保留补强代码。

### P2/P3 必须保留的接入验收项

1. Trace 只供内部审计；客户端响应中不得包含 Trace。审计持久化处理失败的策略需在接入时明确。
2. 规则编号、类别等插件控制的标识信息不自动等同于已脱敏信息。
3. Rejection.Message 的编码、长度合规不代表语义脱敏；增加“插件把敏感原文拼进拒绝文案”的网关验收案例，分离受控客户端提示与内部审计详情。
4. 真实运行时必须实现超时和资源释放；Go 回调配合 context 的测试不等于可强制终止任意进程内代码。
5. HTTP/SSE 审核前零正文、实例版本固定、调度幂等性和有效计费价格更新仍按后续阶段实施。
