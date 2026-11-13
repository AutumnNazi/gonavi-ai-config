package sources

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
)

// opencodeFixturePath 返回照本机 ~/.config/opencode/opencode.json 形态写的夹具（只读使用）。
func opencodeFixturePath(t *testing.T) string {
	t.Helper()
	return newSourceFixturePath(t, sourceOpencode)
}

func newOpencode(path string) Source { return &opencode{path: path} }

// opencodeFixtureSecrets 从夹具本身推导期望密钥，不在测试里另写一份常量。
func opencodeFixtureSecrets(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(opencodeFixturePath(t))
	if err != nil {
		t.Fatalf("读夹具失败：%v", err)
	}
	var fixture struct {
		Provider map[string]json.RawMessage `json:"provider"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("夹具不是合法 JSON：%v", err)
	}
	// 逐条解码：夹具里刻意放了一条非对象条目，按整表定型会直接被它带崩。
	out := make([]string, 0, len(fixture.Provider))
	for _, entry := range fixture.Provider {
		var parsed struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
		}
		if err := json.Unmarshal(entry, &parsed); err != nil {
			continue
		}
		if key := parsed.Options.APIKey; key != "" {
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		t.Fatal("夹具里没有任何密钥值，密钥断言会空过")
	}
	return out
}

// TestOpencodeMapping 固化映射：sourceKey 是 provider id，npm 决定协议，
// models 取模型映射的键，没有 name 就退回 id。
func TestOpencodeMapping(t *testing.T) {
	path := opencodeFixturePath(t)
	result, err := newOpencode(path).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []SourceItem{
		{
			SourceID:  sourceOpencode,
			SourceKey: "custom",
			Name:      "custom",
			BaseURL:   "https://opencode.example-main.test/v1",
			APIFormat: contract.FormatOpenAI,
			Model:     "deepseek-v4-pro",
			Models:    []string{"deepseek-v4-pro", "glm-5.2"},
			HasKey:    true,
			AppType:   sourceOpencode,
		},
		{
			SourceID:  sourceOpencode,
			SourceKey: "custom-anthropic",
			Name:      "custom-anthropic",
			BaseURL:   "https://opencode.example-anthropic.test/v1",
			APIFormat: contract.FormatAnthropic,
			Model:     "claude-opus-5",
			Models:    []string{"claude-opus-5"},
			HasKey:    true,
			AppType:   sourceOpencode,
		},
		{
			// 没有 apiKey 的条目照样可选：hasKey=false，导入时不下发密钥。
			SourceID:  sourceOpencode,
			SourceKey: "custom-no-key",
			Name:      "custom-no-key",
			BaseURL:   "https://opencode.example-nokey.test/v1",
			APIFormat: contract.FormatOpenAI,
			Model:     "grok-4.6",
			Models:    []string{"grok-4.6"},
			HasKey:    false,
			AppType:   sourceOpencode,
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
	// custom-broken 不是对象、custom-no-base 没有 baseURL，两条都记 skipped。
	if result.Skipped != 2 {
		t.Errorf("skipped = %d, want 2", result.Skipped)
	}
	if result.KeyUnavailable {
		t.Error("opencode 的 apiKey 是明文，不该标 keyUnavailable")
	}
	assertUniqueSourceKeys(t, result.Items)
}

// TestOpencodeBadEntryIsolation 单条坏数据只丢它自己，不牵连同文件的其他条目。
func TestOpencodeBadEntryIsolation(t *testing.T) {
	goodEntry := `{"options":{"baseURL":"https://a.test/v1"}}`
	tests := []struct {
		name        string
		body        string
		want        int
		wantSkipped int
	}{
		{
			name:        "条目不是对象",
			body:        `{"provider":{"good":` + goodEntry + `,"bad":42}}`,
			want:        1,
			wantSkipped: 1,
		},
		{
			name:        "条目是 null",
			body:        `{"provider":{"good":` + goodEntry + `,"bad":null}}`,
			want:        1,
			wantSkipped: 1,
		},
		{
			name:        "options 不是对象",
			body:        `{"provider":{"good":` + goodEntry + `,"bad":{"options":"nope"}}}`,
			want:        1,
			wantSkipped: 1,
		},
		{
			name:        "provider 不是对象",
			body:        `{"provider":[]}`,
			want:        0,
			wantSkipped: 1,
		},
		{
			name:        "provider 是 null",
			body:        `{"provider":null}`,
			want:        0,
			wantSkipped: 0,
		},
		{
			name:        "没有 provider 成员",
			body:        `{"$schema":"https://opencode.ai/config.json"}`,
			want:        0,
			wantSkipped: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opencode.json")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			result, err := newOpencode(path).Scan(context.Background())
			if err != nil {
				t.Fatalf("坏条目不该让整份配置失败：%v", err)
			}
			if len(result.Items) != test.want {
				t.Errorf("items = %d, want %d（好条目必须留下）", len(result.Items), test.want)
			}
			if result.Skipped != test.wantSkipped {
				t.Errorf("skipped = %d, want %d", result.Skipped, test.wantSkipped)
			}
			for _, item := range result.Items {
				if item.SourceKey == "bad" {
					t.Errorf("坏条目不该进结果：%s", describeItem(item))
				}
			}
		})
	}
}

// TestOpencodeMissingFile 文件缺失只是「没装/没配」：留空结果、不报错。
func TestOpencodeMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-installed", "opencode.json")
	result, err := newOpencode(path).Scan(context.Background())
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

// TestOpencodeInvalidJSON 坏 JSON 折成有界文案：报错、且不含路径。
func TestOpencodeInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte(`{"provider": {"custom": `), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := newOpencode(path).Scan(context.Background())
	if err == nil {
		t.Fatal("坏 JSON 必须报错，不能静默返回空结果")
	}
	text := errorText(err)
	if text != sourceOpencode+": "+sourceInvalidJSONText {
		t.Errorf("错误文案 = %q", text)
	}
	if strings.Contains(text, path) || len(text) > 200 {
		t.Errorf("错误文案必须无界外信息且简短：%q", text)
	}
}

// TestOpencodeFetchBySourceKey 按 sourceKey 现取完整配置，密钥只在这一步回到内存。
func TestOpencodeFetchBySourceKey(t *testing.T) {
	source := newOpencode(opencodeFixturePath(t))
	secrets := opencodeFixtureSecrets(t)

	item, ok, err := source.Fetch(context.Background(), "custom-anthropic")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !ok {
		t.Fatal("已存在的 sourceKey 应取到条目")
	}
	if item.APIFormat != contract.FormatAnthropic || !item.HasKey || !containsString(secrets, item.Key) {
		t.Errorf("取到的条目不符：%s hasKey=%v", describeItem(item), item.HasKey)
	}

	for _, missing := range []string{"custom-broken", "custom-no-base", "nope", ""} {
		if _, ok, err := source.Fetch(context.Background(), missing); err != nil || ok {
			t.Errorf("sourceKey %q 不该取到条目：ok=%v err=%v", missing, ok, err)
		}
	}
}

// TestOpencodeNoSecretsInItems 契约硬约束：序列化后的条目里绝不能出现密钥子串。
func TestOpencodeNoSecretsInItems(t *testing.T) {
	result, err := newOpencode(opencodeFixturePath(t)).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	body := itemsJSON(t, result.Items)
	assertNoSourceSecret(t, body, opencodeFixtureSecrets(t))
	// 反向自检：确有条目声称 hasKey，否则断言可能因为没解析到密钥而空过。
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
