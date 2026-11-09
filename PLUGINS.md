# Plugin Development Guide

This document defines the directory structure, manifest contract, host API (`cdx`), and design rules for `cdx` plugins.

---

## 1. Directory Layout

Every plugin lives in its own directory (either inside `plugins/<id>` in the repository for default plugins, or packaged as a `.zip` archive for installation into `~/.local/share/cdx/plugins/<id>`):

```text
<plugin-id>/
  manifest.json      # Required: plugin identity and entry script
  main.js            # Required: Goja JS entry script defining setup(cdx)
  web/               # Optional: frontend source and built page panels
    src/panel.jsx    # Optional: page panel source (React or plain JS)
    dist/panel.js    # Built ESM page module served by the Web UI
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
  * `session:before_turn`: inspect or mutate `{ session_id, workdir, model, preset, max_steps, system_prompt, summary, messages, tools }`; `max_steps` is `0` when the turn runs without a step limit.
  * `tool:before_call`: inspect or mutate `{ session_id, root_session_id, depth, workdir, tool, permission, arguments }`, or `throw new Error(...)` to block tool execution. Set `payload.ask` to a [question envelope](#413-client-questions-cdxquestions) to ask the client without holding the plugin VM; the core asks and then runs `tool:decided`.
  * `tool:decided`: receives `{ session_id, root_session_id, depth, workdir, tool, permission, arguments, choice, text, cancelled, tag }` after the client answers. Key `session` grants by `root_session_id` so they cover delegated turns. Act only when `tag` matches the question your plugin asked, then return `allowed: true` to run the tool; otherwise the core reports `error` (or a default message) to the model. Persist `once`, `session`, `daemon`, and `always` grants here. Without a deciding hook an asked tool call is denied.
  * `tool:after_call`: inspect or mutate `{ session_id, workdir, tool, arguments, output }`.

### 4.8. Event & RPC Bus (`cdx.bus`)
* `cdx.bus.publish(topic, payload)`
* `cdx.bus.subscribe(topic, handler)`
* `cdx.bus.handle(topic, responder)`
* `cdx.bus.request(topic, payload)`

Client questions do not use the bus: call `cdx.questions.ask` instead. The old `client:prompt_user` topic is removed.

### 4.9. Core HTTP Router & Gin Middleware (`cdx.http`)
* `cdx.http.get|post|put|delete|patch(path, handler)`
* `cdx.http.group(prefix)` -> returns a scoped router group.
* `cdx.http.use([prefix], middlewareFn)` -> registers Gin middleware supporting `ctx.next()`, `ctx.abortWithJSON(code, obj)`, `ctx.set(k, v)`, `ctx.get(k)`.
* `cdx.http.static(urlPrefix, relativeDir)` -> mounts static files from the plugin directory.

Handlers receive a context object with:

* `ctx.method`, `ctx.path`, `ctx.host()` - request line and the `Host` header;
* `ctx.param(name)`, `ctx.query(name)`, `ctx.getHeader(name)`;
* `ctx.header(name, value)`, `ctx.status(code)`, `ctx.string(code, text)`, `ctx.html(code, html)`, `ctx.json(code, value)`;
* `ctx.bindJSON()` -> parsed request body;
* `ctx.abortWithJSON(code, value)` - stop the chain with a JSON response;
* `ctx.next()` - continue to the next handler;
* `ctx.set(key, value)` / `ctx.get(key)` - per-request values shared between middleware and handlers.

Keep state-changing and data endpoints under `/api`: the `auth` plugin guards that prefix with its token middleware, while other prefixes are public.

### 4.10. Web UI Integration (`cdx.ui`)

The Web UI has three panel levels. Prefer the simplest one that fits the plugin.

**1. Declarative JSON Schema settings**

```js
cdx.ui.register({
  title: "My Plugin",
  description: "Settings for My Plugin",
  order: 50,
  schema: {
    type: "object",
    properties: {
      api_key: { type: "string", title: "API Key", format: "secret" },
      enabled: { type: "boolean", title: "Enabled", default: true }
    }
  }
});
```

`cdx.settings.register(schema)` and `cdx.settings.save(values)` are the low-level equivalents when the plugin already calls them directly. The Web UI renders the form, applies defaults, validates the values, and stores non-secret fields in the plugin's plain storage and `format: "secret"` fields in its encrypted storage. The default settings endpoint is:

```text
GET  /api/core/plugins/<id>/settings
POST /api/core/plugins/<id>/settings
```

To keep an existing plugin-owned endpoint, add `config_url`:

```js
cdx.ui.register({
  title: "My Plugin",
  order: 50,
  config_url: "/api/plugins/my-plugin/config",
  schema: { ... }
});
```

**2. Custom page panel**

A page panel is a built ESM module mounted into an isolated shadow-root container:

```js
cdx.http.static("/ui/my-plugin", "./web");
cdx.ui.register({
  title: "My Plugin",
  description: "Custom interactive workspace",
  order: 50,
  entry: "/ui/my-plugin/dist/panel.js",
  ui_api: 1,
  styles: ["/ui/my-plugin/dist/panel.css"]
});
```

The module must export `mount(container, api)` and may return a cleanup function:

```js
export function mount(container, api) {
  return api.render(<App api={api} />);
}
```

`api` provides:

* `plugin` - the plugin's UI descriptor.
* `toast(message, ok)` - shell toast.
* `confirm(title, message)` - promise-based confirmation dialog.
* `fetchJSON(url, options)` and `postJSON(url, body)` - fetch helpers.
* `addStyles(css)` - append a style tag to the panel's shadow root.
* `render(element)` - recommended React entry point. It creates a root inside the shadow root and wraps the tree in an error boundary.

The container is a `ShadowRoot`, so panel CSS does not leak into the shell. The shell injects its base stylesheet and the descriptors from `styles` before mounting the panel. Use `api.render` for React panels so render failures stay inside the panel instead of breaking the dashboard.

The injected stylesheet covers the dashboard form classes: `.panel`, `.form-stack`, `.grid-2`, `.grid-3`, `label`, `.field-hint`, `.btn`, `.btn-primary`, `.badge`, and the input, select, and textarea controls. Text fields take the full width of their label and textareas do not resize, so a panel with these classes lines up with the rest of the dashboard. A field is title, control, hint, in that order: the title above the control and the hint below it. A checkbox is a control too: give it a `.check-field` box of input height with the checkbox and its label inside, so it lines up with the text fields. A list of checkboxes uses `.checkbox-row`, which keeps the checkbox and its caption on one line. A long explanation belongs behind a question mark, not in a paragraph under the control. For anything else, pass CSS to `api.addStyles(css)`; it appends a style tag to the panel shadow root and returns a cleanup function.

React page panels use the shell-provided React runtime. The import map exposes `react`, `react/jsx-runtime`, `react-dom`, and `react-dom/client`; keep those imports external when building:

```js
// vite.config.js
export default defineConfig({
  build: {
    lib: { entry: "src/panel.jsx", formats: ["es"] },
    rollupOptions: {
      external: ["react", "react-dom", "react-dom/client", "react/jsx-runtime"]
    }
  }
});
```

`ui_api` is the page-panel contract version. The current version is `1`. The Web UI refuses a panel whose `ui_api` is newer than it understands.

**3. Shell slots**

Not available yet. Page panels are the extension point for custom UI in the current version.

The legacy `window.CDXPluginPanels` registry is not used. New page panels are ESM modules and do not need a build step if they use plain DOM or React without JSX.

### 4.11. Plugin Lifecycle Management (`cdx.plugins`)
* `cdx.plugins.list()`
* `cdx.plugins.installZip(url)`
* `cdx.plugins.setEnabled(id, enabled)`
* `cdx.plugins.remove(id)`

### 4.12. System Capabilities (`cdx.sys`)
* `cdx.sys.readFile(path)` / `cdx.sys.writeFile(path, content)` / `cdx.sys.stat(path)` / `cdx.sys.readDir(path)`
* `cdx.sys.random(n)` - returns `n` cryptographically random bytes as a hex string (1-64), for tokens and nonces
* `cdx.sys.env(name)` - returns the value of an environment variable of the daemon process
* `cdx.sys.exec(command, workdir, timeoutMS)`
* `cdx.sys.fetch(url, options, onLineCallback)`

### 4.13. Client Questions (`cdx.questions`)
* `cdx.questions.ask(question)` -> `{ choice, text, cancelled, reason }`.
  * The core routes the question to the client of the running turn and waits for the answer. Without an interactive client the answer is cancelled with a reason.
  * `question` is an envelope: `kind` (rendering hint such as `model` or `permission`), `title` (required), `body`, `details` (`[{ label, value }]`), `options` (`[{ id, label, description }]`), `default` (an option id), `text` (allow a free-text answer), and `tag` (asker identity: the host stamps it for `cdx.questions.ask`, a hook sets it for its own questions).
  * The host sets `source` to `plugin:<id>`; a plugin cannot claim to be the core.
  * Unlike `cdx.bus`, `cdx.questions` also works inside worker VMs, so tools marked with `cdx.inWorker` can ask the user.
  * When options are present the client resolves a default (the given one, otherwise the first option), so Enter accepts it. An unrecognized answer repeats the question until it matches an option or the free text; `y`/`n` work in any case. Free text without an option cancels on an empty line, and non-interactive clients answer `cancelled` without showing the question.
  * Validation rejects an empty title, duplicate option ids, a default that is not an option, more than 20 options or details, and a question with neither options nor free text.

### 4.14. Subagents (`cdx.agents`)
* `cdx.agents.run({ prompt, preset?, model?, max_steps? })` -> `{ session_id, content, steps, stopped_reason, usage }`.
  * Runs a delegated turn in a fresh child session and waits for its result. The child does not see the current conversation, so `prompt` must be a self-contained brief: goal, relevant files, constraints, and the expected result.
  * The child shares the working directory and tools, inherits the parent's preset and model unless overridden, and never becomes the active session for the directory.
  * Depth is capped at `1`: a child cannot start another child. Stateless runs (`--no-session`) cannot delegate.
  * Works in worker VMs, like `cdx.questions`. The question broker is shared, so a child can ask the same client.
* `cdx.agents.runMany({ prompts, preset?, model?, max_steps? })` -> `{ results: [...] }`.
  * Runs up to `8` independent child turns in parallel and returns their results in the input order. Each result has the same fields as `run`, plus `error` for a failed child.
  * The default `subagent` tool uses this with its `tasks` array; a single `prompt` stays one child. Separate tool calls remain sequential, so use `tasks` for real parallelism.
