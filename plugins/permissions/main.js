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
        outside_policy: "ask",
        tool_rules: {}
      };
      cdx.storage.set("config", cfg);
    }
    if (!cfg.tool_rules) {
      cfg.tool_rules = {};
    }
    if (!cfg.outside_policy) {
      cfg.outside_policy = "ask";
    }
    return cfg;
  }

  function policyFor(toolName, permissionLevel, sessionID, rootID) {
    var cfg = getConfig();
    if (cfg.tool_rules && cfg.tool_rules[toolName]) {
      return cfg.tool_rules[toolName];
    }
    var lvl = permissionLevel || "edit";
    var grantKey = lvl + ":" + toolName;

    if (daemonGrants[grantKey]) {
      return "allow";
    }
    // A session grant covers the whole delegation tree.
    var grantSession = rootID || sessionID;
    if (grantSession && sessionGrants[grantSession] && sessionGrants[grantSession][grantKey]) {
      return sessionGrants[grantSession][grantKey];
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

  // Translation lookup for questions sent to the client. Falls back to English.
  function translator() {
    var dict = {};
    try {
      var catalog = cdx.bus.request("i18n:catalog", {});
      if (catalog && catalog.strings) dict = catalog.strings;
    } catch (e) {
      // i18n is unavailable; the English fallbacks stay.
    }
    return function t(key, fallback, vars) {
      var text = Object.prototype.hasOwnProperty.call(dict, key) ? dict[key] : (fallback || key);
      if (vars && typeof text === "string") {
        text = text.replace(/\{(\w+)\}/g, function (match, name) {
          return vars[name] === undefined || vars[name] === null ? match : String(vars[name]);
        });
      }
      return text;
    };
  }

  // Human-readable name of the action behind a tool.
  function toolTitle(tool, t) {
    if (tool === "shell") return t("permissions.ToolShell", "Run a shell command");
    if (tool === "write_file") return t("permissions.ToolWrite", "Write a file");
    if (tool === "edit_file") return t("permissions.ToolEdit", "Edit a file");
    if (tool === "read_file") return t("permissions.ToolRead", "Read a file");
    if (tool === "list_dir") return t("permissions.ToolList", "List a folder");
    if (tool === "grep") return t("permissions.ToolGrep", "Search file contents");
    if (tool === "glob") return t("permissions.ToolGlob", "Find files");
    return t("permissions.ToolUse", "Use the tool {tool}", { tool: tool });
  }

  function permissionLabel(level, t) {
    if (level === "read") return t("permissions.LevelRead", "read");
    if (level === "dangerous") return t("permissions.LevelDangerous", "dangerous");
    return t("permissions.LevelEdit", "edit");
  }

  // Labelled lines shown in the prompt: the full command, the file path, and so on.
  function toolDetails(tool, args, t) {
    var details = [];
    function add(label, value) {
      if (value === undefined || value === null) return;
      var text = String(value);
      if (!text) return;
      details.push({ label: label, value: text });
    }
    args = args || {};
    if (tool === "shell") {
      add(t("permissions.DetailCommand", "Command"), args.command);
    } else if (tool === "write_file" || tool === "edit_file") {
      add(t("permissions.DetailFile", "File"), args.path || args.file);
      if (tool === "write_file" && typeof args.content === "string") {
        add(t("permissions.DetailContent", "Content"), t("permissions.Characters", "{count} characters", { count: args.content.length }));
      }
    } else if (tool === "read_file") {
      add(t("permissions.DetailFile", "File"), args.path || args.file);
    } else if (tool === "list_dir") {
      add(t("permissions.DetailFolder", "Folder"), args.path || args.dir);
    } else if (tool === "grep" || tool === "glob") {
      add(t("permissions.DetailPattern", "Pattern"), args.pattern || args.query || args.glob);
      add(t("permissions.DetailFolder", "Folder"), args.path);
    }
    if (details.length === 0) {
      try {
        add(t("permissions.DetailArguments", "Arguments"), JSON.stringify(args));
      } catch (e) {
        // Arguments that cannot be serialized stay out of the prompt.
      }
    }
    return details;
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

  // Attach a question to the hook payload; the core asks the client and then runs tool:decided.
  function askPolicy(payload, question) {
    payload.ask = question;
    return payload;
  }

  function askOutsideWorkspace(tool, payload, outside, t) {
    var workdir = payload.workdir || "";
    var details = toolDetails(tool, payload.arguments || {}, t);
    details.push({ label: t("permissions.DetailPermission", "Permission"), value: permissionLabel(payload.permission || "edit", t) });
    if (workdir) details.push({ label: t("permissions.DetailWorkingFolder", "Working folder"), value: workdir });
    for (var i = 0; i < outside.length; i++) {
      details.push({ label: i === 0 ? t("permissions.DetailOutside", "Outside") : "", value: outside[i] });
    }
    return askPolicy(payload, {
      kind: "permission",
      tag: "permissions",
      title: t("permissions.OutsideTitle", "{action} outside the working folder", { action: toolTitle(tool, t) }),
      body: t("permissions.OutsideBody", "The action leaves the working folder."),
      details: details,
      options: [
        { id: "once", label: t("permissions.AllowOnce", "Allow once") },
        { id: "deny", label: t("permissions.Deny", "Deny") }
      ],
      default: "once"
    });
  }

  cdx.hooks.on("tool:before_call", 10, function(payload) {
    var tool = payload.tool || "";
    var level = payload.permission || "edit";
    var sessionID = payload.session_id || "";
    var rootID = payload.root_session_id || "";
    var policy = policyFor(tool, level, sessionID, rootID);

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
      var outsidePolicy = getConfig().outside_policy || "ask";
      if (outsidePolicy === "deny") {
        throw new Error("permission denied by policy: the action is outside the working folder: " + buildSummary(tool, args));
      }
      if (outsidePolicy !== "allow") {
        return askOutsideWorkspace(tool, payload, outside, translator());
      }
    }

    if (policy === "allow") {
      return payload;
    }

    var t = translator();
    var details = toolDetails(tool, args, t);
    details.push({ label: t("permissions.DetailPermission", "Permission"), value: permissionLabel(level, t) });
    return askPolicy(payload, {
      kind: "permission",
      tag: "permissions",
      title: toolTitle(tool, t),
      body: buildSummary(tool, args),
      details: details,
      options: [
        { id: "once", label: t("permissions.AllowOnce", "Allow once") },
        { id: "session", label: t("permissions.AllowSession", "Allow for this session") },
        { id: "daemon", label: t("permissions.AllowDaemon", "Allow for this daemon run") },
        { id: "always", label: t("permissions.AlwaysAllow", "Always allow") },
        { id: "deny", label: t("permissions.Deny", "Deny") }
      ],
      default: "once"
    });
  });

  // Persist the user's answer and tell the core whether the tool may run.
  cdx.hooks.on("tool:decided", 10, function(payload) {
    // Only decide questions this plugin asked; another plugin owns its own hook.
    if (payload.tag !== "permissions") {
      return payload;
    }
    var tool = payload.tool || "";
    var level = payload.permission || "edit";
    var sessionID = payload.root_session_id || payload.session_id || "";
    var choice = payload.choice ? String(payload.choice) : "";
    var allowed = !payload.cancelled && choice !== "" && choice !== "deny";

    if (allowed) {
      var grantKey = level + ":" + tool;
      if (choice === "always") {
        var cfg = getConfig();
        cfg.tool_rules[tool] = "allow";
        cdx.storage.set("config", cfg);
        daemonGrants[grantKey] = true;
      } else if (choice === "daemon" || choice === "runtime") {
        daemonGrants[grantKey] = true;
      } else if (choice === "session") {
        if (sessionID && sessionID !== "ephemeral") {
          if (!sessionGrants[sessionID]) sessionGrants[sessionID] = {};
          sessionGrants[sessionID][grantKey] = "allow";
        } else {
          daemonGrants[grantKey] = true;
        }
      }
    }

    payload.allowed = allowed;
    if (!allowed) {
      payload.error = "permission denied by user for tool: " + tool;
    }
    return payload;
  });

  var api = cdx.http.group("/api/plugins/permissions");

  api.get("/config", function(ctx) {
    var cfg = getConfig();
    ctx.json(200, {
      read_policy: cfg.read_policy,
      edit_policy: cfg.edit_policy,
      dangerous_policy: cfg.dangerous_policy,
      outside_policy: cfg.outside_policy || "ask",
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
    if (body.outside_policy === "ask" || body.outside_policy === "allow" || body.outside_policy === "deny") {
      cfg.outside_policy = body.outside_policy;
    }
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

  cdx.http.static("/ui/permissions", "./web");

  cdx.ui.register({
    title: "permissions.Title",
    description: "permissions.Description",
    order: 130,
    entry: "/ui/permissions/dist/panel.js"
  });
}
