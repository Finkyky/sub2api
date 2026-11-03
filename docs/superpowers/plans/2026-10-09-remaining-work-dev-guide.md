# Sub2API 剩余开发总指引（RBAC / 结果缓存 / 通用插件）

日期：2026-10-09
分支：`feature/rbac-user-system`（HEAD `45f2b28`，其余全部为未提交工作区改动）
读者：接手开发的执行模型（gpt-6.1sol）
用途：替代零散交接记录，作为后续开发的唯一入口。各子系统原始设计仍以 `docs/superpowers/specs/` 为准，本文只说明“还剩什么、按什么顺序做、怎样算完成”。

---
## 2026-10-11 M5–M7 最新续接（替代下方历史“下一M5”）

M5任务后端/宿主能力、M6管理界面已完成功能；M7三示例安装包、SDK/开发文档、真PostgreSQL/HTTP/gRPC闭环与16项文本验收矩阵已完成。最终命令、测试数量、修复失败轮与四工件见 `2026-10-11-general-plugin-p4-p6-progress.md` 和 `D:\tmp\sub2api-m5-m7-20261011\VERIFICATION.txt`。

**本批下一项：Docker构建/启动/卷持久化/容器重启后恢复，真实登录/OTP走查单独记录。** Docker尚未执行，不把功能完成等同全部发布验收。M1真实RBAC浏览器、M2审核共享配置新鲜度/负载/指标仍保留。

D1暂缓新增收费/退款，D2兼容API渠道，D3四协议文本JSON/SSE。保留混合工作区，不提交、推送或部署。broker采用任务信封而非新增reverse RPC；pricing_ids可选，缺省明确为channel-wide授权。

---
## 2026-10-11 功能优先续接（当前开发入口）

用户最新要求：先把功能做出来，今晚推进完成；**D1暂缓新收费/退款、D2继续作为兼容API渠道**。D3先四种文本协议 Chat Completions、Responses、Anthropic Messages、Gemini Generate Content，JSON/SSE；音视频、Realtime等后置。不要再用旧D1/D2待确认门禁阻断开发，也不要每小项停下来等“继续”。

本批完成 M4 网关 input + strict output 文本主链，最终专项348项、race 794项、后端unit全量22798项通过（21 skip），build/vet exit0；详细实际状态、最终测试与四工件以 `plans/2026-10-11-general-plugin-p3-gateway-progress.md` 为准。真实旧/新租约、输出零提前字节、fallback重审、旧HIT隔离、usage关联已有对应测试。首批是文本子集，不表示接口所有tool/reasoning/音视频形状全部可用。

**功能下一项：M5任务后端 → M6任务/执行记录与分组链UI → M7示例/文档/Docker集中验收。** M3代码8/8保持，已有实例/配置/绑定API与界面不重做。收费规则D1不扩展；D2已决定不做原生上游适配服务插件。需要新增权限时直接补后端目录/前端目录及RBAC路由一致性，不再重复已完成的M0和M3收尾。

M2的审核共享配置新鲜度、负载/指标和M1浏览器仍是未完成验收项，不冒充已完成。旧快照和旧“下一T2.3/D1–D3门禁”均为历史记录，功能优先顺序以本节和用户最新决定为准。保留混合未提交工作区，不恢复HEAD，不reset/clean/stash，不提交/推送/部署。

```text
接续当前入口和2026-10-11-general-plugin-p3-gateway-progress.md。
M4最终验证证据完成后直接推进M5，不重做M3/渠道快照，不重复询问已决定的D1/D2。
D1暂缓；D2兼容API；D3四种文本JSON/SSE；暂不扩展音视频/Realtime。
先任务定义/手动执行/记录、再定时DB租约与fencing、授权broker和价格版本能力；随后接管理界面，集中做最终回归与部署验收。
只按真实代码和新测试报告状态，不把M4完成说成M5–M7也完成。
```

---
## 2026-10-11 T2.3 渠道请求权威快照（历史开发入口）

本批完成渠道TTL/失效通知延迟子项：成功early pin的Chat请求按ChannelService复用隔离快照，生产repository在readonly repeatable-read事务内读取渠道完整图与原样policy v3，匹配pin后用于映射/限制/定价/账号统计，不发布全局TTL缓存。错误、无接口、版本变化或跨group只使结果缓存BYPASS，正常转发回退原共享路径；未声明/显式bypass不新增渠道事务。

- 初审I1/I2/M1已行为RED后修正：typed usage context把pin/集合/平台选择器传入worker及同步降级，保留worker生命周期；HIT/Publish与scope失败/新source登记串行，已登记在途cell直接semantic_snapshot_pending BYPASS；MaxTokens及22项nullable字段深拷贝。真实双Chat×worker/无pool/已停止/队列满同步路径证明客户费用0.50（旧TTL0.14）、账号统计0.74、原quota口径0.50、来源ID关联及每请求snapshot一次。
- 入口：`plans/2026-10-11-result-cache-channel-authority-plan.md` / `plans/2026-10-11-result-cache-channel-authority-progress.md`。证据：`D:\tmp\sub2api-remaining-work-20261011-m2-channel-authority`；18目标/10原件/8新增，14Go冻结。最终宽专项/race各三轮1110pass/0skip/exit0；全量22452pass/21skip/exit0，build/vet空输出/exit0。复审0Critical/0Important，剩余1条非阻断C1旧Clone注释维护项。
- 四工件已真实创建/重开；副本字节精确补丁370pass、回滚284pass；10/10原件SHA恢复、8/8新增缺失、末目标篡改exit2且18/18无部分恢复。应用补丁须使用`git -c core.autocrlf=false apply`保持字节SHA。详细精确命令/输入/字面输出/退出码见progress/VERIFICATION；主工作区保留本批修改版，不恢复HEAD、不提交/推送/部署。
- **下一项仍为T2.3：ContentModeration/securityaudit审核配置的请求级新鲜语义与权威版本绑定。** 渠道子项不等于审核快照已验收；同事务一致性不等于所有interval/account-stat子表字段已进入v3摘要。当前全渠道投影性能预算留T2.4；本批PG未启动，T2.4/T2.5未验收。保留result-cache-policy-v3、general_runtime_unavailable、D1–D3/M4/M5及正常审核fail-open规则。

```text
读取本节和2026-10-11-result-cache-channel-authority-progress.md，继续审核共享配置新鲜度子项。
不要重复已验收的early pin、channel generation、渠道事务快照、usage context与scope提交边界。
先读policy v3真实材料及审核消费者/上下文边界，再设计请求级审核快照；避免每请求全局Reload/clear。
冻结新写集和原件SHA，测试先行，独立审查，真实副本补丁/回滚与四工件重开。
C1旧Clone注释在下一写集冻结后维护；保留混合工作区及全部既有门禁。
```

## 2026-10-11 T2.3 请求早期权威版本绑定（上一批，已完成）

本批完成请求处理期间权威版本切换子项：首次pin在认证后、模型白名单/composite改写之前；两个Chat handler幂等复用，Begin构造键/读取/申请lease前核对请求绑定。改变或早期失败仅缓存BYPASS，正常审核、上游、计费及group sharing权限保留。详见 `plans/2026-10-11-result-cache-request-policy-plan.md` / `plans/2026-10-11-result-cache-request-policy-progress.md`。

- 实际审核中版本切换12叶子RED 15fail；初审遗漏composite前处理补8叶子RED 11fail并修正。未声明/bypass、早期错误恢复与取消/timeout补齐。最终宽专项/race各三轮771pass/0skip/exit0；复审Critical0/Important0/Minor0。
- 证据 `D:\tmp\sub2api-remaining-work-20261011-m2-request-policy`，14目标/8原件/6新增；10Go源码冻结，最终全量22366pass/21skip/exit0，build/vet空输出/exit0；补丁副本75pass、回滚183pass、原件8/8恢复/新增6/6缺失、篡改预检14目标无部分恢复。四工件重开ZIP CRC/SHA匹配，详细精确命令/输入/输出以progress/VERIFICATION为准。保留本轮前混合工作区，不恢复HEAD，不提交/推送/部署，PG未启动。
- **下一项仍是T2.3：审核/渠道服务快照的新鲜度与权威版本绑定。** 本批只关闭首次pin之后请求处理期间版本变化，pin之前已陈旧的共享审核/渠道语义快照仍未验收。pin或内存generation均非新鲜度证明；result-cache-policy-v3、general_runtime_unavailable、D1–D3及M4/M5门禁保持。T2.4/T2.5未验收。

