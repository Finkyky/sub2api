# 通用插件 P2c 持久化与发布基础 Implementation Plan

> 沿用已确认的 2026-09-30 插件平台设计，使用 executing-plans / test-driven-development 顺序实施。本轮在包含全部既有 backend 改动的副本开发，保留原始哈希，不提交或部署。

**Goal:** 实现实例、不可变加密配置版本、分组绑定仓储与固定版本运行句柄的租用/发布基础。

**Architecture:** PostgreSQL 是实例状态权威。管理写入锁住实例并以 generation 做 CAS；配置版本追加而非覆盖。发布先启动候选，数据库提交成功后才替换本节点句柄；旧租用保持原版本并在引用归零后关闭。存在实例引用时冻结其安装包身份。

**Tech Stack:** database/sql、PostgreSQL、现有 SecretEncryptor/AES-GCM、GeneralPlugin v1、Go 测试和 sqlmock。

## 已确定的设计

- 通用实例和绑定独立于旧 transport bindings，旧协议保持不变。
- `current_revision` 指向已发布配置，`latest_revision` 记录草稿序号；保存、发布、停用和绑定写入均推进 `generation`。
- 回退重新发布已保存的旧配置版本，generation 继续递增，不修改旧配置或旧句柄。
- 分组绑定明确保存 group_id、stage、entry、protocol、sort_order、enabled、can_replace。
- 同入口多协议绑定作为替代选项；任一需要执行的入口缺少协议覆盖时明确失败，其他实例的匹配不抵消该失败。
- 分组读取使用同一 repeatable-read 快照，查询不隐藏停用实例。每次新租用读取数据库；自动后台协调为接续工作。
- 同 plugin_key 安装先获取事务级 advisory lock，再锁安装行；实例创建使用同一安装行锁，有引用时升级和删除被拒绝。
- 失效或崩溃进程在下一次租用时重建，不自动重试已经失败的 Hook/Task 调用。

## Task 1：模型、配置版本及绑定仓储

文件：
- `backend/internal/service/plugin_instance.go`
- `backend/internal/service/plugin_instance_test.go`
- `backend/internal/repository/plugin_instance_repo.go`
- `backend/internal/repository/plugin_instance_repo_test.go`
- `backend/internal/repository/plugin_instance_integration_test.go`
- `backend/internal/repository/plugin_repo.go`
- `backend/migrations/253_general_plugin_instances.sql`

- [x] 先写新类型、CAS 冲突、事务写入失败、绑定校验和快照复制测试，执行红测试确认新类型尚不存在。
- [x] 创建实例/版本/绑定三张表，增加 deferred 当前版本外键，保留旧传输表。
- [x] 实现 Create、Snapshot、SaveRevision、Publish、ReplaceBindings、GroupSnapshots；提交失败不返回新状态。
- [x] 使用现有加密器保存配置，HTTP JSON 投影不返回配置密文或安装信息。
- [x] 补实例引用安装冻结、逐入口协议覆盖的红/绿回归。
- [x] 执行真实 PostgreSQL 验证创建/草稿/回退、双并发 CAS、绑定/停用、迁移重复执行、安装冻结和 AES-GCM 加密。

## Task 2：固定版本发布与租用生命周期

文件：
- `backend/internal/service/plugin_instance_service.go`
- `backend/internal/service/plugin_instance_service_test.go`

- [x] 写发布、旧引用排空、候选启动失败、CAS 失败、回退、停用、节点重建和重复释放测试。
- [x] 内部服务先复验安装包、解密配置，再创建固定实例/配置版本句柄。
- [x] 发布成功才替换 current；发布失败关闭候选；停用拒绝新租用，已有租用可以排空。
- [x] 租用快照隔离调用方修改，Check/Task 与 Release 串行，节点 Close 终止所有句柄。
- [x] 真实子进程验收 alpha → beta → alpha，旧租用保持原配置；错误调用不重试，下一次租用恢复新进程。

## Task 3：回归、回滚与交接

- [x] 执行契约/执行核心、既有插件及仓储回归。
- [x] 执行 race、全仓 vet、服务端构建及差异检查。
- [x] PostgreSQL 6 项集成连续执行 3 轮，共 18 次通过、零跳过。
- [x] repository 全量首次因缺少 sh 失败；测试进程 PATH 加入现有 Git usr/bin 后全量通过，未修改备份功能。
- [x] 在独立副本执行源码回滚，恢复 3 个原文件并移除 9 个新增文件；原始行为探针恢复，旧插件回归通过。
- [x] 将本轮 12 个文件同步回工作区，核对与测试副本哈希一致，并重跑工作区聚焦测试。
- [x] 确认临时 PostgreSQL 基础库无业务表、无测试数据库遗留，并停止本轮临时集群。

证据目录：`D:\tmp\sub2api-plugin-p2c-20261008`。
四件交付：`MODIFIED_FILE.zip`、`DIFF_FILE.patch`、`VERIFICATION.txt`、`ROLLBACK.sh`。
源码回滚仅恢复本轮文件，不执行数据库降级，不撤销此前 RBAC、缓存或插件 P1/P2a/P2b 改动。

## 接续工作

本轮交付内部 P2c 基础，不把整个 P2c 或插件平台标为完成。下一步补实例管理操作、管理员 API、RBAC/step-up/审计、Wire/启动关闭装配及跨节点后台协调；随后接入真实网关 Hook、严格 HTTP/SSE 缓冲、任务调度和宿主业务能力。现有管理入口继续保留 `general_runtime_unavailable`。
