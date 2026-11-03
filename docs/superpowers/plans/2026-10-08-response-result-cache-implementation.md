# 精确请求响应结果缓存 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 默认在当前会话串行实施；仅在用户明确要求并行时使用 subagent-driven-development。

**Goal:** 客户端声明允许缓存、分组启用时，由网关缓存并复用精确请求的完整 Chat 响应，客户端不再维护缓存。

**Architecture:** 首版复用现有 Redis 客户端，结果采用 result-cache:v1: 前缀与 TTL；分组配置和独立访问记录使用 PostgreSQL。策略/指纹/存储/交付/访问记录分别建立聚焦单元，接入两条 Chat 处理路径而不改变正常 RecordUsage 的语义。

**Tech Stack:** Go、Ent、Wire、go-redis/v9、现有 miniredis/testcontainers 测试依赖、PostgreSQL、Vue 3、TypeScript、Vitest。

---

日期：2026-10-08。设计依据：docs/superpowers/specs/2026-10-08-response-result-cache-design.md。
状态：缓存核心与分组界面已有工作区实现；2026-10-08 本轮已补运行参数、租约生命周期、慢 Redis 超时和交付/表单回归。实际进度、验证结果及尚待部署验收见 `2026-10-08-response-result-cache-progress.md`。下面未勾选步骤仍作为逐项验收清单，不代表全部完成。

## 实施约束与文件边界

- 保留现有 RBAC、插件及其他未提交改动。先记录当前 diff 和关键文件哈希，不执行 reset/clean，不批量提交整个工作区。
- 不先启用生产缓存、不先运行数据库迁移。老分组配置默认 disabled。
- 先写失败测试，运行确认失败，再实施，再运行通过；完成每个任务后检查本任务的 diff。
- 控制文档也使用 UTF-8；Windows 上不要依赖默认 ASCII 的 PowerShell 到 Python 管道写中文。
- 现有最大迁移为 239；创建前再次检查编号。计划使用 251_group_response_result_cache.sql 和 252_result_cache_access_logs.sql；如果被其他工作占用，重新分配并更新所有引用。
- 生成 Ent/Wire 时保留现有生成代码增量。不能使用 HEAD 版本覆盖工作区的 RBAC 生成代码。
- 本计划不包含 SSE、WebSocket、Responses 会话续接、语义缓存或独立缓存运营控制台。

| 单元 | 新增文件 | 修改入口 |
| --- | --- | --- |
| 分组配置 | backend/internal/domain/result_cache_config.go、result_cache_config_test.go | service/group.go、Ent group schema、分组读写/DTO |
| 协议/资格 | backend/internal/service/result_cache_protocol.go、result_cache_protocol_test.go、result_cache_policy.go、result_cache_policy_test.go | Chat 请求解析 |
| 指纹 | backend/internal/service/result_cache_fingerprint.go、result_cache_fingerprint_test.go | 有效参数/模型映射后的边界 |
| 结果/租约契约 | backend/internal/service/result_cache_contracts.go | Redis 实现与服务 |
| Redis 存储 | backend/internal/repository/result_cache_store.go、result_cache_store_test.go、result_cache_store_integration_test.go | repository/wire.go |
| 当前策略 | backend/internal/repository/result_cache_policy_repo.go、result_cache_policy_repo_test.go | 分组版本读取/更新和失效 |
| 合并/执行 | backend/internal/service/result_cache_service.go、result_cache_service_test.go | service/wire.go |
| 访问记录 | backend/internal/service/result_cache_access.go、backend/internal/repository/result_cache_access_repo.go 及对应测试 | PostgreSQL 迁移、清理/生命周期 |
| 交付适配 | backend/internal/handler/result_cache_chat.go、result_cache_chat_test.go | 两条 Chat handler 和 handler/wire.go |
| 前端 | frontend/src/components/admin/group/ResponseResultCacheField.vue 及 __tests__/ResponseResultCacheField.spec.ts | GroupsView.vue、types、i18n |
| 协议说明 | docs/RESPONSE_RESULT_CACHE.md | .gitignore 精确放行本功能文档 |

