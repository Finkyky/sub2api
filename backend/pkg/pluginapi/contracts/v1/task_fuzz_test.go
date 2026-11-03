package contractv1

import (
	"encoding/json"
	"errors"
	"testing"
)

func FuzzValidateTaskResult(f *testing.F) {
	f.Add("succeeded", false, "", []byte(`{"version":1}`))
	f.Add("failed", true, "partial_failure", []byte(`{"changed":true}`))
	f.Add("failed", false, "raw error", []byte{0xff})
	f.Add("pass", true, "", []byte(`{`))
	f.Add("succeeded", true, "", []byte{})
	f.Fuzz(func(t *testing.T, status string, changed bool, code string, output []byte) {
		result := TaskResult{Status: TaskStatus(status), Changed: changed, ErrorCode: code, Output: json.RawMessage(output)}
		err := ValidateTaskResult(result)
		if err != nil {
			if !errors.Is(err, ErrInvalidTaskResult) {
				t.Fatalf("unclassified error: %v", err)
			}
			return
		}
		if len(output) > 0 && !json.Valid(output) {
			t.Fatal("accepted invalid JSON")
		}
		switch result.Status {
		case TaskSucceeded:
			if code != "" {
				t.Fatal("accepted failure code with success")
			}
		case TaskFailed:
			if !codePattern.MatchString(code) {
				t.Fatal("accepted invalid failure code")
			}
		default:
			t.Fatal("accepted hook decision or unknown task status")
		}
	})
}
