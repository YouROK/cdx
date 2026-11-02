# Plugin Development Guide

This document defines the directory structure, manifest contract, host API (`cdx`), and design rules for `cdx` plugins.

---

## 1. Directory Layout

Every plugin lives in its own directory (either inside `plugins/<id>` in the repository for default plugins, or packaged as a `.zip` archive for installation into `~/.local/share/cdx/plugins/<id>`):

```text
<plugin-id>/
  manifest.json      # Required: plugin identity and entry script
  main.js            # Required: Goja JS entry script defining setup(cdx)
  web/               # Optional: static frontend assets and custom UI panel
    panel.js         # Optional: custom Web UI panel script
```

---

## 2. `manifest.json` Specification

```json
{
  "id": "my-plugin",
  "name": "My Plugin",
  "version": "1.0.0",
  "min_cdx_version": "0.2.0",
  "description": "Short description of what this plugin does",
  "author": "your-name",
  "homepage": "https://example.com/my-plugin",
  "entry": "main.js",
  "order": 50,
  "workers": 2
}
```

### Field Rules
* `id`: lowercase Latin letters, digits, dots, dashes, or underscores (`^[a-z0-9][a-z0-9._-]{0,63}$`).
* `version`: semantic version `MAJOR.MINOR.PATCH`.
* `min_cdx_version` (optional): minimum compatible `cdx` version checked before `.zip` installation.
* `entry`: relative path to the JavaScript entry file inside the plugin directory (default: `"main.js"`).
* `order` (optional): load priority (lower numbers load first; e.g. `providers` = `5`, `presets` = `8`, `permissions` = `10`, `context` = `15`, `tools-local` = `25`, `webui` = `90`).
* `workers` (optional, default `0`): number of isolated worker VMs (0-16). Only functions explicitly wrapped with `cdx.inWorker(fn)` are dispatched there. Worker VMs run in separate Go goroutines, so the control VM stays responsive for HTTP routes, hooks, status, and UI. The plugin's `setup(cdx)` runs once per worker; keep it idempotent and store shared mutable state in `cdx.storage`/`cdx.secrets`. Inside a worker, `cdx.worker` is `true`, while control-plane APIs (`cdx.http`, `cdx.hooks`, `cdx.ui`, `cdx.bus`) are disabled.

---

## 3. JavaScript Entry & `setup(cdx)`

When the daemon starts or loads a plugin, it creates an isolated `goja` VM, binds the global `cdx` object, executes the entry script, and calls `setup(cdx)`:

```js
function setup(cdx) {
  cdx.log.info("Initializing plugin " + cdx.id);
}
```

---

## 4. The `cdx` Host API

### 4.1. Identity & Live Reference
* `cdx.id` (string) - the plugin's ID from `manifest.json`.
* `cdx.dir` (string) - absolute path to the plugin's directory on disk.
* `cdx.worker` (boolean) - `true` inside an isolated worker VM started for `workers > 0`.
* `cdx.workdir()` (string) - the active session working directory. Tool handlers should resolve relative paths and the default shell directory against it.
* `cdx.inWorker(fn)` - marks a provider `complete` handler or tool handler for execution in a worker VM. Without the marker the function runs in the control VM.
* `cdx.apiDoc()` (string) - returns the live Markdown Plugin API Reference generated directly from the Go host definitions.

### 4.2. Logging (`cdx.log`)
Messages are automatically prefixed with `[Plugin:<id>]`:
* `cdx.log.debug(msg)`
* `cdx.log.info(msg)`
* `cdx.log.warn(msg)`
* `cdx.log.error(msg)`

### 4.3. Plain Storage (`cdx.storage`)
Isolated per-plugin JSON bucket (`plugin_storage:<id>`) in `cdx.db`:
* `cdx.storage.get(key)` -> returns decoded JSON value or `null`.
* `cdx.storage.set(key, value)` -> stores any JSON-serializable value.
* `cdx.storage.delete(key)` -> deletes a key.
* `cdx.storage.keys()` -> returns sorted array of keys.
* `cdx.storage.list()` -> returns map of all `key -> value` pairs.

### 4.4. Encrypted Secrets (`cdx.secrets`)
Isolated per-plugin **AES-256-GCM** encrypted bucket (`plugin_secrets:<id>`) in `cdx.db` with AAD binding:
* `cdx.secrets.get(key)` -> decrypts and returns string secret or `null`.
* `cdx.secrets.set(key, plaintext)` -> encrypts and stores secret (empty string deletes the key).
* `cdx.secrets.delete(key)` -> removes secret.
* `cdx.secrets.has(key)` -> returns `true` if set without exposing plaintext.
* `cdx.secrets.keys()` -> returns sorted list of secret key names.
* `cdx.secrets.masked()` -> returns `{ "<key>": { "has_value": true } }` safe for UI responses.

