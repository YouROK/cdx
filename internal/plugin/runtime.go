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

// RuntimeDependencies bundles the core subsystems exposed to a plugin VM.
type RuntimeDependencies struct {
	Store    *store.Store
	Bus      *bus.Bus
	Router   *router.Router
	Registry *Registry
	Plugins  *Manager
	Logger   *logx.Logger
	Agents   AgentRunner
	Tools    ToolRunner
	Tasks    TaskRunner
	Sys      *SysAPI
	MCP      *mcp.Manager
	Async    *AsyncLimiter
	// AsyncExec bounds concurrent shell processes.
	AsyncExec *AsyncLimiter
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

	activeCtx context.Context
	cmds      chan vmCommand
	jobs      chan func(*goja.Runtime)
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once

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

	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	rt := &Runtime{
		manifest:    m,
		dir:         dir,
		owner:       "plugin:" + m.ID,
		deps:        deps,
		plainStore:  plainNS,
		secretStore: secretNS,
		logScope:    logger.Tagged("Plugin:" + m.ID),
		cmds:        make(chan vmCommand, 64),
		jobs:        make(chan func(*goja.Runtime), 64),
		ctx:         runtimeCtx,
		cancel:      runtimeCancel,
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
				res, callErr := setupFn(goja.Undefined(), vm.Get("cdx"))
				if callErr != nil {
					return nil, fmt.Errorf("setup() in %s: %w", m.Entry, callErr)
				}
				if res != nil {
					if _, ok := res.Export().(*goja.Promise); ok {
						return nil, fmt.Errorf("setup() in %s must be synchronous; start background work with an async function", m.Entry)
					}
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
		rt.cancel()
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
		if rt.deps.MCP != nil {
			rt.deps.MCP.CloseOwner(rt.owner)
		}
		close(rt.done)
	})
}

