package pluginflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

func TestRunWithTraceSequenceAndOwnedFindings(t *testing.T) {
	req := fixtureRequest()
	req.Content.Data = []byte("secret-body")
	first := fixtureBinding(1, 7, contract.Input, 0)
	first.CanReplace = true
	second := fixtureBinding(2, 7, contract.Input, 1)
	third := fixtureBinding(3, 7, contract.Input, 2)
	score := 0.91
	findings := []contract.Finding{{RuleID: "credentials", RuleVersion: "v2.1", Category: "security", Confidence: &score}}
	calls := 0
	invoke := func(_ context.Context, b Binding, input contract.HookRequest) (contract.HookResult, error) {
		calls++
		switch b.ID {
		case 1:
			content := contract.Content{MediaType: "text/plain", Data: []byte("secret-replacement")}
			return contract.HookResult{Decision: contract.Replace, Content: &content, Findings: findings}, nil
		case 2:
			if string(input.Content.Data) != "secret-replacement" {
				t.Fatal("replacement lost")
			}
			score = 0.1
			findings[0].RuleID = "mutated"
			return contract.HookResult{Decision: contract.Block, Rejection: &contract.Rejection{Code: "policy", Message: "secret-denial"}}, nil
		default:
			t.Fatal("called after block")
			return contract.HookResult{}, nil
		}
	}
	out, trace, err := RunWithTrace(context.Background(), req, []Binding{third, second, first}, invoke)
	if err != nil || out.Decision != contract.Block || out.Content != nil || calls != 2 {
		t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
	}
	if trace.RequestID != req.RequestID || trace.AttemptID != req.AttemptID || trace.Failure != "" || len(trace.Records) != 2 {
		t.Fatalf("trace=%+v", trace)
	}
	r := trace.Records[0]
	if r.BindingID != first.ID || r.InstanceID != first.InstanceID || r.GroupID != first.GroupID || r.Stage != first.Stage || r.Version != first.Version || r.ConfigRevision != first.ConfigRevision || r.Decision != contract.Replace || r.Failure != "" || r.Duration < 0 {
		t.Fatalf("record=%+v", r)
	}
	if r.Findings[0].RuleID != "credentials" || r.Findings[0].RuleVersion != "v2.1" || r.Findings[0].Category != "security" || *r.Findings[0].Confidence != 0.91 || r.Findings[0].MetadataOmitted {
		t.Fatalf("finding=%+v", r.Findings[0])
	}
	*r.Findings[0].Confidence = 0.5
	if score != 0.1 {
		t.Fatal("trace aliases plugin score")
	}
	if trace.Records[1].BindingID != second.ID || trace.Records[1].Decision != contract.Block {
		t.Fatalf("records=%+v", trace.Records)
	}
	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-body", "secret-replacement", "secret-denial"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("trace contains %s", secret)
		}
	}
}

func TestRunWithTraceFailuresRetainOnlyValidatedEarlierSteps(t *testing.T) {
	tests := []struct {
		name    string
		invoke  Invoker
		target  error
		failure FailureKind
	}{
		{"error", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			return contract.HookResult{}, errors.New("secret-error")
		}, ErrInvocation, FailureInvocation},
		{"panic", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			panic("secret-error")
		}, ErrInvocation, FailureInvocation},
		{"invalid", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			return contract.HookResult{Decision: "invalid", Findings: []contract.Finding{{RuleID: "secret-error"}}}, nil
		}, contract.ErrInvalidResult, FailureInvalidResult},
		{"plugin deadline", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			return contract.HookResult{}, fmt.Errorf("secret-error: %w", context.DeadlineExceeded)
		}, context.DeadlineExceeded, FailureDeadline},
		{"plugin cancel", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			return contract.HookResult{}, fmt.Errorf("secret-error: %w", context.Canceled)
		}, context.Canceled, FailureCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := fixtureBinding(1, 7, contract.Input, 0)
			first.CanReplace = true
			calls := 0
			invoke := func(ctx context.Context, b Binding, req contract.HookRequest) (contract.HookResult, error) {
				calls++
				if b.ID == 1 {
					content := contract.Content{MediaType: "text/plain", Data: []byte("secret-body")}
					return contract.HookResult{Decision: contract.Replace, Content: &content}, nil
				}
				if b.ID != 2 {
					t.Fatal("called after failure")
				}
				return tt.invoke(ctx, b, req)
			}
			ctx := context.Background()
			out, trace, err := RunWithTrace(ctx, fixtureRequest(), []Binding{first, fixtureBinding(2, 7, contract.Input, 1), fixtureBinding(3, 7, contract.Input, 2)}, invoke)
			if !errors.Is(err, tt.target) || out != (Outcome{}) || calls != 2 || ctx.Err() != nil {
				t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
			}
			if trace.Failure != tt.failure || len(trace.Records) != 2 || trace.Records[0].Decision != contract.Replace || trace.Records[1].Failure != tt.failure || trace.Records[1].Decision != "" || len(trace.Records[1].Findings) != 0 {
				t.Fatalf("trace=%+v", trace)
			}
			data, _ := json.Marshal(trace)
			if strings.Contains(string(data), "secret-") || strings.Contains(err.Error(), "secret-") {
				t.Fatal("raw content or error leaked")
			}
			if tt.failure == FailureDeadline || tt.failure == FailureCanceled {
				if !errors.Is(err, ErrInvocation) {
					t.Fatal("lost invocation classification")
				}
			}
		})
	}
}

