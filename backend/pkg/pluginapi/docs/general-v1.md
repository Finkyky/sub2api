# General Plugin SDK v1：schema2、hook 与独立 task

本文依据本仓库 `general/v1/general.proto`、`general/v1/runtime.go`、`contracts/v1`、宿主 manifest/wire 校验、M4 protocol funcs 和 M5 task broker。schema1 transport-v1 与 schema2 general-v1 分开使用，禁止混合方法、cookie、capability 或配置流程。

## 1. 三个真实示例与构建

源码：`backend/examples/general-plugins/`。共享 `cmd/general-plugin` 入口，通过 `-ldflags "-X main.plugin=<name>"` 选择插件。每个插件有独立 manifest.source.json、config.schema.json、config.example.json、README；GetInfo 读取对应嵌入的源声明。

| 示例 | capability / entry | stage | host_capabilities | 规则与结果 |
| --- | --- | --- | --- | --- |
| credential-input | request.hook.v1 / credential-input | input | 无 | 正则检查业务文本，block/pass |
| business-output | request.hook.v1 / business-output | output | 无 | 关键词加权与阈值，block/pass |
| pricing-sync | independent.task.v1 / pricing-sync | 无 | pricing read/write、network read | 规则计算授权价格提案，succeeded/failed |

三个示例都为 headless，没有 replace、UI、transport/outbound、账号目录或凭据 RPC。pricing-sync 不使用 KV、不创建 HTTP 客户端；只有宿主 broker 根据任务 scope 读取网络和价格。

```sh
# cwd: backend；Go 版本遵循 backend/go.mod（当前 go 1.27.0）
python -B examples/general-plugins/package_test.py
python -B examples/general-plugins/package.py --out /tmp/general-plugin-examples
python -B examples/general-plugins/package.py --verify /tmp/general-plugin-examples/credential-input-1.0.0.s2plugin

go test -count=1 ./pkg/pluginapi/... ./examples/general-plugins/internal/sample/...
# 主仓库安装器、manifest Validate、解包 SHA 与真实 Hashicorp gRPC 子进程测试
# 该测试引用 internal/service，要求主仓库服务包能够编译。
go test -count=1 ./examples/general-plugins -args -packages=/tmp/general-plugin-examples
```

Windows 输出目录示例：`D:/tmp/sub2api-m5-m7-20261011/examples`。打包脚本只依赖 Python 标准库和 Go，继承 GOMODCACHE/GOCACHE/GOTMPDIR，强制 CGO_ENABLED=0，默认交叉编译 Windows/Linux amd64。临时 build 文件只进入输出目录，仓库内输出被拒绝。`--targets windows-amd64` 或 `linux-amd64` 可以单平台构建；双平台安装集成测试要求默认两个目标。

## 2. schema2 与版本兼容

manifest.source.json 是含空 runtimes/files 的**构建模板，不是安装包**。package.py 生成完整 manifest.json：填入真实 runtime 路径及每个普通文件的小写 SHA-256。配置 schema 是包内 sdk/config.schema.json，不是 manifest 的额外字段。

```json
{
  "schema_version": 2,
  "id": "examples.sub2api.pricing-sync",
  "name": "pricing-sync",
  "version": "1.0.0",
  "requires": {"sub2api": ">=0.1.0 <1.0.0", "plugin_protocol": 1, "general_api": 1, "transport_api": 0, "ui_bridge": 0},
  "capabilities": [{"id": "independent.task.v1"}],
  "general": {
    "config_version": 1,
    "tasks": [{"name": "pricing-sync"}],
    "host_capabilities": ["host.channel.pricing.read.v1", "host.channel.pricing.write.v1", "host.network.read.v1"]
  },
  "runtimes": {
    "windows-amd64": {"path": "runtime/windows_amd64/plugin.exe"},
    "linux-amd64": {"path": "runtime/linux_amd64/plugin"}
  },
  "ui": {},
  "files": {"runtime/windows_amd64/plugin.exe": "<实际64位小写SHA256>", "runtime/linux_amd64/plugin": "<实际64位小写SHA256>"}
}
```

