// Package contractv1 defines runtime-independent plugin domain contracts.
// It does not define the transport-v1 wire protocol or HTTP response encoding.
package contractv1

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Stage identifies the input or output check of a business request.
type Stage string

const (
	Input  Stage = "input"
	Output Stage = "output"
)

func (s Stage) Valid() bool { return s == Input || s == Output }

// Decision is the plugin-owned action; confidence never overrides it.
type Decision string

const (
	Pass    Decision = "pass"
	Replace Decision = "replace"
	Block   Decision = "block"
)

// Content carries adapter-defined bytes. Protocol adapters validate their shape.
type Content struct {
	MediaType string `json:"media_type"`
	Data      []byte `json:"data"`
}

// Clone returns content with independent backing bytes.
func (c Content) Clone() Content {
	c.Data = bytes.Clone(c.Data)
	return c
}

// HookRequest contains host-authenticated context and the current content.
type HookRequest struct {
	RequestID string  `json:"request_id"`
	AttemptID string  `json:"attempt_id"`
	GroupID   int64   `json:"group_id"`
	Stage     Stage   `json:"stage"`
	Content   Content `json:"content"`
}

// Finding supplies audit metadata, not a host-side risk decision.
type Finding struct {
	RuleID      string   `json:"rule_id,omitempty"`
	RuleVersion string   `json:"rule_version,omitempty"`
	Category    string   `json:"category,omitempty"`
	Confidence  *float64 `json:"confidence,omitempty"`
}

// Rejection separates the client-facing denial from the inspected content.
// The caller still owns protocol encoding and sensitive-message handling.
type Rejection struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HookResult expresses exactly one action, with only its permitted payload.
type HookResult struct {
	Decision  Decision   `json:"decision"`
	Content   *Content   `json:"content,omitempty"`
	Rejection *Rejection `json:"rejection,omitempty"`
	Findings  []Finding  `json:"findings,omitempty"`
}

var ErrInvalidResult = errors.New("invalid plugin hook result")
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidateHookResult checks the contract and replacement authority only.
// Content size, protocol shape and redaction are adapter responsibilities.
func ValidateHookResult(r HookResult, canReplace bool) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s", ErrInvalidResult, reason)
	}
	if len(r.Findings) > 128 {
		return invalid("too many findings")
	}
	for _, finding := range r.Findings {
		if finding.Confidence == nil {
			continue
		}
		score := *finding.Confidence
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return invalid("confidence outside [0,1]")
		}
	}
	switch r.Decision {
	case Pass:
		if r.Content != nil || r.Rejection != nil {
			return invalid("pass contains an action payload")
		}
	case Replace:
		if !canReplace || r.Content == nil || r.Rejection != nil {
			return invalid("replacement missing or not authorized")
		}
		mediaType, _, err := mime.ParseMediaType(r.Content.MediaType)
		if err != nil || !strings.Contains(mediaType, "/") {
			return invalid("replacement media type missing or invalid")
		}
	case Block:
		if r.Content != nil || r.Rejection == nil || !codePattern.MatchString(r.Rejection.Code) {
			return invalid("block payload invalid")
		}
		message := r.Rejection.Message
		if !utf8.ValidString(message) || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > 512 {
			return invalid("client message invalid")
		}
	default:
		return invalid("unknown decision")
	}
	return nil
}
