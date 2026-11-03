package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"io"
	"mime"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	PluginProtocolOpenAIChat        = "openai.chat_completions.v1"
	PluginProtocolOpenAIResponses   = "openai.responses.v1"
	PluginProtocolAnthropicMessages = "anthropic.messages.v1"
	PluginProtocolGemini            = "gemini.generate_content.v1"
)

func PluginGatewayProtocolSupported(protocol string) bool {
	switch protocol {
	case PluginProtocolOpenAIChat, PluginProtocolOpenAIResponses, PluginProtocolAnthropicMessages, PluginProtocolGemini:
		return true
	}
	return false
}

// ValidatePluginGatewayInput limits replacement authority to request content.
// Non-content root fields are immutable; nested controls are compared by path,
// including absence versus explicit null and array positions.
func ValidatePluginGatewayInput(protocol string, before, after []byte) error {
	if !PluginGatewayProtocolSupported(protocol) {
		return fmt.Errorf("unsupported plugin gateway protocol")
	}
	a, err := pgReadObject(before)
	if err != nil {
		return err
	}
	b, err := pgReadObject(after)
	if err != nil {
		return err
	}
	if err = pgValidateRequest(protocol, a); err != nil {
		return err
	}
	if err = pgValidateRequest(protocol, b); err != nil {
		return err
	}
	if !reflect.DeepEqual(pgInputControls(protocol, a), pgInputControls(protocol, b)) {
		return fmt.Errorf("plugin replacement changes routing or cache controls")
	}
	return nil
}
func pgInputControls(protocol string, obj map[string]any) map[string]any {
	content := map[string]bool{}
	switch protocol {
	case PluginProtocolOpenAIChat:
		content["messages"] = true
	case PluginProtocolOpenAIResponses:
		content["input"] = true
		content["instructions"] = true
	case PluginProtocolAnthropicMessages:
		content["messages"] = true
		content["system"] = true
	case PluginProtocolGemini:
		content["contents"] = true
		content["systemInstruction"] = true
	}
	controls := map[string]any{}
	for k, v := range obj {
		if !content[k] {
			controls["root/"+k] = v
		}
	}
	var walk func(any, string)
	walk = func(v any, path string) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				p := path + "/" + strconv.Quote(k)
				switch k {
				case "model", "stream", "stream_options", "gateway_cache", "prompt_cache_key", "prompt_cache_retention", "cache_control", "cachedContent", "routing", "route", "group_id", "channel_id", "service_tier", "previous_response_id", "conversation", "safety_identifier", "user", "metadata":
					controls[p] = v
				}
				walk(v, p)
			}
		case []any:
			for i, v := range x {
				walk(v, path+"/"+strconv.Itoa(i))
			}
		}
	}
	for k := range content {
		if v, ok := obj[k]; ok {
			walk(v, "content/"+k)
		}
	}
	return controls
}
func pgValidateRequest(protocol string, obj map[string]any) error {
	p := &pgShape{}
	if protocol != PluginProtocolGemini {
		if strings.TrimSpace(p.string(obj["model"])) == "" {
			p.fail("request model required")
		}
	} else if _, ok := obj["model"]; ok {
		p.fail("Gemini body model is not supported")
	}
	if s, ok := obj["stream"]; ok {
		if _, ok := s.(bool); !ok {
			p.fail("stream must be boolean")
		}
	}
	messages := func(v any) {
		list := p.array(v)
		if len(list) == 0 {
			p.fail("messages required")
		}
		for _, v := range list {
			m := p.object(v)
			role := p.string(m["role"])
			switch role {
			case "user", "assistant", "system", "developer", "tool", "function":
			default:
				p.fail("invalid message role")
			}
			if c, ok := m["content"]; ok {
				pgValidateInputText(p, c)
			} else {
				p.fail("message content required")
			}
		}
	}
	switch protocol {
	case PluginProtocolOpenAIChat:
		messages(obj["messages"])
	case PluginProtocolAnthropicMessages:
		messages(obj["messages"])
		n, ok := obj["max_tokens"].(json.Number)
		if !ok {
			p.fail("max_tokens required")
		} else if i, e := n.Int64(); e != nil || i <= 0 {
			p.fail("invalid max_tokens")
		}
		if v, ok := obj["system"]; ok {
			pgValidateInputText(p, v)
		}
	case PluginProtocolOpenAIResponses:
		switch v := obj["input"].(type) {
		case string:
		case []any:
			if len(v) == 0 {
				p.fail("input required")
			}
			for _, item := range v {
				m := p.object(item)
				if _, ok := m["role"]; ok {
					role := p.string(m["role"])
					switch role {
					case "user", "assistant", "system", "developer":
					default:
						p.fail("invalid input role")
					}
					pgValidateInputText(p, m["content"])
				} else {
					p.fail("only message input items supported")
				}
			}
		default:
			p.fail("input must be a string or messages")
		}
		if v, ok := obj["instructions"]; ok {
			p.string(v)
		}
	case PluginProtocolGemini:
		list := p.array(obj["contents"])
		if len(list) == 0 {
			p.fail("Gemini contents required")
		}
		for _, v := range list {
			pgValidateGeminiInput(p, p.object(v))
		}
		if v, ok := obj["systemInstruction"]; ok {
			pgValidateGeminiInput(p, p.object(v))
		}
	}
	return p.err
}
func pgValidateInputText(p *pgShape, v any) {
	if _, ok := v.(string); ok {
		return
	}
	a := p.array(v)
	if len(a) == 0 {
		p.fail("text content required")
	}
	for _, v := range a {
		m := p.object(v)
		switch p.string(m["type"]) {
		case "text", "input_text", "output_text":
			p.string(m["text"])
		default:
			p.fail("unsupported request content block")
		}
	}
}
func pgValidateGeminiInput(p *pgShape, m map[string]any) {
	if r, ok := m["role"]; ok {
		role := p.string(r)
		if role != "user" && role != "model" && role != "system" {
			p.fail("invalid Gemini role")
		}
	}
	parts := p.array(m["parts"])
	if len(parts) == 0 {
		p.fail("Gemini parts required")
	}
	for _, v := range parts {
		part := p.object(v)
		p.string(part["text"])
		for k := range part {
			if k != "text" && k != "cache_control" {
				p.fail("unsupported Gemini request part")
			}
		}
	}
}

