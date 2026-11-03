# 响应结果缓存（result-cache）

本功能复用相同完整请求的完整 Chat 回答，区别于上游提示词缓存。当前接入 `/v1/chat/completions` 的 OpenAI 与 Anthropic 兼容入口；不缓存 SSE、WebSocket、Responses 会话续接或工具执行。

## 开启条件

1. 管理员在分组的新建或编辑页面开启“响应结果缓存”。老分组默认关闭。
2. 客户端发送一次 `X-Gateway-Cache-Protocol: result-cache`。只有 `gateway_cache` 而没有协议头会被拒绝。
3. 请求和响应均满足完整、非流式、纯文本或 JSON 的资格要求。模型权限、输入审核和原有用户准入仍先于缓存查询。

默认 TTL 为 30 秒，响应大小上限为 2097152 字节；TTL 可设为 1～86400 秒，大小可设为 1024～2097152 字节。配置 `revision` 由服务端生成，管理写请求不发送它。

## 最小请求

使用测试分组的实际模型名设置 `RESULT_CACHE_MODEL`，通过环境变量提供地址和密钥。连续执行两次，并观察响应头；不把密钥保存到验证日志。

```powershell
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$body = @{
  model = $env:RESULT_CACHE_MODEL
  messages = @(@{ role = 'user'; content = '同一个完整问题' })
  stream = $false
  gateway_cache = @{ mode = 'read_write'; scope = 'user' }
} | ConvertTo-Json -Depth 8 -Compress
$body | curl.exe -sS -D - "$env:RESULT_CACHE_BASE_URL/v1/chat/completions" `
  -H "Authorization: Bearer $env:RESULT_CACHE_API_KEY" `
  -H "Content-Type: application/json" `
  -H "X-Gateway-Cache-Protocol: result-cache" --data-binary '@-'
