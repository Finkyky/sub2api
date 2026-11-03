# 通用插件 P1 补强 Implementation Plan

> 按 executing-plans 与 test-driven-development 顺序在当前工作区实施；不启动 P2、不提交或推送。

**Goal:** 补齐独立审计出口、文档跟踪、截止时间与模糊测试，保持 P1 决策与旧接口兼容。

**Architecture:** 保留 Run(ctx, request, bindings, invoker) (Outcome, error)，新增 RunWithTrace 返回 (Outcome, ExecutionTrace, error)。客户端 Outcome 不携带审计字段；内部 Trace 在失败时仍保留已执行步骤，但不保留正文、替换内容、拒绝文案或原始错误。

**Tech Stack:** Go 标准库、testing、原生 fuzz；不新增依赖。

## 设计决策

1. 采用返回式内部 Trace 而非同步外部审计回调：核心不依赖数据库／日志系统，也不承担回调 panic、阻塞或失败的业务语义。P2 可在调用完成后把 Trace 交给审计服务。
2. Trace 包含 RequestID、AttemptID、总体 Failure 和逐调用 Records。每条记录包含 BindingID、InstanceID、GroupID、Stage、Version、ConfigRevision、Decision、Failure、Duration 和 Findings。
3. Findings 保留 RuleID、可选 RuleVersion、Category、Confidence。新增可选 RuleVersion 不改变决策校验；审计复制置信度指针。标识字段只保留 128 字节内 ASCII 标识符（字母、数字、下划线、点、冒号、连字符），非法或过长字段置空并标记 MetadataOmitted，而不是截断后冒充有效规则名。
4. 标识符格式约束不是业务脱敏保证；不得自动把 Trace 输出给客户端或不加处理地持久化。P2/P3 仍须对插件控制的标识信息制定审计脱敏策略。
5. 只记录实际发起的调用；未绑定和预检失败的 Records 为空。错误步骤无 Decision／Findings；此前有效记录保留。实际阻断步骤记录 block；后续插件不调用。
6. 保留 context.Canceled / DeadlineExceeded 的错误身份，同时保留 ErrInvocation 分类，不透传插件原始错误文本。区分单插件截止时间与父请求 context 状态。
7. Duration 只计插件调用耗时，不代表 HTTP 总耗时。未启用 Trace 的旧 Run 不分配审计记录。
8. .gitignore 仅开放既有四份插件文档和本补强计划；其他 docs/superpowers 内容继续按原先规则处理。
9. 拒绝文案脱敏、真实运行时超时终止、HTTP 缓冲及审计持久化属于 P2/P3；本轮只登记验收项。

## 文件写集

- 修改 backend/internal/pluginflow/chain.go：共享执行核心、可选记录、保留上下文错误身份。
- 新增 backend/internal/pluginflow/trace.go：内部 Trace 类型、字段筛选、错误分类。
- 新增 backend/internal/pluginflow/trace_test.go：审计与错误边界测试。
- 修改 backend/pkg/pluginapi/contracts/v1/hook.go：Finding 可选 RuleVersion。
- 新增 backend/pkg/pluginapi/contracts/v1/hook_fuzz_test.go 与 task_fuzz_test.go。
- 修改 .gitignore；更新 P1 handoff 与本计划执行状态。

## 执行步骤

- [x] 保存完整本轮基线哈希、ZIP、Git 工作区与索引状态；执行原 P1 和旧插件基线测试。
- [x] 先写 Trace 测试，确认缺失 API 的 RED：顺序、阻断、失败留存、别名隔离、未绑定零记录、不可用绑定预检、元数据限制、取消、实际超时、插件独立超时、原始错误不泄露。
- [x] 实现 Trace 与共享执行核心，观察 GREEN，原 Run 回归保持通过。
- [x] 增加 HookResult 与 TaskResult fuzz（种子含畸形 MIME、Unicode、混合载荷和 JSON），执行种子回归与各 5 秒 fuzz。
- [x] 精确开放文档跟踪；验证五份文档均未被忽略、随机其他文档仍被忽略。
- [x] 执行单测、race、20 次重复测试、vet、旧插件回归、服务端构建、格式与增量白名单检查。
- [x] 更新 handoff，生成 MODIFIED_FILE.zip、DIFF_FILE.patch、VERIFICATION.txt、ROLLBACK.sh；在独立副本撤销本轮而保留原 P1，核对全部基线哈希并运行回归。

## 验证命令

在 backend 中运行：

```powershell
go test ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=1 -v
go test -race ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=1
go test ./pkg/pluginapi/contracts/v1 ./internal/pluginflow -count=20
go test ./pkg/pluginapi/contracts/v1 -run '^$' -fuzz '^FuzzValidateHookResult$' -fuzztime=5s -parallel=2
go test ./pkg/pluginapi/contracts/v1 -run '^$' -fuzz '^FuzzValidateTaskResult$' -fuzztime=5s -parallel=2
go vet ./pkg/pluginapi/... ./internal/pluginflow
go test ./pkg/pluginapi/v1 -count=1
go test ./internal/service -run Plugin -count=1 -timeout=180s
go build -o D:/tmp/sub2api-plugin-phase1-hardening-20260930/server-check.exe ./cmd/server
```

## 自查

与用户确认的插件自主决策、分组隔离、独立任务定义兼容。审计不改变 Outcome，不引入网络、数据库或外部回调。回滚基线是已经完成 P1 的工作区，不是 P1 开发前状态；既有 RBAC 与 P1 文件保持原始备份。
