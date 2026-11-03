package service

import (
	"context"
)

// RunTask must use this instead of the hook's ten-second begin(). It preserves
// the same per-runtime serialization and cancellation/closed semantics while
// bounding tasks by their caller's deadline and the five-minute hard ceiling.
func (r *generalPluginRuntime) beginTask(ctx context.Context) (context.Context, func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	if r.life.Err() != nil {
		return nil, nil, ErrGeneralClosed
	}
	call, cancel := context.WithTimeout(ctx, PluginTaskMaxTimeout)
	abort := context.AfterFunc(r.life, cancel)
	cleanup := func() { abort(); cancel() }
	select {
	case r.slot <- struct{}{}:
	case <-call.Done():
		cleanup()
		if r.life.Err() != nil {
			return nil, nil, ErrGeneralClosed
		}
		return nil, nil, call.Err()
	}
	if e := call.Err(); e != nil {
		<-r.slot
		cleanup()
		if r.life.Err() != nil {
			return nil, nil, ErrGeneralClosed
		}
		return nil, nil, e
	}
	if r.life.Err() != nil {
		<-r.slot
		cleanup()
		return nil, nil, ErrGeneralClosed
	}
	return call, func() { <-r.slot; cleanup() }, nil
}
