package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
	"github.com/AutumnNazi/gonavi-ai-config/internal/sources"
)

// 夹具与 internal/sources 共用 testdata/，期望密钥一律从夹具推导。
func testdataPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "testdata")
	return filepath.Join(append([]string{root}, parts...)...)
}

// ccSwitchFixture 用 testdata 的 cc-switch 夹具建最小库，返回库路径与 claude 那条的密钥。
func ccSwitchFixture(t *testing.T) (string, string) {
	t.Helper()
	raw, err := os.ReadFile(testdataPath(t, "cc-switch", "providers.json"))
	if err != nil {
		t.Fatalf("读夹具失败：%v", err)
	}
	var rows []struct {
		ID             string          `json:"id"`
		AppType        string          `json:"app_type"`
		Name           string          `json:"name"`
		IsCurrent      int             `json:"is_current"`
		WebsiteURL     string          `json:"website_url"`
		Meta           json.RawMessage `json:"meta"`
		SettingsConfig json.RawMessage `json:"settings_config"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("夹具不是合法 JSON：%v", err)
	}
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("create table providers (id text, app_type text, name text, settings_config text, website_url text, meta text, is_current boolean)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	secret := ""
	for index, row := range rows {
		if _, err := db.Exec(
			"insert into providers (id, app_type, name, settings_config, website_url, meta, is_current) values (?,?,?,?,?,?,?)",
			row.ID, row.AppType, row.Name, string(row.SettingsConfig), row.WebsiteURL, string(row.Meta), row.IsCurrent,
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
		_ = index
		if row.AppType == "claude" && secret == "" {
			var config struct {
				Env struct {
					Token string `json:"ANTHROPIC_AUTH_TOKEN"`
				} `json:"env"`
			}
			if err := json.Unmarshal(row.SettingsConfig, &config); err != nil {
				t.Fatalf("夹具 settings_config 不是合法 JSON：%v", err)
			}
			secret = config.Env.Token
		}
	}
	if secret == "" {
		t.Fatal("夹具里没有 claude 密钥，密钥断言会空过")
	}
	return path, secret
}

// fakeHost 是最小宿主桩：记录收到的能力请求，并回放预置响应。
type fakeHost struct {
	method    string
	arguments json.RawMessage
	response  json.RawMessage
	err       error
}

func (h *fakeHost) CallCapability(_ context.Context, method string, arguments any) (json.RawMessage, error) {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, err
	}
	h.method = method
	h.arguments = encoded
	if h.err != nil {
		return nil, h.err
	}
	return h.response, nil
}

// newTestApp 建后端并回带 claude 那条的密钥供断言使用。
//
// 走的是生产装配（app.New 的装配主体 newApp + sources.New 的注册表），只用夹具覆盖 6 个路径
// 变量：断言因此盯的是「生产注册了哪些来源、各来源的覆盖变量有没有接上」，
// 而不是测试另拼的一份来源清单——旧写法自己拼两个来源，恰好把注册表漏接掩盖掉了。
func newTestApp(t *testing.T, host hostCaller) (*App, string) {
	t.Helper()
	dbPath, secret := ccSwitchFixture(t)
	t.Setenv(sources.EnvCCSwitchDB, dbPath)
	t.Setenv(sources.EnvMagpieConfig, magpiePrimaryFixturePath(t))
	t.Setenv(sources.EnvMagpieState, testdataPath(t, "magpie", "providers-state.json"))
	t.Setenv(sources.EnvCCGUIConfig, testdataPath(t, "ccgui", "config.json"))
	t.Setenv(sources.EnvOpencodeConfig, testdataPath(t, "opencode", "opencode.json"))
	t.Setenv(sources.EnvMimocodeConfig, testdataPath(t, "mimocode", "mimocode.jsonc"))
	return newApp(sources.New(), host), secret
}

// magpiePrimaryFixturePath 是契约 §4.1 里 magpie 的主路径夹具（XDG 源配置，明文密钥）。
func magpiePrimaryFixturePath(t *testing.T) string {
	t.Helper()
	path := testdataPath(t, "magpie", "providers.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("magpie 主路径夹具缺失：%v", err)
	}
	return path
}

// magpieFixtureKey 按契约 §5 从主路径夹具里取一条条目的明文密钥：keys[] 存在时取其中
// off:false 的第一条，否则用顶层 key。期望值只有一个来源，不在测试里另写一份常量。
func magpieFixtureKey(t *testing.T, id string) string {
	t.Helper()
	raw, err := os.ReadFile(magpiePrimaryFixturePath(t))
	if err != nil {
		t.Fatalf("读 magpie 夹具失败：%v", err)
	}
	var doc struct {
		Providers []struct {
			ID   string `json:"id"`
			Key  string `json:"key"`
			Keys []struct {
				Key string `json:"key"`
				Off bool   `json:"off"`
			} `json:"keys"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("magpie 夹具不是合法 JSON：%v", err)
	}
	for _, entry := range doc.Providers {
		if entry.ID != id {
			continue
		}
		if entry.Keys != nil {
			for _, candidate := range entry.Keys {
				if !candidate.Off && candidate.Key != "" {
					return candidate.Key
				}
			}
			return ""
		}
		return entry.Key
	}
	t.Fatalf("magpie 夹具里没有 %s", id)
	return ""
}

