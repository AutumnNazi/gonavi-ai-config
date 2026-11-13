# GoNavi AI 配置导入（插件后端）

把本机 AI 工具的供应商配置读出来，勾选后导入 GoNavi 的 AI 供应商列表。
接口以 `docs/CONTRACT.md` 为唯一定义源（来源清单见其 §4.1）；cc-switch 与 magpie 的实测格式见 `docs/CC-SWITCH-FORMAT.md` 与 `docs/MAGPIE-FORMAT.md`。

当前版本 `0.1.0`，已发布 <https://github.com/AutumnNazi/gonavi-ai-config/releases/tag/v0.1.0>（安装用该 Release 的 ZIP 资产；发布后的字节不可替换，修 bug 必须提升版本）。

## 命令

| 命令 | 效果 | 说明 |
|---|---|---|
| `sources.scan` | read | 扫描五个来源（cc-switch / magpie / ccgui / opencode / mimocode），返回可选供应商（**不含密钥与请求头**） |
| `import.apply` | write | 按 (sourceId, sourceKey) 现取配置（含密钥），经宿主能力 `ai.providers.import` 落库 |

返回体 `items[].index` 一律是**调用方 items 数组的下标**：取不到配置的条目占住自己的下标记 `failed/source_missing`，宿主请求体里的下标不外传，因此中间缺一条也不会让后面的条目错位。

## 来源

| 来源 | 主路径（覆盖变量 > XDG > 家目录） | 覆盖变量 | 密钥 |
|---|---|---|---|
| cc-switch | `~/.cc-switch/cc-switch.db`（SQLite 只读：`mode=ro` + `query_only`） | `GONAVI_AI_CONFIG_CCSWITCH_DB` | `settings_config` 内明文 |
| magpie | `$XDG_CONFIG_HOME/magpie/providers.json`（缺省 `~/.config/magpie/providers.json`） | `GONAVI_AI_CONFIG_MAGPIE_CONFIG` | `key` 明文 |
| ccgui | `~/.ccgui/config.json` | `GONAVI_AI_CONFIG_CCGUI_CONFIG` | `authJson.OPENAI_API_KEY` 明文 |
| opencode | `$XDG_CONFIG_HOME/opencode/opencode.json` | `GONAVI_AI_CONFIG_OPENCODE_CONFIG` | `options.apiKey` 明文 |
| mimocode | `$XDG_CONFIG_HOME/mimocode/mimocode.jsonc`（JSONC，剥注释后解析） | `GONAVI_AI_CONFIG_MIMOCODE_CONFIG` | `options.apiKey` 明文 |

cc-switch 按 `app_type` 分派解析：`claude`、`claude-desktop`、`codex`、`gemini`、`opencode`、`grokbuild`、`hermes`；没有可用端点的行会被跳过，`sources[].count` 因此等于本次真正解析出的条数。

ccgui 目前只实现**已验证过的 codex 形态**（`authJson` + `configToml`）；其余分节（claude / gemini / kimi / grok / opencode）本机无样本，条目一律计入 `sources[].skipped` 而不猜字段。

magpie 主路径不可用（不存在或解析失败）时才退回运行态投影 `~/.cache/magpie/providers-state.json`（覆盖变量 `GONAVI_AI_CONFIG_MAGPIE_STATE`），该文件只有掩码密钥。

### 家目录解析

顺序：显式覆盖变量（必须是绝对路径）→ XDG 来源走 `XDG_CONFIG_HOME`，其余走家目录
→ `user.Current().HomeDir`（Windows 上走进程令牌与注册表，不读环境变量）
→ `os.UserHomeDir()` → 失败。

为什么不能只读 `USERPROFILE`：宿主以受限环境启动插件进程，
`internal/plugin/process.go:174-179` 的环境白名单只放行 PATH/SYSTEMROOT/WINDIR/TEMP/TMP/TMPDIR/LANG/LC_ALL 与代理变量，
没有 USERPROFILE；而 Windows 上 `os.UserHomeDir()` 读的正是它，于是家目录为空、
`filepath.Join` 会拼出 `".cc-switch\cc-switch.db"` 这种相对路径——看起来合法，却随进程工作目录飘走。

因此解析不到家目录时该来源 `detected=false`、`path` 留空、
`error` 写明 `cannot resolve user home`，绝不允许退化成相对路径（静默失败）。

## 密钥边界

