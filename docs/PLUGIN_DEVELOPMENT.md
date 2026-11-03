# Sub2API 插件开发指南（中文）

本指南依据 **2026-10-12 当前仓库实现**，面向第一次写插件的开发者。新开发的输入检查、输出检查、价格同步及其他独立任务，优先使用 **schema2 / general.v1**。先完成下面的最小例子，再扩展业务规则；旧的 schema1 OAuth 传输插件放在附录中，两个协议的运行时和配置流程分开使用。

插件是宿主启动的独立进程，不是向前端粘贴一段 JavaScript，也不是往后端直接加载 Go 动态库。开发产物是 `.s2plugin` ZIP 包；宿主通过 Hashicorp go-plugin + gRPC 调用它。

## 阅读路线

1. [选择插件类型](#1-选择插件类型)：明确 hook、task 或旧 transport。
2. [创建工程](#2-开发环境与工程)：知道文件放在哪里、如何使用 SDK。
3. [最小输入检查插件](#3-最小输入检查插件)：完整 Go 代码、配置、清单和测试。
4. [打包](#4-编译与打包)：真实运行时 → 清单 SHA → `.s2plugin`。
5. [签名](#5-生成密钥签名与验签)：使用本仓库实际提供的工具。
6. [安装与发布](#6-安装与发布)：上传、实例、配置版本、发布、分组绑定。
7. [独立任务](#8-独立任务开发)：pricing-sync、scope、执行记录。
8. [排错与发布](#10-排错清单)：权限、签名、版本、运行时和回滚。

## 1. 选择插件类型

| 类型 | 清单/SDK | 最少实现 | 生效流程 | 可参考的真实源码 |
|---|---|---|---|---|
| 输入/输出检查 | schema2 / general.v1；`request.hook.v1` | `GetInfo`、`Configure`、`Check` | 安装 → 实例配置 → 发布 → 分组绑定 | `credential-input` / `business-output` |
| 独立任务 | schema2 / general.v1；`independent.task.v1` | `GetInfo`、`Configure`、`RunTask` | 安装 → 实例配置 → 发布 → 创建并执行任务 | `pricing-sync` |
| OpenAI OAuth 出站传输 | schema1 / transport v1 | `GetInfo`、`Health`、配置方法、`Forward` 等 | 安装 → 旧配置 UI → 传输插件启用 | 附录 A.1–A.11 |

通用协议定义：[general.proto](../backend/pkg/pluginapi/general/v1/general.proto)；运行入口：[runtime.go](../backend/pkg/pluginapi/general/v1/runtime.go)；详细 SDK：[general-v1.md](../backend/pkg/pluginapi/docs/general-v1.md)。

**schema2 使用 `general.Serve`；schema1 使用 `pluginv1.Serve`。** 二者的握手 cookie、RPC、能力名称不同。schema2 headless 插件的配置入口是“插件实例”，不是旧传输插件的“配置 / 启用”按钮。

## 2. 开发环境与工程

- Go 工具链遵循 `backend/go.mod`（当前声明 `go 1.27.0`）。请在 `backend` 目录执行 `go version` 确认生效版本；外层目录的 `go version` 可能不同，Go 的自动工具链可能切换版本。
- Python 3.9+：打包器使用标准库；Node.js 仅用于自己开发静态 UI，并非 headless 插件依赖。
- 当前教程采用主仓库 Go module，方便直接运行；独立仓库也可以依赖公开 SDK，但应固定 SDK revision，开发时可通过 Go `replace` 指向本地 `backend`。不引入宿主 `internal/*` 作为插件依赖。

在主仓库创建以下目录和文件：

```text
backend/examples/hello-input/
├── main.go                 # 第 3 节代码
├── main_test.go            # 第 3 节测试
├── manifest.source.json    # 打包模板，不是可上传的 manifest
├── config.schema.json      # 描述配置；实际 RPC 校验由插件实现
├── config.example.json     # 可直接保存到实例的完整配置
└── README.md               # 用途、配置、限制及变更说明
```

运行时、未签名包、签名包和发布者私钥放在仓库外。后面的 Windows 示例使用 `D:/tmp/hello-input-build`；Linux 对应 `/tmp/hello-input-build`。README 可先写“输入包含配置关键字时阻断；仅支持 string 类型的 Chat Completions messages.content”。

### 独立工程如何引用 SDK

若工程放在仓库之外，先初始化自己的 Go module，再通过本地 replace 使用当前 SDK（Windows 示例，工作目录为自己的插件目录）：

```sh
go mod init example.com/my-hello-input
go mod edit -go=1.27.0
go mod edit -require=github.com/Wei-Shaw/sub2api@v0.0.0
go mod edit -replace=github.com/Wei-Shaw/sub2api=D:/projects/sub2api/backend
```

然后创建下面的 main.go/main_test.go，执行 `go mod tidy` 与 `go test -count=1 ./...`。Linux 替换本地 backend 路径。`v0.0.0` 是被本地 replace 覆盖的开发要求，不是已发布版本；发布时固定一个确实包含 general/v1 的 SDK tag/commit，移除开发机绝对路径的 replace，保留 go.mod/go.sum。当前工作区中的新增 SDK 不自动等于远端已发布 SDK。

### 先跑现有例子，确认开发环境

以下命令工作目录都是 `backend`。这一步沿用仓库已有的三个示例，不需要写新插件：

```sh
python -B examples/general-plugins/package_test.py
python -B examples/general-plugins/package.py --out /tmp/general-plugin-examples
python -B examples/general-plugins/package.py --verify /tmp/general-plugin-examples/credential-input-1.0.0.s2plugin
go test -count=1 ./examples/general-plugins/internal/sample/...
go test -count=1 ./examples/general-plugins -args -packages=/tmp/general-plugin-examples
```

Windows 把输出路径换成 `D:/tmp/general-plugin-examples`。该既有集成测试专门接收 **三个原始未签名示例包**，以开发配置启动；自己的包、签名包另行做对应的宿主安装/进程验收，不将它们误传给这个固定三示例测试。

## 3. 最小输入检查插件

目标：只检查 OpenAI Chat Completions 请求中 string 类型的 `messages[].content`；出现 `keyword` 时返回 `block`，其他文本返回 `pass`。例子只声明一种协议，不宣称支持全部四种文本协议或非文本内容。扩展到 text blocks、Responses、Anthropic、Gemini 时，复用真实示例的协议解析规则并补测试。

### 3.1 配置示例：`config.example.json`

<!-- quickstart-config:start -->
```json
{
  "config_version": 1,
  "keyword": "PLUGIN_TEST_BLOCK"
}
```
<!-- quickstart-config:end -->

空对象 `{}` 使用默认值。配置版本 `config_version` 与实例的 `revision` 是不同概念：前者表示配置解释结构，后者是宿主保存的不可变配置版本。

### 3.2 配置结构：`config.schema.json`

<!-- quickstart-schema:start -->
```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "hello-input config v1",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "config_version": {
      "type": "integer",
      "const": 1,
      "default": 1
    },
    "keyword": {
      "type": "string",
      "minLength": 1,
      "maxLength": 256,
      "default": "PLUGIN_TEST_BLOCK"
    }
  }
}
```
<!-- quickstart-schema:end -->

schema 用于说明/编辑，宿主还会调用插件的 `Configure`。严格拒绝未知配置字段、无效 JSON、错误结构版本和超限内容，实际校验不能只依赖 schema 文件。

### 3.3 清单模板：`manifest.source.json`

<!-- quickstart-manifest:start -->
```json
{
  "schema_version": 2,
  "id": "com.example.hello-input",
  "name": "hello-input",
  "version": "1.0.0",
  "description": "Minimal keyword input check",
  "author": "Example developer",
  "requires": {
    "sub2api": ">=0.1.0 <1.0.0",
    "plugin_protocol": 1,
    "general_api": 1,
    "transport_api": 0,
    "ui_bridge": 0
  },
  "capabilities": [
    {
      "id": "request.hook.v1"
    }
  ],
  "general": {
    "config_version": 1,
    "hooks": [
      {
        "name": "hello-input",
        "stages": [
          "input"
        ],
        "protocols": [
          "openai.chat_completions.v1"
        ],
        "can_replace": false
      }
    ]
  },
  "runtimes": {},
  "ui": {},
  "files": {}
}
```
<!-- quickstart-manifest:end -->

`runtimes` 和 `files` 的空对象是**构建模板**。第 4 节打包器会填充真实路径和 SHA，源模板本身不是安装包。

`GetInfo` 的 plugin ID、版本、config version、capabilities、hook entry/stages/protocols/can_replace 必须与最终清单完全一致。改名时同步修改清单、Go 返回值和测试。宿主会在进程握手时核对这些字段。

### 3.4 完整运行时：`main.go`

<!-- quickstart-go:start -->
```go
package main

import (
 "bytes"
 "context"
 "encoding/json"
 "mime"
 "strings"
 "sync"
 "unicode/utf8"
 general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
 "google.golang.org/grpc/codes"
 "google.golang.org/grpc/status"
)

type configuration struct {
 ConfigVersion int64 `json:"config_version"`
 Keyword string `json:"keyword"`
}
type server struct {
 general.UnimplementedGeneralPluginServer
 mu sync.RWMutex
 configured bool
 instance, revision int64
 config configuration
}
func invalid() error { return status.Error(codes.InvalidArgument, "invalid plugin input") }
func canceled(ctx context.Context) error {
 if err := ctx.Err(); err != nil { return status.FromContextError(err).Err() }
 return nil
}
func (s *server) GetInfo(ctx context.Context, _ *general.InfoRequest) (*general.InfoResponse,error) {
 if err := canceled(ctx); err != nil { return nil,err }
 return &general.InfoResponse{
  PluginId:"com.example.hello-input", PluginVersion:"1.0.0",
  ProtocolVersion:1, GeneralApiVersion:1, ConfigVersion:1,
  Capabilities:[]string{"request.hook.v1"},
  Hooks:[]*general.HookDeclaration{{Name:"hello-input",Stages:[]string{"input"},
   Protocols:[]string{"openai.chat_completions.v1"},CanReplace:false}},
 },nil
}
func (s *server) Configure(ctx context.Context,r *general.ConfigureRequest) (*general.ConfigureResponse,error) {
 if err := canceled(ctx); err != nil { return nil,err }
 if r==nil || r.InstanceId<=0 || r.ConfigRevision<=0 || r.ConfigVersion!=1 { return nil,invalid() }
 if len(r.ConfigJson)>64*1024 { return nil,status.Error(codes.ResourceExhausted,"plugin size limit exceeded") }
 raw := bytes.TrimSpace(r.ConfigJson)
 if len(raw)==0 || raw[0]!='{' || !json.Valid(raw) { return nil,invalid() }
 cfg := configuration{ConfigVersion:1,Keyword:"PLUGIN_TEST_BLOCK"}
 decoder := json.NewDecoder(bytes.NewReader(raw)); decoder.DisallowUnknownFields()
 if err := decoder.Decode(&cfg); err!=nil { return nil,invalid() }
 if cfg.ConfigVersion!=1 || cfg.Keyword=="" || len(cfg.Keyword)>256 || !utf8.ValidString(cfg.Keyword) { return nil,invalid() }
 s.mu.Lock(); defer s.mu.Unlock()
 if s.configured { return nil,status.Error(codes.FailedPrecondition,"immutable plugin configuration") }
 if err := canceled(ctx); err!=nil { return nil,err }
 s.config=cfg; s.instance=r.InstanceId; s.revision=r.ConfigRevision; s.configured=true
 return &general.ConfigureResponse{Accepted:true,InstanceId:s.instance,ConfigRevision:s.revision},nil
}
func (s *server) Check(ctx context.Context,r *general.HookRequest) (*general.HookResponse,error) {
 if err:=canceled(ctx); err!=nil { return nil,err }
 if r==nil { return nil,invalid() }
 s.mu.RLock(); defer s.mu.RUnlock()
 if !s.configured || r.InstanceId!=s.instance || r.ConfigRevision!=s.revision {
  return nil,status.Error(codes.FailedPrecondition,"plugin binding mismatch")
 }
 if r.Entry!="hello-input" || r.Stage!="input" || r.Protocol!="openai.chat_completions.v1" || r.Content==nil { return nil,invalid() }
 if len(r.Content.Data)>1024*1024 { return nil,status.Error(codes.ResourceExhausted,"plugin size limit exceeded") }
 media,params,err:=mime.ParseMediaType(r.Content.MediaType)
 if err!=nil || media!="application/json" || (params["charset"]!="" && !strings.EqualFold(params["charset"],"utf-8")) || !utf8.Valid(r.Content.Data) { return nil,invalid() }
 var request struct { Messages []map[string]any `json:"messages"` }
 if err:=json.Unmarshal(r.Content.Data,&request); err!=nil || len(request.Messages)==0 { return nil,invalid() }
 blocked:=false
 for _,message:=range request.Messages {
  if err:=canceled(ctx); err!=nil { return nil,err }
  text,ok:=message["content"].(string); if !ok { return nil,invalid() }
  blocked = blocked || strings.Contains(text,s.config.Keyword)
 }
 if err:=canceled(ctx); err!=nil { return nil,err }
 if blocked {
  return &general.HookResponse{Decision:"block",
   Rejection:&general.Rejection{Code:"keyword_detected",Message:"Input blocked by configured rule."},
   Findings:[]*general.Finding{{RuleId:"keyword",RuleVersion:"1",Category:"input-rule"}},
  },nil
 }
 return &general.HookResponse{Decision:"pass"},nil
}
func main() { general.Serve(&server{}) }
```
<!-- quickstart-go:end -->

这里通过 `UnimplementedGeneralPluginServer` 提供未声明的 `RunTask` 默认实现；只声明 hook 的插件无需伪造任务入口。进程 stdout 由 go-plugin 握手使用，不向它打印业务内容。

每个进程只接受一次成功的 `Configure`。重新发布配置由宿主创建新进程，不在同一进程里热改实例和 revision。Check 必须核对请求的绑定身份并沿用 RPC context；示例设置 config ≤64 KiB、content ≤1 MiB，SDK 消息总上限是 2 MiB。生产实现还应按详细 SDK 增加 JSON 重复键/深度、配置字段 null、规范文本块及完整资源边界测试；本节是最小 string 消息教学实现，不宣称覆盖完整协议。

### 3.5 完整单元测试：`main_test.go`

<!-- quickstart-go-test:start -->
```go
package main

import (
 "context"
 "testing"
 general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
 "google.golang.org/grpc/codes"
 "google.golang.org/grpc/status"
)
func configured(t *testing.T) *server {
 t.Helper(); s:=&server{}
 _,err:=s.Configure(context.Background(),&general.ConfigureRequest{InstanceId:7,ConfigRevision:1,ConfigVersion:1,ConfigJson:[]byte(`{}`)})
 if err!=nil { t.Fatal(err) }; return s
}
func request(text string) *general.HookRequest {
 return &general.HookRequest{InstanceId:7,ConfigRevision:1,Entry:"hello-input",Stage:"input",Protocol:"openai.chat_completions.v1",
  Content:&general.Content{MediaType:"application/json",Data:[]byte(`{"messages":[{"role":"user","content":"`+text+`"}]}`)}}
}
func TestHelloInput(t *testing.T) {
 for _,tc:=range []struct{name,text,want string}{{"pass","hello","pass"},{"block","PLUGIN_TEST_BLOCK","block"}} {
  t.Run(tc.name,func(t *testing.T){r,err:=configured(t).Check(context.Background(),request(tc.text));if err!=nil || r.Decision!=tc.want { t.Fatalf("result=%v err=%v",r,err) }})
 }
 t.Run("immutable",func(t *testing.T){s:=configured(t);_,err:=s.Configure(context.Background(),&general.ConfigureRequest{InstanceId:7,ConfigRevision:2,ConfigVersion:1,ConfigJson:[]byte(`{}`)});if status.Code(err)!=codes.FailedPrecondition {t.Fatal(err)}})
 t.Run("binding",func(t *testing.T){r:=request("hello");r.ConfigRevision=2;_,err:=configured(t).Check(context.Background(),r);if status.Code(err)!=codes.FailedPrecondition {t.Fatal(err)}})
 t.Run("cancel",func(t *testing.T){ctx,cancel:=context.WithCancel(context.Background());cancel();_,err:=configured(t).Check(ctx,request("hello"));if status.Code(err)!=codes.Canceled {t.Fatal(err)}})
 t.Run("invalid_config",func(t *testing.T){_,err:=(&server{}).Configure(context.Background(),&general.ConfigureRequest{InstanceId:7,ConfigRevision:1,ConfigVersion:1,ConfigJson:[]byte(`{"unknown":true}`)});if status.Code(err)!=codes.InvalidArgument {t.Fatal(err)}})
 t.Run("info",func(t *testing.T){info,err:=(&server{}).GetInfo(context.Background(),&general.InfoRequest{});if err!=nil || info.PluginId!="com.example.hello-input" || info.Hooks[0].Name!="hello-input" {t.Fatalf("info=%v err=%v",info,err)}})
}
```
<!-- quickstart-go-test:end -->

```sh
# cwd: backend
go test -count=1 -v ./examples/hello-input
```

至少应观察到 pass、block、重复 Configure、错误 revision、取消、未知配置字段和身份信息七个子用例通过。进一步测试非法 JSON/UTF-8、超限、错误 entry/stage/protocol、text blocks、并发调用和进程退出；上线前采用真实宿主安装与 gRPC 子进程测试，直接调用 Go 方法不等于握手验收。

## 4. 编译与打包

### 4.1 编译目标运行时

工作目录 `backend`。Linux/macOS/Git Bash：

```sh
mkdir -p /tmp/hello-input-build
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags '-s -w' -o /tmp/hello-input-build/plugin-linux ./examples/hello-input
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags '-s -w' -o /tmp/hello-input-build/plugin-windows.exe ./examples/hello-input
```

Windows PowerShell：

```powershell
$build = 'D:/tmp/hello-input-build'
New-Item -ItemType Directory -Force -Path $build | Out-Null
$previous = @{ GOOS=$env:GOOS; GOARCH=$env:GOARCH; CGO_ENABLED=$env:CGO_ENABLED }
try {
  $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
  $env:GOOS='windows'
  go build -trimpath -buildvcs=false -ldflags '-s -w' -o "$build/plugin-windows.exe" ./examples/hello-input
  if ($LASTEXITCODE -ne 0) { throw 'Windows build failed' }
  $env:GOOS='linux'
  go build -trimpath -buildvcs=false -ldflags '-s -w' -o "$build/plugin-linux" ./examples/hello-input
  if ($LASTEXITCODE -ne 0) { throw 'Linux build failed' }
} finally {
  $env:GOOS=$previous.GOOS; $env:GOARCH=$previous.GOARCH; $env:CGO_ENABLED=$previous.CGO_ENABLED
}
```

构建 Linux 文件不等于在 Linux 上执行过。宿主按 `GOOS-GOARCH` 选择 runtime；arm64 宿主须另编译对应二进制，本通用打包器也接受 `linux-arm64`。

### 4.2 使用本仓库通用 headless 打包器

[plugin-package.py](../backend/scripts/plugin-package.py) 接受自己的源码目录和已编译二进制，不固定插件名称。以下仍在 `backend` 执行：

```sh
python -B scripts/plugin-package.py --source examples/hello-input --runtime linux-amd64=/tmp/hello-input-build/plugin-linux --runtime windows-amd64=/tmp/hello-input-build/plugin-windows.exe --out /tmp/hello-input-build/hello-input-1.0.0.unsigned.s2plugin
python -B scripts/plugin-package.py --verify /tmp/hello-input-build/hello-input-1.0.0.unsigned.s2plugin
```

Windows 把所有 `/tmp/hello-input-build` 替换为 `D:/tmp/hello-input-build`。工具会拒绝覆盖已有输出，重新构建时使用新的输出文件名或单独归档旧产物。

包内布局：

```text
manifest.json
README.md
sdk/config.schema.json
sdk/config.example.json
runtime/linux_amd64/plugin
runtime/windows_amd64/plugin.exe
signature.json                     # 第 5 节签名后增加
```

runtime key 是 `linux-amd64`，包内目录可以是 `linux_amd64`，以清单声明为准。所有普通文件写入 `manifest.files`；manifest 和 signature 自身不写入该映射。

该工具针对 **schema2/general.v1/headless**，不编译二进制、不提供 UI 构建。`--verify` 检查 ZIP/文件哈希/运行时格式，不校验发布者签名；签名验证用下一节工具。宿主 `PluginManifest.Validate` 和实际安装仍是最终验收。完整包规则见 [package-format.md](../backend/pkg/pluginapi/docs/package-format.md)。

## 5. 生成密钥、签名与验签

使用仓库真实提供的 [scripts/plugin-sign/main.go](../backend/scripts/plugin-sign/main.go)，无需另找 `tools/keygen` 或外部 `build.sh`。它只使用 Go 标准库，签名对象是 `manifest.json` 的**精确原始字节**，签名后不重新格式化该文件。

下面以 Bash/Linux 路径演示；Windows 对应替换为 `D:/tmp/hello-input-build` 与本地密钥目录，命令工作目录 `backend`：

```sh
# 只在首次创建发布者时执行；工具拒绝覆盖已有密钥。
go run ./scripts/plugin-sign keygen -private /tmp/hello-input-keys/publisher.private.pem -public /tmp/hello-input-keys/publisher.public.txt
# 保留 unsigned 原包，生成新的签名副本。
go run ./scripts/plugin-sign sign -in /tmp/hello-input-build/hello-input-1.0.0.unsigned.s2plugin -out /tmp/hello-input-build/hello-input-1.0.0.s2plugin -private /tmp/hello-input-keys/publisher.private.pem -key-id my-plugin-publisher-v1
go run ./scripts/plugin-sign verify -in /tmp/hello-input-build/hello-input-1.0.0.s2plugin -public /tmp/hello-input-keys/publisher.public.txt -key-id my-plugin-publisher-v1
```

发布者保留并备份私钥；交给部署者的是 `publisher.public.txt` 和签名包。公钥文件是原始 32 字节 Ed25519 公钥的 Base64 文本，不是 PEM 公钥、不包含私钥。

部署者把公钥文件完整的一行合并进实际 `config.yaml`，`key_id` 与配置键一致：

```yaml
plugins:
  allow_unsigned: false
  trusted_publishers:
    my-plugin-publisher-v1: "这里替换成 publisher.public.txt 的完整内容"
```

Docker Compose 的 `.env` 同时保持 `PLUGINS_ALLOW_UNSIGNED=false`。`trusted_publishers` 是结构配置，写入实际持久化的 config.yaml（标准容器通常是 `/app/data/config.yaml`），不是改 `config.example.yaml` 模板。明确设置了 `CONFIG_FILE` 时以该路径为准；未设置时按 DATA_DIR、/app/data、工作目录、./config、/etc/sub2api 顺序查找。环境变量覆盖同名 YAML 标量。

修改宿主配置后重启或使用原 Compose 参数重新创建服务。单纯安装、发布插件通常无需重启宿主；这次重启的原因是改变了宿主公钥配置。私钥不进入包、Git 或部署服务器。签名代表包来自配置的发布者且未被改动，不代表业务逻辑已经通过测试。

## 6. 安装与发布

### 6.1 后台操作（第一次开发优先用这一条）

1. 系统设置 → 功能开关 → 插件管理 → **显示插件管理菜单** → 保存；该开关仅控制菜单显示，不停止运行中的插件。
2. 超级管理员进入插件管理 `/admin/plugins` → 安装插件 → 上传**签名后的 `.s2plugin`**。
3. 确认签名可信、兼容版本和安装 ID。`plugin_id` 是安装对象的数值 ID，不是清单字符串 `com.example.hello-input`。
4. 插件实例 `/admin/plugin-instances` → 创建实例，选择/填写安装 ID 和名称。创建请求需要配置对象，`{}` 可以由本例规范化为默认配置；初始实例为停用状态。
5. 打开详情 → 完整 JSON 配置 → 粘贴第 3.1 节 → **保存新版本**。配置只写不回显，保存只是生成 revision，不自动发布。
6. 在配置版本列表对目标 revision 点击 **发布 / 回退**，确认已发布版本更新。
7. 新增分组绑定：业务分组 ID、entry=`hello-input`、stage=`input`、protocol=`openai.chat_completions.v1`、enabled=`true`、can_replace=`false`、sort_order=`10`，保存。
8. 用该分组的网关 API Key 发普通文本和 `PLUGIN_TEST_BLOCK` 两种请求，核对 pass/block；插件的 rejection message 不直接作为客户端错误正文。

实例可以被多个分组复用，配置相互独立则创建多个实例。绑定规则和已发布 revision 都是生效条件；仅安装包不执行检查。

### 6.2 接口与返回结构

管理员接口前缀 `/api/v1/admin`。详情和当前运行状态分别在 `/plugin-instances/:id`、`/plugin-instances/:id/status`；status 是只读观察，不会启动插件进程。

| 动作 | 路径 | 请求关键字段 | 返回字段 / 权限 |
|---|---|---|---|
| 上传包 | POST `/plugins/upload` | multipart `plugin` | `.data.id`；超级管理员，step-up |
| 创建实例 | POST `/plugin-instances` | `plugin_id, name, config` | `.data.instance.id`、`.generation`；`plugin_instances.create` |
| 保存配置 | PUT `/plugin-instances/:id/config` | `expected_generation, config` | `.data.instance.latest_revision`；`plugin_instances.update` |
| 发布/回退 | POST `/plugin-instances/:id/publish` | `expected_generation, revision` | `.data.instance`；`plugin_instances.publish` |
| 替换绑定集合 | PUT `/plugin-instances/:id/bindings` | `expected_generation, bindings` | `.data.instance`；`plugin_instances.bindings.update` |
| 停用实例 | POST `/plugin-instances/:id/disable` | `expected_generation` | `.data.instance`；`plugin_instances.disable` |

表中的 `.generation` 指 `.data.instance.generation`。每次写操作之后读取新的 generation，并在下次请求中提交 `expected_generation`。发生 CAS 冲突先刷新，避免用旧值重复覆盖；bindings 是整组替换，提交完整目标集合。写接口会执行 step-up；使用启用 TOTP 的管理员会话先完成二次验证，后台界面会处理该流程。

完整 curl/jq 操作链保留在附录 A.14，包括上传 DTO `.data.id`、实例 DTO `.data.instance` 及任务 DTO `.data.task/.data.run/.data.runs` 的区别。

## 7. 扩展输出检查或替换

- input：Content 为原协议 `application/json`；检查业务正文，不把 model、路由字段、凭据当作消息正文。
- output：Content 为宿主校验完整 JSON/SSE 后聚合的 `text/plain; charset=utf-8`，不是上游 JSON 或原始 SSE 帧。参考 `business-output`。
- 声明输出 hook 后，宿主严格缓冲，审核通过前不发 header/body、不 Flush；这不是逐 token 实时流式审核。
- 决策为 `pass`、`block`、`replace`。replace 需要清单、GetInfo 与 binding 都允许替换；input 替换只改允许的消息正文/指令字段，保持 model、stream、采样、路由、缓存等字段；output 替换返回完整 UTF-8 纯文本。
- 首批支持四种文本协议 ID：`openai.chat_completions.v1`、`openai.responses.v1`、`anthropic.messages.v1`、`gemini.generate_content.v1`。多候选、工具、reasoning、音视频、Realtime 等超出当前文本适配子集时会失败，按目标协议扩展前先对照详细 SDK 与宿主校验。
- input block 发生在上游调用前；output block 已经产生上游用量，当前计费仍走真实 usage 路径。绑定插件的请求绕过既有结果缓存。

## 8. 独立任务开发

参考 `backend/examples/general-plugins/pricing-sync/`、`internal/sample/pricing.go` 及 [general-v1.md](../backend/pkg/pluginapi/docs/general-v1.md)。此类插件声明 `independent.task.v1` 和 `general.tasks`，实现 `RunTask`；不需要分组 hook binding。

任务清单声明所需 host_capabilities，实际资源授权由管理员任务 `scope` 决定。broker 将授权 KV、network、pricing 快照封装进 `TaskRequest.input_json`；插件计算结果/提案，不通过宿主内部数据库实现直接写入。pricing-sync 的离线任务 input 是 `{ "prices": { "model-a": { "input_price": 1.2 } } }`，提交任务定义时不额外套一层 host 或 input。

配置、创建任务、scope 的实际 channel/pricing ID 必须一致。任务接口允许按 pricing_ids 缩小授权；精确字段和非空/空数组的语义见附录 A.14。配置中的规则目标不代替 scope。

开发步骤：写规则与 config schema → 严格 Configure → 解析 broker 信封 → 沿用 ctx → 返回 succeeded/failed 与符合合同的 output_json → 测试无权限、缺数据、null/零基准、越限、取消 → 打包签名 → 创建并发布实例 → `/admin/plugin-tasks` 创建任务 → 手动执行 → `/admin/plugin-runs` 查看记录。

`changed=true` 在纯提案插件中表示有拟更新项，不等于宿主数据库已提交。任务 broker 在事务提交阶段再次检查 scope/claim/字段/数值；网络能力由宿主按 scope 读取，示例插件不主动创建 HTTP 客户端。用 `idempotency_key` 区分重复执行与新业务动作。后台显示普通记录，敏感结果需要独立权限。

## 9. 配置界面与 UI Bridge

本教程和通用打包器是 headless：`ui={}`、`ui_bridge=0`，采用宿主的实例 JSON 编辑器。上传后没有插件独立网页是预期行为。

若开发旧 schema1 的配置网页，使用包内静态 UI + sandbox iframe + [UI Bridge v1](../backend/pkg/pluginapi/docs/ui-bridge.md)，由宿主提供短时资源会话和 bridge token。legacy `config.load/save/test` 与 schema2 instance revision 是不同配置流程。不要把管理员 Token 传给 iframe，也不要在 headless 插件中伪造旧 config API。需要 schema2 自定义 UI 时先确认宿主对该 bridge/实例流程的实际支持，再声明版本和打包资源；本教程不将通用 JSON 编辑器描述成插件网页。

## 10. 排错清单

| 现象 | 检查与处理 |
|---|---|
| 没有插件菜单 | `plugin_management_enabled` 与 `plugins.read/plugin_instances.read/plugin_tasks.read/plugin_runs.read`；菜单开关与运行状态分开 |
| 拒绝未签名包 | 上传 signed 副本；本地开发可明确使用 allow_unsigned，但正式签名路径保持 false |
| 发布者不受信任 | `signature.key_id` 与 `trusted_publishers` 键一致；公钥是原始32-byte Base64；实际config路径和重启是否正确 |
| 签名校验失败 | manifest 原始字节是否被重写；用 signer verify 核对；保留旧包、从新构建产物重新签名 |
| 文件哈希错误 | 二进制/配置/UI改动后重新打包，再签名；不要直接改已签名 ZIP |
| 不兼容/协议握手失败 | 宿主 SemVer 范围、SDK版本、general/transport区别、GetInfo 与清单是否一致 |
| 包安装成功但无检查 | 是否创建实例、发布 revision、启用正确分组/entry/stage/protocol绑定；测试Key的实际分组是否匹配 |
| 有最新配置但行为没变 | 保存只产生新 revision，检查已发布版本，不把 generation 当成 revision |
| `PLUGIN_INSTANCE_CONFLICT` | 刷新实例/generation；检查租约；以最新CAS值提交完整目标操作 |
| output 延迟增加 | 严格缓冲预期；检查 output timeout/大小/并发和2 MiB RPC上限 |
| 子进程马上退出 | runtime操作系统/架构/权限、stdout被日志污染、context/配置错误；查看宿主结构化错误 |
| 编辑页没有已有 JSON | 实例配置只写不回显；保留自己的完整目标配置，不当作空值自动覆盖 |

## 11. 发布、版本与回滚

发布清单：固定代码和SDK revision；插件 SemVer；配置结构版本；规则版本；兼容范围；单元测试；真实包Validate/Install、文件SHA、GetInfo、Configure、Check/RunTask；目标平台执行验证；签名与公钥；完整配置样例；安装/绑定/任务与回滚说明。

`requires.tested_sub2api_versions` 只填真实验证过的版本。编译两平台、直接方法测试、浏览器页面存在，各自不是另外一种验收的替代证据。记录具体命令、输入、输出和退出状态。

实例配置回滚：读取最新 generation → 对保留的旧 revision 执行 publish → 查看 current_revision/status → 用同一业务输入验证旧行为。旧进程和租约按宿主排空，不把停用当作在途请求的立即中断。配置失效则保留旧发布版本。

包升级前保留原 `.s2plugin`、签名、公钥与兼容配置；有实例引用的安装包遵循卸载门禁，先停用/解除引用，再安装目标包并创建兼容实例。任务已提交业务变更的补偿是独立业务动作，不把失败/取消当作自动回滚。

## 12. 文档与工具自检

```sh
# cwd: backend
python -B pkg/pluginapi/docs/quickstart_contract_test.py -v
python -B pkg/pluginapi/docs/docs_contract_test.py -v
go test -count=1 ./examples/hello-input
go test ./scripts/plugin-sign
go vet ./scripts/plugin-sign
```

前两个是离线文档契约测试，不发HTTP、不替代实际安装发布。最小代码须按第3节创建后再运行其go test；尚未创建hello-input时目录不存在属于准备步骤未完成。

### 源码索引

- 协议：[general.proto](../backend/pkg/pluginapi/general/v1/general.proto) / [runtime.go](../backend/pkg/pluginapi/general/v1/runtime.go)
- 真实示例：[examples/general-plugins/README.md](../backend/examples/general-plugins/README.md)
- 包格式与签名：[package-format.md](../backend/pkg/pluginapi/docs/package-format.md)
- SDK/任务信封：[general-v1.md](../backend/pkg/pluginapi/docs/general-v1.md)
- 宿主清单校验：[plugin_manifest.go](../backend/internal/service/plugin_manifest.go)
- 安装与验签：[plugin_package.go](../backend/internal/service/plugin_package.go)
- 实例HTTP：[plugin_instance_handler.go](../backend/internal/handler/admin/plugin_instance_handler.go)
- 任务HTTP：[plugin_task_handler.go](../backend/internal/handler/admin/plugin_task_handler.go)

---

## 附录：旧传输插件及完整合同参考

下方保留旧 schema1 教程、M4 合同与 M7 完整 curl 示例，方便维护已有插件。旧工程的 `tools/keygen` 和 `build.sh` 是外部工程模板，不是本仓库现成命令；新通用插件使用上文实际提供的 scripts/plugin-sign 与 scripts/plugin-package.py。通用插件从上文开始，不使用旧传输 RPC/启用流程。

### 旧教程与进阶合同（参考）

本文面向希望为 Sub2API 开发、打包和发布插件的团队。插件是独立进程和静态 UI 组成的 `.s2plugin` 包，宿主通过稳定的 gRPC 协议调用它。本文以当前宿主已经定义的 `openai.oauth.outbound_transport.v1` 能力作为协议示例，说明开发者需要准备什么、哪些职责属于插件、哪些职责仍由 Sub2API 负责。

schema1 OAuth transport 的说明保留在前半部分；schema2/general.v1 的 M4 合同和 M7 三个可构建安装示例见后半部分。文档不是安装包，真实示例包须由仓库外的打包输出取得。源码示例不等同已发布的官方生产插件。

## 附录 A.1. 准备开发环境

建议使用以下环境：

- 与 backend/go.mod 一致的 Go 工具链（当前 go 1.27.0）；
- Node.js（仅在插件 UI 使用 JavaScript 时需要）；
- Git；
- 与目标部署环境一致的构建工具链。

协议定义和通用说明位于：

- `backend/pkg/pluginapi/v1/plugin.proto`：进程间消息和流式请求定义；
- `backend/pkg/pluginapi/v1/runtime.go`：插件进程启动入口；
- `backend/pkg/pluginapi/v1/manifest.schema.json`：包清单 JSON Schema；
- `backend/pkg/pluginapi/docs/`：开发、UI Bridge、包格式和安全边界说明。

通用插件示例源码已位于 backend/examples/general-plugins/；包含 credential-input、business-output、pricing-sync 和跨平台 package.py。schema1 transport 工程仍可按下文构建，通用插件则参见 SDK general-v1.md 与本文 M7 章节。公开协议以 backend/pkg/pluginapi/ 为准。

## 附录 A.2. 创建插件工程

开发 schema1 transport 插件可以创建独立 Go 工程，目录建议如下：

```text
my-plugin/
├── cmd/<plugin>/main.go
├── internal/pluginconfig/
├── internal/transport/
├── ui/index.html
├── ui/assets/
├── tools/
├── manifest.source.json
└── build.sh
```

开发时至少准备以下部分：

1. `manifest.source.json`：插件 ID、名称、版本、作者、能力和兼容的 Sub2API 版本；
2. `cmd/<plugin>/main.go`：启动入口和运行时版本注入，并同步打包器中的构建目标和二进制名称；
3. `internal/pluginconfig/`：配置结构、默认值、严格校验和规范化；
4. `internal/transport/`：HTTP 客户端、代理、请求头、请求体、网络连接参数、响应流和资源回收；
5. `ui/index.html` 与 `ui/assets/`：插件自己的配置界面；
6. 单元测试、进程集成测试和目标平台构建配置。

入口文件应保持很小，只负责调用 `pluginv1.Serve`。实际逻辑放在可独立测试的包中，避免把配置解析、网络请求和协议组装全部写在 `main.go`。

## 附录 A.3. 编写运行时

运行时实现 `TransportPlugin` 服务，必须满足以下约定：

| 方法 | 要求 |
| --- | --- |
| `GetInfo` | 返回的插件 ID、版本、协议版本、传输 API 版本和能力必须与清单一致。 |
| `Health` | 快速返回进程是否可以接收新请求，不执行长时间网络探测。 |
| `ValidateConfig` | 严格解析 JSON，拒绝未知字段和非法范围，并返回完整的规范化配置。 |
| `ApplyConfig` | 成功后原子切换配置；失败时保留旧配置和旧连接。 |
| `TestConfig` | 针对已保存配置进行快速诊断，返回简短、可展示的结果。 |
| `Forward` | 按协议接收请求流，发出上游请求，再按顺序返回响应流。 |

请求帧顺序为 `start`、零到多个 `body_chunk`、`body_end`；响应帧顺序为 `start`、零到多个 `body_chunk`、`end`。不能继续处理时发送 `error` 帧。

`ForwardResponseError.request_sent` 必须准确：只有在能够确认尚未调用上游 HTTP Transport 时才返回 `false`；一旦已经调用，或无法确认上游是否收到请求，就返回 `true`。宿主会据此决定是否允许切换账号重试，避免重复执行同一个请求。

资源管理也属于运行时契约：复用 HTTP Transport 和连接池，配置切换时关闭旧空闲连接，沿用 gRPC stream 的 context 取消 DNS、连接、上传和响应读取，并始终关闭上游响应体。日志和错误消息不能包含 Token、代理凭据、完整请求体或敏感响应头。

## 附录 A.4. 设计插件配置

插件配置由插件定义，由 Sub2API 加密保存。推荐流程是：

1. 在 `internal/pluginconfig.Config` 中定义字段和默认值；
2. 使用 `json.Decoder.DisallowUnknownFields` 等严格方式解析；
3. 将空对象规范化为完整默认配置；
4. 在 `ValidateConfig` 和 `ApplyConfig` 中复用同一套校验；
5. 配置应用成功后再让宿主保存，保存失败时允许恢复旧配置。

JSON 字段统一使用 `snake_case`。敏感配置不要放入 URL、UI 通知、诊断结果或日志。插件不应从 UI 读取、刷新或持久化 OAuth Token；宿主只在运行时调用需要的网络转发接口。

## 附录 A.5. 实现插件自己的配置 UI

UI 是插件包内的静态页面，不需要修改 Sub2API 前端源码。宿主会在受限 iframe 中加载 `ui/index.html`，并通过 UI Bridge 提供配置读写和测试能力。

页面初始化流程：

1. 加载包内 HTML、CSS 和 JavaScript；
2. 创建 Bridge 并注册 `message` 监听；
3. 发送 `sub2api.plugin.ready`；
4. 调用 `config.load` 渲染表单；
5. 编辑后调用 `config.save`；
6. 测试前先保存，再调用 `config.test`；
7. 页面卸载时调用 `dispose()`。

当前 Bridge 支持：

| 消息 | 用途 |
| --- | --- |
| `config.load` | 读取当前配置。 |
| `config.save` | 提交配置，由运行时校验、应用并加密保存。 |
| `config.test` | 运行已保存配置的诊断。 |
| `ui.resize` | 调整配置 iframe 高度。 |
| `ui.notify` | 显示成功、错误或提示消息。 |

每条消息都必须带 `request_id`，并校验 `event.source`、消息来源标识和 Bridge Token。不要依赖 CDN、远程脚本、Cookie 或本地存储。页面需要兼容窄屏和明暗主题，并正确处理加载、保存、测试、超时和未保存状态。

详细信封格式见 `backend/pkg/pluginapi/docs/ui-bridge.md`。如果后续示例仓库提供可复用的 Bridge SDK，本文会在示例仓库章节补充对应路径和使用方式。

## 附录 A.6. 编写包清单

只维护 `manifest.source.json`，不要手工编辑构建目录中的 `manifest.json`。至少需要填写：

```json
{
  "schema_version": 1,
  "id": "example.openai.transport",
  "name": "Example OpenAI Transport",
  "version": "0.1.0",
  "requires": {
    "sub2api": ">=0.1.179 <0.2.0",
    "recommended_sub2api_version": "0.1.179",
    "tested_sub2api_versions": ["0.1.179"],
    "plugin_protocol": 1,
    "transport_api": 1,
    "ui_bridge": 1
  },
  "capabilities": [
    {
      "id": "openai.oauth.outbound_transport.v1",
      "platform": "openai",
      "account_type": "oauth"
    }
  ],
  "runtimes": {},
  "ui": { "entrypoint": "ui/index.html" },
  "files": {}
}
```

打包器会自动填充目标平台运行时、UI 和运行时文件的 SHA-256。清单中的 `requires.sub2api` 是硬兼容范围；`tested_sub2api_versions` 应只填写真实验证过的版本；`recommended_sub2api_version` 用于管理页面展示。当前宿主仅处理 `openai.oauth.outbound_transport.v1`，声明其他能力不会自动产生新路由。后续增加 Provider 支持时，会在协议、能力清单和宿主路由完成适配后，再补充对应的清单示例。

## 附录 A.7. 生成密钥并签名

生产包应始终签名，宿主默认拒绝未签名包。可以使用插件工程中的密钥生成工具生成一对 Ed25519 密钥；示例仓库发布后会提供标准工具和完整命令：

```bash
go run ./tools/keygen -out build/keys/my-publisher
```

生成的 `my-publisher.private` 只保存在受控的开发机或 CI Secret 中，不能提交到源码仓库、插件包或部署服务器。公钥是 Base64 文本，可以提供给部署者。

插件工程的 `build.sh` 应调用标准打包器。自定义发布者密钥时必须同时提供 `-signing-key` 和 `-key-id`：

```bash
./build.sh \
  -signing-key /安全目录/my-publisher.private \
  -key-id my-publisher-v1 \
  -output dist/my-openai-plugin.s2plugin
```

签名覆盖最终 `manifest.json` 的精确字节；清单中的文件哈希再覆盖运行时和 UI 文件。签名完成后不要重新格式化 `manifest.json`。

部署者在 Sub2API 配置文件中追加公钥：

```yaml
plugins:
  allow_unsigned: false
  trusted_publishers:
    my-publisher-v1: "BASE64_ED25519_PUBLIC_KEY"
```

`trusted_publishers` 是在宿主内置官方公钥之外追加的信任来源，不能覆盖内置公钥。`signature.json` 中的 `key_id` 必须与配置键完全一致。密钥轮换时先发布包含新公钥的宿主配置或版本，再发布新签名包，最后再停用旧密钥。

开发阶段如需使用未签名包，只应在隔离的本地环境临时设置 `plugins.allow_unsigned: true`，测试完成后立即恢复为 `false`。

## 附录 A.8. 构建、测试和安装

在插件目录执行：

```bash
go test ./... -count=1
node --check ui/assets/bridge-v1.js
node --check ui/assets/app.js
./build.sh
unzip -t dist/*.s2plugin
```

回到 Sub2API 仓库根目录后，再使用真实构建包运行宿主集成测试：

```bash
cd ../..
SUB2API_TEST_PLUGIN_PACKAGE=plugins/my-openai-plugin/dist/my-openai-plugin.s2plugin \
  go test ./backend/internal/service -run '^TestPluginRuntimeIntegration$' -count=1
```

最低测试集应覆盖配置默认值和边界值、插件身份、请求和响应分块、流式响应、上下文取消、插件退出、代理开关、包哈希、签名、路径安全、目标平台运行时以及 UI Bridge 的加载、保存、测试、错误和超时。

安装后先保持停用，确认清单兼容性、签名和诊断结果，再按账号灰度启用。API Key 账号和未命中灰度的 OAuth 账号继续走 Sub2API 原有路径。

## 附录 A.9. 发布前检查清单

- 插件版本与 `GetInfo` 返回值一致；
- `requires.sub2api` 覆盖范围经过验证，没有未经测试的破坏性版本；
- `tested_sub2api_versions` 与实际测试记录一致；
- 每个支持的平台和架构都有运行时文件；
- 生产包存在有效 `signature.json`，公钥已交付部署者；
- 包中没有私钥、源映射、测试数据、日志和临时文件；
- UI 不依赖外部资源，也不保存宿主会话信息；
- 配置切换、请求取消、响应关闭和错误重试语义经过测试；
- 发布说明包含升级、停用、回滚和兼容版本信息。

## 附录 A.10. 常见问题

| 现象 | 排查方向 |
| --- | --- |
| 安装提示签名不受信任 | 检查 `signature.json.key_id`、Base64 公钥和配置键是否完全一致。 |
| 插件显示不兼容 | 检查 `requires.sub2api`、`plugin_protocol`、`transport_api` 和 `ui_bridge`。 |
| 插件进程无法启动 | 检查目标系统和架构对应的运行时路径、可执行权限和运行用户权限。 |
| 配置页无法加载 | 检查 `ui.entrypoint`、UI 文件哈希、Bridge Token 校验和 iframe 消息来源。 |
| 保存后配置未生效 | 查看 `ValidateConfig`、`ApplyConfig` 返回的规范化配置和诊断信息。 |
| 请求失败后重复执行 | 检查 `ForwardResponseError.request_sent` 是否准确反映请求是否可能已发出。 |

## 附录 A.11. 需要扩展能力时

如果新插件需要支持其他 Provider、其他账号类型或新的消息字段，应先扩展并版本化公开协议，再由宿主增加能力匹配和生命周期处理。不要仅通过清单声明一个宿主尚未实现的能力。这样可以让旧插件继续运行，也能让新宿主明确拒绝不兼容的插件。

Sub2API 后续会持续补充更多 Provider 的插件适配说明，包括能力标识、请求和响应契约、配置字段、UI Bridge 使用方式、版本兼容要求以及测试清单。本文会随着这些能力的落地继续更新，Provider 专属章节会放在本节之后。

## 附录 A.12. 仓库内通用插件示例

backend/examples/general-plugins/ 提供三个 schema2/general.v1 headless 示例，共享 Go 入口，独立 manifest/config schema，可打包真实 Windows/Linux amd64 runtime。SDK 文档位于 backend/pkg/pluginapi/docs/general-v1.md。外部分发地址和生产签名发布另行管理；本地源码与构建步骤见第 14 节。


## 附录 A.13. 通用请求检查插件（schema2，2026-10-11）

本节是新通用插件路径，前面的 schema1 OAuth 传输插件继续使用原入口。schema2 通过**插件安装 → 实例配置 → 发布版本 → 分组绑定**启用，不通过旧传输插件 Enable 按钮启动。实例可独立配置、发布和回滚；请求在转发前持有 input/output 两阶段租约，因此发布新版本不改变在途请求使用的版本。

### 首批可执行协议

| manifest protocol | 请求入口 | 严格输出 |
| --- | --- | --- |
| `openai.chat_completions.v1` | `POST /v1/chat/completions` | 文本 JSON、SSE |
| `openai.responses.v1` | `POST /v1/responses`、已有 Codex alias | 文本 JSON、SSE |
| `anthropic.messages.v1` | `POST /v1/messages`、已有 Antigravity alias | 文本 JSON、SSE |
| `gemini.generate_content.v1` | `POST /v1beta/models/*:generateContent`、`*:streamGenerateContent` | 文本 JSON、SSE |

这是四种**文本子集**而非上述接口全部能力：单 choice/candidate/output item；文本片段可聚合；SSE 必须有合法结束事件与匹配全文。多候选、工具参数、reasoning/非文本块、音视频、WebSocket/Realtime、failed/incomplete/截断输出会明确失败，不把 EOF 当作检查通过。未绑定插件的原有请求保持原路径，不受这个文本子集限制。规范 SSE 空注释、`: ping`、`: keepalive`、空控制对象或匹配单一 type 的 ping/keepalive，以及 usage 事件可通过；任意带业务 payload 的伪控制事件或注释拒绝。

schema2 清单保留包身份、版本、运行时与文件 SHA 声明。以下字段应合并进完整包清单（不是可直接上传的完整包）：

```json
{
  "schema_version": 2,
  "capabilities": [{"id": "request.hook.v1"}],
  "requires": {
    "plugin_protocol": 1,
    "general_api": 1,
    "transport_api": 0,
    "ui_bridge": 0,
    "sub2api": ">=0.1.0 <1.0.0"
  },
  "general": {
    "config_version": 1,
    "hooks": [{
      "name": "inspect",
      "stages": ["input", "output"],
      "protocols": ["openai.chat_completions.v1"],
      "can_replace": true
    }]
  }
}
```

当前兼容性要求有效且版本匹配的 schema2 清单，所有 hook 协议都必须受支持。独立任务可声明 host.kv.v1、host.network.read.v1 和 host.channel.pricing.read.v1/write.v1，但实际授权由 task scope 决定；未知能力或协议仍受兼容性门禁。外部上游适配服务继续配置为兼容 API 渠道，不纳入本地进程插件管理。

### 请求与替换合同

- input 的 `HookRequest.content` 是 `application/json` 原协议请求体；`replace` 必须返回合法同协议 JSON。只允许修改消息正文/输入指令；model、stream、采样/路由/缓存控制字段与嵌套 cache_control 等保持不变。每次替换都在下一插件执行前验证，后续插件恢复旧字段不等于前一次非法修改有效。
- output 的 content 是 `text/plain; charset=utf-8`，Data 是聚合后的完整 UTF-8 文本，而不是上游 JSON 或 SSE 原帧。output replacement 返回 `text/plain`（可带 UTF-8 charset）完整替换文本；宿主重建合法协议响应，保留 id/model/usage/结束元数据，不回放旧正文、终态正文副本、annotations/logprobs。
- input `block` 在发送上游前返回固定协议错误。插件 rejection message、检查正文、配置和外国错误字符串都不进入客户端错误。
- output 绑定使用严格缓冲：审核前不提交响应 header/body，也不 Flush；仅全部通过后一次提交。超时、断开、超限、协议异常与插件失败都会返回明确错误，不透传未审查内容。严格 SSE 因此不是实时逐帧输出。
- Anthropic 的 invalid-request fallback 和 ClaudeCodeOnly fallback 链在入口预备候选组绑定；实际客户端分类或换组时切换 active chain 并重审输入。候选组存在 input 或 output 绑定时从开始就缓冲，避免早期 keepalive 影响后续拒绝状态。候选绑定准备失败会保守提前拒绝，候选链循环或超过 32 层失败；实际调度分组与已审核组漂移时明确拒绝，不临时发现未经缓冲的输出链。既有收费归属保留原路径，插件检查使用实际选中组。
- 原 `securityaudit` 保持在原网关 handler 的唯一执行位置，看到插件替换后的输入；绑定通用插件时的 HTTP 完成结论绑定 user/key/group/protocol/model/stage/body hash；未绑定的 legacy HTTP/image/media 复用语义不变。fallback 无论正文是否变化都按实际组政策重新判断，不复用原组的完成布尔标记。

### 限额与运行记录

```yaml
plugins:
  gateway_hook_timeout_seconds: 10
  strict_output_timeout_seconds: 120
  strict_output_max_bytes: 8388608
  strict_output_total_bytes: 134217728
  strict_output_max_concurrency: 16
```

插件 gRPC 消息仍有 **2 MiB 上限**，包含 JSON/base64 和契约封装开销，因此可检查的正文通常小于 2 MiB；8 MiB 原始响应缓冲额度不等于可交给插件的 8 MiB 文本。超过运行时消息上限明确失败。

0 使用默认值；调用超时最多 120 秒，严格请求总超时最多 1800 秒；每响应最多 64 MiB、全局已缓冲正文最多 1 GiB、并发最多 1024。有效单响应预算不得超过有效全局预算。全局预算统计已缓存正文，不等同整个进程 RSS（协议解析、复制和替换仍占额外内存）。输入复用网关文本 body 上限，预算在白名单/composite preread 之前安装，原始读取、已读正文和压缩解码共享限额；解码使用 limit+1 检测超限而不是静默截断。绑定请求读体使用实际 HTTP read deadline，并在成功读完后清除，不影响长 SSE 写入。内容发生替换时从接管前独立快照保留宿主可信 CORS/安全头，并重新提交规范 Content-Type/Content-Length；不恢复下游覆盖的策略头，移除旧自定义头、ETag、编码、摘要和 trailer；原样 pass 保持原 header。

任何已绑定插件的请求使结果缓存 **BYPASS / plugin_bound**（读、写均不参与），因此旧 HIT 不继承先前放行结论。未修改 policy v3；未来将绑定与 revision 纳入权威缓存语义后再独立开放缓存。

D1 新收费/退款规则暂缓：output 被阻断时，已有成功上游转发仍按真实 usage 走原记账路径；input 阻断无上游用量。宿主结构化日志 `plugin gateway execution` 包含 request/group/instance/binding/revision/stage/decision/failure/duration，不记录正文、findings、插件 message 或配置。adapter 检查的是正文文本子集，不是对整个响应所有字段的通用脱敏器；保留的 id/model/usage 等 metadata 不进入正文 text 审核。网关日志与独立任务运行记录分开；任务由 M5 /plugin-tasks 管理，普通运行历史隐藏敏感 output。

## 附录 A.14. M7：三个可安装示例、SDK 与执行闭环

### 写集、配置与编译

源码为 backend/examples/general-plugins/。每个插件包含 manifest.source.json、config.schema.json、config.example.json 与 README；共享 cmd/general-plugin 和 internal/sample。SDK 完整规则、信封、错误码、限额与版本说明见 [general-v1.md](../backend/pkg/pluginapi/docs/general-v1.md)。

| 包 | 入口 | 规则 |
| --- | --- | --- |
| credential-input-1.0.0.s2plugin | input hook credential-input | 配置 RE2 正则，匹配凭据样式时 block，不外发 |
| business-output-1.0.0.s2plugin | output hook business-output | 可配置 keywords/weight/threshold，block/pass |
| pricing-sync-1.0.0.s2plugin | 独立 task pricing-sync | 已授权价格与选定网络/离线 prices，规则计算提案 |

```sh
cd backend
python -B examples/general-plugins/package_test.py
python -B examples/general-plugins/package.py --out D:/tmp/sub2api-m5-m7-20261011/examples
python -B examples/general-plugins/package.py --verify D:/tmp/sub2api-m5-m7-20261011/examples/pricing-sync-1.0.0.s2plugin

go test -count=1 ./pkg/pluginapi/... ./examples/general-plugins/internal/sample/...
go test -count=1 ./examples/general-plugins -args -packages=D:/tmp/sub2api-m5-m7-20261011/examples
```

在 Linux 用 /tmp/... 输出路径。默认每包含 runtime/windows_amd64/plugin.exe 与 runtime/linux_amd64/plugin；manifest.runtimes 的 key 则为 windows-amd64/linux-amd64。打包器真实 go build、注入插件名、记录命令、计算 files SHA、解包核对 SHA/PE/ELF，再写 PACKAGES.json。源 manifest 的空 runtimes/files 不是上传包。二进制与临时文件只写到仓库外。

本地包未签名，在开发配置启用 plugins.allow_unsigned=true。生产包按 package-format.md 签 manifest 精确字节并配置 trusted_publishers。不要把开发输出当成生产签名发布。

### 主仓库 PluginManager 安装构造

下面片段在 backend 模块内使用，pluginRepo 与 encryptor 是主线程创建的真实 PG repository/加密服务；先完成数据库迁移。

```go
cfg := &config.Config{Plugins: config.PluginConfig{
    DataDir: "D:/tmp/sub2api-m5-m7-20261011/manager-data",
    AllowUnsigned: true,
    TrustedPublishers: map[string]string{},
    MaxUploadBytes: 64 * 1024 * 1024,
    MaxUncompressedBytes: 128 * 1024 * 1024,
    StartTimeoutSeconds: 10,
}}
manager := service.NewPluginManager(pluginRepo, encryptor, cfg,
    service.PluginHostInfo{Version: "0.1.179"}, nil)
if err := manager.Start(ctx); err != nil { return err }
defer manager.Stop()
f, err := os.Open("D:/tmp/sub2api-m5-m7-20261011/examples/pricing-sync-1.0.0.s2plugin")
if err != nil { return err }
installed, err := manager.Install(ctx, f, nil) // installedBy 可传真实管理员 ID
_ = f.Close()
if err != nil { return err }
if err := installed.Manifest.Validate(); err != nil { return err }
instances := service.NewPluginInstanceService(instanceRepo, encryptor, manager, 10*time.Second)
// 接下来用 instances.Create/SaveConfig/Publish 或对应 HTTP API 创建并发布 revision。
```

NewPluginPackageInstaller.Install 只完成文件/manifest 验证和落盘；manager.Install 还持久化包信息。schema2 不产生 transport 账号 bindings，也不走 manager.Enable/SaveConfig 的旧 transport 接口。

### curl：上传 → 配置版本 → 分组绑定 → 发布 → 检查

示例需要 curl、jq，管理员会话具有对应权限，启用 TOTP step-up 的部署先在同一会话完成 `/api/v1/user/totp/step-up`。不要用 shell `set -x` 打印令牌。BASE、ADMIN_TOKEN、GROUP_ID 和 API_KEY 来自部署环境；没有公开密钥默认值。

```sh
set -eu
: "${BASE:?}" "${ADMIN_TOKEN:?}" "${GROUP_ID:?}" "${API_KEY:?}"
ADMIN="$BASE/api/v1/admin"
PKG_DIR="D:/tmp/sub2api-m5-m7-20261011/examples"
SRC="examples/general-plugins"
# 以下命令 cwd=backend；在 Linux 替换 PKG_DIR。
reply=$(curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
  -F "plugin=@$PKG_DIR/credential-input-1.0.0.s2plugin" "$ADMIN/plugins/upload")
PLUGIN_ID=$(printf '%s' "$reply" | jq -er '.data.id')
reply=$(jq -n --argjson pid "$PLUGIN_ID" '{plugin_id:$pid,name:"credential-check",config:{}}' | \
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data-binary @- "$ADMIN/plugin-instances")
INSTANCE_ID=$(printf '%s' "$reply" | jq -er '.data.instance.id')
GEN=$(printf '%s' "$reply" | jq -er '.data.instance.generation')
reply=$(jq -n --argjson gen "$GEN" --slurpfile cfg "$SRC/credential-input/config.example.json" \
    '{expected_generation:$gen,config:$cfg[0]}' | \
  curl -fsS -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data-binary @- "$ADMIN/plugin-instances/$INSTANCE_ID/config")
GEN=$(printf '%s' "$reply" | jq -er '.data.instance.generation')
REV=$(printf '%s' "$reply" | jq -er '.data.instance.latest_revision')
reply=$(jq -n --argjson gen "$GEN" --argjson iid "$INSTANCE_ID" --argjson gid "$GROUP_ID" \
    '{expected_generation:$gen,bindings:[{instance_id:$iid,group_id:$gid,stage:"input",entry:"credential-input",protocol:"openai.chat_completions.v1",sort_order:10,enabled:true,can_replace:false}]}' | \
  curl -fsS -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data-binary @- "$ADMIN/plugin-instances/$INSTANCE_ID/bindings")
GEN=$(printf '%s' "$reply" | jq -er '.data.instance.generation')
jq -n --argjson gen "$GEN" --argjson rev "$REV" '{expected_generation:$gen,revision:$rev}' | \
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data-binary @- "$ADMIN/plugin-instances/$INSTANCE_ID/publish"
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" "$ADMIN/plugin-instances/$INSTANCE_ID/status"
# 绑定组的真实网关凭据；模型 MODEL 必须在该组可用。
: "${MODEL:?}"
jq -n --arg model "$MODEL" '{model:$model,messages:[{role:"user",content:"sk-abcdefghijklmnop123456"}]}' | \
  curl -sS -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
    --data-binary @- "$BASE/v1/chat/completions"
```

测试 key 是合成标记，不是宿主凭据；输入应在上游调用前被 block。普通文本应 pass。business-output 使用同样安装/配置/发布步骤，绑定 stage=output、entry=business-output；通过可控上游返回包含默认关键词 guaranteed profit 的 JSON/SSE，验证严格缓冲后 block。其他三个 protocol 使用 manifest 的精确 protocol ID 绑定。客户端错误不回显插件 message、规则原文或配置。

### pricing-sync：静态离线与实际网络来源

冻结 config v1（源码 config.example.json）：

```json
{"config_version":1,"source_url":"https://prices.example.test/v1","rules":[{"channel_id":10,"pricing_id":20,"source_key":"model-a","field":"input_price","multiplier":1,"max_price":10,"max_change_ratio":0.5}]}
```

先把 10/20 改为真实授权 channel/pricing ID，安装、创建并发布 pricing-sync 实例；独立 task 不需要 group binding。离线任务定义 input **直接**是 prices，不套 broker 的 host，也不再套第二层 input。PG 当前价格为 1 时预期拟更新为 1.2。

任务 scope 的可选 `pricing_ids` 是正整数 ID 数组：缺省或空数组保留 channel-wide 授权；非空只授权所列精确 pricing 条目，并与 `channel_ids`、`pricing_fields` 同时生效，不扩大原频道/字段授权。价格配置里的 `pricing_id` 只是规则目标，不代替 scope 授权。以下 10/20 与 config 示例保持一致，curl 中 CHANNEL_ID/PRICING_ID 使用真实行 ID：

```json
{"channel_ids":[10],"pricing_ids":[20],"pricing_fields":["input_price"],"network_urls":[],"kv_keys":[]}
```

```sh
: "${PRICING_INSTANCE_ID:?}" "${CHANNEL_ID:?}" "${PRICING_ID:?}"
jq -n --argjson iid "$PRICING_INSTANCE_ID" --argjson cid "$CHANNEL_ID" --argjson pid "$PRICING_ID" \
  '{instance_id:$iid,name:"offline-prices",entry:"pricing-sync",cron:"",enabled:true,timeout_seconds:10,max_retries:0,retry_backoff_seconds:1,input:{prices:{"model-a":{input_price:1.2}}},scope:{channel_ids:[$cid],pricing_ids:[$pid],pricing_fields:["input_price"],network_urls:[],kv_keys:[]}}' | \
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data-binary @- "$ADMIN/plugin-tasks" > /tmp/pricing-task-created.json
TASK_ID=$(jq -er '.data.task.id' /tmp/pricing-task-created.json)
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  --data '{"idempotency_key":"pricing-offline-001"}' "$ADMIN/plugin-tasks/$TASK_ID/run" > /tmp/pricing-task-executed.json
RUN_ID=$(jq -er '.data.run.id' /tmp/pricing-task-executed.json)
RUN_STATUS=$(jq -er '.data.run.status' /tmp/pricing-task-executed.json)
[ "$RUN_STATUS" = "succeeded" ]
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" "$ADMIN/plugin-tasks/$TASK_ID/runs" > /tmp/pricing-task-runs.json
RUN_COUNT=$(jq -er '.data.runs | length' /tmp/pricing-task-runs.json)
jq -e --argjson run_id "$RUN_ID" '.data.runs | any(.id == $run_id and .status == "succeeded")' /tmp/pricing-task-runs.json
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" "$ADMIN/plugin-runs/$RUN_ID" > /tmp/pricing-run.json
RUN_DETAIL_ID=$(jq -er '.data.run.id' /tmp/pricing-run.json)
[ "$RUN_DETAIL_ID" = "$RUN_ID" ]
```

任务创建/Get/Update 响应读取 `data.task`，List 读取 `data.tasks`；执行/单次运行响应读取 `data.run`，运行历史读取 `data.runs`。敏感运行接口单独返回 `data.run` 和 `data.result`，不混入普通历史。安装 upload 的 DTO 仍直接是安装对象，包 ID 读取 `data.id`，不要批量替换成任务 DTO 路径。

重复手动执行使用同一 idempotency_key 验证去重；新的业务更新才用新 key。验证 DB 中对应授权 pricing 行变为 1.2、无其他字段变化；普通运行历史只显示状态和耗时，不输出敏感结果。任务 timeout 与实例调用 deadline 取先到期者。

网络场景将 task.scope.network_urls 设为配置 source_url，源返回 `{"prices":{"model-a":{"input_price":1.2}}}`；broker 根据 URL/DNS/大小/授权策略读取 JSON，再作为 host.network[].body。插件优先该匹配 network body；无匹配条目才用明确 task.input.prices。匹配网络数据不合法时 failed，不静默降级。实际网络读取失败属于 broker 失败，不会因离线输入而忽略该失败。

冻结 broker input/output 及 null 与零值规则详见 SDK。配置只是规则，scope 才是授权；broker 不决定具体价格，插件决定，broker 在提交事务验证权限/字段/有限非负值/fencing。没有数据时 failed 且 output 为空，不设默认价格，不生成随机价格。示例从不读取宿主 Token。

### 配置与包回滚、验证记录

每个实例写请求使用 expected_generation CAS，必须更新为上一次响应的值；更新 config 只保存新 revision，不修改已发布进程。失败发布保持旧已发布 revision。恢复旧配置：读取最新 generation，publish 指定已保存的旧 revision，查询 status/current_revision，并用相同测试请求验证旧行为；旧租约排空后关闭旧进程。

包升级保留原 .s2plugin、原签名、公钥与兼容配置。实例引用的安装包版本不能直接替换时，先按宿主规则停用/解除引用，再安装旧包并重建兼容实例。任务补偿是新的业务动作，不把 ctx 取消或 failed 当作已提交价格的自动回滚。

最低证据：RED 测试、SDK/规则/取消/超限测试、真实双平台 build、包解包 SHA、主仓库 Validate/Install、Hashicorp gRPC GetInfo/Configure/Check/RunTask、PG 生命周期与幂等提交。编译 Linux runtime 不等于在 Linux 上运行；curl 示例是操作步骤，不是已完成浏览器/容器/生产发布的证据。主线程负责记录真实 PG/manager、发布和浏览器环境执行结果。

### M7 文档契约测试（离线）

```sh
# cwd: backend；raw 读取文档 curl/jq 路径并用 JSON DTO fixture 验证，不发请求、不构建、不打包。
python -B pkg/pluginapi/docs/docs_contract_test.py -v
```

覆盖 upload、task 创建、execute、单次 run、runs 数组路径，以及可选 pricing_ids 的精确条目授权说明。包内二进制与既有 .s2plugin 不因这次文档修订而变化。
