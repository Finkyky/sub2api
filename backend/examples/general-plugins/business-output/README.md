# business-output

本例使用 schema2 / general.v1，headless（无 UI）。配置见 config.schema.json 与 config.example.json（包内 sdk/）。

仅声明 output hook。Content 是 M4 完整校验 JSON/SSE 后聚合的 text/plain。每个关键词命中至少一次加一次 weight，跨规则累加，达到 threshold 时 block，否则 pass；不回显关键词或正文。

在 backend 执行：

```sh
python -B examples/general-plugins/package.py --out /tmp/general-plugin-examples
```

Windows 输出可指定 D:/tmp/sub2api-m5-m7-20261011/examples。包包含真实 Windows/Linux amd64 runtime、完整 files SHA、配置 schema。SDK 见 backend/pkg/pluginapi/docs/general-v1.md，安装与执行闭环见 docs/PLUGIN_DEVELOPMENT.md。
