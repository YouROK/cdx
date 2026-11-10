function setup(cdx) {
  var CONFIG_KEY = "servers";
  var CATALOG_PREFIX = "catalog:";
  var EXPOSED_KEY = "exposed";
  var STATUS_KEY = "status";
  var RESULT_LIMIT = 100000;
  var DESCRIPTION_LIMIT = 800;
  var AUTO_GATEWAY_THRESHOLD = 30;
  var MAX_DIRECT_TOOLS = 64;

  // ---- configuration ----

  function loadServers() {
    var list = cdx.storage.get(CONFIG_KEY);
    return list && list.length ? list : [];
  }

  function saveServers(list) {
    cdx.storage.set(CONFIG_KEY, list);
  }

  function findServer(id) {
    var list = loadServers();
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === id) return list[i];
    }
    return null;
  }

  function slugID(value) {
    var id = String(value || "").toLowerCase().replace(/[^a-z0-9_-]+/g, "_").replace(/^_+|_+$/g, "");
    return id.slice(0, 32);
  }

  function secretName(serverID, kind, name) {
    return "server:" + serverID + ":" + kind + ":" + name;
  }

  function secretValues(server) {
    var out = { env: {}, headers: {} };
    var envNames = server.env_secrets || [];
    for (var i = 0; i < envNames.length; i++) {
      var envValue = cdx.secrets.get(secretName(server.id, "env", envNames[i]));
      if (envValue) out.env[envNames[i]] = envValue;
    }
    var headerNames = server.header_secrets || [];
    for (var j = 0; j < headerNames.length; j++) {
      var headerValue = cdx.secrets.get(secretName(server.id, "header", headerNames[j]));
      if (headerValue) out.headers[headerNames[j]] = headerValue;
    }
    return out;
  }

  function mergedEnv(server) {
    var out = {};
    var plain = server.env || {};
    for (var key in plain) {
      if (plain.hasOwnProperty(key)) out[key] = plain[key];
    }
    var secrets = secretValues(server).env;
    for (var name in secrets) {
      if (secrets.hasOwnProperty(name)) out[name] = secrets[name];
    }
    return out;
  }

  function mergedHeaders(server) {
    var out = {};
    var plain = server.headers || {};
    for (var key in plain) {
      if (plain.hasOwnProperty(key)) out[key] = plain[key];
    }
    var secrets = secretValues(server).headers;
    for (var name in secrets) {
      if (secrets.hasOwnProperty(name)) out[name] = secrets[name];
    }
    return out;
  }

  function hostConfig(server) {
    var cfg = {
      id: server.id,
      name: server.name || server.id,
      transport: server.transport === "http" ? "http" : "stdio",
      protocol: server.protocol || "auto",
      timeout_ms: Number(server.timeout_ms) || 0
    };
    if (cfg.transport === "http") {
      cfg.url = server.url || "";
      cfg.headers = mergedHeaders(server);
    } else {
      cfg.command = server.command || "";
      cfg.args = server.args || [];
      cfg.env = mergedEnv(server);
      if (server.cwd) cfg.cwd = server.cwd;
    }
    return cfg;
  }

  // applySecrets stores new values, drops removed names, and returns the kept names.
  function applySecrets(serverID, kind, input, previous, key) {
    var oldNames = (previous && previous[key]) || [];
    var names = [];

    if (input && typeof input === "object" && !Array.isArray(input)) {
      for (var name in input) {
        if (!input.hasOwnProperty(name) || !name) continue;
        var value = input[name];
        if (typeof value === "string" && value !== "") {
          cdx.secrets.set(secretName(serverID, kind, name), value);
          names.push(name);
        } else if (cdx.secrets.has(secretName(serverID, kind, name))) {
          names.push(name);
        }
      }
      for (var i = 0; i < oldNames.length; i++) {
        if (names.indexOf(oldNames[i]) < 0) {
          cdx.secrets.delete(secretName(serverID, kind, oldNames[i]));
        }
      }
    } else if (Array.isArray(input)) {
      names = input.slice();
      for (var j = 0; j < oldNames.length; j++) {
        if (names.indexOf(oldNames[j]) < 0) {
          cdx.secrets.delete(secretName(serverID, kind, oldNames[j]));
        }
      }
    } else {
      names = oldNames.slice();
    }
    names.sort();
    return names;
  }

  function normalizeServer(raw, previous) {
    var server = {
      id: slugID(raw.id || raw.name),
      name: String(raw.name || raw.id || "").trim(),
      enabled: raw.enabled !== false,
      transport: raw.transport === "http" ? "http" : "stdio",
      protocol: String(raw.protocol || "auto"),
      timeout_ms: Math.max(0, Math.floor(Number(raw.timeout_ms) || 0)),
      expose: ["auto", "direct", "gateway", "off"].indexOf(raw.expose) >= 0 ? raw.expose : "auto",
      permission: "auto"
    };
    if (!server.id) throw new Error("server id is required");
    if (["read", "edit", "dangerous"].indexOf(raw.permission) >= 0) {
      server.permission = raw.permission;
    }

    if (server.transport === "http") {
      server.url = String(raw.url || "").trim();
      if (!server.url) throw new Error("server " + server.id + " needs a url");
      server.headers = {};
      var headers = raw.headers || {};
      for (var header in headers) {
        if (headers.hasOwnProperty(header) && typeof headers[header] === "string") {
          server.headers[header] = headers[header];
        }
      }
      server.header_secrets = applySecrets(server.id, "header", raw.header_secrets, previous, "header_secrets");
    } else {
      server.command = String(raw.command || "").trim();
      if (!server.command) throw new Error("server " + server.id + " needs a command");
      server.args = [];
      var args = raw.args || [];
      for (var i = 0; i < args.length; i++) server.args.push(String(args[i]));
      server.cwd = String(raw.cwd || "").trim();
      server.env = {};
      var env = raw.env || {};
      for (var name in env) {
        if (env.hasOwnProperty(name) && typeof env[name] === "string") server.env[name] = env[name];
      }
      server.env_secrets = applySecrets(server.id, "env", raw.env_secrets, previous, "env_secrets");
    }
    return server;
  }

  function removeServerSecrets(server) {
    var envNames = server.env_secrets || [];
    for (var i = 0; i < envNames.length; i++) {
      cdx.secrets.delete(secretName(server.id, "env", envNames[i]));
    }
    var headerNames = server.header_secrets || [];
    for (var j = 0; j < headerNames.length; j++) {
      cdx.secrets.delete(secretName(server.id, "header", headerNames[j]));
    }
  }

  function saveServersInput(input) {
    var current = loadServers();
    var previous = {};
    for (var i = 0; i < current.length; i++) previous[current[i].id] = current[i];

    var rawList = (input && input.servers) || [];
    var list = [];
    var seen = {};
    for (var j = 0; j < rawList.length; j++) {
      var raw = rawList[j] || {};
      var server = normalizeServer(raw, previous[slugID(raw.id || raw.name)]);
      if (seen[server.id]) throw new Error("duplicate server id " + server.id);
      seen[server.id] = true;
      list.push(server);
    }
    for (var id in previous) {
      if (previous.hasOwnProperty(id) && !seen[id]) removeServerSecrets(previous[id]);
    }
    list.sort(function (a, b) { return a.id < b.id ? -1 : a.id > b.id ? 1 : 0; });
    saveServers(list);
    return list;
  }

  // ---- tools ----

  function ensure(serverID) {
    var status = cdx.mcp.status(serverID);
    if (status && status.connected) return status;
    var server = findServer(serverID);
    if (!server) throw new Error("MCP server " + serverID + " is not configured");
    if (!server.enabled) throw new Error("MCP server " + serverID + " is disabled");
    var info = cdx.mcp.connect(hostConfig(server));
    cdx.storage.set(CATALOG_PREFIX + serverID, info.tools || []);
    return info;
  }

  function hash6(text) {
    var hash = 2166136261;
    for (var i = 0; i < text.length; i++) {
      hash ^= text.charCodeAt(i);
      hash = (hash * 16777619) >>> 0;
    }
    return ("000000" + hash.toString(16)).slice(-6);
  }

  function exposedName(serverID, toolName) {
    var server = slugID(serverID);
    var tool = String(toolName || "tool").replace(/[^A-Za-z0-9_-]/g, "_");
    var base = "mcp_" + server + "_" + tool;
    if (base.length > 64) {
      base = base.slice(0, 57) + "_" + hash6(serverID + ":" + toolName);
    }
    return base;
  }

  function permissionFor(server, tool) {
    if (server.permission && server.permission !== "auto") return server.permission;
    var annotations = (tool && tool.annotations) || {};
    if (annotations.readOnlyHint === true) return "read";
    if (annotations.destructiveHint === true || annotations.openWorldHint === true) return "dangerous";
    return "edit";
  }

  function describeTool(tool) {
    var text = String(tool.description || tool.title || tool.name || "");
    if (text.length > DESCRIPTION_LIMIT) text = text.slice(0, DESCRIPTION_LIMIT - 3) + "...";
    return text;
  }

  function resourceText(resource) {
    if (!resource) return "";
    if (resource.text) return String(resource.text);
    if (resource.blob) {
      return "[binary resource " + (resource.mimeType || "unknown") + ", " + String(resource.blob).length + " bytes]";
    }
    return JSON.stringify(resource).slice(0, 2000);
  }

  function toolResult(result) {
    var parts = [];
    var content = (result && result.content) || [];
    for (var i = 0; i < content.length; i++) {
      var item = content[i] || {};
      if (item.type === "text") parts.push(String(item.text || ""));
      else if (item.type === "resource_link") parts.push("Resource: " + (item.uri || ""));
      else if (item.type === "image" || item.type === "audio") {
        parts.push("[" + item.type + " " + (item.mimeType || "unknown") + " omitted]");
      } else if (item.type === "resource") parts.push(resourceText(item.resource));
      else if (item.text) parts.push(String(item.text));
    }
    if (result && result.structuredContent) {
      parts.push(JSON.stringify(result.structuredContent));
    }
    var text = parts.join("\n\n").trim();
    if (result && result.isError) throw new Error(text || "MCP tool failed");
    if (!text) text = "(no output)";
    if (text.length > RESULT_LIMIT) text = text.slice(0, RESULT_LIMIT) + "\n... (truncated)";
    return text;
  }

  function registerDirect(server, tool) {
    var name = exposedName(server.id, tool.name);
    cdx.tools.register({
      name: name,
      description: describeTool(tool),
      permission: permissionFor(server, tool),
      input_schema: tool.inputSchema || { type: "object" }
    }, cdx.inWorker(function (args) {
      ensure(server.id);
      return toolResult(cdx.mcp.call(server.id, tool.name, args || {}));
    }));
    return name;
  }

  function registerGateway(servers) {
    var ids = [];
    for (var i = 0; i < servers.length; i++) ids.push(servers[i].id);
    cdx.tools.register({
      name: "mcp_call",
      description: "Call a tool of a connected MCP server. Get the server id and tool name from mcp_find_tools first.",
      permission: "edit",
      input_schema: {
        type: "object",
        properties: {
          server: { type: "string", description: "MCP server id", enum: ids },
          tool: { type: "string", description: "Tool name reported by the server" },
          arguments: { type: "object", description: "Arguments for the tool" }
        },
        required: ["server", "tool"]
      }
    }, cdx.inWorker(function (args) {
      var serverID = String(args.server || "");
      var toolName = String(args.tool || "");
      if (!serverID || !toolName) throw new Error("server and tool are required");
      ensure(serverID);
      return toolResult(cdx.mcp.call(serverID, toolName, args.arguments || {}));
    }));
    return "mcp_call";
  }

  // normalizeName compares tool names without separators: Doc_New == doc.new.
  function normalizeName(value) {
    return String(value || "").toLowerCase().replace(/[^a-z0-9]+/g, "");
  }

  function toolRows(serverID) {
    var list = loadServers();
    var rows = [];
    for (var i = 0; i < list.length; i++) {
      var server = list[i];
      if (!server.enabled) continue;
      if (serverID && server.id !== serverID) continue;
      var tools = cdx.storage.get(CATALOG_PREFIX + server.id) || [];
      for (var j = 0; j < tools.length; j++) {
        var tool = tools[j] || {};
        rows.push({
          server: server.id,
          name: String(tool.name || ""),
          description: String(tool.description || "")
        });
      }
    }
    return rows;
  }

  function searchResult(rows, match) {
    var out = [];
    for (var i = 0; i < rows.length && out.length < 40; i++) {
      out.push({
        server: rows[i].server,
        name: rows[i].name,
        description: rows[i].description.slice(0, 200)
      });
    }
    return { tools: out, count: rows.length, match: match, truncated: rows.length > out.length };
  }

  function findTools(query, serverID) {
    var text = String(query || "").trim();
    var lower = text.toLowerCase();
    var normalized = normalizeName(text);
    var rows = toolRows(serverID);

    // 1. Exact name.
    var matched = rows.filter(function (row) { return row.name.toLowerCase() === lower; });
    if (matched.length) return searchResult(matched, "exact");

    // 2. Exact name without separators.
    if (normalized) {
      matched = rows.filter(function (row) { return normalizeName(row.name) === normalized; });
      if (matched.length) return searchResult(matched, "normalized");
    }

    // 3. Name prefix.
    if (lower) {
      matched = rows.filter(function (row) {
        return row.name.toLowerCase().indexOf(lower) === 0 ||
          (normalized && normalizeName(row.name).indexOf(normalized) === 0);
      });
      if (matched.length) return searchResult(matched, "prefix");
    }

    // 4. Words in any order, name matches weigh more than descriptions.
    var words = lower.split(/\s+/).filter(function (word) { return normalizeName(word).length >= 2; });
    if (!words.length) {
      return { tools: [], count: 0, match: "none", hint: "Use mcp_list_tools to list every tool of a server." };
    }
    var scored = [];
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      var name = normalizeName(row.name);
      var description = normalizeName(row.description);
      var score = 0;
      var all = true;
      for (var w = 0; w < words.length; w++) {
        var word = normalizeName(words[w]);
        var inName = name.indexOf(word) >= 0;
        var inDescription = description.indexOf(word) >= 0;
        if (!inName && !inDescription) {
          all = false;
          break;
        }
        score += inName ? 2 : 1;
      }
      if (all) scored.push({ row: row, score: score });
    }
    scored.sort(function (a, b) {
      if (a.score !== b.score) return b.score - a.score;
      return a.row.name < b.row.name ? -1 : a.row.name > b.row.name ? 1 : 0;
    });
    var out = [];
    for (var k = 0; k < scored.length; k++) out.push(scored[k].row);
    return searchResult(out, "words");
  }

  function listTools(serverID) {
    var rows = toolRows(serverID);
    var out = [];
    for (var i = 0; i < rows.length && out.length < 200; i++) {
      out.push({
        server: rows[i].server,
        name: rows[i].name,
        description: rows[i].description.slice(0, 160)
      });
    }
    return { tools: out, count: rows.length, truncated: rows.length > out.length };
  }

  function registerFindTools() {
    cdx.tools.register({
      name: "mcp_find_tools",
      description: "Find a tool by name. An exact name matches first, then a name without separators, then a name prefix, and only then words in the name or description. Use mcp_list_tools to see every tool.",
      permission: "read",
      input_schema: {
        type: "object",
        properties: {
          query: { type: "string", description: "Tool name or words to search for" },
          server: { type: "string", description: "Optional MCP server id" }
        },
        required: ["query"]
      }
    }, cdx.inWorker(function (args) {
      return findTools(String(args.query || ""), String(args.server || ""));
    }));
    return "mcp_find_tools";
  }

  function registerListTools() {
    cdx.tools.register({
      name: "mcp_list_tools",
      description: "List every tool of the configured MCP servers, or of one server",
      permission: "read",
      input_schema: {
        type: "object",
        properties: { server: { type: "string", description: "Optional MCP server id" } }
      }
    }, cdx.inWorker(function (args) {
      return listTools(String(args.server || ""));
    }));
    return "mcp_list_tools";
  }

  function unregisterTools() {
    var names = cdx.storage.get(EXPOSED_KEY) || [];
    for (var i = 0; i < names.length; i++) cdx.tools.remove(names[i]);
    cdx.storage.set(EXPOSED_KEY, []);
  }

  function exposureFor(server) {
    var mode = server.expose || "auto";
    if (mode !== "auto") return mode;
    var tools = cdx.storage.get(CATALOG_PREFIX + server.id) || [];
    return tools.length > AUTO_GATEWAY_THRESHOLD ? "gateway" : "direct";
  }

  function registerTools() {
    var exposed = [];
    if (!cdx.worker) unregisterTools();

    var list = loadServers();
    var gateway = [];
    var visible = 0;
    var directCount = 0;
    for (var i = 0; i < list.length; i++) {
      var server = list[i];
      if (!server.enabled) continue;
      var mode = exposureFor(server);
      if (mode === "off") continue;
      visible++;
      var tools = cdx.storage.get(CATALOG_PREFIX + server.id) || [];
      if (mode === "gateway" || (server.expose !== "direct" && directCount + tools.length > MAX_DIRECT_TOOLS)) {
        gateway.push(server);
        continue;
      }
      directCount += tools.length;
      for (var j = 0; j < tools.length; j++) {
        var name = registerDirect(server, tools[j]);
        if (name) exposed.push(name);
      }
    }
    if (gateway.length) exposed.push(registerGateway(gateway));
    if (visible) {
      exposed.push(registerFindTools());
      exposed.push(registerListTools());
    }
    if (visible && hasAnyResources()) exposed.push(registerResourceTools());

    if (!cdx.worker) cdx.storage.set(EXPOSED_KEY, exposed);
  }

  // hasAnyResources reports whether a connected server exposes resources.
  function hasAnyResources() {
    var status = cdx.storage.get(STATUS_KEY) || {};
    for (var id in status) {
      if (status[id] && status[id].resources) return true;
    }
    return false;
  }

  function resourceSummary(resource) {
    return {
      uri: resource.uri,
      name: resource.name || "",
      title: resource.title || "",
      mime_type: resource.mimeType || ""
    };
  }

  function listResources(serverID) {
    var list = loadServers();
    var out = [];
    var truncated = false;
    for (var i = 0; i < list.length; i++) {
      var server = list[i];
      if (!server.enabled) continue;
      if (serverID && server.id !== serverID) continue;
      ensure(server.id);
      var resources = cdx.mcp.listResources(server.id) || [];
      for (var j = 0; j < resources.length; j++) {
        if (out.length >= 100) {
          truncated = true;
          break;
        }
        var item = resourceSummary(resources[j]);
        item.server = server.id;
        out.push(item);
      }
      if (truncated) break;
    }
    return { resources: out, count: out.length, truncated: truncated };
  }

  function readResource(serverID, uri) {
    var server = findServer(serverID);
    if (!server) throw new Error("MCP server " + serverID + " is not configured");
    ensure(serverID);
    var res = cdx.mcp.readResource(serverID, uri) || {};
    var contents = res.contents || [];
    var parts = [];
    for (var i = 0; i < contents.length; i++) {
      var item = contents[i] || {};
      if (item.text) parts.push(String(item.text));
      else if (item.blob) parts.push("[binary resource " + (item.mimeType || "unknown") + "]");
      else parts.push("[" + (item.uri || uri) + "]");
    }
    var text = parts.join("\n\n").trim() || "(empty resource)";
    if (text.length > RESULT_LIMIT) text = text.slice(0, RESULT_LIMIT) + "\n... (truncated)";
    return text;
  }

  function registerResourceTools() {
    cdx.tools.register({
      name: "mcp_list_resources",
      description: "List the resources exposed by the connected MCP servers, optionally filtered by server",
      permission: "read",
      input_schema: {
        type: "object",
        properties: { server: { type: "string", description: "Optional MCP server id" } }
      }
    }, cdx.inWorker(function (args) {
      return listResources(String(args.server || ""));
    }));

    cdx.tools.register({
      name: "mcp_read_resource",
      description: "Read one resource of an MCP server by its URI",
      permission: "read",
      input_schema: {
        type: "object",
        properties: {
          server: { type: "string", description: "MCP server id" },
          uri: { type: "string", description: "Resource URI from mcp_list_resources" }
        },
        required: ["server", "uri"]
      }
    }, cdx.inWorker(function (args) {
      return readResource(String(args.server || ""), String(args.uri || ""));
    }));

    return "mcp_list_resources";
  }

  // testServer builds a connection entry without writing secrets.
  function testServer(raw) {
    var id = slugID(raw.id || raw.name);
    var stored = findServer(id);
    var storedSecrets = stored ? secretValues(stored) : { env: {}, headers: {} };
    var server = {
      id: id,
      name: String(raw.name || id),
      transport: raw.transport === "http" ? "http" : "stdio",
      url: String(raw.url || ""),
      command: String(raw.command || ""),
      args: raw.args || [],
      cwd: String(raw.cwd || ""),
      env: {},
      headers: {},
      env_secrets: [],
      header_secrets: []
    };
    var plainEnv = raw.env || {};
    for (var envName in plainEnv) {
      if (plainEnv.hasOwnProperty(envName)) server.env[envName] = plainEnv[envName];
    }
    var plainHeaders = raw.headers || {};
    for (var headerName in plainHeaders) {
      if (plainHeaders.hasOwnProperty(headerName)) server.headers[headerName] = plainHeaders[headerName];
    }
    overlaySecrets(server, "env", raw.env_secrets, storedSecrets.env);
    overlaySecrets(server, "header", raw.header_secrets, storedSecrets.headers);
    return server;
  }

  // overlaySecrets merges typed and stored secret values into the plain maps.
  function overlaySecrets(server, kind, input, stored) {
    if (!input || typeof input !== "object" || Array.isArray(input)) return;
    var target = kind === "env" ? server.env : server.headers;
    for (var name in input) {
      if (!input.hasOwnProperty(name) || !name) continue;
      var value = input[name];
      if (typeof value === "string" && value !== "") target[name] = value;
      else if (stored[name]) target[name] = stored[name];
    }
  }

  // ---- status and refresh ----

  function describeError(err) {
    if (err && err.message) return String(err.message);
    return String(err);
  }

  function refreshAll() {
    var ids = cdx.mcp.servers() || [];
    for (var k = 0; k < ids.length; k++) cdx.mcp.close(ids[k]);

    var list = loadServers();
    var status = {};
    for (var i = 0; i < list.length; i++) {
      var server = list[i];
      if (!server.enabled) {
        status[server.id] = { connected: false, disabled: true };
        continue;
      }
      try {
        var info = cdx.mcp.connect(hostConfig(server));
        var tools = cdx.mcp.listTools(server.id) || [];
        cdx.storage.set(CATALOG_PREFIX + server.id, tools);
        var capabilities = (info.server && info.server.capabilities) || {};
        status[server.id] = {
          connected: true,
          revision: info.revision,
          server: info.server || {},
          tools: tools.length,
          resources: Boolean(capabilities.resources),
          instructions: String((info.server && info.server.instructions) || "")
        };
      } catch (err) {
        status[server.id] = { connected: false, error: describeError(err) };
        cdx.log.warn("MCP server " + server.id + " failed: " + status[server.id].error);
      }
    }
    cdx.storage.set(STATUS_KEY, status);
    registerTools();
    return status;
  }

  function snapshot() {
    return { servers: loadServers(), status: cdx.storage.get(STATUS_KEY) || {} };
  }

  // registerInstructionsHook adds the instructions a server reports to the system prompt.
  function registerInstructionsHook() {
    cdx.hooks.on("session:before_turn", 12, function (payload) {
      var status = cdx.storage.get(STATUS_KEY) || {};
      var list = loadServers();
      var sections = [];
      var total = 0;
      for (var i = 0; i < list.length; i++) {
        var server = list[i];
        if (!server.enabled) continue;
        var info = status[server.id] || {};
        var text = String(info.instructions || "").trim();
        if (!text) continue;
        if (text.length > 2000) text = text.slice(0, 2000) + "...";
        var section = "### " + (server.name || server.id) + "\n" + text;
        if (total + section.length > 6000) break;
        total += section.length;
        sections.push(section);
      }
      if (!sections.length) return payload;
      var block = "## MCP server instructions\n\n" + sections.join("\n\n");
      payload.system_prompt = payload.system_prompt
        ? payload.system_prompt + "\n\n" + block
        : block;
      return payload;
    });
  }

  // ---- HTTP API ----

  function registerRoutes() {
    cdx.http.get("/api/plugins/mcp/servers", function (ctx) {
      ctx.json(200, snapshot());
    });

    cdx.http.post("/api/plugins/mcp/servers", function (ctx) {
      var body = ctx.bindJSON() || {};
      try {
        saveServersInput(body);
      } catch (err) {
        ctx.json(400, { error: describeError(err) });
        return;
      }
      var status = refreshAll();
      var out = snapshot();
      out.status = status;
      ctx.json(200, out);
    });

    cdx.http.post("/api/plugins/mcp/servers/test", function (ctx) {
      var body = ctx.bindJSON() || {};
      var raw = body.server || {};
      var id = slugID(raw.id || raw.name);
      var result = { ok: false };
      try {
        var server = testServer(raw);
        var info = cdx.mcp.connect(hostConfig(server));
        var tools = cdx.mcp.listTools(server.id) || [];
        result.ok = true;
        result.revision = info.revision;
        result.server = info.server || {};
        result.tools = tools;
      } catch (err) {
        result.error = describeError(err);
      }
      if (id) cdx.mcp.close(id);
      ctx.json(200, result);
    });

    cdx.http.post("/api/plugins/mcp/refresh", function (ctx) {
      var status = refreshAll();
      var out = snapshot();
      out.status = status;
      ctx.json(200, out);
    });

    cdx.http.get("/api/plugins/mcp/tools", function (ctx) {
      var id = String(ctx.query("server") || "");
      var tools = id ? cdx.storage.get(CATALOG_PREFIX + id) || [] : [];
      ctx.json(200, { server: id, tools: tools });
    });

    cdx.http.post("/api/plugins/mcp/import", function (ctx) {
      var body = ctx.bindJSON() || {};
      var parsed;
      try {
        parsed = JSON.parse(String(body.json || ""));
      } catch (err) {
        ctx.json(400, { error: "invalid JSON" });
        return;
      }
      var source = parsed.mcpServers || parsed.servers || {};
      var current = loadServers();
      var byID = {};
      for (var i = 0; i < current.length; i++) byID[current[i].id] = current[i];

      var imported = 0;
      for (var name in source) {
        if (!source.hasOwnProperty(name)) continue;
        var entry = importEntry(name, source[name] || {});
        if (!entry) continue;
        byID[entry.id] = entry;
        imported++;
      }
      var list = [];
      for (var id in byID) {
        if (byID.hasOwnProperty(id)) list.push(byID[id]);
      }
      saveServers(list);
      var status = refreshAll();
      var out = snapshot();
      out.status = status;
      out.imported = imported;
      ctx.json(200, out);
    });
  }

  // importEntry converts one mcp.json entry and moves its values into secrets.
  function importEntry(name, item) {
    var id = slugID(name);
    if (!id) return null;
    var entry = {
      id: id,
      name: String(item.name || name),
      enabled: true,
      transport: item.url ? "http" : "stdio",
      protocol: "auto",
      timeout_ms: 0,
      expose: "auto",
      permission: "auto"
    };
    if (entry.transport === "http") {
      entry.url = String(item.url || "");
      entry.headers = {};
      entry.header_secrets = [];
      var headers = item.headers || {};
      for (var header in headers) {
        if (!headers.hasOwnProperty(header)) continue;
        cdx.secrets.set(secretName(id, "header", header), String(headers[header]));
        entry.header_secrets.push(header);
      }
      entry.header_secrets.sort();
    } else {
      entry.command = String(item.command || "");
      entry.args = [];
      var args = item.args || [];
      for (var i = 0; i < args.length; i++) entry.args.push(String(args[i]));
      entry.cwd = String(item.cwd || "");
      entry.env = {};
      entry.env_secrets = [];
      var env = item.env || {};
      for (var variable in env) {
        if (!env.hasOwnProperty(variable)) continue;
        cdx.secrets.set(secretName(id, "env", variable), String(env[variable]));
        entry.env_secrets.push(variable);
      }
      entry.env_secrets.sort();
    }
    return entry;
  }

  registerTools();
  if (cdx.worker) return;

  registerInstructionsHook();
  registerRoutes();
  cdx.http.static("/ui/mcp", "./web");
  cdx.ui.register({
    title: "mcp.Title",
    description: "mcp.Description",
    order: 170,
    entry: "/ui/mcp/dist/panel.js",
    ui_api: 1
  });
  cdx.after(500, function () {
    if (!cdx.storage.get(STATUS_KEY)) refreshAll();
  });
}