// PluginGatewayOutputContent requires a protocol terminal, not merely EOF.
func PluginGatewayOutputContent(protocol, mediaType string, body []byte) (contract.Content, error) {
	result, err := pgParseOutput(protocol, mediaType, body)
	if err != nil {
		return contract.Content{}, err
	}
	return contract.Content{MediaType: "text/plain; charset=utf-8", Data: []byte(result.text)}, nil
}

// ApplyPluginGatewayOutputReplacement reconstructs text-only output. No original
// delta, terminal text snapshot, logprob, annotation or tool argument is replayed.
func ApplyPluginGatewayOutputReplacement(protocol, mediaType string, original []byte, replacement contract.Content) ([]byte, error) {
	mt, err := pgMediaType(replacement.MediaType)
	if err != nil || mt != "text/plain" || !utf8.Valid(replacement.Data) {
		return nil, fmt.Errorf("replacement must be text/plain UTF-8")
	}
	result, err := pgParseOutput(protocol, mediaType, original)
	if err != nil {
		return nil, err
	}
	pgSetOutputText(protocol, result.doc, string(replacement.Data))
	mt, err = pgMediaType(mediaType)
	if err != nil {
		return nil, err
	}
	if mt == "application/json" {
		return json.Marshal(result.doc)
	}
	return pgEncodeSSE(protocol, result.doc)
}

type pgOutput struct {
	doc  map[string]any
	text string
}

func pgParseOutput(protocol, mediaType string, body []byte) (pgOutput, error) {
	if !PluginGatewayProtocolSupported(protocol) {
		return pgOutput{}, fmt.Errorf("unsupported plugin gateway protocol")
	}
	mt, err := pgMediaType(mediaType)
	if err != nil {
		return pgOutput{}, err
	}
	if mt == "application/json" {
		m, e := pgReadObject(body)
		if e != nil {
			return pgOutput{}, e
		}
		return pgParseJSONOutput(protocol, m)
	}
	if mt != "text/event-stream" {
		return pgOutput{}, fmt.Errorf("unsupported plugin gateway media type")
	}
	frames, err := pgReadSSE(body)
	if err != nil {
		return pgOutput{}, err
	}
	switch protocol {
	case PluginProtocolOpenAIChat:
		return pgChatStream(frames)
	case PluginProtocolOpenAIResponses:
		return pgResponsesStream(frames)
	case PluginProtocolAnthropicMessages:
		return pgAnthropicStream(frames)
	default:
		return pgGeminiStream(frames)
	}
}
func pgMediaType(value string) (string, error) {
	mt, params, err := mime.ParseMediaType(value)
	if err != nil {
		return "", fmt.Errorf("invalid plugin gateway media type")
	}
	if c, ok := params["charset"]; ok && !strings.EqualFold(c, "utf-8") {
		return "", fmt.Errorf("plugin gateway requires UTF-8")
	}
	return mt, nil
}

