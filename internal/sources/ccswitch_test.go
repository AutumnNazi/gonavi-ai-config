package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCCSwitchRowMapping 逐 app_type 核对字段映射：字段名与枚举照 docs/CC-SWITCH-FORMAT.md，
// 不改写 [1M] 这类后缀原文，也不把 meta.proxyUrl 当端点。期望密钥取自夹具本身。
func TestCCSwitchRowMapping(t *testing.T) {
	source := NewCCSwitch(writeCCSwitchFixture(t))
	result, err := source.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	byKey := make(map[string]SourceItem, len(result.Items))
	for _, item := range result.Items {
		byKey[item.SourceKey] = item
	}
	// 夹具 11 行，其中 claude-desktop 空 env / codex 无 base_url / 未支持类型各 1 行被跳过。
	if result.Skipped != 3 {
		t.Errorf("skipped = %d, want 3（claude-desktop 空 env / codex 无 base_url / 未支持类型）", result.Skipped)
	}
	// cc-switch 的库里没有「上游停用」这个概念：disabled 必须恒为 0，
	// 不许为了跟 magpie 对称而把它算出来。
	if result.Disabled != 0 {
		t.Errorf("disabled = %d, want 0（cc-switch 没有停用语义）", result.Disabled)
	}

	tests := []struct {
		name      string
		sourceKey string
		baseURL   string
		apiFormat string
		model     string
		models    []string
		appType   string
		current   bool
	}{
		{
			name: "claude", sourceKey: "claude:c435024e-8384-417e-a2f4-8f8cd4c9c36e",
			baseURL: "https://api.zzzcoding.org", apiFormat: "anthropic", model: "claude-opus-5[1M]",
			models: []string{"claude-opus-5[1M]", "claude-opus-5"}, appType: "claude", current: true,
		},
		{
			name: "claude-desktop", sourceKey: "claude-desktop:ac6b0f26-fd8c-4425-a7f7-62332cb757dc",
			baseURL: "https://api.minimaxi.com/anthropic", apiFormat: "anthropic", model: "MiniMax-M2.7",
			models: []string{"MiniMax-M2.7"}, appType: "claude-desktop",
		},
		{
			// codex 的 base_url 藏在 [model_providers.custom] 段里，meta.proxyUrl 不能被当成端点。
			name: "codex", sourceKey: "codex:8f90a261-fdda-4ab2-aa58-de7ed667eb58",
			baseURL: "https://motomoto.lol/v1", apiFormat: "openai-responses", model: "gpt-6-astra",
			models: []string{"gpt-6-astra"}, appType: "codex",
		},
		{
			name: "gemini", sourceKey: "gemini:gemini-1",
			baseURL: "https://elysia.h-e.top", apiFormat: "gemini", model: "gemini-3.5-flash",
			models: []string{"gemini-3.5-flash"}, appType: "gemini",
		},
		{
			// JSON 对象键顺序会丢，模型列表按排序输出，所以期望值也是排序后的顺序。
			name: "opencode", sourceKey: "opencode:opencode-1",
			baseURL: "https://newapi.zmoon.top/v1", apiFormat: "openai", model: "deepseek-v4-flash-0731",
			models: []string{"deepseek-v4-flash-0731", "glm-5.2"}, appType: "opencode",
		},
		{
			name: "opencode-anthropic", sourceKey: "opencode:opencode-anthropic",
			baseURL: "https://api.example-anthropic.test/v1", apiFormat: "anthropic", model: "claude-opus-5",
			models: []string{"claude-opus-5"}, appType: "opencode",
		},
		{
			name: "grokbuild", sourceKey: "grokbuild:grokbuild-1",
			baseURL: "https://api-fast.hualong.online/v1", apiFormat: "openai-responses", model: "grok-4.6",
			models: []string{"grok-4.6"}, appType: "grokbuild",
		},
		{
			name: "hermes", sourceKey: "hermes:hermes-1",
			baseURL: "https://api.hermes.example/v1", apiFormat: "openai", model: "gpt-4o",
			models: []string{"gpt-4o"}, appType: "hermes",
		},
	}
	secrets := fixtureSecrets(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item, ok := byKey[test.sourceKey]
			if !ok {
				t.Fatalf("缺少 %s 的解析结果", test.sourceKey)
			}
			if item.SourceID != "cc-switch" {
				t.Errorf("sourceId = %q", item.SourceID)
			}
			if item.BaseURL != test.baseURL {
				t.Errorf("baseUrl = %q, want %q", item.BaseURL, test.baseURL)
			}
			if item.APIFormat != test.apiFormat {
				t.Errorf("apiFormat = %q, want %q", item.APIFormat, test.apiFormat)
			}
			if item.Model != test.model {
				t.Errorf("model = %q, want %q", item.Model, test.model)
			}
			if strings.Join(item.Models, "|") != strings.Join(test.models, "|") {
				t.Errorf("models = %v, want %v", item.Models, test.models)
			}
			if item.AppType != test.appType {
				t.Errorf("appType = %q, want %q", item.AppType, test.appType)
			}
			if item.Current != test.current {
				t.Errorf("current = %v, want %v", item.Current, test.current)
			}
			want, hasFixtureSecret := secrets[test.sourceKey]
			if !hasFixtureSecret {
				t.Fatalf("夹具里 %s 没有密钥值，该用例的密钥断言会空过", test.sourceKey)
			}
			if !item.HasKey || item.Key != want {
				t.Errorf("hasKey=%v 且内存里的密钥与夹具不一致（那是 import.apply 唯一需要密钥的地方）", item.HasKey)
			}
		})
	}
	// 没有密钥的行必须如实报 hasKey=false，不能因为同 app_type 别处有密钥就谎报。
	if _, ok := byKey["codex:codex-no-base-url"]; ok {
		t.Error("无 base_url 的 codex 行不该产出条目")
	}
}

