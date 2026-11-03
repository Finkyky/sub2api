package contractv1

import (
	"encoding/json"
	"errors"
	"math"
	"mime"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzValidateHookResult(f *testing.F) {
	// Bits: content, rejection, scored finding, unscored finding, too many
	// findings, replacement authorization. These also generate mixed payloads.
	f.Add("pass", "text/plain", "policy", "Blocked", []byte("secret-body"), uint8(4), 0.9)
	f.Add("replace", "application/json", "policy", "Blocked", []byte(`{"ok":true}`), uint8(33), 0.1)
	f.Add("block", "text/plain", "policy", "Blocked", []byte("secret-body"), uint8(6), 0.01)
	f.Add("replace", "text/plain; broken", "bad code", "\xff", []byte{0xff}, uint8(63), math.Inf(1))
	f.Add("unknown", "", "", "", []byte{}, uint8(0), math.NaN())
	f.Fuzz(func(t *testing.T, decision, mediaType, code, message string, data []byte, flags uint8, score float64) {
		result := HookResult{Decision: Decision(decision)}
		if flags&1 != 0 {
			result.Content = &Content{MediaType: mediaType, Data: data}
		}
		if flags&2 != 0 {
			result.Rejection = &Rejection{Code: code, Message: message}
		}
		if flags&4 != 0 {
			result.Findings = append(result.Findings, Finding{Confidence: &score})
		}
		if flags&8 != 0 {
			result.Findings = append(result.Findings, Finding{RuleID: string(data), RuleVersion: message})
		}
		if flags&16 != 0 {
			result.Findings = make([]Finding, 129)
		}
		canReplace := flags&32 != 0
		err := ValidateHookResult(result, canReplace)
		if err != nil {
			if !errors.Is(err, ErrInvalidResult) || len(err.Error()) > 200 {
				t.Fatalf("unbounded or unclassified error: %v", err)
			}
			return
		}
		if len(result.Findings) > 128 {
			t.Fatal("accepted too many findings")
		}
		for _, finding := range result.Findings {
			if finding.Confidence != nil {
				s := *finding.Confidence
				if math.IsNaN(s) || math.IsInf(s, 0) || s < 0 || s > 1 {
					t.Fatal("accepted invalid confidence")
				}
			}
		}
		switch result.Decision {
		case Pass:
			if result.Content != nil || result.Rejection != nil {
				t.Fatal("pass accepted action payload")
			}
		case Replace:
			if !canReplace || result.Content == nil || result.Rejection != nil {
				t.Fatal("accepted unauthorized or mixed replacement")
			}
			m, _, parseErr := mime.ParseMediaType(result.Content.MediaType)
			if parseErr != nil || !strings.Contains(m, "/") {
				t.Fatal("accepted invalid MIME")
			}
		case Block:
			if result.Content != nil || result.Rejection == nil {
				t.Fatal("accepted mixed block")
			}
			if !codePattern.MatchString(code) || !utf8.ValidString(message) || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > 512 {
				t.Fatal("accepted invalid rejection")
			}
		default:
			t.Fatal("accepted unknown decision")
		}
	})
}

func TestFindingRuleVersionJSONCompatibility(t *testing.T) {
	for _, version := range []string{"", "v2.1"} {
		original := HookResult{Decision: Pass, Findings: []Finding{{RuleID: "rule-1", RuleVersion: version}}}
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "rule_version") != (version != "") {
			t.Fatalf("optional field encoding: %s", data)
		}
		var decoded HookResult
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if err := ValidateHookResult(decoded, false); err != nil {
			t.Fatal(err)
		}
		if decoded.Findings[0].RuleID != "rule-1" || decoded.Findings[0].RuleVersion != version {
			t.Fatalf("roundtrip=%+v", decoded)
		}
	}
}