// TestScanHandlerShape 命令层返回体的字段与契约第 4 节一致。
func TestScanHandlerShape(t *testing.T) {
	app, _ := newTestApp(t, nil)
	raw, err := app.handleScan(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handleScan: %v", err)
	}
	result, ok := raw.(contract.ScanResult)
	if !ok {
		t.Fatalf("返回类型 = %T", raw)
	}
	// 空入参 = 扫契约 §4.1 的全部 5 个来源，顺序即页面默认顺序。
	wantIDs := []string{
		contract.SourceCCSwitch, contract.SourceMagpie, contract.SourceCCGUI,
		contract.SourceOpencode, contract.SourceMimocode,
	}
	if len(result.Sources) != len(wantIDs) {
		t.Fatalf("sources = %d, want %d", len(result.Sources), len(wantIDs))
	}
	for index, status := range result.Sources {
		if status.ID != wantIDs[index] {
			t.Errorf("sources[%d].id = %q, want %q", index, status.ID, wantIDs[index])
		}
		if !status.Detected || status.Error != "" {
			t.Errorf("来源 %s 应探测到且无错误：%+v", status.ID, status)
		}
	}
	// 夹具：cc-switch 11 行中 3 行被跳过得 8 条；magpie 主路径 6 条里 off 与无端点各跳 1 条得 4 条。
	byID := map[string]contract.ScanSourceStatus{}
	total := 0
	for _, status := range result.Sources {
		byID[status.ID] = status
		total += status.Count
	}
	if byID[contract.SourceCCSwitch].Count != 8 || byID[contract.SourceMagpie].Count != 4 {
		t.Errorf("count = cc-switch %d / magpie %d, want 8/4",
			byID[contract.SourceCCSwitch].Count, byID[contract.SourceMagpie].Count)
	}
	// magpie 主路径是带明文密钥的 XDG 源配置：v1.2 起 keyUnavailable 必须为 false
	// （旧期望 true 对应的是「只读运行态投影」的旧语义）。
	if byID[contract.SourceMagpie].KeyUnavailable {
		t.Error("magpie 主路径有明文密钥，不该带 keyUnavailable")
	}
	// 后三个来源的夹具各自必须扫出条目：count=0 意味着夹具与解析器已经对不上了。
	for _, id := range wantIDs[2:] {
		if byID[id].Count == 0 {
			t.Errorf("来源 %s 的夹具没有扫出任何条目", id)
		}
	}
	if len(result.Items) != total {
		t.Errorf("items = %d, want %d（各来源 count 合计）", len(result.Items), total)
	}

	// 未知字段严格拒绝：契约声明 additionalProperties=false。
	if _, err := app.handleScan(context.Background(), json.RawMessage(`{"sources":[],"extra":1}`)); err == nil {
		t.Error("多传字段应被拒绝")
	}
}