// The forwarding accumulator accepts EOF and malformed multi-data payloads.
// Inspection instead rejects duplicate keys, trailing JSON and partial frames;
// errors intentionally omit the inspected text.
func pgReadObject(body []byte) (map[string]any, error) {
	if !utf8.Valid(body) || !pgValidJSONEscapes(body) {
		return nil, fmt.Errorf("invalid UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	v, err := pgReadJSONValue(d, 0)
	if err != nil {
		return nil, fmt.Errorf("malformed plugin gateway JSON")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing plugin gateway JSON")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("plugin gateway JSON object required")
	}
	return m, nil
}
func pgReadJSONValue(d *json.Decoder, depth int) (any, error) {
	if depth > 128 {
		return nil, fmt.Errorf("JSON nesting limit")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				s, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("key required")
				}
				if _, exists := m[s]; exists {
					return nil, fmt.Errorf("duplicate key")
				}
				v, e := pgReadJSONValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("object truncated")
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, e := pgReadJSONValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("array truncated")
			}
			return a, nil
		default:
			return nil, fmt.Errorf("invalid delimiter")
		}
	}
	return t, nil
}

type pgShape struct{ err error }

func (p *pgShape) fail(s string) {
	if p.err == nil {
		p.err = fmt.Errorf("plugin gateway: %s", s)
	}
}
func (p *pgShape) object(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		p.fail("object required")
		return map[string]any{}
	}
	return m
}
func (p *pgShape) array(v any) []any {
	a, ok := v.([]any)
	if !ok {
		p.fail("array required")
	}
	return a
}
func (p *pgShape) string(v any) string {
	s, ok := v.(string)
	if !ok {
		p.fail("string required")
	}
	return s
}
func (p *pgShape) index(m map[string]any, key string) int {
	n, ok := m[key].(json.Number)
	if !ok {
		p.fail("event index required")
		return -1
	}
	i, e := n.Int64()
	if e != nil || i < 0 || i > 100000 {
		p.fail("invalid event index")
		return -1
	}
	return int(i)
}
func pgPick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}
func pgMerge(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}
func pgRejectError(p *pgShape, m map[string]any) {
	if e, ok := m["error"]; ok && e != nil {
		p.fail("upstream error response")
	}
}
func pgUsage(p *pgShape, m map[string]any, key string) {
	v, ok := m[key]
	if !ok || v == nil {
		return
	}
	u := p.object(v)
	var walk func(map[string]any)
	walk = func(x map[string]any) {
		for k, v := range x {
			switch n := v.(type) {
			case json.Number:
				f, e := n.Float64()
				if e != nil || f < 0 {
					p.fail("invalid usage count")
				}
			case map[string]any:
				walk(n)
			case nil:
			case string:
				if k != "modality" && k != "trafficType" {
					p.fail("invalid usage field")
				}
			case []any:
				for _, entry := range n {
					walk(p.object(entry))
				}
			default:
				p.fail("invalid usage field")
			}
		}
	}
	walk(u)
}
func pgFinish(p *pgShape, v any, protocol string) string {
	s := p.string(v)
	valid := false
	switch protocol {
	case PluginProtocolOpenAIChat:
		valid = s == "stop"
	case PluginProtocolAnthropicMessages:
		valid = s == "end_turn" || s == "stop_sequence"
	case PluginProtocolGemini:
		valid = s == "STOP"
	}
	if !valid {
		p.fail("unfinished or non-text terminal")
	}
	return s
}
func pgTextParts(p *pgShape, v any, kind string) string {
	var text strings.Builder
	for _, v := range p.array(v) {
		m := p.object(v)
		if p.string(m["type"]) != kind {
			p.fail("non-text content block")
		}
		text.WriteString(p.string(m["text"]))
		for k := range m {
			switch k {
			case "type", "text", "annotations", "logprobs", "cache_control":
			default:
				p.fail("unsupported text content field")
			}
		}
	}
	return text.String()
}
func pgMessageText(p *pgShape, m map[string]any) string {
	if r, ok := m["role"]; ok && p.string(r) != "assistant" {
		p.fail("non-assistant output")
	}
	for k, v := range m {
		switch k {
		case "role", "content", "annotations":
		case "tool_calls", "function_call", "refusal", "audio", "reasoning_content", "reasoning":
			if v != nil {
				p.fail("non-text output")
			}
		default:
			p.fail("unsupported message field")
		}
	}
	if m["content"] == nil {
		return ""
	}
	if s, ok := m["content"].(string); ok {
		return s
	}
	return pgTextParts(p, m["content"], "text")
}
func pgGeminiText(p *pgShape, c map[string]any) string {
	if r, ok := c["role"]; ok && p.string(r) != "model" {
		p.fail("invalid Gemini output role")
	}
	var text strings.Builder
	for _, v := range p.array(c["parts"]) {
		part := p.object(v)
		for k := range part {
			if k != "text" {
				p.fail("non-text Gemini part")
			}
		}
		text.WriteString(p.string(part["text"]))
	}
	return text.String()
}
func pgParseJSONOutput(protocol string, m map[string]any) (pgOutput, error) {
	p := &pgShape{}
	pgRejectError(p, m)
	for _, key := range []string{"id", "model", "object", "system_fingerprint", "service_tier", "responseId", "modelVersion"} {
		if v, ok := m[key]; ok {
			p.string(v)
		}
	}
	for _, key := range []string{"created", "created_at"} {
		if v, ok := m[key]; ok {
			n, ok := v.(json.Number)
			if !ok {
				p.fail("invalid creation time")
			} else if i, e := n.Int64(); e != nil || i < 0 {
				p.fail("invalid creation time")
			}
		}
	}
	doc := pgPick(m, "id", "model", "object", "created", "created_at", "system_fingerprint", "service_tier", "usage")
	text := ""
	switch protocol {
	case PluginProtocolOpenAIChat:
		choices := p.array(m["choices"])
		if len(choices) != 1 {
			p.fail("exactly one chat choice required")
		}
		if len(choices) == 1 {
			c := p.object(choices[0])
			if _, ok := c["index"]; ok && p.index(c, "index") != 0 {
				p.fail("nonzero chat choice unsupported")
			}
			message := p.object(c["message"])
			if _, ok := message["content"]; !ok {
				p.fail("chat message content required")
			}
			text = pgMessageText(p, message)
			f := pgFinish(p, c["finish_reason"], protocol)
			choice := map[string]any{"index": json.Number("0")}
			choice["finish_reason"] = f
			choice["message"] = map[string]any{"role": "assistant", "content": text}
			doc["choices"] = []any{choice}
		}
		pgUsage(p, m, "usage")
	case PluginProtocolOpenAIResponses:
		if p.string(m["status"]) != "completed" {
			p.fail("incomplete Responses output")
		}
		if v := m["incomplete_details"]; v != nil {
			p.fail("incomplete Responses details")
		}
		output := p.array(m["output"])
		if len(output) > 1 {
			p.fail("multiple Responses output items unsupported")
		}
		item := map[string]any{"type": "message", "role": "assistant", "status": "completed"}
		if len(output) == 1 {
			o := p.object(output[0])
			if p.string(o["type"]) != "message" {
				p.fail("non-text Responses item")
			}
			if r, ok := o["role"]; ok && p.string(r) != "assistant" {
				p.fail("invalid Responses role")
			}
			if s, ok := o["status"]; ok && p.string(s) != "completed" {
				p.fail("unfinished Responses item")
			}
			text = pgTextParts(p, o["content"], "output_text")
			if v, ok := o["id"]; ok {
				p.string(v)
			}
			pgMerge(item, pgPick(o, "id"))
		}
		item["content"] = []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}
		doc["output"] = []any{item}
		doc["status"] = "completed"
		pgMerge(doc, pgPick(m, "error", "incomplete_details"))
		pgUsage(p, m, "usage")
	case PluginProtocolAnthropicMessages:
		if typ, ok := m["type"]; ok && p.string(typ) != "message" {
			p.fail("invalid Anthropic message")
		}
		if r, ok := m["role"]; ok && p.string(r) != "assistant" {
			p.fail("invalid Anthropic role")
		}
		text = pgTextParts(p, m["content"], "text")
		doc["stop_reason"] = pgFinish(p, m["stop_reason"], protocol)
		if v, ok := m["stop_sequence"]; ok && v != nil {
			p.string(v)
		}
		pgMerge(doc, pgPick(m, "stop_sequence"))
		doc["type"] = "message"
		doc["role"] = "assistant"
		doc["content"] = []any{map[string]any{"type": "text", "text": text}}
		pgUsage(p, m, "usage")
	case PluginProtocolGemini:
		doc = pgPick(m, "responseId", "modelVersion", "usageMetadata")
		candidates := p.array(m["candidates"])
		if len(candidates) != 1 {
			p.fail("exactly one Gemini candidate required")
		}
		if v := m["promptFeedback"]; v != nil {
			feedback := p.object(v)
			if _, ok := feedback["blockReason"]; ok {
				p.fail("Gemini prompt blocked")
			}
		}
		if len(candidates) == 1 {
			c := p.object(candidates[0])
			if _, ok := c["index"]; ok && p.index(c, "index") != 0 {
				p.fail("nonzero Gemini candidate unsupported")
			}
			text = pgGeminiText(p, p.object(c["content"]))
			candidate := pgPick(c, "index", "safetyRatings")
			candidate["finishReason"] = pgFinish(p, c["finishReason"], protocol)
			candidate["content"] = map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}
			doc["candidates"] = []any{candidate}
		}
		pgUsage(p, m, "usageMetadata")
	}
	if p.err != nil {
		return pgOutput{}, p.err
	}
	return pgOutput{doc: doc, text: text}, nil
}
func pgSetOutputText(protocol string, m map[string]any, text string) {
	switch protocol {
	case PluginProtocolOpenAIChat:
		m["choices"].([]any)[0].(map[string]any)["message"] = map[string]any{"role": "assistant", "content": text}
	case PluginProtocolOpenAIResponses:
		m["output"].([]any)[0].(map[string]any)["content"] = []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}
	case PluginProtocolAnthropicMessages:
		m["content"] = []any{map[string]any{"type": "text", "text": text}}
	case PluginProtocolGemini:
		m["candidates"].([]any)[0].(map[string]any)["content"] = map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}
	}
}

