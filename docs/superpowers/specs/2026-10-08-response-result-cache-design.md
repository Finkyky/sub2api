# 精确请求响应结果缓存设计

日期：2026-10-08。
状态：用户已确认方案方向及服务端 Redis 存储选型；进入实施计划阶段，运行代码尚未实施。
DesignStatus: approved

## 1. 已确认需求

客户端明确接受本次复用响应，分组允许缓存时，网关负责查询、生成缓存键、写入、过期和清理。客户端不需要保存回答，也不需要提供 namespace、key_parts、股票代码或业务缓存键。

逻辑模型为“完整问题 → 完整响应”。问题包括完整输入和影响回答的生成参数，不只指最后一句话或同一股票代码。首版按精确请求匹配，不做语义相似检索。相同股票的不同问题、不同上下文不能互相命中。

上游 Prompt Caching 与结果缓存独立：cache_control、prompt_cache_key 不代表完整回答复用授权，现有上游缓存字段及计费保持原有语义。

## 2. 首版范围和提出的默认值

首版支持 /v1/chat/completions 的非流式、无状态纯文本或 JSON 请求；完整历史可以包含在本次请求中。兼容别名必须确认走同一适配路径后才开放。

首版不缓存 SSE、WebSocket、Responses 会话引用、图片/音视频、异步任务、工具调用、外部搜索或其他动态工具结果。声明缓存但类型不适用时正常转发并返回 BYPASS，不削减原接口能力。

以下默认值作为实施计划基线；缓存启用和共享由管理员显式配置：

| 分组配置 | 默认值 | 语义 |
| --- | --- | --- |
| enabled | false | 老分组和新分组默认关闭，管理员启用后生效 |
| ttl_seconds | 30 | 允许 1～86400 秒；客户端无需管理有效期 |
| max_response_bytes | 2097152 | 允许 1024～2097152 字节 |
| allow_group_sharing | false | 同分组共享需要管理员显式批准 |
| revision | 1 | 服务端递增，调用方不能指定 |

分组字段建议统一为 response_result_cache_config。关闭同时禁止读取和写入，不只是停止新增缓存。

## 3. 客户端协议

最小接入只增加一个请求头：

```http
POST /v1/chat/completions
Authorization: Bearer API_KEY
Content-Type: application/json
X-Gateway-Cache-Protocol: result-cache
```

```json
{
  "model": "configured-model",
  "messages": [{"role": "user", "content": "分析一下贵州茅台的长期投资价值"}],
  "stream": false
}
```

该协议头明确授权本次使用完整响应缓存，默认 read_write、user 范围，有效期来自分组。不把“客户端支持上游缓存”解释为本次复用授权。

需要控制行为时，可选添加：

```json
"gateway_cache": {"mode": "read_write", "scope": "user"}
```

首版只接受 mode、scope；不要求客户端提供以上对象，不接收最终缓存键或用户/项目/租户 ID。

| mode | 读取 | 未命中/刷新行为 | 写入 |
| --- | --- | --- | --- |
| read_write | 查询新鲜结果 | 调用上游 | 成功完整后写 |
| read_only | 查询新鲜结果 | 调用上游 | 不写 |
| write_only | 不读 | 强制调用上游 | 成功完整后写 |
| bypass | 不读 | 正常调用上游 | 不写 |

协议规则：
- 无协议头：正常转发，不查询、不写入结果缓存。
- 未知协议、错误对象类型、未知 mode/scope：400，不调用上游。
- 有 gateway_cache 但没有协议头：400，避免不明确授权及宿主字段泄漏。
- 分组关闭或请求不适用：BYPASS，正常转发。
- 请求 group 范围但分组未批准共享：403，不悄悄扩大或改变范围。
- 转发前删除 gateway_cache 和网关专用请求头，保留上游 Prompt Caching 字段。

首版不提供客户端业务键和 stale_if_error。Redis 故障时走正常上游流程，不返回无限期旧答案。

## 4. 隔离和共享

默认 user 范围实际隔离到 group_id + user_id + api_key_id；同用户的不同应用凭证默认不共享。

group 范围同时要求 allow_group_sharing=true 和客户端显式 scope=group，由网关解析 group_id，不接收客户端身份。管理员仅为确定可共享的公开内容开放该范围。相同分组不代表不同权限知识库、私有内容和隐藏个性化输入可以共享。

没有可信 project/tenant 成员关系前不增加这些范围；首版不提供 global。

关闭/修改配置递增 revision。读取前和发布前都检查当前可信开关与版本；旧请求不能在关闭后回填。若沿用现有 Group/API Key 配置缓存，更新必须接入它们的失效路径，不仅检查请求早期的旧快照。已发出的响应不尝试撤回。

## 5. 精确缓存键和期限

