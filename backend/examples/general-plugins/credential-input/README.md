# credential-input

本例使用 schema2 / general.v1，headless（无 UI）。配置见 config.schema.json 与 config.example.json（包内 sdk/）。

仅声明 input hook，支持四文本协议规范 JSON。可配置 RE2 正则匹配业务文本，命中 block，否则 pass；不回传原文、不联网。默认 API key、Bearer 与私钥标记规则仅为可定制检测示例。

在 backend 执行：

```sh
python -B examples/general-plugins/package.py --out /tmp/general-plugin-examples
```

Windows 输出可指定 D:/tmp/sub2api-m5-m7-20261011/examples。包包含真实 Windows/Linux amd64 runtime、完整 files SHA、配置 schema。SDK 见 backend/pkg/pluginapi/docs/general-v1.md，安装与执行闭环见 docs/PLUGIN_DEVELOPMENT.md。
