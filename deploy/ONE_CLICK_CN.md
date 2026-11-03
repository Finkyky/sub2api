# 当前源码一键部署

## 1. Linux 服务器启动

前提：Docker Engine、Docker Compose v2（支持 `up --wait`）、Bash 4+ 和 OpenSSL。源码构建需要网络能拉取基础镜像与依赖；脚本不安装系统组件。HTTPS 需要域名正确解析、公网 80/443 可达。

将当前完整源码（包含未提交的新文件）传到服务器，在仓库根目录执行下面三种选择之一：

```bash
# 源码生产构建 + PostgreSQL + Redis，应用默认绑定 loopback。
bash deploy/start.sh

# 首次初始化时明确启用固定 Codex profile。
bash deploy/start.sh --codex-aligned

# 首次初始化时同时配置域名、固定 profile 和 Caddy HTTPS。
bash deploy/start.sh --codex-aligned --https --domain api.example.com
```

这三个命令是备选，不是连续操作。首次生成五个独立的 PostgreSQL、Redis、管理员、JWT、TOTP 随机值，保存到权限 600 的 `deploy/.env`，不会打印秘密；连接池高并发示例替换为后端默认预算。管理员邮箱初始为 `admin@sub2api.local`。

如需要先填写邮箱、域名或网络参数：

```bash
bash deploy/start.sh --init-only --codex-aligned --domain api.example.com
nano deploy/.env
bash deploy/start.sh --codex-aligned --https
```

再次运行原样保留已有 `.env`，不轮换密钥、不重写域名、不悄悄启用 profile。已有部署先在 `.env` 明确设置需要的值；`--codex-aligned` 此时仅检查配置。未配置固定 profile 的第三方 API-key 端点部署可直接使用默认启动。

### 已有部署的数据布局

默认 local（本地目录）；已有命名卷部署使用 `bash deploy/start.sh --storage named`，其他开关可同时追加。脚本检查现有容器、预期挂载目的地、类型、目录/卷名，以及有/无 Compose 标签的预期数据卷，不把命名卷或其他目录静默切换成新空数据库。容器已被 down 移除后仍检查本地持久目录；非标准卷名继续使用原来的 Compose 映射。独立外部数据库部署继续使用 standalone Compose，不转换到这个 all-in-one 入口。

存在持久化数据但 `.env` 丢失时，恢复原 `.env` 后再启动，不生成替代数据库/JWT/TOTP 密钥。`--init-only` 仅供新部署，不联系 Docker；普通首次启动还会检查已有容器/卷。初始化前清除宿主机导出的五个密码/密钥变量，确保写入 `.env` 的值就是首次部署的值。

## 2. 预构建镜像和生命周期

```bash
# .env 的 SUB2API_SOURCE_IMAGE 指向本机已经构建或 docker load 的镜像。
bash deploy/start.sh --no-build

# 指定自建发布镜像，不构建源码；以后启动继续使用该参数。
bash deploy/start.sh --image registry.example.com/sub2api:release --https
```

默认构建根 `Dockerfile`，复用生产 Compose，不使用 debug 开发栈，也不拉取官方应用镜像替代本地代码。`--image` 是本次启动选择，已有 `.env` 不改写。配置校验、构建、健康等待失败都会终止；健康检查不是公网证书或真实业务验收。

HTTPS 源码模式的管理命令保持同样的文件组合：

```bash
cd deploy
docker compose -f docker-compose.local.yml -f docker-compose.source.yml -f docker-compose.https.yml ps
docker compose -f docker-compose.local.yml -f docker-compose.source.yml -f docker-compose.https.yml logs --tail=100 sub2api
```

纯 HTTP 去掉 HTTPS 文件；`--image` 模式去掉 source 文件并保留相同镜像选择。旧 `docker-deploy.sh` 用于下载上游部署文件，不用于发布当前定制源码。

## 3. 配置位置与优先级

- `.env.example`：标量服务参数、默认值和注释。
- 四个 `docker-compose*.yml`：容器环境映射。仅写宿主机 `.env` 而没有映射不会生效。
- `config.example.yaml`：完整结构化示例，不是正在运行的配置。
- 实际配置通常由初始化写入持久化 `/app/data/config.yaml`，本地目录版对应 `deploy/data/config.yaml`。修改时保留数据库、JWT、TOTP 等字段。首次 AUTO_SETUP 会写配置，首次初始化不要挂只读 config.yaml。
- 服务环境变量覆盖同名 YAML；宿主机 shell 变量可覆盖 `.env` 插值。变更后重新创建容器，不能只 restart。
- profile、域名、密钥采用字面量；不要嵌套 Shell 命令或 dotenv 变量展开。初始化器不执行 `.env`。
- 公钥映射等复杂值放实际 YAML，按现有 Compose 的可选挂载说明配置，不伪造逗号分隔的结构。

