package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/gin-gonic/gin"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/mcp"
	"github.com/yourok/cdx/internal/question"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/schema"
	"github.com/yourok/cdx/internal/store"
)

type vmCommand struct {
	fn      func(vm *goja.Runtime) (any, error)
	resp    chan vmResult
	started chan struct{}
	expired chan struct{}
}

type vmResult struct {
	val any
	err error
}

// ErrPluginBusy reports that the plugin VM did not start the call in time.
var ErrPluginBusy = errors.New("plugin is busy")

// vmBusyTimeout bounds how long a caller waits for a busy plugin VM. Without
// it an HTTP request would wait until the VM finishes whatever blocks it, for
// example a permission prompt answered in the terminal.
var vmBusyTimeout = 5 * time.Second

type runtimeMode int

const (
	modePrimary runtimeMode = iota
	modeWorker
)

const workerFunctionFlag = "__cdx_worker"

func unwrapWorkerFunction(v goja.Value) (goja.Callable, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, false
	}
	if obj, ok := v.(*goja.Object); ok {
		if marker := obj.Get(workerFunctionFlag); marker != nil && marker.ToBoolean() {
			fn, _ := goja.AssertFunction(obj.Get("fn"))
			return fn, true
		}
	}
	fn, _ := goja.AssertFunction(v)
	return fn, false
}

// RuntimeDependencies bundles the core subsystems exposed to a plugin VM.
type RuntimeDependencies struct {
	Store    *store.Store
	Bus      *bus.Bus
	Router   *router.Router
	Registry *Registry
	Plugins  *Manager
	Logger   *logx.Logger
	Agents   AgentRunner
	Sys      *SysAPI
	MCP      *mcp.Manager
}

// Runtime manages a single plugin running inside its own dedicated Goja VM goroutine.
type Runtime struct {
	manifest          *Manifest
	dir               string
	owner             string
	deps              RuntimeDependencies
	plainStore        *store.PlainNamespace
	secretStore       *store.SecretNamespace
	logScope          *logx.Scope
	settingsSchema    *schema.Schema
	settingsSchemaRaw map[string]any
	mode              runtimeMode

	activeCtx context.Context
	cmds      chan vmCommand
	done      chan struct{}
	closeOnce sync.Once

	workerMu       sync.Mutex
	workers        []*Runtime
	workerFree     chan *Runtime
	workersFailed  bool
	workerTools    map[string]goja.Callable
	workerComplete goja.Callable

	timerMu sync.Mutex
	timers  []*afterJob
}

// afterJob is a deferred callback scheduled with cdx.after.
type afterJob struct {
	timer *time.Timer
	done  chan struct{}
}

// StartRuntime initializes the Goja VM for a plugin, binds the cdx object, and runs its entry script.
func StartRuntime(ctx context.Context, dir string, m *Manifest, deps RuntimeDependencies) (*Runtime, error) {
	return startRuntime(ctx, dir, m, deps, modePrimary)
}

func startRuntime(ctx context.Context, dir string, m *Manifest, deps RuntimeDependencies, mode runtimeMode) (*Runtime, error) {
	if deps.Sys == nil {
		deps.Sys = NewSysAPI()
	}
	logger := deps.Logger
	if logger == nil {
		logger = logx.New()
	}

	var plainNS *store.PlainNamespace
	var secretNS *store.SecretNamespace
	var err error
	if deps.Store != nil {
		plainNS, err = deps.Store.PluginStorage(m.ID)
		if err != nil {
			return nil, fmt.Errorf("init plugin storage: %w", err)
		}
		secretNS, err = deps.Store.PluginSecrets(m.ID)
		if err != nil {
			return nil, fmt.Errorf("init plugin secrets: %w", err)
		}
	}

	rt := &Runtime{
		manifest:    m,
		dir:         dir,
		owner:       "plugin:" + m.ID,
		deps:        deps,
		plainStore:  plainNS,
		secretStore: secretNS,
		logScope:    logger.Tagged("Plugin:" + m.ID),
		mode:        mode,
		cmds:        make(chan vmCommand, 64),
		done:        make(chan struct{}),
	}

	go rt.loop()

	entryPath := filepath.Join(dir, m.Entry)
	codeBytes, err := os.ReadFile(entryPath)
	if err != nil {
		rt.Close()
		return nil, fmt.Errorf("read entry script: %w", err)
	}

	_, err = rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
		rt.bindCDX(vm)
		_, runErr := vm.RunScript(m.Entry, string(codeBytes))
		if runErr != nil {
			return nil, fmt.Errorf("run %s: %w", m.Entry, runErr)
		}
		if setupVal := vm.Get("setup"); setupVal != nil && !goja.IsUndefined(setupVal) {
			if setupFn, ok := goja.AssertFunction(setupVal); ok {
				if _, callErr := setupFn(goja.Undefined(), vm.Get("cdx")); callErr != nil {
					return nil, fmt.Errorf("setup() in %s: %w", m.Entry, callErr)
				}
			}
		}
		return nil, nil
	})
	if err != nil {
		rt.Close()
		return nil, err
	}

	return rt, nil
}

// Manifest returns the plugin's manifest.
func (rt *Runtime) Manifest() *Manifest {
	return rt.manifest
}

// Dir returns the plugin's directory on disk.
func (rt *Runtime) Dir() string {
	return rt.dir
}

