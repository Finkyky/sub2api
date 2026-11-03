package pluginflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

func fixtureRequest() contract.HookRequest {
	return contract.HookRequest{RequestID: "request-1", AttemptID: "attempt-1",
		GroupID: 7, Stage: contract.Input,
		Content: contract.Content{MediaType: "text/plain", Data: []byte("original")}}
}

func TestRunUnboundDoesNotInvoke(t *testing.T) {
	bindings := []Binding{fixtureBinding(1, 8, contract.Input, 0)}
	out, err := Run(context.Background(), fixtureRequest(), bindings, nil)
	if err != nil || out.Decision != contract.Pass || out.Content == nil || string(out.Content.Data) != "original" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestRunReplacementFeedsNextPlugin(t *testing.T) {
	first := fixtureBinding(1, 7, contract.Input, 0)
	first.CanReplace = true
	second := fixtureBinding(2, 7, contract.Input, 1)
	calls := 0
	invoke := func(_ context.Context, b Binding, req contract.HookRequest) (contract.HookResult, error) {
		calls++
		if b.ID == 1 {
			content := contract.Content{MediaType: "text/plain", Data: []byte("clean")}
			return contract.HookResult{Decision: contract.Replace, Content: &content}, nil
		}
		if string(req.Content.Data) != "clean" {
			t.Fatal("next plugin did not receive replacement")
		}
		return contract.HookResult{Decision: contract.Pass}, nil
	}
	out, err := Run(context.Background(), fixtureRequest(), []Binding{second, first}, invoke)
	if err != nil || calls != 2 || out.Content == nil || string(out.Content.Data) != "clean" {
		t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
	}
}

func TestRunBlockStopsAndReturnsNoOriginalContent(t *testing.T) {
	req := fixtureRequest()
	req.Stage = contract.Output
	bindings := []Binding{fixtureBinding(1, 7, contract.Output, 0), fixtureBinding(2, 7, contract.Output, 1)}
	calls := 0
	invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
		calls++
		score := 0.01
		return contract.HookResult{Decision: contract.Block,
			Rejection: &contract.Rejection{Code: "business_policy", Message: "Blocked"},
			Findings:  []contract.Finding{{Confidence: &score}}}, nil
	}
	out, err := Run(context.Background(), req, bindings, invoke)
	if err != nil || calls != 1 || out.Decision != contract.Block || out.Content != nil || out.Rejection == nil {
		t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
	}
}

func TestRunPassCannotMutateInput(t *testing.T) {
	req := fixtureRequest()
	invoke := func(_ context.Context, _ Binding, input contract.HookRequest) (contract.HookResult, error) {
		input.Content.Data[0] = 'X'
		score := 0.99
		return contract.HookResult{Decision: contract.Pass, Findings: []contract.Finding{{Confidence: &score}}}, nil
	}
	out, err := Run(context.Background(), req, []Binding{fixtureBinding(1, 7, contract.Input, 0)}, invoke)
	if err != nil || out.Content == nil || string(out.Content.Data) != "original" || string(req.Content.Data) != "original" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestRunFailuresReturnNoPartialContent(t *testing.T) {
	tests := []struct {
		name   string
		invoke Invoker
		target error
	}{
		{"missing invoker", nil, ErrInvocation},
		{"plugin error", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			return contract.HookResult{}, errors.New("secret-upstream-token")
		}, ErrInvocation},
		{"panic", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			panic("secret-upstream-token")
		}, ErrInvocation},
		{"unknown result", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			return contract.HookResult{Decision: "unknown"}, nil
		}, contract.ErrInvalidResult},
		{"unauthorized replace", func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
			content := contract.Content{MediaType: "text/plain", Data: []byte("changed")}
			return contract.HookResult{Decision: contract.Replace, Content: &content}, nil
		}, contract.ErrInvalidResult},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Run(context.Background(), fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0)}, tt.invoke)
			if !errors.Is(err, tt.target) || out.Content != nil || out.Rejection != nil {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if strings.Contains(err.Error(), "secret-upstream-token") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestRunCancellationDiscardsResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
		cancel()
		return contract.HookResult{Decision: contract.Pass}, nil
	}
	out, err := Run(ctx, fixtureRequest(), []Binding{fixtureBinding(1, 7, contract.Input, 0)}, invoke)
	if !errors.Is(err, context.Canceled) || out.Content != nil {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestRunPreflightFailuresNeverInvoke(t *testing.T) {
	tests := []struct {
		name    string
		context func() context.Context
		request contract.HookRequest
		binding Binding
		target  error
	}{
		{"already cancelled", func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }, fixtureRequest(), fixtureBinding(1, 7, contract.Input, 0), context.Canceled},
		{"invalid group", context.Background, contract.HookRequest{Stage: contract.Input}, fixtureBinding(1, 7, contract.Input, 0), ErrInvalidContext},
		{"unavailable binding", context.Background, fixtureRequest(), Binding{ID: 1, GroupID: 7, Stage: contract.Input, Enabled: true}, ErrBindingUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
				calls++
				return contract.HookResult{Decision: contract.Pass}, nil
			}
			out, err := Run(tt.context(), tt.request, []Binding{tt.binding}, invoke)
			if !errors.Is(err, tt.target) || calls != 0 || out.Content != nil || out.Decision != "" {
				t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
			}
		})
	}
}

