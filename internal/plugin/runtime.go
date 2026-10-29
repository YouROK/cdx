package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/pluginapi"
)

// Runtime runs one plugin in its own VM.
//
// goja is not safe for concurrent use, so every VM access goes through a
// single owner goroutine over a command channel.
type Runtime struct {
	manifest *Manifest
	dir      string
	bus      *bus.Bus
	vm       *goja.Runtime
	exports  *goja.Object

	// owner is the bus name of this plugin, used to reject recursion.
	owner string

	commands  chan command
	done      chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	crashes int
	stopped bool
}

type command struct {
	fn   func(vm *goja.Runtime) error
	done chan error
}

// RuntimeOptions configures a runtime.
type RuntimeOptions struct {
	Bus *bus.Bus
	// CallBridge runs cdx.request after the permission check.
	CallBridge func(ctx context.Context, topic string, payload any) (any, error)
	// PublishBridge runs cdx.publish after the permission check.
	PublishBridge func(topic string, payload any) error
	Log           func(level, message string)
	// API gives the plugin reach to files, shell and network.
	//
	// These core capabilities are never exposed to the model as tools:
	// a plugin uses them to build its own.
	API *pluginapi.API
}

// Start creates the VM, runs the entry script and registers the tools.
func Start(ctx context.Context, dir string, m *Manifest, opts RuntimeOptions) (*Runtime, error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	r := &Runtime{
		manifest: m,
		dir:      dir,
		bus:      opts.Bus,
		vm:       vm,
		owner:    "plugin:" + m.Name,
		commands: make(chan command, 16),
		done:     make(chan struct{}),
	}

	go r.loop()

	code, err := os.ReadFile(filepath.Join(dir, m.Entry))
	if err != nil {
		r.Close(0)
		return nil, fmt.Errorf("read entry: %w", err)
	}

	timeout := time.Duration(m.InitTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err = r.exec(initCtx, func(vm *goja.Runtime) error {
		r.installBridge(opts)

		if _, err := vm.RunScript(m.Entry, string(code)); err != nil {
			return fmt.Errorf("run plugin: %w", err)
		}

		obj := vm.Get("cdx")
		if obj == nil || goja.IsUndefined(obj) {
			return fmt.Errorf("plugin did not define the cdx object")
		}
		cdxObj := obj.ToObject(vm)
		exports := cdxObj.Get("exports")
		if exports == nil || goja.IsUndefined(exports) {
			return fmt.Errorf("plugin did not set cdx.exports")
		}
		r.exports = exports.ToObject(vm)
		return nil
	})
	if err != nil {
		r.Close(0)
		return nil, err
	}

	if err := r.registerTools(); err != nil {
		r.Close(0)
		return nil, err
	}
	return r, nil
}

// loop serves VM commands on a single goroutine.
func (r *Runtime) loop() {
	defer close(r.done)
	for cmd := range r.commands {
		cmd.done <- cmd.fn(r.vm)
	}
}

// exec runs a function on the VM owner goroutine.
//
// A timeout interrupts the VM, otherwise a stuck script would hold the
// owner goroutine forever.
func (r *Runtime) exec(ctx context.Context, fn func(vm *goja.Runtime) error) error {
	cmd := command{fn: fn, done: make(chan error, 1)}

	select {
	case r.commands <- cmd:
	case <-r.done:
		return fmt.Errorf("plugin runtime is stopped")
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-cmd.done:
		return err
	case <-ctx.Done():
		r.vm.Interrupt(ctx.Err())
		return ctx.Err()
	case <-r.done:
		return fmt.Errorf("plugin runtime is stopped")
	}
}

// installBridge exposes the cdx object to the plugin.
func (r *Runtime) installBridge(opts RuntimeOptions) {
	vm := r.vm
	cdx := vm.NewObject()

	// request blocks: the VM is busy, but the bus and the core are free.
	callBridge := opts.CallBridge
	_ = cdx.Set("request", func(call goja.FunctionCall) goja.Value {
		topic := call.Argument(0).String()
		var payload any
		if arg := call.Argument(1); !goja.IsUndefined(arg) && !goja.IsNull(arg) {
			payload = arg.Export()
		}

		if !r.manifest.HasCapability(topic) {
			panic(vm.ToValue(fmt.Sprintf("permission denied: capability %q is not declared", topic)))
		}
		if callBridge == nil {
			panic(vm.ToValue("request bridge is not configured"))
		}

		ctx, cancel := context.WithTimeout(context.Background(), r.toolTimeout())
		defer cancel()

		value, err := callBridge(ctx, topic, payload)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(value)
	})

	publishBridge := opts.PublishBridge
	_ = cdx.Set("publish", func(call goja.FunctionCall) goja.Value {
		topic := call.Argument(0).String()
		var payload any
		if arg := call.Argument(1); !goja.IsUndefined(arg) && !goja.IsNull(arg) {
			payload = arg.Export()
		}

		// Publishing is limited to declared capabilities.
		if !r.manifest.HasCapability(topic) {
			panic(vm.ToValue(fmt.Sprintf("permission denied: capability %q is not declared", topic)))
		}
		if publishBridge != nil {
			if err := publishBridge(topic, payload); err != nil {
				panic(vm.ToValue(err.Error()))
			}
		}
		return goja.Undefined()
	})

	logFn := opts.Log
	logAt := func(level string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			message := call.Argument(0).String()
			if logFn != nil {
				logFn(level, message)
			}
			return goja.Undefined()
		}
	}

	_ = cdx.Set("log", logAt("info"))
	_ = cdx.Set("exports", vm.NewObject())

	// Files, shell and network namespaces: the model never sees them, a
	// plugin uses them like a standard library.
	if opts.API != nil {
		installCore(cdx, vm, opts.API)
	}
	_ = vm.Set("cdx", cdx)

	console := vm.NewObject()
	_ = console.Set("log", logAt("info"))
	_ = console.Set("info", logAt("info"))
	_ = console.Set("warn", logAt("warn"))
	_ = console.Set("error", logAt("error"))
	_ = vm.Set("console", console)
}

