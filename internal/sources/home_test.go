package sources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonavi-ai-config/internal/contract"
)

// unsetHomeEnv 真正删掉家目录相关变量。t.Setenv 只能设不能删，而这里要复现宿主受限环境
// （internal/plugin/process.go 的环境白名单不含 USERPROFILE），必须真的 unset。
// 同时删掉全部路径覆盖变量与 XDG_CONFIG_HOME：留着任何一个，用例的「只看家目录」前提就被
// 外部环境改写了，结论会随跑测试的机器而变。
func unsetHomeEnv(t *testing.T) {
	t.Helper()
	names := []string{
		"USERPROFILE", "HOME", "HOMEDRIVE", "HOMEPATH", "XDG_CONFIG_HOME",
		EnvCCSwitchDB, EnvMagpieConfig, EnvMagpieState,
		EnvCCGUIConfig, EnvOpencodeConfig, EnvMimocodeConfig,
	}
	for _, name := range names {
		previous, existed := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
		if existed {
			t.Cleanup(func() { _ = os.Setenv(name, previous) })
		}
	}
}

// TestHomeResolutionIgnoresHomeEnvironment 是本次回归的靶心：宿主进程环境里没有
// USERPROFILE/HOME，家目录仍必须解析成绝对路径，两个来源都不许产出相对路径。
func TestHomeResolutionIgnoresHomeEnvironment(t *testing.T) {
	unsetHomeEnv(t)

	home, err := userHome()
	if err != nil {
		t.Fatalf("环境变量被清空后家目录解析失败：%v", err)
	}
	if !filepath.IsAbs(home) {
		t.Fatalf("家目录不是绝对路径：%q", home)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("解析出的家目录不存在：%v", err)
	}

	for _, source := range New() {
		if err := source.PathError(); err != nil {
			t.Fatalf("%s 路径解析失败：%v", source.ID(), err)
		}
		if source.Path() == "" {
			t.Fatalf("%s 路径为空", source.ID())
		}
		if !filepath.IsAbs(source.Path()) {
			t.Fatalf("%s 产出了相对路径 %q（宿主内会指向进程工作目录）", source.ID(), source.Path())
		}
		if !strings.HasPrefix(source.Path(), home) {
			t.Errorf("%s 路径 %q 不在家目录 %q 之下", source.ID(), source.Path(), home)
		}
	}
}

// TestScanWithoutHomeEnvKeepsDetectionHonest 清空家目录变量后跑完整扫描：
// 路径必须是家目录下的绝对路径，且探测结论必须与文件真实存在性一致。
func TestScanWithoutHomeEnvKeepsDetectionHonest(t *testing.T) {
	unsetHomeEnv(t)

	home, err := userHome()
	if err != nil {
		t.Fatalf("家目录解析失败：%v", err)
	}
	result, err := NewScanner(New(), nil).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("扫描不该整批失败：%v", err)
	}
	if len(result.Sources) != len(sourceIDs()) {
		t.Fatalf("sources = %d, want %d", len(result.Sources), len(sourceIDs()))
	}

	// 主路径一律按契约 §4.1：magpie 是 XDG 源配置，不是运行态投影。
	expected := map[string]string{
		contract.SourceCCSwitch: filepath.Join(home, ".cc-switch", "cc-switch.db"),
		contract.SourceMagpie:   filepath.Join(home, ".config", "magpie", "providers.json"),
		contract.SourceCCGUI:    filepath.Join(home, ".ccgui", "config.json"),
		contract.SourceOpencode: filepath.Join(home, ".config", "opencode", "opencode.json"),
		contract.SourceMimocode: filepath.Join(home, ".config", "mimocode", "mimocode.jsonc"),
	}
	for _, status := range result.Sources {
		want, ok := expected[status.ID]
		if !ok {
			t.Fatalf("未知来源 %q", status.ID)
		}
		if status.Path != want {
			t.Errorf("%s path = %q, want %q", status.ID, status.Path, want)
		}
		if !filepath.IsAbs(status.Path) {
			t.Errorf("%s 路径不是绝对路径：%q", status.ID, status.Path)
		}
		if status.Error != "" {
			t.Errorf("%s 不该有错误：%s", status.ID, status.Error)
		}
		_, statErr := os.Stat(want)
		switch {
		case statErr == nil:
			if !status.Detected {
				t.Errorf("%s 文件存在却 detected=false", status.ID)
			}
			// 文件存在但一条都没解析出来，只有在「也没有被跳过、没有被停用的条目」时才说明是
			// 静默丢数据。契约 §4.1 允许合法地返回 0 条：别的机器上 ~/.ccgui/config.json 可能
			// 没有已支持的 codex 分节（那些条目进 skipped），magpie 的条目也可能全被 off 关掉
			// （那些进 disabled）。把这两个计数也判成失败会误伤合法结果。
			if status.Count == 0 && status.Skipped == 0 && status.Disabled == 0 {
				t.Errorf("%s 文件存在却既没有条目也没有跳过计数", status.ID)
			}
		case os.IsNotExist(statErr):
			if status.Detected {
				t.Errorf("%s 文件不存在却 detected=true", status.ID)
			}
		default:
			t.Fatalf("stat %s: %v", want, statErr)
		}
	}
	// 本机装了 cc-switch 时，必须真扫出条目——这是「宿主内 count>0」的最小复现。
	if _, statErr := os.Stat(expected[contract.SourceCCSwitch]); statErr == nil && len(result.Items) == 0 {
		t.Error("cc-switch 库存在却一条都没扫出来")
	}
}

