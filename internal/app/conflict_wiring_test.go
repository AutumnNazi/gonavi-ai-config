package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"gonavi-ai-config/internal/contract"
	"gonavi-ai-config/internal/sources"
)

// stubSource 是最小来源桩：固定一条条目，路径指向当前进程可执行文件（必然存在）。
type stubSource struct {
	item sources.SourceItem
	// id 为空时按 cc-switch 处理，方便只关心条目的用例少写一个字段。
	id string
}

func (s stubSource) ID() string {
	if s.id != "" {
		return s.id
	}
	return contract.SourceCCSwitch
}
func (s stubSource) Label() string    { return "stub" }
func (s stubSource) Path() string     { return os.Args[0] }
func (s stubSource) PathError() error { return nil }

func (s stubSource) Scan(context.Context) (sources.ScanResult, error) {
	return sources.ScanResult{Path: s.Path(), Items: []sources.SourceItem{s.item}}, nil
}

func (s stubSource) Fetch(context.Context, string) (sources.SourceItem, bool, error) {
	return s.item, true, nil
}

// TestScanHandlerMarksConflictsFromRequestHost 走真实 handler 路径，并模拟真实入口的
// 关键约束：app.New(nil)——构造期没有宿主句柄，宿主只在请求 context 里下发。
// 之前 conflict 在真实宿主里恒为 false，正是因为在构造期取宿主，取到的是 nil。
func TestScanHandlerMarksConflictsFromRequestHost(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"providers":[{"name":"夹具站","baseUrl":"https://fixture.example.com/v1"}]}}`)}
	original := hostFromContext
	hostFromContext = func(context.Context) (hostCaller, bool) { return host, true }
	t.Cleanup(func() { hostFromContext = original })

	application := newApp([]sources.Source{stubSource{item: sources.SourceItem{
		SourceID:  contract.SourceCCSwitch,
		SourceKey: "claude:fixture",
		Name:      "夹具站",
		BaseURL:   "https://fixture.example.com/v1",
		APIFormat: "anthropic",
		HasKey:    true,
	}}}, nil)

	value, err := application.Handlers()[contract.CommandSourcesScan](context.Background(), json.RawMessage(`{"sources":["cc-switch"]}`))
	if err != nil {
		t.Fatalf("scan handler: %v", err)
	}
	result, ok := value.(contract.ScanResult)
	if !ok {
		t.Fatalf("unexpected result type %T", value)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	if !result.Items[0].Conflict {
		t.Fatal("按请求取宿主失败：conflict 恒为 false")
	}
	if host.method != capabilityAIProvidersRead {
		t.Fatalf("capability = %q, want %q", host.method, capabilityAIProvidersRead)
	}
}

// TestImportForwardsSourceHeaders 盯住「自定义请求头必须随导入下发」这条：
// 有些中转站靠额外的鉴权头工作，header 一旦在链路上被丢掉，导进来的供应商就是不可用的，
// 而表面上完全看不出来——所以这条不能只靠编译通过。
func TestImportForwardsSourceHeaders(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"imported":1,"skipped":0,"failed":0,"items":[{"index":0,"name":"夹具站","status":"imported","providerId":"provider-1","reason":""}]}}`)}
	original := hostFromContext
	hostFromContext = func(context.Context) (hostCaller, bool) { return host, true }
	t.Cleanup(func() { hostFromContext = original })

	application := newApp([]sources.Source{stubSource{
		id: contract.SourceMagpie,
		item: sources.SourceItem{
			SourceID:  contract.SourceMagpie,
			SourceKey: "with-headers",
			Name:      "夹具站",
			BaseURL:   "https://fixture.example.com/v1",
			APIFormat: "openai",
			HasKey:    true,
			Key:       "sk-fixture-header-key",
			Headers:   map[string]string{"X-Tenant": "acme"},
		},
	}}, nil)

	raw, err := application.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"magpie","sourceKey":"with-headers"}]}`))
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	if raw == nil {
		t.Fatal("handleImport 返回空")
	}
	var arguments contract.HostImportArguments
	if err := json.Unmarshal(host.arguments, &arguments); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if len(arguments.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(arguments.Providers))
	}
	if got := arguments.Providers[0].Headers["X-Tenant"]; got != "acme" {
		t.Fatalf("自定义请求头没有随导入下发：%v", arguments.Providers[0].Headers)
	}
	if arguments.Providers[0].APIKey != "sk-fixture-header-key" {
		t.Fatal("密钥没有随导入下发")
	}
}

// TestImportForwardsFixtureHeadersThroughRealSource 把「请求头保真」从桩推进到真实来源：
// 走生产来源注册表 + magpie 夹具（夹具里 imported 带 headers），断言请求体里出现那个头。
//
// 上面那条用例用的是桩来源：桩直接给出 Headers，解析器把夹具里的 headers 整段丢掉时它照样绿。
// 有些中转站靠额外鉴权头工作，头在链路上丢了只会导出一条看起来正常、实际调不通的供应商，
// 所以这条链路必须有经过解析器的证据。
func TestImportForwardsFixtureHeadersThroughRealSource(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"imported":1,"skipped":0,"failed":0,"items":[{"index":0,"name":"千刀","status":"imported","providerId":"provider-1","reason":""}]}}`)}
	application, _ := newTestApp(t, host)

	raw, err := application.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"magpie","sourceKey":"imported"}]}`))
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	if raw == nil {
		t.Fatal("handleImport 返回空")
	}
	var arguments contract.HostImportArguments
	if err := json.Unmarshal(host.arguments, &arguments); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if len(arguments.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(arguments.Providers))
	}
	if got := arguments.Providers[0].Headers["x-fixture-header"]; got != "v1" {
		t.Fatalf("夹具里的自定义请求头没有随导入下发：%v", arguments.Providers[0].Headers)
	}
	if arguments.Providers[0].APIKey == "" {
		t.Fatal("夹具里这条带明文密钥，必须随导入下发")
	}
}
