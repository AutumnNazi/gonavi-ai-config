//go:build livescan

// 三个新来源的本机真跑入口：go test -tags livescan ./internal/sources/ -run TestLiveScanNewSources -v
// 与 live_scan_test.go 同一套路：默认构建不含它，CI 与普通 go test 都不依赖本机数据。
// 输出里只有路径与元数据，不含任何密钥。
package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestLiveScanNewSources 用本机真实配置跑 ccgui / opencode / mimocode：打印 detected 与 count，
// 并核对序列化结果里没有密钥。sources.New() 的接线由协调者负责，所以这里直接构造来源。
func TestLiveScanNewSources(t *testing.T) {
	ccPath, ccErr := resolveSourcePath(EnvCCGUIConfig, ".ccgui", "config.json")
	ocPath, ocErr := resolveXDGSourcePath(EnvOpencodeConfig, "opencode", "opencode.json")
	mmPath, mmErr := resolveXDGSourcePath(EnvMimocodeConfig, "mimocode", "mimocode.jsonc")
	sources := []Source{
		&ccgui{path: ccPath, resolveErr: ccErr},
		&opencode{path: ocPath, resolveErr: ocErr},
		&mimocode{path: mmPath, resolveErr: mmErr},
	}
	for _, source := range sources {
		if err := source.PathError(); err != nil {
			t.Fatalf("%s 路径解析失败：%v", source.ID(), err)
		}
		if _, err := statPath(source.Path()); err != nil {
			t.Fatalf("%s 本机没有文件：%v", source.ID(), err)
		}
		result, err := source.Scan(context.Background())
		if err != nil {
			t.Fatalf("%s scan: %v", source.ID(), err)
		}
		hasKey := 0
		for _, item := range result.Items {
			if item.HasKey {
				hasKey++
			}
		}
		total := sourceEntryTotal(t, source.ID(), source.Path())
		fmt.Printf("id=%-9s detected=true count=%-3d skipped=%-3d disabled=%-3d 条目总数=%-3d hasKey=%-3d\n",
			source.ID(), len(result.Items), result.Skipped, result.Disabled, total, hasKey)
		if got := len(result.Items) + result.Skipped + result.Disabled; got != total {
			t.Errorf("%s: count+skipped+disabled = %d, want 来源总数 %d", source.ID(), got, total)
		}
		fmt.Printf("   path=%s\n", source.Path())
		for index, item := range result.Items {
			fmt.Printf("   [%d] key=%-46s name=%-20s base=%-44s fmt=%-16s model=%-34s models=%-2d hasKey=%v\n",
				index, item.SourceKey, truncate(item.Name, 20), truncate(item.BaseURL, 44),
				item.APIFormat, truncate(item.Model, 34), len(item.Models), item.HasKey)
		}
		if len(result.Items) == 0 {
			t.Errorf("%s 本机扫描 0 条", source.ID())
		}
		// 真密钥不得出现在序列化结果里：拿条目自己持有的密钥当探针，不必另读一份真值。
		body, err := json.Marshal(result.Items)
		if err != nil {
			t.Fatalf("%s marshal: %v", source.ID(), err)
		}
		for _, item := range result.Items {
			if item.Key != "" && strings.Contains(string(body), item.Key) {
				t.Errorf("%s 的返回体里出现了密钥子串", source.ID())
			}
		}
	}
	fmt.Printf("=== 与 live_scan_test 的 cc-switch / magpie 合计五个来源 ===\n")
}
