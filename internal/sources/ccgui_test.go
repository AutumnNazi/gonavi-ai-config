package sources

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonavi-ai-config/internal/contract"
)

// ccguiFixturePath 返回照本机 ~/.ccgui/config.json 形态写的夹具（只读使用）。
func ccguiFixturePath(t *testing.T) string {
	t.Helper()
	return newSourceFixturePath(t, sourceCCGUI)
}

func newCCGUI(path string) Source { return &ccgui{path: path} }

// ccguiFixtureSecrets 从夹具本身推导期望密钥，不在测试里另写一份常量，避免两边漂移后测试假绿。
func ccguiFixtureSecrets(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(ccguiFixturePath(t))
	if err != nil {
		t.Fatalf("读夹具失败：%v", err)
	}
	var fixture struct {
		Codex struct {
			Providers map[string]struct {
				AuthJSON string `json:"authJson"`
			} `json:"providers"`
		} `json:"codex"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("夹具不是合法 JSON：%v", err)
	}
	out := make([]string, 0, len(fixture.Codex.Providers))
	for _, provider := range fixture.Codex.Providers {
		var auth map[string]string
		if err := json.Unmarshal([]byte(provider.AuthJSON), &auth); err != nil {
			continue
		}
		if key := auth["OPENAI_API_KEY"]; key != "" {
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		t.Fatal("夹具里没有任何密钥值，密钥断言会空过")
	}
	return out
}

// TestCCGUICodexMapping 固化唯一已验证形态（codex）的映射：sourceKey、名称、端点、
// 协议、模型列表与 hasKey 都逐字段比对。
func TestCCGUICodexMapping(t *testing.T) {
	path := ccguiFixturePath(t)
	result, err := newCCGUI(path).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []SourceItem{
		{
			SourceID:  sourceCCGUI,
			SourceKey: "codex:11111111-1111-4111-8111-111111111111",
			Name:      "样例主线路",
			BaseURL:   "https://ccgui.example-main.test/v1",
			APIFormat: contract.FormatOpenAIResponses,
			Model:     "gpt-6-astra",
			Models:    []string{"gpt-6-astra"},
			HasKey:    true,
			AppType:   "codex",
		},
		{
			// 名称为空时退回 uuid：编一个名字比留空更容易让人认错条目。
			SourceID:  sourceCCGUI,
			SourceKey: "codex:22222222-2222-4222-8222-222222222222",
			Name:      "22222222-2222-4222-8222-222222222222",
			BaseURL:   "https://ccgui.example-blank.test/v1",
			APIFormat: contract.FormatOpenAIResponses,
			Model:     "kimi-k3-code",
			Models:    []string{"kimi-k3-code"},
			HasKey:    false,
			AppType:   "codex",
		},
		{
			// authJson 是坏数据：只影响这一条（没有密钥），不影响同文件其他条目。
			SourceID:  sourceCCGUI,
			SourceKey: "codex:44444444-4444-4444-8444-444444444444",
			Name:      "坏 authJson 样例",
			BaseURL:   "https://ccgui.example-broken.test/v1",
			APIFormat: contract.FormatOpenAIResponses,
			Model:     "grok-4.6",
			Models:    []string{"grok-4.6"},
			HasKey:    false,
			AppType:   "codex",
		},
	}
	if len(result.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(result.Items), len(want))
	}
	for index, expected := range want {
		sameItem(t, "items["+itoa(index)+"]", result.Items[index], expected)
	}
	if result.Path != path {
		t.Errorf("path = %q, want %q", result.Path, path)
	}
	if result.Skipped != 2 {
		t.Errorf("skipped = %d, want 2（claude 未验证分节 1 条 + codex 无端点 1 条）", result.Skipped)
	}
	if result.KeyUnavailable {
		t.Error("ccgui 的密钥是明文，不该标 keyUnavailable")
	}
	assertUniqueSourceKeys(t, result.Items)

	// 密钥真取到了才算 hasKey=true，否则这条断言会空过。
	secrets := ccguiFixtureSecrets(t)
	withKey := 0
	for _, item := range result.Items {
		if item.HasKey {
			withKey++
			if !containsString(secrets, item.Key) {
				t.Errorf("%s 的密钥与夹具不符", item.SourceKey)
			}
		} else if item.Key != "" {
			t.Errorf("%s 声称没有密钥却取到了值", item.SourceKey)
		}
	}
	if withKey != 1 {
		t.Errorf("hasKey 条目 = %d, want 1", withKey)
	}
}

// TestCCGUIUnknownSectionsAreSkippedNotGuessed 未验证分节必须走 skipped，
// 且绝不许按 codex 那套字段猜出条目——猜出来的会是一条能导入却连不通的供应商。
func TestCCGUIUnknownSectionsAreSkippedNotGuessed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	// 只有未验证分节，且字段名与 codex 不同：任何一条变成 item 都说明代码在猜。
	body := `{
	  "version": 2,
	  "gemini": {
	    "providers": {
	      "gemini-1": { "id": "gemini-1", "name": "Gemini 样例", "authJson": "{\"GEMINI_API_KEY\":\"not-a-secret\"}", "configToml": "model = \"gemini-3\"" }
	    }
	  },
	  "kimi": {
	    "providers": {
	      "kimi-1": { "id": "kimi-1", "name": "Kimi 样例" },
	      "kimi-2": { "id": "kimi-2", "name": "Kimi 样例二" }
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	result, err := newCCGUI(path).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("未验证分节不该产出条目，得到 %d 条：%s", len(result.Items), describeItem(result.Items[0]))
	}
	if result.Skipped != 3 {
		t.Errorf("skipped = %d, want 3（gemini 1 条 + kimi 2 条）", result.Skipped)
	}
}

// TestCCGUISectionWithoutReportableEntries providers 存在但不是对象时，
// 条目数无从枚举：按一条记 skipped，不静默吞掉。
func TestCCGUISectionWithoutReportableEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"gemini": {"providers": []}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	result, err := newCCGUI(path).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Items) != 0 || result.Skipped != 1 {
		t.Errorf("读不出条目的分节应按一条记 skipped：items=%d skipped=%d", len(result.Items), result.Skipped)
	}
}

// TestCCGUINonSectionConfigIsNotASection 顶层混着非分节配置项，
// 它们没有 providers 成员，既不该产出条目也不该计 skipped。
func TestCCGUINonSectionConfigIsNotASection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"version":2,"soundNotification":{"enabled":true},"promptEnhancer":{"provider":"custom","models":{}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	result, err := newCCGUI(path).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Items) != 0 || result.Skipped != 0 {
		t.Errorf("非分节配置项不该产出条目或 skipped：items=%d skipped=%d", len(result.Items), result.Skipped)
	}
}