```text
读取本节和2026-10-11-result-cache-request-policy-progress.md。
已完成首次权威pin→请求审核/映射/复合路由→Begin期间变更的BYPASS保护，不重复本批。
下一批处理请求开始前已陈旧的审核/渠道语义快照，设计请求内新鲜快照与权威版本读前/读后绑定。
保留正常非声明请求行为、policy v3和计费门禁；新证据目录、原件SHA冻结、独立副本回滚与四工件重开。
T2.3整体仍未完成，PG本批未启动，Account冷初始化并发并未修复，不提交/推送/部署。
```
## 2026-10-11 T2.3 渠道快照发布竞态（历史快照）

本轮完成ChannelService失效后旧构建发布竞态子项及前轮fallback身份Minor；T2.3整体仍在进行。详见`plans/2026-10-11-channel-snapshot-race-plan.md`与`plans/2026-10-11-channel-snapshot-race-progress.md`，下方fallback/PG/双入口均为历史快照。

- 同锁代次推进和成功/错误短缓存发布保护，旧singleflight已发布结果交付复检；DoChan允许取消先退出，健康等待者与独立限时DB构建继续。覆盖两读取阶段、本地/远端、成功/错误、两次失效、已发布旧结果creator/waiter、自回显与取消当前retry；新专项22pass，旧行为16fail事件（均有实际命令结果）。
- 独立审查2 Important及自身通知Minor已闭合，写集文档Minor同步；fallback恢复/warm身份6行补测闭合。首次广race发现原parallel共享冷Account，原件副本13pass/14fail事件复现；只隔离测试对象和嵌套maps，保留parallel/断言，未改Account生产代码，不声称并发冷初始化已修复。
- 最终相同三包专项三轮1389pass、race三轮1389pass，均0skip/exit0。本轮证据D:\tmp\sub2api-remaining-work-20261011-m2-channel-race，8目标/5原件/3新增；四Go源码冻结，全量22292pass/21skip/exit0，build/vet exit0；最终审查无Critical/Important/Minor。交付详细以progress及VERIFICATION为准。回滚基于本轮前混合工作区而非HEAD；PG本轮未启动。
- **下一项仍为T2.3**：审核服务快照与权威版本绑定、渠道TTL/通知延迟窗口及请求早期语义版本绑定。内存generation不等于PolicyVersion，不能由本轮推导旧审核结论可用于新版本缓存。T2.4/T2.5未验收，保持result-cache-policy-v3、general_runtime_unavailable、D1–D3和M4/M5门禁。

```text
读取最新渠道快照节与2026-10-11-channel-snapshot-race-progress.md。
失效后旧渠道成功/失败发布、旧singleflight交付、取消与自回显已验收，fallback身份Minor已闭合，不重复本轮矩阵。
继续审核缓存快照与权威版本绑定、渠道TTL/通知延迟窗口和请求语义版本绑定；先追踪早期审核/映射与ResultCache.Begin读取当前版本的时序。
T2.3整体未完成，不把内存generation冒充权威版本；PG本轮未启动，生产Account冷初始化并发安全未在本轮修复。
新批次新证据目录，冻结原件SHA，保持混合未提交工作区，不恢复HEAD、不提交/推送/部署、不reset/clean/stash。
```

## 2026-10-11 T2.3 跨模型 fallback 快照（历史快照）

本轮接续 PG 链路，完成真实账号 failover 跨模型不回填这一项；T2.3 整体仍在进行。详见 `plans/2026-10-11-result-cache-fallback-plan.md` 与 `plans/2026-10-11-result-cache-fallback-progress.md`。下方 PG/双入口/T2.2/T3.x 为历史快照。

- 两实际 Chat 入口主账号 HTTP503→生产重新选号→备用账号 A映射B；read_write/write_only UNCACHEABLE，read_only MISS。覆盖 B谎报A、同模型保守拒回填、重复真实转发、恢复A后MISS/HIT、warm A不被write_only B覆盖以及source关联。
- 真实单worker计费：失败尝试零账本，成功备用账号一次；bypass计费命令/金额/非零原生缓存token（read13/create7）/配额差分相等，HIT不产生新计费。新增验收初次即通过，未改生产代码；仅在独立副本移除modelMatches发布保护做变异RED，实测A键命中B内容，随后恢复生产文件SHA；映射identity变异亦被字面量B断言捕获。最终三轮专项453pass/race453pass，冻结代码全量22270pass/21skip/exit0，build/vet exit0；复审无Critical/Important，1项非阻塞Minor留下一批。
- 本轮证据 `D:\tmp\sub2api-remaining-work-20261011-m2-fallback`，6目标/3原件/3新增。保留本轮前混合工作区，不恢复HEAD。真实HTTP/worker、观测仓储/miniredis；本轮未启动PG，不声称验证Antigravity设置驱动inline fallback。
- **下一项仍是T2.3**：渠道/审核服务缓存快照与权威版本的在途竞态，可补Anthropic原生提示缓存转换差分；之后才评估T2.3整体完成并进入T2.4/T2.5。保持result-cache-policy-v3、general_runtime_unavailable、D1–D3与M4/M5门禁，不提交/推送/部署、不reset/clean/stash。

```text
读取本节与2026-10-11-result-cache-fallback-progress.md。
跨模型真实账号failover不回填和bypass计费差分已验收，生产逻辑未改；不重复已有PG和双入口/worker矩阵。
从渠道/审核服务缓存快照与T2.2权威版本在途竞态继续，先定位服务缓存版本刷新与正在生成的请求观察链；顺手补本progress记录的恢复/warm局部身份Minor。
T2.3整体仍未完成，PG本轮未启动，general_runtime_unavailable与D1–D3、M4/M5门禁保持不变。
新批次新证据目录，冻结当前原件SHA，保持混合未提交工作区；不恢复HEAD，不提交/推送/部署。
```

## 2026-10-11 T2.3 真实 PostgreSQL 链路快照（历史快照）

本轮接续上一批，已完成真实 PostgreSQL usage/access/ledger/source 关联这一项与策略记账污染修复；T2.3 整体仍在进行。详细见 `plans/2026-10-11-result-cache-pg-chain-plan.md`、`plans/2026-10-11-result-cache-pg-chain-progress.md`。下方双入口/T2.2/T3.x 为历史快照。

- 两入口接入生产PG用户/Key/账号/订阅/用量/原子计费/权威版本/访问仓储，真实worker与计数HTTP上游。MISS→HIT→write_only(UPDATED)→HIT，每入口 usage2/ledger2/access4/upstream2；SQL关联共同request_id/source，HIT余额、Key三个速率窗口、账号总/日/周配额与窗口及cache_read_tokens不增加。两次不同生成回答验证write_only更新内容。
- 实库RED定位正常计费污染v2版本。v3哈希完整账号内容，仅剔除updated_at/last_used_at及extra的7项明确记账字段；配额配置、未知extra、过期/父账号/并发/费率等语义字段继续失效。初审1项Important与3项Minor已修正并新增回归；最终PG专项三轮240pass、race三轮240pass，均0skip/exit0。早期全量两项keepalive失败记录保留，单独十轮20pass；冻结最终代码后unit专项三轮411pass、全量22229pass/21skip/exit0，build/vet exit0，复审无剩余Critical/Important/Minor。
- 使用独立sub2api_it_*库，不迁移输入DSN的数据库；Redis仍为miniredis，不称为外部Redis/供应商部署验收。本轮证据 `D:\tmp\sub2api-remaining-work-20261011-m2-pg-chain`，9目标/6原件/3新增。独立副本回滚，主工作区保留修改版，不恢复HEAD。
- **下一项仍是T2.3**：跨模型fallback不回填，渠道/审核缓存快照与权威版本的在途竞态；可补Anthropic原生提示缓存转换差分。以上完成后才评估T2.3整体完成，进入T2.4/T2.5。保持general_runtime_unavailable、D1–D3、不实现M4/M5、不提交/推送/部署、不reset/clean/stash。

```text
读取本节与2026-10-11-result-cache-pg-chain-progress.md。
本轮真实PG usage/access/ledger/source和记账版本污染修复已验收；独立副本回滚恢复6原件/删除3新增，原基线110pass，旧行为探针12fail事件/10pass/exit1已移除；独立测试库0，PG已停止。
继续跨模型fallback与渠道/审核服务缓存快照在途竞态，不重复已有双入口/worker矩阵。
保持混合未提交工作区和D盘环境，不恢复HEAD，不提交/推送/部署。
```