func TestRunWithTracePreflightAndNoCalls(t *testing.T) {
	tests := []struct {
		name     string
		request  contract.HookRequest
		bindings []Binding
		target   error
		failure  FailureKind
	}{
		{"unbound", fixtureRequest(), []Binding{fixtureBinding(1, 8, contract.Input, 0)}, nil, ""},
		{"invalid context", contract.HookRequest{}, nil, ErrInvalidContext, FailureInvalidContext},
	}
	unavailable := fixtureBinding(2, 7, contract.Input, 1)
	unavailable.Ready = false
	tests = append(tests, struct {
		name     string
		request  contract.HookRequest
		bindings []Binding
		target   error
		failure  FailureKind
	}{"unavailable", fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0), unavailable}, ErrBindingUnavailable, FailureBindingUnavailable})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			out, trace, err := RunWithTrace(context.Background(), tt.request, tt.bindings, func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
				calls++
				return contract.HookResult{Decision: contract.Pass}, nil
			})
			if !errors.Is(err, tt.target) || calls != 0 || len(trace.Records) != 0 || trace.Failure != tt.failure {
				t.Fatalf("trace=%+v calls=%d err=%v", trace, calls, err)
			}
			if err != nil && out != (Outcome{}) {
				t.Fatal("failed preflight exposed content")
			}
			if err == nil && (out.Decision != contract.Pass || out.Content == nil) {
				t.Fatal("unbound request not passed")
			}
		})
	}
}

func TestRunWithTraceFiltersIdentifiersWithoutChangingDecision(t *testing.T) {
	score := 0.8
	cases := []struct {
		value string
		keep  bool
	}{
		{"", true}, {"rule.v1:password-check_1", true}, {strings.Repeat("a", 128), true},
		{strings.Repeat("a", 129), false}, {"password=secret", false}, {"line\nbreak", false}, {"\u4e2d", false}, {string([]byte{0xff}), false},
	}
	for _, tt := range cases {
		t.Run(fmt.Sprintf("%x", tt.value), func(t *testing.T) {
			invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
				return contract.HookResult{Decision: contract.Pass, Findings: []contract.Finding{{RuleID: tt.value, RuleVersion: tt.value, Category: tt.value, Confidence: &score}, {RuleID: "unscored"}}}, nil
			}
			out, trace, err := RunWithTrace(context.Background(), fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0)}, invoke)
			if err != nil || out.Decision != contract.Pass {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			f := trace.Records[0].Findings[0]
			want := ""
			if tt.keep {
				want = tt.value
			}
			if f.RuleID != want || f.RuleVersion != want || f.Category != want || f.MetadataOmitted == tt.keep || *f.Confidence != score {
				t.Fatalf("finding=%+v", f)
			}
			if trace.Records[0].Findings[1].Confidence != nil {
				t.Fatal("invented confidence")
			}
		})
	}
}

func TestRunWithTraceDeadlineDuringInvocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	calls := 0
	out, trace, err := RunWithTrace(ctx, fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0), fixtureBinding(2, 7, contract.Input, 1)}, func(ctx context.Context, _ Binding, _ contract.HookRequest) (contract.HookResult, error) {
		calls++
		<-ctx.Done()
		return contract.HookResult{Decision: contract.Pass}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || out != (Outcome{}) || calls != 1 || trace.Failure != FailureDeadline || len(trace.Records) != 1 || trace.Records[0].Failure != FailureDeadline || trace.Records[0].Decision != "" {
		t.Fatalf("trace=%+v calls=%d err=%v", trace, calls, err)
	}
}

func TestRunWithTraceCanceledBeforeAndDuringInvocation(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(fmt.Sprint(before), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if before {
				cancel()
			}
			out, trace, err := RunWithTrace(ctx, fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0)}, func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
				cancel()
				return contract.HookResult{Decision: contract.Pass}, nil
			})
			want := 1
			if before {
				want = 0
			}
			if !errors.Is(err, context.Canceled) || out != (Outcome{}) || trace.Failure != FailureCanceled || len(trace.Records) != want {
				t.Fatalf("trace=%+v err=%v", trace, err)
			}
		})
	}
}

func TestRunWithTraceMatchesLegacyOutcome(t *testing.T) {
	for _, stage := range []contract.Stage{contract.Input, contract.Output} {
		t.Run(string(stage), func(t *testing.T) {
			req := fixtureRequest()
			req.Stage = stage
			bindings := []Binding{fixtureBinding(1, 7, stage, 0)}
			invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
				return contract.HookResult{Decision: contract.Pass}, nil
			}
			old, oldErr := Run(context.Background(), req, bindings, invoke)
			got, trace, err := RunWithTrace(context.Background(), req, bindings, invoke)
			if oldErr != nil || err != nil || !reflect.DeepEqual(got, old) || len(trace.Records) != 1 || trace.Records[0].Decision != contract.Pass || trace.Records[0].Stage != stage {
				t.Fatalf("trace=%+v err=%v", trace, err)
			}
		})
	}
	out, trace, err := RunWithTrace(context.Background(), fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0)}, nil)
	if !errors.Is(err, ErrInvocation) || out != (Outcome{}) || len(trace.Records) != 0 || trace.Failure != FailureInvocation {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
}

func TestRunWithTracePluginLocalDeadlineLeavesParentActive(t *testing.T) {
	parent := context.Background()
	calls := 0
	out, trace, err := RunWithTrace(parent, fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0), fixtureBinding(2, 7, contract.Input, 1)}, func(ctx context.Context, _ Binding, _ contract.HookRequest) (contract.HookResult, error) {
		calls++
		local, cancel := context.WithTimeout(ctx, time.Millisecond)
		defer cancel()
		<-local.Done()
		return contract.HookResult{}, fmt.Errorf("secret-runtime-detail: %w", local.Err())
	})
	if parent.Err() != nil || calls != 1 || out != (Outcome{}) || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrInvocation) || trace.Failure != FailureDeadline || len(trace.Records) != 1 || trace.Records[0].Failure != FailureDeadline {
		t.Fatalf("trace=%+v calls=%d err=%v", trace, calls, err)
	}
	if strings.Contains(err.Error(), "secret-") {
		t.Fatal("raw error leaked")
	}
}

func TestRunWithTraceRejectsBadResultWithoutAuditMetadata(t *testing.T) {
	for _, name := range []string{"unauthorized replacement", "invalid confidence"} {
		t.Run(name, func(t *testing.T) {
			score := 2.0
			invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
				if name == "invalid confidence" {
					return contract.HookResult{Decision: contract.Pass, Findings: []contract.Finding{{RuleID: "should-not-retain", Confidence: &score}}}, nil
				}
				content := contract.Content{MediaType: "text/plain", Data: []byte("secret-body")}
				return contract.HookResult{Decision: contract.Replace, Content: &content, Findings: []contract.Finding{{RuleID: "should-not-retain"}}}, nil
			}
			out, trace, err := RunWithTrace(context.Background(), fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0)}, invoke)
			if !errors.Is(err, contract.ErrInvalidResult) || out != (Outcome{}) || len(trace.Records) != 1 || trace.Records[0].Decision != "" || trace.Records[0].Failure != FailureInvalidResult || len(trace.Records[0].Findings) != 0 {
				t.Fatalf("trace=%+v err=%v", trace, err)
			}
		})
	}
}