// TestCCGUIMissingFile 文件缺失只是「没装/没配」：留空结果、不报错，也不编一个路径。
func TestCCGUIMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-installed", "config.json")
	result, err := newCCGUI(path).Scan(context.Background())
	if err != nil {
		t.Fatalf("文件缺失不该报错：%v", err)
	}
	if len(result.Items) != 0 || result.Skipped != 0 {
		t.Errorf("文件缺失时不该有产出：items=%d skipped=%d", len(result.Items), result.Skipped)
	}
	if result.Path != path {
		t.Errorf("path = %q, want %q", result.Path, path)
	}
}

// TestCCGUIInvalidJSON 坏 JSON 折成有界文案：报错、且不含路径与密钥。
func TestCCGUIInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"codex": {"providers": `), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := newCCGUI(path).Scan(context.Background())
	if err == nil {
		t.Fatal("坏 JSON 必须报错，不能静默返回空结果")
	}
	text := errorText(err)
	if text != sourceCCGUI+": "+sourceInvalidJSONText {
		t.Errorf("错误文案 = %q", text)
	}
	if strings.Contains(text, path) || len(text) > 200 {
		t.Errorf("错误文案必须无界外信息且简短：%q", text)
	}
}

// TestCCGUIFetchBySourceKey 按 sourceKey 现取完整配置，密钥只在这一步回到内存。
func TestCCGUIFetchBySourceKey(t *testing.T) {
	source := newCCGUI(ccguiFixturePath(t))
	secrets := ccguiFixtureSecrets(t)

	item, ok, err := source.Fetch(context.Background(), "codex:11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !ok {
		t.Fatal("已存在的 sourceKey 应取到条目")
	}
	if item.Name != "样例主线路" || item.BaseURL != "https://ccgui.example-main.test/v1" {
		t.Errorf("取到的条目不符：%s", describeItem(item))
	}
	if !item.HasKey || !containsString(secrets, item.Key) {
		t.Error("取到的条目应带上夹具里的明文密钥")
	}

	for _, missing := range []string{
		"codex:99999999-9999-4999-8999-999999999999",
		"claude:7c1f0c14-5f2e-4a3b-9c8d-1a2b3c4d5e6f", // 未验证分节不产出条目，自然取不到
		"codex",
		"",
	} {
		if _, ok, err := source.Fetch(context.Background(), missing); err != nil || ok {
			t.Errorf("sourceKey %q 不该取到条目：ok=%v err=%v", missing, ok, err)
		}
	}
}

// TestCCGUINoSecretsInItems 契约硬约束：序列化后的条目里绝不能出现密钥子串。
func TestCCGUINoSecretsInItems(t *testing.T) {
	result, err := newCCGUI(ccguiFixturePath(t)).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	body := itemsJSON(t, result.Items)
	assertNoSourceSecret(t, body, ccguiFixtureSecrets(t))
	// 反向自检：确有条目声称 hasKey，否则上面的断言可能因为压根没解析到密钥而空过。
	hasKey := false
	for _, item := range result.Items {
		if item.HasKey {
			hasKey = true
		}
	}
	if !hasKey {
		t.Fatal("夹具里没有 hasKey=true 的条目，密钥断言会空过")
	}
}
