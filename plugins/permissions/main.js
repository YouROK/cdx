function setup(cdx) {
  // In-memory grants remembered per session or for the current daemon run.
  var sessionGrants = {};
  var daemonGrants = {};

  function getConfig() {
    var cfg = cdx.storage.get("config");
    if (!cfg) {
      cfg = {
        read_policy: "allow",
        edit_policy: "ask",
        dangerous_policy: "ask",
        tool_rules: {}
      };
      cdx.storage.set("config", cfg);
    }
    if (!cfg.tool_rules) {
      cfg.tool_rules = {};
    }
    return cfg;
  }

  function policyFor(toolName, permissionLevel, sessionID) {
    var cfg = getConfig();
    if (cfg.tool_rules && cfg.tool_rules[toolName]) {
      return cfg.tool_rules[toolName];
    }
    var lvl = permissionLevel || "edit";
    var grantKey = lvl + ":" + toolName;

    if (daemonGrants[grantKey]) {
      return "allow";
    }
    if (sessionID && sessionGrants[sessionID] && sessionGrants[sessionID][grantKey]) {
      return sessionGrants[sessionID][grantKey];
    }

    if (lvl === "read") {
      return cfg.read_policy || "allow";
    }
    if (lvl === "dangerous") {
      return cfg.dangerous_policy || "ask";
    }
    return cfg.edit_policy || "ask";
  }

  function buildSummary(tool, args) {
    if (!args) return tool;
    if (args.command) return tool + ": " + args.command;
    if (args.path) return tool + " " + args.path;
    if (args.target) return tool + " " + args.target;
    try {
      return tool + " " + JSON.stringify(args);
    } catch (e) {
      return tool;
    }
  }

  function normalizePath(workdir, p) {
    p = String(p || "").trim();
    if (!p) return "";
    if (p.charAt(0) === "~" || p.indexOf("$HOME") >= 0 || p.indexOf("${HOME}") >= 0) {
      return "";
    }
    var base = String(workdir || "/").trim();
    if (base.charAt(0) !== "/") base = "/" + base;
    var absolute = p.charAt(0) === "/";
    var raw = absolute ? p : base.replace(/\/+$/, "") + "/" + p;
    var parts = raw.split("/");
    var out = [];
    for (var i = 0; i < parts.length; i++) {
      var part = parts[i];
      if (!part || part === ".") continue;
      if (part === "..") {
        if (out.length > 0) out.pop();
        continue;
      }
      out.push(part);
    }
    return "/" + out.join("/");
  }

  function isSafeOutside(path) {
    return path === "/dev/null" || path === "/dev/stdout" || path === "/dev/stderr";
  }

  function isInside(workdir, p) {
    if (!workdir) return true;
    var base = normalizePath("/", workdir);
    var target = normalizePath(base, p);
    if (!target) return false;
    if (isSafeOutside(target)) return true;
    if (base === "/") return true;
    return target === base || target.indexOf(base + "/") === 0;
  }

  function uniquePaths(list) {
    var out = [];
    for (var i = 0; i < list.length; i++) {
      if (list[i] && out.indexOf(list[i]) < 0) out.push(list[i]);
    }
    return out;
  }

  function outsidePathsForTool(args, workdir) {
    if (!workdir || !args) return [];
    var keys = ["path", "file", "filename", "dir", "directory"];
    var found = [];
    for (var i = 0; i < keys.length; i++) {
      var value = args[keys[i]];
      if (typeof value === "string" && value.trim() && !isInside(workdir, value)) {
        found.push(value);
      }
    }
    return uniquePaths(found);
  }

  function outsidePathsForShell(command, workdir) {
    if (!workdir) return [];
    var text = String(command || "");
    var found = [];
    var absolute = /(^|[\s"'=<>|&;()])((?:~\/|\/)[^\s"'`;|&<>()]*)/g;
    var match;
    while ((match = absolute.exec(text)) !== null) {
      if (!isInside(workdir, match[2])) found.push(match[2]);
    }

    var cd = /(^|[;&|\n]\s*)cd\s+([^\s;&|]*)/g;
    while ((match = cd.exec(text)) !== null) {
      var arg = match[2] || "";
      if (!arg || arg === "-" || arg.charAt(0) === "~" || !isInside(workdir, arg)) {
        found.push(arg || "~");
      }
    }

    if (/(^|[\s"'=])\.\.(\/|\s|$)/.test(text)) {
      found.push("..");
    }
    if (text.indexOf("$HOME") >= 0 || text.indexOf("${HOME}") >= 0) {
      found.push("$HOME");
    }
    return uniquePaths(found);
  }

  function askOutsideWorkspace(tool, payload, outside) {
    var workdir = payload.workdir || "";
    var summary = "outside workspace: " + tool + " " + outside.join(", ");
    if (workdir) summary += " (workspace: " + workdir + ")";
    var ans = cdx.bus.request("client:prompt_user", {
      kind: "outside_workspace",
      summary: summary,
      question: summary,
      tool: tool,
      permission: payload.permission || "edit",
      level: payload.permission || "edit",
      workspace: workdir,
      outside: outside,
      options: ["once", "deny"],
      arguments: payload.arguments
    });
    var choice = (ans && ans.choice) ? String(ans.choice) : (ans && ans.allow ? "once" : "deny");
    if (choice === "once" || (ans && ans.allow && choice !== "deny")) {
      return payload;
    }
    throw new Error("permission denied by user for tool: " + tool);
  }

  cdx.hooks.on("tool:before_call", 10, function(payload) {
    var tool = payload.tool || "";
    var level = payload.permission || "edit";
    var sessionID = payload.session_id || "";
    var policy = policyFor(tool, level, sessionID);

    if (policy === "deny") {
      throw new Error("permission denied by policy for tool: " + tool);
    }

    var workdir = payload.workdir || "";
    var args = payload.arguments || {};
    var outside = [];
    if (workdir) {
      if (tool === "shell") {
        outside = outsidePathsForShell(args.command, workdir);
      } else {
        outside = outsidePathsForTool(args, workdir);
      }
    }
    if (outside.length > 0) {
      return askOutsideWorkspace(tool, payload, outside);
    }

    if (policy === "allow") {
      return payload;
    }

    var summary = buildSummary(tool, args);
    var ans = cdx.bus.request("client:prompt_user", {
      summary: summary,
      question: summary,
      tool: tool,
      permission: level,
      level: level,
      options: ["once", "session", "daemon", "always", "deny"],
      arguments: args
    });

    var choice = (ans && ans.choice) ? String(ans.choice) : (ans && ans.allow ? "once" : "deny");
    var grantKey = level + ":" + tool;

    if (choice === "always") {
      var cfg = getConfig();
      cfg.tool_rules[tool] = "allow";
      cdx.storage.set("config", cfg);
      daemonGrants[grantKey] = true;
      return payload;
    }

    if (choice === "daemon" || choice === "runtime") {
      daemonGrants[grantKey] = true;
      return payload;
    }

    if (choice === "session") {
      if (sessionID && sessionID !== "ephemeral") {
        if (!sessionGrants[sessionID]) sessionGrants[sessionID] = {};
        sessionGrants[sessionID][grantKey] = "allow";
      } else {
        daemonGrants[grantKey] = true;
      }
      return payload;
    }

    if (choice === "once" || (ans && ans.allow && choice !== "deny")) {
      return payload;
    }

    throw new Error("permission denied by user for tool: " + tool);
  });

  var api = cdx.http.group("/api/plugins/permissions");

  api.get("/config", function(ctx) {
    var cfg = getConfig();
    ctx.json(200, {
      read_policy: cfg.read_policy,
      edit_policy: cfg.edit_policy,
      dangerous_policy: cfg.dangerous_policy,
      tool_rules: cfg.tool_rules,
      daemon_grants: daemonGrants,
      session_grants: sessionGrants
    });
  });

  api.post("/config", function(ctx) {
    var body = ctx.bindJSON() || {};
    var cfg = getConfig();
    if (body.read_policy) cfg.read_policy = body.read_policy;
    if (body.edit_policy) cfg.edit_policy = body.edit_policy;
    if (body.dangerous_policy) cfg.dangerous_policy = body.dangerous_policy;
    if (body.tool_rules) cfg.tool_rules = body.tool_rules;
    cdx.storage.set("config", cfg);
    ctx.json(200, cfg);
  });

  api.post("/reset-grants", function(ctx) {
    sessionGrants = {};
    daemonGrants = {};
    var cfg = getConfig();
    cfg.tool_rules = {};
    cdx.storage.set("config", cfg);
    ctx.json(200, { reset: true });
  });

  cdx.ui.register({
    title: "Permissions",
    description: "Control how read, edit, and dangerous tools are approved",
    order: 20,
    schema: {
      type: "object",
      title: "Permission Policies",
      properties: {
        read_policy: {
          type: "string",
          title: "Read Tools Policy",
          enum: ["allow", "ask", "deny"],
          default: "allow"
        },
        edit_policy: {
          type: "string",
          title: "Edit Tools Policy",
          enum: ["allow", "ask", "deny"],
          enum_labels: ["allow", "ask", "deny"],
          default: "ask"
        },
        dangerous_policy: {
          type: "string",
          title: "Dangerous Tools Policy",
          enum: ["allow", "ask", "deny"],
          default: "ask"
        }
      }
    }
  });
}
