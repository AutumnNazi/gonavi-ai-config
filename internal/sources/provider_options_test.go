package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// describeItem 描述条目用于断言输出。
// 刻意不含 Key：密钥不得进任何输出，测试日志同理——用 %+v 直接打印 SourceItem 会把密钥写进日志。
func describeItem(item SourceItem) string {
	return fmt.Sprintf("{sourceKey:%q name:%q baseUrl:%q apiFormat:%q model:%q models:%v hasKey:%v appType:%q current:%v}",
		item.SourceKey, item.Name, item.BaseURL, item.APIFormat, item.Model, item.Models,
		item.HasKey, item.AppType, item.Current)
}

// sameItem 逐字段比对。Models 用连接后的字符串比：nil 与空切片在协议上没有区别，
// 用 reflect.DeepEqual 会把它们判成不同，测试就会因为无关差异变红。
func sameItem(t *testing.T, label string, got, want SourceItem) {
	t.Helper()
	if got.SourceID != want.SourceID || got.SourceKey != want.SourceKey || got.Name != want.Name ||
		got.BaseURL != want.BaseURL || got.APIFormat != want.APIFormat || got.Model != want.Model ||
		got.HasKey != want.HasKey || got.AppType != want.AppType || got.Current != want.Current ||
		strings.Join(got.Models, "|") != strings.Join(want.Models, "|") {
		t.Errorf("%s 映射不符：\n  got  = %s\n  want = %s", label, describeItem(got), describeItem(want))
	}
}

// newSourceFixturePath 返回 testdata 下照本机真实文件形态写的夹具路径。
func newSourceFixturePath(t *testing.T, sourceID string) string {
	t.Helper()
	names := map[string]string{
		sourceCCGUI:    "config.json",
		sourceOpencode: "opencode.json",
		sourceMimocode: "mimocode.jsonc",
	}
	name, ok := names[sourceID]
	if !ok {
		t.Fatalf("未知来源 %q", sourceID)
	}
	path := testdataPath(t, sourceID, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s 夹具缺失：%v", sourceID, err)
	}
	return path
}

// itemsJSON 序列化扫描条目。除了未导出的 Key 字段（带 json:"-"）没有别处能承载密钥，
// 所以「返回体不含密钥」的断言就挂在它上面。
func itemsJSON(t *testing.T, items []SourceItem) []byte {
	t.Helper()
	body, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("序列化条目失败：%v", err)
	}
	return body
}

// assertNoSourceSecret 断言序列化结果里既没有密钥子串，也没有承载密钥的字段名。
func assertNoSourceSecret(t *testing.T, body []byte, secrets []string) {
	t.Helper()
	text := string(body)
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Errorf("返回体里出现了密钥子串 %q", secret)
		}
	}
	for _, forbidden := range []string{"\"Key\"", "\"apiKey\"", "\"api_key\"", "\"token\"", "\"secret\"", "\"authKey\""} {
		if strings.Contains(text, forbidden) {
			t.Errorf("返回体里出现了疑似密钥字段 %s", forbidden)
		}
	}
}