---


## 2026-10-11 T2.3 双入口与异步计费快照（历史快照）

本轮完成 **T2.3 的真实双入口路由与异步计费矩阵及发现的缺口修复**，T2.3 整体验收仍进行中。详细见 `plans/2026-10-11-result-cache-chain-plan.md`、`plans/2026-10-11-result-cache-chain-progress.md`；下方 T2.2/T3.x 均为历史快照，以本节为准。

- 使用生产 RegisterGatewayRoutes/APIKeyAuth/GroupModelAllowlist、OpenAI 与 Anthropic 实际 Chat handler/service、Standard 准入、计数 HTTP 上游。MISS/HIT/write_only 关联实际账本与用量；HIT 不新增计费/账号配额/cache_read_tokens。热缓存凭证、模型、审核、两层余额及订阅拦截通过。
- 先 RED 后修复：cache access/header/usage 使用共同计费 ID，source 引用生成账本；最终 ID schema64 超长稳定摘要、短值不变；上游发送/声明模型必须确定一致、冲突/空值不回填；Anthropic buffered 补实际模型观察；两入口异步用量在 Submit 前快照字段，不读取已复用 Gin context。真实 worker 延迟、父请求取消和模型污染探针通过。
- 最新专项三轮330 pass、race三轮330 pass，均0skip/exit0；unit全量 **22229 pass、21 test skip、63无测试包、exit0**，build/vet exit0。独立审查的3项Important均已有RED→修正→回归；原有parallel测试5处Gin.SetMode写入移除，parallel断言保持。独立复审无剩余Critical/Important；最终回滚字面证据见本轮VERIFICATION。
- 本轮证据 `D:\tmp\sub2api-remaining-work-20261011-m2-chain`：MODIFIED_FILE.zip、DIFF_FILE.patch、VERIFICATION.txt、可执行ROLLBACK.sh；13目标/9原件/4新增。基线37 pass/0skip/exit0，修改版矩阵与最终全量已实测；独立副本恢复9原件/删除4新增，原基线37 pass；旧行为probe30 fail事件/28 pass/exit1已移除；补丁13/13逐字节应用、末目标篡改exit2且无部分恢复，详见VERIFICATION。主工作区保留修改版，不恢复HEAD。
- 仓储为可观察fixture并执行schema64、Redis为miniredis。本轮没有启动PG；不将这些结果称为实际PG端到端落库或外部Redis部署验收。OpenAI原生控制字段剥离/提示缓存字段保留通过，Anthropic转换的原生提示缓存差分可继续补测。
- **下一批仍为T2.3**：真实PG usage/access/ledger/source关联、跨模型fallback、渠道/审核缓存快照与T2.2权威版本之间的在途竞态。完成后才进入T2.4故障负载与T2.5指标。T2.1/T2.2及M3代码8/8保持；不提前实现M4/M5、不代决D1–D3、保留general_runtime_unavailable，不提交/推送/部署、不reset/clean/stash。

```text
读取 D:\projects\sub2api\docs\superpowers\plans\2026-10-09-remaining-work-dev-guide.md 顶部T2.3快照及 result-cache-chain-progress。
继续T2.3剩余真实PG链路关联、跨模型fallback与渠道/审核快照在途竞态；不重复本轮已验收的两个真实handler路由/Standard准入/真实worker矩阵。
本轮没有启动PG；需要时按T2.2独立测试harness重新启动，结束清理sub2api_it_*库并停止。
保持混合未提交工作区与D盘环境、general_runtime_unavailable及D1–D3门禁；不提交/推送/部署、不reset/clean/stash。
```

---

## 2026-10-11 T2.2 续接快照（历史入口）

本轮接续完成 **T2.2 缓存指纹的真实版本来源**，计划与详细记录见 `plans/2026-10-11-result-cache-version-plan.md`、`plans/2026-10-11-result-cache-version-progress.md`。下方T3.8/T3.7等均为历史快照，以本节为准。

- 原生产缓存已经使用权威PolicyVersion，但仅聚合group/account/channel更新时间和MAX(settings.updated_at)。真实PG复现保留时间戳的模型映射变更、被更晚时间戳遮蔽的审核设置修改/删除仍HIT，以及在途仍回填。
- 只修改policy repository生产来源：单条SQL中effective_route_revision聚合分组、账号配置/关系优先级、渠道配置和模型定价；input_policy_revision取按key排序的完整settings键值。数据库内SHA256加v2域分隔，仍返回原三列config/status/version，不返回credentials/settings明文、不新增客户端字段/迁移/依赖。全settings保守失效会让无关设置降低命中率。
- 缓存专项基线48项→修改版55项，均零skip；PG缓存三轮162项（包含单元与真实PG），普通race三轮165项，新PG专项race三轮63项，均零skip/exit0。unit全量 **22159项通过、21 test skip、63无测试包、exit0**；build/vet exit0。新增PG专项21含子项，16来源变化HIT→MISS、2在途变更不回填、确定性/隔离/摘要契约。
- 独立只读审查无Critical/Important问题；多行排序/无关渠道、价格NULL转换补测和生产规模查询成本为后续建议，不宣称已经负载验收。使用真实PG18.1和miniredis，不等同完整handler/RBAC/审核/计费或外部Redis服务器。
- 证据：`D:\tmp\sub2api-remaining-work-20261011-m2-versions`，四交付MODIFIED_FILE.zip、DIFF_FILE.patch、VERIFICATION.txt、executable ROLLBACK.sh。独立副本恢复4原件/删除3新增，原缓存48项与PG26项通过；重新安装行为probe后旧缺口再次产生19fail事件/2pass/exit1，probe已移除。主工作区保持修改版，八项service/handler/依赖invariant不变。
- 临时sub2api_it_*数据库查询为0；PG停止exit0、status exit3无server。后续需要真实PG时重新启动，不假设实例仍在运行。
- T2.1/T2.2已完成，下一 **T2.3完整网关链路验收**；T2.4故障负载/T2.5指标仍待验收。M3代码8/8保持，浏览器、HTTP信号/Docker与M4–M7仍有剩余工作。保留general_runtime_unavailable与D1–D3决策门禁，不提交/推送/部署，不reset/clean/stash。

### 下一项：T2.3 完整网关链路验收

1. 阅读第5节、缓存spec、客户端文档及本轮progress；定位Chat兼容两入口、路由鉴权/RBAC/模型权限、审核、准入、缓存和实际RecordUsage路径。
2. 用真实handler路由栈+计数模拟上游证明过期/禁用凭证、禁止模型、余额/订阅不足、审核拒绝不能由HIT放行；先RED后最小修复。
3. 验证HIT新request_id、不调用RecordUsage、不累加来源账号配额、不写cache_read_tokens；MISS/write_only照常计费并与访问记录关联。
4. 核对fallback/账号映射/上游实际模型不同不回填、控制字段不发上游，以及渠道/审核缓存快照变化的在途竞态。不用本轮repository+service通过替代完整链路。

```text
读取 D:\projects\sub2api\docs\superpowers\plans\2026-10-09-remaining-work-dev-guide.md 顶部T2.2快照。
T2.1/T2.2及T3.1–T3.8代码任务已完成，不重做。从第5节T2.3完整网关链路验收继续，先真实路由/模拟上游RED，再最小修复和新回归。
保持混合未提交工作区、D盘环境和general_runtime_unavailable；不提交/推送/部署、不reset/clean/stash。PG已停止，需真实PG时重新启动独立测试实例。
```

---
## 2026-10-11 T3.8 续接快照（历史入口）

本轮接续完成 **T3.8 Wire 与应用关闭生命周期**，详细记录见 `plans/2026-10-11-plugin-instance-lifecycle-progress.md`，实施计划见 `plans/2026-10-11-plugin-instance-lifecycle-plan.md`。下方 T3.7/T3.6 与 2026-10-09 各快照为历史状态，以本节为准。

