package sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gonavi-ai-config/internal/contract"
	"gonavi-ai-config/internal/toml"

	_ "modernc.org/sqlite"
)

// ccSwitch 读 cc-switch 的 SQLite 库。库是只读打开的：cc-switch 可能正在运行，
// 读写同开会争锁，而这里只需要 SELECT。
type ccSwitch struct {
	path string
	// resolveErr 非空表示路径没解析出来，此时 path 为空，扫描侧据此报错而不是猜路径。
	resolveErr error
}

// NewCCSwitch 用显式路径构造 cc-switch 来源（测试与独立入口）。
func NewCCSwitch(path string) Source { return &ccSwitch{path: path} }

func (s *ccSwitch) ID() string       { return contract.SourceCCSwitch }
func (s *ccSwitch) Label() string    { return "CC Switch" }
func (s *ccSwitch) Path() string     { return s.path }
func (s *ccSwitch) PathError() error { return s.resolveErr }

// ccSwitchAppTypes 是受支持的 app_type。实测分布 codex 43 / claude 30 / opencode 8 /
// claude-desktop 6 / gemini 3 / grokbuild 2 / hermes 1；七种全部解析，未知类型跳过并计数，
// 这样 sources[].count 与「本次真正解析出的条数」始终一致。
var ccSwitchAppTypes = []string{
	"claude", "claude-desktop", "codex", "gemini", "opencode", "grokbuild", "hermes",
}

const ccSwitchQuery = "select id, app_type, name, settings_config, coalesce(website_url, ''), coalesce(meta, ''), coalesce(is_current, 0) from providers"

func (s *ccSwitch) open() (*sql.DB, error) {
	// mode=ro 与 _pragma=query_only(1) 双保险：即便后续代码写错 SQL 也只能读。
	dsn := "file:" + sqliteURIPath(s.path) + "?mode=ro&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 只读场景串行读取即可，单连接避免 SQLite 锁等待。
	db.SetMaxOpenConns(1)
	return db, nil
}

// sqliteURIPath 把本地路径折成 SQLite URI 认得的正斜杠形式，并转义特殊字符。
func sqliteURIPath(path string) string {
	return strings.ReplaceAll(url.PathEscape(strings.ReplaceAll(path, "\\", "/")), "%2F", "/")
}