// TestImportApplyRequestShape 断言发给宿主 ai.providers.import 的请求体形状。
func TestImportApplyRequestShape(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"imported":2,"skipped":0,"failed":0,"items":[{"index":0,"name":"千刀","status":"imported","providerId":"provider-8b190256","reason":""},{"index":1,"name":"百倍","status":"imported","providerId":"provider-magpie-1","reason":""}]}}`)}
	app, fixtureSecret := newTestApp(t, host)

	raw, err := app.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"cc-switch","sourceKey":"claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e","overwrite":true},{"sourceId":"cc-switch","sourceKey":"claude:已删除的条目"},{"sourceId":"magpie","sourceKey":"multi-key"}]}`))
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	if host.method != capabilityAIProvidersImport {
		t.Fatalf("能力名 = %q, want %q", host.method, capabilityAIProvidersImport)
	}
	var arguments contract.HostImportArguments
	if err := json.Unmarshal(host.arguments, &arguments); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	// 第 2 条（下标 1）取不到配置，不进请求体；下发的 2 条按下标顺序是调用方 0 与 2。
	if len(arguments.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(arguments.Providers))
	}
	first := arguments.Providers[0]
	if first.Name != "千刀" || first.BaseURL != "https://api.zzzcoding.org" || first.APIFormat != "anthropic" {
		t.Errorf("第一条字段不符：%+v", first)
	}
	// 夹具里 claude 的模型集合是 ANTHROPIC_MODEL 与 DEFAULT_* 去重后的有序集合。
	if first.Model != "claude-opus-5[1M]" || strings.Join(first.Models, "|") != "claude-opus-5[1M]|claude-opus-5" {
		t.Errorf("model/models 不符：%+v", first)
	}
	if first.APIKey != fixtureSecret {
		t.Error("有明文密钥的来源必须把密钥下发（这是唯一需要的通路）")
	}
	if !first.Overwrite {
		t.Error("overwrite 应原样透传")
	}
	// magpie 走主路径（XDG 源配置）时带明文密钥：v1.2 起必须原样下发；只有兜底到运行态
	// 投影才不下发 apiKey。旧期望写的是后者，对应的是「magpie 只有掩码」的旧语义。
	second := arguments.Providers[1]
	magpieKey := magpieFixtureKey(t, "multi-key")
	if magpieKey == "" {
		t.Fatal("夹具里的 multi-key 没有明文密钥，密钥下发断言会空过")
	}
	if second.APIKey != magpieKey {
		t.Errorf("magpie 主路径的明文密钥必须原样下发：长度 %d, want %d（不回显密钥本体）",
			len(second.APIKey), len(magpieKey))
	}
	if second.Name != "多密钥站" || second.APIFormat != contract.FormatOpenAIResponses {
		t.Errorf("第二条字段不符：%+v", second)
	}

	// 请求体里不得出现除来源密钥之外的密钥字段名差异：只有 apiKey 一个字段承载密钥。
	var rawArguments map[string]any
	if err := json.Unmarshal(host.arguments, &rawArguments); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := rawArguments["apiKey"]; ok {
		t.Error("apiKey 只能出现在 providers[] 里")
	}

	result, ok := raw.(importApplyResult)
	if !ok {
		t.Fatalf("返回类型 = %T", raw)
	}
	if result.Resolved != 2 {
		t.Errorf("resolved = %d, want 2", result.Resolved)
	}
	// 下标必须是调用方下标：0..n-1，中间缺失的那条占住自己的下标，
	// 后面被顶上的宿主下标（这里是 1）绝不能直接透出。
	if len(result.Items) != 3 {
		t.Fatalf("items = %d, want 3（每个调用方条目都要有结果）", len(result.Items))
	}
	for index, item := range result.Items {
		if item.Index != index {
			t.Errorf("items[%d].index = %d, want %d（宿主下标不得透出）", index, item.Index, index)
		}
	}
	if result.Items[0].Status != "imported" || result.Items[0].ProviderID != "provider-8b190256" {
		t.Errorf("第 1 条应为 imported 且带上宿主生成的 providerId，得到 %+v", result.Items[0])
	}
	if result.Items[1].Status != "failed" || result.Items[1].Reason != "source_missing" {
		t.Errorf("第 2 条应记 failed/source_missing，得到 %+v", result.Items[1])
	}
	if result.Items[2].Status != "imported" || result.Items[2].ProviderID != "provider-magpie-1" {
		t.Errorf("第 3 条应按宿主请求体顺序回填（宿主 index=1），得到 %+v", result.Items[2])
	}
	if result.Imported != 2 || result.Failed != 1 || result.Skipped != 0 {
		t.Errorf("计数与 items 不自洽：imported=%d failed=%d skipped=%d", result.Imported, result.Failed, result.Skipped)
	}
}

// TestImportApplyPassesThroughCancelled 宿主中途取消时必须原样透传：页面要靠它说明
// 「计数只覆盖取消前处理过的部分」，否则用户会把没轮到的条目当成已经处理完。
func TestImportApplyPassesThroughCancelled(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"imported":1,"skipped":0,"failed":1,"cancelled":true,"items":[{"index":0,"name":"千刀","status":"imported","providerId":"provider-1","reason":""},{"index":1,"name":"二","status":"failed","providerId":"","reason":"cancelled"}]}}`)}
	application, _ := newTestApp(t, host)

	raw, err := application.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"cc-switch","sourceKey":"claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e"},{"sourceId":"magpie","sourceKey":"multi-key"}]}`))
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	result, ok := raw.(importApplyResult)
	if !ok {
		t.Fatalf("返回类型 = %T", raw)
	}
	if !result.Cancelled {
		t.Fatal("宿主取消标记必须透传")
	}
	if result.Imported != 1 || result.Failed != 1 || len(result.Items) != 2 {
		t.Fatalf("取消时仍要逐条回报：%+v", result)
	}
	if result.Items[1].Reason != "cancelled" {
		t.Fatalf("取消条目的原因必须保留：%+v", result.Items[1])
	}
}

