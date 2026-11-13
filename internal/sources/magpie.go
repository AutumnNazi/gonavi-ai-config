package sources

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
)

// magpie 读 Magpie 的供应商配置，两级来源：
//   - path：XDG 源配置 providers.json，`key` 是明文，可整条连密钥导入；
//   - fallbackPath：运行态投影 providers-state.json，只有掩码，导入不了密钥。
//
// 顺序有意为之：源配置是 Magpie 自己写配置的地方，运行态文件是它给界面看的投影，
// 密钥在那里被换成掩码。只有源配置不可用才退回运行态，并如实标注密钥不可用。
type magpie struct {
	path         string
	fallbackPath string
	// resolveErr 非空表示主路径没解析出来：报错而不是猜路径，也不拿兜底糊过去。
	resolveErr error
	// fallbackErr 非空表示兜底路径本身没解析出来；只有真的要走兜底时才有意义。
	fallbackErr error
}

// NewMagpie 用显式主路径构造 magpie 来源（测试与独立入口）。没有兜底路径：
// 主路径不可用就如实报错，绝不悄悄换一个文件读。
func NewMagpie(path string) Source { return &magpie{path: path} }

// NewMagpieWithFallback 同时给出主路径与兜底路径，供 sources.New 接线。
func NewMagpieWithFallback(path, fallbackPath string) Source {
	return &magpie{path: path, fallbackPath: fallbackPath}
}

func (s *magpie) ID() string       { return contract.SourceMagpie }
func (s *magpie) Label() string    { return "Magpie" }
func (s *magpie) Path() string     { return s.path }
func (s *magpie) PathError() error { return s.resolveErr }

// magpieMaxConfigBytes 是单个配置文件的上限。实测配置 100 KB 量级，4 MiB 只是挡住
// 被误指到的大文件——那种情况下整文件读进内存再解析属于无界读。
const magpieMaxConfigBytes = 4 << 20

// 有界错误文案：只说明类别，不含路径细节、密钥与堆栈。
const (
	magpieMissingText    = "文件不存在"
	magpieTooLargeText   = "文件过大"
	magpieInvalidJSON    = "不是合法 JSON"
	magpieReadFailedText = "读取失败"
)

// magpieConfig 是 XDG 源配置：顶层是对象，供应商在 providers 数组里。
type magpieConfig struct {
	Providers []magpieConfigProvider `json:"providers"`
}

// magpieConfigProvider 是源配置里的一条供应商。用指针类型接布尔字段：
// 字段缺失与显式 false 的语义不同（off 缺失表示启用），值类型分不出这两者。
type magpieConfigProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
	// Keys 原样留着：它要单独解码，数组里有一条形状不对只需跳过那一条，
	// 不能让整份配置看起来「读不了」。
	Keys      json.RawMessage   `json:"keys"`
	BaseAPI   string            `json:"baseAPI"`
	Chat      string            `json:"chat"`
	Responses string            `json:"responses"`
	Anthropic string            `json:"anthropic"`
	Models    []string          `json:"models"`
	Headers   map[string]string `json:"headers"`
	Off       *bool             `json:"off"`
}

// magpieConfigKey 是 keys[] 里的一条密钥。off 缺省即启用，显式 true 才排除。
type magpieConfigKey struct {
	Key string `json:"key"`
	Off *bool  `json:"off"`
}

// magpieState 是运行态投影：供应商嵌在 providers.providers 里（数组，不是对象）。
type magpieState struct {
	Providers struct {
		Providers []magpieStateProvider `json:"providers"`
	} `json:"providers"`
}

// magpieStateProvider 是运行态条目的最小投影：只取映射与端点需要的字段，
// 掩码字段一概不接——接了也没用，只会给「拿掩码冒充密钥」留口子。
type magpieStateProvider struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Chat      string          `json:"chat"`
	Responses string          `json:"responses"`
	Anthropic string          `json:"anthropic"`
	BaseAPI   string          `json:"baseAPI"`
	Chosen    []string        `json:"chosen"`
	Models    []magpieModel   `json:"models"`
	Key       json.RawMessage `json:"key"`
	Off       bool            `json:"off"`
}

type magpieModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *magpie) Scan(ctx context.Context) (ScanResult, error) {
	if s.resolveErr != nil {
		return ScanResult{}, s.resolveErr
	}
	if err := ctx.Err(); err != nil {
		return ScanResult{}, err
	}
	raw, err := readCapped(s.path)
	if err == nil {
		result, parseErr := parseMagpieConfig(raw)
		if parseErr == nil {
			result.Path = s.path
			return result, nil
		}
		return s.scanFallback(magpieErrorText(parseErr))
	}
	if !IsMissing(err) {
		// 存在但读不了（权限、被占用）：同样走兜底，但仍如实报错。
		return s.scanFallback(magpieErrorText(err))
	}
	return s.scanFallback(magpieMissingText)
}

// scanFallback 在主路径不可用时读运行态投影。主路径不可用的原因传进 boundedReason：
// 两类失败（读不到 / 不是合法 JSON）的处置一样，但只有把原因带回错误文案，
// 用户才知道该去修主配置还是去撑开权限。
func (s *magpie) scanFallback(boundedReason string) (ScanResult, error) {
	if s.fallbackPath == "" {
		// 没有兜底路径：主路径的失败原因就是本次扫描的结论，不静默返回空结果。
		if boundedReason == magpieMissingText {
			return ScanResult{Path: s.path}, nil
		}
		return ScanResult{}, Bounded(s.ID(), boundedReason)
	}
	if s.fallbackErr != nil {
		return ScanResult{}, Bounded(s.ID(), "配置路径不可用："+s.fallbackErr.Error())
	}
	raw, err := readCapped(s.fallbackPath)
	if err != nil {
		if IsMissing(err) {
			// 两个文件都没有：这是「没装/没配」而不是解析失败，不该报错。
			return ScanResult{Path: s.path}, nil
		}
		return ScanResult{}, Bounded(s.ID(), "运行态文件"+magpieErrorText(err))
	}
	result, parseErr := parseMagpieState(raw)
	if parseErr != nil {
		return ScanResult{}, Bounded(s.ID(), "运行态文件"+magpieErrorText(parseErr))
	}
	result.Path = s.fallbackPath
	// 兜底路径只有元数据，没有明文密钥：如实标注密钥不可用，让导入侧知道该让用户手工补填。
	result.KeyUnavailable = true
	// 主路径存在却读不了/不是合法 JSON 时，把原因带回去——数据虽可用，但必须让用户知道
	// 看到的不是主配置，否则他改坏的主配置会被静默忽略。「主路径本就不存在」不算降级。
	if boundedReason != magpieMissingText {
		result.DegradedReason = "主配置" + boundedReason + "，已回退到运行态文件"
	}
	return result, nil
}

func (s *magpie) Fetch(ctx context.Context, sourceKey string) (SourceItem, bool, error) {
	result, err := s.Scan(ctx)
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

// readCapped 读一个有上限的文件。ReadFile 之后再判大小仍然要先读进内存，
// 所以先按大小挡一道，避免被误指到的大文件把内存吃满。
func readCapped(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > magpieMaxConfigBytes {
		return nil, errors.New(magpieTooLargeText)
	}
	return os.ReadFile(path)
}

// magpieErrorText 把内部错误折成有界类别文案。不拼入原始错误：JSON 语法错误会带上
// 出错位置的上下文，路径错误会带路径，两者都不该外传。
func magpieErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case IsMissing(err):
		return magpieMissingText
	case errors.Is(err, os.ErrPermission):
		return "权限不足"
	case err.Error() == magpieTooLargeText:
		return magpieTooLargeText
	case errors.As(err, new(*json.SyntaxError)), errors.As(err, new(*json.UnmarshalTypeError)):
		return magpieInvalidJSON
	}
	if strings.Contains(err.Error(), magpieTooLargeText) {
		return magpieTooLargeText
	}
	return magpieReadFailedText
}

