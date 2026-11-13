# 接口契约 v1（冻结，改动即重新派单）

本文件是「GoNavi AI 配置导入」插件的接口唯一定义源。插件后端、插件页、GoNavi 主仓能力三方全部以本文件为准。
任何三条线之间的字段/命名改动必须先改本文件并重新派单。

## 1. 交付物与边界

| 线 | 仓库 | 负责文件 |
|---|---|---|
| 主仓能力（Lead 负责） | `E:\code\GoNavi` | `internal/app/plugin_capability_ai_import*.go`、`internal/plugin/pages.go`（权限常量）、`internal/app/app.go`（seam 字段）、`internal/app/plugin_capabilities.go`（注册）、`cmd/wails3app/main.go`、`main.go`、`main_bindings.go`、`internal/webserver/server_runtime.go`（seam 接线）、`internal/plugin/permissions_contract_test.go`（权限门禁） |
| 公开权限词表 | `E:\code\GoNavi-Plugin-Market` | `docs/plugins/permissions.md`（`host.ai.providers.write` 等一行；权限门禁按仓库并排检出比对） |
| 插件后端（子代理 A） | `E:\code\plugin\gonavi-ai-config` | `gonavi.plugin.json`、`go.mod`/`go.sum`、`cmd/plugin/**`、`internal/**`、`scripts/**`、`testdata/**` |
| 插件页（子代理 B） | `E:\code\plugin\gonavi-ai-config` | `frontend/**` |

插件 ID：`gh-autumnnazi.ai-config`（远程为 github.com/AutumnNazi/gonavi-ai-config，沿用 `gh-autumnnazi.ssh` 命名）。

## 2. 主仓能力：`ai.providers.import`

- 权限名：`host.ai.providers.write`
- 通道：page | window | backend；运行态：desktop | web；`Mutating: true`
- 上限：MaxArgsBytes 256 KiB、MaxResultBytes 64 KiB、Timeout 120s；单次最多 100 条
- 语义：把结构化供应商写入宿主供应商列表，写入路径必须走**活动 Service 的 AISaveProvider**（保证内存列表与 `ai_config.json` 同步），密钥由宿主 secret 存储落盘

请求（capability arguments）：

```json
{
  "providers": [
    {
      "name": "千刀",
      "baseUrl": "https://api.zzzcoding.org",
      "apiFormat": "anthropic",
      "model": "claude-opus-5",
      "models": ["claude-opus-5", "claude-opus-5[1M]"],
      "apiKey": "sk-...",
      "headers": { "x-extra": "v" },
      "temperature": 0.7,
      "overwrite": false
    }
  ]
}
```

字段约束：

| 字段 | 必填 | 约束 |
|---|---|---|
| name | 是 | trim 后 1..128 |
| baseUrl | 是 | 1..512，必须 http/https，且去凭据/去 fragment |
| apiFormat | 是 | 枚举：openai、openai-responses、anthropic、gemini |
| model | 否 | 0..200 |
| models | 否 | 最多 64 项，每项 0..200 |
| apiKey | 否 | 0..4096 |
| headers | 否 | 最多 16 组，键值各 0..256；**超过 16 组按该条失败**（reason `too_many_headers`），不截断 |
| temperature | 否 | 0..2 |
| overwrite | 否 | 默认 false |

宿主落库规则：`type="custom"`、`authMode="api-key"`、`apiFormat` 取入参；`ID` 为空时由 `AISaveProvider` 生成（`provider-xxxxxxxx`）。

去重身份（宿主侧防御性判重）：`(lower(trim(name)), baseUrl 去凭据/去 fragment、去掉尾部 "/" 后小写)`。去凭据是为了对齐导入侧（导入地址落库前已去凭据），否则既有供应商地址里带凭据时会被判成两条。
命中且 `overwrite=false` → `skipped`，reason `duplicate`；命中且 `overwrite=true` → 复用原 ID 就地更新，`imported`。

返回（capability data）：

```json
{
  "imported": 2,
  "skipped": 1,
  "failed": 0,
  "items": [
    { "index": 0, "name": "千刀", "status": "imported", "providerId": "provider-8b190256", "reason": "" },
    { "index": 1, "name": "百倍", "status": "skipped", "providerId": "", "reason": "duplicate" }
  ]
}
```

