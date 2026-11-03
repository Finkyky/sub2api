package sample

import (
	"context"
	"encoding/json"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	"math"
	"sort"
)

type pricingEntry struct {
	ChannelID int64               `json:"channel_id"`
	PricingID int64               `json:"pricing_id"`
	Fields    map[string]*float64 `json:"fields"`
}
type networkEntry struct {
	URL  string          `json:"url"`
	Body json.RawMessage `json:"body"`
}
type taskEnvelope struct {
	Input map[string]json.RawMessage `json:"input"`
	Host  *struct {
		KV      map[string]json.RawMessage `json:"kv"`
		Network []networkEntry             `json:"network"`
		Pricing []pricingEntry             `json:"pricing"`
	} `json:"host"`
}
type priceUpdate struct {
	ChannelID int64              `json:"channel_id"`
	PricingID int64              `json:"pricing_id"`
	Fields    map[string]float64 `json:"fields"`
}
type taskOutput struct {
	Summary string        `json:"summary,omitempty"`
	Prices  []priceUpdate `json:"price_updates"`
	KV      []any         `json:"kv_updates"`
}

func failed(code string) *general.TaskResponse {
	return &general.TaskResponse{Status: "failed", ErrorCode: code}
}
func (s *Server) runPricing(ctx context.Context, b []byte) (*general.TaskResponse, error) {
	var env taskEnvelope
	if strict(ctx, b, &env) != nil || env.Input == nil || env.Host == nil || env.Host.KV == nil || env.Host.Network == nil || env.Host.Pricing == nil {
		return nil, invalid(ctx)
	}
	if len(env.Host.Network) > 32 || len(env.Host.Pricing) > 256 || len(env.Host.KV) > 128 {
		return nil, limit()
	}
	current := map[[2]int64]pricingEntry{}
	for _, p := range env.Host.Pricing {
		k := [2]int64{p.ChannelID, p.PricingID}
		if p.ChannelID <= 0 || p.PricingID <= 0 || p.Fields == nil || len(p.Fields) > 32 {
			return nil, invalid(ctx)
		}
		if _, ok := current[k]; ok {
			return nil, invalid(ctx)
		}
		current[k] = p
	}
	var source *networkEntry
	for i := range env.Host.Network {
		n := &env.Host.Network[i]
		if n.URL == s.pricing.SourceURL {
			if source != nil {
				return failed("pricing_source_ambiguous"), nil
			}
			source = n
		}
	}
	// A matching authorized network snapshot wins. Only its absence enables
	// the explicit offline task.input.prices source; invalid network data never
	// silently falls back to different prices.
	var sourceBody []byte
	if source != nil {
		sourceBody = source.Body
	} else if offline, ok := env.Input["prices"]; ok {
		sourceBody = append([]byte(`{"prices":`), offline...)
		sourceBody = append(sourceBody, '}')
	} else {
		return failed("pricing_source_missing"), nil
	}
	var prices struct {
		Prices map[string]map[string]*float64 `json:"prices"`
	}
	if strict(ctx, sourceBody, &prices) != nil {
		if e := canceled(ctx); e != nil {
			return nil, e
		}
		return failed("pricing_data_invalid"), nil
	}
	if len(prices.Prices) == 0 {
		return failed("pricing_data_missing"), nil
	}
	if len(prices.Prices) > 256 {
		return nil, limit()
	}
	updates := map[[2]int64]priceUpdate{}
	for _, rule := range s.pricing.Rules {
		if e := canceled(ctx); e != nil {
			return nil, e
		}
		key := [2]int64{rule.ChannelID, rule.PricingID}
		p, ok := current[key]
		if !ok {
			return failed("pricing_target_not_authorized"), nil
		}
		old, ok := p.Fields[rule.Field]
		if !ok {
			return failed("pricing_field_not_authorized"), nil
		}
		if old == nil || math.IsNaN(*old) || math.IsInf(*old, 0) || *old < 0 {
			return failed("pricing_baseline_missing"), nil
		}
		model, ok := prices.Prices[rule.SourceKey]
		if !ok {
			return failed("pricing_data_missing"), nil
		}
		v, ok := model[rule.Field]
		if !ok || v == nil || *v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0) {
			return failed("pricing_data_invalid"), nil
		}
		next := *v * rule.Multiplier
		if math.IsNaN(next) || math.IsInf(next, 0) || next > rule.MaxPrice {
			return failed("pricing_limit_exceeded"), nil
		}
		if next == *old {
			continue
		}
		if *old == 0 || math.Abs(next-*old) / *old > *rule.MaxChangeRatio {
			return failed("pricing_change_exceeded"), nil
		}
		u, ok := updates[key]
		if !ok {
			u = priceUpdate{ChannelID: rule.ChannelID, PricingID: rule.PricingID, Fields: map[string]float64{}}
		}
		u.Fields[rule.Field] = next
		updates[key] = u
	}
	out := taskOutput{Summary: "Pricing rules evaluated.", Prices: []priceUpdate{}, KV: []any{}}
	keys := make([][2]int64, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] == keys[j][0] {
			return keys[i][1] < keys[j][1]
		}
		return keys[i][0] < keys[j][0]
	})
	for _, k := range keys {
		out.Prices = append(out.Prices, updates[k])
	}
	data, e := json.Marshal(out)
	if e != nil {
		return nil, invalid(ctx)
	}
	if len(data) > MaxJSONBytes {
		return nil, limit()
	}
	if e := canceled(ctx); e != nil {
		return nil, e
	}
	return &general.TaskResponse{Status: "succeeded", Changed: len(out.Prices) > 0, OutputJson: data}, nil
}