### Task 1: 分组配置契约与边界测试

**Files:** 新建 domain/result_cache_config.go、result_cache_config_test.go；修改 internal/service/group.go。

- [ ] 写 TestResultCacheConfigDefaults、TestResultCacheConfigBounds：缺省 disabled、TTL=30、max=2097152、共享关闭；拒绝负数、TTL>86400、max<1024/max>2097152。创建新分组的 revision=1，由服务端指定。
- [ ] 运行 `go test ./internal/domain -run ResultCacheConfig -count=1`，预期新符号缺失导致 FAIL；这是本任务的 RED。
- [ ] 定义下列配置和规范化接口；默认值由统一函数生成，前后端不各自发明值。

```go
type GroupResponseResultCacheConfig struct {
    Enabled bool `json:"enabled"`
    TTLSeconds int `json:"ttl_seconds"`
    MaxResponseBytes int `json:"max_response_bytes"`
    AllowGroupSharing bool `json:"allow_group_sharing"`
    Revision int64 `json:"revision"`
}
// NormalizeGroupResponseResultCacheConfig defaults zero values and rejects
// out-of-range fields; revision is persisted server metadata, not client input.
func NormalizeGroupResponseResultCacheConfig(c GroupResponseResultCacheConfig) (GroupResponseResultCacheConfig, error)
```

管理写请求用独立 DTO 接收前四个字段，不接受客户端指定 revision；读取 DTO 返回 revision。规范化函数不代替服务端版本生成。

- [ ] 再运行同一命令，预期 PASS；检查每项默认与设计一致。

### Task 2: 声明协议、控制字段剥离和可缓存资格

**Files:** 新建 service/result_cache_protocol.go、result_cache_protocol_test.go、result_cache_policy.go、result_cache_policy_test.go。

- [ ] 写 TestResultCacheProtocol：无声明不缓存；协议头 result-cache 默认 read_write/user；四种 mode；错误对象、未知协议/范围、重复 JSON 控制键及仅有 gateway_cache 均拒绝；正常上游字段保留。
- [ ] 写 TestResultCacheEligibility：stream=true、tools/functions、外部搜索、多模态、会话引用、未知输出相关语义均 BYPASS；完整纯文本/JSON 可以进入。
- [ ] 运行 `go test ./internal/service -run 'ResultCacheProtocol|ResultCacheEligibility' -count=1`，先确认 RED。
- [ ] 实现解析和资格单元，明确所有返回值，禁止把上游 prompt_cache_key/cache_control 当成结果缓存声明。

```go
type ResultCacheIntent struct { Declared bool; Mode string; Scope string }
func ParseResultCacheIntent(headers http.Header, body []byte) (ResultCacheIntent, []byte, error)
```

返回 body 已剥离 gateway_cache，其他输入保持不变。两条 Chat handler 使用这个统一入口；转发 headers 时剥离 X-Gateway-Cache-*。处理请求大小的现有上限仍有效。

- [ ] 同一命令 GREEN，测试中明确检查 `prompt_cache_key`、`cache_control` 和正文没有被误删。

### Task 3: 精确请求指纹和模型版本

**Files:** 新建 service/result_cache_fingerprint.go、result_cache_fingerprint_test.go、result_cache_contracts.go。

- [ ] 写 TestResultCacheFingerprint 表驱动用例：对象键顺序等价；消息顺序、正文、历史、系统提示、温度、seed、输出 schema、模型、范围、分组/路由/审核版本变化不等价；保持大整数精度，拒绝重复键与尾随第二个 JSON 文档。
- [ ] 运行 `go test ./internal/service -run ResultCacheFingerprint -count=1`，确认 RED。
- [ ] 定义完整键材料并实现规范化+摘要，不使用未加边界的字符串拼接：

