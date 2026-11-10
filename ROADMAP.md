# Roadmap

This document tracks the step-by-step implementation of `cdx`.
Work proceeds one stage at a time. Each stage must have passing unit tests (`go test ./...`) and pass static analysis (`go vet ./...`) before moving to the next.

---

## Stage 0: Project Preparation
- [x] Initialize clean repository files (`.gitignore`, `LICENSE`, `go.mod`).
- [x] Define workflow and architecture rules in `AGENTS.md`.
- [x] Draft `ARCHITECTURE.md` and `ROADMAP.md`.
- [x] Review and approve architecture and roadmap with the author.

## Stage 1: Foundation (Logger, Config Paths, Build System)
- [x] Create `Makefile` (build, test, vet, fmt, clean, install, uninstall with plugin copy).
- [x] Implement `internal/logx` (leveled logger with `[Tag]` validation and formatting).
- [x] Implement `internal/paths` (standard XDG directories for config, data, socket, and plugins).
- [x] Implement minimal `cmd/cdx/main.go` skeleton with version info.

## Stage 2: Storage & Encrypted Secrets (`internal/store`)
- [x] Set up `bbolt` database wrapper in `internal/store`.
- [x] Implement isolated per-plugin plain storage (`storage` namespace).
- [x] Implement AES-256-GCM master key management and isolated per-plugin encrypted storage (`secrets` namespace).
- [x] Add unit tests for storage isolation, encryption/decryption, and secret masking.

## Stage 3: JSON Schema Engine & Core HTTP Router (`internal/schema`, `internal/router`)
- [x] Implement JSON Schema validator (`type`, `properties`, `required`, `enum`, `minimum`, `maximum`, `default`, `format: "secret"`).
- [x] Implement dynamic Core HTTP Router (`internal/router`) allowing plugins to register/reassign HTTP endpoints, static assets, and UI descriptors (`cdx.http`, `cdx.ui`).
- [x] Add unit tests for schema validation and dynamic route registration/unregistration.

## Stage 4: Event, RPC, and Hook Bus (`internal/bus`)
- [x] Implement Pub/Sub event broadcasting with cancellation and buffer policies.
- [x] Implement 1-to-1 RPC request/response routing with timeouts and recursion guards.
- [x] Implement ordered Hook pipeline (`before` / `after` interceptors that can modify data or abort execution).
- [x] Add unit tests for events, RPC calls, and hook chains.

## Stage 5: Plugin Host & JS Runtime (`internal/plugin`)
- [x] Define minimal `manifest.json` loader and validator (`id`, `name`, `version`, `description`, `entry`).
- [x] Implement single-owner `goja` VM runtime with timeouts and crash recovery.
- [x] Expose core JS APIs to plugins:
  - `cdx.log`, `cdx.storage`, `cdx.secrets`
  - `cdx.http`, `cdx.ui`
  - `cdx.bus`, `cdx.hooks`
  - `cdx.tools`, `cdx.providers`
  - `cdx.sys` (HTTP fetch/SSE streaming, file operations, command execution)
- [x] Add an optional worker VM pool (`workers` in the manifest plus `cdx.inWorker(fn)`) so explicitly marked provider completions and tool calls do not block the plugin control VM.
- [x] Add unit tests using mock JS plugins registering routes, UI descriptors, tools, providers, hooks, storage, and secrets.

## Stage 6: Session Orchestrator (`internal/session`)
- [x] Implement session and message persistence in `bbolt`.
- [x] Implement provider registry lookup (querying registered provider plugins for available models).
- [x] Implement the agent turn loop:
  - `session:before_turn` hook execution
  - Provider plugin completion and token streaming
  - Tool call loop with `tool:before_call` and `tool:after_call` hooks
  - Cancellation and error handling
- [x] Add unit tests for full multi-turn conversations and tool execution flows.