## 4. Codex 对齐与兼容参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `GATEWAY_OPENAI_CODEX_CLIENT_PROFILE` | 空 | `codex-linux-rustls-0.153.4` 启用固定基线，不自动跟踪最新 |
| `GATEWAY_DISABLE_CODEX_IDENTITY_ENFORCEMENT` | false | 固定 profile 必须保持身份统一 |
| `GATEWAY_DISABLE_CODEX_ORIGINATOR_NORMALIZATION` | false | 固定 profile 必须保持归一化 |
| `GATEWAY_FORCE_CODEX_CLI` | false | CLI 识别兜底，不是指纹开关 |
| `GATEWAY_CONNECTION_POOL_ISOLATION` | account_proxy | 按账号与代理隔离 |
| `GATEWAY_OPENAI_WS_ENABLED` | true | WebSocket 总开关 |
| `GATEWAY_OPENAI_WS_MODE_ROUTER_V2_ENABLED` | false | 新版账号 WS 模式路由 |
| `GATEWAY_OPENAI_WS_INGRESS_MODE_DEFAULT` | ctx_pool | 新版路由的默认模式 |
| `GATEWAY_OPENAI_WS_FORCE_HTTP` | false | 原模式 HTTP 回退；固定 profile 的 H2 行为另见对齐文档 |
| `GATEWAY_CODEX_IMAGE_GENERATION_BRIDGE_ENABLED` | false | 文本请求自动图片工具桥接，独立功能 |
| `GATEWAY_OPENAI_CODEX_TICKET_ENABLED` | false | 独立打票开关，不随 profile 自动开启 |
| `GATEWAY_OPENAI_CODEX_TICKET_HARVEST_PROXY_URL` | 空 | 打票专用代理，不替代账号出口 |
| `GATEWAY_OPENAI_CODEX_TICKET_FAIL_CLOSED` | true | 打票失败策略；其他模型/预算配置见 YAML |

profile 要求包含此实现的自建镜像。主要作用于 `chatgpt.com/backend-api/codex` 目标范围，不统一第三方 API、OAuth token 端点或插件网络客户端。对齐部分身份和传输字段，不是完整复刻；需实际验证代理、HTTP 流式、WS 和业务链路。完整细节见 `docs/CODEX_LINUX_ALIGNMENT.md`。

start.sh 启动后检查运行容器收到的 profile。另检查启动日志 `Codex client alignment enabled`。

关闭或回滚 profile：`.env` 改为空，实际 YAML 中同名值也清空，清除宿主机同名环境变量，然后重建应用；无需清空账号、票据或数据库。

## 5. Claude TLS、缓存、插件

- `GATEWAY_TLS_FINGERPRINT_ENABLED=true` 是通用 TLS 功能，Anthropic OAuth/SetupToken 账号还需后台单独启用，不等于 Codex profile。
- `GATEWAY_RESULT_CACHE_*`：IO 300ms、等待 2000ms、租约 30000ms、生成 600000ms、32 个填充。不是全局启用开关，仍需分组策略和客户端显式声明；Redis 容量单独规划。
- `PLUGINS_DATA_DIR=/app/data/plugins`：持久化的容器内路径；本地目录版对应 `deploy/data/plugins`。
- `PLUGINS_ALLOW_UNSIGNED=false`；第三方发布者公钥放 YAML 的 `trusted_publishers`。
- `PLUGINS_*` 其他参数覆盖启动、协调、钩子、完整输出缓冲预算，模板已列明，不自动创建插件实例。
- 插件包和解释器/二进制依赖单独提供；根镜像不自动打包本地 plugins 目录。使用时扩展自建镜像，不在启动脚本静默安装。

## 6. HTTPS、可信代理、出口网络

HTTPS overlay 中的 Caddy 使用默认 `172.30.50.0/24` 专用子网和 `172.30.50.10` 固定地址；应用只信任该 IP/32，关闭旧式无条件转发头信任。流式即时刷新，支持 WebSocket，不开启全站压缩。

- `SITE_DOMAIN`：域名；overlay 自动设置 `SERVER_FRONTEND_URL`。
- `SUB2API_HTTPS_NETWORK_SUBNET` / `SUB2API_HTTPS_PROXY_IP`：必须匹配，避免与已有网络重叠。
- `BIND_HOST` 必须为 loopback，避免应用绕过 HTTPS 对外暴露；脚本按最后一个 dotenv 定义检查。
- 未设置 `SERVER_TRUSTED_PROXIES` 时保持容器变量缺席，不覆盖已有 YAML；显式空值才清空可信列表。转发信任变量也采用可选透传，HTTPS overlay 则明确设置严格模式和固定代理。
- 已有网络切换静态子网时可能需维护窗口重建旧 Compose 网络；保留数据目录，不使用清卷选项。
- 已有宿主机 Caddy/Nginx 不启用此 overlay，按实际 Docker 对端设置 `SERVER_TRUSTED_PROXIES`，参考 `EDGE_SECURITY.md`。
- CDN 部署先按 `EDGE_SECURITY.md` 配置可信链；当前模板假设客户端直连 Caddy。
- 账号出口代理在后台绑定；`UPDATE_PROXY_URL` 是更新等相关访问代理，不能代替账号出口。

证书持久化在 `caddy_data` / `caddy_config` 命名卷。健康等待不证明 DNS/证书签发成功，另从外部验收域名 HTTPS。

## 7. 后台业务设置清单

这些值存于数据库，不通过编造环境变量启用：

1. 上游账号、认证方式、出口代理。
2. 管理员和普通用户 RBAC、接口及菜单权限。
3. 分组、模型映射、倍率、额度、API Key。
4. 注册策略、SMTP、验证码、密码找回；实测邮件和链接域名。
5. 账号 `codex_fingerprint_mode` 默认 off，设备/会话收敛独立于固定 profile，不自动开启。
6. 分组结果缓存策略、客户端声明、失效和访问记录。
7. 插件上传、发布者、实例、绑定及运行依赖。

## 8. 验收、备份和发布回滚

验收顺序：容器健康 → 公网 HTTPS → 管理员/普通用户 → 普通/流式/WS 请求 → 代理 → 用量记账 → 启用的缓存/插件 → 重启状态。

应用启动自动执行数据库迁移。升级前保留旧镜像和 `.env`、实际 YAML、数据库、应用/Redis 数据、插件和 Caddy 卷。迁移前向执行，回退镜像不一定恢复旧数据库结构；数据库回滚使用升级前备份。不重新生成 JWT/TOTP/数据库密码。

一键启动提供进程和服务配置自动化，不自动导入账号或完成业务授权。
