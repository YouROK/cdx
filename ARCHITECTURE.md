# Architecture

`cdx` is a command-line agent built on a **microkernel and plugin-first architecture**.
The core binary provides only the foundational runtime, storage, HTTP router, IPC, and execution loop.
All domain features - LLM providers and model management, tools, permission policies, context assembly, and the web dashboard - are implemented as external plugins.

---

## 1. Core Principles

1. **Everything is a Plugin**: The core does not hardcode LLM protocols, model configurations, tool implementations, permission rules, or web pages.
2. **No Embedded Plugins**: Default plugins live in the `plugins/` source directory and are installed into the plugin directory during `make install`. They use the exact same API as third-party plugins.
3. **Self-Contained Plugins**: If a plugin acts as an LLM provider (or any other feature), it owns its entire lifecycle: it serves its own UI/settings window, registers its own HTTP endpoints on the core router, stores its models/configs in its plain `storage`, and stores API keys in its encrypted `secrets`. The core only asks the plugin for its available models and forwards completion requests to it.
4. **Core HTTP Router**: The core provides a built-in HTTP router as a base primitive. Plugins register their own routes, API handlers, and static assets on this router, while the `webui` plugin mounts the main web shell on `/`.
5. **Daemon + Thin Clients**: A background daemon holds the plugin runtimes, database, HTTP router, and active sessions. Thin clients (CLI, Web) communicate with the daemon over a fast local Unix domain socket (IPC) or HTTP.
6. **Isolated Storage & Encrypted Secrets**: Every plugin receives an isolated plain key-value namespace and an isolated AES-256-GCM encrypted namespace inside the single BoltDB database.

---

## 2. High-Level System Overview

```text
+-------------------------------------------------------------------+
|                           Clients                                 |
|   +--------------------+                 +--------------------+   |
|   |    cdx CLI         |                 |   Web Browser      |   |
|   +---------+----------+                 +---------+----------+   |
+-------------|--------------------------------------|--------------+
              | Unix Socket (IPC)                    | HTTP
+-------------v--------------------------------------v--------------+
|                         cdx Daemon (Core)                         |
|                                                                   |
|  +------------------+  +------------------+  +-----------------+  |
|  |   Event & Hook   |  |   Session Loop   |  |   Core HTTP     |  |
|  |       Bus        |  |   (Orchestrator) |  |     Router      |  |
|  +--------+---------+  +--------+---------+  +--------+--------+  |
|           |                     |                     |           |
|  +--------v---------------------v---------------------v--------+  |
|  |                   Plugin Host (Goja JS VMs)                 |  |
|  |                  + JSON Schema Engine                       |  |
|  +--------+-------------------------------------------+--------+  |
|           |                                           |           |
|  +--------v-------------------+   +-------------------v--------+  |
|  | BoltDB: Plain Storage      |   | BoltDB: Encrypted Secrets  |  |
|  | (per-plugin namespace)     |   | (AES-256-GCM per-plugin)   |  |
|  +----------------------------+   +----------------------------+  |
+-------------------------------------------------------------------+
              |                       |                       |
      +-------v-------+       +-------v-------+       +-------v-------+
      | LLM Providers |       | Tools & Perm  |       | WebUI & Ctx   |
      |   (Plugins)   |       |   (Plugins)   |       |   (Plugins)   |
      +---------------+       +---------------+       +---------------+
```

---

## 3. Core Subsystems (`internal/`)

### 3.1. Logger (`internal/logx`)
* Structured, leveled logging (`Debug`, `Info`, `Warn`, `Error`).
* Every message starts with a consistent module or plugin tag: `[Daemon]`, `[Router]`, `[Bus]`, `[Plugin:openai]`.
* Never logs values from the encrypted secrets store.

### 3.2. Storage & Encrypted Secrets (`internal/store`)
* Backed by a single `bbolt` database file (`cdx.db`).
* **Core Buckets**: sessions, conversation messages, and core state (such as the currently selected default provider/model ID).
* **Plugin Plain Storage (`storage`)**: isolated bucket per plugin (`plugin_storage:<id>`) for storing JSON values (e.g., configured provider endpoints, model lists, permission rules).
* **Plugin Encrypted Storage (`secrets`)**: isolated bucket per plugin (`plugin_secrets:<id>`).
  * Values are encrypted before write and decrypted on read using **AES-256-GCM** with a unique random nonce per write.
  * Used by plugins to store sensitive values such as API keys and tokens.

