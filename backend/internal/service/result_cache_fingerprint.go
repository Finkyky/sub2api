package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

// CanonicalResultCacheJSON preserves strings, array order and numeric precision.
// Duplicate keys are rejected instead of letting parser implementations disagree.
func CanonicalResultCacheJSON(body []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	v, err := resultCacheJSONValue(d)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("multiple or invalid JSON values")
	}
	return json.Marshal(v)
}
func resultCacheJSONValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		out := map[string]any{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return nil, e
			}
			k, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := out[k]; exists {
				return nil, fmt.Errorf("duplicate JSON key: %s", k)
			}
			v, e := resultCacheJSONValue(d)
			if e != nil {
				return nil, e
			}
			out[k] = v
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("unterminated object")
		}
		return out, nil
	case '[':
		out := []any{}
		for d.More() {
			v, e := resultCacheJSONValue(d)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("unterminated array")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}

type ResultCacheKeyMaterial struct {
	GroupID          int64             `json:"group_id"`
	ScopeIdentity    string            `json:"scope_identity"`
	Scope            string            `json:"scope"`
	Endpoint         string            `json:"endpoint"`
	EffectiveModel   string            `json:"model"`
	PolicyVersion    string            `json:"policy_version"`
	SemanticHeaders  map[string]string `json:"headers,omitempty"`
	EffectiveRequest json.RawMessage   `json:"request"`
}

func BuildResultCacheKey(m ResultCacheKeyMaterial) (string, error) {
	body, err := CanonicalResultCacheJSON(m.EffectiveRequest)
	if err != nil {
		return "", err
	}
	m.EffectiveRequest = body
	encoded, err := json.Marshal(struct {
		Version  string                 `json:"version"`
		Material ResultCacheKeyMaterial `json:"material"`
	}{"chat-result-v1", m})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "result-cache:v1:" + hex.EncodeToString(digest[:]), nil
}