```text
key_material = {
  protocol_version,
  server_resolved_scope,
  group_id,
  scope_identity,
  normalized_endpoint,
  effective_model,
  effective_request_fingerprint,
  representation_version,
  group_cache_revision,
  effective_route_revision,
  input_policy_revision
}
redis_key = result-cache:v1:SHA256(canonical_json(key_material))
```

使用结构化编码后摘要，不使用未加边界的字符串拼接。

有效请求指纹基于分组参数限制/改写后的请求，删除 gateway_cache；覆盖完整 messages、系统提示词、内容片段、生成参数、输出格式/schema。仅规范化对象字段顺序，保留数组顺序、正文、数值精度及缺省/显式值差异。

不简单排除 user、metadata 等可能影响上游或权限的字段。只有已证明纯本地追踪的字段可以排除；影响输出的版本/请求头必须入指纹，未适配的动态语义跳过缓存。

先解析渠道模型映射，再查缓存。模型映射、输出相关路由及审核策略变化必须能失效旧结果，分组缓存版本不能代替所有变化来源。

首版只发布能够确定模型身份的结果。账号级映射、跨模型 fallback 或上游实际模型与查找时目标不一致时，正常返回但不回填正常键，不能把 B 的回答当成 A 的缓存。

缓存声明表示客户端接受有效期内复用第一次成功结果，不要求模型原本每次采样一致；temperature、seed 等仍参与指纹。

TTL 从生成成功完成时开始，HIT 不续期。记录 created_at、expires_at；读取同时校验应用层年龄和存储期限。动态股票内容配置短有效期，稳定内容可以较长，但不存在“同一股票永久有效”。

修改配置/清理分组缓存可以递增版本，旧键按 TTL 回收，不扫描删除整个 Redis 实例。

## 6. 完整结果、交付和状态头

结果包含记录版本、生成/过期时间、策略版本、有效模型、最终客户端响应 JSON、原生成用量快照及来源请求引用。不缓存 Authorization、Cookie 或原始上游响应头全集。

命中生成新的网关 request_id 和顶层 Chat completion id，保留回答内容、原生成时间和原生成用量；来源 ID 只在内部记录。原请求 ID 不能当作本次记账幂等键。不得把这套 Chat 规则直接套在有状态 Responses ID 上。

仅缓存 HTTP 200、合法完整、大小满足限制的响应。错误、取消、非法 JSON、工具调用、finish_reason=length/content_filter 等异常终止不缓存。超过大小上限只停止缓存收集，不改变原请求行为。

命中前仍执行当前身份有效性、模型权限、请求限流、必要余额/订阅/额度准入和输入审核。首版保持原准入策略，不因为 HIT 免费就开放过期身份或受限凭证。HIT 不占上游账号推理槽位，但保留用户级并发/连接控制。

已有输出交付规则照常执行；未来输出审核/插件变化应重新校验或按版本失效，不能继承旧放行结论。

```http
X-Gateway-Cache-Protocol: result-cache
X-Gateway-Cache-Status: HIT
X-Gateway-Cache-Age: 8
X-Gateway-Cache-TTL: 30
```

| 状态 | 含义 |
| --- | --- |
| HIT | 新鲜结果命中，无本次上游调用 |
| MISS | 允许读取但未命中，已调用上游 |
| UPDATED | write_only 成功生成且发布 |
| BYPASS | 分组关闭、客户端绕过、类型不适用或存储故障 |
| UNCACHEABLE | 上游结果不满足写入条件 |

无声明请求可不增加缓存头。Age 只用于 HIT，不向普通调用者暴露完整键摘要。read_only 的 MISS 不暗示成功写入。

非流式适配器在提交响应头前完成状态判定；写入失败不能把上游成功改为请求失败。最终存储失败记录 BYPASS/storage_error，正常结果照常返回。未来 SSE 需区分开始时查找状态与结束时写入结果。

## 7. 并发和故障

首版复用项目现有 Redis 客户端和实例，使用独立 result-cache:v1: 键前缀、大小上限和容量预算；缓存正文不放 PostgreSQL，分组配置和访问记录放 PostgreSQL。大规模结果缓存宜与鉴权/额度工作负载隔离；更换逻辑数据库不等于隔离实例内存容量。

read_write 同键 MISS 使用节点内请求合并，多实例采用有期限的填充租约和所有者 token。发布/解锁校验所有权，租约过期后的旧生产者不能覆盖新结果。等待者交付前重新验证身份和版本。

各请求仍独立准入和记录，不合并不同范围、版本或请求指纹。只承诺正常租约内减少重复生成，不承诺网络故障下严格只调用一次。

read_only 未命中不获得写租约；write_only 强制实际刷新，不转换为普通 HIT。刷新发布也必须遵守所有权和版本校验。

Redis 查询、写入和填充等待有独立短超时；故障或等待预算耗尽走正常上游路径，仍受原有限流保护。损坏记录视为 MISS，尝试清理，不向客户端输出损坏 JSON。