- Wire 的 cleanup 与 handler 共享唯一 `PluginInstanceService`；插件关闭链依次实例 Close → manager Stop，其余业务服务保持并行，最后 Redis → Ent。整个 cleanup 由 `sync.Once` 保证重复调用只执行一次、并发调用等待同一次清理。
- 真实 Application.Cleanup fixture 三轮、6 场景：current 与 retired 各持租约，两个真实 GeneralPlugin PID 全部退出；active 协调循环实际取消并 join；旧/新 lease 均拒绝新工作，repository enabled/generation/revision 不变。内存 repository，不是 PG，不等同完整 initializeApplication/HTTP 信号退出或 Docker 验收。
- 最终 cmd/server **25 项通过、零跳过**；race 三轮 **378 项通过、零跳过**；后端 unit 全量 **22152 项通过、21 test skip、63 无测试包、exit0**，build/vet 均 exit0。Wire CLI 重复生成字节一致。完整命令、skip 原因与字面结果见本轮 VERIFICATION.txt/FULL_SKIPS.json。
- 独立审查无 Critical/Important 生产装配缺陷；Redis 关闭错误后 Ent 继续关闭的 I1 回归缺口已补测，独立副本 continue→return mutation 真实 RED，恢复后 GREEN。Minor M1/M2 的测试超时收尾/非 Windows 空格路径健壮性保留为后续项，未宣称修复。
- **M3 代码任务 8/8：T3.1–T3.8 完成，不代表整个项目完成。** 保留 general_runtime_unavailable；浏览器端到端、完整 HTTP 信号退出、缓存版本/网关/负载、M4–M7/Docker 未由本轮测试替代。前端/PG 本轮未重跑。
- 证据：`D:\tmp\sub2api-remaining-work-20261011-m3-lifecycle`，四工件为 MODIFIED_FILE.zip、DIFF_FILE.patch、VERIFICATION.txt、executable ROLLBACK.sh。独立副本回滚恢复4原件/删除4新增；cmd/server恢复2项，T3.7实例专项103项仍通过；最后文件篡改拒绝且不部分恢复；主工作区保持修改版。
- 工作区保持 `feature/rbac-user-system` 混合未提交状态，不提交、推送、部署，不 reset/clean/stash。现有10秒context不是全局关闭硬上限。

### 下一项：T2.2 缓存指纹的真实版本来源

1. 阅读结果缓存 spec/进度和本指引第5节，定位 `effective_route_revision` / `input_policy_revision` 的生成、调用和可变配置来源。
2. 先写修改模型映射/路由或审核配置后旧键不能命中的 RED；核对实际换组、有效策略和渠道模型映射路径，再做最小实现与版本失效回归。
3. 保留计费/响应语义和插件门禁，按写集执行专项、race和完整回归，记录新证据，不复用本轮通过结果。
4. T1.2 浏览器端到端、T2.3–T2.5 仍独立待验收；M4/M5 开始前确认 D1/D2/D3 并写详细计划，不默认接入插件网关或解除门禁。

```text
读取 D:\projects\sub2api\docs\superpowers\plans\2026-10-09-remaining-work-dev-guide.md 顶部 T3.8 快照。
T3.1–T3.8 的 M3 代码任务已完成，不重做。从第5节 T2.2 缓存真实版本来源继续，先定位路由/模型映射/审核配置版本链，再 RED/GREEN 开发。
保留混合未提交工作区与 D盘环境；不提交/推送/部署，不 reset/clean/stash；保留 general_runtime_unavailable。浏览器、完整 HTTP 信号/Docker、缓存完整网关与负载、M4–M7仍有独立剩余工作。
```

---
## 2026-10-11 T3.7 续接快照（历史入口）

本轮接续完成 **T3.7 跨节点 generation 协调**，详细记录见 `plans/2026-10-11-plugin-instance-reconcile-progress.md`，实施计划见 `plans/2026-10-11-plugin-instance-reconcile-plan.md`。下方 T3.6 与 2026-10-09 各快照均为历史状态，以本节为准。

- 本节点句柄保存 generation；后台只协调暖实例，完整权威读取后统一撤回停用/删除/过代句柄，再轮转启动并二次核对候选。冷实例不预热，草稿不发布，数据库只读，旧 lease 自然排空。
- 独立审查修正暖 Acquire 失败 intent 丢失、崩溃历史过早 superseded、慢 Close 阻断统一撤回；同进程 PG fixture 隔离与同时 current/retired 真实进程关闭也补齐，复审 R1–R4/C1 已关闭。
- 新增协调专项 **41 项通过、零跳过**；race 三轮 **309 项通过、零跳过**；真实 PG 两节点+子进程同进程三轮通过、零跳过，实例 PG 回归40项通过。后端 unit 全量 **22129 项通过、21 test skip、63 无测试包、exit0**，build/vet 均 exit0。跳过原因与完整命令见本轮记录和 VERIFICATION.txt。
- 可配置 interval=5s、timeout=30s；默认停用/删除撤回条件上限 **65s**，不含 DB 故障、超预算/忽略取消或系统暂停，旧 lease 排空不受该上限。20ms/2s 实库三轮传播23.1693ms/19.3778ms/27.5242ms。
- **M3 业务代码任务 7/8：T3.1–T3.7 完成，T3.8 应用级 Wire 关闭装配未完成。** 保留 general_runtime_unavailable；真实浏览器、缓存完整网关/版本/负载、M4–M7/Docker 未被本轮验收替代。前端 T3.6 2472 项是上一轮结果，本轮未重跑。
- 证据：`D:\tmp\sub2api-remaining-work-20261011-m3-reconcile`，四交付为 MODIFIED_FILE.zip、DIFF_FILE.patch、VERIFICATION.txt、ROLLBACK.sh；原 SHA、真实测试与独立副本回滚见最终 VERIFICATION.txt。测试 PG 已停止。
- 工作区仍为 `feature/rbac-user-system` 混合未提交状态，不提交、推送、部署，不 reset/clean/stash。

### 下一项：T3.8 Wire 与应用关闭生命周期

1. 阅读本节、本轮 progress、cmd/server/wire.go、wire_gen.go，以及 PluginInstanceService/PluginManager/ResultCacheService 的 Close 和当前 cleanup 顺序。
2. 明确关闭顺序与重复调用契约；先写应用级关闭装配 RED，不以服务自身 Close 单元测试冒充 Wire 清理完成。
3. 将实例循环与当前/排空子进程纳入真实应用退出，执行 build/Wire/子进程退出与并发回归；保留其他服务关闭行为和既有未提交改动。
4. T1.2、T2.2–T2.5 与 M4/M5 产品决策仍是独立剩余任务；T3.8 完成也不代表整个项目完成。

---
## 2026-10-11 T3.6 续接快照（历史入口）


本轮已读取 2026-10-09 收工记录，接续完成 **T3.6 插件实例前端代码与组件/API 验收**。详细记录见 `plans/2026-10-11-plugin-instance-frontend-progress.md`；此前各节为历史记录，不覆盖本节。

- 结构化 Manifest 绑定编辑器、只写 JSON 新 revision、只读状态面板、删除确认、精确权限/step-up、409 显式刷新及中英文 i18n 已实现。
- 独立审查修正 bindings:null、真实中文文案、撤权再授权复活旧操作、创建晚到续体和绑定数量/排序边界；67 项专项回归通过。
- 本轮最终前端 typecheck、lint:check、check:i18n、test:run、build 全部 exit0；全量 **319 文件 / 2472 项通过**。基线为 316 文件 / 2405 项，未复用旧结果。
- **M3 代码任务为 6/8：T3.1–T3.6 完成，T3.7/T3.8 未完成。** 真实浏览器登录/权限/OTP/前后端端到端验收本轮未执行，后端/PG未改动也未重跑；这些状态不得由组件通过替代。
- 证据：`D:\tmp\sub2api-remaining-work-20261011-m3-frontend`，四项交付为 MODIFIED_FILE.zip、DIFF_FILE.patch、VERIFICATION.txt、ROLLBACK.sh；原 SHA、测试、回滚副本及实际结果见 VERIFICATION.txt。
- 工作区继续保持 `feature/rbac-user-system` 混合未提交状态；不提交、推送或部署，不 reset/clean/stash。

### 下一项：T3.7 跨节点 generation 协调

1. 阅读第 6 节及 PluginInstanceService 的 Publish/Acquire/Disable/Delete/Close 实现，核对数据库 generation 与本节点句柄状态。
2. 明确可配置周期、循环启动/取消/关闭、失败重试和本节点租约排空边界；先写详细计划和失败测试。
3. 测试跨节点发布/停用/删除传播及其时间上限，不将状态查询变成隐式启动；保留旧租约自然排空和网关兼容门禁。
4. T3.7 验收后推进 T3.8 Wire 关闭装配；T1.2、T2.2–T2.5、M4/M5 产品决策及 Docker 验收仍未完成。