status 枚举：`imported` | `skipped` | `failed`；reason 枚举：`duplicate` | `invalid_name` | `invalid_base_url` | `invalid_api_format` | `invalid_api_key` | `too_many_headers` | `save_failed`。

（本枚举是**宿主人参**返回的取值，插件不得自造；插件侧自己的 `source_missing` 见第 4 节。）

（`invalid_name` 为 2026-11 补充：条目名 trim 后为空或超过 128 字符时逐条记失败，不影响同批其他条目。消费方必须对未知 reason 回退到通用文案，不要用穷举 switch。）

（`too_many_headers` 为 2026-11 补充：自定义请求头超过 16 组时该条失败。以前是静默保留前 16 组——头常承载鉴权，丢掉一个会产出一条看起来正常、实际调不通的供应商，且不留任何痕迹。）

失败返回宿主统一信封 `{"ok":false,"error":{"code":"..."}}`，code 枚举：`invalid_arguments` | `permission_denied` | `too_many_providers` | `internal` | `unknown_method`。
单条失败不得中断整批：逐条落库，逐条记 status。

（`unknown_method` 为 2026-11 补充：宿主没有注册 `ai.providers.import` 时返回它——典型原因是宿主的正式版还没带这个能力。
页面必须把它翻译成「宿主版本不支持导入、请升级」并保留重试入口；落到「未识别的原因」等于把版本问题说成插件故障。）

## 3. 插件清单（`gonavi.plugin.json`，子代理 A 产出，字段冻结）

```json
{
  "schemaVersion": 1,
  "apiVersion": 1,
  "id": "gh-autumnnazi.ai-config",
  "name": "AI 配置导入",
  "version": "0.1.0",
  "minHostVersion": "1.2.3",
  "description": "把 cc switch / magpie 等 AI 工具的供应商配置导入 GoNavi AI 供应商列表。",
  "author": "AutumnNazi",
  "license": "MIT",
  "permissions": ["ui.pages", "ui.html-panel", "host.ai.providers.read", "host.ai.providers.write"],
  "permissionNotes": {
    "ui.pages": "在主窗口工具栏插件菜单里加入「AI 配置导入」入口，并以标签页打开导入页。",
    "ui.html-panel": "导入页是自定义 HTML 页面，页面文档内联在清单里，需要该权限才能在沙箱 iframe 中渲染。",
    "host.ai.providers.read": "只读现有 AI 供应商的名称与地址元数据，用于标记重复项，不读取任何密钥。",
    "host.ai.providers.write": "把用户勾选的供应商写入 GoNavi AI 供应商列表；不读取、不回显任何既有密钥。"
  },
  "backend": {
    "platforms": {
      "windows/amd64": { "executable": "bin/gonavi-ai-config-windows-amd64.exe" }
    }
  },
  "contributes": {
    "commands": [
      { "id": "sources.scan", "title": "扫描本机 AI 工具配置，返回可选供应商（不含密钥）", "effect": "read", "inputSchema": { "type": "object", "additionalProperties": false, "properties": { "sources": { "type": "array", "items": { "type": "string", "enum": ["cc-switch", "magpie", "ccgui", "opencode", "mimocode"] }, "maxItems": 5 } } } },
      { "id": "import.apply", "title": "把勾选的供应商导入 GoNavi AI 供应商列表", "effect": "write", "inputSchema": { "type": "object", "additionalProperties": false, "required": ["items"], "properties": { "items": { "type": "array", "minItems": 1, "maxItems": 100, "items": { "type": "object", "additionalProperties": false, "required": ["sourceId", "sourceKey"], "properties": { "sourceId": { "type": "string", "enum": ["cc-switch", "magpie", "ccgui", "opencode", "mimocode"] }, "sourceKey": { "type": "string", "maxLength": 200 }, "overwrite": { "type": "boolean" } } } } } } }
    ],
    "pages": [
      { "id": "ai-config-import", "title": "AI 配置导入", "html": { "source": "<构建期把 frontend/index.html 的完整文本内联到这里>" } }
    ],
    "navigation": [
      { "id": "ai-config-import", "title": "AI 配置导入", "target": "page", "page": "ai-config-import", "location": "tools", "order": 20 }
    ]
  }
}
```

### 3.1 清单里的 `html.source` 是内联文本（v1.1 修正）