密钥与自定义请求头只在内存里按 `sourceKey` 现取现用：`sources.SourceItem`（含 `Key`/`Headers`）只用于 Fetch，
给页面的 `contract.ScanItem` 里没有任何可承载密钥或请求头的字段，
`sources.scan` 的返回体因此不可能带上它们。单测对「返回体序列化后不含密钥子串」有断言，
并用一条含密钥的载荷证明该断言确实会失败（守卫本身不是恒真）。

## 页面内联

`contributes.pages[].html.source` 是**内联 HTML 文本**，不是文件路径：宿主只做 UTF-8 与 256 KiB 校验后直接当页面文档渲染。
因此仓库根的 `gonavi.plugin.json` 是模板（`html.source` 为占位令牌 `__INLINE_FRONTEND_INDEX_HTML__`），
打包时由 `scripts/build.mjs` 读 `frontend/index.html` 的内容写进去。
页面因此不必再以文件形式进包，`frontend/tests/` 的截图也不会白占体积。

## 构建与验证

```bash
go build ./... && go vet ./... && go test ./...
go test -tags livescan ./internal/sources/ -run TestLiveScan -v   # 用本机真实数据跑一次扫描
node scripts/build.mjs                                            # 产出 dist/<id>-<version>.zip
```

产物路径由脚本打印。包内只含 `bin/`、清单与 `LICENSE`（作者指南第 5 节的根目录要求：清单 + 平台二进制 + 许可正文，本插件不声明图标）；清单里已内联页面文档。

打包脚本按作者指南的约定消费 SDK：`go mod` 里 `require github.com/Syngnat/GoNavi/sdk/go v0.0.0` 加一条指向本机 GoNavi 检出的 `replace`（见 [sdk.md](https://plugin.gonavi.org/docs/sdk)）——发布的是打包归档而不是源码树，绝对路径不会进包。

## 边界

本插件只读上游数据，写操作一律经宿主能力 `ai.providers.import`；
不直接读写 `ai_config.json`，以免绕过活动 Service 导致导入被下一次保存覆盖。

## 宿主版本要求

插件用到两条宿主窄口径权限与对应能力：

- `host.ai.providers.read` → 能力 `ai.providers.read`：读现有供应商的名称/地址做重复标记（不读密钥）。
- `host.ai.providers.write` → 能力 `ai.providers.import`：把勾选的供应商写进宿主列表（主仓 `internal/app/plugin_capability_ai_import.go`）。

**这两个能力尚未随任何正式版发布**（旧版只有绑在 `host.metadata.read` 上的 `ai.providers`），因此清单声明 `minHostVersion: "1.2.3"` ——即承载这两个能力的下一个发布版本。

- 宿主按 semver 比较下限，`dev`/`dev-*`/`0.0.*`/含 `-dev`、`-test`、`-snapshot` 的**开发版宿主对任何下限都放行**：本地 `AppVersion=dev-local` 的构建照常安装，正式 1.2.2 及更早的宿主会在安装时就被拒绝（不再出现「装得上、扫描正常、点导入才失败」）。
- 兜底文案仍然保留：若下限被绕过（两侧同时改版本号、或手工装包），`import.apply` 拿到的 `unknown_method` 会被页面翻成「这个 GoNavi 没有导入能力，请升级」，扫描不受影响。

## 已知限制（附本机实测）

| 限制 | 说明 | 本机影响（2026-11 实测） |
|---|---|---|
| ccgui 只解析 codex 分节 | claude / gemini / kimi / grok / opencode 无样本，一律计入 `skipped` 不猜字段 | 无：这五个分节的 `providers` 实测都是空（codex 1 条） |
| ccgui 的 `apiFormat` 固定 `openai-responses` | 不读 TOML 的协议字段 | 无：本机唯一一条 TOML 写的是 `api_protocol = "responses"` |
| magpie 的 Codex 订阅/回环网关未实现 | `127.0.0.1:3425` + 固定 Key 那种形态 | 无：本机 55 条里 0 条属于该形态 |
| magpie 多密钥只取第一条可用的 `key` | `keys[]` 里多账号只导入一个 | 无：本机 55 条里 0 条有多条可用密钥 |
| mimocode 只剥 `//` 与 `/* */` 注释 | 尾随逗号等宽松 JSONC 会整份判非法，计入 `error` | 无：本机文件可正常解析 |
| `windows/amd64` 之外的平台无后端 | 清单只声明了一个平台 | 按需补 |

`go test -tags livescan` 的用例断言本机真实数据，只用于开发机自检（默认不打 tag 不跑），不要当成可移植测试。
