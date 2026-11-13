package sources

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonavi-ai-config/internal/contract"
)

// magpieStateFixturePath 是运行态兜底夹具（只有掩码）。
func magpieStateFixturePath(t *testing.T) string {
	t.Helper()
	return fixtureExists(t, testdataPath(t, "magpie", "providers-state.json"))
}

func fixtureExists(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("夹具缺失 %s：%v", path, err)
	}
	return path
}

// loadMagpieFixture 读源配置夹具原文，密钥的期望值一律从夹具本身推导，
// 不在测试里另写一份常量——两边各写一份迟早漂移，那时测试照样绿。
func loadMagpieFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(magpieConfigFixturePath(t))
	if err != nil {
		t.Fatalf("读夹具失败：%v", err)
	}
	return raw
}

type magpieFixtureEntry struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Key    string   `json:"key"`
	Models []string `json:"models"`
	Off    bool     `json:"off"`
	// Keys 原样保留：形状不对的那条要用它做反例，解析成结构体反而先炸。
	Keys json.RawMessage `json:"keys"`
}

// magpieFixtureEntries 暴露夹具的原始形态，供「密钥取自哪一层」和
// 「哪些条目本该被跳过」的断言取期望值。
func magpieFixtureEntries(t *testing.T) []magpieFixtureEntry {
	t.Helper()
	var doc struct {
		Providers []magpieFixtureEntry `json:"providers"`
	}
	if err := json.Unmarshal(loadMagpieFixture(t), &doc); err != nil {
		t.Fatalf("夹具不是合法 JSON：%v", err)
	}
	if len(doc.Providers) == 0 {
		t.Fatal("夹具没有任何供应商")
	}
	return doc.Providers
}

// magpieFixtureKeyEntries 解出夹具条目的 keys[]；形状不对时返回 nil。
func magpieFixtureKeyEntries(t *testing.T, entry magpieFixtureEntry) []magpieFixtureKey {
	t.Helper()
	if len(entry.Keys) == 0 {
		return nil
	}
	var keys []magpieFixtureKey
	if err := json.Unmarshal(entry.Keys, &keys); err != nil {
		return nil
	}
	return keys
}

type magpieFixtureKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	Off bool   `json:"off"`
}

// magpieFixtureSecret 返回某条夹具条目「按契约该选中的」明文密钥：
// keys[] 存在时取其中 off:false 的第一条，否则用顶层 key。
func magpieFixtureSecret(t *testing.T, id string) string {
	t.Helper()
	for _, entry := range magpieFixtureEntries(t) {
		if entry.ID != id {
			continue
		}
		if entries := magpieFixtureKeyEntries(t, entry); entries != nil {
			for _, candidate := range entries {
				if !candidate.Off && candidate.Key != "" {
					return candidate.Key
				}
			}
			// keys[] 里一条可用密钥都没有：按契约不该选中任何密钥，期望值为空。
			return ""
		}
		return entry.Key
	}
	t.Fatalf("夹具里没有 %s", id)
	return ""
}

// magpieUnknownShapeSecret 返回 keys[] 结构未知那条的顶层 key：
// 它是「宁可不导也不许拿顶层 key 顶替」的反例值。
func magpieUnknownShapeSecret(t *testing.T) string {
	t.Helper()
	for _, entry := range magpieFixtureEntries(t) {
		if entry.ID == "keys-unknown-shape" {
			return entry.Key
		}
	}
	t.Fatal("夹具里没有 keys 结构未知的条目")
	return ""
}

