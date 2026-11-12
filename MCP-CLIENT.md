# MCP Client

`cdx` acts as an **MCP host**: it connects to external MCP servers, discovers their tools, and lets
the model call them. The feature is owned by the `mcp` plugin. The core provides the transport and
JSON-RPC machinery, because a plugin cannot own the connection lifecycle itself.

This document covers the client direction only: `cdx` consumes tools from other MCP servers. The
opposite direction, where another agent uses `cdx` as an MCP server, is described in
[`MCP-SERVER.md`](MCP-SERVER.md).

---

## 1. Terms

MCP (Model Context Protocol) is a JSON-RPC 2.0 protocol between a **host** and a **server**:

```text
cdx daemon (host)                          external programs and services
  MCP client  --stdio (stdin/stdout)-->  npx @modelcontextprotocol/server-filesystem
  MCP client  --Streamable HTTP------->  https://mcp.example.com/mcp
```

* **Server** is the side that exposes tools, resources, and prompts. It is a process launched by
  `cdx` or an HTTP endpoint. It is not a `cdx` component and nothing is started on the `cdx` side.
* **Client** is the connector inside `cdx` that talks to one server.
* **Host** is the `cdx` daemon that owns the clients, the configuration, and the model loop.

A server is added once, in the MCP settings page, exactly like a provider is added once. After
that its tools appear to the model in every session.

Supported transports:

1. **stdio** - `cdx` starts the command, sends one JSON-RPC message per line to its stdin, and reads
   responses from its stdout. stderr is logged and never parsed.
2. **Streamable HTTP** - every message is a POST to one endpoint; the reply is JSON or an SSE stream
   scoped to that request.

---

## 2. Protocol revisions

The protocol changed shape in revision `2026-07-28`:

| | Legacy (`2025-11-25` and earlier) | Modern (`2026-07-28` and later) |
|---|---|---|
| Session | `initialize` + `notifications/initialized` | none, every request carries `_meta` |
| Version | chosen by `initialize` | `io.modelcontextprotocol/protocolVersion` in `_meta` and `MCP-Protocol-Version` |
| Discovery | capabilities from `initialize` | `server/discover` |
| Server to client | JSON-RPC requests (`roots/list`, `sampling/createMessage`, `elicitation/create`) | `resultType: "input_required"` with `inputRequests`, retried with `inputResponses` |
| Change notifications | `notifications/*/list_changed` | `subscriptions/listen` stream |
| HTTP session | `Mcp-Session-Id` header | no session header |

`cdx` is **dual-era**: it speaks both, because most published servers still implement the handshake.
Detection is `server/discover` first, falling back to `initialize` when the server answers with
`-32601` or an unsupported-version error. The server entry can force a revision with
`"protocol": "2025-06-18"` when detection picks wrong.

A result without `resultType` is treated as `"complete"`, as the specification requires for
older servers.

---

## 3. Architecture

```text
plugins/mcp (JS)                 internal/mcp (Go)                external
  config + secrets   -->  Manager (per-plugin connection pool)
  tool registration  <--  Client (JSON-RPC correlation)  <---->  stdio process
  settings page             transport (stdio / HTTP)             HTTP endpoint
```

* `internal/mcp` owns framing, request correlation, timeouts, cancellation, process lifetime, and
  protocol-era detection.
* `cdx.mcp` exposes the client to plugins and returns promises for blocking calls, like
  `cdx.questions` and `cdx.agents`. A tool handler therefore waits at `await` while the plugin VM
  keeps serving other work.
* `plugins/mcp` owns configuration, secrets, discovery policy, tool naming, permission mapping, and
  the settings page. It registers tools and forwards calls.

The split is deliberate: the core learns one protocol binding (like it owns the HTTP router and the
process runner), while all MCP policy stays in the plugin.

---

## 4. Server configuration

Stored in the plugin plain storage for the `mcp` plugin; secret values are stored in its encrypted
secrets and are never returned by the API.

