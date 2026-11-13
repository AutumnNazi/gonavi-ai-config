package sources

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
)

// ProviderRef 是宿主「已存在供应商」的最小投影，只带判重需要的名称与地址。
type ProviderRef struct {
	Name    string
	BaseURL string
}

// ProviderLister 是宿主只读能力 host.ai.providers.read（ai.providers.read）的最小接口。
type ProviderLister interface {
	ListProviders(ctx context.Context) ([]ProviderRef, error)
}

// Scanner 把已注册来源组织成一次 sources.scan。
type Scanner struct {
	sources []Source
	lister  ProviderLister
}

// NewScanner 构造扫描器。lister 为 nil 时不做冲突判定——冲突只是提示，
// 不该因为拿不到宿主元数据就让整次扫描失败。
func NewScanner(sources []Source, lister ProviderLister) *Scanner {
	return &Scanner{sources: sources, lister: lister}
}

// Scan 按入参执行扫描。单个来源失败只写该来源的 error，其余来源照常返回。
func (scanner *Scanner) Scan(ctx context.Context, request contract.ScanRequest) (contract.ScanResult, error) {
	wanted, err := normalizeSources(request.Sources)
	if err != nil {
		return contract.ScanResult{}, err
	}
	result := contract.ScanResult{
		Sources: make([]contract.ScanSourceStatus, 0, len(wanted)),
		Items:   make([]contract.ScanItem, 0),
	}
	// sourceKey 唯一性在单次 scan 内保证：契约要求同一返回体内 sourceKey 不重复。
	seenKeys := make(map[string]bool)
	for _, id := range wanted {
		source := scanner.find(id)
		if source == nil {
			// 入参校验认识这个 id、但调用方没把它注册进 scanner：这是装配错误，
			// 必须报成该来源的错误，而不是 nil 解引用把整次扫描连同进程一起带走。
			result.Sources = append(result.Sources, contract.ScanSourceStatus{
				ID:    id,
				Label: id,
				Error: id + ": 来源未注册",
			})
			continue
		}
		status := contract.ScanSourceStatus{ID: id, Label: source.Label(), Path: source.Path()}
		if pathErr := source.PathError(); pathErr != nil {
			// 路径没解析出来：必须留空 path 并写明原因。此时给一个相对路径比报错更糟——
			// 它看起来合法，却随进程工作目录飘走，属于静默失败。
			status.Path = ""
			status.Detected = false
			status.Error = source.ID() + ": " + pathErr.Error()
			result.Sources = append(result.Sources, status)
			continue
		}
		if _, err := statPath(source.Path()); err != nil {
			// 文件不存在不是错误，只是没探测到。
			status.Detected = false
			status.Error = ""
			result.Sources = append(result.Sources, status)
			continue
		}
		status.Detected = true
		scanned, scanErr := source.Scan(ctx)
		if scanErr != nil {
			status.Error = errorText(scanErr)
			result.Sources = append(result.Sources, status)
			continue
		}
		status.KeyUnavailable = scanned.KeyUnavailable
		// 降级不是失败：数据照常返回，只把「读的不是主数据源」这件事作为提示带出去。
		status.Notice = scanned.DegradedReason
		// count 只数真正进入返回体的条目：跨来源重名命中时被去掉的那些必须计入 skipped，
		// 否则 count + skipped + disabled 与来源条目总数对不上，条目在页面上凭空少一条。
		added := 0
		deduplicated := 0
		for _, item := range scanned.Items {
			key := item.SourceID + "\x00" + item.SourceKey
			if seenKeys[key] {
				deduplicated++
				continue
			}
			seenKeys[key] = true
			result.Items = append(result.Items, contract.ScanItem{
				SourceID:  item.SourceID,
				SourceKey: item.SourceKey,
				Name:      item.Name,
				BaseURL:   item.BaseURL,
				APIFormat: item.APIFormat,
				Model:     item.Model,
				Models:    item.Models,
				HasKey:    item.HasKey,
				AppType:   item.AppType,
				Current:   item.Current,
			})
			added++
		}
		status.Count = added
		status.Skipped = scanned.Skipped + deduplicated
		status.Disabled = scanned.Disabled
		result.Sources = append(result.Sources, status)
	}
	result.ConflictUnavailable = !scanner.markConflicts(ctx, result.Items)
	return result, nil
}