// TestImportApplyFailureEnvelope 宿主失败信封原样转交。
func TestImportApplyFailureEnvelope(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":false,"error":{"code":"permission_denied"}}`)}
	app, _ := newTestApp(t, host)
	raw, err := app.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"cc-switch","sourceKey":"claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e"}]}`))
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	envelope, ok := raw.(contract.HostEnvelope)
	if !ok {
		t.Fatalf("返回类型 = %T", raw)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "permission_denied" {
		t.Errorf("失败信封未原样转交：%+v", envelope)
	}

	// 其它失败码同样要原样透传（宿主未注入 store 时返回 internal）。
	host.response = json.RawMessage(`{"ok":false,"error":{"code":"internal"}}`)
	raw, err = app.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"cc-switch","sourceKey":"claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e"}]}`))
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	if other, ok := raw.(contract.HostEnvelope); !ok || other.Error == nil || other.Error.Code != "internal" {
		t.Errorf("internal 失败信封未透传：%+v", raw)
	}
}

// TestImportApplyIncompleteHostResponse 宿主少回一条时必须报错：
// 静默漏行会让页面把某一条显示成成功，而它其实没落库。
func TestImportApplyIncompleteHostResponse(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"imported":1,"skipped":0,"failed":0,"items":[{"index":0,"name":"千刀","status":"imported","providerId":"provider-1","reason":""}]}}`)}
	app, _ := newTestApp(t, host)
	_, err := app.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"cc-switch","sourceKey":"claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e"},{"sourceId":"magpie","sourceKey":"multi-key"}]}`))
	if err == nil {
		t.Fatal("宿主少回一条时应报错，而不是静默漏行")
	}
	// 前提自检：两条都取到了配置，宿主请求体里确实是 2 条。少了这条自检，magpie 那条一旦
	// 取不到配置就会退化成「只发 1 条」，报错便另有原因，守卫会在不知不觉中空过。
	var arguments contract.HostImportArguments
	if err := json.Unmarshal(host.arguments, &arguments); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if len(arguments.Providers) != 2 {
		t.Fatalf("providers = %d, want 2：请求体没满两条，少回一条的前提不成立", len(arguments.Providers))
	}

	// 宿主下标越界同样要报错，不能把宿主下标当调用方下标透出。
	host.response = json.RawMessage(`{"ok":true,"data":{"imported":1,"skipped":0,"failed":0,"items":[{"index":7,"name":"千刀","status":"imported","providerId":"provider-1","reason":""},{"index":1,"name":"百倍","status":"imported","providerId":"provider-2","reason":""}]}}`)
	if _, err := app.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"cc-switch","sourceKey":"claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e"},{"sourceId":"magpie","sourceKey":"multi-key"}]}`)); err == nil {
		t.Fatal("宿主下标越界时应报错")
	}
}

// TestImportApplyArgumentGuards 参数上限与形状由契约固定。
func TestImportApplyArgumentGuards(t *testing.T) {
	app, _ := newTestApp(t, &fakeHost{response: json.RawMessage(`{"ok":true,"data":{}}`)})
	tests := []struct {
		name string
		body string
	}{
		{name: "空 items", body: `{"items":[]}`},
		{name: "缺 items", body: `{}`},
		{name: "多传字段", body: `{"items":[{"sourceId":"magpie","sourceKey":"a","nope":1}]}`},
		{name: "超过 100 条", body: fmt.Sprintf(`{"items":[%s]}`, strings.Repeat(`{"sourceId":"magpie","sourceKey":"x"},`, 100))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := app.handleImport(context.Background(), json.RawMessage(test.body)); err == nil {
				t.Error("应报参数错误")
			}
		})
	}
}

// TestImportApplyDuplicateHostIndex 宿主回重复下标时必须报错：两条结果撞同一个 index 时，
// 后一条覆盖前一条、被覆盖那条留下零值，页面会把一条成功显示成失败、另一条张冠李戴。
// 数量与越界都合法，只有重复这一种形状能触发，所以必须单独钉住。
func TestImportApplyDuplicateHostIndex(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"imported":2,"skipped":0,"failed":0,"items":[{"index":0,"name":"A","status":"imported","providerId":"p1","reason":""},{"index":0,"name":"B","status":"imported","providerId":"p2","reason":""}]}}`)}
	app, _ := newTestApp(t, host)
	if _, err := app.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"cc-switch","sourceKey":"claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e"},{"sourceId":"magpie","sourceKey":"multi-key"}]}`)); err == nil {
		t.Fatal("宿主回重复下标时应报错，而不是静默错位")
	}
	// 前提自检：两条都取到了配置，宿主请求体里确实是 2 条，重复下标的前提才成立。
	var arguments contract.HostImportArguments
	if err := json.Unmarshal(host.arguments, &arguments); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if len(arguments.Providers) != 2 {
		t.Fatalf("providers = %d, want 2：请求体没满两条，重复下标的前提不成立", len(arguments.Providers))
	}
}

// TestImportApplyFetchErrorIsNotSourceMissing 读取来源失败必须与「来源里没有这条」分开记：
// 库损坏/权限问题被说成 source_missing，会让用户以为配置被删，去上游白找一圈。
func TestImportApplyFetchErrorIsNotSourceMissing(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "cc-switch.db")
	if err := os.WriteFile(badPath, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(sources.EnvCCSwitchDB, badPath)
	t.Setenv(sources.EnvMagpieConfig, magpiePrimaryFixturePath(t))
	t.Setenv(sources.EnvMagpieState, testdataPath(t, "magpie", "providers-state.json"))
	t.Setenv(sources.EnvCCGUIConfig, testdataPath(t, "ccgui", "config.json"))
	t.Setenv(sources.EnvOpencodeConfig, testdataPath(t, "opencode", "opencode.json"))
	t.Setenv(sources.EnvMimocodeConfig, testdataPath(t, "mimocode", "mimocode.jsonc"))
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"imported":0,"skipped":0,"failed":0,"items":[]}}`)}
	app := newApp(sources.New(), host)
	raw, err := app.handleImport(context.Background(), json.RawMessage(
		`{"items":[{"sourceId":"cc-switch","sourceKey":"claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e"}]}`))
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	result := raw.(importApplyResult)
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	if result.Items[0].Reason != "source_read_failed" {
		t.Errorf("库读不了应记 source_read_failed，得到 reason=%q（不许与「配置被删」共用文案）", result.Items[0].Reason)
	}
}

