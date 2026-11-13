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

// mimocodeFixturePath 返回照本机 ~/.config/mimocode/mimocode.jsonc 形态写的夹具（只读使用）。
func mimocodeFixturePath(t *testing.T) string {
	t.Helper()
	return newSourceFixturePath(t, sourceMimocode)
}

func newMimocode(path string) Source { return &mimocode{path: path} }

// mimocodeFixtureSecrets 从夹具本身推导期望密钥。夹具带注释，所以先剥注释再解；
// 剥注释用的就是被测的那条路径，它若剥坏，这里会直接解析失败而不是悄悄少算。
func mimocodeFixtureSecrets(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(mimocodeFixturePath(t))
	if err != nil {
		t.Fatalf("读夹具失败：%v", err)
	}
	var fixture struct {
		Provider map[string]struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(stripJSONComments(string(raw))), &fixture); err != nil {
		t.Fatalf("夹具剥注释后不是合法 JSON：%v", err)
	}
	out := make([]string, 0, len(fixture.Provider))
	for _, provider := range fixture.Provider {
		if key := provider.Options.APIKey; key != "" {
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		t.Fatal("夹具里没有任何密钥值，密钥断言会空过")
	}
	return out
}

// TestStripJSONComments 注释剥离的逐例断言。字符串里的 // 与 /* 必须原样保留——
// baseURL 的 "https://" 就是最容易被误伤的一处，剥坏了会让整份配置解析失败。
func TestStripJSONComments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "无注释原样返回",
			input: `{"a": 1}`,
			want:  `{"a": 1}`,
		},
		{
			name:  "整行注释",
			input: "{\n  // 一行注释\n  \"a\": 1\n}",
			want:  "{\n  \n  \"a\": 1\n}",
		},
		{
			name:  "行尾注释",
			input: "{\"a\": 1 // 行尾注释\n}",
			want:  "{\"a\": 1 \n}",
		},
		{
			name:  "单行块注释",
			input: "{\n  /* 一行块注释 */\n  \"a\": 1\n}",
			want:  "{\n  \n  \"a\": 1\n}",
		},
		{
			// 块注释内部的换行写回输出，报错行号才不会因为注释而漂移。
			name:  "跨行块注释保留内部换行",
			input: "{\n  /* 块\n     注释 */\n  \"a\": 1\n}",
			want:  "{\n  \n\n  \"a\": 1\n}",
		},
		{
			name:  "字符串里的双斜杠不动",
			input: `{"baseURL": "https://example.test/v1"}`,
			want:  `{"baseURL": "https://example.test/v1"}`,
		},
		{
			name:  "字符串里的块注释符不动",
			input: `{"name": "a /* b */ c"}`,
			want:  `{"name": "a /* b */ c"}`,
		},
		{
			name:  "转义引号不结束字符串",
			input: "{\"name\": \"say \\\"hi\\\" // 还在字符串里\", \"b\": 2}",
			want:  "{\"name\": \"say \\\"hi\\\" // 还在字符串里\", \"b\": 2}",
		},
		{
			name:  "转义反斜杠后字符串正常结束",
			input: "{\"path\": \"C:\\dir\", // 注释\n\"b\": 2}",
			want:  "{\"path\": \"C:\\dir\", \n\"b\": 2}",
		},
		{
			name:  "注释在文件末尾",
			input: "{\"a\": 1}\n// 末尾注释",
			want:  "{\"a\": 1}\n",
		},
		{
			name:  "字符串外的单个斜杠不是注释",
			input: `{"a": "x"} /`,
			want:  `{"a": "x"} /`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := stripJSONComments(test.input); got != test.want {
				t.Errorf("stripJSONComments = %q, want %q", got, test.want)
			}
		})
	}
}

