# Linux 已有环境更新部署指南

本文用于 Linux 服务器已经部署 Sub2API、已有数据库和配置时，发布新的源码修改。更新日期：2026-10-12。

**更新不是重新初始化：沿用原来的数据库、管理员、密钥、数据挂载和反向代理配置。**

最短路径：如果原来使用 `bash deploy/start.sh` 启动，先将修改后的完整源码同步到服务器，再用原来的存储与 HTTPS 参数运行该脚本。它会重新构建当前源码，并更新运行容器。

## 1. 先确认原来的部署方式

- **Docker + 仓库内置部署脚本**：按第 3 节更新。
- **二进制 + systemd**：按第 4 节更新。
- **外部数据库或自定义 Compose**：保留原部署文件、项目名称、环境文件和挂载，参考第 3.5 节。

以下命令中的 `/path/to/sub2api` 是源码目录占位符，请替换为实际路径。Docker 备份示例采用仓库默认容器名称 `sub2api`、`sub2api-postgres`；自定义名称应同步替换。命令块按章节执行，表格中的不同启动命令是备选，不是连续操作。

```bash
cd /path/to/sub2api

# 确认正在运行的容器及其镜像
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'

# 查看应用原来的 Compose 文件组合
docker inspect sub2api \
  --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}'

# 查看 PostgreSQL 的挂载类型和目录
docker inspect sub2api-postgres \
  --format '{{range .Mounts}}{{println .Type .Source "->" .Destination}}{{end}}'
```

针对 PostgreSQL 数据挂载，`bind` 表示宿主机目录挂载，`volume` 表示 Docker 卷；同时核对实际目录或卷名。内置脚本的默认布局是 `deploy/` 下的本地目录，命名卷布局使用 `--storage named`。非标准卷名、自定义目录或混合布局继续沿用原来的 Compose 映射，不仅凭挂载类型切换启动方式。

systemd 部署可先查看真实启动命令：

```bash
sudo systemctl cat sub2api
```

## 2. 将新源码同步到 Linux

```bash
cd /path/to/sub2api
git status --short
```

先确认 Linux 上的代码包含本次修改。只有修改已经提交并推送到你的代码仓库时，`git pull` 才能取得它们；本地未提交的修改和新增文件需要一并上传。存在服务器本地修改时，先备份并核对差异，再合并更新。

同步源码时保留服务器现有的：

- `deploy/.env`，以及实际使用的其他环境文件。
- 实际配置文件 `config.yaml`。本地目录版通常位于 `deploy/data/config.yaml`，容器内通常位于 `/app/data/config.yaml`。
- PostgreSQL、Redis、应用和插件的持久化数据。
- 原来的 Compose 项目名称、数据挂载、数据库镜像版本。
- Nginx/Caddy 配置和证书数据。

`.env.example` 和 `config.example.yaml` 是模板，不是线上配置。已有环境继续使用原来的数据库密码、JWT 密钥和 TOTP 密钥。源码同步不要顺便替换现有数据目录或切换 PostgreSQL 主版本。

本次菜单调整属于前端代码更新。Docker 需要重建镜像并更新容器；二进制部署需要先构建前端，再构建带前端资源的后端。单纯重启旧容器或旧程序不会编译新源码。

## 3. Docker 部署更新

### 3.1 保存配置、数据和旧镜像

下面在 Bash 中执行。`BACKUP_DIR`、`ROLLBACK_IMAGE` 等变量在当前终端有效，后续回滚章节说明如何在新终端重新设置。