func (s *ccSwitch) Scan(ctx context.Context) (ScanResult, error) {
	if s.resolveErr != nil {
		return ScanResult{}, s.resolveErr
	}
	db, err := s.open()
	if err != nil {
		return ScanResult{}, Bounded(s.ID(), "打开数据库失败")
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, ccSwitchQuery)
	if err != nil {
		return ScanResult{}, Bounded(s.ID(), "读取 providers 表失败")
	}
	defer rows.Close()

	result := ScanResult{Path: s.path}
	for rows.Next() {
		var (
			id, appType, name, settingsConfig, websiteURL, meta string
			isCurrent                                           int
		)
		if err := rows.Scan(&id, &appType, &name, &settingsConfig, &websiteURL, &meta, &isCurrent); err != nil {
			return ScanResult{}, Bounded(s.ID(), "读取 providers 行失败")
		}
		item, ok := parseCCSwitchRow(appType, id, name, settingsConfig, websiteURL, meta, isCurrent == 1)
		if !ok {
			result.Skipped++
			continue
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return ScanResult{}, Bounded(s.ID(), "遍历 providers 表失败")
	}
	return result, nil
}

func (s *ccSwitch) Fetch(ctx context.Context, sourceKey string) (SourceItem, bool, error) {
	if s.resolveErr != nil {
		return SourceItem{}, false, s.resolveErr
	}
	appType, id, ok := splitCCSwitchKey(sourceKey)
	if !ok {
		return SourceItem{}, false, nil
	}
	db, err := s.open()
	if err != nil {
		return SourceItem{}, false, Bounded(s.ID(), "打开数据库失败")
	}
	defer db.Close()
	var (
		name, settingsConfig, websiteURL, meta string
		isCurrent                              int
	)
	row := db.QueryRowContext(ctx,
		"select name, settings_config, coalesce(website_url, ''), coalesce(meta, ''), coalesce(is_current, 0) from providers where id = ? and app_type = ?",
		id, appType)
	if err := row.Scan(&name, &settingsConfig, &websiteURL, &meta, &isCurrent); err != nil {
		// 只有「查无此条」才算没找到。库损坏、被占用、context 取消这些错误必须如实上报，
		// 否则页面会把「库读不了」显示成「来源里已经没有这条配置」。
		if errors.Is(err, sql.ErrNoRows) {
			return SourceItem{}, false, nil
		}
		return SourceItem{}, false, Bounded(s.ID(), "读取 providers 行失败")
	}
	item, ok := parseCCSwitchRow(appType, id, name, settingsConfig, websiteURL, meta, isCurrent == 1)
	return item, ok, nil
}

// splitCCSwitchKey 解析 sourceKey。id 可能含冒号，所以只切第一段。
func splitCCSwitchKey(sourceKey string) (string, string, bool) {
	appType, id, found := strings.Cut(sourceKey, ":")
	if !found || appType == "" || id == "" {
		return "", "", false
	}
	for _, known := range ccSwitchAppTypes {
		if known == appType {
			return appType, id, true
		}
	}
	return "", "", false
}

// parseCCSwitchRow 按 app_type 分派解析。ok=false 表示该行没有可导入的端点，
// 直接跳过而不是返回空条目——空条目在页面上只会是无法导入的噪声。
// 契约对 codex 的规定是「baseUrl 缺失则判为不可导入」，这里统一按「无端点即跳过」执行，
// 且不读取 meta.proxyUrl（那是本地代理地址，不是供应商端点）。
func parseCCSwitchRow(appType, id, name, settingsConfig, websiteURL, meta string, current bool) (SourceItem, bool) {
	var config map[string]any
	if err := json.Unmarshal([]byte(settingsConfig), &config); err != nil {
		return SourceItem{}, false
	}
	item := SourceItem{
		SourceID:  contract.SourceCCSwitch,
		SourceKey: appType + ":" + id,
		Name:      strings.TrimSpace(name),
		AppType:   appType,
		Current:   current,
	}
	switch appType {
	case "claude", "claude-desktop":
		env := stringMap(config["env"])
		item.BaseURL = strings.TrimSpace(env["ANTHROPIC_BASE_URL"])
		item.APIFormat = contract.FormatAnthropic
		item.Key = strings.TrimSpace(env["ANTHROPIC_AUTH_TOKEN"])
		item.Model = strings.TrimSpace(env["ANTHROPIC_MODEL"])
		item.Models = dedupe([]string{
			env["ANTHROPIC_MODEL"],
			env["ANTHROPIC_DEFAULT_OPUS_MODEL"],
			env["ANTHROPIC_DEFAULT_SONNET_MODEL"],
			env["ANTHROPIC_DEFAULT_HAIKU_MODEL"],
			env["ANTHROPIC_DEFAULT_FABLE_MODEL"],
			env["CLAUDE_CODE_SUBAGENT_MODEL"],
		})
	case "codex":
		tomlText := stringValue(config["config"])
		item.Key = strings.TrimSpace(stringMap(config["auth"])["OPENAI_API_KEY"])
		// codex 的 base_url 写在 [model_providers.<id>] 里，<id> 事先不知道，按段前缀扫。
		item.BaseURL, _ = toml.FirstInSections(tomlText, "base_url", "model_providers.")
		if item.BaseURL == "" {
			item.BaseURL = strings.TrimSpace(websiteURL)
		}
		item.Model = toml.Top(tomlText, "model")
		item.Models = dedupe([]string{item.Model})
		item.APIFormat = ccSwitchFormat(stringMapAny(meta)["apiFormat"], contract.FormatOpenAIResponses)
	case "gemini":
		env := stringMap(config["env"])
		item.BaseURL = strings.TrimSpace(env["GOOGLE_GEMINI_BASE_URL"])
		item.APIFormat = contract.FormatGemini
		item.Key = strings.TrimSpace(env["GEMINI_API_KEY"])
		item.Model = strings.TrimSpace(env["GEMINI_MODEL"])
		item.Models = dedupe([]string{item.Model})
	case "opencode":
		options := mapValue(config["options"])
		item.BaseURL = strings.TrimSpace(stringValue(options["baseURL"]))
		item.Key = strings.TrimSpace(stringValue(options["apiKey"]))
		item.APIFormat = contract.FormatOpenAI
		if strings.Contains(strings.ToLower(stringValue(config["npm"])), "anthropic") {
			item.APIFormat = contract.FormatAnthropic
		}
		item.Models = objectKeys(config["models"])
		if len(item.Models) > 0 {
			item.Model = item.Models[0]
		}
	case "grokbuild":
		tomlText := stringValue(config["config"])
		item.BaseURL, _ = toml.FirstInSections(tomlText, "base_url", "model.")
		if item.BaseURL == "" {
			item.BaseURL = strings.TrimSpace(websiteURL)
		}
		item.Model = toml.Get(tomlText, "models", "default")
		item.Models = dedupe([]string{item.Model})
		// 密钥字段名由 meta.apiKeyField 给出（实测 ANTHROPIC_AUTH_TOKEN）；
		// 它写在 [model."<id>"] 段里或顶层都可能，两种都试。
		apiKeyField := strings.TrimSpace(stringValue(stringMapAny(meta)["apiKeyField"]))
		if apiKeyField == "" {
			apiKeyField = "ANTHROPIC_AUTH_TOKEN"
		}
		item.Key, _ = toml.FirstInSectionsAny(tomlText, []string{"api_key", apiKeyField}, "model.")
		if item.Key == "" {
			item.Key = toml.Top(tomlText, "api_key")
		}
		if item.Key == "" {
			item.Key = toml.Top(tomlText, apiKeyField)
		}
		item.APIFormat = ccSwitchFormat(stringMapAny(meta)["apiFormat"], contract.FormatOpenAIResponses)
	case "hermes":
		item.BaseURL = strings.TrimSpace(stringValue(config["base_url"]))
		item.Key = strings.TrimSpace(stringValue(config["api_key"]))
		item.Model = strings.TrimSpace(stringValue(config["model"]))
		item.APIFormat = contract.FormatOpenAI
		item.Models = hermesModels(config["models"])
		if len(item.Models) == 0 {
			item.Models = dedupe([]string{item.Model})
		}
	default:
		return SourceItem{}, false
	}
	// 没有可用端点就不可导入：契约要求 codex 缺 baseUrl 时判为不可导入。
	if strings.TrimSpace(item.BaseURL) == "" {
		return SourceItem{}, false
	}
	item.HasKey = item.Key != ""
	return item, true
}

// ccSwitchFormat 归一化 cc-switch 的 apiFormat 写法（实测 openai_responses），
// 未知值回退到该 app_type 的默认协议。
func ccSwitchFormat(raw any, fallback string) string {
	value := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(stringValue(raw))), "_", "-")
	switch value {
	case contract.FormatOpenAI:
		return contract.FormatOpenAI
	case contract.FormatOpenAIResponses:
		return contract.FormatOpenAIResponses
	case contract.FormatAnthropic:
		return contract.FormatAnthropic
	case contract.FormatGemini:
		return contract.FormatGemini
	default:
		return fallback
	}
}

func hermesModels(raw any) []string {
	list, _ := raw.([]any)
	out := make([]string, 0, len(list))
	for _, entry := range list {
		if object, ok := entry.(map[string]any); ok {
			out = append(out, stringValue(object["id"]))
		}
	}
	return dedupe(out)
}

func stringMap(raw any) map[string]string {
	object, _ := raw.(map[string]any)
	out := make(map[string]string, len(object))
	for name, value := range object {
		out[name] = stringValue(value)
	}
	return out
}

func stringMapAny(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func mapValue(raw any) map[string]any {
	object, _ := raw.(map[string]any)
	return object
}

func stringValue(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// objectKeys 保序返回 JSON 对象的键；JSON 解码成 map 已丢失顺序，排序只为让结果稳定。
func objectKeys(raw any) []string {
	object, _ := raw.(map[string]any)
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return dedupe(keys)
}