// TestMimocodeMapping 固化映射：形状与 opencode 一致，但夹具带注释、条目自带 name。
func TestMimocodeMapping(t *testing.T) {
	path := mimocodeFixturePath(t)
	result, err := newMimocode(path).Scan(context.Background())
	if err != nil {
		t.Fatalf("带注释的配置必须能解析：%v", err)
	}
	want := []SourceItem{
		{
			SourceID:  sourceMimocode,
			SourceKey: "b6508e38-3fd1-4382-8ebf-8dab72c6e7d3",
			Name:      "nvidia",
			BaseURL:   "https://integrate.example-nvidia.test/v1",
			APIFormat: contract.FormatAnthropic,
			Model:     "deepseek-ai/deepseek-v4-pro-0813",
			Models:    []string{"deepseek-ai/deepseek-v4-pro-0813", "moonshotai/kimi-k3"},
			HasKey:    true,
			AppType:   sourceMimocode,
		},
		{
			SourceID:  sourceMimocode,
			SourceKey: "hyb-grok",
			Name:      "黑与白grok",
			BaseURL:   "https://ai.hybgzs.com/v1",
			APIFormat: contract.FormatOpenAI,
			Model:     "grok-4.7",
			Models:    []string{"grok-4.7"},
			HasKey:    true,
			AppType:   sourceMimocode,
		},
		{
			SourceID:  sourceMimocode,
			SourceKey: "local",
			Name:      "本机中转",
			BaseURL:   "http://127.0.0.1:3425/v1",
			APIFormat: contract.FormatOpenAI,
			Model:     "nvidia/z-ai/glm-5.3-flash",
			Models:    []string{"nvidia/z-ai/glm-5.3-flash"},
			HasKey:    true,
			AppType:   sourceMimocode,
		},
		{
			// 没有 apiKey 与 models 的条目照样可选，只是没有密钥。
			SourceID:  sourceMimocode,
			SourceKey: "no-key",
			Name:      "no-key",
			BaseURL:   "https://mimocode.example-nokey.test/v1",
			APIFormat: contract.FormatOpenAI,
			HasKey:    false,
			AppType:   sourceMimocode,
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
	if result.Skipped != 0 {
		t.Errorf("skipped = %d, want 0", result.Skipped)
	}
	assertUniqueSourceKeys(t, result.Items)
}

// TestMimocodeCommentStrippingIsRequired 夹具里的注释若没被剥掉，解析必须失败：
// 这条用例证明 mapping 用例真的走过了剥注释这条路，而不是碰巧夹具里没有注释。
func TestMimocodeCommentStrippingIsRequired(t *testing.T) {
	raw, err := os.ReadFile(mimocodeFixturePath(t))
	if err != nil {
		t.Fatalf("读夹具失败：%v", err)
	}
	if !strings.Contains(string(raw), "//") || !strings.Contains(string(raw), "/*") {
		t.Fatal("夹具里没有注释，注释剥离用例会空过")
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err == nil {
		t.Fatal("夹具带着注释却能被直接解析，说明注释剥离用例没有真正覆盖")
	}
}

// TestMimocodeMissingFile 文件缺失只是「没装/没配」：留空结果、不报错。
func TestMimocodeMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-installed", "mimocode.jsonc")
	result, err := newMimocode(path).Scan(context.Background())
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

// TestMimocodeInvalidJSON 坏 JSON 折成有界文案：报错、且不含路径。
func TestMimocodeInvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "截断的 JSON", body: "{\n  // 注释\n  \"provider\": {"},
		{name: "剥完注释只剩注释", body: "// 只有注释\n/* 什么都没有 */"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mimocode.jsonc")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := newMimocode(path).Scan(context.Background())
			if err == nil {
				t.Fatal("坏 JSON 必须报错，不能静默返回空结果")
			}
			text := errorText(err)
			if text != sourceMimocode+": "+sourceInvalidJSONText {
				t.Errorf("错误文案 = %q", text)
			}
			if strings.Contains(text, path) || len(text) > 200 {
				t.Errorf("错误文案必须无界外信息且简短：%q", text)
			}
		})
	}
}

// TestMimocodeFetchBySourceKey 按 sourceKey 现取完整配置，密钥只在这一步回到内存。
func TestMimocodeFetchBySourceKey(t *testing.T) {
	source := newMimocode(mimocodeFixturePath(t))
	secrets := mimocodeFixtureSecrets(t)

	item, ok, err := source.Fetch(context.Background(), "local")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !ok {
		t.Fatal("已存在的 sourceKey 应取到条目")
	}
	if item.Name != "本机中转" || !item.HasKey || !containsString(secrets, item.Key) {
		t.Errorf("取到的条目不符：%s hasKey=%v", describeItem(item), item.HasKey)
	}

	for _, missing := range []string{"no-base", "nope", ""} {
		if _, ok, err := source.Fetch(context.Background(), missing); err != nil || ok {
			t.Errorf("sourceKey %q 不该取到条目：ok=%v err=%v", missing, ok, err)
		}
	}
}

// TestMimocodeNoSecretsInItems 契约硬约束：序列化后的条目里绝不能出现密钥子串。
func TestMimocodeNoSecretsInItems(t *testing.T) {
	result, err := newMimocode(mimocodeFixturePath(t)).Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	body := itemsJSON(t, result.Items)
	assertNoSourceSecret(t, body, mimocodeFixtureSecrets(t))
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
