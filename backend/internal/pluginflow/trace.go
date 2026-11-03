package pluginflow

import (
	"context"
	"errors"
	"strings"
	"time"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

// FailureKind is a bounded host classification, never a plugin error message.
type FailureKind string

const (
	FailureInvocation         FailureKind = "invocation_error"
	FailureCanceled           FailureKind = "canceled"
	FailureDeadline           FailureKind = "deadline_exceeded"
	FailureInvalidResult      FailureKind = "invalid_result"
	FailureBindingUnavailable FailureKind = "binding_unavailable"
	FailureInvalidContext     FailureKind = "invalid_context"
)

// AuditFinding retains bounded identifiers and an independently owned score.
// Identifiers remain plugin-controlled: syntax filtering is not semantic
// redaction. The audit adapter must apply its disclosure policy before storage.
type AuditFinding struct {
	RuleID          string
	RuleVersion     string
	Category        string
	Confidence      *float64
	MetadataOmitted bool
}

// ExecutionRecord describes one attempted invocation. It deliberately excludes
// body bytes, replacement content, rejection messages and raw plugin errors.
// Decision and Findings are populated only for a validated, accepted result.
type ExecutionRecord struct {
	BindingID      int64
	InstanceID     int64
	GroupID        int64
	Stage          contract.Stage
	Version        string
	ConfigRevision int64
	Decision       contract.Decision
	Failure        FailureKind
	Duration       time.Duration // Invocation time, not HTTP or whole-chain latency.
	Findings       []AuditFinding
}

// ExecutionTrace is host-internal, separate from client-eligible Outcome.
// Only attempted invocations appear in Records; a preflight error is represented
// by Failure with no Records. Earlier records survive a later chain failure.
// This type is not a client response or an automatic logging/persistence API.
type ExecutionTrace struct {
	RequestID string
	AttemptID string
	Failure   FailureKind
	Records   []ExecutionRecord
}

// RunWithTrace preserves Run semantics and additionally returns host audit data.
// Errors still return an empty Outcome; trace data must not be sent to clients.
func RunWithTrace(ctx context.Context, req contract.HookRequest, bindings []Binding, invoke Invoker) (Outcome, ExecutionTrace, error) {
	trace := ExecutionTrace{RequestID: req.RequestID, AttemptID: req.AttemptID}
	out, err := run(ctx, req, bindings, invoke, &trace)
	trace.Failure = classifyFailure(err)
	return out, trace, err
}

func classifyFailure(err error) FailureKind {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return FailureCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return FailureDeadline
	case errors.Is(err, contract.ErrInvalidResult):
		return FailureInvalidResult
	case errors.Is(err, ErrBindingUnavailable):
		return FailureBindingUnavailable
	case errors.Is(err, ErrInvalidContext):
		return FailureInvalidContext
	default:
		return FailureInvocation
	}
}

// auditIdentifier omits malformed/oversized metadata rather than changing the
// business decision or truncating an identifier into a different valid rule.
func auditIdentifier(value string) (string, bool) {
	if len(value) > 128 {
		return "", true
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == ':' || c == '-') {
			return "", true
		}
	}
	return strings.Clone(value), false
}

func (trace *ExecutionTrace) record(b Binding, result contract.HookResult, err error, duration time.Duration) {
	if trace == nil {
		return
	}
	r := ExecutionRecord{BindingID: b.ID, InstanceID: b.InstanceID, GroupID: b.GroupID, Stage: b.Stage, Version: b.Version, ConfigRevision: b.ConfigRevision, Failure: classifyFailure(err), Duration: duration}
	if err == nil {
		r.Decision = result.Decision
		if len(result.Findings) > 0 {
			r.Findings = make([]AuditFinding, len(result.Findings))
		}
		for i, f := range result.Findings {
			id, idOmitted := auditIdentifier(f.RuleID)
			version, versionOmitted := auditIdentifier(f.RuleVersion)
			category, categoryOmitted := auditIdentifier(f.Category)
			finding := AuditFinding{RuleID: id, RuleVersion: version, Category: category, MetadataOmitted: idOmitted || versionOmitted || categoryOmitted}
			if f.Confidence != nil {
				score := *f.Confidence
				finding.Confidence = &score
			}
			r.Findings[i] = finding
		}
	}
	trace.Records = append(trace.Records, r)
}