---

## 2026-10-09 收工快照（明日续接入口）

记录日期：**2026-10-09**；明天 **2026-10-10** 从本节继续。**本节为 2026-10-09 历史状态，以顶部 2026-10-11 续接快照为准。** 本次只整理交接，不修改业务代码；T3.6 尚未开始。

### 已完成 / 未完成

| 模块 | 已完成 | 未完成或待验收 |
| --- | --- | --- |
| M0 环境与基线 | T0.1–T0.4：独立PostgreSQL、契约与测试环境修补、后端/前端全量基线 | 后续每个切片仍需运行本切片验收，不能沿用旧基线冒充新结果 |
| M1 RBAC | 首版后端/前端功能；T1.1实库三轮；当前7个实例权限码、10条实例路由策略与前端权限同步 | T1.2 浏览器端到端；T1.3根据验收问题补回归并修复；T1.4对未来新增权限继续同步，尚未作为整个后续开发的最终验收关闭 |
| M2 结果缓存 | 解析/指纹/策略/Redis租约与合并/入口接入等现有实现；T2.1实库三轮 | T2.2–T2.5：真实版本来源、完整网关/计费链路、Redis故障与并发负载、指标低基数核对 |
| M3 插件P2c | **T3.1** 只读status API；**T3.2** 事务硬删除/冻结解锁/本节点租约约束；**T3.3** 六类写操作脱敏审计；**T3.4** 生产路由/中间件HTTP错误矩阵；**T3.5** PG事务/CAS/候选进程等集成验证 | **T3.6** 前端配置/绑定/状态/删除/i18n；**T3.7** 跨节点generation协调与传播上限；**T3.8** Wire关闭装配及子进程退出 |
| M4/M5 插件P3/P4 | 保留现有协议/执行核心与兼容门禁，不据此声明网关或任务平台已实现 | 网关Hook/严格输出缓冲、独立任务/宿主能力尚未开始；先确认D1/D3、D2并制定详细计划 |
| M6/M7 插件P5/P6 | 现有最小管理页不等价完整管理平台 | 完整管理界面、示例插件/SDK文档、全链路及Docker验收未完成 |

**M3 为 5/8**（T3.1–T3.5完成；T3.6–T3.8未完成），不是整个项目完成度。RBAC浏览器、结果缓存网关/负载、插件跨节点、完整管理界面和Docker尚未验收；单元/组件/实库通过不替代这些验收。

### 明天第一项：T3.6（从设计/现有页面核对开始）

1. 先读本文件第6节、`specs/2026-09-30-general-plugin-platform-design.md`、`specs/2026-10-08-general-plugin-p2c-admin-design.md`和`plans/2026-10-09-plugin-instance-http-progress.md`。核对已有页面/API/权限/i18n，不重做T3.1–T3.5。
2. 读取`frontend/src/views/admin/PluginInstancesView.vue`、`frontend/src/api/admin/pluginInstances.ts`、现有权限操作策略、Manifest类型/来源、组件与API测试，以及中英文locale。先明确T3.6组件划分、写集和测试计划，再实现。
3. 结构化绑定编辑器：分组、stage、entry、protocol、sort_order、enabled、can_replace；Manifest声明决定可选项，替换JSON textarea。
4. 新配置revision编辑器：有config schema时生成表单，否则JSON编辑+客户端校验；**配置只写不回显**。
5. 补齐只读状态面板、精确权限+step-up删除、409提示刷新、失败反馈；全部文案纳入中英文i18n。
6. 先RED后GREEN；Vitest覆盖权限隐藏、撤权、冲突、失败反馈及配置不回显；按第0节执行前端typecheck/lint/test/build/check:i18n。浏览器环境届时重新检查，真实验收有证据才勾选。
7. T3.6验收/审查/交付记录完成后再推进T3.7、T3.8。T1.2和T2.2–T2.5可作为独立剩余任务继续排期，不因先推进T3.6而视为完成。

### 已冻结的接口语义

- runtime unavailable **422**；service closed优先 **503**；reason固定`PLUGIN_INSTANCE_UNAVAILABLE`。
- 生产缺step-up grant **403 STEP_UP_REQUIRED**；旧T3.3 audit-only fixture的428不是生产契约。保留现有更严格的写操作step-up要求。
- 非法JSON/非正ID/generation/revision/缺config **400**，未知实例或revision **404**，CAS/启用或busy删除 **409**。
- 未知读取保持合法原HTTP错误状态，但只返回固定`PLUGIN_INSTANCE_READ_FAILED`，不透传原metadata/cause/异常文本。
- Publish/Acquire的parent取消或截止不覆盖LastError；候选自身timeout仍记录真实runtime失败；失败不推进DB generation，也不损害旧runtime/租约。
- `general_runtime_unavailable`兼容门禁保持；M4/M5之前不提前接网关或解除门禁。

### 最新验证与证据入口（2026-10-09 T3.4）

| 记录 | 实际结果 |
| --- | --- |
| UNIT_FINAL | exit0，5包，136顶层/878含子用例pass，零fail/skip |
| PG_FINAL | exit0，三轮84顶层/117含子pass；39真实PG+45仓储单元，零fail/skip；新HTTP链51条审计，旧T3.3链42条 |
| RACE_FINAL | exit0，三轮5包，408顶层/2634含子pass，零fail/skip |
| BACKEND_FULL_FINAL | exit0，58包，6729顶层/13761含子pass；另68无测试包skip事件、17测试skip，零fail，具体原因保留在JSON |
| BUILD_FINAL / VET_FINAL | 均exit0 |
| FRONTEND_CONTRACT | exit0，4文件40项；本轮完整前端全量、完整unit-tag全量未重跑，M0/T3.3历史全量不能冒充本轮结果 |
| 独立审查/回滚交付 | I1/M1闭环；9项副本原SHA/旧行为/基线及篡改保护通过，主工作区保持修改版；PG清理完成并已停止 |

T3.4证据：`D:\tmp\sub2api-remaining-work-20261009-m3-http`，其中`VERIFICATION.txt`含完整命令/输入/输出/退出码，`MODIFIED_FILE.zip`、`DIFF_FILE.patch`、`ROLLBACK.sh`为本轮冻结交付。status/delete/audit证据和细节分别见对应`2026-10-09-plugin-instance-*-progress.md`。**本次收工记录仅更新此总指引，T3.4交付物保留原快照，不因文档续记覆盖。**

### 工作区与执行环境

- 仓库`D:\projects\sub2api`，分支`feature/rbac-user-system`，HEAD `45f2b2845fdb495d6437e56309dff4084eedb1fc`；混合未提交工作区继续保留，不提交、不推送、不部署。不reset/clean/stash，不用HEAD覆盖当前文件。
- 所有开发、证据、缓存及临时文件使用D盘；可复用`D:\tmp\sub2api-remaining-work-20261009-m3-http\check.py`，新的验收标签/目录需独立，不覆盖已有成功或失败记录。
- Go环境：`GOMODCACHE=D:/projects/goprojects/pkg/mod`、`GOCACHE=D:/tmp/sub2api-go-build-cache`、`GOTMPDIR=D:/tmp/sub2api-go-tmp`、`GOMAXPROCS=4`；`TEMP=TMP=D:/tmp/sub2api-temp`；`npm_config_cache=D:/tmp/sub2api-npm-cache`；PATH前置`D:/software/Git/usr/bin`。race用`CGO_ENABLED=1`。
- 如需再跑真实PG，先恢复独立测试实例并设置`SUB2API_TEST_POSTGRES_DSN`，不要假设此前已停止的PG仍在运行。T3.6纯文档/前端工作不为旧验收重启PG。
- D1–D7仍按第11节处理：现阶段保留D6更严格step-up，不替用户决定D1/D2/D3，不执行D7提交/推送/部署。

### 明天可直接使用的续接指令

```text
先读取 D:\projects\sub2api\docs\superpowers\plans\2026-10-09-remaining-work-dev-guide.md 顶部收工快照，
再读T3.6相关spec、现有前端页面/API/权限/i18n和T3.4进度文档。
从T3.6插件实例前端配置/结构化绑定/状态/删除/i18n继续，先确认组件与测试计划，再RED/GREEN开发。
T3.1–T3.5已经完成，不重做；T3.6–T3.8、RBAC浏览器、缓存网关/负载、M4–M7仍未完成。
保持D盘环境和混合未提交工作区；不提交、不推送、不部署；保留既有接口语义与插件门禁。
```

