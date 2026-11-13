package sources

import (
	"context"
	"strings"
)

// sourceMimocode 是来源标识，与 docs/CONTRACT.md 第 4.1 节的 sourceId 一致。
const sourceMimocode = "mimocode"

// mimocode 读 XDG 目录下的 mimocode.jsonc：形状与 opencode.json 相同，
// 但扩展名是 .jsonc，文件里可以带 // 与 /* */ 注释，所以解析前先剥注释。
type mimocode struct {
	path string
	// resolveErr 非空表示路径没解析出来，此时 path 为空，扫描侧据此报错而不是猜路径。
	resolveErr error
}

func (s *mimocode) ID() string       { return sourceMimocode }
func (s *mimocode) Label() string    { return sourceMimocode }
func (s *mimocode) Path() string     { return s.path }
func (s *mimocode) PathError() error { return s.resolveErr }

func (s *mimocode) Scan(ctx context.Context) (ScanResult, error) {
	if s.resolveErr != nil {
		return ScanResult{}, s.resolveErr
	}
	if err := ctx.Err(); err != nil {
		return ScanResult{}, err
	}
	return scanProviderOptionsFile(s, true)
}

// Fetch 按 sourceKey（就是 provider id）现取一条完整配置，密钥在这一步才回到内存。
func (s *mimocode) Fetch(ctx context.Context, sourceKey string) (SourceItem, bool, error) {
	return fetchFromScan(ctx, s, sourceKey)
}

// stripJSONComments 剥掉 // 行注释与 /* */ 块注释，字符串里的 // 与 /* 原样保留——
// baseURL 的 "https://" 与 JSON 里的 URL 值就是最容易被误伤的地方。
// 块注释里的换行会写回输出，这样解析报错的行号不会因为注释而漂移。
//
// 有意不处理尾随逗号等其它 JSONC 宽松语法：需求只要求能读带注释的配置，
// 每多放宽一分就多一种「看起来解析成功其实读错了」的可能。
func stripJSONComments(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	inString := false
	escaped := false
	for i := 0; i < len(text); {
		char := text[i]
		if inString {
			out.WriteByte(char)
			i++
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		switch {
		case char == '"':
			inString = true
			out.WriteByte(char)
			i++
		case char == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case char == '/' && i+1 < len(text) && text[i+1] == '*':
			i += 2
			for i < len(text) && !(text[i] == '*' && i+1 < len(text) && text[i+1] == '/') {
				if text[i] == '\n' {
					out.WriteByte('\n')
				}
				i++
			}
			i += 2
		default:
			out.WriteByte(char)
			i++
		}
	}
	return out.String()
}