// magpieFixtureSecrets 返回夹具里全部明文密钥值，密钥泄露断言用它。
func magpieFixtureSecrets(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	out := []string{}
	add := func(value string) {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	for _, entry := range magpieFixtureEntries(t) {
		add(entry.Key)
		for _, candidate := range magpieFixtureKeyEntries(t, entry) {
			add(candidate.Key)
		}
	}
	if len(out) == 0 {
		t.Fatal("夹具里没有任何密钥值，密钥断言会空过")
	}
	return out
}

func magpieFixtureItem(t *testing.T, result ScanResult, sourceKey string) *SourceItem {
	t.Helper()
	for index := range result.Items {
		if result.Items[index].SourceKey == sourceKey {
			return &result.Items[index]
		}
	}
	t.Fatalf("结果里没有条目 %s", sourceKey)
	return nil
}

// TestMagpieConfigScanMapping 是本文件的主用例：主路径读 XDG 源配置，
// 密钥来自正确的层，协议映射与端点选择照 docs/MAGPIE-FORMAT.md，off 与未知结构按契约跳过。
func TestMagpieConfigScanMapping(t *testing.T) {
	result, err := NewMagpie(magpieConfigFixturePath(t)).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Path != magpieConfigFixturePath(t) {
		t.Errorf("path = %q, want %q", result.Path, magpieConfigFixturePath(t))
	}
	if result.KeyUnavailable {
		t.Error("主路径拿到了明文密钥，keyUnavailable 必须为 false")
	}
	// 夹具 6 条：off:true 停用 1、无端点不支持 1；keys 形状不对那条只丢密钥，条目照常可选。
	// 两个计数分开断言：把 off 那条算进 skipped，页面就会把它说成插件不认识的条目。
	if result.Disabled != 1 {
		t.Errorf("disabled = %d, want 1（off:true 那条是上游停用）", result.Disabled)
	}
	if result.Skipped != 1 {
		t.Errorf("skipped = %d, want 1（无端点那条是结构不支持）", result.Skipped)
	}
	if len(result.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(result.Items))
	}

	tests := []struct {
		name      string
		sourceKey string
		baseURL   string
		apiFormat string
		model     string
		models    []string
		hasKey    bool
	}{
		{
			// baseAPI 缺省：按 anthropic → responses → chat 取第一个非空。
			name: "顶层 key 明文且 baseAPI 缺省回退 anthropic", sourceKey: "imported",
			baseURL: "https://api.zzzcoding.org", apiFormat: contract.FormatOpenAI,
			model: "claude-opus-5[1M]", models: []string{"claude-opus-5[1M]", "claude-opus-5"}, hasKey: true,
		},
		{
			// keys[] 存在：取 off:false 的第一条，顶层 key 不参与。
			name: "keys[] 取第一条启用项", sourceKey: "multi-key",
			baseURL: "https://multi.example/v1", apiFormat: contract.FormatOpenAIResponses,
			model: "gpt-6-astra", models: []string{"gpt-6-astra"}, hasKey: true,
		},
		{
			// keys[] 存在但拉不出可用密钥：不许回退顶层 key 顶替，只丢密钥，条目照常可选。
			name: "keys[] 无可用密钥时不下发密钥且不回退顶层 key", sourceKey: "keys-unknown-shape",
			baseURL: "https://unknown.example/v1", apiFormat: contract.FormatOpenAI,
			model: "", models: []string{}, hasKey: false,
		},
		{
			// 没有密钥不是跳过理由：条目照常可选，只是导入时不下发密钥。
			name: "无密钥条目照常返回但 hasKey=false", sourceKey: "no-key",
			baseURL: "https://nokey.example/v1", apiFormat: contract.FormatOpenAI,
			model: "gpt-6-astra", models: []string{"gpt-6-astra"}, hasKey: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found := magpieFixtureItem(t, result, test.sourceKey)
			if found.SourceID != contract.SourceMagpie {
				t.Errorf("sourceId = %q", found.SourceID)
			}
			if found.BaseURL != test.baseURL {
				t.Errorf("baseUrl = %q, want %q", found.BaseURL, test.baseURL)
			}
			if found.APIFormat != test.apiFormat {
				t.Errorf("apiFormat = %q, want %q", found.APIFormat, test.apiFormat)
			}
			if found.Model != test.model {
				t.Errorf("model = %q, want %q", found.Model, test.model)
			}
			if strings.Join(found.Models, "|") != strings.Join(test.models, "|") {
				t.Errorf("models = %v, want %v", found.Models, test.models)
			}
			if found.HasKey != test.hasKey {
				t.Errorf("hasKey = %v, want %v", found.HasKey, test.hasKey)
			}
			want := magpieFixtureSecret(t, test.sourceKey)
			if found.Key != want {
				t.Errorf("密钥与契约选择规则不符：got %q, want %q", found.Key, want)
			}
		})
	}

	for _, skipped := range []string{"disabled", "no-endpoint"} {
		for _, item := range result.Items {
			if item.SourceKey == skipped {
				t.Errorf("条目 %s 应被跳过", skipped)
			}
		}
	}
}

