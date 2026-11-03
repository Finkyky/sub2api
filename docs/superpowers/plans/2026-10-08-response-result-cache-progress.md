# 响应结果缓存开发进度（2026-10-08）

分支：`feature/rbac-user-system`。保留已有未提交的 RBAC、插件及其他工作，不提交或推送混合工作区。本轮继续响应结果缓存，不运行生产迁移，不开启生产分组。

## 本轮缓存增量

- `gateway.result_cache` 运行参数：I/O、等待、租约、缓存生成预算及每进程并发填充数；支持环境变量、范围验证与启动注入。
- 生成预算到期、客户端取消、续租失败、服务停机时主动释放分布式租约及本地槽位。超时/失效生产者不发布结果；停机序列等待续租退出后关闭缓存连接池。
- 慢 Redis socket 的 25ms 预算在旧实现中阻塞约 1 秒，已用真实 TCP 慢端点复现。缓存现使用同一 Redis 实例的独立连接池，启用上下文超时且不自动重试，不修改共享客户端设置或全局 Redis eviction 策略。
- Chat 适配器增加模式、隔离、共享审批、关闭/在途失效、TTL、截断结果、请求身份及访问记录测试。使用 go-redis + miniredis 和模拟上游计数，不冒充生产端到端测试。
- 分组缓存表单提取默认/回显/校验/写入投影；组件只回传四个管理员字段，剥离 `revision` 等服务端元数据。新增/编辑均使用显式投影，缺省老数据保持关闭。
- 新增客户端协议说明 `docs/RESPONSE_RESULT_CACHE.md`，精确放行功能文档。

## 已执行证据

目录：`D:\tmp\sub2api-result-cache-controls-20261008`。

- 配置包全量：`CONFIG_ALL.txt`，exit 0。
- 缓存后端聚焦及既有 Chat/审核回归：`BACKEND_FOCUSED.txt`，exit 0。
- 缓存 service/repository/handler race：`RACE.txt`，exit 0。
- 后端构建：`BUILD.txt`，exit 0；全仓 `go vet ./...`：`VET.txt`，exit 0。
- Chat 交付矩阵：`CHAT_ACCEPTANCE.txt`，exit 0。四种模式序列为 MISS/HIT/UPDATED/HIT/BYPASS，上游计数 3；HIT 的 upstream_called=false。
- 前端缓存与权限回归：`FRONTEND_REGRESSION.txt`，9 个文件、63 项通过；`FRONTEND_TYPECHECK.txt` 和 `FRONTEND_I18N.txt` 均 exit 0。
- `pnpm build`: `FRONTEND_BUILD.txt`, exit 0.
- 本轮代码基线、修改版与副本回滚结果见 `VERIFICATION.txt`；原始哈希和原始文件保留，工作区保留修改版。

## 完整回归的实际状态

`go test -tags=unit ./... -timeout=60s` 已运行，exit 1，不是全量通过：

1. Codex HTTP/2 本地连接测试连接被终止。
2. 三个 pg_dump 测试缺少 `sh`。
3. `/auth/me` API contract 快照尚未包含工作区已有 RBAC 三个字段。
4. service 包在 60 秒测试预算结束时超时；这不是缓存聚焦失败的证据。

前三类问题在保存的本轮改动前源码副本中重新运行，同样失败，记录于 `BASELINE_FULL_FAILURES.txt`。本轮不修改这些非缓存功能；后续需要独立处理并重新完成全量验证。

## 尚待缓存部署验收

- PostgreSQL 两条新迁移的真实执行/幂等性、配置持久化与 revision CAS 并发集成测试。
- 完整生产路由依赖下的失效凭证、模型禁用、审核拒绝及真实上游计数验收。
- 正常 usage_logs 与独立缓存访问记录关联、HIT 零重复扣费与来源账号配额不累加的数据库级验证。
- 审核/路由配置变化、模型 fallback、真实 Redis 故障和负载/容量测试。
- 延长适当测试预算并解决全量既有失败，之后再声明整个缓存功能完成。

本轮交付的是缓存运行边界、交付回归及分组表单收尾。自动适配器测试通过不替代上述部署验收。

## Verified rollback checkpoint

- `BASELINE`: `CANCEL lease_held=true fill_slots=1`, probe exit 1.
- `MODIFIED`: `CANCEL lease_held=false fill_slots=0`, probe exit 0.
- Independent copy: `ROLLBACK HASHES_RESTORED=19`, script exit 0; restored cache service/repository tests exit 0.
- Restored cancellation probe returns the original `CANCEL lease_held=true fill_slots=1`, expected probe exit 1. The working tree retains the modified implementation.
- Four artifacts: `MODIFIED_FILE.zip`, `DIFF_FILE.patch`, `VERIFICATION.txt`, `ROLLBACK.sh` in the evidence directory above.

## 数据库写入边界增量（2026-10-08）

证据目录：`D:\tmp\sub2api-result-cache-db-20261008`。

### 已修正

- 分组创建与更新先检查空输入，不再在检查前访问缓存配置。
- 规范化配置和待写入版本仅传入 Ent builder；数据库执行、CAS 比较或事务提交失败时保留调用方原始配置。只有 Save 成功才发布新的 `response_result_cache_config.revision`。
- 新建分组成功后由服务端设 revision=1；创建失败不再提前改写输入的 TTL 或 revision。
- 240 迁移的对象类型约束增加按约束名及所属表检查的条件块，避免重复增加同名约束。结构回归通过；SQL 的真实重复执行仍待数据库集成验证。

### 新增验证

- 单元：nil 输入、数据库执行失败、同对象重试、CAS 冲突、创建失败、服务端初始版本、成功保存及提交失败。
- 访问记录单元：HIT 新增成本为零、MISS 成本保持 NULL（真实成本属于正常 usage ledger）、重复 request_id 无操作、元数据不含正文/账号归因、10000 行批次清理及写入错误。
- PostgreSQL 用例：分组创建/详情/lite/列表/复制投影；两个真实并发更新只允许一个成功且旧快照重试仍冲突；关闭后当前策略版本变化；240/241 迁移重复执行；访问记录持久化、去重、成本字段、usage_logs 行数不变及留存清理边界。

### 实际执行结果

- 缓存后端聚焦、race、全仓 vet、后端构建：均 exit 0。
- `go test -c -tags=integration` 和 integration tag 的 repository vet：均 exit 0。
- 实际 PostgreSQL 集成命令：exit 1，harness 输出 `integration tests require Docker or SUB2API_TEST_POSTGRES_DSN (PostgreSQL URL with CREATEDB); no tests ran`。本机未配置专用测试 DSN；下载域名 DNS 解析也失败。本轮没有运行真实数据库迁移，不宣称数据库集成通过。
- 原始哈希、失败基线、修改版、独立副本回滚与字面输出统一保存于本轮 `VERIFICATION.txt`；工作区保留修改版。

后续执行顺序：提供显式测试 DSN → 运行上述 PostgreSQL 用例 → 完整网关鉴权/模型/审核与真实计费关联验收。不要把编译通过当作数据库用例已经执行。
### Database increment rollback evidence

- BASELINE: `FAILED_WRITE caller_revision=5`, probe exit 1.
- MODIFIED: `FAILED_WRITE caller_revision=4`, probe exit 0.
- Independent copy: `ROLLBACK HASHES_RESTORED=10`, script exit 0; restored cache service/repository tests exit 0.
- The restored failure probe returns the original caller revision 5, expected exit 1. The live working tree keeps the modified revision-preserving implementation.