type pgFrame struct {
	event  string
	object map[string]any
	done   bool
}

func pgReadSSE(body []byte) ([]pgFrame, error) {
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("invalid UTF-8 SSE")
	}
	body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	if bytes.ContainsRune(body, '\r') {
		return nil, fmt.Errorf("invalid SSE line ending")
	}
	frames := []pgFrame{}
	data := []string{}
	event := ""
	pending := false
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		// The final split element is EOF, not a received blank separator.
		if i == len(lines)-1 && line == "" {
			break
		}
		if line == "" {
			if len(data) > 0 {
				payload := strings.Join(data, "\n")
				f := pgFrame{event: event}
				if payload == "[DONE]" {
					f.done = true
				} else {
					obj, err := pgReadObject([]byte(payload))
					if err != nil {
						return nil, err
					}
					f.object = obj
				}
				frames = append(frames, f)
			} else if event != "" {
				return nil, fmt.Errorf("SSE event missing data")
			}
			data = nil
			event = ""
			pending = false
			continue
		}
		if strings.HasPrefix(line, ":") {
			// Comments are transport controls, not plugin business text. Because pass
			// commits the original buffer, accept only canonical heartbeat comments;
			// arbitrary comment payloads must not become an uninspected output channel.
			switch strings.TrimSpace(strings.TrimPrefix(line, ":")) {
			case "", "ping", "keepalive":
			default:
				return nil, fmt.Errorf("unsupported SSE control comment")
			}
			continue
		}
		pending = true
		if value, ok := extractOpenAISSEDataLine(line); ok {
			data = append(data, value)
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid SSE field")
		}
		v = strings.TrimPrefix(v, " ")
		switch k {
		case "event":
			if event != "" {
				return nil, fmt.Errorf("duplicate SSE event")
			}
			event = v
		case "id":
		case "retry":
			if _, err := strconv.ParseUint(v, 10, 32); err != nil {
				return nil, fmt.Errorf("invalid SSE retry")
			}
		default:
			return nil, fmt.Errorf("unknown SSE field")
		}
	}
	if pending || len(data) > 0 {
		return nil, fmt.Errorf("truncated SSE frame")
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("empty SSE response")
	}
	return frames, nil
}
func pgFrameType(p *pgShape, f pgFrame) string {
	pgRejectError(p, f.object)
	typ := ""
	if v, ok := f.object["type"]; ok {
		typ = p.string(v)
	}
	if f.event == "error" || typ == "error" {
		p.fail("upstream SSE error")
	}
	if f.event != "" && typ != "" && f.event != typ {
		p.fail("SSE event type mismatch")
	}
	if typ == "" {
		typ = f.event
	}
	return typ
}