// Close unregisters all plugin hooks, routes, tools, and stops the VM loop.
func (rt *Runtime) Close() {
	rt.closeOnce.Do(func() {
		rt.workerMu.Lock()
		workers := rt.workers
		rt.workers = nil
		rt.workerMu.Unlock()
		for _, worker := range workers {
			worker.Close()
		}

		if rt.deps.Bus != nil {
			rt.deps.Bus.UnregisterOwner(rt.owner)
		}
		if rt.deps.Router != nil {
			rt.deps.Router.UnregisterOwner(rt.owner)
		}
		if rt.deps.Registry != nil {
			rt.deps.Registry.UnregisterPlugin(rt.manifest.ID)
		}
		rt.timerMu.Lock()
		jobs := rt.timers
		rt.timers = nil
		rt.timerMu.Unlock()
		for _, job := range jobs {
			job.timer.Stop()
		}
		if rt.mode == modePrimary && rt.deps.MCP != nil {
			rt.deps.MCP.CloseOwner(rt.owner)
		}
		close(rt.done)
	})
}

func (rt *Runtime) poolEnabled() bool {
	return rt.mode == modePrimary && rt.manifest.Workers > 0
}

func (rt *Runtime) ensureWorkerPool(ctx context.Context) []*Runtime {
	if !rt.poolEnabled() {
		return nil
	}

	rt.workerMu.Lock()
	defer rt.workerMu.Unlock()

	if rt.workers != nil {
		return rt.workers
	}
	if rt.workersFailed {
		return nil
	}

	deps := rt.deps
	deps.Bus = nil
	deps.Router = nil
	deps.Registry = nil
	deps.Plugins = nil

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	workers := make([]*Runtime, 0, rt.manifest.Workers)
	for i := 0; i < rt.manifest.Workers; i++ {
		worker, err := startRuntime(startCtx, rt.dir, rt.manifest, deps, modeWorker)
		if err != nil {
			for _, started := range workers {
				started.Close()
			}
			rt.workersFailed = true
			rt.logScope.Warn("Failed to start worker VM %d: %v", i+1, err)
			return nil
		}
		workers = append(workers, worker)
	}
	rt.workerFree = make(chan *Runtime, len(workers))
	for _, worker := range workers {
		rt.workerFree <- worker
	}
	rt.workers = workers
	rt.logScope.Debug("Started %d worker VMs", len(workers))
	return workers
}

// errNoWorkers reports that the plugin has no usable worker VMs.
var errNoWorkers = errors.New("plugin has no worker VMs")

// acquireWorker takes a free worker VM, waiting for one when all are busy.
func (rt *Runtime) acquireWorker(ctx context.Context) (*Runtime, error) {
	workers := rt.ensureWorkerPool(ctx)
	if len(workers) == 0 {
		return nil, errNoWorkers
	}
	select {
	case <-rt.done:
		return nil, fmt.Errorf("plugin %q is stopped", rt.manifest.ID)
	default:
	}
	select {
	case worker := <-rt.workerFree:
		return worker, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-rt.done:
		return nil, fmt.Errorf("plugin %q is stopped", rt.manifest.ID)
	}
}

// releaseWorker returns a worker VM to the pool unless it has stopped.
func (rt *Runtime) releaseWorker(worker *Runtime) {
	if worker == nil {
		return
	}
	select {
	case <-worker.done:
		return
	default:
	}
	select {
	case rt.workerFree <- worker:
	default:
	}
}

func (rt *Runtime) runComplete(ctx context.Context, completeFn goja.Callable, req CompleteRequest, onStream func(StreamEvent)) (*CompleteResponse, error) {
	if completeFn == nil {
		return nil, fmt.Errorf("provider %q has no complete handler", rt.manifest.ID)
	}
	val, err := rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
		emitFn := func(ev map[string]any) {
			if onStream == nil {
				return
			}
			data, _ := json.Marshal(ev)
			var se StreamEvent
			if json.Unmarshal(data, &se) == nil {
				onStream(se)
			}
		}
		res, callErr := completeFn(goja.Undefined(), vm.ToValue(req), vm.ToValue(emitFn))
		if callErr != nil {
			return nil, callErr
		}
		return res.Export(), nil
	})
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(val)
	if err != nil {
		return nil, err
	}
	var resp CompleteResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (rt *Runtime) completeInWorker(ctx context.Context, req CompleteRequest, onStream func(StreamEvent)) (*CompleteResponse, error, bool) {
	worker, err := rt.acquireWorker(ctx)
	if err != nil {
		if errors.Is(err, errNoWorkers) {
			return nil, nil, false
		}
		return nil, err, true
	}
	defer rt.releaseWorker(worker)
	if worker.workerComplete == nil {
		return nil, nil, false
	}
	resp, err := worker.runComplete(ctx, worker.workerComplete, req, onStream)
	return resp, err, true
}

func (rt *Runtime) runTool(ctx context.Context, fn goja.Callable, args map[string]any) (any, error) {
	return rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
		res, callErr := fn(goja.Undefined(), vm.ToValue(args))
		if callErr != nil {
			return nil, callErr
		}
		return res.Export(), nil
	})
}