// TestSourceSecretGuardDetectsLeak 证明守卫本身有效：恒真的空断言等于没有断言。
func TestSourceSecretGuardDetectsLeak(t *testing.T) {
	leaked, err := json.Marshal([]map[string]any{{"name": "样本", "Key": "sk-fixture-leak-value"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	recorder := &testing.T{}
	assertNoSourceSecret(recorder, leaked, []string{"sk-fixture-leak-value"})
	if !recorder.Failed() {
		t.Fatal("守卫对含密钥的载荷没有报错，等于没有守卫")
	}
	// 只泄露字段名（值为空）也必须被抓到。
	recorder = &testing.T{}
	assertNoSourceSecret(recorder, []byte("{\"items\":[{\"apiKey\":\"\"}]}"), nil)
	if !recorder.Failed() {
		t.Fatal("守卫对空密钥字段名没有报错")
	}
}

// TestAppendUniqueDropsLaterDuplicate 契约要求同一次 scan 内 sourceKey 唯一：
// 重复的条目丢掉后者并计数，而不是两条都进返回体。
func TestAppendUniqueDropsLaterDuplicate(t *testing.T) {
	result := ScanResult{}
	seen := map[string]bool{}
	appendUnique(&result, seen, SourceItem{SourceKey: "custom", Name: "先出现的一条"})
	appendUnique(&result, seen, SourceItem{SourceKey: "other", Name: "另一条"})
	appendUnique(&result, seen, SourceItem{SourceKey: "custom", Name: "重复的一条"})
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2（重复的应被丢掉）", len(result.Items))
	}
	if result.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", result.Skipped)
	}
	if result.Items[0].Name != "先出现的一条" {
		t.Errorf("保留下来的应是先出现的那条，得到 %q", result.Items[0].Name)
	}
}

// TestSourceErrorTextIsBounded 错误文案必须有界，且绝不把路径带出去。
func TestSourceErrorTextIsBounded(t *testing.T) {
	somePath := filepath.Join("C:", "Users", "someone", ".config", "opencode", "opencode.json")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "文件不存在", err: os.ErrNotExist, want: sourceMissingText},
		{name: "包了一层的不存在", err: &os.PathError{Op: "open", Path: somePath, Err: os.ErrNotExist}, want: sourceMissingText},
		{name: "权限不足", err: os.ErrPermission, want: sourcePermissionText},
		{name: "文件过大", err: errors.New(sourceTooLargeText), want: sourceTooLargeText},
		{name: "其它读取失败", err: &os.PathError{Op: "open", Path: somePath, Err: errors.New("device not ready")}, want: sourceReadFailedText},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sourceErrorText(test.err)
			if got != test.want {
				t.Errorf("sourceErrorText = %q, want %q", got, test.want)
			}
			if got != "" && (strings.Contains(got, "someone") || strings.Contains(got, somePath)) {
				t.Errorf("错误文案里带上了路径：%q", got)
			}
		})
	}
}

// TestReadSourceFileRejectsOversizedFile 覆盖变量被误指到大文件时先按大小挡住，
// 而不是把整个文件读进内存再解析。
func TestReadSourceFileRejectsOversizedFile(t *testing.T) {
	oversized := filepath.Join(t.TempDir(), "huge.json")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Truncate 造稀疏文件，测试不必真写 4 MiB。
	if err := file.Truncate(sourceMaxConfigBytes + 1); err != nil {
		file.Close()
		t.Fatalf("truncate: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := readSourceFile(oversized); err == nil || !strings.Contains(err.Error(), sourceTooLargeText) {
		t.Fatalf("超限文件应被拒绝，得到 %v", err)
	}

	small := filepath.Join(t.TempDir(), "small.json")
	if err := os.WriteFile(small, []byte("{\"provider\":{}}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := readSourceFile(small)
	if err != nil {
		t.Fatalf("上限之内的文件读取失败：%v", err)
	}
	if string(raw) != "{\"provider\":{}}" {
		t.Errorf("读出的内容不符：%q", raw)
	}
}

// assertUniqueSourceKeys 契约硬约束：同一来源的条目 sourceKey 必须唯一。
func assertUniqueSourceKeys(t *testing.T, items []SourceItem) {
	t.Helper()
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item.SourceKey] {
			t.Errorf("sourceKey 重复：%q", item.SourceKey)
		}
		seen[item.SourceKey] = true
	}
	if len(items) == 0 {
		t.Fatal("没有任何条目，唯一性断言会空过")
	}
}

// containsString 判断值是否在期望集合里。
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// itoa 拼下标用，省得各处重复写 fmt.Sprintf。
func itoa(value int) string { return fmt.Sprintf("%d", value) }
