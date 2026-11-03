package contractv1

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestValidateHookResult(t *testing.T) {
	high, low, invalid, nan, infinite := 0.99, 0.01, 1.1, math.NaN(), math.Inf(1)
	rejection := &Rejection{Code: "policy_blocked", Message: "Content blocked"}
	content := &Content{MediaType: "text/plain", Data: []byte("clean")}
	tests := []struct {
		name       string
		result     HookResult
		canReplace bool
		valid      bool
	}{
		{"pass", HookResult{Decision: Pass}, false, true},
		{"plugin pass wins over high confidence", HookResult{Decision: Pass, Findings: []Finding{{Confidence: &high}}}, false, true},
		{"plugin block wins over low confidence", HookResult{Decision: Block, Rejection: rejection, Findings: []Finding{{Confidence: &low}}}, false, true},
		{"replace", HookResult{Decision: Replace, Content: content}, true, true},
		{"empty replacement is explicit", HookResult{Decision: Replace, Content: &Content{MediaType: "text/plain", Data: []byte{}}}, true, true},
		{"unknown decision", HookResult{Decision: "allow"}, false, false},
		{"pass cannot carry replacement", HookResult{Decision: Pass, Content: content}, true, false},
		{"pass cannot carry rejection", HookResult{Decision: Pass, Rejection: rejection}, false, false},
		{"replace must be authorized", HookResult{Decision: Replace, Content: content}, false, false},
		{"replace needs content", HookResult{Decision: Replace}, true, false},
		{"replace needs media type", HookResult{Decision: Replace, Content: &Content{}}, true, false},
		{"replace cannot reject", HookResult{Decision: Replace, Content: content, Rejection: rejection}, true, false},
		{"block needs rejection", HookResult{Decision: Block}, false, false},
		{"block cannot return original body", HookResult{Decision: Block, Content: content, Rejection: rejection}, false, false},
		{"block needs machine code", HookResult{Decision: Block, Rejection: &Rejection{Code: "bad code", Message: "Blocked"}}, false, false},
		{"confidence out of range", HookResult{Decision: Pass, Findings: []Finding{{Confidence: &invalid}}}, false, false},
		{"confidence nan", HookResult{Decision: Pass, Findings: []Finding{{Confidence: &nan}}}, false, false},
		{"confidence infinite", HookResult{Decision: Pass, Findings: []Finding{{Confidence: &infinite}}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHookResult(tt.result, tt.canReplace)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
}

func TestCloneContentOwnsBytes(t *testing.T) {
	original := Content{MediaType: "text/plain", Data: []byte("original")}
	cloned := original.Clone()
	cloned.Data[0] = 'X'
	if string(original.Data) != "original" {
		t.Fatal("content copy shares backing memory")
	}
}

func TestValidateHookResultBoundaries(t *testing.T) {
	validBlock := func(message string) HookResult {
		return HookResult{Decision: Block, Rejection: &Rejection{Code: "policy_blocked", Message: message}}
	}
	tests := []struct {
		name   string
		result HookResult
		valid  bool
	}{
		{"unscored finding", HookResult{Decision: Pass, Findings: []Finding{{RuleID: "deterministic"}}}, true},
		{"maximum findings", HookResult{Decision: Pass, Findings: make([]Finding, 128)}, true},
		{"too many findings", HookResult{Decision: Pass, Findings: make([]Finding, 129)}, false},
		{"unicode message at limit", validBlock(strings.Repeat("\u4e2d", 512)), true},
		{"unicode message over limit", validBlock(strings.Repeat("\u4e2d", 513)), false},
		{"blank message", validBlock(" \t\n"), false},
		{"invalid utf8 message", validBlock(string([]byte{0xff})), false},
		{"invalid media type", HookResult{Decision: Replace, Content: &Content{MediaType: "secret-invalid-type"}}, false},
		{"invalid media parameters", HookResult{Decision: Replace, Content: &Content{MediaType: "text/plain; secret-invalid-parameter"}}, false},
		{"structured media parameters", HookResult{Decision: Replace, Content: &Content{MediaType: "application/json; charset=utf-8"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHookResult(tt.result, true)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
			if err != nil && (!errors.Is(err, ErrInvalidResult) || strings.Contains(err.Error(), "secret-")) {
				t.Fatalf("unexpected error classification or leaked input: %v", err)
			}
		})
	}
}

func TestStageValid(t *testing.T) {
	for _, stage := range []Stage{Input, Output} {
		if !stage.Valid() {
			t.Fatalf("valid stage rejected: %q", stage)
		}
	}
	for _, stage := range []Stage{"", "job", "INPUT"} {
		if stage.Valid() {
			t.Fatalf("invalid stage accepted: %q", stage)
		}
	}
}