func (rt *Runtime) toolInWorker(ctx context.Context, name string, args map[string]any) (any, error, bool) {
	worker, err := rt.acquireWorker(ctx)
	if err != nil {
		if errors.Is(err, errNoWorkers) {
			return nil, nil, false
		}
		return nil, err, true
	}
	defer rt.releaseWorker(worker)
	fn := worker.workerTools[name]
	if fn == nil {
		return nil, nil, false
	}
	res, err := worker.runTool(ctx, fn, args)
	return res, err, true
}

func (rt *Runtime) loop() {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	for {
		select {
		case <-rt.done:
			return
		case cmd := <-rt.cmds:
			select {
			case <-cmd.expired:
				cmd.resp <- vmResult{err: ErrPluginBusy}
				continue
			default:
			}
			if cmd.started != nil {
				close(cmd.started)
			}
			val, err := safeRun(vm, cmd.fn)
			cmd.resp <- vmResult{val: val, err: err}
		}
	}
}

func safeRun(vm *goja.Runtime, fn func(vm *goja.Runtime) (any, error)) (val any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("plugin vm panic: %v", r)
		}
	}()
	return fn(vm)
}

func (rt *Runtime) execOnVM(ctx context.Context, fn func(vm *goja.Runtime) (any, error)) (any, error) {
	resp := make(chan vmResult, 1)
	started := make(chan struct{})
	expired := make(chan struct{})
	wrapped := func(vm *goja.Runtime) (any, error) {
		prev := rt.activeCtx
		rt.activeCtx = ctx
		defer func() { rt.activeCtx = prev }()
		return fn(vm)
	}
	select {
	case <-rt.done:
		return nil, fmt.Errorf("plugin %q is stopped", rt.manifest.ID)
	case <-ctx.Done():
		return nil, ctx.Err()
	case rt.cmds <- vmCommand{fn: wrapped, resp: resp, started: started, expired: expired}:
	}

	timer := time.NewTimer(vmBusyTimeout)
	defer timer.Stop()

	select {
	case <-rt.done:
		return nil, fmt.Errorf("plugin %q stopped during call", rt.manifest.ID)
	case <-ctx.Done():
		close(expired)
		return nil, ctx.Err()
	case res := <-resp:
		return res.val, res.err
	case <-started:
		// The call is running: wait for it without a queue deadline.
		select {
		case <-rt.done:
			return nil, fmt.Errorf("plugin %q stopped during call", rt.manifest.ID)
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-resp:
			return res.val, res.err
		}
	case <-timer.C:
		close(expired)
		return nil, ErrPluginBusy
	}
}

// scheduleAfter runs a callback on the plugin control VM after the delay.
func (rt *Runtime) scheduleAfter(ms int, fn goja.Callable) bool {
	if rt.mode != modePrimary || fn == nil {
		return false
	}
	delay := time.Duration(ms) * time.Millisecond
	if delay < 0 {
		delay = 0
	}
	if delay > time.Hour {
		delay = time.Hour
	}

	job := &afterJob{done: make(chan struct{})}
	job.timer = time.AfterFunc(delay, func() {
		defer close(job.done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
			_, callErr := fn(goja.Undefined())
			return nil, callErr
		})
		if err != nil {
			rt.logScope.Debug("Deferred callback failed: %v", err)
		}
	})

	rt.timerMu.Lock()
	kept := rt.timers[:0]
	for _, existing := range rt.timers {
		select {
		case <-existing.done:
		default:
			kept = append(kept, existing)
		}
	}
	rt.timers = append(kept, job)
	rt.timerMu.Unlock()
	return true
}

func (rt *Runtime) bindCDX(vm *goja.Runtime) {
	cdxObj := vm.NewObject()
	_ = cdxObj.Set("id", rt.manifest.ID)
	_ = cdxObj.Set("dir", rt.dir)
	_ = cdxObj.Set("worker", rt.mode == modeWorker)
	_ = cdxObj.Set("workdir", func() string { return WorkdirFromContext(rt.currentCtx()) })
	_ = cdxObj.Set("inWorker", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("cdx.inWorker requires a function"))
		}
		if _, ok := goja.AssertFunction(call.Arguments[0]); !ok {
			panic(vm.NewTypeError("cdx.inWorker requires a function"))
		}
		obj := vm.NewObject()
		_ = obj.Set(workerFunctionFlag, true)
		_ = obj.Set("fn", call.Arguments[0])
		return obj
	})
	_ = cdxObj.Set("after", func(ms int, fn goja.Callable) bool { return rt.scheduleAfter(ms, fn) })
	_ = cdxObj.Set("apiDoc", RenderAPIDoc)
	_ = cdxObj.Set("log", rt.buildLogObject(vm))
	_ = cdxObj.Set("storage", rt.buildStorageObject(vm))
	_ = cdxObj.Set("secrets", rt.buildSecretsObject(vm))
	_ = cdxObj.Set("settings", rt.buildSettingsObject(vm))
	_ = cdxObj.Set("bus", rt.buildBusObject(vm))
	_ = cdxObj.Set("hooks", rt.buildHooksObject(vm))
	_ = cdxObj.Set("questions", rt.buildQuestionsObject(vm))
	_ = cdxObj.Set("agents", rt.buildAgentsObject(vm))
	_ = cdxObj.Set("http", rt.buildHTTPObject(vm, ""))
	_ = cdxObj.Set("ui", rt.buildUIObject(vm))
	_ = cdxObj.Set("tools", rt.buildToolsObject(vm))
	_ = cdxObj.Set("providers", rt.buildProvidersObject(vm))
	_ = cdxObj.Set("plugins", rt.buildPluginsObject(vm))
	_ = cdxObj.Set("mcp", rt.buildMCPObject(vm))
	_ = cdxObj.Set("sys", rt.buildSysObject(vm))

	_ = vm.Set("cdx", cdxObj)
}

