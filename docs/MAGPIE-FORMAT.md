# Magpie 格式（实测）

Magpie 有两处供应商数据，**语义完全不同**，读错一处就会得出相反结论：

| 用途 | 路径 | 密钥 |
|---|---|---|
| 主路径：源配置 | `$XDG_CONFIG_HOME/magpie/providers.json`（缺省 `~/.config/magpie/providers.json`） | `key` **明文** |
| 兜底：运行态投影 | `~/.cache/magpie/providers-state.json` | 仅 `key.masked` 掩码 |

同目录还有两个辅助文件（当前实现未使用，留档备查）：`settings.json`（界面设置、网关端口线索）、`stash.json`（当前选中的 Codex 模型）。

> 早先本文件写过「magpie 未把明文密钥暴露在可读文件里」——那只对运行态投影成立。源配置在 XDG 目录，密钥是明文，见下。

## 主路径：providers.json（实测 54 条）

顶层：`{ providers: [...], noAutoGroups, order, pluginLeft, deleted }`。

条目字段覆盖率实测：`id`(54) `name`(54) `key`(54) `models`(43) `website`(31) `icon`(29) `baseAPI`(28) `pickedFrom`(25) `responses`(24) `off`(23) `chat`(21) `anthropic`(16) `proxy`(10) `noModels`(7) `delisted`(5) `hiddenDrawers`(5) `was`(4) `limitRestOff`(3) `headers`(3) `keys`(2)。

| 字段 | 含义 |
|---|---|
| `id` | 唯一键（`imported-4`、`chatflow-gpt` 这类），即 `sourceKey` |
| `name` | 显示名，可能是中文 |
| `key` | **明文字符串**（实测 67 字符的 `sk-...`） |
| `keys` | 可选的多密钥数组：`[{ key, off }]`；只有 2 条供应商用它 |
| `baseAPI` | 首选协议：`chat` / `responses` / `anthropic` |
| `chat` / `responses` / `anthropic` | 三种协议各自的端点，可能缺失或空串 |
| `models` | **字符串数组**（不是对象数组，与运行态投影不同） |
| `headers` | 自定义请求头；实测 3 条有。**导入时必须一并带上**，否则这类中转站导入后不可用 |
| `off` | `true` 表示该供应商被关闭 → 跳过（Magpie 自己也不把它算可用） |

## 兜底：providers-state.json（实测 51 条）

顶层 `{ version, providers: { providers: [...], presets, gateway, ... } }` —— 注意 `providers.providers` 是**数组**。

条目的密钥只有 `key: {set, masked, optional}` 与 `keyList: [{id, masked, active, on}]`，全是 9 字符掩码；`models` 是 `[{id, name, efforts, images}]` 对象数组。

## 密钥选择规则

1. 条目有 `keys` 字段时**以它为准**：取第一条 `off` 非 true 的 `key`；数组里一条可用都没有就不下发密钥（`hasKey=false`），**不拿顶层 `key` 顶替**——那属于猜。
2. 没有 `keys` 字段时用顶层 `key`。
3. 走兜底文件时不存在明文，一律 `hasKey=false` 且来源标 `keyUnavailable=true`。

## 协议 → GoNavi apiFormat

| magpie baseAPI | GoNavi apiFormat |
|---|---|
| `chat` | `openai` |
| `responses` | `openai-responses` |
| `anthropic` | `anthropic` |

端点选择：优先 `baseAPI` 对应字段；该字段缺失或为空则按 `anthropic → responses → chat` 取第一个非空。原样保留，不做拼接或裁剪。
