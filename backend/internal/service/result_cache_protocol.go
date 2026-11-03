package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type ResultCacheIntent struct {
	Declared bool
	Mode     string
	Scope    string
}

func ParseResultCacheIntent(headers http.Header, body []byte) (ResultCacheIntent, []byte, error) {
	intent := ResultCacheIntent{Mode: "read_write", Scope: "user"}
	protocol := strings.TrimSpace(headers.Get("X-Gateway-Cache-Protocol"))
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return intent, nil, fmt.Errorf("invalid JSON request")
	}
	control, present := fields["gateway_cache"]
	if protocol == "" && !present {
		return intent, body, nil
	}
	if protocol != "result-cache" {
		return intent, nil, fmt.Errorf("X-Gateway-Cache-Protocol must be result-cache")
	}
	if len(headers.Values("X-Gateway-Cache-Protocol")) != 1 {
		return intent, nil, fmt.Errorf("cache protocol must be specified once")
	}
	canonical, err := CanonicalResultCacheJSON(body)
	if err != nil {
		return intent, nil, err
	}
	if err = json.Unmarshal(canonical, &fields); err != nil {
		return intent, nil, err
	}
	intent.Declared = true
	if present {
		var values map[string]json.RawMessage
		if len(control) == 0 || control[0] != '{' || json.Unmarshal(control, &values) != nil {
			return intent, nil, fmt.Errorf("gateway_cache must be an object")
		}
		for k, v := range values {
			switch k {
			case "mode":
				err = json.Unmarshal(v, &intent.Mode)
			case "scope":
				err = json.Unmarshal(v, &intent.Scope)
			default:
				return intent, nil, fmt.Errorf("unknown gateway_cache field: %s", k)
			}
			if err != nil {
				return intent, nil, fmt.Errorf("gateway_cache.%s must be a string", k)
			}
		}
	}
	switch intent.Mode {
	case "read_write", "read_only", "write_only", "bypass":
	default:
		return intent, nil, fmt.Errorf("unsupported result cache mode")
	}
	switch intent.Scope {
	case "user", "group":
	default:
		return intent, nil, fmt.Errorf("unsupported result cache scope")
	}
	delete(fields, "gateway_cache")
	clean, err := json.Marshal(fields)
	return intent, clean, err
}

// Only non-streaming text Chat requests have a replay contract in v1.
func ResultCacheRequestEligible(body []byte) bool {
	var f map[string]json.RawMessage
	if json.Unmarshal(body, &f) != nil {
		return false
	}
	allowed := map[string]bool{}
	for _, k := range []string{"model", "messages", "stream", "stream_options", "temperature", "top_p", "n", "max_tokens", "max_completion_tokens", "frequency_penalty", "presence_penalty", "stop", "seed", "logit_bias", "logprobs", "top_logprobs", "response_format", "user", "metadata", "reasoning_effort", "reasoning", "service_tier", "prompt_cache_key", "prompt_cache_retention", "safety_identifier", "prediction", "verbosity", "cache_control", "tools", "functions", "function_call", "tool_choice", "store"} {
		allowed[k] = true
	}
	for k := range f {
		if !allowed[k] {
			return false
		}
	}
	for _, k := range []string{"tools", "functions", "function_call", "tool_choice", "previous_response_id", "conversation", "web_search_options", "modalities", "audio", "background", "context_management"} {
		if v, ok := f[k]; ok && string(v) != "null" && string(v) != "[]" {
			return false
		}
	}
	if v, ok := f["store"]; ok && string(v) != "false" && string(v) != "null" {
		return false
	}
	if v, ok := f["stream"]; ok && string(v) != "false" {
		return false
	}
	var messages []struct {
		Role         string          `json:"role"`
		Content      json.RawMessage `json:"content"`
		ToolCalls    json.RawMessage `json:"tool_calls"`
		FunctionCall json.RawMessage `json:"function_call"`
	}
	if json.Unmarshal(f["messages"], &messages) != nil || len(messages) == 0 {
		return false
	}
	for _, m := range messages {
		if m.Role == "tool" || m.Role == "function" || len(m.ToolCalls) > 0 || len(m.FunctionCall) > 0 {
			return false
		}
		var text string
		if json.Unmarshal(m.Content, &text) == nil {
			continue
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(m.Content, &parts) != nil {
			return false
		}
		for _, p := range parts {
			if p.Type != "text" {
				return false
			}
		}
	}
	return true
}