```bash
set -euo pipefail
umask 077

cd /path/to/sub2api

BACKUP_DIR="$HOME/sub2api-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -m 700 "$BACKUP_DIR"

# 保留服务器正在使用的环境文件和应用数据
cp -a deploy/.env "$BACKUP_DIR/deploy.env"
docker cp sub2api:/app/data "$BACKUP_DIR/app-data"

# 使用运行容器的实际镜像 ID，避免备份到错误的同名标签
OLD_IMAGE_ID="$(docker inspect sub2api --format '{{.Image}}')"
ROLLBACK_IMAGE="sub2api-backup:$(date +%Y%m%d-%H%M%S)"
docker image tag "$OLD_IMAGE_ID" "$ROLLBACK_IMAGE"
printf '%s\n' "$ROLLBACK_IMAGE" > "$BACKUP_DIR/rollback-image.txt"

echo "备份目录：$BACKUP_DIR"
echo "回滚镜像：$ROLLBACK_IMAGE"
```

`docker image tag` 保留一个可引用的旧镜像标签，镜像仍位于当前主机。升级期间保留该镜像，不清理这个备份标签。如需异机备份，可以额外执行：

```bash
docker image save "$ROLLBACK_IMAGE" -o "$BACKUP_DIR/old-image.tar"
```

`/app/data` 的备份不包含 PostgreSQL 数据库，也不覆盖应用目录之外的自定义插件挂载或 Caddy 卷。按原部署布局补充备份这些目录或卷；应用/插件文件频繁变化时安排维护窗口，以获得一致的文件备份。

使用仓库内置 PostgreSQL 容器时，备份应用数据库：

```bash
docker exec sub2api-postgres sh -c \
  'export PGPASSWORD="$POSTGRES_PASSWORD"; exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > "$BACKUP_DIR/database.dump"

# 非空检查不能替代恢复演练；备份应按既有流程校验并保存到异机位置
test -s "$BACKUP_DIR/database.dump"
```

使用外部数据库时，按照实际连接信息执行现有数据库备份流程。`pg_dump` 示例备份的是应用数据库，不包含整个 PostgreSQL 集群的角色和其他数据库。

### 3.2 选择与原部署一致的更新命令

当前源码部署入口是 `deploy/start.sh`。在仓库根目录，按原来的布局选择一条：

| 原来的部署方式 | 更新命令 |
| --- | --- |
| 本地目录存储，未使用内置 Caddy HTTPS | `bash deploy/start.sh` |
| 本地目录存储，使用内置 Caddy HTTPS | `bash deploy/start.sh --https` |
| Docker 命名卷存储，未使用内置 Caddy HTTPS | `bash deploy/start.sh --storage named` |
| Docker 命名卷存储，使用内置 Caddy HTTPS | `bash deploy/start.sh --storage named --https` |

这里的 `--https` 指仓库自带的 Caddy HTTPS 部署。如果 HTTPS 由宿主机 Nginx/Caddy 提供，继续沿用原来的反向代理，不另行启用内置 Caddy。

脚本要求 Docker Compose v2 支持 `up --wait`，以及 Bash 4+；构建需要访问基础镜像与依赖。脚本不会安装系统组件。已有配置继续使用原值；启用内置 HTTPS 时，检查已有 `.env` 中的 `SITE_DOMAIN` 和 loopback 绑定设置。

脚本的更新流程是：

1. 保留已有 `deploy/.env`，校验配置和当前存储布局。
2. 使用根目录 `Dockerfile` 构建当前源码。该 Dockerfile 先构建前端，再构建嵌入前端资源的 Go 后端。
3. 更新 Compose 服务，等待健康状态并输出服务状态。

发布新的源码修改时，使用上表中的源码构建命令；`--no-build` 会跳过构建，`--image` 会使用指定的预构建镜像，二者都不是本步骤的默认选择。只拉取上游官方应用镜像也不会发布本地定制修改。

保留原有数据挂载即可继续使用原数据。更新无需先执行 `docker compose down`；保留数据的更新流程不要执行 `docker compose down -v`，该选项会删除 Compose 管理的命名卷。

### 3.3 检查运行结果

```bash
docker ps --filter name=sub2api
docker logs --tail 100 sub2api

# 8080 是默认宿主机端口，按实际映射替换
curl -fsS http://127.0.0.1:8080/health
```

进一步验收：