func (rt *Runtime) buildLogObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("debug", func(msg string) { rt.logScope.Debug("%s", msg) })
	_ = obj.Set("info", func(msg string) { rt.logScope.Info("%s", msg) })
	_ = obj.Set("warn", func(msg string) { rt.logScope.Warn("%s", msg) })
	_ = obj.Set("error", func(msg string) { rt.logScope.Error("%s", msg) })
	return obj
}

func (rt *Runtime) buildStorageObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("get", func(key string) any {
		if rt.plainStore == nil {
			return nil
		}
		val, found, err := rt.plainStore.GetAny(key)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if !found {
			return nil
		}
		return val
	})
	_ = obj.Set("set", func(key string, val any) {
		if rt.plainStore == nil {
			return
		}
		if err := rt.plainStore.Set(key, val); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = obj.Set("delete", func(key string) {
		if rt.plainStore == nil {
			return
		}
		if err := rt.plainStore.Delete(key); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = obj.Set("keys", func() []string {
		if rt.plainStore == nil {
			return nil
		}
		keys, err := rt.plainStore.Keys()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return keys
	})
	_ = obj.Set("list", func() map[string]any {
		if rt.plainStore == nil {
			return map[string]any{}
		}
		all, err := rt.plainStore.List()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return all
	})
	return obj
}

func (rt *Runtime) buildSecretsObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("get", func(key string) any {
		if rt.secretStore == nil {
			return nil
		}
		val, found, err := rt.secretStore.Get(key)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if !found {
			return nil
		}
		return val
	})
	_ = obj.Set("set", func(key, val string) {
		if rt.secretStore == nil {
			return
		}
		if err := rt.secretStore.Set(key, val); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = obj.Set("delete", func(key string) {
		if rt.secretStore == nil {
			return
		}
		if err := rt.secretStore.Delete(key); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = obj.Set("has", func(key string) bool {
		if rt.secretStore == nil {
			return false
		}
		has, err := rt.secretStore.Has(key)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return has
	})
	_ = obj.Set("keys", func() []string {
		if rt.secretStore == nil {
			return nil
		}
		keys, err := rt.secretStore.Keys()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return keys
	})
	_ = obj.Set("masked", func() map[string]map[string]bool {
		if rt.secretStore == nil {
			return map[string]map[string]bool{}
		}
		m, err := rt.secretStore.MaskedMap()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return m
	})
	return obj
}

func (rt *Runtime) setSettingsSchema(raw map[string]any) error {
	parsed, err := schema.Parse(raw)
	if err != nil {
		return err
	}
	rt.settingsSchema = parsed
	rt.settingsSchemaRaw = raw
	return nil
}

// SettingsSnapshot returns the registered settings schema, current values, and secret flags.
func (rt *Runtime) SettingsSnapshot() (map[string]any, map[string]any, map[string]bool, bool) {
	if rt.settingsSchema == nil || rt.settingsSchemaRaw == nil {
		return nil, nil, nil, false
	}
	values := map[string]any{}
	secrets := map[string]bool{}
	for name, prop := range rt.settingsSchema.Properties {
		if prop == nil {
			continue
		}
		if prop.Format == schema.FormatSecret {
			if rt.secretStore != nil {
				has, err := rt.secretStore.Has(name)
				if err == nil && has {
					secrets[name] = true
				}
			}
			continue
		}
		if rt.plainStore != nil {
			val, found, err := rt.plainStore.GetAny(name)
			if err == nil && found {
				values[name] = val
				continue
			}
		}
		if prop.Default != nil {
			values[name] = prop.Default
		}
	}
	return rt.settingsSchemaRaw, values, secrets, true
}

// SaveSettings validates and persists declarative plugin settings.
func (rt *Runtime) SaveSettings(values map[string]any) (map[string]any, error) {
	if rt.settingsSchema == nil {
		return nil, fmt.Errorf("settings schema is not registered")
	}
	validated, err := rt.settingsSchema.Validate(values)
	if err != nil {
		return nil, err
	}
	for _, name := range rt.settingsSchema.SecretFields() {
		if str, ok := validated[name].(string); ok && str == "" {
			delete(validated, name)
		}
	}
	plain, secrets := rt.settingsSchema.SplitSecrets(validated)
	if rt.plainStore != nil {
		for k, v := range plain {
			if err := rt.plainStore.Set(k, v); err != nil {
				return nil, err
			}
		}
	}
	if rt.secretStore != nil {
		for k, v := range secrets {
			if err := rt.secretStore.Set(k, v); err != nil {
				return nil, err
			}
		}
	}
	return plain, nil
}

func (rt *Runtime) buildSettingsObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("register", func(rawSchema map[string]any) {
		if err := rt.setSettingsSchema(rawSchema); err != nil {
			panic(vm.NewGoError(err))
		}
		if rt.deps.Router != nil {
			rt.deps.Router.RegisterUI(router.UIDescriptor{
				PluginID:    rt.manifest.ID,
				Title:       rt.manifest.Name,
				Description: rt.manifest.Description,
				Order:       rt.manifest.Order,
				Schema:      rawSchema,
			})
		}
	})
	_ = obj.Set("save", func(values map[string]any) map[string]any {
		plain, err := rt.SaveSettings(values)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return plain
	})
	return obj
}

func (rt *Runtime) buildBusObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("publish", func(topic string, payload any) int {
		if rt.deps.Bus == nil {
			return 0
		}
		return rt.deps.Bus.Publish(rt.currentCtx(), topic, payload)
	})
	_ = obj.Set("subscribe", func(topic string, fn goja.Callable) {
		if rt.deps.Bus == nil {
			return
		}
		rt.deps.Bus.Subscribe(rt.owner, topic, func(ctx context.Context, payload any) {
			_, _ = rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
				_, err := fn(goja.Undefined(), vm.ToValue(payload))
				return nil, err
			})
		})
	})
	_ = obj.Set("handle", func(topic string, fn goja.Callable) {
		if rt.deps.Bus == nil {
			return
		}
		rt.deps.Bus.Handle(rt.owner, topic, func(ctx context.Context, payload any) (any, error) {
			return rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
				res, err := fn(goja.Undefined(), vm.ToValue(payload))
				if err != nil {
					return nil, err
				}
				return res.Export(), nil
			})
		})
	})
	_ = obj.Set("request", func(topic string, payload any) any {
		if rt.deps.Bus == nil {
			return nil
		}
		res, err := rt.deps.Bus.Request(rt.currentCtx(), rt.owner, topic, payload)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return res
	})
	return obj
}