// parseMagpieConfig 解析 XDG 源配置。明文密钥在这一步进内存，条目上标 hasKey=true；
// 密钥本身只留在 SourceItem.Key 里（json:"-"），不会出现在任何返回体。
func parseMagpieConfig(raw []byte) (ScanResult, error) {
	var config magpieConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return ScanResult{}, err
	}
	fields := magpieEntryFields(raw)
	result := ScanResult{Items: make([]SourceItem, 0, len(config.Providers))}
	for index, provider := range config.Providers {
		if provider.Off != nil && *provider.Off {
			// off:true 是上游明确停用，与「我们解析不出来」是两回事：单独计数，页面才能
			// 指引用户去 Magpie 里启用，而不是让他以为插件不认这条。
			result.Disabled++
			continue
		}
		// keys[] 是「这条供应商有一组可选密钥」的结构声明：字段在就必须以它为准，
		// 哪怕里面一条可用的都没有，也不许拿顶层 key 顶替——那属于猜。
		if _, present := fields[index]["keys"]; present {
			key, keyErr := magpieListKey(provider.Keys)
			if keyErr != nil {
				result.Skipped++
				continue
			}
			item, ok := mapMagpieConfigItem(provider, key)
			if !ok {
				result.Skipped++
				continue
			}
			item.HasKey = key != ""
			result.Items = append(result.Items, item)
			continue
		}
		// 没有 keys[]：用顶层 key。它缺失或为空串时照常返回该条目，只是没有密钥可下发。
		key := strings.TrimSpace(provider.Key)
		item, ok := mapMagpieConfigItem(provider, key)
		if !ok {
			result.Skipped++
			continue
		}
		item.HasKey = key != ""
		result.Items = append(result.Items, item)
	}
	// 一条密钥都没有（例如整份配置还没填 key）：这时的可用性与兜底路径等价。
	result.KeyUnavailable = !hasAnyKey(result.Items)
	return result, nil
}