```go
type ResultCacheKeyMaterial struct {
    ProtocolVersion string
    Scope string
    GroupID int64
    ScopeIdentity string
    Endpoint string
    EffectiveModel string
    EffectiveRequestFingerprint string
    RepresentationVersion string
    GroupCacheRevision int64
    EffectiveRouteRevision string
    InputPolicyRevision string
}
func BuildResultCacheKey(m ResultCacheKeyMaterial) (string, error)
```

规范化基于 JSON token/RawMessage，保留数值词法精度和缺省差异；不 trim 正文、不排序数组、不静默丢弃未知字段。作用到实际分组改写后的请求。路由版本使用可信输出策略快照摘要，不能只拿 pricing 或缓存开关代替。

- [ ] 定义结果结构：版本、生成/过期时间、模型/策略版本、body、生成用量快照、来源请求引用；定义 ResultCacheLease（token、过期时间、版本）供任务 5、7 共用。
- [ ] 同一命令 GREEN。用固定时钟测试 TTL，不用真实 sleep 来证明键生成。

### Task 4: 分组持久化、原子版本和即时失效

**Files:** 新建 migrations/251_group_response_result_cache.sql、repository/result_cache_policy_repo.go 及测试；修改 ent/schema/group.go、service/group.go、service/admin_group.go、repository/group_repo.go、handler/admin/group_handler.go、handler/dto/types.go。

- [ ] 写 TestGroupResultCacheRoundTrip、TestGroupResultCacheRevisionCAS、TestGroupResultCacheCopyDefaults、TestGroupResultCacheDisableInflight：创建/详情/列表/修改/复制投影；并发修改不能复用同一 revision；关闭后的旧快照不能读取或发布。
- [ ] 仓储聚焦测试先 RED；单元模拟不代替真正 PostgreSQL 的 CAS 验证。
- [ ] 增加 JSONB 分组字段，默认结构完整且 enabled=false；Ent 使用同一 domain 类型。读取老数据按统一规范化补默认。管理 DTO 客户端不能写 revision。
- [ ] 分组更新仓储事务内锁定/比较当前版本并递增，不在 service 内无锁 `old+1`。所有覆盖该 JSON 字段的路径必须遵守同一更新规则。新复制分组独立 revision=1，默认关闭，不能继承已开启共享。
- [ ] 接入现有 `InvalidateAuthCacheByGroupID`，模型路由/审核变化影响对应有效版本；result_cache_policy_repo 从可信当前状态读开关和版本，查询故障 BYPASS，不使用旧允许状态。
- [ ] 运行 `go generate ./ent`；逐项审阅 Ent diff，确保 RBAC 模型仍存在。
- [ ] 运行 `go test ./internal/repository ./internal/service -run 'GroupResultCache' -count=1`，预期 GREEN。
- [ ] 运行 `go test -tags=integration ./internal/repository -run 'GroupResultCache' -count=1`，需要 Docker/PostgreSQL；环境不可用时单独报告，不宣称集成通过。

### Task 5: Redis 结果存储和带所有权的填充租约

**Files:** 新建 repository/result_cache_store.go、result_cache_store_test.go、result_cache_store_integration_test.go；修改 repository/wire.go。

- [ ] 使用现有 miniredis 写 TestResultCacheStore：读写/TTL/HIT不续期/大小限制/损坏记录/独立前缀；使用真实 Redis 集成验证 Lua 租约语义。
- [ ] 写 TestResultCacheLeaseOwnership：旧 token 不能发布、解锁或覆盖新值；租约过期后旧生成者不能回填；当前策略版本不同拒绝发布。
- [ ] 运行 `go test ./internal/repository -run 'ResultCacheStore|ResultCacheLease' -count=1`，先 RED。
- [ ] NewResultCacheStore 接收现有 `*redis.Client`；结果 `result-cache:v1:`、锁 `result-cache-lock:v1:`、不删除鉴权/额度键。记录逻辑 expires_at 和 Redis TTL，HIT 校验两者，大小同时检查编码后字节。
- [ ] 租约用随机 token + SET NX PX；Lua 发布/解锁比较 token，不采用无条件 DEL。续租也校验所有者；SQL 当前策略校验与键版本共同防止旧策略交付。
- [ ] 写入、读取、等待各设独立短超时；错误分类不包含缓存正文。存储故障由服务转为正常上游路径。
- [ ] 聚焦命令 GREEN，再执行 `go test -tags=integration ./internal/repository -run 'ResultCacheStore|ResultCacheLease' -count=1` 验证真实 Redis。