生产者上下文与单个等待者取消解耦，但执行预算有上限；取消客户端不交付，所有等待者取消时按生产者策略结束工作，不能留下无限期后台生成。

## 8. 计费、审计和指标

首版 HIT 本次上游成本和缓存内部收费均为零；MISS/write_only 的实际生成执行原有正常计费，是否成功写入缓存不影响实际消耗记账。

HIT 不调用正常 RecordUsage，不创建虚假账号调用，也不向来源账号累计本次配额。当前 usage_logs.account_id 必填，OpenAI RecordUsage 依赖 Account 并扣费，不能套用缓存来源账号复用它。

新增独立 result_cache_access_logs：独立 request_id、当前 user/api_key/group、接口和模型、模式/范围、缓存状态/原因、年龄/耗时、是否调用上游、来源生成请求引用、原生成用量快照、本次上游成本及缓存收费。只保存必要元数据，不保存问题和回答正文，默认留存 7 天并定期清理。

正常 usage_logs 与缓存访问记录按本次 request_id 关联，不重复汇总生成 token/成本。缓存来源引用不是本次调用账号。

为兼容客户端，响应 usage 保留原回答生成用量；HIT 头和协议文档明确其不代表本次新增上游消耗。内部交付用量和新增生成用量分开统计，不把结果命中写进现有 cache_read_tokens。

指标包含 hit/miss/bypass/uncacheable、存储错误、合并等待、缓存年龄、条目大小和交付延迟。标签保持低基数，不使用正文、用户 ID 或完整缓存摘要。

首版实现分组配置和访问数据基础，不扩大为独立运营控制台。

## 9. 现有代码落点

| 职责 | 路径 |
| --- | --- |
| 分组配置类型 | backend/internal/domain 下新增单元；backend/internal/service/group.go |
| Ent 与迁移 | backend/ent/schema/group.go；新增访问日志 schema 和数据库迁移 |
| 分组读写/DTO | backend/internal/repository/group_repo.go；backend/internal/service/admin_group.go；backend/internal/handler/admin/group_handler.go；backend/internal/handler/dto/types.go |
| Chat 接入 | backend/internal/handler/openai_chat_completions.go；核对 gateway_handler_chat_completions.go 的兼容路径 |
| 策略/指纹/缓存服务 | backend/internal/service 下聚焦的 result_cache_* 单元 |
| Redis/租约/访问记录 | backend/internal/repository 下聚焦的 result_cache_* 单元 |
| 依赖注入 | service/repository/handler wire 及 backend/cmd/server/wire_gen.go |
| 分组界面 | frontend/src/views/admin/GroupsView.vue、独立配置组件、frontend/src/types/index.ts、中英文文案 |

保留现有 RBAC 和通用插件未提交改动。本功能属于网关核心，不依赖尚未接入真实请求的通用插件实例管理。

## 10. 验收矩阵

1. 无声明两次相同请求均正常调用上游，无结果缓存读写。
2. 分组关闭返回 BYPASS，不读旧缓存，旧版本在途请求不回填。
3. 开启并声明：首次 MISS、随后 HIT，上游计数一次，回答内容一致。
4. 问题、历史、系统提示、模型、生成参数、输出 schema 不同不命中；对象字段顺序不同可命中，数组和正文不同不能。
5. 四种 mode 遵守读写矩阵，read_only MISS 不写，write_only 实际生成。
6. user 隔离到用户和 API Key；group 共享双重批准，跨分组不能命中。
7. 过期身份、禁止模型、审核阻断不能通过旧缓存放行。
8. TTL/配置/路由/审核版本变化失效；HIT 不续期。
9. 错误、超大、不完整、截断、工具、流式响应不写；缓存失败不改变原成功结果。
10. 上游缓存字段保留，网关专用声明不传上游。
11. 跨模型 fallback 和无法确定的账号映射不污染有效模型键。
12. 并发合并降低重复调用，取消/租约失效/旧生产者不覆盖新结果。
13. Redis 故障正常转发，原有限流和授权仍生效。
14. HIT 新 request_id、独立访问记录、不重复扣费或累计账号配额、不增加 cache_read_tokens。
15. 分组新建/修改/复制/详情与列表投影保留配置，权限控制及服务端版本递增正确。
16. 访问记录留存清理、指标标签和 TTL 故障路径有测试；聚焦通过不代替完整回归。

## 11. 下一阶段

设计和 Redis 存储选型已确认；实施计划位于 docs/superpowers/plans/2026-10-08-response-result-cache-implementation.md，按测试驱动顺序完成：契约/指纹 → 分组持久化 → Redis/租约 → 独立访问记录 → Chat 接入 → 前端配置 → 集成回归。

本轮只交付设计，不运行迁移、不改运行代码、不提交现有实现。流式、其他协议、有状态会话和语义缓存单独扩展。
