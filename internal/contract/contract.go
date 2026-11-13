// Package contract 定义插件命令的输入输出形状与来源标识，是 docs/CONTRACT.md 第 3、4 节的
// 唯一 Go 映射。字段名、枚举与上限在此固化，调用方不得就地改写协议。
package contract

import "encoding/json"

// 来源标识，对应清单 inputSchema 的 enum。
const (
	SourceCCSwitch = "cc-switch"
	SourceMagpie   = "magpie"
	SourceCCGUI    = "ccgui"
	SourceOpencode = "opencode"
	SourceMimocode = "mimocode"
)

// apiFormat 枚举，与契约第 2 节宿主侧枚举一致。
const (
	FormatOpenAI          = "openai"
	FormatOpenAIResponses = "openai-responses"
	FormatAnthropic       = "anthropic"
	FormatGemini          = "gemini"
)

// 命令与上限。上限是契约写死的，不随调用方变化。
const (
	CommandSourcesScan = "sources.scan"
	CommandImportApply = "import.apply"

	MaxSources        = 5
	MaxImportItems    = 100
	MaxSourceKeyBytes = 200
	// MaxHostImportBytes 是宿主能力 ai.providers.import 的 MaxArgsBytes（契约第 2 节，256 KiB）。
	// 插件按 100 条校验，但请求体还可能因超长密钥/请求头而超限，那会被宿主整批拒绝，
	// 用户只会看到「宿主认为参数不合法」。所以在调用前先量一次大小，超了自己拦。
	MaxHostImportBytes = 256 << 10
)

// ScanRequest 是 sources.scan 的入参。Sources 为空表示清单里的所有来源都扫。
type ScanRequest struct {
	Sources []string `json:"sources"`
}

// ScanSourceStatus 描述一个来源的探测结果。
type ScanSourceStatus struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Detected       bool   `json:"detected"`
	Path           string `json:"path"`
	Count          int    `json:"count"`
	Error          string `json:"error"`
	KeyUnavailable bool   `json:"keyUnavailable,omitempty"`
	// Skipped 是本来源里**结构不支持**、因而没有返回的条目数（缺端点、字段形态不认识等）。
	// Disabled 是本来源里被上游主动**停用**、因而没有返回的条目数（magpie 的 off:true）。
	//
	// 两者必须分开：把「用户自己关掉的」说成「插件不认识」会让人以为功能有缺陷，
	// 而实际上用户在上游启用一下就能导入。两个计数都必须回传——来源读到过这些条目却
	// 一条不说，就是静默丢数据，用户只会看到数字对不上。逐条原因通道留待后续版本。
	Skipped  int `json:"skipped,omitempty"`
	Disabled int `json:"disabled,omitempty"`
	// Notice 是本来源「数据可用、但有事要说明」的提示。当前唯一来源是 magpie 主配置损坏后
	// 回退到运行态投影：它不是在报错（数据照常返回），所以不置 error 状态，只作为一行提示显示。
	Notice string `json:"notice,omitempty"`
}

// ScanItem 是返回给页面的单条可选供应商。这里刻意没有密钥字段，也没有任何
// 可以承载密钥的宽松容器——序列化后不可能出现密钥。
type ScanItem struct {
	SourceID  string   `json:"sourceId"`
	SourceKey string   `json:"sourceKey"`
	Name      string   `json:"name"`
	BaseURL   string   `json:"baseUrl"`
	APIFormat string   `json:"apiFormat"`
	Model     string   `json:"model"`
	Models    []string `json:"models"`
	HasKey    bool     `json:"hasKey"`
	AppType   string   `json:"appType"`
	Current   bool     `json:"current"`
	Conflict  bool     `json:"conflict"`
}

// ScanResult 是 sources.scan 的返回体。
type ScanResult struct {
	Sources []ScanSourceStatus `json:"sources"`
	Items   []ScanItem         `json:"items"`
	// ConflictUnavailable 为 true 表示本次没能取到宿主已有的供应商列表（能力被拒或宿主不可用），
	// items[].conflict 因此全为 false——那是「没标」而不是「没有冲突」。页面据此给一行提示，
	// 否则用户会把「没标记」当成「不重复」，导入时却被跳过。
	ConflictUnavailable bool `json:"conflictUnavailable,omitempty"`
}

// ImportItemRef 是 import.apply 入参里的一条引用。
type ImportItemRef struct {
	SourceID  string `json:"sourceId"`
	SourceKey string `json:"sourceKey"`
	Overwrite bool   `json:"overwrite"`
}

// ImportRequest 是 import.apply 的入参。
type ImportRequest struct {
	Items []ImportItemRef `json:"items"`
}

// HostProviderRequest 是发给宿主能力 ai.providers.import 的单条请求。
// APIKey 带 omitempty：降级来源（magpie）不下发该字段，宿主持无密钥供应商。
type HostProviderRequest struct {
	Name        string            `json:"name"`
	BaseURL     string            `json:"baseUrl"`
	APIFormat   string            `json:"apiFormat"`
	Model       string            `json:"model,omitempty"`
	Models      []string          `json:"models,omitempty"`
	APIKey      string            `json:"apiKey,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
	Overwrite   bool              `json:"overwrite"`
}

// HostImportArguments 是 ai.providers.import 的 capability arguments。
type HostImportArguments struct {
	Providers []HostProviderRequest `json:"providers"`
}

// HostImportItem 是宿主返回的单条结果。
type HostImportItem struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	ProviderID string `json:"providerId"`
	Reason     string `json:"reason"`
}

// HostImportData 是宿主返回体里的 data 段。
type HostImportData struct {
	Imported int              `json:"imported"`
	Skipped  int              `json:"skipped"`
	Failed   int              `json:"failed"`
	Items    []HostImportItem `json:"items"`
}

// HostEnvelope 是宿主能力的统一信封，成功与失败共用一个形状。
type HostEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *HostError      `json:"error,omitempty"`
}

// HostError 是失败信封里的错误体。
type HostError struct {
	Code string `json:"code"`
}
