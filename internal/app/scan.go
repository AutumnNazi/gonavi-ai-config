package app

import (
	"context"
	"encoding/json"

	goplugin "github.com/Syngnat/GoNavi/sdk/go"

	"github.com/AutumnNazi/gonavi-ai-config/internal/contract"
	"github.com/AutumnNazi/gonavi-ai-config/internal/sources"
)

// handleScan 实现 sources.scan。命令只做参数解码、扫描与编码，
// 密钥在这一步之后就不再被引用（条目里的密钥字段标了 json:"-"）。
func (a *App) handleScan(ctx context.Context, raw json.RawMessage) (any, error) {
	var request contract.ScanRequest
	if err := decodeArguments(raw, &request); err != nil {
		return nil, err
	}
	result, err := a.scannerFor(ctx).Scan(ctx, request)
	if err != nil {
		return nil, &goplugin.Error{Code: errBadArgument, Message: err.Error()}
	}
	return result, nil
}

// hostProviderLister 是 host.ai.providers.read（ai.providers.read）的适配器。
// 能力被拒时返回错误，扫描侧降级为不标冲突。
type hostProviderLister struct {
	host hostCaller
}

func (lister hostProviderLister) ListProviders(ctx context.Context) ([]sources.ProviderRef, error) {
	raw, err := lister.host.CallCapability(ctx, capabilityAIProvidersRead, map[string]any{})
	if err != nil {
		return nil, err
	}
	var envelope contract.HostEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if !envelope.OK {
		return nil, errHostRejected
	}
	var data struct {
		Providers []struct {
			Name    string `json:"name"`
			BaseURL string `json:"baseUrl"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, err
	}
	refs := make([]sources.ProviderRef, 0, len(data.Providers))
	for _, provider := range data.Providers {
		refs = append(refs, sources.ProviderRef{Name: provider.Name, BaseURL: provider.BaseURL})
	}
	return refs, nil
}
