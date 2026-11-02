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
