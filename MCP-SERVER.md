# MCP Server

`cdx` can act as an **MCP server**: another agent connects over HTTP, discovers the tools `cdx`
offers, and calls them. The opposite direction, where `cdx` is the host that consumes external MCP
servers, is described in [`MCP-CLIENT.md`](MCP-CLIENT.md).

The feature is owned by the `mcp-server` plugin. The core keeps only what a plugin cannot own: the
HTTP listener it already provides and one shared tool execution path with hooks.

Status: stages 40 to 43 are implemented: client tokens, both revisions of the protocol, the tools
the client's permissions allow, the approval queue with in-band elicitation, the panel, and
background tasks. Section 10 lists what is deliberately left out.

---

## 1. Scope

In scope:

* a Streamable HTTP endpoint that speaks MCP to any client: Codex, DSH, another `cdx`, Claude;
* a per-client token issued in the settings page, with permissions per class of tool;
* tools executed through the same pipeline as local calls, so `permissions` and approvals still
  apply;
* an optional task tool that runs a delegated `cdx` turn, present only when a model is configured.

Out of scope for the first version:

* OAuth authorization and dynamic client registration;
* pairing codes: a client is trusted by a token, not by an exchange between two `cdx` instances;
* server to client requests, and therefore elicitation, sampling, SSE, and `GET /mcp`;
* access to sessions other than the ones the caller started;
* a custom binary protocol and custom cryptography.

The model is a terminal, not a peer: a token grants a role on this daemon, the local user can revoke
it and kill a task at any time, and the remote side can never reach the rest of the API.

## 2. Terms

* **Client** - an agent that connects to this daemon and calls its tools.
* **Token** - the credential a client presents; it identifies the client and carries its scope.
* **Scope** - the permission classes granted to one client, each `allow`, `ask`, or `deny`.
* **Task** - a delegated `cdx` turn started through a tool call, owned by the client that started it.

## 3. Transport

MCP Streamable HTTP on one path, `/mcp`:

* legacy revision: `initialize` handshake, `Mcp-Session-Id` header, JSON replies;
* modern revision `2026-07-28`: `server/discover` answers with `supportedVersions`, every request
  carries `io.modelcontextprotocol/protocolVersion` in `_meta`, results carry `resultType`, and no
  session header is used;
* `DELETE /mcp` closes a session;
* SSE is not used: there is no server initiated notification stream, so `notifications/tools/
  list_changed` and `subscriptions/listen` are not offered.

The endpoint is served by the daemon's existing HTTP listener, so it is reachable wherever the
dashboard is and `CDX_HTTP_ADDR` decides the address. The plugin switch only enables the route:
turned off, `/mcp` answers `404` while the dashboard keeps working.

## 4. Authentication

* A token is created in the settings page, bound to a client name and a scope, and shown once.
* MCP tokens are separate from the dashboard token of the `auth` plugin: one never authenticates the
  other, and none of the dashboard behaviour (one shared token, auto-generation, the local host
  exception) applies here. The format is `cdx_mcp_<token id>_<secret>`, so a token names its client
  before lookup and a logged prefix never reveals the secret.
* The secret half is kept in the plugin secrets store, encrypted at rest, and compared in constant
  time. It is shown once at creation and never returned by the API again.
* The client id inside the token names the client in logs, in the client list, and in every audit
  line, because the modern revision carries no session header.
* A missing or wrong token returns `401` with `WWW-Authenticate: Bearer`.
* `/mcp` must **not** inherit the dashboard exception: when the daemon starts with
  `CDX_ALLOW_REMOTE_HOST`, the `auth` plugin skips its token and Host checks, and that must not
  apply here.
* A request that carries an `Origin` must have the same host as the request itself, which blocks
  DNS rebinding. An agent sends no `Origin` and is allowed.

## 5. Tools

Tool names are prefixed with `cdx_`, because a client already has its own `shell` and `read_file`
and a bare name would collide with them.

| Tool | Permission | Notes |
|---|---|---|
| `cdx_read_file` | read | capped, with an offset and a limit |
| `cdx_list_dir` | read | one directory, no recursion by default |
| `cdx_write_file` | edit | off by default for a new client |
| `cdx_shell` | dangerous | off by default for a new client |
| `cdx_web_search` | read | only when the provider plugin offers search |
| `cdx_task_*` | depends | see section 7 |

