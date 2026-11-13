package app

import (
	"context"
	"encoding/json"

	goplugin "github.com/Syngnat/GoNavi/sdk/go"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
)

// importApplyResult 是 import.apply 的返回体：宿主结果 + resolved。
// Items[].Index 一律是**调用方 items 数组的下标**，宿主请求体里的下标不外传——
// 两套下标空间混用会让调用方对不上自己勾选的行。
type importApplyResult struct {
	Imported int                       `json:"imported"`
	Skipped  int                       `json:"skipped"`
	Failed   int                       `json:"failed"`
	Items    []contract.HostImportItem `json:"items"`
	Resolved int                       `json:"resolved"`
}

// handleImport 实现 import.apply：按 (sourceId, sourceKey) 现取完整配置（含密钥），
// 组装 ai.providers.import 请求体交给宿主，再原样转交宿主结果并附加 resolved。
// 取不到配置的条目不进请求体，占住自己的下标记 failed / source_missing。
func (a *App) handleImport(ctx context.Context, raw json.RawMessage) (any, error) {
	var request contract.ImportRequest
	if err := decodeArguments(raw, &request); err != nil {
		return nil, err
	}
	if len(request.Items) == 0 || len(request.Items) > contract.MaxImportItems {
		return nil, &goplugin.Error{Code: errBadArgument, Message: "invalid arguments"}
	}
	// 优先用请求 context 里绑定的宿主网关：那是 SDK 为本次调用建立的真实通路，
	// 能力请求因此回到发起该命令的宿主实例。测试与独立运行才回退到注入的桩。
	host := a.host
	if fromContext, ok := hostFromContext(ctx); ok {
		host = fromContext
	}
	if host == nil {
		return nil, &goplugin.Error{Code: errInternal, Message: "host capability is unavailable"}
	}

	// items 按调用方下标就位；emitted 记录「宿主请求体下标 → 调用方下标」的映射，
	// 回填时用它还原，缺失条目因此不会让后面的条目错位。
	items := make([]contract.HostImportItem, len(request.Items))
	emitted := make([]int, 0, len(request.Items))
	providers := make([]contract.HostProviderRequest, 0, len(request.Items))
	for callerIndex, ref := range request.Items {
		item, found, err := a.scanner.Fetch(ctx, ref.SourceID, ref.SourceKey)
		if err != nil {
			// 读取失败与「来源里没有这条」是两回事：前者要如实说「读不了」（库损坏、权限、
			// 来源未注册、sourceKey 超长），别和 source_missing 共用文案把库的问题说成配置被删。
			items[callerIndex] = contract.HostImportItem{
				Index:  callerIndex,
				Status: "failed",
				Reason: "source_read_failed",
			}
			continue
		}
		if !found {
			// source_missing 是插件侧语义：来源里已经没有这条配置了。
			items[callerIndex] = contract.HostImportItem{
				Index:  callerIndex,
				Status: "failed",
				Reason: "source_missing",
			}
			continue
		}
		emitted = append(emitted, callerIndex)
		provider := contract.HostProviderRequest{
			Name:      item.Name,
			BaseURL:   item.BaseURL,
			APIFormat: item.APIFormat,
			Model:     item.Model,
			Models:    item.Models,
			Overwrite: ref.Overwrite,
		}
		// 降级来源（magpie）没有明文密钥：不下发 apiKey，宿主持无密钥供应商。
		if item.HasKey {
			provider.APIKey = item.Key
		}
		// 自定义请求头同样只在 Fetch 时取到，与密钥一起下发；scan 返回体里没有这个字段。
		if len(item.Headers) > 0 {
			provider.Headers = item.Headers
		}
		providers = append(providers, provider)
	}

	resolved := len(emitted)
	if len(providers) > 0 {
		arguments := contract.HostImportArguments{Providers: providers}
		// 宿主能力的 MaxArgsBytes 是 256 KiB：超了会被它整批拒绝，用户看到的是「参数不合法」，
		// 归因不到「我勾的这几条正好都带超长密钥/请求头」。所以在调用前先量一次大小。
		if encoded, marshalErr := json.Marshal(arguments); marshalErr != nil {
			return nil, &goplugin.Error{Code: errInternal, Message: "host capability request is invalid"}
		} else if len(encoded) > contract.MaxHostImportBytes {
			return nil, &goplugin.Error{Code: errBadArgument, Message: "import request exceeds the host argument limit"}
		}
		response, err := host.CallCapability(ctx, capabilityAIProvidersImport, arguments)
		if err != nil {
			return nil, &goplugin.Error{Code: errInternal, Message: "host capability call failed"}
		}
		var envelope contract.HostEnvelope
		if err := json.Unmarshal(response, &envelope); err != nil {
			return nil, &goplugin.Error{Code: errInternal, Message: "host capability response is invalid"}
		}
		if !envelope.OK {
			// 宿主失败信封原样转交，页面按 code 分支处理。
			return envelope, nil
		}
		var data contract.HostImportData
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return nil, &goplugin.Error{Code: errInternal, Message: "host capability response is invalid"}
		}
		// 宿主承诺「一条请求一条结果且 index 等于请求体下标」。对不上就报错，
		// 不能把宿主下标当调用方下标透出去——那会让页面标错行。
		if len(data.Items) != len(providers) {
			return nil, &goplugin.Error{Code: errInternal, Message: "host capability response is incomplete"}
		}
		// 数量对上之后还要查下标：越界与重复都得挡。重复下标会让后一条覆盖前一条、
		// 被覆盖那条留下零值，页面于是把一条成功显示成失败、另一条张冠李戴。
		usedIndices := make([]bool, len(emitted))
		for _, hostItem := range data.Items {
			if hostItem.Index < 0 || hostItem.Index >= len(emitted) {
				return nil, &goplugin.Error{Code: errInternal, Message: "host capability response index is invalid"}
			}
			if usedIndices[hostItem.Index] {
				return nil, &goplugin.Error{Code: errInternal, Message: "host capability response index is duplicated"}
			}
			usedIndices[hostItem.Index] = true
			callerIndex := emitted[hostItem.Index]
			hostItem.Index = callerIndex
			items[callerIndex] = hostItem
		}
	}

	// 计数按最终 items 重算，保证 imported+skipped+failed 与 items 永远自洽。
	result := importApplyResult{Resolved: resolved, Items: items}
	for _, item := range items {
		switch item.Status {
		case "imported":
			result.Imported++
		case "skipped":
			result.Skipped++
		default:
			result.Failed++
		}
	}
	return result, nil
}
