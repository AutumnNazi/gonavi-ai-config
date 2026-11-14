package sources

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
)

type fakeLister struct {
	refs []ProviderRef
	err  error
}

func (f fakeLister) ListProviders(context.Context) ([]ProviderRef, error) { return f.refs, f.err }

func scannerFor(ccPath, magpiePath string, lister ProviderLister) *Scanner {
	return NewScanner([]Source{NewCCSwitch(ccPath), NewMagpie(magpiePath)}, lister)
}

// fixtureScanner 用生产注册表跑扫描：fixtureEnv 把 6 个路径变量都指到夹具，
// 缺省入参因此扫满契约 §4.1 的全部来源。
func fixtureScanner(t *testing.T, lister ProviderLister) *Scanner {
	t.Helper()
	return NewScanner(fixtureSources(t), lister)
}

// TestScanSourceKeyUnique 契约硬约束：同一 sources.scan 结果内 sourceKey 必须唯一。
func TestScanSourceKeyUnique(t *testing.T) {
	result, err := fixtureScanner(t, nil).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	seen := make(map[string]int, len(result.Items))
	for _, item := range result.Items {
		seen[item.SourceID+"|"+item.SourceKey]++
	}
	for key, count := range seen {
		if count > 1 {
			t.Errorf("sourceKey 重复 %s x%d", key, count)
		}
	}
	if len(result.Items) == 0 {
		t.Fatal("夹具扫描没有任何条目")
	}
	// count 必须等于本次实际产出的条数。
	total := 0
	for _, status := range result.Sources {
		total += status.Count
	}
	if total != len(result.Items) {
		t.Errorf("sources[].count 合计 = %d，实际条目 = %d", total, len(result.Items))
	}
	for _, status := range result.Sources {
		if !status.Detected {
			t.Errorf("来源 %s 应被探测到", status.ID)
		}
		if status.Error != "" {
			t.Errorf("来源 %s 不该有错误：%s", status.ID, status.Error)
		}
	}
}

// assertNoSecret 是「返回体不含密钥」的唯一判据。它单独抽出来是为了能被反向用例调用：
// 一个永远为真的守卫等于没有守卫，所以下面用一条含密钥的载荷证明它确实会失败。
func assertNoSecret(t *testing.T, body []byte) {
	t.Helper()
	text := string(body)
	for _, secret := range allFixtureSecrets(t) {
		if strings.Contains(text, secret) {
			t.Errorf("返回体里出现了密钥子串 %q", secret)
		}
	}
	for _, forbidden := range []string{"\"apiKey\"", "\"key\"", "\"token\"", "\"api_key\""} {
		if strings.Contains(text, forbidden) {
			t.Errorf("返回体里出现了疑似密钥字段 %s", forbidden)
		}
	}
}

// TestScanResponseHasNoSecrets 是契约第 4 节硬约束 1 的断言：
// 返回体序列化后不得出现任何夹具密钥子串，也不得有承载密钥的字段。
func TestScanResponseHasNoSecrets(t *testing.T) {
	result, err := fixtureScanner(t, nil).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	encoded, err := EncodeScanResult(result)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	assertNoSecret(t, encoded)
	// 反向自检：确有条目声称 hasKey，否则上面的断言可能因为压根没解析到密钥而空过。
	hasKey := 0
	for _, item := range result.Items {
		if item.HasKey {
			hasKey++
		}
	}
	if hasKey == 0 {
		t.Fatal("夹具里没有任何 hasKey=true 的条目，密钥断言会空过")
	}
}

// TestSecretGuardDetectsLeak 证明守卫本身是有效的：同一个守卫必须对含密钥的载荷报错。
// 没有这条用例，TestScanResponseHasNoSecrets 可能只是一个恒真的空断言。
func TestSecretGuardDetectsLeak(t *testing.T) {
	secrets := allFixtureSecrets(t)
	leaked := struct {
		Items []map[string]any `json:"items"`
	}{Items: []map[string]any{{"name": "千刀", "apiKey": secrets[0]}}}
	encoded, err := json.Marshal(leaked)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	recorder := &testing.T{}
	assertNoSecret(recorder, encoded)
	if !recorder.Failed() {
		t.Fatal("守卫对含密钥的载荷没有报错，等于没有守卫")
	}
	// 顺带确认纯字段名泄露（值为空）也会被抓到。
	recorder = &testing.T{}
	assertNoSecret(recorder, []byte(`{"items":[{"apiKey":""}]}`))
	if !recorder.Failed() {
		t.Fatal("守卫对空密钥字段名没有报错")
	}
}