```json
{
  "servers": [
    {
      "id": "github",
      "name": "GitHub",
      "enabled": true,
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_PERSONAL_ACCESS_TOKEN": "secret:env:GITHUB_PERSONAL_ACCESS_TOKEN" },
      "cwd": "",
      "protocol": "auto",
      "timeout_ms": 120000,
      "expose": "direct",
      "permission": "auto"
    },
    {
      "id": "docs",
      "name": "Docs",
      "enabled": true,
      "transport": "http",
      "url": "https://mcp.example.com/mcp",
      "headers": { "Authorization": "secret:header:Authorization" }
    }
  ]
}
```

Field rules:

* `id` - lowercase latin, digits, dash, underscore; at most 32 characters; immutable after creation,
  because it is part of every exposed tool name and every saved permission grant.
* `transport` - `stdio` or `http`.
* `stdio` uses `command`, optional `args`, optional `env`, optional `cwd`.
* `http` uses `url` and optional `headers`.
* `cwd` - empty means the active session working directory (see section 6).
* `protocol` - `auto` (default), or a fixed revision such as `2026-07-28` or `2025-06-18`.
* `timeout_ms` - per-request timeout, default `120000`; `0` means the default.
* `expose` - `auto` (default), `direct`, `gateway`, or `off` (see section 5).
* `permission` - `auto`, `read`, `edit`, or `dangerous` (see section 5).

`enabled: false` keeps the entry but never connects.

---

## 5. Tools

### Naming

An MCP tool is exposed as `mcp__<server>__<tool>`. The server and tool parts are sanitized to
`[A-Za-z0-9_-]`, the total length is capped at 64 bytes, and a truncated name gets a short hash
suffix so two long names do not collide. The mapping `exposed name -> server/tool` is stored with the
cached catalog, so a refresh does not rename existing tools.

Tools from different servers never collide, and neither do they collide with the built-in tools
(`read_file`, `shell`, ...).

### Exposure modes

* `auto` (default) - `direct` up to 30 tools, `gateway` above that.
* `direct` - one tool per MCP tool. Best for a small server.
* `gateway` - one tool, `mcp_call(server, tool, arguments)`, plus `mcp_find_tools` and
  `mcp_list_tools`. The model loads the catalog on demand, because the tool list is repeated in the
  system prompt and in every provider request.
* `off` - connected and browsable in the settings page, but not offered to the model.

Automatic exposure also keeps the total number of direct tools across servers at 64: servers beyond
that budget switch to the gateway, so a few large servers cannot flood the context. Explicit
`direct` entries are honored anyway.

Each direct tool adds its JSON schema to every provider request, so the threshold trades context
for discovery. Weak models pick better from a short list; a server with a few dozen tools is still
manageable, and the settings page allows `direct` for a server whose catalog is worth it.

### Tool discovery

`mcp_find_tools(query, server?)` matches in this order:

1. the exact tool name;
2. the exact name without separators, so `Doc_New` and `doc.new` both match `doc_new`;
3. the name prefix;
4. words in any order in the name or description; a name match weighs more than a description
   match.

A one-letter query never runs the word search: it returns a hint to use `mcp_list_tools`, which
lists every tool (optionally for one server). This prevents the model from scanning the alphabet
with single letters.

### Instructions

A server may report `instructions` during the handshake. The plugin appends them to the system
prompt in `session:before_turn` under `## MCP server instructions`, capped at 2000 characters per
server and 6000 in total. The text is declared by the server, so it is untrusted input: it can
guide the model, but it cannot grant permissions or bypass the permission rules.

### Loop guard

The session loop counts tool calls per turn by tool name plus arguments with numbers replaced, so
`layer: 1` and `layer: 2` share a signature. When one signature reaches 10 calls, the client gets a
`loop` question: continue, or stop the turn. Stopping writes an error tool result for the pending
call and ends the turn with `loop_detected`; continuing resets the counter, so the question can
return after another 10 calls. Without an interactive client the turn stops.

### Permissions

Annotations are hints declared by the server, not guarantees:

* `readOnlyHint: true` -> `read`;
* `destructiveHint` or `openWorldHint` -> `dangerous`;
* otherwise -> `edit`.

The server entry can override the result with `permission`. Per-tool rules live in the
`permissions` plugin, keyed by the exposed name.

### Results

`tools/call` results are converted to plain text for the model:

