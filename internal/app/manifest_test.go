package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	goplugin "github.com/Syngnat/GoNavi/sdk/go"
)

// 与 scripts/build.mjs 里的常量一致；两边任何一处改了另一处必须跟着改。
const (
	inlineHTMLToken = "__INLINE_FRONTEND_INDEX_HTML__"
	pageEntryPath   = "frontend/index.html"
)

// repoRoot 从测试文件位置向上找 go.mod。契约与清单都在仓库根，
// 这样测试不会因为工作目录不同而假绿。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("找不到仓库根（没有 go.mod）")
		}
		dir = parent
	}
}

// TestManifestValidatesAgainstSDK 用 SDK 自带校验器验证清单，
// 保证打出来的包能被宿主安装，而不是靠肉眼比对契约文本。
func TestManifestValidatesAgainstSDK(t *testing.T) {
	path := filepath.Join(repoRoot(t), "gonavi.plugin.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := goplugin.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if err := goplugin.ValidateManifest(manifest); err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
}

// TestManifestPageIsInlinedByBuild 锁定「页面内联」这条约定：
// html.source 必须是内联占位令牌（不是文件路径），且打包脚本确实替换它。
// 宿主把 html.source 直接当页面文档渲染，写成路径只会白屏。
func TestManifestPageIsInlinedByBuild(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "gonavi.plugin.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := goplugin.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Contributes == nil || len(manifest.Contributes.Pages) != 1 {
		t.Fatal("清单应恰好声明一个页面")
	}
	source := manifest.Contributes.Pages[0].HTML
	if source == nil {
		t.Fatal("页面应使用 html.source")
	}
	if source.Source != inlineHTMLToken {
		t.Errorf("模板 html.source = %q, want 内联占位令牌 %q", source.Source, inlineHTMLToken)
	}
	if strings.HasSuffix(strings.TrimSpace(source.Source), ".html") {
		t.Error("模板 html.source 不能是文件路径：宿主会把它当文档内容渲染")
	}
	script, err := os.ReadFile(filepath.Join(root, "scripts", "build.mjs"))
	if err != nil {
		t.Fatalf("read build script: %v", err)
	}
	for _, want := range []string{inlineHTMLToken, pageEntryPath} {
		if !strings.Contains(string(script), want) {
			t.Errorf("打包脚本缺少 %q，页面不会被内联进清单", want)
		}
	}
}

// TestManifestDeclaresContractCommands 清单声明的命令 id 必须与后端注册表一致。
func TestManifestDeclaresContractCommands(t *testing.T) {
	path := filepath.Join(repoRoot(t), "gonavi.plugin.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := goplugin.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	declared := make(map[string]bool)
	if manifest.Contributes != nil {
		for _, command := range manifest.Contributes.Commands {
			declared[command.ID] = true
		}
	}
	registered := New(nil).Handlers()
	for id := range registered {
		if !declared[id] {
			t.Errorf("后端注册了清单未声明的命令 %q", id)
		}
	}
	for id := range declared {
		if _, ok := registered[id]; !ok {
			t.Errorf("清单声明的命令 %q 没有后端处理器", id)
		}
	}
}