files 中占位符仅为说明；实际包还包含 README、两个配置 JSON，必须由脚本计算所有 SHA。runtime **key 使用连字符** windows-amd64/linux-amd64，包内目录名可以用下划线。headless 的 ui.entrypoint 为空且 ui_bridge=0；可选 UI 仍遵循 ui/ 哈希与 UI Bridge v1。

- version 是插件 SemVer；general.config_version 是配置解释版本，不是实例 revision。
- requires.plugin_protocol/general_api、配置版本、capabilities、host_capabilities、hooks 的 entry/stage/protocol/can_replace、tasks 的 entry，必须与 GetInfo 严格一致。
- transport_api 必须为 0。legacy `pkg/pluginapi/v1/manifest.schema.json` 只针对 schema1；schema2 最终依据 `PluginManifest.Validate` 与运行时握手。
- requires.sub2api 是宿主版本范围，不代表范围内每版都已验证；示例没有未经验证的 tested_sub2api_versions 声明。
- 配置解释不兼容时升级 config_version 和 schema，并设计迁移。新 revision 创建新进程，旧进程持有旧配置直到租约排空。

## 3. 运行时与不可变配置

入口模式：

```go
package main
import general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
// 实现 GetInfo、Configure、Check 和/或 RunTask；完整实现见 internal/sample。
func main() { general.Serve(newPlugin()) }
```

general.Serve 使用 Hashicorp go-plugin gRPC，cookie 为 SUB2API_GENERAL_PLUGIN_COOKIE=sub2api-general-v1，插件名 general_plugin。不要向 stdout 打印业务日志，避免污染启动握手。

Configure 接收正的 instance_id/config_revision、与 manifest 相同的 config_version、对象 config_json。成功响应 accepted=true，并回显同一 instance/revision。示例成功后冻结配置，后续 Configure 返回 FailedPrecondition；失败不发布新状态。每次 Check/RunTask 校验 binding。GetInfo 深复制，配置通过 RWMutex 保护。

`{}` 使用 config.example.json 的完整默认值；显式顶层字段覆盖默认值，显式 rules **整组替换**，每条 rule 的必填字段不从旧规则继承。config_version 可省略使用 1，显式值必须为 1。拒绝未知字段、重复 JSON key、尾随 JSON、null 配置字段、非法 UTF-8、不配对 Unicode escape、深度超限、非法范围和重复规则。规则 id/version 只放短标识符，不放原文或凭据。

- credential：id/version/RE2 pattern；最多 32 条，pattern <=256 字节，禁止匹配空串。默认匹配 sk-key、Bearer、私钥起始标记，是可配置检测示例，不代表完整凭据识别器。
- business：id/version/keywords/weight；每个关键词出现至少一次加一次 weight，重复出现不重复计分，忽略大小写，跨规则累加，score >= threshold 时 block。weight/threshold 在 (0,1e6]；32 条规则、每条 32 个关键词、每个 128 字节。
- pricing：规则中的 target/field/URL 不是授权，只能与 host.pricing 和宿主 task scope 相交。

## 4. M4 四文本协议 Content

| protocol | input application/json 中检查的文本 | output |
| --- | --- | --- |
| openai.chat_completions.v1 | messages[].content 字符串或 text blocks | 聚合 UTF-8 文本 |
| openai.responses.v1 | input 字符串或 message content；instructions | 聚合 UTF-8 文本 |
| anthropic.messages.v1 | messages[].content；system | 聚合 UTF-8 文本 |
| gemini.generate_content.v1 | contents[].parts[].text；systemInstruction.parts[].text | 聚合 UTF-8 文本 |

input Data 是原协议 JSON 请求，M4 已校验文本子集；示例只扫描正文/指令，不扫描 model、routing、metadata、headers。output 的上游 JSON/SSE 由 `PluginGatewayOutputContent` 严格校验完整终态后规范化成 `text/plain; charset=utf-8`，不是 raw JSON/SSE。分散的关键词片段因此一起受检。未知协议、媒体类型错误和直接传入 text/event-stream 均失败。

HookResponse 合同（本地可调用 contracts/v1 验证）：