```

省略 `gateway_cache` 时，协议头默认选择 `read_write`、`user`。无协议头且无控制对象时，按原请求正常转发，不执行结果缓存读写。

## 模式

| mode | 读取已有结果 | 未命中时正常调用上游 | 保存生成结果 |
| --- | --- | --- | --- |
| `read_write` | 是 | 是 | 是，受完整性、策略版本和租约校验 |
| `read_only` | 是 | 是 | 否 |
| `write_only` | 否 | 是 | 是，成功刷新返回 `UPDATED` |
| `bypass` | 否 | 是 | 否 |

`read_only` 未命中不是错误，也不是“禁止调用上游”。缓存故障、等待预算耗尽或填充容量耗尽时，缓存退出，不取代正常上游错误处理和计费。

## 请求早期版本绑定（2026-10-11 T2.3 子项）

两个 Chat 入口对已声明且非 `bypass` 的请求，在认证之后、模型白名单/composite 预处理之前的路由中间件调用 `BindRequestPolicy` 固定该请求的权威 `PolicyVersion`。请求 context 只保存版本、分组、来源服务和失败原因，不保存配置原值；第一次绑定（含错误）不可被重新绑定覆盖。handler 保留幂等绑定，覆盖直接调用；路由 pin 覆盖 `/v1/chat/completions` 和 `/chat/completions` 别名。

`Begin` 保留当前身份、审核、计费准入与分组共享权限检查，并在构造缓存键、读取或获取填充租约之前核对早期绑定。版本切换返回 `BYPASS / policy_changed`；早期权威查询错误或空版本即使随后恢复，也返回 `BYPASS / policy_error`。正常上游、审核及记账路径继续运行。已有 HIT 和发布前版本复检保持不变。未声明缓存不读取结果策略；显式 `bypass` 不增加早期查询，保留 Begin 原有策略检查。其他 opt-in 请求增加一次有 IO timeout 的权威读取。

**本批边界：** 只关闭“早期绑定 → 语义处理 → Begin”期间版本改变的窗口；请求开始前已陈旧的审核/渠道共享快照仍需与权威版本绑定。pin 不是语义快照新鲜度证明，渠道内存 generation 也不是 `PolicyVersion`。不由本批推导旧审核结论可用于新版本缓存；T2.3 整体仍未验收。

新增实际双入口测试覆盖三 mode × 冷/暖缓存12叶子策略切换、composite 读取后切版本的双 URL × 冷/暖8叶子、早期读取失败后恢复及未声明/显式 bypass；验证正常上游与账本关联、无暖缓存来源继承、无新版本污染、下一请求正常 MISS/HIT。本批使用可观测仓储与 miniredis，未启动 PostgreSQL；实库策略摘要验证沿用前批，不把本批视作新增实库验收。

## 渠道请求语义快照（2026-10-11 T2.3 渠道子项）

上一节的版本pin本身不证明共享语义快照新鲜。本批让已声明且非bypass的Chat请求按请求、按ChannelService复用一份渠道快照：生产repository在只读repeatable-read事务里读取渠道、分组关联/平台、模型定价/区间/分时配置、账号统计规则/定价/区间，并读取**同事务**的policy v3权威版本。匹配请求pin后，映射、限制与计费沿该快照读取，不回写共享TTL缓存或共享singleflight，不用generation代替权威版本。

快照错误返回 `semantic_snapshot_error`，缺少权威读取接口返回 `semantic_snapshot_unavailable`，版本/启用状态不匹配或请求中换group返回 `policy_changed`。这些原因在请求内保持粘性，仅使结果缓存BYPASS；渠道正常转发回退原共享路径，原审核、准入和计费照常。暖缓存不会因这种请求被读取/覆盖。session保留快照集合，在权威复检之后以scope锁串行HIT/发布与失败/新source登记；已登记而未完成的cell仅缓存 `BYPASS / semantic_snapshot_pending`，不持锁等待builder。健康集合持锁至限时发布结束，定义本地提交边界，不提供DB到Redis的分布式原子提交。父context限制快照DB构建；取消waiter可先退出，不延长DB预算。所有返回price/interval nullable字段（含 `MaxTokens *int`，共22项）经深拷贝隔离。

usage worker和同步降级通过 `CopyResultCacheUsageContext` 仅复制pin/快照集合及渠道平台选择器，保留worker自己的Deadline/Done/Err，不整体继承HTTP context，不重新绑定权威。已完成的快照在HTTP取消后仍可复用；新的DB构建仍受原请求预算限制。两Chat入口×worker/无pool/已停止/队列满同步降级的实际resolver测试验证新客户费用0.50（旧TTL为0.14）、新账号统计费用0.74、原配额客户成本口径0.50，snapshot只读一次且账本/usage/access来源ID相连。

未声明与显式bypass不新增渠道事务。其他opt-in每请求新增一次限时只读事务；当前复用全渠道读取，分组定向投影及性能预算留后续T2.4。policy v3摘要材料原样保留；同事务一致性不等于所有区间/账号统计子表字段均进入内容摘要，此项留后续复核。事务边界测试采用sqlmock与MaxOpenConns(1)，实际双Chat HTTP测试采用观测仓储/miniredis，本批未启动PG，不声称新增实库验收。

**当前剩余边界：** 渠道TTL/通知延迟窗口已采用本请求权威快照；ContentModeration/securityaudit共享配置的新鲜度仍未绑定，T2.3整体未完成。policy v3 SQL摘要材料、general_runtime_unavailable与D1–D3/M4/M5门禁保持，不由本批推导所有审核结论已能用于新版本缓存。
## 隔离与失效

- `scope=user` 默认同时隔离用户、API Key 与分组。
- `scope=group` 还要求管理员启用分组共享；未批准的共享请求返回拒绝。批准后，同分组的客户端可以复用相同请求，不跨分组共享。
- 正文、消息顺序、历史、模型、生成参数、输出格式和影响输出的头部都参与精确请求身份；JSON 对象字段顺序可规范化，数组顺序与正文不被忽略。
- 分组、账号、通道、设置的当前策略版本参与缓存身份。关闭分组立即停止新读取；在途生成完成时再次检查版本，过时结果不回填。
- TTL 到期后重新生成；HIT 不续期。错误、截断、过滤、工具或超大响应不保存。

网关会剥离 `gateway_cache` 和 `X-Gateway-Cache-*` 请求头；上游的 `prompt_cache_key`、`cache_control` 等字段保留。

## 可信版本来源（2026-10-11 T2.2）

生产缓存键的 `PolicyVersion` 不来自客户端或认证缓存。repository 用单条 PostgreSQL 查询读取当前分组与关联配置，在数据库内部计算 SHA-256，并合成以下来源：

- **effective_route_revision**：分组完整配置（含模型路由、默认映射及其他输出相关选项）、本分组未删除账号的 credentials/extra 与关联优先级、关联渠道的映射/限制/功能配置、渠道模型定价集合。账号/渠道已有 updated_at 也继续参与保守失效。
- **input_policy_revision**：按 key 排序的完整 settings 键值集合，包含 content_moderation_config、prompt_audit_config、risk_control_enabled 等。审核配置的修改、新增、删除均参与，而不是仅使用全表 MAX(updated_at)。
- 分组缓存配置及服务端 revision。v2 域分隔确保旧版时间戳摘要不再生成相同身份；它不是客户端协议或 Redis 记录格式版本升级。

上述名称是同一 `PolicyVersion` 内的权威来源，不是新增客户端字段。原始 credentials/settings 值只在数据库内参与摘要，查询仅返回分组缓存配置、状态、64位十六进制摘要；不返回这些原值，也不把摘要用作指标标签。

内容变更即使保留 updated_at，或被另一条较新 settings 时间戳遮蔽，也会改变缓存键。相同 JSONB 对象的字段顺序不影响版本，关系集合按稳定主键排序。旧条目按原 TTL 自然回收，不扫描删除 Redis；在途生产者发布前重读版本，变化后不回填。

完整 settings 集合是有意的保守策略：无关设置修改也可能降低命中率，优先避免漏掉影响输出的配置。该来源不是细粒度单调修订号，内容恢复且其他来源一致时可回到同一语义版本。生产数据规模下的查询成本、完整 handler/RBAC/审核/计费与上游计数验收分别继续按 T2.3/T2.4 进行。

本轮验证使用真实 PostgreSQL、生产 policy repository/cache service 和 miniredis；miniredis不是外部Redis服务器，也不替代完整生产路由栈。详细记录见 `docs/superpowers/plans/2026-10-11-result-cache-version-progress.md`。
## 响应与计费

- `X-Gateway-Cache-Protocol: result-cache`：网关已识别声明。
- `X-Gateway-Cache-Status`：`MISS`、`HIT`、`UPDATED`、`BYPASS` 或 `UNCACHEABLE`。
- HIT 额外返回 `X-Gateway-Cache-Age` 和 `X-Gateway-Cache-TTL`，单位为秒。
- 每次声明请求获得独立 `X-Gateway-Request-Id`；HIT 生成新的 Chat completion id，保留原回答的正文、生成时间和 `usage`。

HIT 响应中的 `usage` 是原回答的生成用量，不代表本次新增上游消耗。HIT 不再选择来源账号生成，不调用正常生成计费；MISS 和 write_only 的实际上游生成仍走原有计费。缓存访问单独记录，不重复累计来源账号配额，不把结果命中混入上游 `cache_read_tokens`。

独立访问记录只保存必要元数据，不保存问题/回答正文；默认保留 7 天，每小时按批清理。首次版本尚无独立缓存运营控制台。

## 运行参数与 Redis

`gateway.result_cache` 使用以下默认值（也可用对应 `GATEWAY_RESULT_CACHE_*` 环境变量覆盖）：

```yaml
gateway:
  result_cache:
    io_timeout_ms: 300
    wait_timeout_ms: 2000
    lease_ttl_ms: 30000
    generation_timeout_ms: 600000
    max_concurrent_fills: 32