### Task 6: 独立缓存访问记录与清理

**Files:** 新建 migrations/252_result_cache_access_logs.sql、service/result_cache_access.go、result_cache_access_test.go、repository/result_cache_access_repo.go、result_cache_access_repo_test.go、result_cache_access_repo_integration_test.go。

- [ ] 写 TestResultCacheAccessNoDoubleBilling、TestResultCacheAccessRedaction、TestResultCacheAccessRetention：HIT 无本次账号归因、零新增成本、不调用正常 RecordUsage，不保存正文/Authorization，保留期 7 天。
- [ ] 运行 `go test ./internal/service ./internal/repository -run ResultCacheAccess -count=1`，确认 RED。
- [ ] 采用已有 audit_log_repo.go 的 SQL append-only 风格，新表不要求本次 account_id。字段与设计第 8 节对应，request_id 唯一用于当前缓存访问；source_request_id 仅表示生成来源。
- [ ] 每个有效声明请求产生独立访问事件；MISS 的正常 usage_logs 保持既有真实成本，不能再把相同成本汇总一次。日志写入失败不改变交付和既有正常扣费；增加低基数失败指标。
- [ ] 清理按 created_at 索引分批删除，连接服务启动/关闭上下文；不启动无法关闭的 goroutine，清理不操作真实 usage_logs。
- [ ] 单元 GREEN，再执行 `go test -tags=integration ./internal/repository -run ResultCacheAccess -count=1` 验证写入、唯一约束、查询关联和保留期清理。

### Task 7: 请求合并与缓存执行服务

**Files:** 新建 service/result_cache_service.go、result_cache_service_test.go；修改 service/wire.go。

- [ ] 写 TestResultCacheServiceModes：四种模式、disabled、group 共享双重批准、当前策略读取失败、过期、存储错误和不可缓存响应。
- [ ] 写 TestResultCacheCoalescing：相同键合并、不同身份/参数不合并、等待者取消、生产者超时、租约丢失、等待耗尽走正常上游、配置关闭时拒绝旧交付。
- [ ] 运行 `go test ./internal/service -run 'ResultCacheService|ResultCacheCoalescing' -count=1`，确认 RED。
- [ ] ResultCacheService 依赖存储、当前策略、访问记录和可注入时钟；它不直接依赖 OpenAIRecordUsage。用独立生产者预算管理合并，单个等待者取消不取消其他等待者，所有等待者离开后结束无主工作。
- [ ] 所有参与者各自完成准入，再参与合并；执行生产者时只记录一次真实生成扣费，等待者各记自己的 HIT/合并访问。不拿来源 request_id 作为等待者的记账 ID。
- [ ] write_only 必须执行刷新；read_only MISS 不获取写租约；bypass 不查询存储。跨模型 fallback/工具/截断/超大返回正常响应但不发布。
- [ ] 同一命令 GREEN，再运行 `go test -race ./internal/service -run 'ResultCacheService|ResultCacheCoalescing' -count=1`。Windows race 需要可用 C 编译器；缺少时报告并安排 Linux CI，不伪报通过。

### Task 8: 非流式 Chat 适配与两条入口

**Files:** 新建 handler/result_cache_chat.go、result_cache_chat_test.go；修改 handler/openai_chat_completions.go、handler/gateway_handler_chat_completions.go、handler/openai_gateway_handler.go、handler/gateway_handler.go、handler/wire.go；服务的非流式 Chat 输出边界按需增加聚焦单元，不重构无关流式逻辑。