// markConflicts 调用宿主只读能力取已存在供应商，按第 2 节的判重身份标冲突。
// 返回 false 表示「想标但没标成」——能力被拒或宿主不可用。这不是扫描失败（冲突只是提示），
// 但必须让调用方知道 items[].conflict 全为 false 是「没标」而不是「没有冲突」。
// 没有注入 lister（独立运行）不算不可用：那是装配上就不做冲突判定。
func (scanner *Scanner) markConflicts(ctx context.Context, items []contract.ScanItem) bool {
	if scanner.lister == nil || len(items) == 0 {
		return true
	}
	existing, err := scanner.lister.ListProviders(ctx)
	if err != nil {
		return false
	}
	known := make(map[string]bool, len(existing))
	for _, provider := range existing {
		if key := identity(provider.Name, provider.BaseURL); key != "" {
			known[key] = true
		}
	}
	for index := range items {
		if known[identity(items[index].Name, items[index].BaseURL)] {
			items[index].Conflict = true
		}
	}
	return true
}

// Fetch 按 (sourceId, sourceKey) 现取一条完整配置（含密钥）。
//
// 未注册的来源、超长的 sourceKey 都返回错误而不是 found=false：它们与「来源里没有这条」
// 不是一回事，混进同一个 reason 会让调用方把「请求不合法」当成「配置被删」。
func (scanner *Scanner) Fetch(ctx context.Context, sourceID, sourceKey string) (SourceItem, bool, error) {
	source := scanner.find(sourceID)
	if source == nil {
		return SourceItem{}, false, Bounded(sourceID, "来源未注册")
	}
	if len(sourceKey) > contract.MaxSourceKeyBytes {
		return SourceItem{}, false, Bounded(sourceID, "sourceKey 超长")
	}
	return source.Fetch(ctx, sourceKey)
}

func (scanner *Scanner) find(id string) Source {
	for _, source := range scanner.sources {
		if source.ID() == id {
			return source
		}
	}
	return nil
}

// sourceIDs 是 sources.scan 认识的全部来源，顺序即页面默认顺序。集中一处，新增来源时
// 默认值与入参校验一起生效，不会出现「缺省能扫、显式传就被拒」的半接线状态。
func sourceIDs() []string {
	return []string{
		contract.SourceCCSwitch,
		contract.SourceMagpie,
		contract.SourceCCGUI,
		contract.SourceOpencode,
		contract.SourceMimocode,
	}
}

// normalizeSources 校验入参来源并去重；空入参按契约默认为全部来源都扫。
func normalizeSources(requested []string) ([]string, error) {
	if len(requested) > contract.MaxSources {
		return nil, errors.New("too many sources")
	}
	if len(requested) == 0 {
		return sourceIDs(), nil
	}
	out := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	known := make(map[string]bool, len(sourceIDs()))
	for _, id := range sourceIDs() {
		known[id] = true
	}
	for _, raw := range requested {
		id := strings.TrimSpace(raw)
		if !known[id] {
			return nil, errors.New("unsupported source")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// errorText 取有界错误文案，绝不带上内部堆栈或参数。
func errorText(err error) string {
	var message *ErrMessage
	if errors.As(err, &message) {
		return message.Text
	}
	return "扫描失败"
}

// EncodeScanResult 是测试与独立入口共用的序列化入口：密钥断言就挂在它上面。
func EncodeScanResult(result contract.ScanResult) ([]byte, error) {
	return json.Marshal(result)
}