### 4.5. LLM Tools (`cdx.tools`)
* `cdx.tools.register(spec, handler)`:
  ```js
  cdx.tools.register({
    name: "read_note",
    description: "Read a user note by title",
    permission: "read", // "read" | "edit" | "dangerous"
    input_schema: {
      type: "object",
      properties: {
        title: { type: "string", description: "Note title" }
      },
      required: ["title"]
    }
  }, cdx.inWorker(function(args) {
    return { content: cdx.storage.get("note:" + args.title) };
  }));
  ```

  Wrap the handler with `cdx.inWorker(...)` to run it in a worker VM when `workers > 0`. The handler and `state` must not rely on mutable JS variables from the control VM; use `cdx.storage`/`cdx.secrets` for shared state. Relative filesystem paths and the default shell directory should be resolved against `cdx.workdir()`.
* `cdx.tools.list()` -> returns all registered tool specifications.

### 4.6. LLM Providers (`cdx.providers`)
* `cdx.providers.register({ listModels, complete })`:
  * `listModels()`: returns array of `{ id, name, provider, default, context_window }`.
  * `complete(req, emit)`: executes a completion turn for `req.model`, calling `emit({ type: "text"|"reasoning", delta: "..." })` for live streaming and returning `{ content, reasoning, tool_calls, usage }`. Wrap it as `complete: cdx.inWorker(complete)` to run it in a worker VM when `workers > 0`.

### 4.7. Lifecycle Hooks (`cdx.hooks`)
* `cdx.hooks.on(hookName, priority, handler)`:
  * `session:before_turn`: inspect or mutate `{ session_id, workdir, model, preset, max_steps, system_prompt, summary, messages, tools }`.
  * `tool:before_call`: inspect or mutate `{ session_id, workdir, tool, permission, arguments }`, or `throw new Error(...)` to block tool execution.
  * `tool:after_call`: inspect or mutate `{ session_id, workdir, tool, arguments, output }`.

### 4.8. Event & RPC Bus (`cdx.bus`)
* `cdx.bus.publish(topic, payload)`
* `cdx.bus.subscribe(topic, handler)`
* `cdx.bus.handle(topic, responder)`
* `cdx.bus.request(topic, payload)` (e.g., `cdx.bus.request("client:prompt_user", { summary, tool, permission, options })`)

### 4.9. Core HTTP Router & Gin Middleware (`cdx.http`)
* `cdx.http.get|post|put|delete|patch(path, handler)`
* `cdx.http.group(prefix)` -> returns a scoped router group.
* `cdx.http.use([prefix], middlewareFn)` -> registers Gin middleware supporting `ctx.next()`, `ctx.abortWithJSON(code, obj)`, `ctx.set(k, v)`, `ctx.get(k)`.
* `cdx.http.static(urlPrefix, relativeDir)` -> mounts static files from the plugin directory.

### 4.10. Web UI Integration (`cdx.ui`)
* **Declarative JSON Schema settings**:
  ```js
  cdx.ui.register({
    title: "My Plugin",
    description: "Settings for My Plugin",
    order: 50,
    schema: {
      type: "object",
      properties: {
        api_key: { type: "string", title: "API Key", format: "secret" }
      }
    }
  });
  ```
* **Custom Web Panel (`web/panel.js`)**:
  ```js
  cdx.http.static("/ui/my-plugin", "./web");
  cdx.ui.register({
    title: "My Plugin",
    description: "Custom interactive workspace",
    order: 50,
    entry: "/ui/my-plugin/panel.js"
  });
  ```
  Inside `web/panel.js`, register `window.CDXPluginPanels["my-plugin"] = function(container) { ... }`.

### 4.11. Plugin Lifecycle Management (`cdx.plugins`)
* `cdx.plugins.list()`
* `cdx.plugins.installZip(url)`
* `cdx.plugins.setEnabled(id, enabled)`
* `cdx.plugins.remove(id)`

### 4.12. System Capabilities (`cdx.sys`)
* `cdx.sys.readFile(path)` / `cdx.sys.writeFile(path, content)` / `cdx.sys.stat(path)` / `cdx.sys.readDir(path)`
* `cdx.sys.exec(command, workdir, timeoutMS)`
* `cdx.sys.fetch(url, options, onLineCallback)`
