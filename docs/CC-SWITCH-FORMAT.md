# CC Switch 格式（实测）

来源库：`%USERPROFILE%\.cc-switch\cc-switch.db`（SQLite，用只读 URI `file:...?mode=ro` 打开，避免锁冲突）。

实测环境：2026-11 快照，providers 表 93 行。

## 表结构（只列用得到的）

```sql
providers(
  id TEXT, app_type TEXT, name TEXT, settings_config TEXT,
  website_url TEXT, category TEXT, created_at INTEGER, sort_index INTEGER,
  notes TEXT, icon TEXT, icon_color TEXT, meta TEXT,
  is_current BOOLEAN, in_failover_queue BOOLEAN, cost_multiplier TEXT,
  limit_daily_usd TEXT, limit_monthly_usd TEXT, provider_type TEXT
)
```

- `settings_config` 与 `meta` 都是 JSON 文本。
- `app_type` 实测分布：codex 43、claude 30、opencode 8、claude-desktop 6、gemini 3、grokbuild 2、hermes 1。
- `provider_type` 全为 NULL（勿依赖）。

## settings_config 形态（按 app_type）

### claude / claude-desktop

```json
{ "env": {
    "ANTHROPIC_AUTH_TOKEN": "<密钥>",
    "ANTHROPIC_BASE_URL": "https://api.zzzcoding.org",
    "ANTHROPIC_MODEL": "claude-opus-5[1M]",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-5[1M]",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-opus-5[1M]",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-opus-5",
    "CLAUDE_CODE_SUBAGENT_MODEL": "claude-opus-5[1M]"
} }
```

映射：`baseUrl = ANTHROPIC_BASE_URL`；`apiKey = ANTHROPIC_AUTH_TOKEN`；`apiFormat = anthropic`；
`model = ANTHROPIC_MODEL`；`models` 取 `ANTHROPIC_DEFAULT_*_MODEL` 与 `ANTHROPIC_MODEL` 去重后的有序集合（保留 `[1M]` 这类后缀原文，不要改写）。

### codex

```json
{ "auth": { "OPENAI_API_KEY": "<密钥>" },
  "config": "model_provider = \"custom\"\nmodel = \"gpt-6-astra\"\nmodel_reasoning_effort = \"high\"\n\n[model_providers]\n..." }
```

`config` 是 TOML 文本。映射：`apiKey = auth.OPENAI_API_KEY`；`model` 从 TOML 的顶层 `model =` 取；
`baseUrl` 从 TOML 的 `[model_providers.<id>] base_url` 取（缺失时回退 `meta.proxyUrl` 之外的 `website_url`，仍缺失则该条判为不可导入：`invalid_base_url`）；
`apiFormat` 由 `meta.apiFormat`（实测出现 `openai_responses`）决定，缺省 `openai-responses`。

### gemini

```json
{ "env": { "GEMINI_API_KEY": "<密钥>", "GOOGLE_GEMINI_BASE_URL": "https://elysia.h-e.top", "GEMINI_MODEL": "gemini-3.5-flash" } }
```

映射：`apiFormat = gemini`；`baseUrl = GOOGLE_GEMINI_BASE_URL`；`apiKey = GEMINI_API_KEY`；`model = GEMINI_MODEL`。

### opencode

```json
{ "npm": "@ai-sdk/openai-compatible",
  "options": { "apiKey": "<密钥>", "baseURL": "https://newapi.zmoon.top/v1", "setCacheKey": "..." },
  "models": { "deepseek-v4-flash-0731": { "name": "deepseek-v4-flash-0731" } } }
```

映射：`baseUrl = options.baseURL`；`apiKey = options.apiKey`；`apiFormat = openai`；`models = models 的键`；`model = models 第一个键`。`npm = @ai-sdk/anthropic` 时 `apiFormat = anthropic`。

### grokbuild

```json
{ "config": "[models]\ndefault = \"grok-4.6\"\n[model.\"grok-4.6\"]\nmodel = \"grok-4.6\"\nbase_url = \"...\"\n" }
```

映射：`model = TOML 的 [models].default`；`baseUrl` 从 `[model."<id>"].base_url` 取；
`apiFormat` 取 `meta.apiFormat`（实测 `openai_responses`），缺省 `openai-responses`；
密钥字段名由 `meta.apiKeyField` 给出（实测 `ANTHROPIC_AUTH_TOKEN`）。

### hermes

```json
{ "name": "existing-hermes", "base_url": "https://api.hermes.example/v1",
  "api_key": "<密钥>", "model": "gpt-4o", "models": [ { "id": "gpt-4o", "name": "GPT-4o Updated" } ] }
```

映射：`baseUrl = base_url`；`apiKey = api_key`；`model = model`；`models = models[].id`；`apiFormat = openai`。

## 已验证不存在的字段

`provider_type` 恒为 NULL；没有可直接复用的 sourceId 字段（因此 `sourceKey` 用 `<app_type>:<id>`）。
