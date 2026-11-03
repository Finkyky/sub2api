# M7 general.v1 示例插件

三个独立 schema2/headless 包：credential-input（输入凭据样式阻断）、business-output（输出关键词/阈值）、pricing-sync（独立价格同步提案）。共享 Go server 与命令入口，GetInfo 从各自嵌入 manifest 生成；没有宿主凭据、主动 HTTP 或秘密日志。

## 构建与验证

在 backend 目录运行：

```sh
python -B examples/general-plugins/package_test.py
python -B examples/general-plugins/package.py --out D:/tmp/sub2api-m5-m7-20261011/examples
go test -count=1 ./pkg/pluginapi/... ./examples/general-plugins/internal/sample/...
go test -count=1 ./examples/general-plugins -args -packages=D:/tmp/sub2api-m5-m7-20261011/examples
```

跨平台输出可换 /tmp/...。默认每包包含真实 Windows/Linux amd64 binary，SHA 声明含 runtime、README、配置 schema/example。package.py 只用 Python 标准库和 Go，禁止把二进制放回仓库，PACKAGES.json 给出包 SHA。此为 unsigned 开发构建，本地 allow_unsigned=true，生产另签名。

源码 manifest.source.json 中空 runtimes/files 是模板，不是合法安装包；打包后由主仓库 PluginManifest.Validate 与 PluginPackageInstaller.Install 校验。sdk_test.go 使用真实包和 Hashicorp gRPC，不以 mock 响应替代进程。

配置可用空对象取默认值，规则整组替换且版本为 1。pricing-sync 支持明确的 task.input.prices 离线输入；有匹配 source_url 的 host.network body 时优先网络，匹配但非法则 failed。完整信封样例见 pricing-sync/task.envelope.example.json。

SDK：[general-v1.md](../../pkg/pluginapi/docs/general-v1.md)。安装、配置、绑定、发布、任务执行与回滚：[PLUGIN_DEVELOPMENT.md](../../../docs/PLUGIN_DEVELOPMENT.md)。