// registerTools registers the plugin tools on the bus.
func (r *Runtime) registerTools() error {
	for i := range r.manifest.Tools {
		spec := r.manifest.Tools[i]
		name := spec.Name

		// The function must be exported by the script.
		fnValue := r.exports.Get(name)
		if fnValue == nil || goja.IsUndefined(fnValue) {
			return fmt.Errorf("tool %q is declared in the manifest but not exported", name)
		}

		responder := r.toolResponder(name)
		topic := r.manifest.ToolID(name)
		if err := r.bus.Register(r.owner, topic, responder); err != nil {
			return err
		}
	}
	return nil
}

// toolResponder builds the handler for one tool call.
func (r *Runtime) toolResponder(name string) bus.Responder {
	return func(ctx context.Context, payload any) (any, error) {
		args := map[string]any{}
		if m, ok := payload.(map[string]any); ok {
			args = m
		}

		var out any
		err := r.exec(ctx, func(vm *goja.Runtime) error {
			fnValue := r.exports.Get(name)
			fn, ok := goja.AssertFunction(fnValue)
			if !ok {
				return fmt.Errorf("tool %q is not callable", name)
			}

			value, err := fn(goja.Undefined(), vm.ToValue(args))
			if err != nil {
				return err
			}
			if value == nil {
				return nil
			}
			out = value.Export()
			return nil
		})
		if err != nil {
			r.countCrash(err)
			return nil, err
		}
		return jsonSafe(out), nil
	}
}

// countCrash counts plugin failures.
func (r *Runtime) countCrash(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	r.crashes++
	crashes := r.crashes
	r.mu.Unlock()

	logx.Warn("[Plugin:%s] Tool call failed: %v", r.manifest.Name, err)
	_ = crashes
}

// Close stops the VM and removes the plugin registrations.
func (r *Runtime) Close(timeout time.Duration) {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		if r.stopped {
			r.mu.Unlock()
			return
		}
		r.stopped = true
		r.mu.Unlock()

		// One call drops every subscription and handler of this owner.
		if r.bus != nil {
			r.bus.Clear(r.owner)
		}

		close(r.commands)
		select {
		case <-r.done:
		case <-time.After(timeout):
			r.vm.Interrupt(fmt.Errorf("shutdown timeout"))
		}
	})
}

// toolTimeout returns the per-call timeout.
func (r *Runtime) toolTimeout() time.Duration {
	if r.manifest.ToolTimeoutMS > 0 {
		return time.Duration(r.manifest.ToolTimeoutMS) * time.Millisecond
	}
	return 60 * time.Second
}

// jsonSafe reduces a value to plain JSON-friendly data.
func jsonSafe(v any) any {
	switch value := v.(type) {
	case nil:
		return nil
	case string, bool, float64, int64, int:
		return value
	}

	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Sprint(v)
	}
	return out
}

// Manifest returns the plugin manifest.
func (r *Runtime) Manifest() *Manifest { return r.manifest }

// Name returns the plugin name.
func (r *Runtime) Name() string { return r.manifest.Name }

// Owner returns the bus owner name.
func (r *Runtime) Owner() string { return r.owner }