func (rt *Runtime) currentCtx() context.Context {
	if rt.activeCtx != nil {
		return rt.activeCtx
	}
	return context.Background()
}

func (rt *Runtime) buildHooksObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("on", func(hookName string, priority int, fn goja.Callable) {
		if rt.deps.Bus == nil {
			return
		}
		rt.deps.Bus.OnHook(rt.owner, hookName, priority, func(ctx context.Context, payload map[string]any) (map[string]any, error) {
			val, err := rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
				res, callErr := fn(goja.Undefined(), vm.ToValue(payload))
				if callErr != nil {
					return nil, callErr
				}
				if res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
					return payload, nil
				}
				return res.Export(), nil
			})
			if err != nil {
				return nil, err
			}
			if m, ok := val.(map[string]any); ok {
				return m, nil
			}
			return payload, nil
		})
	})
	_ = obj.Set("run", func(hookName string, payload map[string]any) map[string]any {
		if rt.deps.Bus == nil {
			return payload
		}
		out, err := rt.deps.Bus.RunHook(context.Background(), hookName, payload)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return out
	})
	return obj
}

// buildQuestionsObject exposes the client question broker to plugins, including worker VMs.
func (rt *Runtime) buildQuestionsObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("ask", func(raw map[string]any) map[string]any {
		data, err := json.Marshal(raw)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		var q question.Question
		if err := json.Unmarshal(data, &q); err != nil {
			panic(vm.NewGoError(fmt.Errorf("invalid question: %w", err)))
		}
		// The host stamps the source and tag, so a question cannot claim another origin.
		q.Source = "plugin:" + rt.manifest.ID
		q.Tag = rt.manifest.ID
		if strings.TrimSpace(q.Kind) == "" {
			q.Kind = "custom"
		}
		answer, err := question.Ask(rt.currentCtx(), q)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return map[string]any{
			"choice":    answer.Choice,
			"text":      answer.Text,
			"cancelled": answer.Cancelled,
			"reason":    answer.Reason,
		}
	})
	return obj
}

// buildAgentsObject exposes delegated turns to plugins, including worker VMs.
func (rt *Runtime) buildAgentsObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("run", func(raw map[string]any) map[string]any {
		if rt.deps.Agents == nil {
			panic(vm.NewGoError(errors.New("subagents are not available")))
		}
		prompt, _ := raw["prompt"].(string)
		if strings.TrimSpace(prompt) == "" {
			panic(vm.NewGoError(errors.New("prompt is required")))
		}
		req := AgentRequest{Prompt: prompt}
		if value, ok := raw["preset"].(string); ok {
			req.Preset = value
		}
		if value, ok := raw["model"].(string); ok {
			req.Model = value
		}
		req.MaxSteps = agentMaxSteps(raw["max_steps"])

		result, err := rt.deps.Agents.RunChild(rt.currentCtx(), req)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return map[string]any{
			"session_id":     result.SessionID,
			"content":        result.Content,
			"steps":          result.Steps,
			"stopped_reason": result.StoppedReason,
			"usage":          result.Usage,
		}
	})
	_ = obj.Set("runMany", func(raw map[string]any) map[string]any {
		if rt.deps.Agents == nil {
			panic(vm.NewGoError(errors.New("subagents are not available")))
		}
		items, _ := raw["prompts"].([]any)
		preset, _ := raw["preset"].(string)
		model, _ := raw["model"].(string)
		maxSteps := agentMaxSteps(raw["max_steps"])
		reqs := make([]AgentRequest, 0, len(items))
		for _, item := range items {
			prompt, _ := item.(string)
			if strings.TrimSpace(prompt) == "" {
				continue
			}
			reqs = append(reqs, AgentRequest{Prompt: prompt, Preset: preset, Model: model, MaxSteps: maxSteps})
		}
		if len(reqs) == 0 {
			panic(vm.NewGoError(errors.New("prompts are required")))
		}
		if len(reqs) > MaxAgentTasks {
			panic(vm.NewGoError(fmt.Errorf("at most %d subagent tasks are allowed", MaxAgentTasks)))
		}

		results, err := rt.deps.Agents.RunChildren(rt.currentCtx(), reqs)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		out := make([]map[string]any, 0, len(results))
		for _, result := range results {
			out = append(out, map[string]any{
				"session_id":     result.SessionID,
				"content":        result.Content,
				"steps":          result.Steps,
				"stopped_reason": result.StoppedReason,
				"error":          result.Error,
			})
		}
		return map[string]any{"results": out}
	})
	return obj
}