- 检查日志中是否有启动、迁移或配置读取错误。
- 使用实际域名检查 HTTPS 和反向代理。
- 登录后台，强制刷新页面，检查新菜单和当前页面高亮。
- 分别检查管理员、普通用户和授权角色的入口。
- 验证普通/流式请求、用量记账，以及已启用的插件功能。

健康检查证明服务健康端点可访问，不等于全部业务验收完成。

### 3.4 回退到旧镜像

先设置真实的备份目录。即使换了终端，也可以从保存的文件读取旧镜像标签：

```bash
set -euo pipefail
cd /path/to/sub2api

# 替换为第 3.1 节输出的实际备份目录
BACKUP_DIR="$HOME/sub2api-backup-YYYYMMDD-HHMMSS"
ROLLBACK_IMAGE="$(cat "$BACKUP_DIR/rollback-image.txt")"
docker image inspect "$ROLLBACK_IMAGE" >/dev/null
```

保留同样的存储和 HTTPS 参数，选择一条回滚命令：

| 原来的部署方式 | 回滚命令 |
| --- | --- |
| 本地目录，未使用内置 Caddy HTTPS | `bash deploy/start.sh --image "$ROLLBACK_IMAGE"` |
| 本地目录，使用内置 Caddy HTTPS | `bash deploy/start.sh --image "$ROLLBACK_IMAGE" --https` |
| 命名卷，未使用内置 Caddy HTTPS | `bash deploy/start.sh --image "$ROLLBACK_IMAGE" --storage named` |
| 命名卷，使用内置 Caddy HTTPS | `bash deploy/start.sh --image "$ROLLBACK_IMAGE" --storage named --https` |

回滚后再次执行第 3.3 节的健康和业务检查。如果旧镜像已导出并且当前主机缺少它，先用 `docker image load -i "$BACKUP_DIR/old-image.tar"` 加载。

**镜像回退与数据库回退是两件事。**应用启动会执行数据库迁移；涉及与旧版本不兼容的迁移时，需要结合升级前数据库备份、应用数据和旧配置制定恢复步骤，并停止相关写入后按既有恢复流程执行。不要仅替换旧镜像就认定数据库结构已经恢复。

### 3.5 外部数据库或自定义 Compose

如果原来使用 `docker-compose.standalone.yml`、非标准卷名、自定义目录或混合布局，继续使用原部署文件，不切换到默认三服务部署。保留原 Compose 文件顺序、项目名称（含 `-p`/`COMPOSE_PROJECT_NAME`）、环境文件和所有覆盖文件。

更新流程仍然是：备份 → 将应用镜像构建为当前源码 → 使用原 Compose 组合更新应用服务 → 检查健康及业务。

使用源代码构建覆盖文件时，检查合并后的 `build`、`image` 和挂载；`deploy/docker-compose.source.yml` 中的构建上下文相对该部署目录指向仓库根目录。`docker compose config --quiet` 可校验原文件组合的语法，但还应核对最终数据挂载和镜像版本。

## 4. 二进制 + systemd 部署更新

### 4.1 确认工具链与实际程序路径

```bash
cd /path/to/sub2api
node --version
pnpm --version
go version
sudo systemctl cat sub2api
```

工具链以当前源码声明为准：`backend/go.mod` 在本文编写时声明 Go 1.27.0；根 Dockerfile 使用 Node 24 和 pnpm 9。手工构建时使用与这些声明相匹配的工具链。

仓库提供的 `deploy/sub2api.service` 使用 `/opt/sub2api/sub2api` 作为 `ExecStart`，`/opt/sub2api` 作为工作目录。如果你的实际配置不同，后续程序路径按 `systemctl cat sub2api` 的输出替换。

构建前按原部署布局备份实际配置、数据库、插件及应用数据。下面的程序备份只用于二进制回退，不代替数据备份。

### 4.2 先构建前端，再构建嵌入前端的程序

```bash
set -euo pipefail
cd /path/to/sub2api

pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend build

mkdir -p backend/bin
(
  cd backend
  CGO_ENABLED=0 go build -tags embed -trimpath \
    -o bin/sub2api ./cmd/server
)

test -s backend/bin/sub2api
```