- [ ] 写 TestResultCacheChat 的真实 HTTP 假上游计数测试：第一次 MISS、第二次 HIT，上游计数=1、回答相同、网关 request_id/completion id 不同。
- [ ] 写 TestResultCacheChatAuthAuditOrder：禁止凭证/模型/输入必须在查缓存前结束；HIT 不占账号推理槽位但保留用户限流；来源账号不新增计费。
- [ ] 写 TestResultCacheChatProtocolStripped：上游收不到 gateway_cache/网关头，上游缓存字段仍在。
- [ ] 运行 `go test -tags=unit ./internal/handler -run ResultCacheChat -count=1`，确认 RED。
- [ ] 两条入口在解析/分组参数改写/模型映射/输入审核和准入后接入查询，在账号选择前返回 HIT。正常 MISS 保留调度、fallback、错误处理与原 RecordUsage。
- [ ] 使用非流式输出专用边界：暂存小响应以判定状态，最多收集分组大小上限；超限切回正常直通前设置 UNCACHEABLE，不无限缓冲。返回成功后才能发布完整结果。不能缓存任意 Gin writer 字节而忽略协议完成性。
- [ ] 响应只接受完整合法 choices，拒绝 length/content_filter/tool_calls；新 completion id 按 Chat 格式生成，回答/原生成时间/原 usage 保留；返回 HIT/Age/TTL，来源指纹不暴露。
- [ ] 所有写头时序有测试：MISS/UPDATED/UNCACHEABLE/BYPASS 在头提交前确定；缓存写入失败不替换上游成功。
- [ ] 聚焦命令 GREEN，额外运行 `go test -tags=unit ./internal/handler -run 'ChatCompletions|SecurityAudit' -count=1` 防止现有顺序/计费回归。

### Task 9: 分组界面和权限回归

**Files:** 新建 ResponseResultCacheField.vue、__tests__/ResponseResultCacheField.spec.ts；修改 GroupsView.vue、frontend/src/types/index.ts、i18n/locales/zh/admin/overview.ts、i18n/locales/en/admin/overview.ts。

- [ ] 写组件测试：默认关闭、启用后显示 TTL/大小、范围上下限、共享告知、双向绑定，不能发送 server revision。
- [ ] 在 frontend 运行 `pnpm exec vitest run src/components/admin/group/__tests__/ResponseResultCacheField.spec.ts`，确认 RED。
- [ ] 定义读类型（含 revision）与管理写类型（无 revision）；新增独立字段组件，不把复杂交互继续堆进 GroupsView。创建/编辑/复制/提交均携带明确配置；缺省老数据规范化。
- [ ] 管理开关仅有 groups.update/create 等既有权限的操作者可变更；只读操作者看状态但不能更新。显示 HIT 免费、默认 user 隔离和 group 共享可见性说明。
- [ ] 同一组件命令 GREEN；运行 `pnpm typecheck`、`pnpm run check:i18n` 和 `pnpm exec vitest run src/permissions/__tests__ src/components/admin/group/__tests__`。

### Task 10: Wire、容量设置、协议说明与端到端验证

**Files:** 修改 repository/service/handler wire、cmd/server/wire_gen.go、config/config.go、deploy/config.example.yaml；新增 docs/RESPONSE_RESULT_CACHE.md，.gitignore 精确放行该文档和设计/计划，不放开无关目录。