// bigKeySource 是返回超长密钥的来源桩，用来构造超过宿主 MaxArgsBytes 的请求体。
type bigKeySource struct{}

func (bigKeySource) ID() string       { return contract.SourceMagpie }
func (bigKeySource) Label() string    { return "magpie" }
func (bigKeySource) Path() string     { return "stub" }
func (bigKeySource) PathError() error { return nil }
func (bigKeySource) Scan(context.Context) (sources.ScanResult, error) {
	return sources.ScanResult{}, nil
}
func (bigKeySource) Fetch(context.Context, string) (sources.SourceItem, bool, error) {
	return sources.SourceItem{
		SourceID:  contract.SourceMagpie,
		SourceKey: "k",
		Name:      "n",
		BaseURL:   "https://x.example",
		APIFormat: contract.FormatOpenAI,
		HasKey:    true,
		Key:       strings.Repeat("k", 4096),
	}, true, nil
}

// TestImportApplyRejectsOversizedHostRequest 请求体超过宿主 MaxArgsBytes 时插件必须先拦下，
// 而不是交给宿主整批拒绝——那样用户只会看到「宿主认为参数不合法」，归因不到超长密钥。
func TestImportApplyRejectsOversizedHostRequest(t *testing.T) {
	host := &fakeHost{response: json.RawMessage(`{"ok":true,"data":{"imported":0,"skipped":0,"failed":0,"items":[]}}`)}
	app := NewWithScanner(sources.NewScanner([]sources.Source{bigKeySource{}}, nil), host)
	refs := make([]string, 0, contract.MaxImportItems)
	for index := 0; index < contract.MaxImportItems; index++ {
		refs = append(refs, `{"sourceId":"magpie","sourceKey":"k"}`)
	}
	if _, err := app.handleImport(context.Background(), json.RawMessage(`{"items":[`+strings.Join(refs, ",")+`]}`)); err == nil {
		t.Fatal("请求体超过宿主上限时应由插件先拦下")
	}
	if host.arguments != nil {
		t.Error("超限请求不该发到宿主")
	}
}