// TestMagpieConfigKeysSelection 固化 keys[] 的边界：全部 off、空数组、
// 以及值不是字符串的 keys[] —— 都不许回退顶层 key 顶替，宁可少一条。
func TestMagpieConfigKeysSelection(t *testing.T) {
	// keysField 单独传，才能区分「字段在、里面没东西」与「字段不在」。
	entry := func(id, topKey, keysField string) string {
		return `{"id":"` + id + `","name":"` + id + `","baseAPI":"chat","chat":"https://x.example/v1","key":"` + topKey + `"` + keysField + `}`
	}
	tests := []struct {
		name      string
		payload   string
		items     int
		skipped   int
		disabled  int
		sourceKey string
		wantKey   string
		hasKey    bool
	}{
		{
			// off:true 是上游明确停用：计入 disabled，不能混进「结构不支持」。
			name:    "off:true 计入 disabled 而非 skipped",
			payload: `{"id":"turned-off","name":"已关闭","baseAPI":"chat","chat":"https://x.example/v1","off":true}`,
			items:   0, skipped: 0, disabled: 1,
		},
		{
			name:    "keys[] 第一条 off 则取第二条",
			payload: entry("pick-second", "sk-top-aaa", `,"keys":[{"key":"sk-off-bbb","off":true},{"key":"sk-on-ccc","off":false}]`),
			items:   1, skipped: 0, sourceKey: "pick-second", wantKey: "sk-on-ccc", hasKey: true,
		},
		{
			// 字段在、里面一条可用的都没有：只丢密钥，不许拿顶层 key 顶替。
			name:    "keys[] 全部 off：不下发密钥且不回退顶层 key",
			payload: entry("all-off", "sk-top-ddd", `,"keys":[{"key":"sk-off-eee","off":true}]`),
			items:   1, skipped: 0, sourceKey: "all-off", wantKey: "", hasKey: false,
		},
		{
			name:    "keys[] 为空数组：同为「字段在」，不回退顶层 key",
			payload: entry("empty-keys", "sk-top-fff", `,"keys":[]`),
			items:   1, skipped: 0, sourceKey: "empty-keys", wantKey: "", hasKey: false,
		},
		{
			// 没有 keys 字段：这时才该用顶层 key。
			name:    "没有 keys 字段时用顶层 key",
			payload: entry("top-key-only", "sk-top-hhh", ""),
			items:   1, skipped: 0, sourceKey: "top-key-only", wantKey: "sk-top-hhh", hasKey: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(`{"providers":[` + test.payload + `]}`)
			result, err := parseMagpieConfig(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(result.Items) != test.items || result.Skipped != test.skipped || result.Disabled != test.disabled {
				t.Fatalf("items = %d skipped = %d disabled = %d, want %d/%d/%d",
					len(result.Items), result.Skipped, result.Disabled, test.items, test.skipped, test.disabled)
			}
			if test.items == 0 {
				return
			}
			item := magpieFixtureItem(t, result, test.sourceKey)
			if item.Key != test.wantKey || item.HasKey != test.hasKey {
				t.Errorf("key = %q hasKey = %v, want %q/%v", item.Key, item.HasKey, test.wantKey, test.hasKey)
			}
		})
	}
}

