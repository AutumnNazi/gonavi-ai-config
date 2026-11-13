package sources

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gonavi-ai-config/internal/contract"

	_ "modernc.org/sqlite"
)

// 夹具放在 testdata/ 下，样例照 docs/CC-SWITCH-FORMAT.md 与 docs/MAGPIE-FORMAT.md 的实测形态。
// 里面的密钥值是明显的占位假值：断言期望值一律从夹具本身推导，
// 不在测试里另写一份常量，避免两边漂移后测试假绿。
type fixtureRow struct {
	ID             string          `json:"id"`
	AppType        string          `json:"app_type"`
	Name           string          `json:"name"`
	IsCurrent      int             `json:"is_current"`
	WebsiteURL     string          `json:"website_url"`
	Meta           json.RawMessage `json:"meta"`
	SettingsConfig json.RawMessage `json:"settings_config"`
}

// testdataPath 用测试文件位置定位 testdata，测试工作目录变了也不会找不到夹具。
func testdataPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "testdata")
	return filepath.Join(append([]string{root}, parts...)...)
}

func loadCCSwitchRows(t *testing.T) []fixtureRow {
	t.Helper()
	raw, err := os.ReadFile(testdataPath(t, "cc-switch", "providers.json"))
	if err != nil {
		t.Fatalf("读夹具失败：%v", err)
	}
	var rows []fixtureRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("夹具不是合法 JSON：%v", err)
	}
	if len(rows) == 0 {
		t.Fatal("夹具没有任何行")
	}
	return rows
}

// fixtureEnv 把 6 个路径覆盖变量全部指到 testdata 夹具。夹具齐全时测试不碰真实用户目录，
// 而 New() 走的仍是生产注册表本身，不是测试另拼的一份来源清单——注册表漏接一个来源、
// 或某个来源的覆盖变量没接上，都会在用到它的用例里红。
func fixtureEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvCCSwitchDB, writeCCSwitchFixture(t))
	t.Setenv(EnvMagpieConfig, magpieConfigFixturePath(t))
	t.Setenv(EnvMagpieState, magpieStateFixturePath(t))
	t.Setenv(EnvCCGUIConfig, newSourceFixturePath(t, sourceCCGUI))
	t.Setenv(EnvOpencodeConfig, newSourceFixturePath(t, sourceOpencode))
	t.Setenv(EnvMimocodeConfig, newSourceFixturePath(t, sourceMimocode))
}

// fixtureSources 用生产装配构造全部来源，断言的对象因此是「生产注册了哪几个来源」。
func fixtureSources(t *testing.T) []Source {
	t.Helper()
	fixtureEnv(t)
	return New()
}

// magpieConfigFixturePath 是契约 §4.1 里 magpie 的主路径夹具（XDG 源配置，明文密钥）。
func magpieConfigFixturePath(t *testing.T) string {
	t.Helper()
	path := testdataPath(t, "magpie", "providers.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("magpie 主路径夹具缺失：%v", err)
	}
	return path
}

// writeCCSwitchFixture 用夹具行建一个最小 providers 表库，只含用到的列。
func writeCCSwitchFixture(t *testing.T) string {
	t.Helper()
	rows := loadCCSwitchRows(t)
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("create table providers (id text, app_type text, name text, settings_config text, website_url text, category text, created_at integer, sort_index integer, notes text, icon text, icon_color text, meta text, is_current boolean, in_failover_queue boolean, cost_multiplier text, limit_daily_usd text, limit_monthly_usd text, provider_type text)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for index, row := range rows {
		if _, err := db.Exec(
			"insert into providers (id, app_type, name, settings_config, website_url, meta, is_current, sort_index) values (?,?,?,?,?,?,?,?)",
			row.ID, row.AppType, row.Name, string(row.SettingsConfig), row.WebsiteURL, string(row.Meta), row.IsCurrent, index,
		); err != nil {
			t.Fatalf("insert row: %v", err)
		}
	}
	return path
}

// fixtureSecrets 返回夹具里每一行的密钥值，键是 sourceKey。
// 「返回体不含密钥」的断言与逐条映射断言都用它，期望值只有一个来源。
func fixtureSecrets(t *testing.T) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, row := range loadCCSwitchRows(t) {
		if secret := rowSecret(t, row); secret != "" {
			out[row.AppType+":"+row.ID] = secret
		}
	}
	return out
}