- [ ] 配置项明确现有 Redis 与独立实例后续切换的边界、读/写超时、生成/等待预算和最大并发填充数。若原实例容量不够，由部署设置限额或独立实例，不从代码无条件 CONFIG SET 修改整个 Redis 的 eviction 策略。
- [ ] 在 backend 运行 `go generate ./cmd/server`，核对生成调用包含缓存 service/store/access 生命周期并保留 RBAC/插件注入；执行 `go build ./cmd/server`。
- [ ] 写路由覆盖测试：/v1/chat/completions 的 OpenAI/Anthropic 兼容路径，未支持的流式/工具 BYPASS；不把新的结果缓存套到 /models 等已有缓存。
- [ ] 协议说明包含最小 curl 请求、四种 mode、范围、状态头、原 usage 与本次实际成本区别、默认 disabled、管理员开启流程。

```powershell
curl.exe -sS "$env:RESULT_CACHE_BASE_URL/v1/chat/completions" `
  -H "Authorization: Bearer $env:RESULT_CACHE_API_KEY" `
  -H "Content-Type: application/json" `
  -H "X-Gateway-Cache-Protocol: result-cache" `
  -d '{"model":"configured-model","messages":[{"role":"user","content":"同一个完整问题"}],"stream":false}'
```

执行前使用测试环境的真实配置模型名替换 configured-model；不把真实密钥写入文档、终端保存文件或访问日志。两次调用要核对状态头和假上游计数，而不只比较正文。

- [ ] 完成设计 16 项验收；运行下列回归矩阵，将字面 stdout/exitcode 保存到本功能证据目录：

```powershell
# backend
 go test ./internal/domain ./internal/service ./internal/repository ./internal/server/routes -run 'ResultCache|GroupResultCache|RBAC|Permission' -count=1
 go test -tags=unit ./internal/handler -run 'ResultCache|ChatCompletions|SecurityAudit' -count=1
 go test -race ./internal/service ./internal/repository -run 'ResultCache' -count=1
 go test -tags=integration ./internal/repository -run 'ResultCache|GroupResultCache' -count=1
 go test -tags=unit ./...
 go vet ./...
 go build ./cmd/server
# frontend
 pnpm typecheck
 pnpm run check:i18n
 pnpm exec vitest run src/components/admin/group/__tests__ src/permissions/__tests__
 pnpm build
```

- [ ] 逐个声明实际 PASS/FAIL/环境阻塞，不把本轮文档检查当作这些测试已经执行。

## 覆盖对应表

| 设计验收编号 | 主要任务 |
| --- | --- |
| 1～3 无声明/关闭/MISS-HIT | 2、4、7、8 |
| 4 请求精确性与规范化 | 3 |
| 5 四种模式 | 2、7、8 |
| 6 隔离与共享 | 2、3、4、7、9 |
| 7 当前准入/审核 | 8 |
| 8 TTL/版本 | 3、4、5、7 |
| 9 非完整结果与故障 | 2、5、7、8 |
| 10 专用字段剥离 | 2、8 |
| 11 模型 fallback | 3、8 |
| 12 并发/取消/租约 | 5、7 |
| 13 Redis 故障 | 5、7、8 |
| 14 记账和来源区别 | 6、7、8 |
| 15 分组 CRUD/复制/版本/权限 | 4、9 |
| 16 清理/指标/完整回归 | 6、10 |

## 发布和功能回退

1. 完整验证后先合入代码与新增表/字段；全部分组保持 disabled，原请求不改变。
2. 在测试分组开启 30 秒 TTL，观察命中、上游计数、原计费、缓存故障和内存，再逐步开放。
3. 功能回退首先关闭分组缓存并递增版本，停止新读写；旧结果按 TTL 清理。
4. 若需撤销代码，使用此次实施前保存的工作区基线，仅撤销本功能文件，不覆盖 RBAC/插件；数据库字段/访问表先保留，验证旧版本对额外结构兼容，不通过生产删表回滚破坏记录。
5. 文档审阅更新的回滚证据不等于以上运行功能回退已验证；运行功能的回退需要实施后独立测试。

## 实施交付条件

十项任务的 GREEN 与真实集成结果、分组开启/关闭、同请求上游计数、零重复计费及功能回退全部有证据后，才声明缓存功能完成。此计划文件本身不是功能交付。
