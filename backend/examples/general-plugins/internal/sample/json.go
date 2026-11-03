package sample

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

var errJSON = errors.New("invalid JSON")

// Check duplicates, depth, UTF-8, paired escapes and trailing tokens before decoding.
// No parser error or inspected value is returned across the RPC boundary.
func object(ctx context.Context, b []byte) (map[string]any, error) {
	if !utf8.Valid(b) || !validEscapes(b) {
		return nil, errJSON
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, e := value(ctx, d, 0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errJSON
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errJSON
	}
	return m, nil
}
func value(ctx context.Context, d *json.Decoder, depth int) (any, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if depth > 64 {
		return nil, errJSON
	}
	t, e := d.Token()
	if e != nil {
		return nil, errJSON
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, errJSON
				}
				s, ok := k.(string)
				if !ok {
					return nil, errJSON
				}
				if _, ok = m[s]; ok {
					return nil, errJSON
				}
				v, e := value(ctx, d, depth+1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			t, e = d.Token()
			if e != nil || t != json.Delim('}') {
				return nil, errJSON
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, e := value(ctx, d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			t, e = d.Token()
			if e != nil || t != json.Delim(']') {
				return nil, errJSON
			}
			return a, nil
		default:
			return nil, errJSON
		}
	}
	return t, nil
}
func strict(ctx context.Context, b []byte, out any) error {
	if _, e := object(ctx, b); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return errJSON
	}
	return ctx.Err()
}
func validEscapes(b []byte) bool {
	in := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			in = !in
			continue
		}
		if !in || b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return false
		}
		if b[i] != 'u' {
			continue
		}
		if i+4 >= len(b) {
			return false
		}
		n, e := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !in
}