func allFixtureSecrets(t *testing.T) []string {
	t.Helper()
	seen := make(map[string]bool)
	var out []string
	for _, secret := range fixtureSecrets(t) {
		if !seen[secret] {
			seen[secret] = true
			out = append(out, secret)
		}
	}
	if len(out) == 0 {
		t.Fatal("夹具里没有任何密钥值，密钥断言会空过")
	}
	return out
}

// sourceEntryTotal 从一个来源的数据文件独立数出「真正读到的条目总数」：
// cc-switch 数表行数，magpie 数 providers 数组长度，ccgui 数各分节 providers 成员的键数之和，
// opencode / mimocode 数 provider 对象的键数。
//
// 有意不复用解析器的产出：拿解析器自己的结果验证解析器等于没验。计数自洽
// （count + skipped + disabled 等于来源总数）要的正是这份独立数出来的总数。
func sourceEntryTotal(t *testing.T, sourceID, path string) int {
	t.Helper()
	if sourceID == contract.SourceCCSwitch {
		db, err := sql.Open("sqlite", "file:"+sqliteURIPath(path)+"?mode=ro")
		if err != nil {
			t.Fatalf("打开 %s 数据文件失败：%v", sourceID, err)
		}
		defer db.Close()
		var total int
		if err := db.QueryRow("select count(*) from providers").Scan(&total); err != nil {
			t.Fatalf("数 %s 的 providers 行失败：%v", sourceID, err)
		}
		return total
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读数据文件失败 %s：%v", path, err)
	}
	// 剥注释对不含注释的正经 JSON 是恒等变换，所以 .json 与 .jsonc 共用一条读法。
	text := stripJSONComments(string(raw))
	switch sourceID {
	case contract.SourceMagpie:
		var doc struct {
			Providers []json.RawMessage `json:"providers"`
		}
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			t.Fatalf("%s 不是合法 JSON：%v", path, err)
		}
		return len(doc.Providers)
	case sourceCCGUI:
		var sections map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &sections); err != nil {
			t.Fatalf("%s 不是合法 JSON：%v", path, err)
		}
		total := 0
		for _, sectionRaw := range sections {
			var section map[string]json.RawMessage
			if json.Unmarshal(sectionRaw, &section) != nil {
				continue
			}
			var providers map[string]json.RawMessage
			// 没有 providers 成员的是配置项不是分节：这一句必然解析失败，跳过。
			if json.Unmarshal(section["providers"], &providers) != nil {
				continue
			}
			total += len(providers)
		}
		return total
	case sourceOpencode, sourceMimocode:
		var config struct {
			Provider map[string]json.RawMessage `json:"provider"`
		}
		if err := json.Unmarshal([]byte(text), &config); err != nil {
			t.Fatalf("%s 不是合法 JSON：%v", path, err)
		}
		return len(config.Provider)
	}
	t.Fatalf("sourceEntryTotal 不认识来源 %s", sourceID)
	return 0
}

// rowSecret 按 app_type 从夹具行里取密钥，与解析器的取值位置一一对应。
func rowSecret(t *testing.T, row fixtureRow) string {
	t.Helper()
	var config map[string]any
	if err := json.Unmarshal(row.SettingsConfig, &config); err != nil {
		t.Fatalf("夹具 settings_config 不是合法 JSON：%v", err)
	}
	stringAt := func(container map[string]any, name string) string {
		value, _ := container[name].(string)
		return value
	}
	switch row.AppType {
	case "claude", "claude-desktop":
		env, _ := config["env"].(map[string]any)
		return stringAt(env, "ANTHROPIC_AUTH_TOKEN")
	case "codex":
		auth, _ := config["auth"].(map[string]any)
		return stringAt(auth, "OPENAI_API_KEY")
	case "gemini":
		env, _ := config["env"].(map[string]any)
		return stringAt(env, "GEMINI_API_KEY")
	case "opencode":
		options, _ := config["options"].(map[string]any)
		return stringAt(options, "apiKey")
	case "grokbuild":
		// grokbuild 的密钥写在 TOML 文本里，按最简特征取 api_key = "..." 的值。
		text := stringAt(config, "config")
		for _, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "api_key") {
				return strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "api_key")), "= \"")
			}
		}
		return ""
	case "hermes":
		return stringAt(config, "api_key")
	default:
		return ""
	}
}
