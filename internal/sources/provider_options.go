package sources

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
)

// 本文件放 ccgui / opencode / mimocode 三个新来源共用的读取与错误文案，
// 以及 opencode 与 mimocode 的解析。后两者的配置形状完全一致，
// 差别只在扩展名和注释，所以解析只写一份。

// sourceMaxConfigBytes 是单个来源配置文件的大小上限。实测三个来源的文件都在 100 KB 量级，
// 4 MiB 只为挡住覆盖变量被误指到的大文件——那种情况整文件读进内存再解析属于无界读。
const sourceMaxConfigBytes = 4 << 20

// 有界错误文案：只说明类别，不含路径、密钥与堆栈，直接进 sources[].error。
const (
	sourceMissingText     = "文件不存在"
	sourceTooLargeText    = "文件过大"
	sourceInvalidJSONText = "不是合法 JSON"
	sourcePermissionText  = "权限不足"
	sourceReadFailedText  = "读取失败"
)

// readSourceFile 先按大小挡一道再读，避免被误指到的大文件把内存吃满。
func readSourceFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > sourceMaxConfigBytes {
		return nil, errors.New(sourceTooLargeText)
	}
	return os.ReadFile(path)
}

// sourceErrorText 把读文件的错误折成有界类别文案。不拼入原始错误：路径错误会带上路径，
// 解析错误会带上出错位置，两者都不该外传。
func sourceErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case IsMissing(err):
		return sourceMissingText
	case errors.Is(err, os.ErrPermission):
		return sourcePermissionText
	case strings.Contains(err.Error(), sourceTooLargeText):
		return sourceTooLargeText
	default:
		return sourceReadFailedText
	}
}

// sortedRawKeys 返回 JSON 对象的键。JSON 解码成 map 后原始顺序已经丢了，
// 排序只是为了让同一份配置每次扫描产出同样的条目顺序。
func sortedRawKeys(source map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// appendUnique 按 sourceKey 并入条目：同一来源内重复的丢掉后者并计数。
// 契约要求同一次 scan 内 sourceKey 唯一，而 JSON 对象本身挡不住上游换格式后撞键，
// 所以入列这条路统一走这里，而不是各来源各写一遍。
func appendUnique(result *ScanResult, seen map[string]bool, item SourceItem) {
	if seen[item.SourceKey] {
		result.Skipped++
		return
	}
	seen[item.SourceKey] = true
	result.Items = append(result.Items, item)
}

// fetchFromScan 重扫一遍按 sourceKey 取完整条目（含密钥）。
// 三个来源的配置都很小，重扫比另写一套索引更不容易和 Scan 的映射走偏——
// 密钥的取值位置只存在一处，多一条路就多一个走偏的地方。
func fetchFromScan(ctx context.Context, source Source, sourceKey string) (SourceItem, bool, error) {
	result, err := source.Scan(ctx)
	if err != nil {
		return SourceItem{}, false, err
	}
	for _, item := range result.Items {
		if item.SourceKey == sourceKey {
			return item, true, nil
		}
	}
	return SourceItem{}, false, nil
}

// providerOptionsConfig 是 opencode / mimocode 共用的顶层形状：供应商挂在 provider 对象下。
// provider 收成 RawMessage：它是不是对象由解析侧判定，值本身不在这里定型。
type providerOptionsConfig struct {
	Provider json.RawMessage `json:"provider"`
}

// providerOptionsEntry 是一条供应商条目。opencode 不写 name，只有 mimocode 写；
// 取不到就退回条目 id，不编一个名字出来。
type providerOptionsEntry struct {
	Name    string                     `json:"name"`
	NPM     string                     `json:"npm"`
	Options providerOptions            `json:"options"`
	Models  map[string]json.RawMessage `json:"models"`
}

type providerOptions struct {
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseURL"`
}

// parseProviderOptions 解析 opencode / mimocode 的配置正文。
func parseProviderOptions(path, sourceID string, raw []byte) (ScanResult, error) {
	var config providerOptionsConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return ScanResult{}, Bounded(sourceID, sourceInvalidJSONText)
	}
	result := ScanResult{Path: path}
	if len(config.Provider) == 0 {
		// 没有 provider 成员：这份配置里就是没有供应商，不是坏数据。
		return result, nil
	}
	// 条目先收成 RawMessage 再逐条解码：一条坏数据只丢它自己，不牵连同文件的其他条目。
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(config.Provider, &providers); err != nil {
		// provider 存在但不是对象：条目数无从枚举，按一条记 skipped，不静默吞掉。
		result.Skipped++
		return result, nil
	}
	seen := make(map[string]bool, len(providers))
	for _, id := range sortedRawKeys(providers) {
		item, ok := providerOptionsItem(sourceID, id, providers[id])
		if !ok {
			result.Skipped++
			continue
		}
		appendUnique(&result, seen, item)
	}
	return result, nil
}

// providerOptionsItem 把一条供应商映射成扫描条目。
// 没有 baseURL 的条目不可导入（宿主契约要求 baseUrl），记 skipped 而不是产出空地址条目。
func providerOptionsItem(sourceID, id string, raw json.RawMessage) (SourceItem, bool) {
	id = strings.TrimSpace(id)
	var entry providerOptionsEntry
	if id == "" || json.Unmarshal(raw, &entry) != nil {
		return SourceItem{}, false
	}
	baseURL := strings.TrimSpace(entry.Options.BaseURL)
	if baseURL == "" {
		return SourceItem{}, false
	}
	item := SourceItem{
		SourceID:  sourceID,
		SourceKey: id,
		Name:      strings.TrimSpace(entry.Name),
		BaseURL:   baseURL,
		APIFormat: providerOptionsFormat(entry.NPM),
		AppType:   sourceID,
	}
	if item.Name == "" {
		item.Name = id
	}
	// models 取模型映射的键；排序只为让顺序稳定，去重与去空白交给 dedupe。
	item.Models = dedupe(sortedRawKeys(entry.Models))
	if len(item.Models) > 0 {
		item.Model = item.Models[0]
	}
	item.Key = strings.TrimSpace(entry.Options.APIKey)
	item.HasKey = item.Key != ""
	return item, true
}

// providerOptionsFormat 按 npm 包名判定协议：anthropic 包走 anthropic，其余走 openai。
func providerOptionsFormat(npm string) string {
	if strings.Contains(strings.ToLower(npm), "anthropic") {
		return contract.FormatAnthropic
	}
	return contract.FormatOpenAI
}
