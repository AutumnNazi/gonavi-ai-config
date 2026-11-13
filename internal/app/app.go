// Package app 把来源扫描、宿主能力调用与命令注册组装成 GoNavi 插件后端。
// 诊断一律写 stderr，命令返回值只承载协议字段。
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	goplugin "github.com/Syngnat/GoNavi/sdk/go"

	"gonavi-ai-config/internal/contract"
	"gonavi-ai-config/internal/sources"
)

// 错误码用 JSON-RPC 保留段之外的自定义段，宿主透传给页面。
const (
	errBadArgument = 4100
	errInternal    = 4199
)

// 宿主能力名，与契约第 2 节一致。
const (
	capabilityAIProvidersRead   = "ai.providers.read"
	capabilityAIProvidersImport = "ai.providers.import"
)

// errHostRejected 表示宿主以信封返回失败，而不是传输层错误。
var errHostRejected = errors.New("host capability rejected the request")

// hostCaller 是宿主能力调用的最小接口。订阅 SDK 的具体类型只为在真实进程里取值，
// 测试用假桩实现同一接口即可断言请求体形状。
type hostCaller interface {
	CallCapability(ctx context.Context, method string, arguments any) (json.RawMessage, error)
}

// App 汇集插件后端运行期状态。
type App struct {
	registered []sources.Source
	scanner    *sources.Scanner
	host       hostCaller
}

// New 构造后端。host 为 nil 时命令仍可用，只是 import.apply 与冲突判定无法落地——
// 独立运行与单元测试走这条路径。
func New(host hostCaller) *App {
	return newApp(sources.New(), host)
}

// newApp 是 app.New 的装配主体。抽出来是为了让测试走与生产完全相同的装配路径：
// 冲突判定此前在真实宿主里恒为 false，正是因为这处装配把 lister 写成了 nil，
// 而注入式测试各自新建一份 Scanner，恰好绕开了这条路径。
func newApp(registered []sources.Source, host hostCaller) *App {
	var lister sources.ProviderLister
	if host != nil {
		lister = hostProviderLister{host: host}
	}
	return &App{
		registered: registered,
		scanner:    sources.NewScanner(registered, lister),
		host:       host,
	}
}

// scannerFor 按请求装配扫描器。冲突判定要调宿主只读能力，而宿主句柄只在请求 context 里
// 下发（真实入口是 app.New(nil)），所以这里必须现取，不能只依赖构造期注入。
func (a *App) scannerFor(ctx context.Context) *sources.Scanner {
	if host, ok := hostFromContext(ctx); ok {
		return sources.NewScanner(a.registered, hostProviderLister{host: host})
	}
	return a.scanner
}

// NewWithScanner 用自定义来源与宿主桩构造后端，供测试注入夹具。
func NewWithScanner(scanner *sources.Scanner, host hostCaller) *App {
	return &App{scanner: scanner, host: host}
}

// Handlers 返回命令注册表：键是清单 contributes.commands 里的命令 id。
func (a *App) Handlers() map[string]goplugin.Handler {
	return map[string]goplugin.Handler{
		contract.CommandSourcesScan: a.handleScan,
		contract.CommandImportApply: a.handleImport,
	}
}

// hostFromContext 取当前请求绑定的宿主网关。SDK 的 Host 只能从 context 拿，
// 因此真实进程里 import.apply 必须带着 handler 的 context 进来。
//
// 声明为变量是为了给测试留接缝：宿主能力只在请求 context 里下发，包外无法构造那个 context，
// 若写成普通函数，任何「按请求取宿主」的行为都只能靠集成测试覆盖。
var hostFromContext = func(ctx context.Context) (hostCaller, bool) {
	host, ok := goplugin.HostFromContext(ctx)
	if !ok {
		return nil, false
	}
	return host, true
}

// decodeArguments 严格解码命令入参：契约要求 additionalProperties=false，
// 多传字段即视为参数非法，避免调用方以为多传的字段生效了。
func decodeArguments(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &goplugin.Error{Code: errBadArgument, Message: "invalid arguments"}
	}
	return nil
}