A class set to `deny` is not listed at all, `ask` is listed and needs an approval, and `allow` runs
directly. Every tool carries annotations: `readOnlyHint` for a read tool and `destructiveHint` for a
dangerous one, so a client maps it to its own permission classes instead of treating it as an edit. The list is rebuilt from the registry on every request, so a tool registered later shows
up without a restart.

Never exposed: `subagent`, question tools, plugin management, provider settings, secrets, and the
`/api` surface in general. Tools the MCP client plugin registered itself, whose names start with
`mcp_`, are hidden as well: re-exposing them would turn this server into a proxy that reaches another
server through it, and two servers connected to each other would grow a new cycle on every refresh.

### Permissions

Each client has one of `allow`, `ask`, or `deny` for each class of tool: `read`, `edit`,
`dangerous`, and `task`. A new client starts with `allow` for read, `ask` for edit, and `deny` for
dangerous and for tasks. The panel edits four rows, not a matrix over every tool.

A task does not imply `dangerous`. The session of a task sees only the classes the client has on
`allow`, so `dangerous: deny` keeps `cdx_shell` out of the task as well. The panel warns when a
client has both a task and `dangerous` on `allow`, because that is remote code execution with a model
in the middle.

A decision made for a remote call is never remembered: an approval covers one exact call and is
consumed by it. The `permissions` plugin stores an "always allow" grant for local calls, and a
remote client cannot create one, because a persistent grant is set in the panel, deliberately.

## 6. Execution

Every call runs the full local pipeline: `tool:before_call`, the approval question, `tool:decided`,
the handler, `tool:after_call`.

`Registry.CallTool` validates the schema and runs the handler only; the hooks live in the session
loop. The core therefore gains one shared `RunTool` used by both paths, and the server plugin calls
it. Without that, an MCP client silently bypasses the `permissions` plugin.

Approval for a tool call does not block the request. The plugin VM is single threaded, so a handler
that waited would also freeze the panel that has to answer it. The matrix is checked first, and
`deny` answers at once. On `ask` the flow follows the client's revision.

**Modern revision.** The server answers with `resultType: "input_required"`, a `requestState` that
is the approval id, and one `elicitation/create` request whose schema asks a single boolean. The
client asks its own user and repeats the call with `requestState` and `inputResponses`; an accepted
answer runs the tool once, a declined one returns a tool error. An approval given in the panel in
the meantime counts as accepted, and the retry keeps working.

**Legacy revision.** There is no in-band channel, so the call creates the same pending approval,
keeps the exact arguments, and returns a tool error that names the approval id and asks the client
to retry. The operator approves or denies it in the panel, and the next identical call goes through
once.

In both cases the server stays free between calls to serve the panel and other clients.

### Known limitation: the local approval cannot answer

The `permissions` plugin runs underneath every call, and its `ask` cannot reach an MCP client: no
terminal is attached to the request, so `question.Ask` returns "no interactive client" and the hook
records a denial. The tool then fails with `permission denied by user`, and the error text says that
the Permissions page must allow the class too.

So a class a remote client should use must be `allow` on the Permissions page, and the matrix of the
client decides whether the call runs at all. This stays until stage 44, where the question it raises
travels back to the client instead of turning into a denial.

## 7. Task tool

An agent that wants more than a tool call delegates a task, and `cdx` runs it with its own model and
the tools that client may use. The turn runs in the background: it survives a broken connection, a
sleeping laptop, and a restart of the client, and the client collects the result by polling.

| Tool | Returns | Notes |
|---|---|---|
| `cdx_task_start(prompt, dir?, profile?, max_steps?, timeout_s?)` | `{ task_id }` | starts a turn and returns at once |
| `cdx_task_get(task_id)` | status, result, question | polling, compact report |
| `cdx_task_answer(task_id, question_id, choice?, text?, cancelled?)` | `{ status }` | answers the pending question |
| `cdx_task_cancel(task_id)` | `{ status }` | idempotent |

There is no blocking wrapper. A handler runs on the plugin VM that also serves the panel and every
other client, so waiting inside a call would freeze all of them: polling is the only shape that
keeps the server responsive.

Rules:

* A task needs a model. Without one, `cdx_task_start` fails at once with "no model is configured on
  this daemon" and nothing is created. The tools stay listed rather than appearing and disappearing,
  because the listing cannot announce a change without an event stream.
