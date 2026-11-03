# 通用插件 P2b RPC 与进程实例运行层 Implementation Plan

> 使用 executing-plans / test-driven-development 顺序实施；保留工作区基线，不提交或部署。

**Goal:** 新增独立 GeneralPlugin v1 服务与可实际调用的进程实例句柄，验证 Hook/Task、握手、配置隔离和资源释放。

**Architecture:** 旧 TransportPlugin protobuf 与 SDK 不修改。新增 pkg/pluginapi/general/v1 protobuf 与 go-plugin SDK，新增 service/plugin_general_runtime.go 与 wire 适配器。每个实例／配置版本独占一个进程，配置只在创建时应用一次。句柄固定安装、实例和配置版本；未来 P2c 发布新句柄，不原地改写已有句柄。

**Tech Stack:** 现有 protobuf、gRPC、go-plugin、Go 标准库；生成器从仓库锁定的 protobuf 依赖本地构建，不修改依赖版本。

## 决策与边界

- 协议采用独立 protobuf 消息，不直接用 P1 领域结构作网络协议。GetInfo 返回包身份、API 版本、配置版本、hooks/tasks 和请求的宿主能力；宿主逐项和清单匹配（忽略声明列表顺序，但拒绝缺失、重复或多声明）。
- 配置只允许对象 JSON，最大 64 KiB；Configure 回显实例 ID 与配置修订号。没有运行中 ApplyConfig，避免组间配置串用。
- Hook 带入口、协议、实例／修订号、分组、阶段与请求标识；宿主调用前核对声明和固定绑定。结果转换后执行 P1 校验，替换需要清单和绑定双重许可。
- Task 带入口、实例／修订号、job_id、input_json；只提供调用原语，不调度、不授予宿主能力、不写计费。失败且 Changed=true 的既有任务语义保留。
- 每实例最多一个正在执行的 RPC；排队也计入调用超时。总消息上限 2 MiB，正文 1 MiB，任务输入／输出和配置 64 KiB。超时不重试；关闭取消排队／在途调用并终止进程。进程级 CPU/OS 内存配额与跨实例并发预算仍待部署层实现，不把消息限额宣称为完整沙箱。
- 生命周期与创建请求分开：启动握手成功后不因创建请求结束销毁句柄；显式 Close 终止实例。启动失败必须清理已启动的进程。
- gRPC 原始错误文本不向调用者传播；保留取消／超时错误身份，其他错误返回固定分类。
- SDK 使用独立 magic cookie 和注册名；协议版本沿用进程封装 v1，general_api 单独版本化。宿主服务反向 broker 暂不暴露，HostCapabilities 仍是请求声明。
- 本轮运行层只由内部接口和测试使用；既有管理员启用入口仍返回 general_runtime_unavailable，直到实例持久化与生命周期协调接入。修改提示使其准确表述为“管理入口尚未接入”。

## 写集

- 新增 backend/pkg/pluginapi/general/v1/{general.proto,general.pb.go,general_grpc.pb.go,runtime.go}。
- 新增 backend/internal/service/{plugin_general_runtime.go,plugin_general_wire.go,plugin_general_runtime_test.go}。
- 新增 backend/internal/service/testdata/general-plugin/main.go：仅测试用真实子进程，支持可控错误与延时模式，不是生产示例插件。
- 修改 plugin_general_manifest.go 的 GeneralAPI 常量来源及未接入提示；.gitignore 精确放开新计划／测试 fixture；更新总路线图。

## 步骤

- [x] 保存基线及 P1/P2a 回归。
- [x] 写握手和调用测试，确认缺少新协议／运行层的 RED。
- [x] 定义 protobuf 并本地生成代码，实现 SDK、严格转换与声明对比。
- [x] 实现启动／配置／固定调用／关闭；验证 invalid schema、身份不匹配、缺失能力与错误配置回显。
- [x] 使用真实子进程验证两实例不同配置、绑定不匹配零调用、pass/replace/block、非法结果、Task 结果、超时、取消、关闭和关闭后的拒绝。
- [x] 执行 race、重复测试、旧插件回归、vet、构建和代码审查。
- [x] 更新交接记录并验证独立副本回滚；实际工作区保留实现。

## 验收与后续

测试 fixture 在临时目录构建，go-plugin 由宿主实际启动，不以 in-memory gRPC 冒充子进程验收。原旧传输子进程测试仍需外部 SUB2API_TEST_PLUGIN_PACKAGE，缺少时如实记为跳过。

P2c 继续实现数据库实例／分组绑定、配置发布和跨节点协调；本轮不解除生产管理入口门禁、不连接真实请求、不启动调度器。

## 实施与交接记录（2026-10-01）

### 已验证的真实执行

- 实际构建并启动测试子进程；两个进程分别使用 alpha/beta 配置，Hook 返回对应内容，Task 返回对应结构化结果。
- GetInfo 精确核对包 ID／版本、进程协议／通用 API／配置版本、hooks/tasks、宿主能力；列表顺序可不同，缺失／额外／重复声明拒绝。
- 创建上下文结束后运行句柄仍有效；调用方修改清单或配置缓冲不影响已有实例；配置修订号固定。
- 绑定不匹配或未知入口／协议不进行 RPC；使用 fixture 调用计数证明零调用。
- 结果校验覆盖 pass/replace/block、非法决策、正文大小、任务失败但已变更，以及原始错误文本隔离。
- 配置是对象 JSON，上限 64 KiB；任务输入／输出 64 KiB；正文 1 MiB；gRPC 消息 2 MiB。消息超限的真实进程测试通过。

### 生命周期补强

初次非协作超时测试观察到“RPC 已超时但进程仍可复用”的 RED。现改为：已派发 RPC 返回错误或超时时，先终止该实例，再释放串行调用槽，不自动重试。未派发的排队请求超时／取消不会终止正在执行的实例。

Close 会取消在途和排队调用并终止子进程；重复关闭无副作用。清理等待进程退出，故 RPC deadline 是操作预算，不等于包含清理耗时的端到端精确延迟承诺。正常发布所需的优雅排空、引用计数与跨节点撤换由 P2c 实现；此处 Close 是明确终止语义。

### 测试与工具

- 通用／旧插件定向回归、宿主侧 race、3 次重复运行、P1 核心、vet、服务端构建通过。
- 真实测试 fixture 以普通 go build 构建；race 检查覆盖宿主测试进程，不冒称子进程同样做了 race 插桩。
- 生成器：protoc 3.14.0；protoc-gen-go v1.36.11 从当前仓库依赖本地构建；protoc-gen-go-grpc 1.5.1。两份生成文件重新生成后字节完全一致。
- 旧 TransportPlugin 的外部安装包进程测试仍因缺少 SUB2API_TEST_PLUGIN_PACKAGE 跳过，与本轮已通过的通用子进程测试分开记录。
- 运行时独立审查未发现剩余阻塞问题；已复核非协作进程退出、排队取消与不可变句柄。
- 证据：D:\tmp\sub2api-plugin-p2b-20261001。基线保留已完成的 P1/P2a 及既有 RBAC；实际工作区保留本轮代码。

### 下一步 P2c

新增实例／配置版本／分组绑定的持久化与管理服务；发布时创建新句柄、在途请求固定旧句柄；显式停用与强制关闭行为分开。接好进程所有者、跨节点状态、审计持久化后，再开放管理启用入口。

本轮 Task 仅为调用原语，没有宿主能力授予、价格写入或调度器；分组身份仍由未来宿主鉴权与绑定快照提供，不由插件或请求正文自报。