* `content[]` text parts are joined with a blank line;
* `resource_link` parts become a list of URIs;
* `image` and `audio` parts cannot be shown to the model, so they are written to a temporary file
  and reported as a path with a note;
* `structuredContent` is appended as JSON;
* `isError: true` becomes a tool error, so the model sees the server message as an error;
* the whole result is clipped to `max_result_chars` (default `100000`).

---

## 6. Working directory and roots

A stdio server is started in the **active session working directory** unless `cwd` is set. This is
the same directory the agent runs in and the same one the local tools use, so file-based servers
(`filesystem`, `git`, `sqlite`, and similar) resolve relative paths the way the user expects.

* The connection is lazy: nothing is started at daemon start, the first call of a turn starts the
  process in that turn's working directory.
* When a call arrives from a different working directory and `cwd` is not fixed, the process is
  restarted in the new directory. A server that must keep its own directory sets `cwd` explicitly.
* The client advertises the working directory as an MCP **root** during initialization, so servers
  that support roots can resolve the project without hardcoded paths.
* `cdx` does not rewrite tool arguments. If a tool takes a path, the model passes it; the tool
  description and the root tell the server which project is meant.

HTTP servers have no process and no directory; they receive the same roots and the working directory
in the initialization metadata.

---

## 7. Lifecycle

* **Connect** - on the first tool call, on an explicit "Connect" in the settings page, or on a
  background refresh when the catalog is empty.
* **Refresh** - `tools/list` runs at connect and again on a list-changed notification. The plugin
  re-registers the catalog and removes tools the server no longer reports.
* **Crash** - a server that exits is marked `error` with its exit status. The next call retries the
  connection with backoff, so a broken server does not block the turn.
* **Workdir change** - a lazy stdio server restarts in the new directory unless `cwd` is fixed.
* **Idle** - connections with no calls for 30 minutes are closed and reopened on demand.
* **Unload** - disabling or removing the plugin closes all of its connections and kills its
  processes.

---

## 8. Timeouts, cancellation, and limits

* A request runs under the turn context: cancelling the turn cancels the MCP request and sends
  `notifications/cancelled` to servers that support it.
* Every request has `timeout_ms` (default 120 s) and the connection has a startup timeout of 30 s.
* The line reader accepts up to 16 MiB per message.
* A server can handle several calls at once; the core multiplexes them by request id. Per-server
  concurrency is capped at 8 in flight.

---

## 9. Security

* A stdio server is an arbitrary program running with the daemon's privileges. Adding one is a
  deliberate trust decision, so the settings page shows the exact command and the environment it
  receives before it is enabled.
* Secrets (env values, HTTP headers) live in the plugin's encrypted store and are masked in API
  responses.
* Names and values of secrets never reach the logs; server stderr is logged with the
  `[MCP:<id>]` tag and is treated as untrusted text.
* HTTP redirects are not followed across hosts, so an authorization header cannot leak to a
  different origin.
* OAuth for remote servers is out of scope for the first version; a static header covers most
  self-hosted deployments.

---

## 10. Client features

| Feature | First version | Notes |
|---|---|---|
| Tools (`tools/list`, `tools/call`) | yes | including pagination, annotations, structured output |
| Progress (`notifications/progress`) | yes | logged, and forwarded to the live turn view |
| Cancellation | yes | turn context and `notifications/cancelled` |
| List-changed notifications | yes | re-registers the tool catalog |
| Roots | yes | the working directory of the turn |
| Elicitation / `input_required` | yes | routed through `cdx.questions`, so the user answers in the client |
| Resources | yes | `mcp_list_resources` and `mcp_read_resource` tools over `resources/list` and `resources/read` |
| Prompts | later | needs a command surface that `cdx` does not have yet |
| Sampling | later | server asks the model to complete; needs a provider path in the core |
| Tasks extension | no | experimental extension, not needed for tools |

---

## 11. Out of scope

* `cdx` as an MCP server for other agents. That is a separate plugin with its own transport and
  security model, described in [`MCP-SERVER.md`](MCP-SERVER.md).
* Importing servers from a URL or the public registry. The first version imports a local
  `mcp.json` (Claude Desktop and Cursor format) and lets the user review the commands.
