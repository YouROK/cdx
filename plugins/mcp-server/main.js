function setup(cdx) {
  var ENABLED_KEY = "enabled";
  var CLIENTS_KEY = "clients";
  var TOKEN_PREFIX = "cdx_mcp_";
  var PROTOCOL_VERSION = "2025-06-18";
  var MODERN_VERSION = "2026-07-28";
  var SERVER_VERSION = "1.0.0";
  var TOOL_PREFIX = "cdx_";
  var RESULT_LIMIT = 100000;
  var BODY_LIMIT = 1048576;
  var SEEN_WINDOW = 60;
  var SESSION_TTL = 3600;
  var APPROVALS_KEY = "approvals";
  var APPROVAL_PENDING_TTL = 300;
  var APPROVAL_TTL = 600;
  var APPROVAL_DENIED_TTL = 60;

  // One row per class of tool, like the Permissions plugin. A new client may
  // read and must ask for everything else.
  var CLASSES = ["read", "edit", "dangerous", "task"];
  var DEFAULT_PERMISSIONS = { read: "allow", edit: "ask", dangerous: "deny", task: "deny" };
  var TASK_ROOT_KEY = "remote_root";
  var TASK_PRESETS_KEY = "task_presets";
  var DEFAULT_TASK_PRESET = "subagent";

  // Task tools are built here, not registered in the tool registry, so the
  // local agent never sees them and their names do not get a second prefix.
  var TASK_TOOLS = [
    {
      name: TOOL_PREFIX + "task_start",
      annotations: { destructiveHint: true },
      description:
        "Start a background task on this daemon. Its own model runs the task with the tools this client may use, and the task keeps running when the connection drops. Returns the task id; check it with cdx_task_get.",
      inputSchema: {
        type: "object",
        properties: {
          prompt: { type: "string", description: "Self-contained brief for the remote agent" },
          dir: { type: "string", description: "Working directory inside the remote root; created when missing" },
          profile: { type: "string", description: "Preset name from the server allowlist" },
          max_steps: { type: "integer", description: "Step budget; the session budget when omitted" },
          timeout_s: { type: "integer", description: "Wall clock limit in seconds" }
        },
        required: ["prompt"]
      }
    },
    {
      name: TOOL_PREFIX + "task_get",
      annotations: { destructiveHint: true },
      description: "Read the state of a task: running, needs_input, finished, failed, or cancelled, with its result and any pending question.",
      inputSchema: {
        type: "object",
        properties: { task_id: { type: "string", description: "Task id returned by cdx_task_start" } },
        required: ["task_id"]
      }
    },
    {
      name: TOOL_PREFIX + "task_answer",
      annotations: { destructiveHint: true },
      description: "Answer the question a task waits for. Use choice once to allow that call, deny to refuse it.",
      inputSchema: {
        type: "object",
        properties: {
          task_id: { type: "string" },
          question_id: { type: "string", description: "Question id from cdx_task_get" },
          choice: { type: "string", description: "once, deny, continue, or stop" },
          text: { type: "string", description: "Free-text answer when the question asks for one" },
          cancelled: { type: "boolean" }
        },
        required: ["task_id"]
      }
    },
    {
      name: TOOL_PREFIX + "task_cancel",
      annotations: { destructiveHint: true },
      description: "Stop a running task. Cancelling an unknown or finished task does nothing.",
      inputSchema: {
        type: "object",
        properties: { task_id: { type: "string" } },
        required: ["task_id"]
      }
    }
  ];

  // Tools that must never be reachable from another agent.
  var HIDDEN_TOOLS = { ask_user: true, subagent: true };
  // Downstream MCP tools would form a proxy chain, and a second prefix would
  // collide with the one this plugin adds.
  // Names registered by the MCP client plugin start with "mcp_" as well, and
  // re-exposing them would let a client reach another server through this one.
  var HIDDEN_PREFIXES = ["mcp__", "mcp_", TOOL_PREFIX];

  var sessions = {};
  var toolMap = {};

  // ---- helpers ----

  function now() {
    return Math.floor(Date.now() / 1000);
  }

  function isEnabled() {
    return cdx.storage.get(ENABLED_KEY) === true;
  }

  // Reads a field from a Go struct exposed through goja, where the property
  // may keep the Go name or the snake_case JSON tag.
  function pick(obj, names) {
    if (obj === null || obj === undefined) return undefined;
    for (var i = 0; i < names.length; i++) {
      var value = obj[names[i]];
      if (value !== undefined && value !== null) return value;
    }
    return undefined;
  }

  function trim(value) {
    return String(value === undefined || value === null ? "" : value).replace(/^\s+|\s+$/g, "");
  }

  // Compares without exiting on the first different character, so the time
  // does not depend on how much of the secret matched.
  function sameString(given, expected) {
    var a = String(given === undefined || given === null ? "" : given);
    var b = String(expected === undefined || expected === null ? "" : expected);
    var diff = a.length ^ b.length;
    var length = Math.max(a.length, b.length);
    for (var i = 0; i < length; i++) {
      diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
    }
    return diff === 0;
  }

  function hostName(host) {
    host = trim(host).toLowerCase();
    if (host.charAt(0) === "[") {
      var closing = host.indexOf("]");
      return closing === -1 ? host : host.slice(0, closing + 1);
    }
    var colon = host.lastIndexOf(":");
    if (colon > 0) return host.slice(0, colon);
    return host;
  }

  function originHost(origin) {
    var value = trim(origin).toLowerCase();
    var scheme = value.indexOf("://");
    if (scheme >= 0) value = value.slice(scheme + 3);
    var slash = value.indexOf("/");
    if (slash >= 0) value = value.slice(0, slash);
    return value;
  }

  // An agent sends no Origin; a browser does, and then it must match the host
  // of the request, which blocks DNS rebinding.
  function originAllowed(ctx) {
    var origin = ctx.getHeader("Origin");
    if (!trim(origin)) return true;
    return originHost(origin) === trim(ctx.host()).toLowerCase();
  }

  // ---- clients and tokens ----

  // Fills in missing permissions for a client stored before they existed.
  function normalizeClient(client) {
    if (!client.permissions || typeof client.permissions !== "object") {
      client.permissions = {};
    }
    for (var i = 0; i < CLASSES.length; i++) {
      var name = CLASSES[i];
      var value = client.permissions[name];
      if (value !== "allow" && value !== "ask" && value !== "deny") {
        client.permissions[name] = DEFAULT_PERMISSIONS[name];
      }
    }
    return client;
  }

  function loadClients() {
    var list = cdx.storage.get(CLIENTS_KEY);
    if (!list || !list.length) return [];
    for (var i = 0; i < list.length; i++) normalizeClient(list[i]);
    return list;
  }

  function saveClients(list) {
    cdx.storage.set(CLIENTS_KEY, list);
  }

  function findClient(id) {
    var list = loadClients();
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === id) return list[i];
    }
    return null;
  }

  function createClient(name, permissions) {
    var id = cdx.sys.random(8);
    var secret = cdx.sys.random(24);
    var list = loadClients();
    var record = normalizeClient({
      id: id,
      name: name,
      created_at: now(),
      last_seen_at: 0,
      last_seen_ip: ""
    });
    if (permissions && typeof permissions === "object") {
      for (var i = 0; i < CLASSES.length; i++) {
        var value = permissions[CLASSES[i]];
        if (value === "allow" || value === "ask" || value === "deny") {
          record.permissions[CLASSES[i]] = value;
        }
      }
    }
    list.push(record);
    saveClients(list);
    cdx.secrets.set("client:" + id, secret);
    return { client: record, token: TOKEN_PREFIX + id + "_" + secret };
  }

  // ---- approvals ----

  function loadApprovals() {
    var list = cdx.storage.get(APPROVALS_KEY);
    if (!list || !list.length) return [];
    var stamp = now();
    var kept = [];
    for (var i = 0; i < list.length; i++) {
      if (list[i].expires_at > stamp) kept.push(list[i]);
    }
    return kept;
  }

  function saveApprovals(list) {
    cdx.storage.set(APPROVALS_KEY, list);
  }

  // Sorts object keys, so the same arguments always produce the same key.
  function stableValue(value) {
    if (value === null || typeof value !== "object") return JSON.stringify(value);
    if (Object.prototype.toString.call(value) === "[object Array]") {
      var items = [];
      for (var i = 0; i < value.length; i++) items.push(stableValue(value[i]));
      return "[" + items.join(",") + "]";
    }
    var keys = [];
    for (var key in value) {
      if (value.hasOwnProperty(key)) keys.push(key);
    }
    keys.sort();
    var parts = [];
    for (var j = 0; j < keys.length; j++) {
      parts.push(JSON.stringify(keys[j]) + ":" + stableValue(value[keys[j]]));
    }
    return "{" + parts.join(",") + "}";
  }

  function approvalKey(clientID, tool, args) {
    return clientID + "|" + tool + "|" + stableValue(args);
  }

  function preview(value, limit) {
    var text;
    try {
      text = JSON.stringify(value);
    } catch (err) {
      text = String(value);
    }
    text = trim(text);
    if (text.length > limit) text = text.slice(0, limit) + "...";
    return text;
  }

  // Resolves one "ask" decision: an approved record lets the call through
  // once, a denied one is reported once, and the first call creates the
  // pending record the operator sees in the panel.
  function resolveApproval(client, tool, args) {
    var list = loadApprovals();
    var key = approvalKey(client.id, tool, args);
    for (var i = list.length - 1; i >= 0; i--) {
      var record = list[i];
      if (record.key !== key) continue;
      if (record.status === "approved") {
        list.splice(i, 1);
        saveApprovals(list);
        return { ok: true };
      }
      if (record.status === "denied") {
        list.splice(i, 1);
        saveApprovals(list);
        return { ok: false, reason: "the operator denied approval " + record.id };
      }
      return { ok: false, reason: "approval " + record.id + " is pending in the MCP server panel; wait for it and call the tool again" };
    }
    var stamp = now();
    var pending = {
      id: cdx.sys.random(6),
      key: key,
      client_id: client.id,
      client_name: client.name,
      tool: tool,
      arguments: preview(args, 500),
      created_at: stamp,
      expires_at: stamp + APPROVAL_PENDING_TTL,
      status: "pending"
    };
    list.push(pending);
    saveApprovals(list);
    return { ok: false, reason: "approval required: the operator must approve " + pending.id + " in the MCP server panel, then call the tool again" };
  }

  function decideApproval(id, status) {
    var list = loadApprovals();
    var stamp = now();
    for (var i = 0; i < list.length; i++) {
      if (list[i].id !== id) continue;
      if (list[i].status !== "pending") return false;
      list[i].status = status;
      list[i].decided_at = stamp;
      list[i].expires_at = stamp + (status === "approved" ? APPROVAL_TTL : APPROVAL_DENIED_TTL);
      saveApprovals(list);
      return true;
    }
    return false;
  }

  function revokeClient(id) {
    var list = loadClients();
    var kept = [];
    var found = false;
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === id) {
        found = true;
      } else {
        kept.push(list[i]);
      }
    }
    if (!found) return false;
    saveClients(kept);
    cdx.secrets.delete("client:" + id);
    return true;
  }

  function touchClient(client, ip) {
    var stamp = now();
    if (stamp - (client.last_seen_at || 0) < SEEN_WINDOW) return;
    client.last_seen_at = stamp;
    client.last_seen_ip = trim(ip);
    var list = loadClients();
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === client.id) {
        list[i].last_seen_at = client.last_seen_at;
        list[i].last_seen_ip = client.last_seen_ip;
        saveClients(list);
        return;
      }
    }
  }

  function authenticate(ctx) {
    var header = String(ctx.getHeader("Authorization") || "");
    if (header.slice(0, 7).toLowerCase() !== "bearer ") return null;
    var token = trim(header.slice(7));
    if (token.slice(0, TOKEN_PREFIX.length) !== TOKEN_PREFIX) return null;
    var rest = token.slice(TOKEN_PREFIX.length);
    var separator = rest.indexOf("_");
    if (separator <= 0) return null;
    var id = rest.slice(0, separator);
    var secret = rest.slice(separator + 1);
    var stored = cdx.secrets.get("client:" + id);
    if (!stored || !sameString(secret, stored)) return null;
    var client = findClient(id);
    if (!client) return null;
    touchClient(client, hostName(ctx.host()));
    return client;
  }

  // ---- tools ----

  function hiddenName(name) {
    if (HIDDEN_TOOLS[name]) return true;
    for (var i = 0; i < HIDDEN_PREFIXES.length; i++) {
      if (name.indexOf(HIDDEN_PREFIXES[i]) === 0) return true;
    }
    return false;
  }

  // Builds the full index of exposed tools and remembers which real tool each
  // name maps to. The catalog is filtered per client when it is listed.
  function toolIndex() {
    toolMap = {};
    var list = cdx.tools.list() || [];
    var tools = [];
    for (var i = 0; i < list.length; i++) {
      var spec = list[i];
      var name = pick(spec, ["Name", "name"]);
      if (!name || hiddenName(name)) continue;
      var declared = pick(spec, ["Permission", "permission"]) || "";
      var permission = declared === "read" || declared === "edit" || declared === "dangerous" ? declared : "dangerous";
      var exposed = TOOL_PREFIX + name;
      toolMap[exposed] = { name: name, permission: permission };
      tools.push({
        name: exposed,
        description: pick(spec, ["Description", "description"]) || "",
        inputSchema: pick(spec, ["InputSchema", "input_schema"]) || { type: "object" },
        // A client maps these hints to its own permission classes, so a read
        // tool does not look like an edit and does not ask on every call.
        annotations: {
          readOnlyHint: permission === "read",
          destructiveHint: permission === "dangerous"
        }
      });
    }
    return tools;
  }

  // Lists only the classes this client may use; a denied class is not
  // advertised at all.
  function toolsFor(client) {
    var all = toolIndex();
    var tools = [];
    for (var i = 0; i < all.length; i++) {
      var entry = toolMap[all[i].name];
      if (!entry) continue;
      var decision = client && client.permissions ? client.permissions[entry.permission] : "deny";
      if (decision === "deny") continue;
      tools.push(all[i]);
    }
    if (client && client.permissions && client.permissions.task !== "deny") {
      for (var j = 0; j < TASK_TOOLS.length; j++) tools.push(TASK_TOOLS[j]);
    }
    return tools;
  }

  function toolError(text) {
    return { content: [{ type: "text", text: text }], isError: true };
  }

  function toolResult(text) {
    return { content: [{ type: "text", text: text }], isError: false };
  }

  async function callTool(client, params, wire) {
    params = params || {};
    wire = wire || {};
    var exposed = String(params.name || "");
    var args = params.arguments || {};
    if (!toolMap[exposed]) toolIndex();
    var entry = toolMap[exposed] || taskEntry(exposed);
    if (!entry) return toolError("unknown tool: " + exposed);

    var decision = "deny";
    if (client && client.permissions && client.permissions[entry.permission]) {
      decision = client.permissions[entry.permission];
    }
    if (decision === "deny") {
      cdx.log.warn("Client " + client.id + " denied " + exposed);
      return toolError("the server policy denies " + entry.permission + " tools for this client");
    }
    if (decision === "ask") {
      if (wire.modern && wire.requestState) {
        var resumed = consumeElicitation(client, exposed, args, wire.requestState, wire.inputResponses);
        if (!resumed.ok) {
          cdx.log.info("Client " + client.id + " declined " + exposed);
          return toolError(resumed.reason);
        }
      } else if (wire.modern) {
        var pending = createPendingApproval(client, exposed, args);
        cdx.log.info("Client " + client.id + " asked for approval " + pending.id);
        return inputRequired(pending.id, exposed, args);
      } else {
        var approval = resolveApproval(client, exposed, args);
        if (!approval.ok) {
          cdx.log.info("Client " + client.id + " needs approval for " + exposed);
          return toolError(approval.reason);
        }
      }
    }

    if (entry.permission === "task") {
      cdx.log.info("Client " + client.id + " called " + exposed);
      return runTaskTool(client, exposed, args);
    }

    var text = "";
    var failed = false;
    try {
      var result = await cdx.tools.call(entry.name, args, {
        session_id: "mcp:" + client.id,
        workdir: cdx.workdir()
      });
      if (result) {
        text = result.output !== undefined && result.output !== null ? String(result.output) : "";
        if (result.error) {
          failed = true;
          if (!text) text = String(result.error);
          // The local Permissions plugin answers an "ask" with a denial when no
          // client is attached, so point the operator at the real cause.
          if (text.indexOf("permission denied") !== -1) {
            text += "\nThe Permissions page of this daemon denied the call: no one is at its terminal to answer a question, so allow the class there too.";
          }
        }
      }
    } catch (err) {
      failed = true;
      text = "error: " + (err && err.message ? err.message : String(err));
    }
    if (text.length > RESULT_LIMIT) {
      text = text.slice(0, RESULT_LIMIT) + "\n[truncated]";
    }
    cdx.log.info("Client " + client.id + " called " + exposed + (failed ? " with an error" : ""));
    return { content: [{ type: "text", text: text }], isError: failed };
  }

  // ---- modern revision ----

  // Adds the result kind the modern revision expects.
  function modernResult(result, modern) {
    if (!modern || !result || typeof result !== "object") return result;
    if (result.resultType) return result;
    var out = {};
    for (var key in result) {
      if (result.hasOwnProperty(key)) out[key] = result[key];
    }
    out.resultType = "complete";
    return out;
  }

  function createPendingApproval(client, tool, args) {
    var list = loadApprovals();
    var stamp = now();
    var record = {
      id: cdx.sys.random(6),
      key: approvalKey(client.id, tool, args),
      client_id: client.id,
      client_name: client.name,
      tool: tool,
      arguments: preview(args, 500),
      created_at: stamp,
      expires_at: stamp + APPROVAL_PENDING_TTL,
      status: "pending"
    };
    list.push(record);
    saveApprovals(list);
    return record;
  }

  // The modern revision answers in band: the server returns input_required and
  // the client repeats the call with the responses of the user it asked.
  function inputRequired(id, tool, args) {
    var requests = {};
    requests[id] = {
      method: "elicitation/create",
      params: {
        mode: "form",
        message: "Allow " + tool + " to run with these arguments: " + preview(args, 300),
        requestedSchema: {
          type: "object",
          properties: {
            approve: {
              type: "boolean",
              title: "Allow this call",
              description: "Pick yes to run it once; anything else refuses it."
            }
          },
          required: ["approve"]
        }
      }
    };
    return { resultType: "input_required", requestState: id, inputRequests: requests };
  }

  // Applies the answer of the elicitation round trip to the stored approval.
  function consumeElicitation(client, tool, args, requestState, responses) {
    var list = loadApprovals();
    var key = approvalKey(client.id, tool, args);
    for (var i = 0; i < list.length; i++) {
      var record = list[i];
      if (record.id !== requestState) continue;
      if (record.key !== key) return { ok: false, reason: "the approval does not match this call" };
      var response = responses ? responses[requestState] : null;
      if (!response && responses) {
        for (var name in responses) {
          if (responses.hasOwnProperty(name)) {
            response = responses[name];
            break;
          }
        }
      }
      var action = response && response.action ? String(response.action) : "";
      var content = response && response.content ? response.content : {};
      // An approval given in the panel counts as well.
      var approved = record.status === "approved" || (action === "accept" && content.approve === true);
      list.splice(i, 1);
      saveApprovals(list);
      if (approved) return { ok: true };
      return { ok: false, reason: "the operator did not allow this call" };
    }
    return { ok: false, reason: "the approval expired; call the tool again" };
  }

  // ---- background tasks ----

  function taskRecord(client) {
    var map = cdx.storage.get("tasks");
    if (!map || typeof map !== "object") return null;
    return map[client.id] || null;
  }

  function saveTaskRecord(client, record) {
    var map = cdx.storage.get("tasks");
    if (!map || typeof map !== "object") map = {};
    map[client.id] = record;
    cdx.storage.set("tasks", map);
  }

  // A finished task keeps its record, so its result stays readable, but only a
  // running one occupies the slot.
  function runningTask(client) {
    var record = taskRecord(client);
    if (!record) return null;
    var info = cdx.tasks.get(record.task_id);
    if (!info || !info.found) return null;
    if (info.status === "running" || info.status === "needs_input") return info;
    return null;
  }

  function remoteRoot() {
    var root = trim(cdx.storage.get(TASK_ROOT_KEY) || "");
    if (root) return root.replace(/\/+$/, "");
    var home = trim(cdx.sys.env("HOME") || "");
    return home + "/.local/state/cdx/remote";
  }

  // Resolves the working directory of a task: inside the remote root, without
  // parent segments, and created before the turn starts.
  function taskDir(client, requested) {
    var root = remoteRoot();
    var dir = trim(requested || "");
    if (!dir) {
      dir = root + "/" + client.id + "/" + cdx.sys.random(8);
    } else {
      if (dir.charAt(0) !== "/") return { error: "dir must be an absolute path" };
      if (dir.indexOf("..") !== -1) return { error: "dir must not contain .." };
      if (dir !== root && dir.indexOf(root + "/") !== 0) {
        return { error: "dir must be inside " + root };
      }
    }
    try {
      cdx.sys.writeFile(dir + "/.keep", "");
    } catch (err) {
      return { error: "cannot create " + dir + ": " + (err && err.message ? err.message : String(err)) };
    }
    return { dir: dir };
  }

  function taskPresets() {
    var raw = trim(cdx.storage.get(TASK_PRESETS_KEY) || "");
    if (!raw) raw = DEFAULT_TASK_PRESET;
    var parts = raw.split(",");
    var out = [];
    for (var i = 0; i < parts.length; i++) {
      var name = trim(parts[i]);
      if (name) out.push(name);
    }
    return out;
  }

  function pickPreset(requested) {
    var presets = taskPresets();
    var wanted = trim(requested || "");
    if (!wanted) return presets.length ? presets[0] : "";
    for (var i = 0; i < presets.length; i++) {
      if (presets[i] === wanted) return wanted;
    }
    return "";
  }

  // The task sees every tool whose class this client may use without asking,
  // because nobody answers a question inside a background turn.
  function allowedToolNames(client) {
    var all = toolIndex();
    var names = [];
    for (var i = 0; i < all.length; i++) {
      var entry = toolMap[all[i].name];
      if (entry && client.permissions[entry.permission] === "allow") names.push(entry.name);
    }
    return names;
  }

  function taskReport(info) {
    var lines = ["task " + info.task_id + ": " + info.status];
    if (info.question) {
      lines.push("question " + info.question.id + " (" + (info.question.kind || "question") + "): " + info.question.title);
      if (info.question.body) lines.push(info.question.body);
      lines.push("Answer with cdx_task_answer and question_id " + info.question.id + ".");
    }
    if (info.stopped_reason) lines.push("stopped: " + info.stopped_reason);
    if (info.error) lines.push("error: " + info.error);
    if (info.result) lines.push("", String(info.result));
    var text = lines.join("\n");
    if (text.length > RESULT_LIMIT) text = text.slice(0, RESULT_LIMIT) + "\n[truncated]";
    return text;
  }

  function startTask(client, args) {
    var running = runningTask(client);
    if (running) {
      return toolError("task_slot_busy: task " + running.task_id + " is still " + running.status + "; read it or cancel it first");
    }
    var prompt = trim(args.prompt);
    if (!prompt) return toolError("a prompt is required");
    var preset = pickPreset(args.profile);
    if (!preset) return toolError("the profile is not in the server allowlist");
    var target = taskDir(client, args.dir);
    if (target.error) return toolError(target.error);
    var info;
    try {
      info = cdx.tasks.start({
        prompt: prompt,
        workdir: target.dir,
        preset: preset,
        owner: "mcp:" + client.id,
        max_steps: Number(args.max_steps) || 0,
        timeout_s: Number(args.timeout_s) || 0,
        allowed_tools: allowedToolNames(client)
      });
    } catch (err) {
      return toolError("cannot start the task: " + (err && err.message ? err.message : String(err)));
    }
    saveTaskRecord(client, { task_id: info.task_id, started_at: now() });
    cdx.log.info("Client " + client.id + " started task " + info.task_id);
    return toolResult("task " + info.task_id + " started in " + target.dir);
  }

  function taskInfo(client, args) {
    var id = trim(args.task_id);
    if (!id) return toolError("task_id is required");
    var record = taskRecord(client);
    if (!record || record.task_id !== id) return toolError("the task belongs to another client");
    var info = cdx.tasks.get(id);
    if (!info || !info.found) return toolError("the task is not found");
    return toolResult(taskReport(info));
  }

  function answerTask(client, args) {
    var id = trim(args.task_id);
    var record = taskRecord(client);
    if (!record || record.task_id !== id) return toolError("the task belongs to another client");
    var answer = { cancelled: args.cancelled === true };
    if (args.choice) answer.choice = String(args.choice);
    if (args.text) answer.text = String(args.text);
    var result = cdx.tasks.answer(id, trim(args.question_id || ""), answer);
    if (!result.ok) return toolError(result.error || "the answer was not accepted");
    return toolResult("answer delivered to task " + id);
  }

  function cancelTask(client, args) {
    var id = trim(args.task_id);
    var record = taskRecord(client);
    if (!record || record.task_id !== id) return toolError("the task belongs to another client");
    var result = cdx.tasks.cancel(id);
    if (!result.ok) return toolError(result.error || "the task was not cancelled");
    return toolResult("task " + id + " cancelled");
  }

  // Routes the task tools, which are not part of the registry catalog.
  function taskEntry(exposed) {
    for (var i = 0; i < TASK_TOOLS.length; i++) {
      if (TASK_TOOLS[i].name === exposed) return { name: exposed, permission: "task" };
    }
    return null;
  }

  function runTaskTool(client, exposed, args) {
    if (exposed === TASK_TOOLS[0].name) return startTask(client, args);
    if (exposed === TASK_TOOLS[1].name) return taskInfo(client, args);
    if (exposed === TASK_TOOLS[2].name) return answerTask(client, args);
    return cancelTask(client, args);
  }

  // ---- JSON-RPC ----

  function rpcResult(id, result) {
    return { jsonrpc: "2.0", id: id, result: result };
  }

  function rpcError(id, code, message) {
    return { jsonrpc: "2.0", id: id === undefined ? null : id, error: { code: code, message: message } };
  }

  function pruneSessions() {
    var cutoff = now() - SESSION_TTL;
    for (var id in sessions) {
      if (sessions.hasOwnProperty(id) && sessions[id].created_at < cutoff) delete sessions[id];
    }
  }

  cdx.http.use("/mcp", function(ctx) {
    if (!isEnabled()) {
      ctx.abortWithJSON(404, { error: "the MCP server is disabled" });
      return;
    }
    var length = parseInt(ctx.getHeader("Content-Length") || "0", 10);
    if (length > BODY_LIMIT) {
      ctx.abortWithJSON(413, { error: "the request body is too large" });
      return;
    }
    if (!originAllowed(ctx)) {
      ctx.abortWithJSON(403, { error: "the Origin header does not match the request host" });
      return;
    }
    var client = authenticate(ctx);
    if (!client) {
      ctx.header("WWW-Authenticate", "Bearer");
      ctx.abortWithJSON(401, { error: "the MCP token is missing or invalid" });
      return;
    }
    ctx.set("mcp_client", client.id);
    ctx.set("mcp_client_name", client.name);
  });

  cdx.http.post("/mcp", async function(ctx) {
    var payload = null;
    try {
      payload = ctx.bindJSON();
    } catch (err) {
      ctx.json(400, rpcError(null, -32700, "the request body is not valid JSON"));
      return;
    }
    if (!payload || typeof payload !== "object" || payload.method === undefined) {
      ctx.json(400, rpcError(null, -32600, "the request is not a JSON-RPC request"));
      return;
    }

    var id = payload.id;
    var method = String(payload.method);
    var params = payload.params || {};
    var client = findClient(ctx.get("mcp_client")) || { id: ctx.get("mcp_client"), name: ctx.get("mcp_client_name") };
    var notification = id === undefined || id === null;

    var meta = params["_meta"] || {};
    var requestedVersion = String(meta["io.modelcontextprotocol/protocolVersion"] || "");
    var modern = requestedVersion === MODERN_VERSION;
    var wire = {
      modern: modern,
      requestState: String(params["requestState"] || ""),
      inputResponses: params["inputResponses"] || null
    };

    if (method === "server/discover") {
      ctx.json(200, rpcResult(id, {
        resultType: "complete",
        supportedVersions: [MODERN_VERSION, PROTOCOL_VERSION],
        capabilities: { tools: { listChanged: false } },
        serverInfo: { name: "cdx", version: SERVER_VERSION }
      }));
      return;
    }

    if (method === "initialize") {
      pruneSessions();
      var sid = "mcp-" + cdx.sys.random(16);
      sessions[sid] = { client: client.id, created_at: now() };
      ctx.header("Mcp-Session-Id", sid);
      ctx.json(200, rpcResult(id, {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: { tools: { listChanged: false } },
        serverInfo: { name: "cdx", version: SERVER_VERSION }
      }));
      return;
    }

    if (method === "notifications/initialized" || method === "notifications/cancelled") {
      ctx.status(202);
      return;
    }

    if (method === "ping") {
      ctx.json(200, rpcResult(id, modernResult({}, modern)));
      return;
    }

    if (method === "tools/list") {
      ctx.json(200, rpcResult(id, modernResult({ tools: toolsFor(client) }, modern)));
      return;
    }

    if (method === "tools/call") {
      ctx.json(200, rpcResult(id, modernResult(await callTool(client, params, wire), modern)));
      return;
    }

    if (method === "resources/list") {
      ctx.json(200, rpcResult(id, modernResult({ resources: [] }, modern)));
      return;
    }

    if (method === "prompts/list") {
      ctx.json(200, rpcResult(id, modernResult({ prompts: [] }, modern)));
      return;
    }

    if (notification) {
      ctx.status(202);
      return;
    }
    ctx.json(200, rpcError(id, -32601, "method not found: " + method));
  });

  cdx.http.delete("/mcp", function(ctx) {
    var sid = trim(ctx.getHeader("Mcp-Session-Id"));
    if (sid && sessions[sid]) delete sessions[sid];
    ctx.status(204);
  });

  // This version has no server to client stream, so GET is not offered.
  cdx.http.get("/mcp", function(ctx) {
    ctx.header("Allow", "POST, DELETE");
    ctx.json(405, { error: "the MCP server does not offer an event stream" });
  });

  // ---- settings API ----

  cdx.http.get("/api/plugins/mcp-server/clients", function(ctx) {
    ctx.json(200, { enabled: isEnabled(), clients: loadClients() });
  });

  cdx.http.post("/api/plugins/mcp-server/clients", function(ctx) {
    var body = ctx.bindJSON() || {};
    var name = trim(body.name);
    if (!name) {
      ctx.json(400, { error: "a client name is required" });
      return;
    }
    if (name.length > 64) {
      ctx.json(400, { error: "the client name is too long" });
      return;
    }
    var created = createClient(name, body.permissions);
    cdx.log.info("Issued an MCP token for client " + created.client.name);
    ctx.json(201, created);
  });

  cdx.http.delete("/api/plugins/mcp-server/clients/:id", function(ctx) {
    var id = ctx.param("id");
    if (!revokeClient(id)) {
      ctx.json(404, { error: "the client does not exist" });
      return;
    }
    cdx.log.info("Revoked the MCP token " + id);
    ctx.status(204);
  });

  cdx.http.post("/api/plugins/mcp-server/clients/:id/permissions", function(ctx) {
    var id = ctx.param("id");
    var body = ctx.bindJSON() || {};
    var list = loadClients();
    var found = null;
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === id) {
        found = normalizeClient(list[i]);
        break;
      }
    }
    if (!found) {
      ctx.json(404, { error: "the client does not exist" });
      return;
    }
    for (var j = 0; j < CLASSES.length; j++) {
      var name = CLASSES[j];
      var value = body[name];
      if (value === undefined) continue;
      if (value !== "allow" && value !== "ask" && value !== "deny") {
        ctx.json(400, { error: "the permission of " + name + " must be allow, ask, or deny" });
        return;
      }
      found.permissions[name] = value;
    }
    saveClients(list);
    ctx.json(200, { client: found });
  });

  cdx.http.get("/api/plugins/mcp-server/approvals", function(ctx) {
    var list = loadApprovals();
    var pending = [];
    for (var i = 0; i < list.length; i++) {
      if (list[i].status === "pending") pending.push(list[i]);
    }
    ctx.json(200, { approvals: pending });
  });

  cdx.http.post("/api/plugins/mcp-server/approvals/:id/:decision", function(ctx) {
    var decision = String(ctx.param("decision") || "");
    if (decision !== "approve" && decision !== "deny") {
      ctx.json(400, { error: "the decision must be approve or deny" });
      return;
    }
    var status = decision === "approve" ? "approved" : "denied";
    if (!decideApproval(ctx.param("id"), status)) {
      ctx.json(404, { error: "the approval does not exist or was already decided" });
      return;
    }
    cdx.log.info("Approval " + ctx.param("id") + " was " + status);
    ctx.json(200, { status: status });
  });

  cdx.http.static("/ui/mcp-server", "./web");

  cdx.ui.register({
    title: "mcpserver.Title",
    description: "mcpserver.Description",
    order: 430,
    entry: "/ui/mcp-server/dist/panel.js",
    schema: {
      type: "object",
      title: "mcpserver.Schema.Title",
      properties: {
        enabled: {
          type: "boolean",
          title: "mcpserver.Schema.Enabled",
          description: "mcpserver.Schema.EnabledHint",
          default: false
        },
        remote_root: {
          type: "string",
          title: "mcpserver.Schema.RemoteRoot",
          description: "mcpserver.Schema.RemoteRootHint",
          default: ""
        },
        task_presets: {
          type: "string",
          title: "mcpserver.Schema.TaskPresets",
          description: "mcpserver.Schema.TaskPresetsHint",
          default: "subagent"
        }
      }
    }
  });
}