---

## 0. 执行规则（必须遵守）

1. **工作区是混合未提交状态。** 不得执行 `git reset --hard`、`git clean`、`git checkout -- <dir>`、`git stash`，不得用 HEAD 版本覆盖任何文件（尤其是 `backend/ent/` 生成代码和 `wire_gen.go`）。不 commit、不 push、不部署，除非用户另行明确授权。
2. **先读后改。** 每个任务开始前读本任务列出的文件和对应 spec；不重做已完成部分（第 1 节列出）。
3. **测试驱动。** 先写失败测试并运行确认 RED，再实现到 GREEN。每个任务结束运行该任务的验收命令，并把真实命令、退出码、关键输出写入对应进度文档。
4. **如实报告。** 编译通过 ≠ 测试通过；单元/组件测试 ≠ PostgreSQL 集成 ≠ 浏览器验收。没有运行的项目写“未执行”及原因，跳过的测试写“跳过”，不得写成通过。
5. **迁移编号。** 当前最新迁移为 `253_general_plugin_instances.sql`。新增迁移从 243 起，创建前重新 `ls backend/migrations` 确认；不得修改已有迁移来代替新迁移。
6. **代码生成。** 修改 Ent schema 后运行 `go generate ./ent`；修改 Wire 后运行 `wire ./cmd/server`。生成后检查 diff 只包含预期增量。
7. **权限。** 每新增一条管理路由，必须同时在 `backend/internal/server/middleware/admin_route_permission.go` 加精确策略、在 `backend/internal/service/permission_catalog.go` 补权限码（如需新码），并让 `routes/admin_permission_coverage_test.go` 通过。未映射路由默认 403 是设计行为，不要放宽。
8. **敏感数据。** API 投影、日志、审计中不得出现插件配置明文/密文、安装路径、缓存正文、凭据。
9. **遇到第 11 节“待用户决策”的问题时停下来询问**，不要自行假设产品规则；与该问题无关的任务继续推进。
10. **Windows 环境注意：** 部分测试需要 `sh`，测试前把 `D:\software\Git\usr\bin` 加入 `PATH`；Go/npm 缓存、临时文件和证据统一使用 D 盘目录。中文文档用 UTF-8 写入，不要经 PowerShell 默认编码管道。

### 常用验证命令

```text
# 后端（backend/）
go build ./cmd/server
go vet ./...
go test -p=1 ./... -count=1 -timeout=600s
go test -tags integration ./internal/repository -run '<Pattern>' -count=1 -v -timeout=300s   # 需要 Docker 或 SUB2API_TEST_POSTGRES_DSN

# 前端（frontend/）
pnpm typecheck && pnpm lint:check && pnpm test:run && pnpm build
pnpm run check:i18n
```

---

## 1. 现状总览（已核对代码与交接文档）

| 子系统 | 已完成 | 未完成 |
| --- | --- | --- |
| RBAC | 角色/权限/用户多角色 Ent 模型与迁移 239；83 个权限码；453 条管理路由精确策略（含 pages，其中 42 条超管专属）；JWT/WS/Passkey/后端模式使用服务端权限快照；敏感用户变更事务授权与超管保护；前端角色/权限页面、路由守卫、`v-permission` 指令、撤权刷新；9 项 PG 集成测试通过 | 浏览器端到端验收从未执行；审计仍是异步 HTTP（非 outbox）；用户角色筛选仅当前页 |
| 结果缓存 | 协议解析、指纹、策略、Redis 存储与所有权租约、独立连接池、请求合并、访问记录与 7 天清理、两条 Chat 入口接入、分组表单、运行参数、迁移 240/241、协议文档 | 完整网关链路验收（失效凭证/禁用模型/审核拒绝/真实计费关联）；Redis 故障与负载测试 |
| 通用插件 | P1 契约与串行执行核心（`internal/pluginflow`）；P2a schema2 清单；P2b GeneralPlugin gRPC + SDK + 进程句柄；P2c 实例/不可变配置版本/分组绑定仓储（迁移 242）、发布/回退/租约服务、10 个管理 API（含 status/删除）+ RBAC + 写操作 step-up、六类写审计、HTTP错误/脱敏矩阵、PG验证、最小前端页 | P2c：前端配置/绑定编辑器、状态/删除面板与i18n、跨节点协调、启动/关闭装配核对（status/删除API、审计、HTTP错误矩阵与PG已完成）；P3 网关 Hook 与严格缓冲（零实现）；P4 任务调度与宿主能力（零实现）；P5 完整管理界面；P6 示例插件、SDK 文档、全链路与 Docker 验收；`general_runtime_unavailable` 门禁仍在 |

已确认不存在的文件（需新建）：`plugin_gateway_hooks.go`、`plugin_response_buffer.go`、`plugin_task_service.go`、`plugin_task_scheduler.go`、`plugin_pricing_capability.go`、`plugin_task_repo.go`、`plugin_task_handler.go`、`plugin_instance_admin*.go`；初始快照的插件实例 handler 曾无 `SetAuditAction/SetAuditExtra` 调用，现 T3.3 已完成；初始快照曾缺少 `/status` 路由和删除接口，现已由 T3.1/T3.2 补齐（见续接记录）。

---

## 2. 里程碑与顺序

```text
M0 环境与回归基线 ──┬─> M1 RBAC 收尾
                    ├─> M2 结果缓存收尾
                    └─> M3 插件 P2c 收尾
                              ├─> M4 插件 P3 网关输入/严格输出   (需决策 D1、D3)
                              └─> M5 插件 P4 独立任务/宿主能力    (需决策 D2)
                                        └─> M6 插件 P5 管理界面
                                                └─> M7 插件 P6 示例、文档、全链路与 Docker 验收
```

最新进度（2026-10-11）：M0、T1.1、T2.1/T2.2、T3.1–T3.8代码任务已完成，M3代码8/8；下一T2.3。当前7个实例权限码和10条操作策略已同步，未来新增继续核对。RBAC/插件浏览器、缓存完整网关/负载/指标、完整HTTP信号退出/Docker与M4–M7仍待独立验收。顶部T2.2快照为续接入口，各历史进度文档保留原记录。

M1、M2、M3 互不依赖，可按顺序串行完成（推荐顺序 M1 → M2 → M3，因为前两者只剩验收收尾）。M4/M5 必须在 M3 的接口冻结后开始，开始前先写各自的详细计划文件并请用户确认。

---

## 3. M0：环境与回归基线

目标：让“全量通过”成为可信声明。

- [x] T0.1 准备 PostgreSQL 测试环境：Docker 可用，或请用户提供 `SUB2API_TEST_POSTGRES_DSN`（需 CREATEDB 权限的专用测试库，**不得使用生产库**）。拿不到时停下询问，不要伪造通过。
- [x] T0.2 修复 `/auth/me` API 契约快照：`backend/internal/server/api_contract_test.go` 补入 RBAC 新增的三个字段（以 `handler/auth_handler.go` 实际输出为准）。
- [x] T0.3 处理 `go test -tags=unit ./... -timeout=60s` 的既有失败：
  - Codex HTTP/2 本地连接测试被终止 — 定位是环境问题还是测试缺陷，修测试同步，不改生产行为；
  - 3 个 `pg_dump` 测试缺 `sh` — 通过 PATH 解决，记录到 README/进度文档，不改备份功能；
  - service 包 60 秒超时 — 使用 `-timeout=600s -p=1` 作为标准全量命令，确认不是死锁。
- [x] T0.4 运行第 0 节全部后端、前端命令，生成基线结果写入 `docs/superpowers/plans/2026-10-09-baseline.md`。

出口：后端全量、前端全量全部 exit 0；PG 集成环境可用。

---

## 4. M1：RBAC 收尾

设计：`specs/2026-09-28-rbac-user-system-design.md`；最新状态：`plans/2026-09-29-rbac-progress-handoff.md` 顶部。**首版 RBAC 功能已无已知未实现项，本里程碑以验收和修补为主。**

