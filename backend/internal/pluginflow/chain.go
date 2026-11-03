package pluginflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

var ErrInvocation = errors.New("plugin invocation failed")

// Invoker calls a pinned runtime. It must honor the context and enforce resource
// limits; Run cannot forcibly interrupt a non-cooperative in-process function.
type Invoker func(context.Context, Binding, contract.HookRequest) (contract.HookResult, error)

// Outcome contains client-eligible content only after the complete chain passes.
// Pass means continuation is allowed, not that the content is unchanged.
type Outcome struct {
	Decision  contract.Decision
	Content   *contract.Content
	Rejection *contract.Rejection
}

func invokeProtected(ctx context.Context, b Binding, req contract.HookRequest, invoke Invoker) (result contract.HookResult, err error) {
	defer func() {
		if recover() != nil {
			result = contract.HookResult{}
			err = fmt.Errorf("%w: binding=%d", ErrInvocation, b.ID)
		}
	}()
	result, err = invoke(ctx, b, req)
	if err != nil {
		// Preserve bounded cancellation identities, not the plugin's error text.
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			if errors.Is(err, cause) {
				return contract.HookResult{}, fmt.Errorf("%w: binding=%d: %w", ErrInvocation, b.ID, cause)
			}
		}
		return contract.HookResult{}, fmt.Errorf("%w: binding=%d", ErrInvocation, b.ID)
	}
	return result, nil
}

// Run executes the selected group chain in stable order, copying content at
// invocation boundaries. Block and errors never return inspected body bytes.
// It does not allocate audit records; use RunWithTrace for host audit metadata.
func Run(ctx context.Context, req contract.HookRequest, bindings []Binding, invoke Invoker) (Outcome, error) {
	return run(ctx, req, bindings, invoke, nil)
}

func run(ctx context.Context, req contract.HookRequest, bindings []Binding, invoke Invoker, trace *ExecutionTrace) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	selected, err := SelectBindings(bindings, req.GroupID, req.Stage)
	if err != nil {
		return Outcome{}, err
	}
	// A missing adapter is a host setup failure, not a plugin invocation.
	// Unbound requests still pass without requiring an adapter.
	if len(selected) > 0 && invoke == nil {
		return Outcome{}, ErrInvocation
	}
	current := req.Content.Clone()
	for _, binding := range selected {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		call := req
		call.Content = current.Clone()
		var started time.Time
		if trace != nil {
			started = time.Now()
		}
		result, err := invokeProtected(ctx, binding, call, invoke)
		var duration time.Duration
		if trace != nil {
			duration = time.Since(started)
		}
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		}
		if err == nil {
			err = contract.ValidateHookResult(result, binding.CanReplace)
		}
		trace.record(binding, result, err, duration)
		if err != nil {
			if errors.Is(err, contract.ErrInvalidResult) {
				return Outcome{}, fmt.Errorf("binding=%d: %w", binding.ID, err)
			}
			return Outcome{}, err
		}
		switch result.Decision {
		case contract.Pass:
		case contract.Replace:
			current = result.Content.Clone()
		case contract.Block:
			rejection := *result.Rejection
			return Outcome{Decision: contract.Block, Rejection: &rejection}, nil
		}
	}
	return Outcome{Decision: contract.Pass, Content: &current}, nil
}