### 3.3. Bus, Events, and Hooks (`internal/bus`)
The bus connects the core and all plugins without coupling them together. It supports three communication patterns:
1. **Events (Pub/Sub)**: Asynchronous notifications (`session:started`, `message:delta`, `plugin:loaded`).
2. **Requests (RPC)**: Direct 1-to-1 calls to a registered service topic.
3. **Hooks (Middleware Pipeline)**: Ordered interceptors that can inspect, modify, or abort an operation before or after it happens:
   * `session:before_turn` - inject system prompts, `AGENTS.md`, or compact history.
   * `tool:before_call` - inspect tool arguments, block execution, or attach an `ask` question for the client.
   * `tool:decided` - after the client answers an `ask` question, allow or deny the tool call and record the choice.
   * `tool:after_call` - post-process or log tool output.

### 3.4. Core HTTP Router (`internal/router`)
* A foundational HTTP server and dynamic router built on top of `gin.Engine`.
* Exposes Gin's capabilities directly to plugins via `cdx.http`:
  * **Middleware (`use`)**: `cdx.http.use([prefix], handler)` with full support for `ctx.next()`, `ctx.abort()`, `ctx.set(key, val)`, and `ctx.get(key)`.
  * **Route groups & parameters**: `cdx.http.group(prefix, ...middleware)` and routes (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`) with `:param` and `*wildcard` matching.
  * **Context helpers & SSE**: JSON binding/rendering, headers, cookies, query params, status codes, and Server-Sent Events (`ctx.stream`) for live updates.
  * **Static asset directories**: `cdx.http.static(prefix, dir)` - serves frontend scripts, HTML, CSS, or icons from the plugin's folder.
  * **UI registration**: `cdx.ui.register({ title, icon, order, schema, entry, ui_api, config_url, styles })` - registers a declarative JSON Schema panel or a custom ESM page panel. Page panels are mounted into a shadow-root container and versioned with `ui_api`. Schema panels use the generic plugin settings endpoint unless `config_url` points at a plugin-owned endpoint.
* All routes and middleware registered by a plugin can be dynamically reassigned or torn down cleanly if a plugin is reloaded or stopped.
* The core does not authenticate API calls itself: the `auth` plugin registers middleware on `/api`, so every core and plugin endpoint is covered by the same check while that plugin is enabled.

### 3.5. JSON Schema Engine (`internal/schema`)
* Validates tool inputs and declarative plugin settings against standard JSON Schema (`type: "object"`).
* Supports `format: "secret"` so declarative forms can automatically mask secret fields and store them in `cdx.secrets`.
* Exposes declarative settings through `GET/POST /api/core/plugins/:id/settings`, backed by the plugin's isolated plain and encrypted storage. Plugins that already own a config endpoint can set `config_url` instead.

### 3.6. Plugin Host (`internal/plugin`)
* Discovers plugins from configured directories (`/usr/share/cdx/plugins`, `~/.local/share/cdx/plugins`).
* Each plugin lives in its own folder with a minimal `manifest.json` (`id`, `name`, `version`, `description`, `entry`) and its JS/web files.
* Runs each plugin in a dedicated `goja` VM goroutine (thread-safe command channel).
* Supports an optional worker pool (`workers` in `manifest.json`): functions explicitly marked with `cdx.inWorker(fn)`, currently provider `complete` and tool handlers, execute in isolated worker VMs, each on its own goroutine, while the control VM keeps serving HTTP routes, hooks, UI, and status requests. Worker VMs share `storage`/`secrets` but not JS memory, so shared mutable state must live in storage.
* Exposes the `cdx` host object to JS plugins:
  * `cdx.log` - scoped logger (`[Plugin:<id>]`).
  * `cdx.storage` - isolated plain key-value store (`get`, `set`, `delete`, `list`).
  * `cdx.secrets` - isolated encrypted key-value store (`get`, `set`, `delete`, `has`, `keys`).
  * `cdx.http` - register HTTP endpoints and static asset routes on the core router.
  * `cdx.ui` - register the plugin's settings/management view (JSON Schema or custom UI module) for the web panel.
  * `cdx.tools` - register tools for the LLM (`name`, `description`, `schema`, handler).
  * `cdx.providers` - register as an LLM provider (`listModels` callback, `complete` streaming handler). The core only queries which models the provider exposes and delegates chat completion turns to it.
  * `cdx.hooks` - register lifecycle interceptors (`on(hookName, priority, handler)`).
  * `cdx.bus` - publish events and send RPC requests.
  * `cdx.questions` - ask the active client a question through the core broker; works in worker VMs.
  * `cdx.agents` - run a delegated turn in a fresh child session and wait for its result; works in worker VMs.
  * `cdx.sys` - low-level host capabilities (HTTP fetch/streaming, file I/O, cryptographic random bytes, process execution).

### 3.7. Session Orchestrator (`internal/session`)
* Manages conversation sessions and message history in `bbolt`.
* Binds every session to a working directory and resolves the active session like Git: the nearest session in the current directory or its parents wins. An explicit session in a child directory shadows the parent session without affecting it.
* Serializes turns per session: a turn reserves its session before reading history and releases it when it finishes, and a prompt that arrives meanwhile waits in FIFO order. A session therefore always has a single writer, and the next turn sees the previous answer. Session resolution is atomic, so two simultaneous first prompts share one session. `--no-session` runs are not queued; to work in parallel in one directory, create a separate session with `cdx session new`.
* Owns the client question broker: the core validates a question envelope (`kind`, `title`, `body`, `details`, `options`, `default`, `text`), routes it to the client of the running turn, and returns `{ choice, text, cancelled }` without interpreting the question kind. The step-limit prompt uses it, and plugins reach the same broker through `cdx.questions.ask`, including from worker VMs.
* Runs delegated turns: `cdx.agents.run` asks the orchestrator to create a child session (`parent_id`, `root_id`, `depth + 1`) and run a full turn in it while the parent waits. The child shares the working directory and tools, keeps its own history, and never becomes the active session. Depth is capped at `1`, and a stateless parent cannot delegate.
* Enforces a per-turn tool-step budget from core settings (default `40`, range `1`-`200`). The `step_limit_mode` setting decides what happens when the budget is exhausted: `none` runs the turn without a limit, `ask` (default) asks the interactive client whether to continue with another equal budget, and `stop` ends the turn. The settings are re-read at that point, so a change made during the turn applies to it. The stop reason is stored on the session and returned to the client.
* Writes the prompt and every finished step to the store, so an interrupted turn keeps its prompt, answers, and tool results; a tool call left without a result is dropped when the session is loaded.
* Keeps the running turn in memory (streamed text, reasoning, tool calls, and the number of messages stored before it). `GET /api/core/sessions/:id/live` returns that snapshot and the sessions list reports the IDs of sessions with a running turn, so the dashboard can follow a turn without waiting for the stored messages.
* Supports two session kinds:
  1. **Temporary (`temp`, default)**: Automatically created in the working directory when no session is active. Expires after a configurable TTL (default: `24h` since last activity).
  2. **Persistent (`persistent`)**: Never expires automatically. Created with `cdx session new`, or by switching the active session with `cdx session mode persistent`.
* Session management is CLI-only: `cdx session <new|list|use|mode|delete>`. `use` selects an existing session for the directory, `mode` switches the active session between `temp` and `persistent`, and `delete` removes a session with its history. None of these commands takes a prompt.
* **Stateless** execution is a per-request flag (`--no-session`) that skips reading and writing history without touching the directory session.
* Queries registered provider plugins for the list of available models.
* Runs the agent turn loop:
  1. Appends user message.
  2. Runs `session:before_turn` hooks (context plugins attach system prompt, rules, and available tool schemas).
  3. Calls the selected provider plugin's `complete` handler, streaming token deltas to the client.
  4. If the LLM requests tool calls, runs `tool:before_call` hooks. When a hook attaches an `ask` question, the core asks the client, runs `tool:decided`, and only then executes the target tool plugin; it then runs `tool:after_call` hooks and feeds results back to the provider plugin.
  5. Persists turn history and emits completion events.

### 3.8. IPC & CLI (`internal/ipc`, `internal/cli`)
* Framed streaming protocol over a Unix domain socket.
* Supports live token deltas, progress spinners, and interactive permission prompts from the daemon back to the CLI terminal.
* Output adapts to the destination: progress, reasoning, colors, and the bell appear only when stdout is a terminal. Pipes and redirects receive plain final content, while `--quiet` disables progress and `--json` prints the final result as JSON.
* `cdx --config` starts the daemon if needed, reads its HTTP address over IPC, prints the dashboard URL, and opens it in the default browser.
* The HTTP listener defaults to `127.0.0.1:8787`. `--http` and `CDX_HTTP_ADDR` select another address; `CDX_ALLOW_REMOTE_HOST` binds all interfaces when neither is set.

---

## 4. Default Plugins (`plugins/`)

Default plugins live in `plugins/` and are installed into the plugin directory via `make install`:

1. **`plugins/auth`**: Generates the API token into its encrypted secrets and guards every `/api` route as middleware: requests need the `X-CDX-Token` header and a local `Host`, which blocks DNS rebinding. Serves the token to the dashboard over the bus (`auth:token`) and exposes the `API access` settings page. Loaded first (`order: 1`), because the other plugins register endpoints behind it. When the daemon starts with `CDX_ALLOW_REMOTE_HOST`, both checks are skipped so the dashboard can be opened from another machine without a token.
2. **`plugins/webui`**: Registers `/` on the core HTTP router to serve the React web application shell. Embeds the API token from the `auth` plugin into the page, serves only the built assets from `web/dist`, and re-reads `index.html` on every request so a rebuild needs only a reload. Queries the core for registered plugin UI panels, renders JSON Schema forms itself, and mounts custom page panels as isolated shadow-root modules. The shell serves the shared React runtime through an import map so compiled plugin panels use one React instance.
3. **`plugins/providers`**:
   * Owns provider connections, model lists, parameters (`temperature`, `context_window`, `max_tokens`, `reasoning`), and model discovery.
   * Saves configurations in its `cdx.storage` and API keys in its `cdx.secrets`.
   * Exposes its own React page panel and HTTP endpoints on the core router.
   * Registers available models with the core via `cdx.providers`.
4. **`plugins/presets`**:
   * Manages built-in (`standard`, `mini`, `creator`, `subagent`) and custom agent presets.
   * Replaces `{{workspace}}` and `{{max_iterations}}` in preset prompts from the active session and step budget.
   * Dynamically embeds the live Plugin API Reference (`cdx.apiDoc()`, generated from `internal/plugin/doc.go`) into the `creator` preset.
   * Sets the base system prompt in `session:before_turn` (priority `5`) and exposes the `Agent presets` page in the Web UI as a React page panel.
5. **`plugins/permissions`**: Intercepts `tool:before_call`, checks stored rules (`allow`, `ask`, `deny`) in its storage, and returns an `ask` question instead of blocking the plugin VM. It persists the `once`, `session`, `daemon`, and `always` choices in `tool:decided` and exposes its policies, remembered grants, and grant reset as a React page panel. Actions that leave the working folder are governed by a separate `outside_policy` (`ask`, `allow`, `deny`). Prompts describe the action in full (`title` plus labelled `details`) so the client shows the exact command, file, or folder instead of a clipped summary.
6. **`plugins/context`**: Appends `AGENTS.md`, conversation summary, and the `Environment` / `Tools` block in `session:before_turn` (priority `10`), compacts history when approaching message limits, and exposes its settings as a declarative schema panel. The summary is capped (`max_summary_chars`, default 8000), keeps its newest part, and each dropped message is summarized once per session (a per-session counter in the plugin storage): every turn passes the whole history, so re-summarizing it would grow the summary past the model context window.
7. **`plugins/tools-local`**: Provides filesystem and shell tools (`read_file`, `write_file`, `edit_file`, `list_dir`, `shell`) to the model.
8. **`plugins/ask`**: Registers the `ask_user` tool, so the model can ask a question with optional choices or a free-text answer. The tool runs in a worker VM and uses `cdx.questions.ask`, so the plugin control VM stays responsive while the user answers.
9. **`plugins/subagent`**: Registers the `subagent` tool, which delegates a self-contained task to a child session through `cdx.agents.run`. The child uses the `subagent` preset, works in the same directory, and returns a concise final report as the tool result.