`pages[].html.source` 不是文件路径，而是**内联的 HTML 文档文本**：`internal/plugin/manifest_ui.go:20-31` 只做非空、UTF-8 与 256 KiB 上限校验；`frontend/src/pluginPages/PluginPageHost.tsx:41` 把它直接交给页面渲染器。对照实现见 `E:\code\plugin\gonavi-tools\scripts\build.mjs:65`（`html: { source: page }`，`page` 是文件内容字符串）。

自定义 HTML 页还必须声明 `ui.html-panel` 权限，否则清单校验直接拒绝（`internal/plugin/pages.go:99-100`）。

因此仓库根的 `gonavi.plugin.json` 只是**模板**，可安装包里的清单由 `scripts/build.mjs` 在构建期把 `frontend/index.html` 的内容写进 `html.source`；包内不需要、也不应该再放一份 `frontend/` 目录（页面测试与截图更不该进包）。

### 3.2 版本声明（v1.3 修正：宿主下限写实）

- 插件 `version` 是朴素的 `0.1.0`：宿主与 SDK 的清单校验要求核心版本是 `X.Y.Z` 三段数字（`internal/plugin/manifest.go:239-241`、`sdk/go/manifest_validation.go:869`），预发布后缀合法但会带来「0.1.0-dev 与 0.1.0 谁更新」这类与功能无关的排序问题，而版本号在插件侧表达「还在改」的作用有限，所以不再加后缀。
- `minHostVersion` 是 **`1.2.3`**（承载 `ai.providers.read` / `ai.providers.import` 两个能力的下一个发布版本）。原值 `1.2.0` 是假声明：它比任何已发布宿主都低，于是装得上、扫描正常、点导入才失败。
  宿主按 semver 比较下限；而 `dev`/`dev-*`/`0.0.*`/含 `-dev`、`-test`、`-snapshot` 的**开发版宿主对任何下限都放行**（`internal/buildversion/version.go:14-51`），因此本地 dev 构建（`AppVersion=dev-local`）不受影响，正式 1.2.2 及更早的宿主会在安装时就被明确拒绝——这正是想要的行为。
  另：宿主清单校验里 `minHostVersion` 必须写成三段数字（`dev` 这种整段写法会直接判非法、装不上），这也是「dev 只能挂后缀」这条结论的来源。
- 页面仍把宿主回执的 `unknown_method` 翻成「宿主版本不支持、请升级」，用于兜住「下限被绕过」（两侧同时改过版本、或用户手工装包）的情况。

### 3.3 两条宿主权限都是窄口径（v1.3 修正：不再借数据库元数据权限）

本插件用两条宿主权限：`host.ai.providers.read`（读供应商名称/地址做判重）、`host.ai.providers.write`（写入勾选条目）。

早先判重借的是 `host.metadata.read`——它的公开语义是「读数据库的库/表/列元数据」，一个 AI 配置插件为此申请数据库元数据权限属于过度授权（宿主的只读能力 `ai.providers` 就是绑在它上面的历史包袱）。
主仓因此新增只读能力 `ai.providers.read` + 权限 `host.ai.providers.read`；旧的 `ai.providers` 能力与其权限绑定原样保留，不打断已有插件。

## 4. 插件命令协议（子代理 A 实现，子代理 B 消费）

### `sources.scan`（read）

入参：`{ "sources": ["cc-switch","magpie","ccgui","opencode","mimocode"] }`（缺省 = 五个都扫；最多 5 个）

返回：

```json
{
  "sources": [
    { "id": "cc-switch", "label": "CC Switch", "detected": true, "path": "C:\\Users\\cky\\.cc-switch\\cc-switch.db", "count": 90, "error": "", "skipped": 3 },
    { "id": "magpie", "label": "Magpie", "detected": true, "path": "C:\\Users\\cky\\.config\\magpie\\providers.json", "count": 23, "error": "", "skipped": 7, "disabled": 24 }
  ],
  "items": [
    {
      "sourceId": "cc-switch",
      "sourceKey": "claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e",
      "name": "千刀",
      "baseUrl": "https://api.zzzcoding.org",
      "apiFormat": "anthropic",
      "model": "claude-opus-5",
      "models": ["claude-opus-5"],
      "hasKey": true,
      "appType": "claude",
      "current": false,
      "conflict": false
    }
  ]
}
```

