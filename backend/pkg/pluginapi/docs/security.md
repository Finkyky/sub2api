# 插件安全边界

## 能提供的隔离

- 私有实现以独立二进制交付，宿主公开源码不包含其业务逻辑。
- 进程协议避免 Go 动态链接和共享内存 ABI。
- 包签名和文件哈希防止未授权替换。
- UI 使用短时 URL、独立 Bridge Token 和 sandbox iframe。
- 插件故障时 OAuth 插件路径失败关闭，不静默切回另一种网络行为。

## 不能提供的保证

- 闭源二进制仍可能被逆向分析。
- 子进程不是操作系统沙箱。
- 插件拥有 Sub2API 服务用户可访问的文件、环境变量和网络权限。
- 包签名证明发布者身份，不证明实现无漏洞或符合 Provider 条款。

## 部署要求

- 官方 OpenAI Transport 使用宿主内置公钥；只向 `plugins.trusted_publishers` 添加经过审核的第三方公钥。
- 使用专用低权限系统用户运行 Sub2API。
- 限制该用户的文件权限、出站网络和环境变量。
- 不向插件环境注入无关密钥。
- 对插件升级保留旧包和回滚流程。
- 记录安装、启用、停用、配置和删除操作，但不记录配置明文。

## 敏感数据

插件处理真实 OAuth Authorization 请求头，必须避免将请求头、请求体、代理凭据和上游敏感响应写入日志或诊断消息。UI 配置中不应出现 OAuth Token。

## schema2 hook/task 最小权限与脱敏

general.v1 示例没有 OAuth outbound、账号目录或身份解析 capability；宿主不通过 hook/task 附带 Authorization/Token。general runtime 使用 SkipHostEnv=true，但子进程不是 OS 沙箱，文件和网络访问仍由运行用户与部署策略控制。

manifest host_capabilities 与配置 target/field/URL 不是授权。task scope 限定 KV keys、network URLs、channel IDs 和 pricing fields；broker 在 Prepare/Commit fenced claim 中检查。pricing-sync 仅消费 host.network body 或显式 task.input.prices，规则在插件；无数据/越限/未授权返回 failed，不编造价格。

输出、配置、业务文本、网络 body 与 KV 按敏感数据处理。示例没有业务日志，错误固定，findings 只有短 rule ID/version/category，不回显片段。普通 run/history 仅状态/错误码/耗时，敏感 output 单独加密授权读取。纯提案取消不提交；自定义外部副作用需独立设计幂等和补偿，ctx 取消不是事务回滚。