// TestScanConflictDetection 冲突判定用第 2 节的判重身份（名称 + 去尾斜杠地址）。
func TestScanConflictDetection(t *testing.T) {
	// 第二条取 magpie 主路径夹具里真实存在的 multi-key：名称两端空白 + 地址尾斜杠，
	// 两处归一化都必须生效（旧的 imported-3 只存在于运行态夹具，主路径里没有这条）。
	lister := fakeLister{refs: []ProviderRef{
		{Name: "千刀", BaseURL: "https://api.zzzcoding.org/"},
		{Name: "  多密钥站  ", BaseURL: "https://multi.example/v1/"},
		// 既有供应商把 key 粘进了 URL：判重身份必须去凭据后才能与导入端同名同址。
		{Name: "华龙", BaseURL: "https://u:p@api-fast.hualong.online/v1"},
	}}
	result, err := fixtureScanner(t, lister).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	conflicts := map[string]bool{}
	for _, item := range result.Items {
		conflicts[item.SourceKey] = item.Conflict
	}
	if !conflicts["claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e"] {
		t.Error("cc-switch 的千刀 应判定为冲突（大小写/尾斜杠归一化后同名同址）")
	}
	if !conflicts["multi-key"] {
		t.Error("magpie 的 multi-key 应判定为冲突（名称两端空白、地址尾斜杠归一化后同名同址）")
	}
	if !conflicts["grokbuild:grokbuild-1"] {
		t.Error("既有供应商地址带凭据时，去凭据后同名同址仍应判定为冲突（与宿主判重规则一致）")
	}
	if conflicts["gemini:gemini-1"] {
		t.Error("未命中的条目不应该是冲突")
	}
	// 取不到宿主元数据时降级为不标冲突，而不是让扫描失败。
	degraded, err := fixtureScanner(t, fakeLister{err: errors.New("permission denied")}).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("宿主能力失败时扫描不该失败：%v", err)
	}
	for _, item := range degraded.Items {
		if item.Conflict {
			t.Error("拿不到宿主元数据时不应标冲突")
		}
	}
	// 但「没标」必须让上层知道是「没标」而不是「没有冲突」。
	if !degraded.ConflictUnavailable {
		t.Error("取不到宿主供应商列表时必须置 conflictUnavailable，页面才能提示「没标 ≠ 不重复」")
	}
}

// TestIdentityNormalizesOnlyCaseInsensitiveParts 判重身份必须与宿主同规则：
// 名称大小写不敏感；地址「协议 + 主机名」大小写不敏感（大小写不敏感的部分），
// 但**路径与查询保留原样**——/tenant/A 与 /tenant/a 是两个不同的供应商，
// 整串小写会把它们合成一条，页面标冲突、宿主跳过，用户就永远导不进来。
func TestIdentityNormalizesOnlyCaseInsensitiveParts(t *testing.T) {
	same := [][2]string{
		{"https://API.example.com/v1", "https://api.example.com/v1"},
		{"https://api.example.com/v1/", "https://api.example.com/v1"},
		{"HTTPS://api.example.com/v1", "https://api.example.com/v1"},
		{"https://u:p@api.example.com/v1", "https://api.example.com/v1"},
		{"https://api.example.com/v1#frag", "https://api.example.com/v1"},
	}
	for _, pair := range same {
		if identity("站", pair[0]) != identity("站", pair[1]) {
			t.Errorf("应判为同一条：%q vs %q", pair[0], pair[1])
		}
	}
	different := [][2]string{
		{"https://api.example.com/tenant/A", "https://api.example.com/tenant/a"},
		{"https://api.example.com/v1?a=1", "https://api.example.com/v1?a=2"},
		{"https://api.example.com/a", "https://api.example.com/b"},
	}
	for _, pair := range different {
		if identity("站", pair[0]) == identity("站", pair[1]) {
			t.Errorf("不应判为同一条（路径/查询大小写敏感）：%q vs %q", pair[0], pair[1])
		}
	}
	// 名称大小写不敏感这条不能被改坏。
	if identity("Tenant", "https://api.example.com/v1") != identity("tenant", "https://api.example.com/v1") {
		t.Error("名称大小写不敏感")
	}
}