// TestMagpieFallbackWhenPrimaryMissing 主路径不存在时走运行态兜底：
// 元数据照常给，但必须如实标注密钥不可用。
func TestMagpieFallbackWhenPrimaryMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "providers.json")
	result, err := NewMagpieWithFallback(missing, magpieStateFixturePath(t)).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Path != magpieStateFixturePath(t) {
		t.Errorf("path = %q, want 兜底路径", result.Path)
	}
	if !result.KeyUnavailable {
		t.Error("兜底路径只有掩码，keyUnavailable 必须为 true")
	}
	if result.DegradedReason != "" {
		t.Errorf("主路径本就不存在，不算降级，notice 该为空：%q", result.DegradedReason)
	}
	if len(result.Items) == 0 {
		t.Fatal("兜底夹具应扫出条目")
	}
	for _, item := range result.Items {
		if item.HasKey {
			t.Errorf("%s 声称有密钥：掩码不是密钥，不许伪造 hasKey", item.SourceKey)
		}
		if item.Key != "" {
			t.Errorf("%s 携带了密钥内容", item.SourceKey)
		}
	}
	// 兜底里 imported-3 那条必须原样可用。
	found := magpieFixtureItem(t, result, "imported-3")
	if found.BaseURL != "https://sub.100xlabs.space" || found.APIFormat != contract.FormatAnthropic {
		t.Errorf("兜底映射不符：%+v", found)
	}
}

// TestMagpieFallbackWhenPrimaryInvalid 主路径存在但不是合法 JSON 时同样走兜底，
// 且不把 JSON 解析细节外传。
func TestMagpieFallbackWhenPrimaryInvalid(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(broken, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("写坏夹具：%v", err)
	}
	result, err := NewMagpieWithFallback(broken, magpieStateFixturePath(t)).Scan(context.Background())
	if err != nil {
		t.Fatalf("主路径坏了不该报错，应走兜底：%v", err)
	}
	if !result.KeyUnavailable || result.Path != magpieStateFixturePath(t) {
		t.Errorf("应走兜底并标注密钥不可用：path=%q keyUnavailable=%v", result.Path, result.KeyUnavailable)
	}
	// 主配置存在却坏了：必须把「已降级」带回去，否则改坏的主配置会被静默忽略。
	if result.DegradedReason == "" {
		t.Error("主配置不是合法 JSON 时必须带降级原因")
	}
	if len(result.Items) == 0 {
		t.Error("兜底应扫出条目")
	}
}

// TestScanSurfacesMagpieDegradedNotice 端到端：主配置损坏走兜底时，sources[].notice
// 必须把这件事实带出去。数据照常可用，所以它不是 error（不置读取失败状态）。
func TestScanSurfacesMagpieDegradedNotice(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(broken, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("写坏夹具：%v", err)
	}
	source := NewMagpieWithFallback(broken, magpieStateFixturePath(t))
	scanner := NewScanner([]Source{source}, nil)
	result, err := scanner.Scan(context.Background(), contract.ScanRequest{Sources: []string{contract.SourceMagpie}})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	status := result.Sources[0]
	if status.Error != "" {
		t.Errorf("降级不是读取失败，不该置 error：%q", status.Error)
	}
	if status.Notice == "" {
		t.Fatal("主配置损坏时必须在 sources[].notice 里说明")
	}
	if status.Count == 0 {
		t.Error("降级时数据仍应照常返回")
	}
}

// TestMagpieBothPathsMissing 两个文件都没有只是没装/没配，不是错误。
func TestMagpieBothPathsMissing(t *testing.T) {
	dir := t.TempDir()
	result, err := NewMagpieWithFallback(filepath.Join(dir, "providers.json"), filepath.Join(dir, "providers-state.json")).Scan(context.Background())
	if err != nil {
		t.Fatalf("缺失文件不该报错：%v", err)
	}
	if len(result.Items) != 0 || result.KeyUnavailable {
		t.Errorf("items = %d keyUnavailable = %v, want 0/false", len(result.Items), result.KeyUnavailable)
	}
}

// TestMagpieFallbackMissingReportsPathError 主路径解析不出来时必须报错，
// 不许退化成「读兜底」或者「返回空结果」——那会让配置错误看起来像没装。
func TestMagpieFallbackMissingReportsPathError(t *testing.T) {
	source := &magpie{
		resolveErr:   errors.New("override path must be absolute"),
		fallbackPath: magpieStateFixturePath(t),
	}
	if _, err := source.Scan(context.Background()); err == nil {
		t.Fatal("主路径不可解析时必须报错")
	}
}