// TestCCSwitchFetchBySourceKey 验证 import.apply 的现取现用路径。
func TestCCSwitchFetchBySourceKey(t *testing.T) {
	source := NewCCSwitch(writeCCSwitchFixture(t))
	secrets := fixtureSecrets(t)

	item, found, err := source.Fetch(context.Background(), "gemini:gemini-1")
	if err != nil || !found {
		t.Fatalf("fetch: found=%v err=%v", found, err)
	}
	if item.Key != secrets["gemini:gemini-1"] {
		t.Error("fetch 未按夹具取回密钥")
	}

	if _, found, err := source.Fetch(context.Background(), "cc-switch:不存在的键"); err != nil || found {
		t.Errorf("未知 sourceKey 应返回 found=false，得到 found=%v err=%v", found, err)
	}
	// 未支持的 app_type 前缀不能被当成合法键。
	if _, found, _ := source.Fetch(context.Background(), "mcod:unknown-app"); found {
		t.Error("未支持的 app_type 不该被解析")
	}
}

// TestCCSwitchFetchReportsReadFailure 库存在但读不了时 Fetch 必须报错，
// 不能把「读库失败」静默当成「这条不存在」——那会把库损坏显示成配置被删。
// 反向对照：同一个坏库 Scan 会报「读取 providers 表失败」，Fetch 必须一致地报错。
func TestCCSwitchFetchReportsReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	if err := os.WriteFile(path, []byte("this is definitely not a sqlite database"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	source := NewCCSwitch(path)
	_, found, err := source.Fetch(context.Background(), "claude:x")
	if err == nil {
		t.Fatalf("库读不了时 Fetch 必须报错，得到 found=%v err=nil", found)
	}
	if found {
		t.Error("读失败不该返回 found=true")
	}
	if _, scanErr := source.Scan(context.Background()); scanErr == nil {
		t.Error("同一个坏库 Scan 应报错，用于对照 Fetch 的错误不被吞")
	}
}