## Stage 7: Daemon, IPC, and CLI (`internal/ipc`, `internal/daemon`, `internal/cli`)
- [x] Implement Unix socket IPC server and client with bidirectional streaming (for live token output and interactive user prompts).
- [x] Implement daemon lifecycle (`start`, `stop`, `status`, HTTP router startup, signal handling).
- [x] Implement CLI commands:
  - One-shot and interactive chat execution
  - Session management (`cdx session new`, `list`, `use`, `mode`, `delete`)
  - Plugin status & management (`plugins list`)
- [x] Add integration tests for CLI-to-Daemon communication.

## Stage 8: Default Core Plugins (`plugins/`)
- [x] `plugins/permissions`: tool call interception, rule storage in `cdx.storage`, interactive user prompt, and web UI panel.
- [x] `plugins/context`: system prompt builder, `AGENTS.md` loader, token estimator, and history compaction.
- [x] `plugins/tools-local`: `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `shell`.
- [x] `plugins/providers` (OpenAI-compatible & Anthropic): full self-contained provider/model management, saving configs in `cdx.storage` and API keys in `cdx.secrets`, model discovery, streaming completions, and provider settings UI panel.
- [x] Test default plugins end-to-end with the daemon.

## Stage 9: Web UI Shell Plugin (`plugins/webui`)
- [x] Implement `plugins/webui` mounting on `/` of the core HTTP router.
- [x] Render navigation for all installed plugins, supporting both automatic JSON Schema forms and custom plugin UI panels (such as the Providers & Models panel).
- [x] Verify full installation and packaging via `make install`.

## Stage 10: React Web UI and Page Panel Contract
- [x] Rebuild the `plugins/webui` shell as a React application with a shared React runtime exposed through an import map.
- [x] Render declarative JSON Schema panels in the shell and expose generic plugin settings endpoints.
- [x] Define the versioned ESM page-panel contract (`mount(container, api)`, `ui_api`).
- [x] Mount page panels in shadow-root containers with lazy loading, base styles, and per-panel error boundaries.
- [x] Migrate the Providers & Models and Agent presets panels to React.
- [x] Move Permissions and Context settings to declarative schema panels.
- [x] Add the Vite build pipeline and wire it into `make build`.

## Stage 11: Web UI Parity and Access Control
- [x] Guard every `/api` route with a token owned by the `auth` plugin, stored in its encrypted secrets.
- [x] Reject API requests whose `Host` header is not local, to block DNS rebinding.
- [x] Embed the token into the served page from the `webui` plugin, so no token is kept in the URL.
- [x] Serve only the built assets from `web/dist` and re-read `index.html` on every request.
- [x] Add hash routing with deep links and grouped menu sections.
- [x] Refresh the menu after plugin install, enable, and remove; recover from a failed settings load.
- [x] Show remembered permission grants and reset them from the Permissions page.
- [x] Add frontend unit tests (`npm test`) and a watch build (`npm run dev`).

## Stage 12: Permission Prompt UX
- [x] Show the full action in permission prompts, wrapped to the terminal width instead of clipped.
- [x] Describe outside-folder actions with the working folder, the outside path, and the full command or file.
- [x] Accept Enter for the default answer (`once`) and show `once`/`deny` as `Y/n` when only those two are offered.
- [x] Add an outside-folder policy (`ask`, `allow`, `deny`) with a note that it is not a sandbox.
- [x] Add "?" hints with the full explanation to the settings pages; the popover opens on click for touch screens.

## Stage 13: Runtime Feedback
- [x] Flatten progress labels to one line, so a command with line breaks no longer leaves stale spinner lines.
- [x] Ring the bell for every interactive question, including the step-budget prompt.
- [x] Shorten the end-of-turn summary to duration, steps, tokens, and changed files; drop the tool-error section.
- [x] Answer a busy plugin VM with `503` instead of hanging, so a page does not freeze while a prompt waits in the terminal.
- [x] Retry busy settings pages automatically once the plugin is free again.
- [x] Replace the ask-on-limit checkbox with a step-limit mode (no limit, ask, stop) and keep the budget between 1 and 200.

## Stage 14: Plugin Authoring Mode
- [x] Rewrite the creator preset with the Goja runtime limits, the HTTP and panel contracts, and a plain-DOM panel skeleton.
- [x] Align panel form controls with the dashboard: full-width fields and fixed-height textareas.
- [x] Give every field the same order - title above, control, hint below - including checkboxes; long explanations move behind the question mark. State the rule in the creator prompt and PLUGINS.md.
- [x] Add a live creator test that builds a minimal plugin with a tool and a panel, then loads it and checks both.
- [x] Make `.check-field` work on a label (`flex-direction: row`, checkbox first) and document `.checkbox-row`.
- [x] Describe the HTTP handler context (`ctx.bindJSON` and friends) in the generated API reference so the creator reads request bodies correctly.

## Stage 15: Context Budget
- [x] Cap the conversation summary with `max_summary_chars` (default 8000), keeping its newest part and a trim marker.
- [x] Clip an oversized summary on read, so a session with a bloated summary recovers on the next turn.
- [x] Expose the summary limit in the Context & Memory settings.
- [x] Summarize dropped messages only once per session, so each turn no longer re-appends the whole dropped history.

## Stage 16: Live Session View
- [x] Track the running turn in the session orchestrator: streamed text, reasoning, tool calls, and the pre-turn history length.
- [x] Expose `GET /api/core/sessions/:id/live` and report running session IDs in the sessions list.
- [x] Add a `Live` view to the dashboard: the current turn at the bottom, the newest stored messages above it, and older messages behind a "Show earlier" button.
- [x] Poll the live state while the view is open and refetch the history when a turn starts or finishes.
- [x] Cover the live state with unit tests and the endpoint with a daemon test.

## Stage 17: Release Packaging
- [x] Build Linux binaries for `i386`, `x86_64`, `armv7`, and `aarch64` from a tag.
- [x] Publish deb, rpm, and apk packages for every architecture.
- [x] Publish a zip with the binary, the default plugins, and the install/uninstall scripts.
- [x] Move the default system plugin directory to `/usr/share/cdx/plugins` and the Makefile prefix to `/usr`.
- [x] Gate the release on `go vet`, `go test ./...`, and the frontend tests.

## Stage 18: Settings Page Launcher
- [x] Add `cdx --config`: start the daemon if needed, read its HTTP address over IPC, print the URL, and open the dashboard in the default browser.
- [x] Map wildcard listeners to `127.0.0.1` and keep the command successful when no browser is available.
- [x] Cover the URL mapping and the command with tests.

## Stage 19: Remote Web Access
- [x] Add `CDX_ALLOW_REMOTE_HOST` to skip the host and token checks for browser access from other machines.
- [x] Bind the daemon to all interfaces when the variable is set and no explicit HTTP address is configured.
- [x] Expose `cdx.sys.env` to plugins and cover remote access with daemon tests.

## Stage 20: Concurrent Prompts
- [x] Resolve the active session atomically, so simultaneous first prompts share one session.
- [x] Serialize turns per session with a FIFO queue instead of corrupting history.
- [x] Announce a waiting turn with a `waiting` event and show it in the CLI.
- [x] Cover same-session serialization, arrival order, parallel sessions, cancellation, and stateless runs with tests.

## Stage 21: Client Questions
- [x] Add a validated question envelope and a per-turn asker in the core.
- [x] Expose `cdx.questions.ask` to plugins, including worker VMs, with the host stamping the source.
- [x] Route the step-limit prompt through the broker and render envelope questions in the CLI.
- [x] Cover validation, asker routing, worker calls, and CLI rendering with tests.

## Stage 22: Permission Questions
- [x] Return an `ask` question from `tool:before_call` instead of blocking the plugin VM on the client.
- [x] Add the `tool:decided` hook so a plugin persists grants and allows or denies the call.
- [x] Move the permissions plugin to the generic question envelope.
- [x] Cover the ask/decision path, denial without a decision hook, and the permissions plugin with tests.

## Stage 23: Model Questions
- [x] Add the `ask_user` tool plugin with optional choices and a free-text answer.
- [x] Run the tool in worker VMs through `cdx.questions.ask` so the control VM stays free.
- [x] Return the chosen label, the typed answer, or a cancelled flag to the model.
- [x] Cover choices, free text, and cancellation with a plugin test.

## Stage 24: Question Defaults
- [x] Resolve an option default (given, otherwise the first option) and let Enter accept it.
- [x] Fall back to the default on unrecognized input; keep permission prompts fail-closed with deny.
- [x] Let `ask_user` mark a recommended option as the default.
- [x] Cover Enter, unknown input, permission deny, free text, and recommended defaults with tests.

## Stage 25: Subagents
- [x] Track the running turn in the context: session, root, depth, workdir, preset, and model.
- [x] Create child sessions with `parent_id`, `root_id`, and `depth` without activating them.
- [x] Run a delegated turn with `Orchestrator.RunChild` and cap the depth at `1`.
- [x] Expose `cdx.agents.run` to plugins, including worker VMs, through an agent runner.
- [x] Add the `subagent` tool plugin and the built-in `subagent` preset.
- [x] Key session permission grants by the root session so they cover the tree.
- [x] Cover child sessions, depth limits, stateless rejection, the tool, and the preset with tests.

## Stage 26: Single Question Path
- [x] Remove the legacy `client:prompt_user` bus prompt and the old CLI permission renderer.
- [x] Route every client question through `cdx.questions.ask` and the envelope renderer.
- [x] Migrate the CLI end-to-end test to the question API.
- [x] Update the plugin API reference, architecture, and plugin docs.

## Stage 27: Parallel Subagents
- [x] Make worker calls take a free VM or wait in the queue instead of failing after 5 seconds.
- [x] Raise the default provider, tool, and subagent pools to 8 workers.
- [x] Add `cdx.agents.runMany` and up to 8 parallel tasks to the `subagent` tool.
- [x] Cover pool queueing, parallel child turns, and the batch tool with tests.

## Stage 28: Session Identity and Sub-session UI
- [x] Add the `subagent` session kind, bound to the parent and exempt from the temp TTL.
- [x] Store a short title from the first prompt and show it in the CLI and web session lists.
- [x] Group sub-sessions under the parent with a spoiler in the web UI.
- [x] Cascade session deletion to all children.

## Stage 29: MCP Client Core (stdio)
- [x] Add `internal/mcp`: JSON-RPC 2.0 framing, request correlation, per-request timeouts, and cancellation.
- [x] Implement the stdio transport with a process group, stderr logging, large messages, and crash detection.
- [x] Detect the protocol era with `server/discover` and the legacy `initialize` fallback, carry `_meta` on modern requests, and allow a forced revision.
- [x] Implement `tools/list` with pagination and `tools/call` with content, structured output, and error mapping.
- [x] Add a connection manager keyed by plugin and server, closed on plugin unload.
- [x] Cover legacy and modern fake servers, concurrent calls, timeout, cancellation, and crash with tests.

## Stage 30: MCP Client HTTP and Server Interaction
- [x] Implement Streamable HTTP with JSON and SSE responses, the protocol version header, and one POST per message.
- [x] Support the legacy `Mcp-Session-Id` handshake and the `2024-11-05` HTTP+SSE transport fallback.
- [x] Accept server notifications: progress, list changes, and the `subscriptions/listen` stream.
- [x] Return `input_required` results to a client input handler that retries the request with `inputResponses` and `requestState`.
- [x] Cover the HTTP transport, SSE streaming, session handoff, and notifications with tests.

## Stage 31: MCP Host API and Plugin
- [x] Expose `cdx.mcp` to plugins, including worker VMs, and close the plugin's connections on unload.
- [x] Add a deferred callback (`cdx.after`) so a plugin can refresh its catalog without blocking `setup`.
- [x] Let the registry remove tools, so a refresh drops the tools a server no longer reports.
- [x] Accept MCP JSON Schema unions and ignore unsupported keywords in the schema engine.
- [x] Add `plugins/mcp`: configuration, encrypted secrets, lazy connect, tool naming, permission mapping, and working directory rules.
- [x] Route elicitation and roots input requests through `cdx.questions` and the turn working directory.
- [x] Cover discovery, direct and gateway exposure, refresh, and permission mapping with tests.

## Stage 32: MCP Settings Page
- [x] Add a React page panel with the server list, connection status, tool catalog, and last error.
- [x] Add, edit, delete, enable, and test a server, with the exact command shown before it is saved.
- [x] Import an `mcp.json` file in the Claude Desktop and Cursor format with a review step.
- [x] Store environment values and headers as secrets and mask them in API responses.
- [x] Add refresh and reconnect actions with progress feedback.

## Stage 33: MCP Resources and Context Budget
- [x] Expose `resources/list` and `resources/read` as built-in tools.
- [x] Add the `direct`, `gateway`, and `off` exposure modes with a tool-count threshold.
- [x] Cap and truncate MCP tool descriptions and results.
- [x] Keep the system prompt and provider tool payload within the configured budget.

## Stage 34: MCP Documentation and End-to-End
- [x] Document the plugin, the server configuration, and the protocols in `MCP.md`, `PLUGINS.md`, and `README.md`.
- [x] Add a daemon test with a fake MCP server that runs a full turn calling its tool.
- [x] Verify installation and packaging of the new plugin.

## Stage 35: Plugin Lifecycle Events and Priority Bands
- [x] Publish `plugin:enabled`, `plugin:disabled`, `plugin:removed`, `plugin:installed`, and `plugins:reloaded` with the plugin state.
- [x] Shift user-installed plugins into the external order band and default a missing order to `5000`.
- [x] Renumber built-in plugins and their UI panels into the `100`-`999` band.
- [x] Cover order normalization, lifecycle events, and reload with tests.
- [x] Document the order bands and lifecycle events in `ARCHITECTURE.md` and `PLUGINS.md`.

## Stage 36: Translation Catalog Plugin
- [x] Add `plugins/i18n` with `order: 0` and the base English catalog.
- [x] Accept translation tables over `i18n:register` on the control VM and merge them by plugin priority.
- [x] Serve the merged catalog to plugins and the web shell, store the selected language, and expose the `Language` page.
- [x] Drop a plugin's tables on `plugin:disabled` and `plugin:removed`.
- [x] Cover layering, English fallback, locale switch, disable, enable, and reload with a plugin test.

## Stage 37: Web Shell Translations
- [x] Fetch the merged catalog with the dashboard status and expose `t(key)` through the UI context.
- [x] Translate navigation labels, page headers, panel titles, and JSON Schema labels.
- [x] Pass `t(key)` and the selected language to page panels through the panel API.
- [x] Move the built-in panel titles and schema labels to keys in the base catalog.
- [x] Sort the menu by the translated labels.

## Stage 38: Page and Panel Translations
- [x] Translate the General, Sessions, Plugins, Logs, and Live Session page strings.
- [x] Translate the Providers, Presets, Permissions, and MCP panel strings.
- [x] Translate message role labels, token counters, and hint labels.
- [x] Add every new English value to the base catalog.
- [x] Rebuild the web assets and keep the frontend and backend tests passing.

## Stage 39: Plugin Question Translations
- [x] Translate permission question titles, bodies, detail labels, and option labels from the catalog.
- [x] Keep English literals as fallbacks when `i18n` is unavailable.
- [x] Cover a translated permission prompt with a plugin test.
- [x] Add a frontend test that checks every literal `t()` key against the base catalog.
