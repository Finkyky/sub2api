package contractv1

import (
	"encoding/json"
	"testing"
)

func TestValidateTaskResult(t *testing.T) {
	tests := []struct {
		name   string
		result TaskResult
		valid  bool
	}{
		{"success no change", TaskResult{Status: TaskSucceeded}, true},
		{"success changed", TaskResult{Status: TaskSucceeded, Changed: true, Output: json.RawMessage(`{"version":2}`)}, true},
		{"failure", TaskResult{Status: TaskFailed, ErrorCode: "upstream_unavailable"}, true},
		{"partial commits are explicit", TaskResult{Status: TaskFailed, Changed: true, ErrorCode: "partial_failure"}, true},
		{"hook decision is not task status", TaskResult{Status: "pass"}, false},
		{"success with failure code", TaskResult{Status: TaskSucceeded, ErrorCode: "failed"}, false},
		{"failure requires code", TaskResult{Status: TaskFailed}, false},
		{"failure code is structured", TaskResult{Status: TaskFailed, ErrorCode: "raw error text"}, false},
		{"invalid json", TaskResult{Status: TaskSucceeded, Output: json.RawMessage(`{`)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateTaskResult(tt.result); (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
		})
	}
}