// TestMagpieStateScanSkipsUnknownShape 兜底路径沿用同样的跳过语义：
// 无端点等未知结构跳过，count 与实际产出一致（skip 不冒充条目）。
func TestMagpieStateScanSkipsUnknownShape(t *testing.T) {
	// 兜底路径必须通过两路径构造进入：把运行态文件当主路径喂进去本来就是错的。
	missing := filepath.Join(t.TempDir(), "providers.json")
	result, err := NewMagpieWithFallback(missing, magpieStateFixturePath(t)).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Disabled != 1 {
		t.Errorf("disabled = %d, want 1（off:true 那条）", result.Disabled)
	}
	if result.Skipped != 1 {
		t.Errorf("skipped = %d, want 1（无端点那条）", result.Skipped)
	}
	if len(result.Items) != 2 {
		t.Errorf("items = %d, want 2", len(result.Items))
	}
	if !result.KeyUnavailable {
		t.Error("只有掩码时 keyUnavailable 必须为 true")
	}
}

// TestMagpieScanResponseHasNoSecret 契约第 4 节硬约束 1：扫描返回体序列化后
// 不得出现任何明文密钥子串，也不得有承载密钥的字段名。
func TestMagpieScanResponseHasNoSecret(t *testing.T) {
	// 只扫 magpie：契约来源清单已扩到 5 个，而这里只注册了两个来源。
	scanner := scannerFor(writeCCSwitchFixture(t), magpieConfigFixturePath(t), nil)
	result, err := scanner.Scan(context.Background(), contract.ScanRequest{Sources: []string{contract.SourceMagpie}})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	encoded, err := EncodeScanResult(result)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	text := string(encoded)
	for _, secret := range magpieFixtureSecrets(t) {
		if strings.Contains(text, secret) {
			t.Errorf("返回体里出现了 magpie 密钥子串 %q", secret)
		}
	}
	for _, forbidden := range []string{`"apiKey"`, `"key"`, `"token"`} {
		if strings.Contains(text, forbidden) {
			t.Errorf("返回体里出现了疑似密钥字段 %s", forbidden)
		}
	}
	// 反向自检：确有条目声称 hasKey，否则上面的断言可能因为压根没拿到密钥而空过。
	hasKey := 0
	for _, item := range result.Items {
		if item.SourceID == contract.SourceMagpie && item.HasKey {
			hasKey++
		}
	}
	if hasKey == 0 {
		t.Fatal("没有任何 hasKey=true 的 magpie 条目，密钥断言会空过")
	}
	// 再钉一次「跳过的那条顶层 key 也没漏出去」。
	if leaked := magpieUnknownShapeSecret(t); leaked != "" && strings.Contains(text, leaked) {
		t.Errorf("被跳过条目的顶层 key 泄进了返回体：%q", leaked)
	}
}

// TestMagpieConfigPathResolution 复现宿主受限环境（家目录环境变量被清空）：
// XDG 主路径必须解析成绝对路径，覆盖变量优先，相对覆盖变量当场拒绝。
func TestMagpieConfigPathResolution(t *testing.T) {
	unsetHomeEnv(t)
	home, err := userHome()
	if err != nil {
		t.Fatalf("家目录解析失败：%v", err)
	}
	expected := filepath.Join(home, ".config", "magpie", "providers.json")

	t.Run("缺省走 XDG 根", func(t *testing.T) {
		path, err := resolveXDGSourcePath(EnvMagpieConfig, "magpie", "providers.json")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if path != expected {
			t.Errorf("path = %q, want %q", path, expected)
		}
	})

	t.Run("XDG_CONFIG_HOME 覆盖生效", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", root)
		path, err := resolveXDGSourcePath(EnvMagpieConfig, "magpie", "providers.json")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if want := filepath.Join(root, "magpie", "providers.json"); path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})

	t.Run("显式覆盖变量优先于 XDG", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv(EnvMagpieConfig, filepath.Join(root, "custom.json"))
		path, err := resolveXDGSourcePath(EnvMagpieConfig, "magpie", "providers.json")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if want := filepath.Join(root, "custom.json"); path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})

	t.Run("相对覆盖变量被拒绝", func(t *testing.T) {
		t.Setenv(EnvMagpieConfig, filepath.Join("magpie", "providers.json"))
		if _, err := resolveXDGSourcePath(EnvMagpieConfig, "magpie", "providers.json"); err == nil {
			t.Fatal("相对覆盖路径应被拒绝")
		}
	})

	t.Run("相对 XDG_CONFIG_HOME 被拒绝", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join("magpie", ".config"))
		if _, err := resolveXDGSourcePath(EnvMagpieConfig, "magpie", "providers.json"); err == nil {
			t.Fatal("相对 XDG_CONFIG_HOME 应被拒绝")
		}
	})
}