func (rt *Runtime) runComplete(ctx context.Context, completeFn goja.Callable, req CompleteRequest, onStream func(StreamEvent)) (*CompleteResponse, error) {
	if completeFn == nil {
		return nil, fmt.Errorf("provider %q has no complete handler", rt.manifest.ID)
	}
	val, err := rt.callResult(ctx, func(vm *goja.Runtime) (goja.Value, error) {
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
		return completeFn(goja.Undefined(), vm.ToValue(req), vm.ToValue(emitFn))
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

func (rt *Runtime) runTool(ctx context.Context, fn goja.Callable, args map[string]any) (any, error) {
	return rt.callResult(ctx, func(vm *goja.Runtime) (goja.Value, error) {
		return fn(goja.Undefined(), vm.ToValue(args))
	})
}

func (rt *Runtime) loop() {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	vm.SetAsyncContextTracker(&ctxTracker{rt: rt})
	vm.SetPromiseRejectionTracker(func(p *goja.Promise, op goja.PromiseRejectionOperation) {
		if op == goja.PromiseRejectionReject {
			rt.logScope.Warn("Unhandled promise rejection: %v", p.Result())
		}
	})

	for {
		select {
		case <-rt.done:
			return
		case job := <-rt.jobs:
			_, err := safeRun(vm, func(vm *goja.Runtime) (any, error) {
				job(vm)
				return nil, nil
			})
			if err != nil {
				rt.logScope.Warn("Async job failed: %v", err)
			}
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

// scheduleAfter runs a callback on the plugin VM after the delay.
func (rt *Runtime) scheduleAfter(ms int, fn goja.Callable) bool {
	if fn == nil {
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
		ctx, cancel := context.WithTimeout(rt.ctx, 2*time.Minute)
		defer cancel()
		if _, err := rt.callResult(ctx, func(vm *goja.Runtime) (goja.Value, error) {
			return fn(goja.Undefined())
		}); err != nil {
			rt.logScope.Warn("Deferred callback failed: %v", err)
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
	_ = cdxObj.Set("workdir", func() string { return WorkdirFromContext(rt.currentCtx()) })
	_ = cdxObj.Set("after", func(ms int, fn goja.Callable) bool { return rt.scheduleAfter(ms, fn) })
	_ = cdxObj.Set("yield", func() goja.Value { return rt.yield(vm) })
	_ = cdxObj.Set("sleep", func(ms int) goja.Value { return rt.sleep(vm, ms) })
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
	_ = cdxObj.Set("tasks", rt.buildTasksObject(vm))
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
			_, err := rt.callResult(ctx, func(vm *goja.Runtime) (goja.Value, error) {
				return fn(goja.Undefined(), vm.ToValue(payload))
			})
			if err != nil {
				rt.logScope.Warn("Bus subscriber %q failed: %v", topic, err)
			}
		})
	})
	_ = obj.Set("handle", func(topic string, fn goja.Callable) {
		if rt.deps.Bus == nil {
			return
		}
		rt.deps.Bus.Handle(rt.owner, topic, func(ctx context.Context, payload any) (any, error) {
			return rt.callResult(ctx, func(vm *goja.Runtime) (goja.Value, error) {
				return fn(goja.Undefined(), vm.ToValue(payload))
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
	if rt.ctx != nil {
		return rt.ctx
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
			val, err := rt.callResult(ctx, func(vm *goja.Runtime) (goja.Value, error) {
				res, callErr := fn(goja.Undefined(), vm.ToValue(payload))
				if callErr != nil {
					return nil, callErr
				}
				if res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
					return vm.ToValue(payload), nil
				}
				return res, nil
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
		out, err := rt.deps.Bus.RunHook(rt.currentCtx(), hookName, payload)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return out
	})
	return obj
}

// buildQuestionsObject exposes the client question broker to plugins.
func (rt *Runtime) buildQuestionsObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("ask", func(raw map[string]any) goja.Value {
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
		return rt.async(vm, nil, func(ctx context.Context) (any, error) {
			answer, askErr := question.Ask(ctx, q)
			if askErr != nil {
				return nil, askErr
			}
			return map[string]any{
				"choice":    answer.Choice,
				"text":      answer.Text,
				"cancelled": answer.Cancelled,
				"reason":    answer.Reason,
			}, nil
		})
	})
	return obj
}

// buildAgentsObject exposes delegated turns to plugins.
func (rt *Runtime) buildAgentsObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("run", func(raw map[string]any) goja.Value {
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

		return rt.async(vm, nil, func(ctx context.Context) (any, error) {
			result, runErr := rt.deps.Agents.RunChild(ctx, req)
			if runErr != nil {
				return nil, runErr
			}
			return map[string]any{
				"session_id":     result.SessionID,
				"content":        result.Content,
				"steps":          result.Steps,
				"stopped_reason": result.StoppedReason,
				"usage":          result.Usage,
			}, nil
		})
	})
	_ = obj.Set("runMany", func(raw map[string]any) goja.Value {
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

		return rt.async(vm, nil, func(ctx context.Context) (any, error) {
			results, runErr := rt.deps.Agents.RunChildren(ctx, reqs)
			if runErr != nil {
				return nil, runErr
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
			return map[string]any{"results": out}, nil
		})
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

// ginWriter serializes the plugin handler with the request goroutine. When the
// client goes away the command keeps running on the VM, and a write to a gin
// context that the HTTP server already finished would race with its own reply.
type ginWriter struct {
	mu   sync.Mutex
	done bool
}

// run executes one response write unless the request already finished.
func (w *ginWriter) run(fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	fn()
}

// finish marks the request as answered, waiting for a write in flight.
func (w *ginWriter) finish() {
	w.mu.Lock()
	w.done = true
	w.mu.Unlock()
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
			writer := &ginWriter{}
			_, err := rt.callResult(c.Request.Context(), func(vm *goja.Runtime) (goja.Value, error) {
				ctxObj := rt.buildGinContextObject(vm, c, writer)
				return fn(goja.Undefined(), ctxObj)
			})
			writer.finish()
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
			writer := &ginWriter{}
			_, err := rt.callResult(c.Request.Context(), func(vm *goja.Runtime) (goja.Value, error) {
				ctxObj := rt.buildGinContextObject(vm, c, writer)
				_ = ctxObj.Set("next", func() {
					// Deferred until the VM command finishes so downstream handlers in the same plugin can acquire the VM without deadlock.
				})
				return fn(goja.Undefined(), ctxObj)
			})
			writer.finish()
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

func (rt *Runtime) buildGinContextObject(vm *goja.Runtime, c *gin.Context, writer *ginWriter) *goja.Object {
	ctxObj := vm.NewObject()
	_ = ctxObj.Set("method", c.Request.Method)
	_ = ctxObj.Set("path", c.Request.URL.Path)
	_ = ctxObj.Set("param", func(name string) string { return c.Param(name) })
	_ = ctxObj.Set("query", func(name string) string { return c.Query(name) })
	_ = ctxObj.Set("getHeader", func(name string) string { return c.GetHeader(name) })
	_ = ctxObj.Set("host", func() string { return c.Request.Host })
	_ = ctxObj.Set("header", func(k, v string) { writer.run(func() { c.Header(k, v) }) })
	_ = ctxObj.Set("status", func(code int) { writer.run(func() { c.Status(code) }) })
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
		writer.run(func() { c.JSON(code, v) })
	})
	_ = ctxObj.Set("string", func(code int, s string) {
		writer.run(func() { c.String(code, "%s", s) })
	})
	_ = ctxObj.Set("html", func(code int, htmlStr string) {
		writer.run(func() { c.Data(code, "text/html; charset=utf-8", []byte(htmlStr)) })
	})
	_ = ctxObj.Set("abortWithJSON", func(code int, v any) {
		writer.run(func() { c.AbortWithStatusJSON(code, v) })
	})
	_ = ctxObj.Set("next", func() {
		writer.run(func() { c.Next() })
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
		if rt.deps.Plugins != nil {
			desc.Order = rt.deps.Plugins.orderFor(rt.dir, desc.Order)
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
		fn, ok := goja.AssertFunction(handler)
		if !ok || fn == nil {
			panic(vm.NewTypeError("tool handler must be a function"))
		}
		if rt.deps.Registry == nil {
			return
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
				return rt.runTool(ctx, fn, args)
			},
		})
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = obj.Set("remove", func(name string) bool {
		if rt.deps.Registry == nil {
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
	_ = obj.Set("call", func(name string, args map[string]any, opts map[string]any) goja.Value {
		if rt.deps.Tools == nil {
			panic(vm.NewGoError(errors.New("tool execution is not available")))
		}
		if strings.TrimSpace(name) == "" {
			panic(vm.NewGoError(errors.New("tool name is required")))
		}
		call := ToolCall{Name: name, Arguments: args}
		if opts != nil {
			if value, ok := opts["session_id"].(string); ok {
				call.SessionID = value
			}
			if value, ok := opts["root_session_id"].(string); ok {
				call.RootID = value
			}
			if value, ok := opts["workdir"].(string); ok {
				call.Workdir = value
			}
			call.Depth = agentMaxSteps(opts["depth"])
		}
		if call.SessionID == "" {
			call.SessionID = "plugin:" + rt.manifest.ID
		}
		return rt.async(vm, nil, func(ctx context.Context) (any, error) {
			output, runErr := rt.deps.Tools.RunTool(ctx, call)
			result := map[string]any{"output": output}
			if runErr != nil {
				result["error"] = runErr.Error()
			}
			return result, nil
		})
	})
	return obj
}

// buildTasksObject exposes background turns to plugins.
func (rt *Runtime) buildTasksObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	runner := func() TaskRunner {
		if rt.deps.Tasks == nil {
			panic(vm.NewGoError(errors.New("background tasks are not available")))
		}
		return rt.deps.Tasks
	}
	_ = obj.Set("start", func(raw map[string]any) map[string]any {
		if raw == nil {
			panic(vm.NewGoError(errors.New("task options are required")))
		}
		req := TaskRequest{
			Prompt:         jsString(raw["prompt"]),
			Workdir:        jsString(raw["workdir"]),
			Preset:         jsString(raw["preset"]),
			Model:          jsString(raw["model"]),
			Owner:          jsString(raw["owner"]),
			MaxSteps:       agentMaxSteps(raw["max_steps"]),
			TimeoutSeconds: agentMaxSteps(raw["timeout_s"]),
		}
		if list, ok := raw["allowed_tools"].([]any); ok {
			for _, item := range list {
				if name, ok := item.(string); ok && name != "" {
					req.AllowedTools = append(req.AllowedTools, name)
				}
			}
		}
		info, err := runner().StartTask(rt.currentCtx(), req)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return map[string]any{"task_id": info.ID, "session_id": info.SessionID}
	})
	_ = obj.Set("get", func(id string) map[string]any {
		info, ok := runner().GetTask(rt.currentCtx(), id)
		if !ok {
			return map[string]any{"found": false}
		}
		return taskInfoMap(info)
	})
	_ = obj.Set("list", func() []map[string]any {
		infos := runner().ListTasks(rt.currentCtx())
		out := make([]map[string]any, 0, len(infos))
		for _, info := range infos {
			out = append(out, taskInfoMap(info))
		}
		return out
	})
	_ = obj.Set("answer", func(id, questionID string, answer map[string]any) map[string]any {
		if err := runner().AnswerTask(rt.currentCtx(), id, questionID, answer); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
		return map[string]any{"ok": true}
	})
	_ = obj.Set("cancel", func(id string) map[string]any {
		if err := runner().CancelTask(rt.currentCtx(), id); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
		return map[string]any{"ok": true}
	})
	return obj
}

// taskInfoMap converts one task snapshot into a JavaScript object.
func taskInfoMap(info TaskInfo) map[string]any {
	out := map[string]any{
		"found":          true,
		"task_id":        info.ID,
		"session_id":     info.SessionID,
		"status":         info.Status,
		"result":         info.Result,
		"stopped_reason": info.StoppedReason,
		"error":          info.Error,
	}
	if info.Question != nil {
		out["question"] = map[string]any{
			"id":    info.Question.ID,
			"kind":  info.Question.Kind,
			"title": info.Question.Title,
			"body":  info.Question.Body,
		}
	}
	return out
}

func (rt *Runtime) buildProvidersObject(vm *goja.Runtime) *goja.Object {
	obj := vm.NewObject()
	_ = obj.Set("register", func(specObj *goja.Object) {
		if specObj == nil {
			return
		}
		listFn, _ := goja.AssertFunction(specObj.Get("listModels"))
		completeFn, _ := goja.AssertFunction(specObj.Get("complete"))
		if rt.deps.Registry == nil {
			return
		}

		rt.deps.Registry.RegisterProvider(ProviderEntry{
			PluginID: rt.manifest.ID,
			ListModels: func(ctx context.Context) ([]ModelInfo, error) {
				if listFn == nil {
					return nil, nil
				}
				val, err := rt.callResult(ctx, func(vm *goja.Runtime) (goja.Value, error) {
					return listFn(goja.Undefined())
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
				return rt.runComplete(ctx, completeFn, req, onStream)
			},
		})
	})
	_ = obj.Set("listModels", func() goja.Value {
		if rt.deps.Registry == nil {
			return vm.ToValue([]ModelInfo{})
		}
		return rt.async(vm, nil, func(ctx context.Context) (any, error) {
			models, err := rt.deps.Registry.ListModels(ctx)
			if err != nil {
				return nil, err
			}
			return models, nil
		})
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
		if err := rt.deps.Plugins.RemovePlugin(rt.currentCtx(), id); err != nil {
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
	_ = obj.Set("exec", func(command string, workdir string, timeoutMS int) goja.Value {
		return rt.async(vm, rt.deps.AsyncExec, func(ctx context.Context) (any, error) {
			if workdir == "" {
				workdir = WorkdirFromContext(ctx)
			}
			return rt.deps.Sys.Exec(ctx, command, workdir, timeoutMS)
		})
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
		var onLine goja.Callable
		if len(call.Arguments) > 2 {
			onLine, _ = goja.AssertFunction(call.Arguments[2])
		}
		return rt.async(vm, rt.deps.Async, func(ctx context.Context) (any, error) {
			return rt.deps.Sys.Fetch(ctx, url, opts, func(line string) error {
				if onLine == nil {
					return nil
				}
				matched := make(chan error, 1)
				rt.post(func(vm *goja.Runtime) {
					_, err := onLine(goja.Undefined(), vm.ToValue(line))
					matched <- err
				})
				select {
				case err := <-matched:
					return err
				case <-ctx.Done():
					return ctx.Err()
				case <-rt.done:
					return fmt.Errorf("plugin %q is stopped", rt.manifest.ID)
				}
			})
		})
	})
	return obj
}
