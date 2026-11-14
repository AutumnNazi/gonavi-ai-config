// Package sources 实现 cc-switch 与 magpie 两个本地来源的探测、扫描与密钥现取现用。
// 密钥只在本包与 import.apply 之间的内存里流转，绝不进入任何返回体或错误文案。
package sources

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// SourceItem 是一条可选供应商。Key 是密钥，用 json:"-" 挡在返回体之外——
// 契约硬约束是「返回体里绝不能出现密钥」，靠类型系统保证比靠人工检查可靠。
type SourceItem struct {
	SourceID  string
	SourceKey string
	Name      string
	BaseURL   string
	APIFormat string
	Model     string
	Models    []string
	HasKey    bool
	AppType   string
	Current   bool

	// Key 与 Headers 只在 Fetch 时取值，随导入请求下发给宿主，因此它们必须带
	// json:"-"：给页面看的 contract.ScanItem 里本就没有能装密钥的字段，而这两个标签
	// 是第二道锁——只要有人不小心把这个内部结构序列化一次，密钥就会以字段名出现。
	Key     string            `json:"-"`
	Headers map[string]string `json:"-"`
}

// ScanResult 是一个来源的扫描产出。
type ScanResult struct {
	Path           string
	Items          []SourceItem
	KeyUnavailable bool
	// Skipped 与 Disabled 必须分开计数：前者是「我们解析不出可导入的条目」（缺端点、
	// 形状不认识），后者是「上游自己把这条关掉了」。合成一个数会让页面把用户自己关掉的
	// 条目说成本插件不认识，用户于是去上游白折腾一圈。两者都不许静默吞掉——读到了条目
	// 却一条不报，用户只会看到页面上数字对不上。
	Skipped  int
	Disabled int
	// DegradedReason 非空表示这次读的不是主数据源：主配置存在却读不了/解析失败，已回退到
	// 运行态投影。调用方据此在 sources[].notice 里告诉用户「你看到的不是主配置」，
	// 否则一个被改坏的主配置会被静默忽略，用户还以为一切正常。
	DegradedReason string
}

// Source 是一个本地数据来源。
type Source interface {
	ID() string
	Label() string
	// Path 返回数据文件路径。路径无法解析时返回空串，原因见 PathError。
	Path() string
	// PathError 返回路径无法解析的原因；nil 表示 Path() 可用。
	PathError() error
	Scan(ctx context.Context) (ScanResult, error)
	// Fetch 按 sourceKey 现取一条完整配置。
	Fetch(ctx context.Context, sourceKey string) (SourceItem, bool, error)
}

// 路径覆盖环境变量：测试与独立运行用，宿主启动时不设。
const (
	EnvCCSwitchDB  = "GONAVI_AI_CONFIG_CCSWITCH_DB"
	EnvMagpieState = "GONAVI_AI_CONFIG_MAGPIE_STATE"
	// magpie 的源配置（明文密钥所在）在 XDG 目录，运行态文件只作兜底：前者是 Magpie 自己
	// 写配置的地方，后者是它给界面看的投影，密钥在那里被替换成掩码。
	EnvMagpieConfig   = "GONAVI_AI_CONFIG_MAGPIE_CONFIG"
	EnvCCGUIConfig    = "GONAVI_AI_CONFIG_CCGUI_CONFIG"
	EnvOpencodeConfig = "GONAVI_AI_CONFIG_OPENCODE_CONFIG"
	EnvMimocodeConfig = "GONAVI_AI_CONFIG_MIMOCODE_CONFIG"
)

// homeResolver 是家目录解析入口。测试注入失败场景用；生产路径就是 userHome。
var homeResolver = userHome

// New 按当前环境构造全部来源。
//
// magpie 的主路径是 XDG 源配置（有明文密钥），运行态投影只作兜底——顺序反了就等于永远
// 拿不到密钥，页面还会提示「需手工补填」，把一个可解的问题显示成不可解。
func New() []Source {
	ccPath, ccErr := resolveSourcePath(EnvCCSwitchDB, ".cc-switch", "cc-switch.db")
	mgPath, mgErr := resolveXDGSourcePath(EnvMagpieConfig, "magpie", "providers.json")
	mgFallbackPath, mgFallbackErr := resolveSourcePath(EnvMagpieState, ".cache", "magpie", "providers-state.json")
	cgPath, cgErr := resolveSourcePath(EnvCCGUIConfig, ".ccgui", "config.json")
	ocPath, ocErr := resolveXDGSourcePath(EnvOpencodeConfig, "opencode", "opencode.json")
	mcPath, mcErr := resolveXDGSourcePath(EnvMimocodeConfig, "mimocode", "mimocode.jsonc")
	return []Source{
		&ccSwitch{path: ccPath, resolveErr: ccErr},
		&magpie{path: mgPath, fallbackPath: mgFallbackPath, resolveErr: mgErr, fallbackErr: mgFallbackErr},
		&ccgui{path: cgPath, resolveErr: cgErr},
		&opencode{path: ocPath, resolveErr: ocErr},
		&mimocode{path: mcPath, resolveErr: mcErr},
	}
}