```

I/O 预算为每次 Redis、策略或访问记录操作的上限；等待预算为争用同一租约的等待时间，0 表示不等待。租约按 TTL 的三分之一续租，I/O 预算必须小于该间隔。生成预算限制缓存填充的资格，不替代正常上游请求超时；预算到期、客户端取消、续租失效或服务停止时，主动释放租约与本地填充槽位，过时生产者不发布新条目。

结果缓存连接现有 Redis 实例，使用独立管理、支持上下文截止时间的连接池；关闭该池不关闭其他模块的共享客户端。缓存 I/O 不自动重试，故障尽快回到正常转发路径。当前没有单独 Redis 地址设置；容量不够时由部署规划扩容或后续增加独立实例注入。代码不对整个 Redis 执行 `CONFIG SET`，不改变全局 eviction 策略。32 个并发填充槽位按进程计算，不是集群总额度。

## 验证与回退

自动测试已覆盖模式矩阵、隔离/共享、关闭和在途失效、TTL、重新生成、新请求身份、完整性及慢 Redis 超时。2026-10-11 新增生产 RegisterGatewayRoutes + APIKeyAuth + GroupModelAllowlist + 两个实际 Chat handler/service + Standard 准入 + 计数 HTTP 上游验收；仓储仍为可观察 fixture、Redis 为 miniredis，尚不等同 PostgreSQL 端到端落库、外部 Redis 或供应商部署验收。

运行环境回退先关闭分组缓存并递增版本，旧结果自然到期。代码回退使用本轮保存的工作区原始文件，只撤销本轮缓存增量，不覆盖已有 RBAC/插件改动；不通过删表撤销访问记录。

开发进度和剩余部署验收见 `docs/superpowers/plans/2026-10-08-response-result-cache-progress.md`。
## 数据库集成测试入口

数据库写入失败或 CAS 冲突不会消耗调用方的缓存版本；成功写入才发布新 revision。分组复制的缓存配置保留，但新分组版本从 1 开始。

集成 harness 只使用显式 `SUB2API_TEST_POSTGRES_DSN` 或 Docker。外部 DSN 模式需要 CREATEDB，并在连接后创建新的 `sub2api_it_*` 数据库，迁移及用例在新数据库内执行，结束后删除它，不向输入 URL 所指数据库运行迁移。示例中的槽位须替换为测试环境值，连接信息不保存进源码或测试报告。

```powershell
# Supply SUB2API_TEST_POSTGRES_DSN through the test environment first.
# postgres://TEST_USER:TEST_PASSWORD@TEST_HOST:TEST_PORT/postgres?sslmode=disable
Set-Location backend
go test -tags=integration ./internal/repository `
  -run 'GroupResultCache.*Integration|ResultCacheAccess.*Integration' -count=1 -v
```