// agentMaxSteps reads the optional max_steps value passed from JavaScript.
func agentMaxSteps(value any) int {
	switch v := value.(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func (rt *Runtime) buildHTTPObject(vm *goja.Runtime, groupPrefix string) *goja.Object {
	obj := vm.NewObject()

	joinPath := func(sub string) string {
		if groupPrefix == "" {
			return sub
		}
		return strings.TrimSuffix(groupPrefix, "/") + "/" + strings.TrimPrefix(sub, "/")
	}

	registerMethod := func(method, pattern string, fn goja.Callable) {
		if rt.deps.Router == nil {
			return
		}
		fullPattern := joinPath(pattern)
		rt.deps.Router.Handle(rt.owner, method, fullPattern, func(c *gin.Context) {
			_, err := rt.execOnVM(c.Request.Context(), func(vm *goja.Runtime) (any, error) {
				ctxObj := rt.buildGinContextObject(vm, c)
				_, callErr := fn(goja.Undefined(), ctxObj)
				return nil, callErr
			})
			if err != nil && !c.Writer.Written() {
				writePluginError(c, rt.manifest.ID, err)
			}
		})
	}

	_ = obj.Set("handle", registerMethod)
	_ = obj.Set("get", func(p string, fn goja.Callable) { registerMethod("GET", p, fn) })
	_ = obj.Set("post", func(p string, fn goja.Callable) { registerMethod("POST", p, fn) })
	_ = obj.Set("put", func(p string, fn goja.Callable) { registerMethod("PUT", p, fn) })
	_ = obj.Set("delete", func(p string, fn goja.Callable) { registerMethod("DELETE", p, fn) })
	_ = obj.Set("patch", func(p string, fn goja.Callable) { registerMethod("PATCH", p, fn) })

	_ = obj.Set("use", func(call goja.FunctionCall) goja.Value {
		if rt.deps.Router == nil || len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		prefix := groupPrefix
		var fn goja.Callable
		if len(call.Arguments) == 1 {
			fn, _ = goja.AssertFunction(call.Arguments[0])
		} else {
			prefix = joinPath(call.Arguments[0].String())
			fn, _ = goja.AssertFunction(call.Arguments[1])
		}
		if fn == nil {
			panic(vm.NewTypeError("middleware handler must be a function"))
		}
		rt.deps.Router.Use(rt.owner, prefix, func(c *gin.Context) {
			_, err := rt.execOnVM(c.Request.Context(), func(vm *goja.Runtime) (any, error) {
				ctxObj := rt.buildGinContextObject(vm, c)
				_ = ctxObj.Set("next", func() {
					// Deferred until the VM command finishes so downstream handlers in the same plugin can acquire the VM without deadlock.
				})
				_, callErr := fn(goja.Undefined(), ctxObj)
				return nil, callErr
			})
			if err != nil && !c.Writer.Written() {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": pluginErrorMessage(rt.manifest.ID, err)})
				return
			}
			if !c.IsAborted() {
				c.Next()
			}
		})
		return goja.Undefined()
	})

	_ = obj.Set("group", func(prefix string) *goja.Object {
		return rt.buildHTTPObject(vm, joinPath(prefix))
	})

	_ = obj.Set("static", func(prefix, relDir string) {
		if rt.deps.Router == nil {
			return
		}
		target := relDir
		if !filepath.IsAbs(target) {
			target = filepath.Join(rt.dir, relDir)
		}
		rt.deps.Router.Static(rt.owner, joinPath(prefix), target)
	})

	return obj
}

// writePluginError answers a failed plugin handler; a busy VM is a retryable 503.
func writePluginError(c *gin.Context, pluginID string, err error) {
	if errors.Is(err, ErrPluginBusy) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": pluginErrorMessage(pluginID, err)})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": pluginErrorMessage(pluginID, err)})
}

func pluginErrorMessage(pluginID string, err error) string {
	if errors.Is(err, ErrPluginBusy) {
		return fmt.Sprintf("plugin %q is busy", pluginID)
	}
	return err.Error()
}

