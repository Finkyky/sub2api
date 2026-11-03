package sample

import (
	"context"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	"mime"
	"strings"
	"unicode/utf8"
)

// Input is M4 protocol JSON. Output is M4's terminal-validated JSON/SSE text,
// NOT raw protocol JSON or SSE frames. Routing/metadata strings are not scanned.
func hookText(ctx context.Context, r *general.HookRequest) (string, error) {
	mt, params, e := mime.ParseMediaType(r.Content.MediaType)
	if e != nil {
		return "", errJSON
	}
	if c, ok := params["charset"]; ok && !strings.EqualFold(c, "utf-8") {
		return "", errJSON
	}
	if r.Stage == "output" {
		if mt != "text/plain" || !utf8.Valid(r.Content.Data) {
			return "", errJSON
		}
		return string(r.Content.Data), nil
	}
	if mt != "application/json" {
		return "", errJSON
	}
	m, e := object(ctx, r.Content.Data)
	if e != nil {
		return "", e
	}
	var parts []string
	add := func(v any) error { return inputText(v, &parts) }
	switch r.Protocol {
	case "openai.chat_completions.v1", "anthropic.messages.v1":
		list, ok := m["messages"].([]any)
		if !ok || len(list) == 0 {
			return "", errJSON
		}
		for _, v := range list {
			o, ok := v.(map[string]any)
			if !ok {
				return "", errJSON
			}
			if e := add(o["content"]); e != nil {
				return "", e
			}
		}
		if r.Protocol == "anthropic.messages.v1" {
			if v, ok := m["system"]; ok {
				if e := add(v); e != nil {
					return "", e
				}
			}
		}
	case "openai.responses.v1":
		switch v := m["input"].(type) {
		case string:
			parts = append(parts, v)
		case []any:
			if len(v) == 0 {
				return "", errJSON
			}
			for _, v := range v {
				o, ok := v.(map[string]any)
				if !ok {
					return "", errJSON
				}
				if e := add(o["content"]); e != nil {
					return "", e
				}
			}
		default:
			return "", errJSON
		}
		if v, ok := m["instructions"]; ok {
			v, ok := v.(string)
			if !ok {
				return "", errJSON
			}
			parts = append(parts, v)
		}
	case "gemini.generate_content.v1":
		list, ok := m["contents"].([]any)
		if !ok || len(list) == 0 {
			return "", errJSON
		}
		if v, ok := m["systemInstruction"]; ok {
			list = append(list, v)
		}
		for _, v := range list {
			o, ok := v.(map[string]any)
			if !ok {
				return "", errJSON
			}
			p, ok := o["parts"].([]any)
			if !ok || len(p) == 0 {
				return "", errJSON
			}
			for _, v := range p {
				o, ok := v.(map[string]any)
				if !ok {
					return "", errJSON
				}
				text, ok := o["text"].(string)
				if !ok {
					return "", errJSON
				}
				parts = append(parts, text)
			}
		}
	default:
		return "", errJSON
	}
	return strings.Join(parts, "\n"), ctx.Err()
}
func inputText(v any, parts *[]string) error {
	switch v := v.(type) {
	case string:
		*parts = append(*parts, v)
	case []any:
		for _, v := range v {
			o, ok := v.(map[string]any)
			if !ok {
				return errJSON
			}
			typ, ok := o["type"].(string)
			if !ok || (typ != "text" && typ != "input_text" && typ != "output_text") {
				return errJSON
			}
			text, ok := o["text"].(string)
			if !ok {
				return errJSON
			}
			*parts = append(*parts, text)
		}
	default:
		return errJSON
	}
	return nil
}
func (s *Server) businessCheck(ctx context.Context, text string) *general.HookResponse {
	text = strings.ToLower(text)
	score := 0.0
	r := &general.HookResponse{Decision: "pass"}
	for _, rule := range s.business.Rules {
		if ctx.Err() != nil {
			return r
		}
		matched := false
		for _, word := range rule.Keywords {
			if strings.Contains(text, word) {
				score += rule.Weight
				matched = true
			}
		}
		if matched {
			r.Findings = append(r.Findings, &general.Finding{RuleId: rule.ID, RuleVersion: rule.Version, Category: "business"})
		}
	}
	if score >= s.business.Threshold {
		r.Decision = "block"
		r.Rejection = &general.Rejection{Code: "business_rule_blocked", Message: "Output was blocked by a business rule."}
	}
	return r
}
