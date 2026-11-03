# pricing-sync

本例使用 schema2 / general.v1，headless（无 UI）。配置见 config.schema.json 与 config.example.json（包内 sdk/）。

仅声明独立 task 与 pricing read/write、network read。唯一匹配 source_url 的 host.network body 优先；无匹配条目时用显式 task.input.prices。匹配网络数据非法时 failed，不静默回退。prices 为 model -> field -> number。配置规则使用 multiplier、max_price、max_change_ratio 向授权 channel_id/pricing_id/field 提案。缺数据、未授权、null/零变动基准或越限时 failed，无更新输出。无 KV、无自主 HTTP、无宿主凭据读取。

在 backend 执行：

```sh
python -B examples/general-plugins/package.py --out /tmp/general-plugin-examples
```

Windows 输出可指定 D:/tmp/sub2api-m5-m7-20261011/examples。包包含真实 Windows/Linux amd64 runtime、完整 files SHA、配置 schema。SDK 见 backend/pkg/pluginapi/docs/general-v1.md，安装与执行闭环见 docs/PLUGIN_DEVELOPMENT.md。