- pass：无 Content/Rejection，可有 Findings。
- block：无 Content，有固定 code/message；不回显原文、关键词、正则或 Token。
- replace：要求 manifest 与 binding can_replace=true，必须有 Content；三个示例不声明 replace。input 为同协议 JSON，output 为 text/plain 全文；宿主再次校验不能篡改 model、路由、stream、缓存等控制字段。

confidence（如使用）在 [0,1]，缺失与显式零区分；它是审计元数据，不替宿主决定 action。decision 在插件，宿主负责授权和合同验证。

## 5. 冻结 broker envelope、离线与网络价格

TaskRequest.input_json 是 broker 拼装的信封，不是原 task.input：

```json
{
  "input": {"prices": {"model-a": {"input_price": 1.2}}},
  "host": {
    "kv": {},
    "network": [],
    "pricing": [{"channel_id":10,"pricing_id":20,"fields":{"input_price":1,"output_price":null}}]
  }
}
```

网络源的 network 条目：

```json
[{"url":"https://prices.example.test/v1","body":{"prices":{"model-a":{"input_price":1.2}}}}]
```

body 是 JSON 对象，不是 JSON 字符串。prices 固定为 `map<source_key,map<field,number|null>>`。离线源明确放 task.input.prices。插件优先选择唯一匹配 source_url 的 network body；**只有匹配条目不存在时**才使用 input.prices。匹配网络数据重复/非法/缺失时 failed，不静默回退静态价格。input 不控制规则、目标、URL 或 host 授权。

冻结配置（config.example.json）：

```json
{
  "config_version":1,
  "source_url":"https://prices.example.test/v1",
  "rules":[{"channel_id":10,"pricing_id":20,"source_key":"model-a","field":"input_price","multiplier":1,"max_price":10,"max_change_ratio":0.5}]
}
```

PG integration 用真实 channel/pricing 行 ID 替换 10/20。源码另提供 task.input.example.json（原任务输入）与 task.envelope.example.json（完整信封）。host.pricing fields 的 null 是缺失基准，不是零。

对应 task.scope（不是 config，也不加进冻结 broker envelope）：

```json
{"channel_ids":[10],"pricing_ids":[20],"pricing_fields":["input_price"],"network_urls":[],"kv_keys":[]}
```

`pricing_ids` 是可选正整数 ID 数组：缺省或空数组保留 channel-wide 授权，非空限制为所列精确 pricing 条目；仍与 channel_ids/pricing_fields 同时生效，不扩大原频道/字段授权。config.rules[].pricing_id 只定义插件目标，宿主 Prepare/Commit 根据 scope 再验证，规则不授予权限。

规则在插件内计算 next=selected_price*multiplier，要求有限非负、next <= max_price，且基准>0 时 abs(next-current)/current <= max_change_ratio。相同价格成功 no-op；null 基准失败；零基准的非零变动失败；缺数据不编造默认价格。multiplier/max_price 在 (0,1e6]，max_change_ratio 在 [0,100]；规则 <=32，重复目标+字段拒绝。source_url 为 HTTPS，无 userinfo/query/fragment，<=512 字节，仅作快照选择键，不在插件中请求。

与 M5 broker 支持字段一致：input_price、output_price、cache_write_price、cache_read_price、per_request_price。

成功 OutputJson：

```json
{"summary":"Pricing rules evaluated.","price_updates":[{"channel_id":10,"pricing_id":20,"fields":{"input_price":1.2}}],"kv_updates":[]}
```

status=succeeded、changed=true、error_code 为空。no-op 返回空数组、changed=false。changed 在纯提案示例中表示有拟更新项，不等于 DB 已提交。任何规则失败则 status=failed、changed=false、固定 error_code、无 output_json；全部通过才输出提案，避免局部更新。

通用 output 合同：`{summary?:string,price_updates:[{channel_id,pricing_id,fields:map<string,float64>}],kv_updates:[{key,value:any,delete?:bool}]}`。本例无 KV 声明，kv_updates 始终为空。其他插件使用 KV 时须声明 host.kv.v1，并获得 scope.kv_keys 授权；delete=true 才删除。