// resolveSourcePath 解析一个来源的数据文件绝对路径：显式覆盖变量优先，其次家目录。
//
// 解析不到就返回错误，绝不退化成相对路径——宿主以受限环境启动插件进程时家目录可能不可见，
// 相对路径会随进程工作目录飘走，看起来像合法路径却永远指向错的地方，属于静默失败。
func resolveSourcePath(overrideEnv string, parts ...string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(overrideEnv)); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("override path must be absolute")
		}
		return override, nil
	}
	home, err := homeResolver()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("user home is not an absolute path")
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

// xdgConfigHome 返回 XDG 配置根：显式 XDG_CONFIG_HOME 优先（必须是绝对路径），否则家目录下的 .config。
//
// opencode、mimocode 与 magpie 的源配置都按 XDG 约定落在这里，所以三者共用一个解析入口，
// 免得各自实现一遍、各自在受限环境里踩同一个坑。
func xdgConfigHome() (string, error) {
	if override := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		return override, nil
	}
	home, err := homeResolver()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("user home is not an absolute path")
	}
	return filepath.Join(home, ".config"), nil
}

// resolveXDGSourcePath 解析位于 XDG 配置根下的来源文件路径：显式覆盖变量优先，其次 XDG_CONFIG_HOME。
// 与 resolveSourcePath 一样，解析不到就报错，绝不退化成相对路径。
func resolveXDGSourcePath(overrideEnv string, parts ...string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(overrideEnv)); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("override path must be absolute")
		}
		return override, nil
	}
	root, err := xdgConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

// userHome 解析用户家目录。
//
// 顺序有意为之：os/user 在 Windows 上走进程令牌与注册表，不读环境变量；而 os.UserHomeDir()
// 读的 USERPROFILE 不在宿主的插件进程环境白名单里（internal/plugin/process.go:174-179），
// 受限启动时它必然失败。所以先试 os/user，再用 os.UserHomeDir 兜底。
func userHome() (string, error) {
	if current, err := user.Current(); err == nil {
		if home := strings.TrimSpace(current.HomeDir); home != "" {
			return home, nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if trimmed := strings.TrimSpace(home); trimmed != "" {
			return trimmed, nil
		}
	}
	return "", errors.New("cannot resolve user home")
}

// ErrMessage 携带一段有界、不含密钥与堆栈的 error 文案，供 sources[].error 使用。
type ErrMessage struct{ Text string }

func (e *ErrMessage) Error() string { return e.Text }

// Bounded 把内部错误折成有界文案。数据库错误可能带上参数片段，所以只保留类别描述。
func Bounded(sourceID, reason string) error {
	return &ErrMessage{Text: sourceID + ": " + reason}
}

// IsMissing 判断文件/目录不存在，用于 detected=false 的分支。
func IsMissing(err error) bool { return errors.Is(err, os.ErrNotExist) }

// dedupe 保序去重，模型列表的语义是「有序集合」。
func dedupe(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// identity 是判重身份，规则与契约 §2 的宿主去重身份一致：名称大小写不敏感、去空白；
// 地址只小写协议与主机名（大小写不敏感的那部分），路径与查询原样保留，去凭据/fragment、
// 去路径尾部 "/"。两边必须同规则——页面按它标「冲突」，宿主按它判「重复」，规则一旦不同，
// 页面就会预判成「会新建」而宿主实际跳过。
//
// 路径大小写是区分供应商的：/tenant/A 与 /tenant/a 是两个地址，整串小写会把它们合成一条。
func identity(name, baseURL string) string {
	return strings.ToLower(strings.TrimSpace(name)) + "\x00" + normalizeIdentityBaseURL(baseURL)
}

// normalizeIdentityBaseURL 归一化判重用的地址：小写协议与主机名，去掉凭据与 fragment，
// 去掉路径尾部的 "/"；路径与查询大小写原样保留。解析失败就退回原文。
// 与宿主的 pluginAIProviderImportIdentityURL 同规则，保证两侧判重一致。
func normalizeIdentityBaseURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	parsed.User = nil
	parsed.Fragment = ""
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path != "" {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	return parsed.String()
}
