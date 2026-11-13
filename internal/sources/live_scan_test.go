//go:build livescan

// 本机真跑入口：go test -tags livescan ./internal/sources/ -run TestLiveScan -v
// 默认构建不含它，所以 CI 与普通 go test 不会依赖本机数据。输出不含任何密钥。
package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
)

func TestLiveScan(t *testing.T) {
	result, err := NewScanner(New(), nil).Scan(context.Background(), contract.ScanRequest{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	fmt.Println("=== sources ===")
	// 逐来源核对计数自洽：count + skipped + disabled 必须等于数据文件里真正读到的条目总数。
	// 总数由 sourceEntryTotal 独立数出来（cc-switch 数表行数，其余数配置里的条目键）。
	for _, status := range result.Sources {
		total := 0
		if status.Detected && status.Error == "" {
			total = sourceEntryTotal(t, status.ID, status.Path)
		}
		fmt.Printf("id=%-10s label=%-9s detected=%-5v count=%-3d skipped=%-3d disabled=%-3d 条目总数=%-3d keyUnavailable=%-5v error=%q\n",
			status.ID, status.Label, status.Detected, status.Count, status.Skipped, status.Disabled,
			total, status.KeyUnavailable, status.Error)
		if total != 0 && status.Count+status.Skipped+status.Disabled != total {
			t.Errorf("%s: count+skipped+disabled = %d+%d+%d = %d, want 来源总数 %d",
				status.ID, status.Count, status.Skipped, status.Disabled,
				status.Count+status.Skipped+status.Disabled, total)
		}
		fmt.Printf("   path=%s\n", status.Path)
	}
	fmt.Printf("=== items total=%d ===\n", len(result.Items))
	bySource := map[string]int{}
	withKey := map[string]int{}
	byAppType := map[string]int{}
	for _, item := range result.Items {
		bySource[item.SourceID]++
		if item.HasKey {
			withKey[item.SourceID]++
		}
		byAppType[item.AppType]++
	}
	for _, id := range []string{contract.SourceCCSwitch, contract.SourceMagpie} {
		fmt.Printf("  %-10s items=%-3d hasKey=%-3d\n", id, bySource[id], withKey[id])
	}
	fmt.Println("=== app_type 分布 ===")
	types := make([]string, 0, len(byAppType))
	for name := range byAppType {
		types = append(types, name)
	}
	sort.Strings(types)
	for _, name := range types {
		fmt.Printf("  %-16s %d\n", name, byAppType[name])
	}
	fmt.Println("=== 前 8 条（不含密钥）===")
	for index, item := range result.Items {
		if index >= 8 {
			break
		}
		fmt.Printf("  [%-9s] key=%-46s name=%-14s base=%-44s fmt=%-16s model=%-24s models=%-2d hasKey=%v\n",
			item.SourceID, item.SourceKey, truncate(item.Name, 14), truncate(item.BaseURL, 44),
			item.APIFormat, truncate(item.Model, 24), len(item.Models), item.HasKey)
	}
	// 逐条核对 count 与实际条数一致。
	total := 0
	for _, status := range result.Sources {
		total += status.Count
	}
	if total != len(result.Items) {
		t.Errorf("sources[].count 合计=%d，实际 items=%d", total, len(result.Items))
	}
	encoded, err := EncodeScanResult(result)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	fmt.Printf("=== 返回体字节数=%d，含 apiKey 字段=%v ===\n", len(encoded), strings.Contains(string(encoded), "apiKey"))
	if strings.Contains(string(encoded), "apiKey") {
		t.Error("返回体里出现了 apiKey 字段")
	}
	var decoded contract.ScanResult
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, id := range []string{contract.SourceCCSwitch, contract.SourceMagpie} {
		if bySource[id] == 0 {
			t.Errorf("来源 %s 本机扫描结果为 0 条", id)
		}
	}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
