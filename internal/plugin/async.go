package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
)

// AsyncLimiter bounds the number of host calls waiting on an external resource.
// A nil limiter is unlimited.
type AsyncLimiter struct {
	slots chan struct{}
}

// NewAsyncLimiter creates a limiter with the given number of slots.
func NewAsyncLimiter(n int) *AsyncLimiter {
	if n <= 0 {
		return nil
	}
	return &AsyncLimiter{slots: make(chan struct{}, n)}
}

// Acquire takes a slot, waiting for one when all are busy.
func (l *AsyncLimiter) Acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}
	select {
	case l.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release returns a slot.
func (l *AsyncLimiter) Release() {
	if l == nil {
		return
	}
	<-l.slots
}

// post queues fn on the VM goroutine; the job is dropped when the plugin stops.
func (rt *Runtime) post(fn func(*goja.Runtime)) {
	select {
	case rt.jobs <- fn:
	case <-rt.done:
	}
}

// async starts work on its own goroutine and returns a promise settled on the VM goroutine.
// A non-nil limiter bounds the number of calls waiting on an external resource.
func (rt *Runtime) async(vm *goja.Runtime, limiter *AsyncLimiter, work func(context.Context) (any, error)) goja.Value {
	p, resolve, reject := vm.NewPromise()
	ctx, cancel := context.WithCancel(rt.currentCtx())
	stop := context.AfterFunc(rt.ctx, cancel)

	go func() {
		defer cancel()
		defer stop()

		var (
			res any
			err error
		)
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("async call panic: %v", r)
			}
			rt.post(func(vm *goja.Runtime) {
				if err != nil {
					_ = reject(vm.NewGoError(err))
					return
				}
				_ = resolve(res)
			})
		}()

		if limiter != nil {
			if err = limiter.Acquire(ctx); err != nil {
				return
			}
			defer limiter.Release()
		}
		res, err = work(ctx)
	}()

	return vm.ToValue(p)
}

// bindAsync exposes a blocking Go call as a promise-returning JS function.
// Arguments are exported on the VM goroutine before the work starts.
func (rt *Runtime) bindAsync(vm *goja.Runtime, obj *goja.Object, name string, limiter *AsyncLimiter,
	work func(ctx context.Context, args []any) (any, error)) {
	_ = obj.Set(name, func(call goja.FunctionCall) goja.Value {
		args := make([]any, len(call.Arguments))
		for i, arg := range call.Arguments {
			args[i] = arg.Export()
		}
		return rt.async(vm, limiter, func(ctx context.Context) (any, error) {
			return work(ctx, args)
		})
	})
}

// callResult invokes a plugin function and waits for a returned promise off the VM goroutine.
func (rt *Runtime) callResult(ctx context.Context, invoke func(*goja.Runtime) (goja.Value, error)) (any, error) {
	done := make(chan vmResult, 1)

	_, err := rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
		res, callErr := invoke(vm)
		if callErr != nil {
			done <- vmResult{err: callErr}
			return nil, nil
		}
		if res == nil {
			done <- vmResult{}
			return nil, nil
		}
		exported := res.Export()
		if _, ok := exported.(*goja.Promise); ok {
			rt.then(vm, res, func(val any, callErr error) {
				done <- vmResult{val: val, err: callErr}
			})
			return nil, nil
		}
		done <- vmResult{val: exported}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}

	select {
	case r := <-done:
		return r.val, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-rt.done:
		return nil, fmt.Errorf("plugin %q is stopped", rt.manifest.ID)
	}
}

// then attaches Go callbacks to a JS promise; the callbacks run on the VM goroutine.
func (rt *Runtime) then(vm *goja.Runtime, value goja.Value, done func(any, error)) {
	thenFn, ok := goja.AssertFunction(value.ToObject(vm).Get("then"))
	if !ok {
		done(value.Export(), nil)
		return
	}
	_, _ = thenFn(value,
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			done(call.Argument(0).Export(), nil)
			return goja.Undefined()
		}),
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			done(nil, jsErrorFromValue(call.Argument(0)))
			return goja.Undefined()
		}),
	)
}

// jsErrorFromValue converts a promise rejection reason into a Go error.
func jsErrorFromValue(value goja.Value) error {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return errors.New("promise rejected")
	}
	if obj, ok := value.(*goja.Object); ok {
		if msg := obj.Get("message"); msg != nil && !goja.IsUndefined(msg) && !goja.IsNull(msg) {
			return errors.New(msg.String())
		}
	}
	return errors.New(value.String())
}

// ctxTracker keeps the active context across promise continuations.
type ctxTracker struct {
	rt     *Runtime
	parent context.Context
}

// Grab saves the context of the call that scheduled a reaction.
func (t *ctxTracker) Grab() any { return t.rt.currentCtx() }

// Resumed installs the saved context before the reaction runs.
func (t *ctxTracker) Resumed(tracked any) {
	ctx, _ := tracked.(context.Context)
	t.parent = t.rt.activeCtx
	t.rt.activeCtx = ctx
}

// Exited restores the context of the surrounding call.
func (t *ctxTracker) Exited() { t.rt.activeCtx = t.parent }

// yield returns a promise resolved on the next iteration of the VM loop.
func (rt *Runtime) yield(vm *goja.Runtime) goja.Value {
	p, resolve, _ := vm.NewPromise()
	rt.post(func(*goja.Runtime) { _ = resolve(nil) })
	return vm.ToValue(p)
}

// sleep returns a promise resolved after the delay.
func (rt *Runtime) sleep(vm *goja.Runtime, ms int) goja.Value {
	if ms < 0 {
		ms = 0
	}
	return rt.async(vm, nil, func(ctx context.Context) (any, error) {
		timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
}