前端构建产物位于 `backend/internal/web/dist`。`backend/internal/web/embed_on.go` 使用 `//go:embed all:dist` 将其嵌入带 `embed` 标签的程序。保持上述构建顺序，避免把旧前端资源编进新程序。

本步骤先完成构建，服务仍可运行；构建成功后再进入替换阶段。

### 4.3 备份并替换运行程序

以下对应仓库默认 systemd 路径，只停止应用服务，不停止数据库或 Redis：

```bash
set -euo pipefail
cd /path/to/sub2api

BINARY_BACKUP="/opt/sub2api/sub2api.bak.$(date +%Y%m%d-%H%M%S)"
sudo cp -a /opt/sub2api/sub2api "$BINARY_BACKUP"
echo "旧程序备份：$BINARY_BACKUP"

sudo systemctl stop sub2api
sudo install -m 755 backend/bin/sub2api /opt/sub2api/sub2api
sudo systemctl start sub2api

sudo systemctl status sub2api --no-pager
sudo journalctl -u sub2api -n 100 --no-pager
curl -fsS http://127.0.0.1:8080/health
```

保留现有配置、资源和数据目录，并检查真实端口与工作目录。若替换或启动失败，直接按第 4.4 节恢复旧程序，不在服务停止状态下重复构建。

只替换程序不需要 `daemon-reload`。如果同时修改了 systemd unit 文件，加载新的 unit 后再启动：

```bash
sudo systemctl daemon-reload
sudo systemctl restart sub2api
```

随后按第 3.3 节的业务清单验收新版本。

### 4.4 二进制回滚

在新终端中，将占位符替换为第 4.3 节输出的真实旧程序路径：

```bash
set -euo pipefail
BINARY_BACKUP="/opt/sub2api/sub2api.bak.YYYYMMDD-HHMMSS"
sudo test -s "$BINARY_BACKUP"

sudo systemctl stop sub2api
sudo cp -a "$BINARY_BACKUP" /opt/sub2api/sub2api
sudo systemctl start sub2api

sudo systemctl status sub2api --no-pager
sudo journalctl -u sub2api -n 100 --no-pager
curl -fsS http://127.0.0.1:8080/health
```

二进制回滚同样不自动恢复数据库迁移；涉及不兼容结构变更时，结合升级前数据备份执行恢复。

## 5. 更新后仍看不到新菜单时的检查顺序

1. **源码**：确认服务器上包含修改后的 `frontend/src/components/layout/AppSidebar.vue`、语言文件及新增文件，不只是拉取了上游代码。
2. **构建**：检查前端与镜像/二进制构建是否成功；确认没有使用旧镜像或跳过源码构建。
3. **运行版本**：Docker 检查 `docker inspect sub2api --format '{{.Image}}'`；systemd 检查实际 `ExecStart` 和启动日志。
4. **访问目标**：确认域名、反向代理和负载均衡指向已更新的实例；多实例部署逐一核对。
5. **浏览器与缓存**：强制刷新或重新打开页面；存在 CDN/代理缓存时检查旧 HTML 是否仍被缓存。
6. **菜单条件**：核对登录用户权限、后台功能开关和简易模式。分类会按可见的下级入口过滤，空分类会隐藏。

## 6. 相关项目文件

- [当前源码一键部署说明](../deploy/ONE_CLICK_CN.md)
- [部署文件总览](../deploy/README.md)
- 源码部署脚本：`deploy/start.sh`
- 源码构建覆盖文件：`deploy/docker-compose.source.yml`
- 多阶段构建入口：`Dockerfile`
- systemd 默认配置：`deploy/sub2api.service`
- 前端构建命令：`frontend/package.json`
- 前端资源嵌入实现：`backend/internal/web/embed_on.go`

本文记录操作步骤。文档命令需在你的 Linux 环境按实际路径和原部署参数执行；本文的命令示例校验不代表已经更新了远程服务器。
