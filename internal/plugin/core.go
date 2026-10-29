package plugin

// Binds core capabilities to the cdx object inside the plugin VM.
//
// The model never sees these functions: a plugin uses them to build its own
// tools. Calls go straight to Go without the bus, which keeps the path short
// and avoids a goroutine per call.

import (
	"encoding/json"
	"fmt"

	"github.com/dop251/goja"

	"github.com/yourok/cdx/internal/pluginapi"
)

// installCore adds the fs, shell and http namespaces to cdx.
func installCore(cdx *goja.Object, vm *goja.Runtime, api *pluginapi.API) {
	fs := vm.NewObject()
	_ = cdx.Set("fs", fs)

	// Arguments and results cross as plain JS values, so a plugin never
	// deals with serialization itself.
	_ = fs.Set("read", func(call goja.FunctionCall) goja.Value {
		res, err := api.Read(argString(call, 0))
		return result(vm, res, err)
	})
	_ = fs.Set("write", func(call goja.FunctionCall) goja.Value {
		res, err := api.Write(argString(call, 0), argString(call, 1))
		return result(vm, res, err)
	})
	_ = fs.Set("edit", func(call goja.FunctionCall) goja.Value {
		res, err := api.Edit(argString(call, 0), argString(call, 1), argString(call, 2), argBool(call, 3))
		return result(vm, res, err)
	})
	_ = fs.Set("list", func(call goja.FunctionCall) goja.Value {
		res, err := api.List(argString(call, 0))
		return result(vm, res, err)
	})
	_ = fs.Set("stat", func(call goja.FunctionCall) goja.Value {
		res, err := api.Stat(argString(call, 0))
		return result(vm, res, err)
	})
	_ = fs.Set("exists", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(api.Exists(argString(call, 0)))
	})
	_ = fs.Set("mkdir", func(call goja.FunctionCall) goja.Value {
		return result(vm, map[string]any{"created": true}, api.Mkdir(argString(call, 0)))
	})
	_ = fs.Set("remove", func(call goja.FunctionCall) goja.Value {
		return result(vm, map[string]any{"removed": true}, api.Remove(argString(call, 0)))
	})
	_ = fs.Set("glob", func(call goja.FunctionCall) goja.Value {
		res, err := api.Glob(argString(call, 0))
		return result(vm, res, err)
	})
	_ = fs.Set("search", func(call goja.FunctionCall) goja.Value {
		var opts pluginapi.SearchOptions
		if obj := call.Argument(1); obj != nil && !goja.IsUndefined(obj) && !goja.IsNull(obj) {
			if err := exportInto(vm, obj, &opts); err != nil {
				panic(vm.ToValue(err.Error()))
			}
		}
		res, err := api.Search(argString(call, 0), opts)
		return result(vm, res, err)
	})

	shell := vm.NewObject()
	_ = cdx.Set("shell", shell)
	_ = shell.Set("exec", func(call goja.FunctionCall) goja.Value {
		res, err := api.Exec(argString(call, 0))
		return result(vm, res, err)
	})

	httpSpace := vm.NewObject()
	_ = cdx.Set("http", httpSpace)
	_ = httpSpace.Set("request", func(call goja.FunctionCall) goja.Value {
		var req pluginapi.HTTPRequest
		if obj := call.Argument(0); obj == nil || goja.IsUndefined(obj) || goja.IsNull(obj) {
			panic(vm.ToValue("request requires an options object"))
		} else if err := exportInto(vm, obj, &req); err != nil {
			panic(vm.ToValue(err.Error()))
		}
		res, err := api.HTTP(req)
		return result(vm, res, err)
	})
}

// result returns a value or throws it as a JS exception.
//
// An exception lets the plugin catch the failure and decide what to do,
// unlike a null result which reads as success.
func result(vm *goja.Runtime, value any, err error) goja.Value {
	if err != nil {
		panic(vm.ToValue(err.Error()))
	}
	if value == nil {
		return goja.Null()
	}
	return vm.ToValue(value)
}

func exportInto(vm *goja.Runtime, value goja.Value, target any) error {
	data, err := json.Marshal(value.Export())
	if err != nil {
		return fmt.Errorf("invalid options: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("invalid options: %w", err)
	}
	return nil
}

func argString(call goja.FunctionCall, index int) string {
	v := call.Argument(index)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

func argBool(call goja.FunctionCall, index int) bool {
	v := call.Argument(index)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return false
	}
	return v.ToBoolean()
}