// TestScanSingleSourceFailure 单个来源失败只写该来源的 error，其余来源照常返回。
func TestScanSingleSourceFailure(t *testing.T) {
	// 把一个目录当 cc-switch 库文件：探测得到，但只读打开必然失败。
	brokenPath := filepath.Join(t.TempDir(), "cc-switch.db")
	if err := os.MkdirAll(brokenPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fixtureEnv(t)
	t.Setenv(EnvCCSwitchDB, brokenPath)
	result, err := NewScanner(New(), nil).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("整批不该失败：%v", err)
	}
	if len(result.Sources) != len(sourceIDs()) {
		t.Fatalf("sources = %d, want %d", len(result.Sources), len(sourceIDs()))
	}
	broken := result.Sources[0]
	if broken.ID != contract.SourceCCSwitch {
		t.Fatalf("来源顺序不符合预期：%s", broken.ID)
	}
	if broken.Error == "" {
		t.Error("失败的来源必须写 error")
	}
	if broken.Count != 0 {
		t.Errorf("失败来源 count = %d, want 0", broken.Count)
	}
	healthyItems := 0
	for _, healthy := range result.Sources[1:] {
		if healthy.Error != "" {
			t.Errorf("来源 %s 不该被邻居带崩：%s", healthy.ID, healthy.Error)
		}
		if healthy.Count == 0 {
			t.Errorf("来源 %s 应照常返回条目：%+v", healthy.ID, healthy)
		}
		if healthy.ID == contract.SourceMagpie && healthy.KeyUnavailable {
			// v1.2：magpie 主路径是带明文密钥的 XDG 源配置，keyUnavailable 必须为 false。
			t.Error("magpie 主路径拿到了明文密钥，不该带 keyUnavailable 提示")
		}
		healthyItems += healthy.Count
	}
	if len(result.Items) != healthyItems {
		t.Errorf("items = %d, want %d", len(result.Items), healthyItems)
	}
}

// TestScanRequestedSources 缺省扫契约 §4.1 的全部来源（顺序即页面默认顺序），显式入参只扫指定的。
func TestScanRequestedSources(t *testing.T) {
	scanner := fixtureScanner(t, nil)
	all := []string{
		contract.SourceCCSwitch, contract.SourceMagpie, contract.SourceCCGUI,
		contract.SourceOpencode, contract.SourceMimocode,
	}
	tests := []struct {
		name    string
		request contract.ScanRequest
		want    []string
	}{
		{name: "缺省扫全部五个来源", request: contract.ScanRequest{}, want: all},
		// 正好 5 条就是上限本身：边界是「>5 才报错」，第 5 条必须被接受。
		{name: "显式传满五个来源", request: contract.ScanRequest{Sources: all}, want: all},
		{name: "只扫 cc-switch", request: contract.ScanRequest{Sources: []string{contract.SourceCCSwitch}}, want: []string{contract.SourceCCSwitch}},
		{name: "只扫 magpie", request: contract.ScanRequest{Sources: []string{contract.SourceMagpie}}, want: []string{contract.SourceMagpie}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := scanner.Scan(context.Background(), test.request)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(result.Sources) != len(test.want) {
				t.Fatalf("sources = %d, want %d", len(result.Sources), len(test.want))
			}
			for index, id := range test.want {
				if result.Sources[index].ID != id {
					t.Errorf("sources[%d].id = %q, want %q", index, result.Sources[index].ID, id)
				}
			}
		})
	}

	if _, err := scanner.Scan(context.Background(), contract.ScanRequest{Sources: []string{"nope"}}); err == nil {
		t.Error("未知来源应报参数错误")
	}
	// 上限是 contract.MaxSources，按入参长度判（去重前），所以重复 id 凑到 6 条同样越界。
	overflow := append(append([]string{}, all...), contract.SourceCCSwitch)
	if _, err := scanner.Scan(context.Background(), contract.ScanRequest{Sources: overflow}); err == nil {
		t.Errorf("超过 maxSources=%d 的入参应报参数错误", contract.MaxSources)
	}
}

