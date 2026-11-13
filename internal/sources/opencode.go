package sources

import (
	"context"
)

// sourceOpencode 是来源标识，与 docs/CONTRACT.md 第 4.1 节的 sourceId 一致。
const sourceOpencode = "opencode"

// opencode 读 XDG 目录下的 opencode.json：顶层 provider 对象，每条供应商自带 npm 包名、
// options{apiKey,baseURL} 与 models 映射，apiKey 是明文。
type opencode struct {
	path string
	// resolveErr 非空表示路径没解析出来，此时 path 为空，扫描侧据此报错而不是猜路径。
	resolveErr error
}

func (s *opencode) ID() string       { return sourceOpencode }
func (s *opencode) Label() string    { return sourceOpencode }
func (s *opencode) Path() string     { return s.path }
func (s *opencode) PathError() error { return s.resolveErr }

func (s *opencode) Scan(ctx context.Context) (ScanResult, error) {
	if s.resolveErr != nil {
		return ScanResult{}, s.resolveErr
	}
	if err := ctx.Err(); err != nil {
		return ScanResult{}, err
	}
	return scanProviderOptionsFile(s, false)
}

// Fetch 按 sourceKey（就是 provider id）现取一条完整配置，密钥在这一步才回到内存。
func (s *opencode) Fetch(ctx context.Context, sourceKey string) (SourceItem, bool, error) {
	return fetchFromScan(ctx, s, sourceKey)
}

// scanProviderOptionsFile 是 opencode 与 mimocode 共用的扫描骨架：
// 读文件 → 需要时剥注释 → 解析。文件不存在不是错误，只是没装/没配。
func scanProviderOptionsFile(source Source, stripComments bool) (ScanResult, error) {
	raw, err := readSourceFile(source.Path())
	if err != nil {
		if IsMissing(err) {
			return ScanResult{Path: source.Path()}, nil
		}
		return ScanResult{}, Bounded(source.ID(), sourceErrorText(err))
	}
	if stripComments {
		raw = []byte(stripJSONComments(string(raw)))
	}
	return parseProviderOptions(source.Path(), source.ID(), raw)
}