// pgKeepalive recognizes a control schema, not just an event name. Named
// controls allow {} or a matching singleton type; unnamed controls require the
// singleton type. Terminal markers and any additional payload are not controls.
func pgKeepalive(f pgFrame) bool {
	if f.done || f.object == nil {
		return false
	}
	if f.event != "" && f.event != "ping" && f.event != "keepalive" {
		return false
	}
	if len(f.object) == 0 {
		return f.event == "ping" || f.event == "keepalive"
	}
	if len(f.object) != 1 {
		return false
	}
	typ, ok := f.object["type"].(string)
	if !ok || (typ != "ping" && typ != "keepalive") {
		return false
	}
	return f.event == "" || f.event == typ
}
func pgStableMetadata(p *pgShape, doc, m map[string]any, keys ...string) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if old, exists := doc[k]; exists && !reflect.DeepEqual(old, v) {
				p.fail("SSE identity changed")
			}
			doc[k] = v
		}
	}
}
func pgChatStream(frames []pgFrame) (pgOutput, error) {
	p := &pgShape{}
	doc := map[string]any{}
	var text strings.Builder
	finished, done, seen := false, false, false
	for _, f := range frames {
		typ := pgFrameType(p, f)
		if pgKeepalive(f) {
			continue
		}
		if done {
			p.fail("data after chat terminal")
			continue
		}
		if f.done {
			if !finished || !seen {
				p.fail("premature chat terminal")
			}
			done = true
			continue
		}
		if typ != "" {
			p.fail("unknown chat SSE event")
		}
		m := f.object
		pgStableMetadata(p, doc, m, "id", "model", "created", "system_fingerprint", "service_tier")
		if v, ok := m["usage"]; ok {
			pgUsage(p, m, "usage")
			doc["usage"] = v
		}
		choices := p.array(m["choices"])
		if len(choices) == 0 {
			if _, ok := m["usage"]; !ok {
				p.fail("empty chat event without usage")
			}
			continue
		}
		if len(choices) != 1 {
			p.fail("multiple chat stream choices unsupported")
			continue
		}
		if finished {
			p.fail("chat delta after finish")
			continue
		}
		c := p.object(choices[0])
		if p.index(c, "index") != 0 {
			p.fail("nonzero chat choice unsupported")
		}
		delta := p.object(c["delta"])
		text.WriteString(pgMessageText(p, delta))
		seen = true
		if v := c["finish_reason"]; v != nil {
			doc["finish_reason"] = pgFinish(p, v, PluginProtocolOpenAIChat)
			finished = true
		}
	}
	if !done || !finished {
		p.fail("truncated chat SSE")
	}
	doc["object"] = "chat.completion"
	doc["choices"] = []any{map[string]any{"index": json.Number("0"), "message": map[string]any{"role": "assistant", "content": text.String()}, "finish_reason": doc["finish_reason"]}}
	delete(doc, "finish_reason")
	if p.err != nil {
		return pgOutput{}, p.err
	}
	return pgParseJSONOutput(PluginProtocolOpenAIChat, doc)
}
func pgResponsesStream(frames []pgFrame) (pgOutput, error) {
	p := &pgShape{}
	doc := map[string]any{}
	var text strings.Builder
	terminal, textDone, haveDelta := false, false, false
	itemID := ""
	for _, f := range frames {
		typ := pgFrameType(p, f)
		if pgKeepalive(f) {
			continue
		}
		if f.done {
			p.fail("unexpected Responses DONE")
			continue
		}
		if terminal {
			p.fail("data after Responses terminal")
			continue
		}
		m := f.object
		for _, key := range []string{"output_index", "content_index"} {
			if _, ok := m[key]; ok && p.index(m, key) != 0 {
				p.fail("multiple Responses text segments in SSE unsupported")
			}
		}
		switch typ {
		case "response.created", "response.in_progress":
			r := p.object(m["response"])
			pgRejectError(p, r)
			pgStableMetadata(p, doc, r, "id", "model", "created_at")
			if v, ok := r["output"]; ok && len(p.array(v)) != 0 {
				p.fail("unexpected initial Responses output")
			}
		case "response.output_item.added":
			item := p.object(m["item"])
			if p.string(item["type"]) != "message" {
				p.fail("non-text Responses item")
			}
			if v, ok := item["id"]; ok {
				itemID = p.string(v)
			}
			if v, ok := item["content"]; ok && len(p.array(v)) > 0 {
				if pgTextParts(p, v, "output_text") != "" {
					p.fail("nonempty initial Responses item")
				}
			}
		case "response.content_part.added":
			part := p.object(m["part"])
			if p.string(part["type"]) != "output_text" || p.string(part["text"]) != "" {
				p.fail("invalid Responses text start")
			}
		case "response.output_text.delta":
			if textDone {
				p.fail("Responses delta after text done")
			}
			text.WriteString(p.string(m["delta"]))
			haveDelta = true
		case "response.output_text.done":
			if textDone {
				p.fail("duplicate Responses text done")
			}
			if p.string(m["text"]) != text.String() {
				p.fail("Responses text snapshot mismatch")
			}
			textDone = true
		case "response.content_part.done":
			part := p.object(m["part"])
			if p.string(part["type"]) != "output_text" || p.string(part["text"]) != text.String() {
				p.fail("Responses part snapshot mismatch")
			}
		case "response.output_item.done":
			item := p.object(m["item"])
			if p.string(item["type"]) != "message" || pgTextParts(p, item["content"], "output_text") != text.String() {
				p.fail("Responses item snapshot mismatch")
			}
		case "response.completed":
			r := p.object(m["response"])
			pgStableMetadata(p, doc, r, "id", "model", "created_at")
			pgMerge(doc, r)
			if _, ok := r["status"]; !ok {
				doc["status"] = "completed"
			}
			if _, ok := r["output"]; !ok {
				item := map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text.String()}}}
				if itemID != "" {
					item["id"] = itemID
				}
				doc["output"] = []any{item}
			}
			terminal = true
		default:
			p.fail("unsupported or failed Responses SSE event")
		}
	}
	if !terminal {
		p.fail("truncated Responses SSE")
	}
	if p.err != nil {
		return pgOutput{}, p.err
	}
	result, err := pgParseJSONOutput(PluginProtocolOpenAIResponses, doc)
	if err != nil {
		return pgOutput{}, err
	}
	if haveDelta && result.text != text.String() {
		return pgOutput{}, fmt.Errorf("Responses terminal snapshot mismatch")
	}
	return result, nil
}
func pgAnthropicStream(frames []pgFrame) (pgOutput, error) {
	p := &pgShape{}
	doc := map[string]any{}
	var text strings.Builder
	started, open, finished, terminal := false, false, false, false
	nextIndex := 0
	for _, f := range frames {
		typ := pgFrameType(p, f)
		if pgKeepalive(f) {
			continue
		}
		if f.done {
			p.fail("unexpected Anthropic DONE")
			continue
		}
		if terminal {
			p.fail("data after Anthropic terminal")
			continue
		}
		m := f.object
		switch typ {
		case "message_start":
			if started {
				p.fail("duplicate Anthropic message start")
			}
			started = true
			msg := p.object(m["message"])
			pgRejectError(p, msg)
			pgMerge(doc, msg)
			pgUsage(p, msg, "usage")
			if len(p.array(msg["content"])) != 0 {
				p.fail("nonempty Anthropic start")
			}
		case "content_block_start":
			if !started || open || finished || p.index(m, "index") != nextIndex {
				p.fail("invalid Anthropic block start")
			}
			open = true
			block := p.object(m["content_block"])
			if p.string(block["type"]) != "text" {
				p.fail("non-text Anthropic block")
			}
			text.WriteString(p.string(block["text"]))
		case "content_block_delta":
			if !open || p.index(m, "index") != nextIndex {
				p.fail("Anthropic delta outside block")
			}
			delta := p.object(m["delta"])
			if p.string(delta["type"]) != "text_delta" {
				p.fail("non-text Anthropic delta")
			}
			text.WriteString(p.string(delta["text"]))
		case "content_block_stop":
			if !open || p.index(m, "index") != nextIndex {
				p.fail("invalid Anthropic block stop")
			}
			open = false
			nextIndex++
		case "message_delta":
			if !started || open || finished {
				p.fail("invalid Anthropic message delta")
			}
			delta := p.object(m["delta"])
			doc["stop_reason"] = pgFinish(p, delta["stop_reason"], PluginProtocolAnthropicMessages)
			pgMerge(doc, pgPick(delta, "stop_sequence"))
			finished = true
			if v, ok := m["usage"]; ok {
				pgUsage(p, m, "usage")
				if v == nil {
					doc["usage"] = nil
				} else {
					u := map[string]any{}
					if old, ok := doc["usage"].(map[string]any); ok {
						pgMerge(u, old)
					}
					pgMerge(u, p.object(v))
					doc["usage"] = u
				}
			}
		case "message_stop":
			if !started || open || !finished {
				p.fail("premature Anthropic terminal")
			}
			terminal = true
		default:
			p.fail("unknown Anthropic SSE event")
		}
	}
	if !terminal {
		p.fail("truncated Anthropic SSE")
	}
	doc["content"] = []any{map[string]any{"type": "text", "text": text.String()}}
	if p.err != nil {
		return pgOutput{}, p.err
	}
	return pgParseJSONOutput(PluginProtocolAnthropicMessages, doc)
}
func pgGeminiStream(frames []pgFrame) (pgOutput, error) {
	p := &pgShape{}
	doc := map[string]any{}
	var text strings.Builder
	finished, seen := false, false
	candidate := map[string]any{}
	for _, f := range frames {
		typ := pgFrameType(p, f)
		if pgKeepalive(f) {
			continue
		}
		if f.done || typ != "" {
			p.fail("unknown Gemini SSE event")
			continue
		}
		m := f.object
		pgStableMetadata(p, doc, m, "responseId", "modelVersion")
		if v, ok := m["usageMetadata"]; ok {
			pgUsage(p, m, "usageMetadata")
			doc["usageMetadata"] = v
		}
		if v := m["promptFeedback"]; v != nil {
			fb := p.object(v)
			if _, ok := fb["blockReason"]; ok {
				p.fail("Gemini prompt blocked")
			}
		}
		values, ok := m["candidates"]
		if !ok {
			if _, ok := m["usageMetadata"]; !ok {
				p.fail("Gemini event missing candidates")
			}
			continue
		}
		a := p.array(values)
		if len(a) == 0 {
			if _, ok := m["usageMetadata"]; !ok {
				p.fail("empty Gemini event")
			}
			continue
		}
		if len(a) != 1 || finished {
			p.fail("multiple or post-terminal Gemini candidate")
			continue
		}
		c := p.object(a[0])
		if _, ok := c["index"]; ok && p.index(c, "index") != 0 {
			p.fail("nonzero Gemini candidate unsupported")
		}
		seen = true
		pgMerge(candidate, pgPick(c, "index", "safetyRatings"))
		if v, ok := c["content"]; ok {
			text.WriteString(pgGeminiText(p, p.object(v)))
		}
		if v, ok := c["finishReason"]; ok {
			candidate["finishReason"] = pgFinish(p, v, PluginProtocolGemini)
			finished = true
		}
	}
	if !seen || !finished {
		p.fail("truncated Gemini SSE")
	}
	candidate["content"] = map[string]any{"role": "model", "parts": []any{map[string]any{"text": text.String()}}}
	doc["candidates"] = []any{candidate}
	if p.err != nil {
		return pgOutput{}, p.err
	}
	return pgParseJSONOutput(PluginProtocolGemini, doc)
}