func (rt *Runtime) buildGinContextObject(vm *goja.Runtime, c *gin.Context) *goja.Object {
	ctxObj := vm.NewObject()
	_ = ctxObj.Set("method", c.Request.Method)
	_ = ctxObj.Set("path", c.Request.URL.Path)
	_ = ctxObj.Set("param", func(name string) string { return c.Param(name) })
	_ = ctxObj.Set("query", func(name string) string { return c.Query(name) })
	_ = ctxObj.Set("getHeader", func(name string) string { return c.GetHeader(name) })
	_ = ctxObj.Set("host", func() string { return c.Request.Host })
	_ = ctxObj.Set("header", func(k, v string) { c.Header(k, v) })
	_ = ctxObj.Set("status", func(code int) { c.Status(code) })
	_ = ctxObj.Set("set", func(k string, v any) { c.Set(k, v) })
	_ = ctxObj.Set("get", func(k string) any {
		val, ok := c.Get(k)
		if !ok {
			return nil
		}
		return val
	})
	_ = ctxObj.Set("body", func() string {
		data, _ := io.ReadAll(c.Request.Body)
		return string(data)
	})
	_ = ctxObj.Set("bindJSON", func() any {
		data, err := io.ReadAll(c.Request.Body)
		if err != nil || len(data) == 0 {
			return map[string]any{}
		}
		var out any
		if err := json.Unmarshal(data, &out); err != nil {
			panic(vm.NewGoError(err))
		}
		return out
	})
	_ = ctxObj.Set("json", func(code int, v any) {
		c.JSON(code, v)
	})
	_ = ctxObj.Set("string", func(code int, s string) {
		c.String(code, "%s", s)
	})
	_ = ctxObj.Set("html", func(code int, htmlStr string) {
		c.Data(code, "text/html; charset=utf-8", []byte(htmlStr))
	})
	_ = ctxObj.Set("abortWithJSON", func(code int, v any) {
		c.AbortWithStatusJSON(code, v)
	})
	_ = ctxObj.Set("next", func() {
		c.Next()
	})
	return ctxObj
}

func (rt *Runtime) buildUIObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("register", func(spec map[string]any) {
		if rt.deps.Router == nil {
			return
		}
		desc := router.UIDescriptor{
			PluginID:    rt.manifest.ID,
			Title:       rt.manifest.Name,
			Description: rt.manifest.Description,
			Order:       rt.manifest.Order,
		}
		if t, ok := spec["title"].(string); ok && t != "" {
			desc.Title = t
		}
		if d, ok := spec["description"].(string); ok && d != "" {
			desc.Description = d
		}
		if ic, ok := spec["icon"].(string); ok {
			desc.Icon = ic
		}
		if ord, ok := spec["order"].(int64); ok {
			desc.Order = int(ord)
		} else if ordF, ok := spec["order"].(float64); ok {
			desc.Order = int(ordF)
		}
		if entry, ok := spec["entry"].(string); ok {
			desc.Entry = entry
			desc.UIApi = 1
		}
		if api, ok := spec["ui_api"].(int64); ok && api > 0 {
			desc.UIApi = int(api)
		} else if apiF, ok := spec["ui_api"].(float64); ok && apiF > 0 {
			desc.UIApi = int(apiF)
		}
		if configURL, ok := spec["config_url"].(string); ok {
			desc.ConfigURL = configURL
		}
		if styles, ok := spec["styles"].([]any); ok {
			for _, item := range styles {
				if style, ok := item.(string); ok && style != "" {
					desc.Styles = append(desc.Styles, style)
				}
			}
		}
		if sch, ok := spec["schema"].(map[string]any); ok {
			if err := rt.setSettingsSchema(sch); err != nil {
				panic(vm.NewGoError(err))
			}
			desc.Schema = sch
		}
		rt.deps.Router.RegisterUI(desc)
	})
	_ = obj.Set("list", func() []router.UIDescriptor {
		if rt.deps.Router == nil {
			return nil
		}
		return rt.deps.Router.ListUI()
	})
	return obj
}