硬约束：

1. **返回体里绝不能出现密钥**：不得有 apiKey / key / token 字段，也不得把密钥拼进任何字符串。密钥只在后端内存里按 `sourceKey` 现取现用。
2. `sourceKey` 是后端能唯一定位该条来源配置的不透明键（cc-switch 用 `<app_type>:<providers.id>`，magpie 用 `<provider id>`）。同一 `sources.scan` 结果内的 sourceKey 必须唯一。
3. `conflict` 由后端调用宿主只读能力 `ai.providers.read`（权限 `host.ai.providers.read`）后，按第 2 节的去重身份与**已存在供应商**比对得出。取不到宿主列表时（能力被拒或宿主不可用）不标冲突，并把顶层 `conflictUnavailable` 置 `true`——页面据此提示「没标 ≠ 不重复」。
4. 单个来源扫描失败只写该来源的 `error`（有界错误文案，不含密钥与堆栈），其余来源照常返回；不得整批失败。`sources[].notice` 是「数据可用、但有事要说明」的非错误提示（当前唯一来源：magpie 主配置损坏后回退运行态文件），不置错误态、不影响 `detected` 与 `count`；与 `error` 语义分开——`error` 是读失败，`notice` 是已降级但可用。
5. `cc-switch` 数据源：SQLite `%USERPROFILE%\.cc-switch\cc-switch.db`，表 `providers`（列见 `docs/CC-SWITCH-FORMAT.md`），以只读模式打开。
6. `magpie` 数据源：`%USERPROFILE%\.cache\magpie\providers-state.json`，`providers.providers` 数组（列见 `docs/MAGPIE-FORMAT.md`）。
7. 路径解析：`USERPROFILE` 环境变量优先，取不到再回退 `os.UserHomeDir()`；另提供显式覆盖环境变量 `GONAVI_AI_CONFIG_CCSWITCH_DB` 与 `GONAVI_AI_CONFIG_MAGPIE_STATE`（测试与独立运行用）。

### `import.apply`（write）

入参：`{ "items": [ { "sourceId": "cc-switch", "sourceKey": "claude:...", "overwrite": false } ] }`

流程：按 `(sourceId, sourceKey)` 从来源现取完整配置（含密钥）→ 组装第 2 节请求体 → 通过 SDK `Host.CallCapability(ctx, "ai.providers.import", req)` 调用宿主 → 原样转交宿主结果，并附加 `"resolved": <成功取到配置的条数>`。

返回：第 2 节返回体 + `resolved` 字段；取不到配置的条目不进宿主请求体，逐条计为 `failed`。reason 分两种：来源里已经没有该条 → `source_missing`；读取来源失败（文件损坏/被占用/权限不足/来源未注册/`sourceKey` 超长）→ `source_read_failed`。两者必须分开——把「读不了」说成「配置被删」会让用户去上游白找。请求体超过宿主 `MaxArgsBytes`（256 KiB）时插件自行拦下，返回 `invalid_arguments`，不留到宿主整批拒绝。

**`items[].index` 一律是调用方 `items` 数组的下标（0..n-1）**（v1.1 明确）：取不到配置的条目要占住它自己的下标，宿主返回的逐条结果按发出顺序回填到对应下标；宿主返回的 index（宿主请求体内的下标）不得直接透出。

### 4.1 来源清单（v1.2）

| sourceId | label | 主路径（覆盖变量 > XDG > 家目录） | 覆盖变量 | 形态 | 密钥 |
|---|---|---|---|---|---|
| `cc-switch` | CC Switch | `~/.cc-switch/cc-switch.db` | `GONAVI_AI_CONFIG_CCSWITCH_DB` | SQLite `providers` 表 | `settings_config` 内明文 |
| `magpie` | Magpie | `$XDG_CONFIG_HOME/magpie/providers.json`（缺省 `~/.config/magpie/providers.json`） | `GONAVI_AI_CONFIG_MAGPIE_CONFIG` | `{providers:[{id,name,key,keys[],baseAPI,chat,responses,anthropic,models[],headers,off}]}` | `key` 明文字符串；`off:true` 跳过 |
| `ccgui` | ccgui | `~/.ccgui/config.json` | `GONAVI_AI_CONFIG_CCGUI_CONFIG` | `{<app>:{providers:{<uuid>:{id,name,authJson,configToml}},current}}` | 已验证 codex 形态：`authJson.OPENAI_API_KEY` 明文 |
| `opencode` | opencode | `$XDG_CONFIG_HOME/opencode/opencode.json`（缺省 `~/.config/opencode/opencode.json`） | `GONAVI_AI_CONFIG_OPENCODE_CONFIG` | `{provider:{<id>:{npm,options:{apiKey,baseURL},models:{<id>:{name}}}}}` | `options.apiKey` 明文 |
| `mimocode` | mimocode | `$XDG_CONFIG_HOME/mimocode/mimocode.jsonc`（缺省 `~/.config/mimocode/mimocode.jsonc`） | `GONAVI_AI_CONFIG_MIMOCODE_CONFIG` | 同 opencode，但扩展名 `.jsonc`（需剥注释） | `options.apiKey` 明文 |