SQL-only 的缓存分组/访问记录用例不要求 Redis。测试包括真实 CAS 并发、失败对象重试、分组投影/复制、策略失效、迁移重复执行、访问去重、成本字段与 10000 行清理边界。集成二进制编译成功不等于这些用例已运行；缺少数据库条件时 harness 返回非零退出码，不产生假通过。
## 2026-10-11 完整路由身份与模型契约

- `X-Gateway-Request-Id`、`result_cache_access_logs.request_id` 与本次实际计费的 `usage_logs.request_id` 使用同一个不透明 ID。常见格式为 `client:<服务端路由 UUID>`，缺失路由身份的直接 handler 调用会先绑定新的 context ID。可持久化的短 ID 保持不变；最终 ID 超过 64 字节时，统一计费 resolver 保留命名空间并使用完整 SHA256 的紧凑摘要，不用字符串截断。客户端不应解析或复用该 ID。
- HIT 仍产生新的本次身份和 completion id；内部 `source_request_id` 引用原生成的计费 ID，不引用上游 `X-Request-ID`。HIT 不提交 RecordUsage、不产生额外 usage 行、不累加来源账号配额，也不把返回的原生成 usage 转成 `cache_read_tokens`。
- 凭证状态、过期、模型权限、审核、余额和订阅准入发生在缓存读之前。自动路由矩阵先得到 MISS/HIT 再修改准入条件，验证相同请求被拒绝。
- 发送模型、上游实际声明模型必须确定且与缓存有效模型一致。模型映射不一致、响应模型缺失/null/空白或声明冲突时，不回填，正常转发和生成计费仍执行。未定价但身份一致的自定义模型不因价格目录缺失而被缓存拒绝。
- 两个 handler 在提交异步用量任务前快照渠道/请求模型字段。任务不再从可能被 Gin 复用的请求 context 读取这些字段；真实 worker 延迟执行、父请求取消的路由测试验证原模型与计费/来源 ID 不变。
- Anthropic 非流式 Chat 转换返回实际上游模型观察值，客户端模型仍保留原请求名。显式 `billing_model_source=response_model` 继续遵循现有低价采纳与冲突/未知价回退规则；默认计费来源不随新增观察值改价，也不根据是否声明缓存控制切换规则。

本轮详见 `docs/superpowers/plans/2026-10-11-result-cache-chain-progress.md`。T2.3 剩余真实 PostgreSQL 链路关联、跨模型 fallback 与渠道/审核快照竞态；T2.4 故障负载和 T2.5 指标继续按总指引执行。

## 2026-10-11：真实 PostgreSQL 双入口链路

独立数据库 `sub2api_it_route_*` 接入生产用户、API Key、账号/关系、订阅、usage、原子计费、策略版本与访问日志仓储，保留真实路由、鉴权、准入、上游 HTTP 和异步 worker。两入口验证 `MISS → HIT → write_only(UPDATED) → HIT`；每条生成以相同 request_id 关联 access/usage/usage_billing_dedup，HIT 的 source_request_id 指向实际生成用量和去重账本。HIT 只新增零收费访问记录，不新增 usage/ledger，不改变余额、Key 配额/速率用量、账号配额及 cache_read_tokens。Redis 仍为 miniredis，不把此验证称为外部 Redis 或供应商部署验收。