- [x] T1.1 PG 集成复跑：`go test -tags integration ./internal/repository -run RBAC -count=3`，零跳过。
- [ ] T1.2 浏览器端到端验收（Playwright 或可用浏览器；若没有浏览器环境，如实记录并请用户安排人工走查）。本轮浏览器工具清单为空，初始化 iab 返回 `Browser is not available: iab`；未启动真实浏览器验收，继续保持未勾选。场景：
  1. 创建/编辑/删除角色，系统角色与超管角色锁定不可改；
  2. 分配权限（分组/搜索选择），普通操作者不能分配超出自己的权限；
  3. 用户多角色分配与清空；
  4. 只读角色：菜单、按钮、表单正确隐藏/禁用，API 直接调用返回 403；
  5. 直达无权限页面落到 `AccessDenied`；
  6. 撤权后页面切换/聚焦即刷新菜单与权限；
  7. 敏感操作触发 step-up；
  8. 用户属性部分保存失败时提示“基础信息已保存”；
  9. 代理列表不显示原始密码，导出走 `proxies.export` + step-up。
- [ ] T1.3 对验收发现的每个问题：先写回归测试（Vitest/Go）再修复。
- [ ] T1.4 新增权限码的同步：M3–M6 新增的插件路由权限码在此处统一核对进入权限目录与前端 `frontend/src/permissions/`。
- 不在本里程碑：跨页全局角色筛选、持久化审计 outbox（见决策 D4、D5）。

出口：T1.1–T1.3 有证据；后端与前端全量通过。

---

## 5. M2：结果缓存收尾

设计：`specs/2026-10-08-response-result-cache-design.md`；进度：`plans/2026-10-08-response-result-cache-progress.md`；客户端协议：`docs/RESPONSE_RESULT_CACHE.md`。

- [x] T2.1 PG 集成：执行已写好的用例（分组创建/详情/lite/列表/复制投影；并发 CAS 只有一个成功；240/241 迁移重复执行幂等；访问记录持久化、去重、成本字段、`usage_logs` 行数不变、留存清理边界）。
- [x] T2.2 真实版本来源（2026-10-11）：权威SQL内容快照聚合实际分组/账号/渠道/定价及完整settings，不再仅依赖updated_at/MAX；数据库内SHA256，模型映射/审核配置变更后旧键MISS、在途不回填、真实PG同进程三轮与race/全量通过。保守全settings失效和规模成本见 `2026-10-11-result-cache-version-progress.md`；不替代T2.3完整路由栈。
- [ ] T2.3 完整网关链路验收（使用真实 handler 路由栈 + 模拟上游计数服务器）：
  - 过期/禁用 API Key、禁止模型、余额/订阅不足、输入审核拒绝 — 都不能被 HIT 放行；
  - HIT 不调用 `RecordUsage`、不累加来源账号配额、不写 `cache_read_tokens`，生成新 request_id；
  - MISS/write_only 正常计费，`usage_logs` 与 `result_cache_access_logs` 可按 request_id 关联；
  - 跨模型 fallback / 账号级映射 / 上游实际模型不一致时不回填；
  - `gateway_cache` 字段和协议头不发往上游，`prompt_cache_key`/`cache_control` 保留。
- [ ] T2.4 故障与负载：Redis 不可用、慢、返回损坏记录 → 正常转发并 BYPASS；同键并发 N 个请求上游调用次数 ≈ 1；生产者取消/租约过期后旧生产者不能覆盖。给出简单压测数据（并发度、P50/P99、上游调用数）。
- [ ] T2.5 指标标签低基数核对（不含用户 ID、键摘要、正文）。

出口：设计第 10 节 16 项验收逐条有测试或证据映射，写入进度文档。

---

## 6. M3：通用插件 P2c 收尾

设计：`specs/2026-09-30-general-plugin-platform-design.md`、`specs/2026-10-08-general-plugin-p2c-admin-design.md`；进度：`plans/2026-10-08-general-plugin-p2c-admin-progress.md`。

现有代码：`service/plugin_instance.go`、`plugin_instance_service.go`、`repository/plugin_instance_repo.go`、`handler/admin/plugin_instance_handler.go`、`routes/admin.go:783`、`frontend/src/views/admin/PluginInstancesView.vue`、`frontend/src/api/admin/pluginInstances.ts`。

- [x] T3.1 新增 `GET /admin/plugin-instances/:id/status`：数据库 enabled/current revision、本节点租约引用数、retired/closed、固定分类最近错误摘要；只读，不启动/解密/退休实例。精确 `plugin_instances.read`，前端类型/API/操作策略同步；生命周期、脱敏、权限、真实 PG+HTTP 验收见 `2026-10-09-plugin-instance-status-progress.md`。状态面板仍属 T3.6。
- [x] T3.2 删除实例：仅允许 disabled 且本节点无活跃租约的实例；删除绑定与配置版本（或软删除，选择一种并在计划里写明理由）；新增权限码 `plugin_instances.delete` + step-up；删除后解除安装包冻结。
- [x] T3.3 审计：六类写操作调用 `middleware.SetAuditAction` / `SetAuditExtra`，记录已知正整数 instance_id/plugin_id、expected/new generation、revision、固定结果码及冲突标记；失败不推算新generation，删除/失败未返回的package/revision省略，不新增观察IO。整块省略body/query/raw params，固定路由动作与最终类型/枚举投影；真实PG审计/候选发布回退三轮及脱敏、拒绝、读回归、回滚已通过，见 `2026-10-09-plugin-instance-audit-progress.md`。
- [x] T3.4 handler 层 HTTP 集成测试（真实 Gin 路由 + 中间件）：无权限 403、缺 step-up、非法 JSON 400、未知实例/revision 404、generation 冲突 409、运行时不可用 422、服务关闭 503；响应体不泄露敏感字段。
- [x] T3.5 PG 集成：创建→保存配置→发布→回退→停用→绑定替换，加双并发 CAS，`-count=3` 零跳过。
- [x] T3.6 前端（2026-10-11 代码、组件/API 和全量前端验收完成；真实浏览器端到端未执行）：
  - 结构化绑定编辑器（分组选择、stage、entry、protocol、sort_order、enabled、can_replace，选项来自 Manifest 声明），替换现有 JSON textarea；
  - 新配置 revision 编辑：若 Manifest 提供 config schema 则生成表单，否则 JSON 编辑器 + 客户端校验；明确“配置只写不回显”；
  - 状态面板（T3.1）、删除（T3.2）、409 冲突时提示刷新；
  - 所有文案进 i18n（当前页面是硬编码英文），中英文齐全；
  - Vitest 覆盖权限隐藏、冲突、失败提示。
- [x] T3.7 跨节点协调：后台循环（间隔可配置）读取实例 generation，发现本节点句柄落后于数据库（被停用/发布新 revision/删除）时排空并替换；停用传播时间写入文档并测试上限。
- [x] T3.8 Wire 与生命周期（2026-10-11）：与 handler 共享实例服务；整体 cleanup Once；实例 Close → manager Stop，最后 Redis → Ent；current/retired 真进程退出、并发/重复关闭与错误继续回归通过。完整 HTTP 信号退出/Docker 未由 fixture 替代，见 `2026-10-11-plugin-instance-lifecycle-progress.md`。

出口：P2 出口条件满足（总计划第 4 节）；但 `general_runtime_unavailable` 门禁**仍保留**，直到 M4 网关接入完成。

---

## 7. M4：插件 P3 网关输入与严格输出

**代码已完成（2026-10-11）：** 详见本批P3计划/progress；D1暂缓新收费/退款，D3四种文本JSON/SSE。