manifest 仅为能力请求声明。管理员 scope 指定 channel_ids、可选 pricing_ids、pricing_fields、network_urls、kv_keys；broker Prepare 仅读授权快照并保存 fenced claim；插件决定价格；broker Commit 再验证目标/字段/数值/形状/claim，与执行结果在宿主事务中提交。缓存失效属于宿主 AfterCommit。插件 succeeded 不等于事务成功。

## 6. 错误、取消、超时与限额

| 分类 | 结果 |
| --- | --- |
| 非法 config/JSON/media/stage/protocol/entry | InvalidArgument：invalid plugin input |
| 未配置、binding 不符、重配置 | FailedPrecondition：固定消息 |
| 未声明的方法 | Unimplemented |
| 超限 | ResourceExhausted：plugin size limit exceeded |
| context 取消或截止 | Canceled / DeadlineExceeded |
| 源缺失或重复 | failed：pricing_source_missing / pricing_source_ambiguous |
| prices 缺失或非法 | pricing_data_missing / pricing_data_invalid |
| 目标或字段未授权 | pricing_target_not_authorized / pricing_field_not_authorized |
| 基准缺失、绝对价格或相对变动越限 | pricing_baseline_missing / pricing_limit_exceeded / pricing_change_exceeded |

示例 Content <=1 MiB；config JSON 与 task input/output <=64 KiB；general RPC 消息 <=2 MiB。JSON 深度 <=64；task network <=32、pricing <=256、KV <=128、prices models <=256、授权 pricing fields <=32。宿主还可能有更严格的 scope/上传/调用限额，取严格一方。原始 M4 缓冲额度不是可送入插件的文本额度。

沿用 RPC ctx；JSON 解析、规则循环和返回提案前检查取消。任务 timeout 与实例调用 timeout 取先到期者。宿主取消后退休隔离进程、释放租约；取消返回不证明自定义插件外部副作用回滚。本例只计算纯提案，不自行提交价格或 KV。

示例没有业务日志：不打印 config/content/envelope/network body/HTTP headers/URL 凭据/KV。错误固定，findings 只包含短 id/version/category；普通 run/history 不回显敏感 OutputJson，敏感结果单独加密授权读取。独立进程不是 OS 沙箱，general runtime 使用 SkipHostEnv=true；示例没有 getenv、账号读取、密钥文件读取或主动网络代码。部署仍需受控文件权限与出站网络。

## 7. 安装、配置、发布、执行与回滚

完整 curl 与 PluginManager 构造见仓库 docs/PLUGIN_DEVELOPMENT.md 的 M7 章节。实例写操作必须读取每次响应的新 generation；publish 指定 revision。任务创建只提交业务 input.prices，不提交 host。失败发布保持旧 revision；回滚重新发布已保存旧 revision；迁移或版本替换必须保持包/配置兼容。

可复用开发提示词：

```text
基于 backend/examples/general-plugins 创建 schema2/general.v1 headless 插件。
先写失败的规则/配置/取消/超限测试，再实现；GetInfo 从 manifest 声明生成。
Configure 冻结 instance/revision/config_version；四文本协议使用 input JSON 或 M4 聚合 output text/plain。
Task 只消费冻结 {input,host:{kv,network,pricing}}；来源选择、计算和 action 在插件，broker 只验证授权。
无宿主凭据读取；错误固定，日志/findings/output 不回显秘密；没有数据 failed，不编造价格。
跨编译真实 Windows/Linux runtime，files 全部 SHA，包放仓库外。
运行主仓库安装器、Validate、解包 SHA、真实 Hashicorp gRPC 和 SDK 测试，记录命令/输入/输出/退出码。
发布、PG、浏览器和容器验证分别记录实际执行证据，不将编译替代环境验证。
```

### 离线文档契约测试

在 backend 执行 `python -B pkg/pluginapi/docs/docs_contract_test.py -v`。测试 raw 读取开发文档的 curl/jq 路径，并用 task/run/runs JSON DTO fixture 验证；同时检查 SDK scope.pricing_ids 例子与授权说明。不执行 HTTP、Go 编译或包构建。