实库验收暴露并修正了策略自失效：原 v2 哈希把账号 `updated_at` 和配额记账写入作为路由版本，导致正常生成计费后缓存键变更。当前 `result-cache-policy-v3` 哈希完整账号内容，仅去除顶层 `updated_at`/`last_used_at`，仅从 `extra` 排除 `quota_used`、`quota_daily_used`、`quota_weekly_used`、`quota_daily_start`、`quota_weekly_start`、`quota_daily_reset_at`、`quota_weekly_reset_at` 七项运行期记账字段。配额限制、重置配置及未知扩展字段、credentials/模型映射、账号状态/关系优先级、group/channel/pricing 和全 settings 键值仍纳入权威版本。v3 域隔离会使旧 v2 缓存键自然失效；正常计费和 last-used 更新不再制造每请求失效。

本轮入口为 `docs/superpowers/plans/2026-10-11-result-cache-pg-chain-progress.md`。T2.3 仍待跨模型 fallback、渠道/审核缓存快照与权威版本的在途竞态，可补 Anthropic 原生提示缓存转换差分；T2.4/T2.5 尚未验收。

## 2026-10-11：跨模型账号 failover 不回填

新增两入口真实账号 failover 验收：主账号实际 HTTP 503，生产 handler 排除主账号并重新选号，备用账号将请求模型 A 映射成 B 后成功。观察上游 HTTP 模型与凭据，不以直接调用 NoteUpstream 或单账号映射代替 failover。read_write/write_only 返回 UNCACHEABLE，不将 B 发布至 A 键；read_only 无写租约，状态仍为 MISS。B 响应谎报 A、同模型 failover 也沿用保守拒回填规则。重复请求继续转发；主账号恢复后正常 A MISS→HIT。已有暖缓存 A 经 write_only fallback B 后仍保留 A 内容及原生成 source_request_id。

失败尝试不生成计费命令/usage，成功备用账号只生成一次；cache opt-in 与同路径 bypass 的完整计费命令（请求 ID/指纹字段除外）、实际费用、非零原生提示缓存 token（read13/create7）和配额增量一致。HIT 不增加计费/usage/配额，访问记录仍关联实际生成来源。本轮使用真实异步 worker、计数 HTTP 上游、观测仓储与 miniredis；本轮未启动 PG，不将其称为外部 Redis/供应商验收。

本轮未修改生产代码，已有错误 Reject 与成功 sent/response model 核验满足验收。独立副本移除 Complete 的 modelMatches 发布保护后，新断言行为失败；变异探针实测 A 键命中 B 回答，再恢复生产文件 SHA 后回归通过。变异不是原生产缺陷。

入口：`docs/superpowers/plans/2026-10-11-result-cache-fallback-progress.md`。此处覆盖真实账号 failover，不包含 Antigravity 设置驱动的 inline fallback 分支。T2.3 尚待渠道/审核服务缓存快照与权威版本在途竞态，可补 Anthropic 原生提示缓存转换差分；T2.4/T2.5 尚未验收。`result-cache-policy-v3`、general_runtime_unavailable、D1–D3 和 M4/M5 门禁保持不变。

## 2026-10-11：渠道快照失效后的在途发布保护

ChannelService已增加内存构建代次：本地失效或远端通知递增代次，旧在途成功/错误短缓存均不能覆盖新快照；旧已发布singleflight结果交付时也重新核对代次，创建者与等待者重试当前快照。DoChan允许取消调用者先退出，共享数据库构建保持独立限时上下文；交付边界优先检查取消与旧代次，不泄漏过期错误。正常TTL10分钟、错误短TTL5秒、DB超时10秒及CRUD预热/通知规则不变。确定性测试覆盖两读取阶段、成功/失败、本地/远端、连续失效、取消、新旧共享等待者与自身通知回显。

本轮入口为`docs/superpowers/plans/2026-10-11-channel-snapshot-race-progress.md`。此处generation是进程内失效代次，**不是result-cache-policy-v3的权威PolicyVersion**：服务TTL/通知延迟期间的语义快照、审核快照及请求早期策略与结果缓存版本绑定尚待后续验收。T2.3仍未整体完成，PG本轮未启动。