// TestScanResultShapeIsContractFrozen 固化返回体字段名与 JSON 形状，
// 防止后续改动悄悄改名或给密钥留出位置。
//
// sources[] 的字段分两类，必须分开断言：必现字段（无 omitempty）一条都不能少；
// 可选字段（keyUnavailable、skipped、disabled 都带 omitempty）为假值时本就不出现，出现时类型必须对。
// 两类合起来是契约允许的全集——集合外的字段一律判失败，这条防泄漏的严格性不能降。
func TestScanResultShapeIsContractFrozen(t *testing.T) {
	result, err := fixtureScanner(t, nil).Scan(context.Background(), contract.ScanRequest{Sources: []string{"magpie"}})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	encoded, err := EncodeScanResult(result)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded struct {
		Sources []map[string]any `json:"sources"`
		Items   []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Sources) == 0 || len(decoded.Items) == 0 {
		t.Fatalf("夹具没扫出条目，形状断言会空过：sources=%d items=%d", len(decoded.Sources), len(decoded.Items))
	}

	requiredSourceKeys := []string{"id", "label", "detected", "path", "count", "error"}
	// 值类型按 encoding/json 解出来的 Go 类型写：布尔是 bool，数字是 float64。
	allowedSourceKeys := map[string]string{
		"id": "", "label": "", "detected": "", "path": "", "count": "", "error": "",
		"keyUnavailable": "bool", "skipped": "number", "disabled": "number", "notice": "string",
	}
	for _, key := range requiredSourceKeys {
		if _, ok := decoded.Sources[0][key]; !ok {
			t.Errorf("sources[0] 缺少必现字段 %q", key)
		}
	}
	for key, value := range decoded.Sources[0] {
		wantType, allowed := allowedSourceKeys[key]
		if !allowed {
			t.Errorf("sources[0] 出现契约外字段 %q（多出的字段可能承载了不该外传的内容）", key)
			continue
		}
		switch wantType {
		case "bool":
			if _, ok := value.(bool); !ok {
				t.Errorf("sources[0].%s 类型 = %T, want bool", key, value)
			}
		case "number":
			if _, ok := value.(float64); !ok {
				t.Errorf("sources[0].%s 类型 = %T, want number", key, value)
			}
		case "string":
			if _, ok := value.(string); !ok {
				t.Errorf("sources[0].%s 类型 = %T, want string", key, value)
			}
		}
	}
	// 语义钉在这里：夹具走的是带明文密钥的主路径，keyUnavailable 为假 → omitempty 下不该出现；
	// 夹具 6 条里 off:true 一条、无端点一条，两个计数都必须回传且各自独立——
	// 合成一个数就会把用户自己关掉的条目说成插件不认识的条目。
	if _, present := decoded.Sources[0]["keyUnavailable"]; present {
		t.Errorf("magpie 主路径有明文密钥，keyUnavailable 不该出现：%v", decoded.Sources[0]["keyUnavailable"])
	}
	if skipped, present := decoded.Sources[0]["skipped"]; !present {
		t.Error("sources[0] 缺少 skipped：夹具里有结构不支持的条目，跳过数必须回传")
	} else if skipped != float64(1) {
		t.Errorf("sources[0].skipped = %v, want 1（无端点那条）", skipped)
	}
	if disabled, present := decoded.Sources[0]["disabled"]; !present {
		t.Error("sources[0] 缺少 disabled：夹具里有 off:true 的条目，停用数必须回传")
	} else if disabled != float64(1) {
		t.Errorf("sources[0].disabled = %v, want 1（off:true 那条）", disabled)
	}

	wantItemKeys := []string{"sourceId", "sourceKey", "name", "baseUrl", "apiFormat", "model", "models", "hasKey", "appType", "current", "conflict"}
	for _, key := range wantItemKeys {
		if _, ok := decoded.Items[0][key]; !ok {
			t.Errorf("items[0] 缺少字段 %q", key)
		}
	}
	if len(decoded.Items[0]) != len(wantItemKeys) {
		t.Errorf("items[0] 字段数 = %d, want %d（多出的字段可能承载了不该外传的内容）", len(decoded.Items[0]), len(wantItemKeys))
	}
}