* `profile` names a preset from the `task_presets` setting, a comma separated allowlist whose first
  entry is the default. A client cannot send preset text or a system prompt.
* `dir` must be an absolute path inside `remote_root` without `..` segments. Without `dir` the task
  gets `<remote_root>/<client id>/<random>`, created before the turn starts.
* One active task per token. A second start fails with `task_slot_busy` and the id of the running
  task. A finished task keeps its record, so its result stays readable until the next start
  replaces it, and the record lives in the plugin storage, so a reload does not leak the slot.
* The session of a task is created as kind `task` with `origin=mcp:<token id>`, and it is not made
  the active session of its directory. The local user sees and kills it in the dashboard.
* The tools of the session are filtered to the classes the client has on `allow`: `ask` and `deny`
  never reach a task, because nobody is waiting to answer a question.
* The step limit of a task is always `stop`, so a task never parks on a "continue" question.
* A task that needs a decision stops with `status: "needs_input"` and a question with its own id.
  `cdx_task_answer` resumes it. An answer of `always`, `daemon`, or `session` is downgraded to
  `once`, so a remote answer never creates a persistent grant.
* A task stops on its own timeout: fifteen minutes by default, two hours at most.
* There is no session CRUD over MCP. Session identifiers are not scoped to a client, so exposing
  them would leak another client's or the local user's conversation.

## 8. Settings page

The page is a module panel served from `/ui/mcp-server`:

* master switch, which turns the route back into `404`;
* the client list with the client id, the last time it was used, four permission selects, and
  revoke;
* issuing a token, which is shown once and never again;
* pending approvals with the client, the tool, the exact arguments, and approve or deny buttons,
  refreshed every four seconds;
* the task root and the task preset allowlist, edited as declarative settings.

## 9. Limits, logging, security

* Limits: 1 MiB request body and 100k characters per result, for the whole server; one active task
  per token; the step budget and the turn timeout are the session ones. Nothing here applies to
  local sessions or to the dashboard.
* Every call is logged by the plugin as `[Plugin:mcp-server] Client <id> called <tool>`, with the
  decision and the failure. A plugin cannot set its own log tag, so the client id is part of the
  message rather than the tag.
* A token with `cdx_shell` on `allow` is remote code execution; the panel keeps `dangerous` on
  `deny` for a new client and says what the class does next to each row.
* A delegated task is an agent with the server's tools and the server's policy. The client's prompt
  is untrusted input for that agent, so a profile never grants more than the plugin allows.
* Recursion: a task must not reach the server it runs on, and nested tasks across hosts carry a hop
  counter so two `cdx` instances cannot call each other forever.

## 10. Open questions

* OAuth: needed for clients that cannot send a static header, and the only way to get a real
  "approve this connection" prompt instead of copying a token.
* SSE and subscriptions: needed to announce `tools/list_changed` and to stream task progress, so the
  task tools can appear only when a model exists.
* Artifacts: `cdx_read_file` covers small results; binary output such as a build artifact needs a
  separate channel with its own caps.
* Multi-client task visibility: whether a local user should see a remote task's full trace in the
  session list or only in the plugin panel.

## 11. Tests

`internal/daemon/mcpserver_test.go` covers the handshake on both revisions, the token and the master
switch, the permission matrix with approvals, the task lifecycle, and the tool annotations.
`mcpserver_multi_test.go` covers two servers behind one client: catalog namespacing, parallel calls,
one server timing out without touching the other, a turn that calls both in one step, and the exact
tool payload the model receives.

`mcpserver_cascade_test.go` covers two more cases. The first is a chain of three daemons
(`cdx1 -> cdx2 -> cdx3`): the first daemon starts a task on the second, the model of the second reads
a file through the third, the middle daemon asks for an outside-folder approval, and that question
travels up to the first daemon and the answer travels back. The second case connects one client to
three servers and checks that the catalogs and the listings stay isolated and that two servers keep
working after the third one stops. Both cases also assert that a middle daemon does not pass the
tools of the next daemon through, so a chain stays a chain and not a proxy catalog.

The model test in `mcpserver_model_test.go` needs a real endpoint and is skipped unless
`CDX_TEST_MODEL_BASE`, `CDX_TEST_MODEL_KEY`, and `CDX_TEST_MODEL_MODEL` are set.