func TestRunLaterFailureDiscardsPreviousReplacement(t *testing.T) {
	first := fixtureBinding(1, 7, contract.Input, 0)
	first.CanReplace = true
	for _, panicSecond := range []bool{false, true} {
		calls := 0
		invoke := func(_ context.Context, b Binding, req contract.HookRequest) (contract.HookResult, error) {
			calls++
			if b.ID == 1 {
				content := contract.Content{MediaType: "text/plain", Data: []byte("partial-sensitive-content")}
				return contract.HookResult{Decision: contract.Replace, Content: &content}, nil
			}
			if string(req.Content.Data) != "partial-sensitive-content" {
				t.Fatal("wrong chain input")
			}
			if panicSecond {
				panic("private failure")
			}
			return contract.HookResult{}, errors.New("private failure")
		}
		out, err := Run(context.Background(), fixtureRequest(), []Binding{first, fixtureBinding(2, 7, contract.Input, 1)}, invoke)
		if !errors.Is(err, ErrInvocation) || calls != 2 || out.Content != nil || out.Rejection != nil || out.Decision != "" {
			t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
		}
	}
}

func TestRunPinsBindingValuesAndCopiesReplacement(t *testing.T) {
	first := fixtureBinding(1, 7, contract.Input, 0)
	first.CanReplace = true
	bindings := []Binding{first, fixtureBinding(2, 7, contract.Input, 1)}
	replacement := contract.Content{MediaType: "text/plain", Data: []byte("clean")}
	invoke := func(_ context.Context, b Binding, req contract.HookRequest) (contract.HookResult, error) {
		if b.ID == 1 {
			bindings[1].Version = "new-version"
			bindings[1].ConfigRevision = 2
			bindings[1].Enabled = false
			return contract.HookResult{Decision: contract.Replace, Content: &replacement}, nil
		}
		if b.Version != "1.0.0" || b.ConfigRevision != 1 || !b.Enabled {
			t.Fatal("binding snapshot changed in flight")
		}
		replacement.Data[0] = 'X'
		if string(req.Content.Data) != "clean" {
			t.Fatal("replacement aliases the plugin buffer")
		}
		req.Content.Data[0] = 'Y'
		return contract.HookResult{Decision: contract.Pass}, nil
	}
	out, err := Run(context.Background(), fixtureRequest(), bindings, invoke)
	if err != nil || out.Content == nil || string(out.Content.Data) != "clean" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	replacement.Data[1] = 'Z'
	if string(out.Content.Data) != "clean" {
		t.Fatal("outcome aliases the plugin buffer")
	}
}

func TestRunUnboundOutcomeOwnsContent(t *testing.T) {
	req := fixtureRequest()
	out, err := Run(context.Background(), req, nil, nil)
	if err != nil || out.Content == nil {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	out.Content.Data[0] = 'X'
	if string(req.Content.Data) != "original" {
		t.Fatal("outcome aliases caller input")
	}
}

func TestRunReplacementThenCancellationOrInvalidResult(t *testing.T) {
	for _, name := range []string{"canceled", "invalid result"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := fixtureBinding(1, 7, contract.Input, 0)
			first.CanReplace = true
			bindings := []Binding{first, fixtureBinding(2, 7, contract.Input, 1), fixtureBinding(3, 7, contract.Input, 2)}
			calls := 0
			target := contract.ErrInvalidResult
			if name == "canceled" {
				target = context.Canceled
			}
			invoke := func(_ context.Context, b Binding, req contract.HookRequest) (contract.HookResult, error) {
				calls++
				if b.ID == 1 {
					content := contract.Content{MediaType: "text/plain", Data: []byte("replacement")}
					return contract.HookResult{Decision: contract.Replace, Content: &content}, nil
				}
				if b.ID != 2 {
					t.Fatal("invoked after chain failure")
				}
				if string(req.Content.Data) != "replacement" {
					t.Fatal("replacement not forwarded")
				}
				if name == "canceled" {
					cancel()
					return contract.HookResult{Decision: contract.Pass}, nil
				}
				return contract.HookResult{Decision: "invalid"}, nil
			}
			out, err := Run(ctx, fixtureRequest(), bindings, invoke)
			if !errors.Is(err, target) || calls != 2 || out != (Outcome{}) {
				t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
			}
		})
	}
}

func TestRunPreflightsAllMatchingBindings(t *testing.T) {
	healthy := fixtureBinding(1, 7, contract.Input, 0)
	unavailable := fixtureBinding(2, 7, contract.Input, 1)
	unavailable.Ready = false
	for _, tt := range []struct {
		name     string
		bindings []Binding
	}{
		{"healthy first", []Binding{healthy, unavailable}},
		{"unavailable first", []Binding{unavailable, healthy}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			invoke := func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error) {
				calls++
				return contract.HookResult{Decision: contract.Pass}, nil
			}
			out, err := Run(context.Background(), fixtureRequest(), tt.bindings, invoke)
			if !errors.Is(err, ErrBindingUnavailable) || calls != 0 || out != (Outcome{}) {
				t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
			}
		})
	}
}