// TestMagpieMissingFile 文件不存在不是错误，只是没探测到。
func TestMagpieMissingFile(t *testing.T) {
	result, err := NewMagpie(t.TempDir() + "\\不存在的目录\\providers.json").Scan(context.Background())
	if err != nil {
		t.Fatalf("缺失文件不该报错：%v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("items = %d, want 0", len(result.Items))
	}
}

// TestMagpieConfigInvalidKeysPayload 是 keys 形状未知的对照用例：
// 只丢这一条，不猜、不整份失败。
func TestMagpieConfigInvalidKeysPayload(t *testing.T) {
	// keys 是对象而不是数组：形状说不清楚。这时只丢这条，不许回退顶层 key，也不让整份配置失败。
	result, err := parseMagpieConfig([]byte(`{"providers":[{"id":"bad","baseAPI":"chat","chat":"https://x.example/v1","key":"sk-top-iii","keys":{"a":1}}]}`))
	if err != nil {
		t.Fatalf("单条 keys 形状不对不该让整份配置解析失败：%v", err)
	}
	if len(result.Items) != 0 || result.Skipped != 1 {
		t.Fatalf("items = %d skipped = %d, want 0/1（形状说不清就跳过，不猜）", len(result.Items), result.Skipped)
	}
}

// TestMagpiePluginEndpointIsSkipped 插件型端点（plugin://…）不是宿主能直连的地址：
// 契约 §4.1 把这类条目算「结构不支持」，所以它进 skipped 而不是 disabled，
// 更不许原样当 baseUrl 下发——那会是一条页面看着正常、实际永远连不通的供应商。
func TestMagpiePluginEndpointIsSkipped(t *testing.T) {
	result, err := parseMagpieConfig([]byte(`{"providers":[
		{"id":"plugin-chat","name":"插件型","baseAPI":"chat","chat":"plugin://raccoon/v1"},
		{"id":"plugin-fallback","name":"插件型但有真端点","chat":"plugin://loomy/v1","anthropic":"https://loomy.example"},
		{"id":"plugin-only","name":"只剩插件端点","anthropic":"plugin://trae-cn/v1"}
	]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 失败信息不打印 SourceItem 本体：%+v 会把 Key 写进日志，本包的输出纪律是不给密钥留口子。
	if len(result.Items) != 1 {
		t.Fatalf("只有带真端点的那条该产出条目：items = %d, skipped = %d, disabled = %d",
			len(result.Items), result.Skipped, result.Disabled)
	}
	if result.Items[0].SourceKey != "plugin-fallback" {
		t.Fatalf("该产出的是 plugin-fallback，实际 %s", describeItem(result.Items[0]))
	}
	if result.Items[0].BaseURL != "https://loomy.example" {
		t.Errorf("baseUrl = %q, want 真端点（插件端点必须被跳过，不许顶替真端点）", result.Items[0].BaseURL)
	}
	if result.Skipped != 2 {
		t.Errorf("skipped = %d, want 2（两条只剩插件端点）", result.Skipped)
	}
	if result.Disabled != 0 {
		t.Errorf("disabled = %d, want 0（插件端点不是上游停用）", result.Disabled)
	}
}
