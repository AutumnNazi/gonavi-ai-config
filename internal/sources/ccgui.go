package sources

import (
	"context"
	"encoding/json"
	"strings"

	"gonavi-ai-config/internal/contract"
	"gonavi-ai-config/internal/toml"
)

// sourceCCGUI 是来源标识，与 docs/CONTRACT.md 第 4.1 节的 sourceId 一致。
const sourceCCGUI = "ccgui"

// ccguiCodexApp 是唯一已验证形态的分节。
//
// 其余分节（claude / gemini / kimi / grok / opencode）本机没有样本，一律记 skipped，
// 绝不按 codex 那套字段去猜——猜出来的 baseUrl 会是一条能导入却连不通的供应商。
const ccguiCodexApp = "codex"

// ccgui 读 ~/.ccgui/config.json：顶层是若干分节，每个分节里 providers 是以 uuid 为键的条目表。
// 只有 codex 分节的形态经过验证：authJson 里的 OPENAI_API_KEY 是明文密钥，
// configToml 里写模型与端点。
type ccgui struct {
	path string
	// resolveErr 非空表示路径没解析出来，此时 path 为空，扫描侧据此报错而不是猜路径。
	resolveErr error
}

func (s *ccgui) ID() string       { return sourceCCGUI }
func (s *ccgui) Label() string    { return sourceCCGUI }
func (s *ccgui) Path() string     { return s.path }
func (s *ccgui) PathError() error { return s.resolveErr }

// ccguiProvider 是一条分节条目的最小投影：只接 codex 已验证的字段，其余一概不读。
type ccguiProvider struct {
	Name       string `json:"name"`
	AuthJSON   string `json:"authJson"`
	ConfigTOML string `json:"configToml"`
}

func (s *ccgui) Scan(ctx context.Context) (ScanResult, error) {
	if s.resolveErr != nil {
		return ScanResult{}, s.resolveErr
	}
	if err := ctx.Err(); err != nil {
		return ScanResult{}, err
	}
	raw, err := readSourceFile(s.path)
	if err != nil {
		if IsMissing(err) {
			return ScanResult{Path: s.path}, nil
		}
		return ScanResult{}, Bounded(s.ID(), sourceErrorText(err))
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sections); err != nil {
		return ScanResult{}, Bounded(s.ID(), sourceInvalidJSONText)
	}
	result := ScanResult{Path: s.path}
	seen := make(map[string]bool)
	for _, app := range sortedRawKeys(sections) {
		var section map[string]json.RawMessage
		if err := json.Unmarshal(sections[app], &section); err != nil {
			// 非对象：顶层还混着 version 这类标量配置，它们不是分节。
			continue
		}
		providersRaw, ok := section["providers"]
		if !ok {
			// 顶层还有 soundNotification、promptEnhancer 这类非分节配置，用「有没有 providers 成员」
			// 这个结构信号判定分节，比按固定名单枚举可靠：新增分节不会被悄悄当成配置项漏掉。
			continue
		}
		var providers map[string]json.RawMessage
		if err := json.Unmarshal(providersRaw, &providers); err != nil {
			// providers 存在但不是对象：这一节的条目数无从枚举，按一条记 skipped，
			// 不静默吞掉——静默吞掉等于把配置文件里的东西凭空消失。
			result.Skipped++
			continue
		}
		current := ccguiSectionCurrent(section["current"])
		for _, id := range sortedRawKeys(providers) {
			if app != ccguiCodexApp {
				result.Skipped++
				continue
			}
			item, ok := ccguiCodexItem(id, providers[id], current)
			if !ok {
				result.Skipped++
				continue
			}
			appendUnique(&result, seen, item)
		}
	}
	return result, nil
}

// Fetch 按 sourceKey（<app>:<uuid>）现取一条完整配置，密钥在这一步才回到内存。
func (s *ccgui) Fetch(ctx context.Context, sourceKey string) (SourceItem, bool, error) {
	return fetchFromScan(ctx, s, sourceKey)
}

// ccguiSectionCurrent 取分节自己的「当前使用」标识。取不到（null、数字、缺字段）就是空串，
// 此时没有任何条目会被标成 current——标错一个当前项比不标更容易误导人。
func ccguiSectionCurrent(raw json.RawMessage) string {
	var current string
	if len(raw) == 0 || json.Unmarshal(raw, &current) != nil {
		return ""
	}
	return strings.TrimSpace(current)
}

// ccguiCodexItem 映射 codex 分节的一条条目，只取两处已验证的字段：
// authJson.OPENAI_API_KEY 是明文密钥，configToml 的顶层 model 与 [model_providers.*].base_url。
//
// sourceKey 用条目表的键（uuid）而不是条目里的 id：uuid 是配置自己的索引，
// 条目内字段缺失或与键不一致时索引仍然唯一。
func ccguiCodexItem(id string, raw json.RawMessage, current string) (SourceItem, bool) {
	var entry ccguiProvider
	if err := json.Unmarshal(raw, &entry); err != nil {
		return SourceItem{}, false
	}
	// codex 的 base_url 写在 [model_providers.<id>] 里，<id> 事先不知道，按段前缀扫。
	baseURL, _ := toml.FirstInSections(entry.ConfigTOML, "base_url", "model_providers.")
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		// 没有端点就不可导入，跳过而不是产出一条空地址供应商。
		return SourceItem{}, false
	}
	item := SourceItem{
		SourceID:  sourceCCGUI,
		SourceKey: ccguiCodexApp + ":" + id,
		Name:      strings.TrimSpace(entry.Name),
		BaseURL:   baseURL,
		// 协议按 responses 走，与 cc-switch 的 codex 一致：契约只认 codex 形态的
		// 「顶层 model + [model_providers.*].base_url」两处取值，解析器不读 api_protocol / wire_api。
		APIFormat: contract.FormatOpenAIResponses,
		AppType:   ccguiCodexApp,
		Current:   current != "" && current == id,
	}
	if item.Name == "" {
		item.Name = id
	}
	item.Model = strings.TrimSpace(toml.Top(entry.ConfigTOML, "model"))
	item.Models = dedupe([]string{item.Model})
	item.Key = ccguiCodexKey(entry.AuthJSON)
	item.HasKey = item.Key != ""
	return item, true
}

// ccguiCodexKey 从 authJson 里取明文密钥。它是 JSON 串而不是对象，
// 解析失败就是「这条没有密钥」，不退回别的字段猜一个。
func ccguiCodexKey(authJSON string) string {
	var auth map[string]any
	if err := json.Unmarshal([]byte(authJSON), &auth); err != nil {
		return ""
	}
	return strings.TrimSpace(stringValue(auth["OPENAI_API_KEY"]))
}