// TestUnresolvableHomeReportsErrorNotRelativePath 家目录彻底解析不到时，
// 必须 detected=false、path 留空、error 写明原因；绝不许退化成相对路径。
func TestUnresolvableHomeReportsErrorNotRelativePath(t *testing.T) {
	unsetHomeEnv(t)
	previous := homeResolver
	homeResolver = func() (string, error) { return "", errors.New("cannot resolve user home") }
	t.Cleanup(func() { homeResolver = previous })

	result, err := NewScanner(New(), nil).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("单个来源路径不可解析不该让整批失败：%v", err)
	}
	if len(result.Sources) != len(sourceIDs()) {
		t.Fatalf("sources = %d, want %d", len(result.Sources), len(sourceIDs()))
	}
	for _, status := range result.Sources {
		if status.Detected {
			t.Errorf("%s 家目录解析失败时 detected 必须为 false", status.ID)
		}
		if status.Path != "" {
			t.Errorf("%s 家目录解析失败时 path 必须留空，得到 %q", status.ID, status.Path)
		}
		if !strings.Contains(status.Error, "cannot resolve user home") {
			t.Errorf("%s error 未写明原因：%q", status.ID, status.Error)
		}
		if len(status.Error) > 200 {
			t.Errorf("%s error 文案过长：%d 字节", status.ID, len(status.Error))
		}
	}
	if len(result.Items) != 0 {
		t.Errorf("路径不可解析时不该产出条目，得到 %d 条", len(result.Items))
	}
}

// TestRelativeOverrideIsRejected 覆盖变量也必须是绝对路径：
// 相对路径是同一类静默失败，必须当场报错而不是照单全收。
func TestRelativeOverrideIsRejected(t *testing.T) {
	unsetHomeEnv(t)
	t.Setenv(EnvCCSwitchDB, filepath.Join(".cc-switch", "cc-switch.db"))

	result, err := NewScanner(New(), nil).Scan(context.Background(), contract.ScanRequest{Sources: []string{contract.SourceCCSwitch}})
	if err != nil {
		t.Fatalf("扫描不该整批失败：%v", err)
	}
	status := result.Sources[0]
	if status.Detected {
		t.Error("相对覆盖路径不该被当成探测成功")
	}
	if status.Path != "" {
		t.Errorf("相对覆盖路径应留空 path，得到 %q", status.Path)
	}
	if !strings.Contains(status.Error, "absolute") {
		t.Errorf("error 应说明要求绝对路径：%q", status.Error)
	}
}

// TestAbsoluteOverrideStillWins 绝对覆盖路径照旧生效，不因上面的收紧而失效。
func TestAbsoluteOverrideStillWins(t *testing.T) {
	unsetHomeEnv(t)
	dbPath := writeCCSwitchFixture(t)
	t.Setenv(EnvCCSwitchDB, dbPath)

	result, err := NewScanner(New(), nil).Scan(context.Background(), contract.ScanRequest{Sources: []string{contract.SourceCCSwitch}})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	status := result.Sources[0]
	if status.Path != dbPath {
		t.Errorf("path = %q, want %q", status.Path, dbPath)
	}
	if !status.Detected || status.Count == 0 {
		t.Errorf("覆盖路径下应探测到并扫出条目：%+v", status)
	}
}