// parseMagpieState 解析运行态投影。这里不可能拿到明文密钥，所以条目的 hasKey 一律
// 由调用方置 false，解析不出来就报错，绝不用别处的值顶上。
func parseMagpieState(raw []byte) (ScanResult, error) {
	var state magpieState
	if err := json.Unmarshal(raw, &state); err != nil {
		return ScanResult{}, err
	}
	providers := state.Providers.Providers
	result := ScanResult{Items: make([]SourceItem, 0, len(providers))}
	for _, provider := range providers {
		if provider.Off {
			// 运行态投影的停用语义与源配置一致，同样计入 disabled。
			result.Disabled++
			continue
		}
		item, ok := mapMagpieStateItem(provider)
		if !ok {
			result.Skipped++
			continue
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// mapMagpieConfigItem 把源配置的一条映射成扫描条目。unknown 时返回 false，
// 由调用方计入 skipped——未知结构禁止猜，尤其禁止猜出一个没依据的 baseUrl。
func mapMagpieConfigItem(provider magpieConfigProvider, key string) (SourceItem, bool) {
	id := strings.TrimSpace(provider.ID)
	endpoint, ok := magpieEndpoint(provider.BaseAPI, provider.Anthropic, provider.Responses, provider.Chat)
	if !ok || id == "" {
		return SourceItem{}, false
	}
	item := SourceItem{
		SourceID:  contract.SourceMagpie,
		SourceKey: id,
		Name:      strings.TrimSpace(provider.Name),
		BaseURL:   endpoint,
		APIFormat: magpieFormat(strings.ToLower(strings.TrimSpace(provider.BaseAPI))),
		AppType:   "magpie",
		Key:       key,
		// 自定义请求头要一并带下去：有些中转站要求额外的鉴权头，丢了它这条供应商
		// 导入后就是不可用的，而用户从表面上完全看不出来。
		Headers: provider.Headers,
	}
	applyMagpieModels(&item, provider.Models, nil)
	return item, true
}

// mapMagpieStateItem 映射运行态条目：模型是对象数组，端点字段与源配置同名。
func mapMagpieStateItem(provider magpieStateProvider) (SourceItem, bool) {
	id := strings.TrimSpace(provider.ID)
	endpoint, ok := magpieEndpoint(provider.BaseAPI, provider.Anthropic, provider.Responses, provider.Chat)
	if !ok || id == "" {
		return SourceItem{}, false
	}
	item := SourceItem{
		SourceID:  contract.SourceMagpie,
		SourceKey: id,
		Name:      strings.TrimSpace(provider.Name),
		BaseURL:   endpoint,
		APIFormat: magpieFormat(strings.ToLower(strings.TrimSpace(provider.BaseAPI))),
		AppType:   "magpie",
	}
	models := make([]string, 0, len(provider.Models))
	for _, model := range provider.Models {
		models = append(models, model.ID)
	}
	applyMagpieModels(&item, models, provider.Chosen)
	return item, true
}

// magpieEndpoint 按 baseAPI 选端点；该协议字段为空时按 anthropic → responses → chat
// 取第一个非空（docs/MAGPIE-FORMAT.md 的实测规则）。全都为空、只剩插件型端点时算没有端点。
func magpieEndpoint(baseAPI, anthropic, responses, chat string) (string, bool) {
	trim := func(value string) string { return strings.TrimSpace(value) }
	// 插件型端点不算端点：它选不出来就继续往后找，找不到就是不支持这条（计 skipped）。
	pick := func(value string) (string, bool) {
		value = trim(value)
		if value == "" || magpiePluginEndpoint(value) {
			return "", false
		}
		return value, true
	}
	switch strings.ToLower(trim(baseAPI)) {
	case "chat":
		if value, ok := pick(chat); ok {
			return value, true
		}
	case "responses":
		if value, ok := pick(responses); ok {
			return value, true
		}
	case "anthropic":
		if value, ok := pick(anthropic); ok {
			return value, true
		}
	}
	for _, candidate := range []string{anthropic, responses, chat} {
		if value, ok := pick(candidate); ok {
			return value, true
		}
	}
	return "", false
}

// magpiePluginEndpoint 判断端点是不是 Magpie 内置插件的内部协议（plugin://…）。
//
// 这类供应商（raccoon、trae-cn 这些）的端点由 Magpie 自己接线，不是宿主能直连的 HTTP 地址。
// 原样当 baseUrl 下发，用户会拿到一条永远连不通、页面却显示正常的供应商——正是本包
// 一直在挡的那类静默失败，所以它和「没有端点」同处置，不计入 disabled。
func magpiePluginEndpoint(endpoint string) bool {
	scheme, _, found := strings.Cut(endpoint, "://")
	return found && strings.EqualFold(scheme, "plugin")
}

// applyMagpieModels 填 model 与 models：chosen 只在运行态出现，作 model 与 models 的兜底。
func applyMagpieModels(item *SourceItem, models, chosen []string) {
	if len(chosen) > 0 {
		item.Model = strings.TrimSpace(chosen[0])
	}
	item.Models = dedupe(models)
	if len(item.Models) == 0 {
		item.Models = dedupe(chosen)
	}
	if item.Model == "" && len(item.Models) > 0 {
		item.Model = item.Models[0]
	}
}

// magpieListKey 按契约第 5 节取 keys[] 里第一条启用项的密钥：off 缺省即启用，
// 显式 true 排除。解析不出来只让这一条没有密钥，不影响整份配置。
func magpieListKey(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var keys []magpieConfigKey
	if err := json.Unmarshal(raw, &keys); err != nil {
		return "", err
	}
	for _, entry := range keys {
		if entry.Off != nil && *entry.Off {
			continue
		}
		if key := strings.TrimSpace(entry.Key); key != "" {
			return key, nil
		}
	}
	return "", nil
}

// magpieEntryFields 取出每条供应商实际出现了哪些键。
//
// 为什么需要它：json 反序列化只回答「值是什么」，不回答「字段在不在」，而这两者
// 在 keys[] 上语义相反——字段缺失走顶层 key，字段存在就必须以它为准。
func magpieEntryFields(raw []byte) []map[string]json.RawMessage {
	var loose struct {
		Providers []map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(raw, &loose); err != nil {
		return nil
	}
	return loose.Providers
}

func hasAnyKey(items []SourceItem) bool {
	for _, item := range items {
		if item.HasKey {
			return true
		}
	}
	return false
}

func magpieFormat(baseAPI string) string {
	switch baseAPI {
	case "responses":
		return contract.FormatOpenAIResponses
	case "anthropic":
		return contract.FormatAnthropic
	default:
		return contract.FormatOpenAI
	}
}