// TestScanCountsAreAccountedPerSource 计数自洽：每个来源的 count + skipped + disabled
// 必须等于该来源数据文件里真正读到的条目总数。少任何一项都意味着条目被静默吞掉，
// 用户只会看到页面上数字凭空变少——这正是把两类跳过分开计数要挡住的失败。
//
// 来源总数由 sourceEntryTotal 从数据文件独立数出来，期望值不写死：夹具改了它跟着改，
// 不会出现「夹具换了、数字还绿着」的假绿。
func TestScanCountsAreAccountedPerSource(t *testing.T) {
	result, err := fixtureScanner(t, nil).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// 用环境变量而不是再构造一份夹具：这里数的必须是本次真正被扫的那个文件。
	paths := map[string]string{
		contract.SourceCCSwitch: os.Getenv(EnvCCSwitchDB),
		contract.SourceMagpie:   os.Getenv(EnvMagpieConfig),
		contract.SourceCCGUI:    os.Getenv(EnvCCGUIConfig),
		contract.SourceOpencode: os.Getenv(EnvOpencodeConfig),
		contract.SourceMimocode: os.Getenv(EnvMimocodeConfig),
	}
	if len(result.Sources) != len(paths) {
		t.Fatalf("sources = %d, want %d", len(result.Sources), len(paths))
	}
	for _, status := range result.Sources {
		path, ok := paths[status.ID]
		if !ok {
			t.Fatalf("未知来源 %q", status.ID)
		}
		total := sourceEntryTotal(t, status.ID, path)
		if got := status.Count + status.Skipped + status.Disabled; got != total {
			t.Errorf("%s: count+skipped+disabled = %d+%d+%d = %d, want 来源总数 %d（条目不许被静默吞掉）",
				status.ID, status.Count, status.Skipped, status.Disabled, got, total)
		}
	}
	// cc-switch 没有「上游停用」这个概念：它的 disabled 必须恒为 0，不为对称而编造。
	for _, status := range result.Sources {
		if status.ID == contract.SourceCCSwitch && status.Disabled != 0 {
			t.Errorf("cc-switch disabled = %d, want 0（cc-switch 没有停用语义）", status.Disabled)
		}
	}
}

// dupSource 是一个会返回重复 sourceKey 的来源桩，用来锁住扫描层去重时的计数自洽。
type dupSource struct {
	path  string
	items []SourceItem
}

func (d dupSource) ID() string       { return contract.SourceCCGUI }
func (d dupSource) Label() string    { return "dup" }
func (d dupSource) Path() string     { return d.path }
func (d dupSource) PathError() error { return nil }
func (d dupSource) Scan(context.Context) (ScanResult, error) {
	return ScanResult{Path: d.path, Items: d.items}, nil
}
func (d dupSource) Fetch(context.Context, string) (SourceItem, bool, error) {
	return SourceItem{}, false, nil
}

// TestScanDeduplicatesWithinSourceAccounting 同一来源内重复 sourceKey 只留一条，
// 被去掉的那条必须计入 skipped：计数自洽（count+skipped+disabled 等于来源条目总数）
// 一旦破掉，条目就会在页面上凭空少一条且毫无提示。
func TestScanDeduplicatesWithinSourceAccounting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dup.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	item := SourceItem{SourceID: contract.SourceCCGUI, SourceKey: "same", Name: "A", BaseURL: "https://a.example", APIFormat: contract.FormatOpenAI}
	scanner := NewScanner([]Source{dupSource{path: path, items: []SourceItem{item, item}}}, nil)
	result, err := scanner.Scan(context.Background(), contract.ScanRequest{Sources: []string{contract.SourceCCGUI}})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1（重复 sourceKey 只留一条）", len(result.Items))
	}
	status := result.Sources[0]
	if status.Count != 1 {
		t.Errorf("count = %d, want 1（只数真正进入返回体的条目）", status.Count)
	}
	if got := status.Count + status.Skipped + status.Disabled; got != 2 {
		t.Errorf("count+skipped+disabled = %d, want 2（去重掉的那条必须计入 skipped，不许静默吞掉）", got)
	}
}