- [x] T4.1 调研并写入计划：每个平台入口（OpenAI Chat/Responses、Anthropic Messages、Gemini 等，见 `server/routes/gateway.go`）中“鉴权后确定实际分组”“读取请求体”“发送上游”“写客户端”的精确位置；复合分组（`docs/COMPOSITE_GROUPS.md`）换组点。
- [x] T4.2 `service/plugin_gateway_hooks.go`：从分组绑定快照 + `pluginflow.Chain` 执行输入链；`block` 时上游调用次数为 0；换组后重新取绑定。
- [x] T4.3 `handler/plugin_response_buffer.go`：受控大小/总预算/并发/超时的缓冲；严格模式下审核结束前不写、不 flush 任何正文；超限/超时/取消为明确失败，不透传。
- [x] T4.4 协议适配：非流式 JSON 与 SSE 先做首批协议（D3），SSE 按语义事件聚合完整文本再检查；未支持协议绑定严格检查时请求直接失败，不绕过。
- [x] T4.5 与 `securityaudit` 协调器定义唯一执行归属，避免双重审核。
- [x] T4.6 与结果缓存的交互：输出插件绑定或其 revision 变化必须进入缓存键或使缓存失效（设计第 6 节“不能继承旧放行结论”）；HIT 时输出插件是否重新执行要在计划中明确。
- [x] T4.7 用量：阻断时仍记录真实上游用量与插件结论；D1暂缓新收费/退款，现有成功上游usage记账保持；执行结论为脱敏结构化日志，持久化记录/界面留M5/M6。
- [x] T4.8 解除 `general_runtime_unavailable`（`service/plugin_compatibility.go`）仅针对已接入的 hook 入口。
- 验收：使用可观测的模拟上游和带受控延迟的测试插件证明：审核完成前客户端收到字节数为 0；阻断后原文不出现；SSE 跨事件敏感片段被检出；超限、断开、超时、取消各自行为正确；未绑定分组插件调用次数为 0；旧 TransportPlugin 回归通过。

---

## 8. M5：插件 P4 独立任务与宿主能力

**实际执行计划：** `plans/2026-10-11-general-plugin-p4-p6.md`；D2已决定为兼容API渠道。

- [x] T5.1 迁移（243+）：任务定义（实例、入口、cron、目标范围、超时、重试）与执行记录（状态、耗时、结果、错误、业务变更关联、幂等键）。
- [x] T5.2 `plugin_task_service.go`：手动与定时共享执行入口；`plugin_task_scheduler.go`：多节点使用数据库租约 + fencing token，旧执行者提交被拒绝；不承诺恰好一次。
- [x] T5.3 宿主能力 broker（P2b 中暂未暴露）：按实例授权的 KV、受限网络访问、限定查询；检测插件不继承传输插件凭据。
- [x] T5.4 `plugin_pricing_capability.go`：显式目标渠道和价格字段；新价格版本化保存（新旧值、来源）；失败保留旧价；在途请求用快照价格。复用/核对 `upstream_billing_probe.go`、`channel_service.go`。
- [x] T5.5 管理 API `plugin_task_handler.go`（列表/创建/修改/立即执行/历史），接入 RBAC、step-up、审计。
- 验收：无网关流量可触发；手动与定时结果一致；多节点竞争、重试、超时、停用、价格自动生效与失败保留旧价均有测试。

---

## 9. M6：插件 P5 管理界面

- [x] 插件库（现有 `PluginsView.vue`）、实例、分组绑定、任务、执行记录分区组织；新增 `components/admin/plugin/PluginInstancesPanel.vue`、`PluginGroupBindingsPanel.vue`、`PluginTasksPanel.vue`、`PluginRunsPanel.vue`。
- [x] 分组页（`GroupsView.vue`）展示实际生效的插件链；插件页展示绑定的分组与阶段。
- [x] 执行记录默认只显示脱敏信息；查看敏感记录需要单独权限。
- [x] 组件/API 测试覆盖只读用户、撤权刷新、配置失败、409 冲突、日志脱敏；之后浏览器验收。

---

## 10. M7：插件 P6 示例、文档与发布验收

- [x] 三个可打包安装的示例插件：输入凭据检测、输出业务规则检查、独立价格同步；规则和阈值在插件内。
- [x] 更新 `docs/PLUGIN_DEVELOPMENT.md` 和 `backend/pkg/pluginapi/docs/`：清单 schema2、配置、hook/task 入口、宿主能力、错误与超时、脱敏、打包、版本兼容。
- [x] 开发环境全链路：上传 → 实例配置 → 绑定 → 请求/任务 → 审计记录。
- [ ] Docker 构建、启动、数据持久化、重启后实例恢复（参考 `docs/DOCKER_RBAC_DEPLOYMENT_CN.md`）。
- [x] 总计划第 9 节 16 项验收矩阵逐条映射证据。

---

## 11. 待用户决策（遇到时停下询问）

| 编号 | 问题 | 影响 |
| --- | --- | --- |
| D1 | 输出被插件阻断时，上游已产生的费用是否向用户收取，或退款 | M4 T4.7 |
| D2 | 外部“上游适配服务”（如 prism2api 类）是否纳入原生插件管理，还是继续作为兼容 API 渠道 | M5 范围 |
| D3 | 严格输出检查首批支持的协议/接口（建议：OpenAI Chat 非流式 + SSE） | M4 T4.4 |
| D4 | RBAC 审计是否需要持久化 outbox（当前为异步 HTTP 审计） | M1 范围 |
| D5 | 用户角色筛选是否需要服务端跨页筛选 | M1 范围 |
| D6 | 插件实例 create/config/bindings 当前代码全部要求 step-up，设计只要求 publish/disable；保留代码的更严格行为还是按设计放宽 | M3（默认保留更严格行为） |
| D7 | 是否、何时提交/推送/部署；混合工作区如何拆分提交 | 所有里程碑结束后 |

---

## 12. 每个任务的交付记录格式

每完成一个里程碑，在 `docs/superpowers/plans/` 新建或更新 `YYYY-MM-DD-<subsystem>-progress.md`，包含：

1. 本轮完成的任务编号和修改/新增文件列表；
2. 精确执行的命令、退出码、关键输出（失败也保留）；
3. 未执行项及原因（例如没有 PG/浏览器）；
4. 新发现的问题和待决策项；
5. 下一步从哪个任务编号继续。

不要在进度文档中把“编译通过”“组件测试通过”写成“功能完成”。


## 续接记录：T3.2 插件实例删除（2026-10-09）

已实现事务硬删除、generation CAS、disabled、全部本节点新旧句柄租约检查、最后实例引用解除安装冻结，以及精确 plugin_instances.delete + 原有 step-up。无新迁移，242 未修改。跨节点排空仍归 T3.7，不宣称集群零租约。

详细设计、PG + DELETE HTTP、RED/GREEN、审查、失败修正与回滚见 `2026-10-09-plugin-instance-delete-progress.md`。本轮证据 `D:\tmp\sub2api-remaining-work-20261009-m3-delete`。主工作区保留混合未提交版，未提交/推送/部署。

下一切片 T3.3 全写操作审计；T3.4 HTTP 错误/脱敏矩阵、T3.6 前端删除/配置、T3.7 跨节点、T3.8 关闭序列待执行。浏览器、缓存版本来源与 M4/M5 产品决策门禁不变。


## 续接记录：T3.3 插件实例审计（2026-10-09）

六类写操作结构化审计、整块正文/query/raw params省略、固定动作与字段投影完成。审查修正取消额外写前Snapshot，未知元数据省略；写未知错误固定503、读取保持原错误矩阵，缺config400。真实生产路由/审计服务/PG/候选进程三轮，每轮14条持久化审计；认证/权限/step-up仍fixture，不等价浏览器/真实OTP验收。后端标准全量、unit全量、race三轮、build/vet、前端4文件40项契约通过，失败/既有跳过保留。四交付与副本回滚见 `2026-10-09-plugin-instance-audit-progress.md`；证据 `D:\tmp\sub2api-remaining-work-20261009-m3-audit`。

最新完成 T3.1/T3.2/T3.3/T3.4/T3.5，M3 为5/8；T3.4完整HTTP矩阵、最终全量/race/实库、独立审查、四交付及副本回滚已完成。下一T3.6；T3.7/T3.8仍待执行。当前7实例权限码、10实例路由；M1浏览器、M2网关/负载与M4/M5决策/门禁不变，主工作区未提交、推送或部署。


## 续接记录：T3.4 插件实例 HTTP 错误矩阵（2026-10-09，已完成）

runtime422/closed503优先、正发布参数提前400、未知读取保留HTTP状态但固定错误投影已落地。Publish/Acquire取消不污染LastError，候选自身timeout仍记录真实失败。生产路由/RBAC/step-up、10路由5xx日志脱敏和真PG Configure拒绝/旧runtime保持验证通过；生产缺grant403，旧428仅fixture。最终标准全量、定向unit、race三轮、PG三轮、build/vet、前端4文件40项契约通过，实际skip及失败记录保留。独立审查I1/M1闭环；四交付重开及副本回滚原SHA/旧行为通过，主工作区保持修改版。详情 `2026-10-09-plugin-instance-http-progress.md`，证据 `D:\tmp\sub2api-remaining-work-20261009-m3-http`。下一T3.6，M3完成5/8；未提交、推送或部署。