func (rt *Runtime) buildToolsObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("register", func(specMap map[string]any, handler goja.Value) {
		name, _ := specMap["name"].(string)
		fn, runInWorker := unwrapWorkerFunction(handler)
		if fn == nil {
			panic(vm.NewTypeError("tool handler must be a function"))
		}
		if rt.mode == modeWorker {
			if name == "" {
				return
			}
			if rt.workerTools == nil {
				rt.workerTools = map[string]goja.Callable{}
			}
			rt.workerTools[name] = fn
			return
		}
		if rt.deps.Registry == nil {
			return
		}
		if runInWorker && rt.manifest.Workers == 0 {
			rt.logScope.Warn("Tool %s is marked with cdx.inWorker but workers are disabled; using the control VM", name)
		}
		desc, _ := specMap["description"].(string)
		perm, _ := specMap["permission"].(string)
		inSchema, _ := specMap["input_schema"].(map[string]any)
		if inSchema == nil {
			inSchema, _ = specMap["schema"].(map[string]any)
		}

		err := rt.deps.Registry.RegisterTool(ToolSpec{
			PluginID:    rt.manifest.ID,
			Name:        name,
			Description: desc,
			Permission:  perm,
			InputSchema: inSchema,
			handler: func(ctx context.Context, args map[string]any) (any, error) {
				if runInWorker && rt.poolEnabled() {
					if res, err, ok := rt.toolInWorker(ctx, name, args); ok {
						return res, err
					}
				}
				return rt.runTool(ctx, fn, args)
			},
		})
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = obj.Set("remove", func(name string) bool {
		if rt.mode == modeWorker || rt.deps.Registry == nil {
			return false
		}
		return rt.deps.Registry.RemoveTool(name)
	})
	_ = obj.Set("list", func() []ToolSpec {
		if rt.deps.Registry == nil {
			return nil
		}
		return rt.deps.Registry.ListTools()
	})
	return obj
}

func (rt *Runtime) buildProvidersObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("register", func(specObj *goja.Object) {
		if specObj == nil {
			return
		}
		listFn, listInWorker := unwrapWorkerFunction(specObj.Get("listModels"))
		completeFn, completeInWorker := unwrapWorkerFunction(specObj.Get("complete"))
		if rt.mode == modeWorker {
			rt.workerComplete = completeFn
			return
		}
		if rt.deps.Registry == nil {
			return
		}
		if listInWorker {
			rt.logScope.Warn("Provider listModels is marked with cdx.inWorker but always runs in the control VM")
		}
		if completeInWorker && rt.manifest.Workers == 0 {
			rt.logScope.Warn("Provider complete is marked with cdx.inWorker but workers are disabled; using the control VM")
		}

		rt.deps.Registry.RegisterProvider(ProviderEntry{
			PluginID: rt.manifest.ID,
			ListModels: func(ctx context.Context) ([]ModelInfo, error) {
				if listFn == nil {
					return nil, nil
				}
				val, err := rt.execOnVM(ctx, func(vm *goja.Runtime) (any, error) {
					res, callErr := listFn(goja.Undefined())
					if callErr != nil {
						return nil, callErr
					}
					return res.Export(), nil
				})
				if err != nil {
					return nil, err
				}
				data, err := json.Marshal(val)
				if err != nil {
					return nil, err
				}
				var models []ModelInfo
				if err := json.Unmarshal(data, &models); err != nil {
					return nil, err
				}
				return models, nil
			},
			Complete: func(ctx context.Context, req CompleteRequest, onStream func(StreamEvent)) (*CompleteResponse, error) {
				if completeInWorker && rt.poolEnabled() {
					if resp, err, ok := rt.completeInWorker(ctx, req, onStream); ok {
						return resp, err
					}
				}
				return rt.runComplete(ctx, completeFn, req, onStream)
			},
		})
	})
	_ = obj.Set("listModels", func() []ModelInfo {
		if rt.deps.Registry == nil {
			return nil
		}
		m, _ := rt.deps.Registry.ListModels(context.Background())
		return m
	})
	return obj
}

func (rt *Runtime) buildPluginsObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("list", func() []PluginInfo {
		if rt.deps.Plugins == nil {
			return nil
		}
		return rt.deps.Plugins.ListDetailed()
	})
	_ = obj.Set("installZip", func(url string) *PluginInfo {
		if rt.deps.Plugins == nil {
			panic(vm.NewGoError(fmt.Errorf("plugin manager is not available")))
		}
		info, err := rt.deps.Plugins.InstallFromURL(rt.currentCtx(), url, rt.deps.Plugins.UserPluginDir())
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return info
	})
	_ = obj.Set("setEnabled", func(id string, enabled bool) *PluginInfo {
		if rt.deps.Plugins == nil {
			panic(vm.NewGoError(fmt.Errorf("plugin manager is not available")))
		}
		info, err := rt.deps.Plugins.SetEnabled(rt.currentCtx(), id, enabled)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return info
	})
	_ = obj.Set("remove", func(id string) {
		if rt.deps.Plugins == nil {
			panic(vm.NewGoError(fmt.Errorf("plugin manager is not available")))
		}
		if err := rt.deps.Plugins.RemovePlugin(id); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	return obj
}

func (rt *Runtime) buildSysObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("readFile", func(path string) string {
		s, err := rt.deps.Sys.ReadFile(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return s
	})
	_ = obj.Set("writeFile", func(path, content string) {
		if err := rt.deps.Sys.WriteFile(path, content); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = obj.Set("stat", func(path string) map[string]any {
		info, err := rt.deps.Sys.Stat(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return info
	})
	_ = obj.Set("readDir", func(path string) []map[string]any {
		list, err := rt.deps.Sys.ReadDir(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return list
	})
	_ = obj.Set("random", func(n int) string {
		hex, err := rt.deps.Sys.RandomHex(n)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return hex
	})
	_ = obj.Set("env", func(name string) string {
		return rt.deps.Sys.Env(name)
	})
	_ = obj.Set("exec", func(command string, workdir string, timeoutMS int) map[string]any {
		ctx := rt.currentCtx()
		if workdir == "" {
			workdir = WorkdirFromContext(ctx)
		}
		res, err := rt.deps.Sys.Exec(ctx, command, workdir, timeoutMS)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return res
	})
	_ = obj.Set("fetch", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("fetch requires a url"))
		}
		url := call.Arguments[0].String()
		var opts map[string]any
		if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
			opts, _ = call.Arguments[1].Export().(map[string]any)
		}
		if opts == nil {
			opts = map[string]any{}
		}
		var onLine func(string) error
		if len(call.Arguments) > 2 {
			if fn, ok := goja.AssertFunction(call.Arguments[2]); ok {
				onLine = func(line string) error {
					_, err := fn(goja.Undefined(), vm.ToValue(line))
					return err
				}
			}
		}
		res, err := rt.deps.Sys.Fetch(rt.currentCtx(), url, opts, onLine)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(res)
	})
	return obj
}