magpie 兜底：主路径不存在或解析失败时读 `~/.cache/magpie/providers-state.json`（覆盖变量 `GONAVI_AI_CONFIG_MAGPIE_STATE`）。该文件只提供元数据 → 来源置 `keyUnavailable: true`、条目 `hasKey=false`、导入不下发密钥。此外：**主路径存在却读不了/不是合法 JSON** 时，还要在 `sources[].notice` 里写明「主配置…，已回退到运行态文件」——否则一个被改坏的主配置会被静默忽略（「主路径本就不存在」不算降级，`notice` 为空）。

映射规则（各家共用）：

- `npm` 为 `@ai-sdk/anthropic` → `apiFormat = anthropic`；其余（含 `@ai-sdk/openai-compatible`）→ `openai`。
- `models` 取模型映射的键（opencode / mimocode）；magpie 取 `models[]` 字符串数组；ccgui 沿用 cc-switch codex 那套 TOML 解析（顶层 `model` + `[model_providers.*].base_url`）。
- `sourceKey`：cc-switch `<app_type>:<id>`；magpie `<provider id>`；ccgui `<app>:<uuid>`；opencode / mimocode `<provider id>`。同一返回体内必须唯一。
- 未知结构的条目：**禁止猜字段**，条目不返回，并把条数计入该来源的 `sources[].skipped`。ccgui 除 codex 外的分节（claude / gemini / kimi / grok / opencode）本机无样本，必须走这条路径，不得编造映射。
- `sources[].skipped` 与 `sources[].disabled` 是两个**必须分开**的计数（可省略为 0）：
  - `skipped`：来源里的条目**结构不支持**（含 `sourceKey` 重复被去掉的条目），无法映射成 GoNavi 供应商。本机实例：cc-switch 的 3 条官方 OAuth 条目（无自建端点）、magpie 的 7 条插件型条目（端点是 `plugin://`）。
  - `disabled`：条目在**上游被用户停用**（magpie 的 `off:true`）。本机实例：magpie 24 条。用户在上游启用即可导入，**不得说成"插件不认识"**。
- **逐条原因通道**（给每个未返回条目单独回 reason）留作后续版本；本轮只回两个计数。
- 路径解析一律用 `resolveSourcePath` / `resolveXDGSourcePath`（`internal\sources\sources.go`）：解析不到就报错，绝不产出相对路径。

## 5. Magpie 密钥（v1.2 修正：明文可得）

早先「magpie 只暴露掩码密钥」的结论**只对运行态文件成立**：`~/.cache/magpie/providers-state.json` 里确实只有 `key.masked` 与 `keyList[].masked`。Magpie 的**源配置**在 XDG 目录 `~/.config/magpie/providers.json`，那里 `key` 是明文（本机实测 54 条供应商中 44 条带明文密钥，23 条 `off:true`）。

要求：
1. 主路径读 XDG 源配置，`key` 直接作为 apiKey 导入、`hasKey=true`；`off:true` 的条目跳过（Magpie 自己也不把它算作可用），来源的 `count` 与实际返回的 items 数保持一致。
2. 只有主路径不可用（不存在或解析失败）才退回运行态文件，并按兜底语义置 `keyUnavailable=true`、条目 `hasKey=false`、导入不下发 apiKey。
3. 仍然禁止伪造、截断或猜测密钥。`keys[]` 数组存在时取其中 `off:false` 的第一条；没有则用顶层 `key`。