// Canonical stream shapes mirror internal/pkg/apicompat. Only validated terminal
// metadata and replacement text are emitted; original frames are never replayed.
func pgEncodeSSE(protocol string, doc map[string]any) ([]byte, error) {
	var out bytes.Buffer
	var encodeErr error
	sequence := 0
	emit := func(event string, m map[string]any) {
		if protocol == PluginProtocolOpenAIResponses {
			m["sequence_number"] = sequence
			sequence++
		}
		b, e := json.Marshal(m)
		if e != nil {
			encodeErr = e
			return
		}
		if event != "" {
			out.WriteString("event: " + event + "\n")
		}
		out.WriteString("data: ")
		out.Write(b)
		out.WriteString("\n\n")
	}
	switch protocol {
	case PluginProtocolOpenAIChat:
		choice := doc["choices"].([]any)[0].(map[string]any)
		msg := choice["message"].(map[string]any)
		base := func() map[string]any {
			m := pgPick(doc, "id", "model", "created", "system_fingerprint", "service_tier")
			m["object"] = "chat.completion.chunk"
			return m
		}
		m := base()
		m["choices"] = []any{map[string]any{"index": choice["index"], "delta": map[string]any{"role": "assistant", "content": msg["content"]}, "finish_reason": nil}}
		emit("", m)
		m = base()
		m["choices"] = []any{map[string]any{"index": choice["index"], "delta": map[string]any{}, "finish_reason": choice["finish_reason"]}}
		emit("", m)
		if u, ok := doc["usage"]; ok {
			m = base()
			m["choices"] = []any{}
			m["usage"] = u
			emit("", m)
		}
		out.WriteString("data: [DONE]\n\n")
	case PluginProtocolOpenAIResponses:
		item := doc["output"].([]any)[0].(map[string]any)
		part := item["content"].([]any)[0].(map[string]any)
		start := pgPick(doc, "id", "model", "object", "created_at")
		start["status"] = "in_progress"
		start["output"] = []any{}
		emit("response.created", map[string]any{"type": "response.created", "response": start})
		added := pgPick(item, "id", "type", "role")
		added["status"] = "in_progress"
		added["content"] = []any{}
		emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": added})
		fields := func(typ string) map[string]any {
			m := map[string]any{"type": typ, "output_index": 0, "content_index": 0}
			if id, ok := item["id"]; ok {
				m["item_id"] = id
			}
			return m
		}
		m := fields("response.content_part.added")
		m["part"] = map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		emit("response.content_part.added", m)
		m = fields("response.output_text.delta")
		m["delta"] = part["text"]
		emit("response.output_text.delta", m)
		m = fields("response.output_text.done")
		m["text"] = part["text"]
		emit("response.output_text.done", m)
		m = fields("response.content_part.done")
		m["part"] = part
		emit("response.content_part.done", m)
		emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		emit("response.completed", map[string]any{"type": "response.completed", "response": doc})
	case PluginProtocolAnthropicMessages:
		part := doc["content"].([]any)[0].(map[string]any)
		start := pgPick(doc, "id", "model", "type", "role", "usage")
		start["content"] = []any{}
		start["stop_reason"] = nil
		start["stop_sequence"] = nil
		emit("message_start", map[string]any{"type": "message_start", "message": start})
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": part["text"]}})
		emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		m := map[string]any{"type": "message_delta", "delta": pgPick(doc, "stop_reason", "stop_sequence")}
		if u, ok := doc["usage"]; ok {
			m["usage"] = u
		}
		emit("message_delta", m)
		emit("message_stop", map[string]any{"type": "message_stop"})
	case PluginProtocolGemini:
		emit("", doc)
	}
	return out.Bytes(), encodeErr
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. Inspection must
// reject them instead of silently changing the text visible to the plugin.
func pgValidJSONEscapes(body []byte) bool {
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		i++
		if i >= len(body) {
			return false
		}
		if body[i] != 'u' {
			continue
		}
		if i+4 >= len(body) {
			return false
		}
		n, e := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
