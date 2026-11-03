package contractv1

import (
	"encoding/json"
	"errors"
)

// TaskStatus is independent of request-chain decisions and scheduler leases.
type TaskStatus string

const (
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
)

// TaskResult reports a plugin job result. Changed may remain true on failure
// when earlier business operations committed; it does not claim atomic rollback.
type TaskResult struct {
	Status    TaskStatus      `json:"status"`
	Changed   bool            `json:"changed"`
	ErrorCode string          `json:"error_code,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

var ErrInvalidTaskResult = errors.New("invalid plugin task result")

// ValidateTaskResult validates status and structured output without applying it.
func ValidateTaskResult(r TaskResult) error {
	switch r.Status {
	case TaskSucceeded:
		if r.ErrorCode != "" {
			return ErrInvalidTaskResult
		}
	case TaskFailed:
		if !codePattern.MatchString(r.ErrorCode) {
			return ErrInvalidTaskResult
		}
	default:
		return ErrInvalidTaskResult
	}
	if len(r.Output) > 0 && !json.Valid(r.Output) {
		return ErrInvalidTaskResult
	}
	return nil
}
