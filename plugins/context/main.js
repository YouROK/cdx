function setup(cdx) {
  function getSettings() {
    var cfg = cdx.storage.get("config");
    if (!cfg) {
      cfg = {
        load_agents_md: true,
        max_messages: 40
      };
      cdx.storage.set("config", cfg);
    }
    return cfg;
  }

  function tryReadContextFiles(workdir) {
    if (!workdir) return [];
    var names = ["AGENTS.md", "CLAUDE.md"];
    var found = [];
    for (var i = 0; i < names.length; i++) {
      try {
        var content = cdx.sys.readFile(workdir + "/" + names[i]);
        if (content && content.trim().length > 0) {
          found.push({ name: names[i], dir: workdir, content: content.trim() });
        }
      } catch (e) {}
    }
    return found;
  }

  function buildEnvironmentBlock(payload) {
    var lines = [];
    if (payload.workdir) {
      lines.push("## Workspace");
      lines.push("- path: " + payload.workdir);
      lines.push("- Treat this directory as the root for relative paths.");
      lines.push("- Work inside it unless the request explicitly names another path.");
      lines.push("- Do not recursively search /, the home directory, or parent directories.");
      lines.push("");
    }

    lines.push("## Environment");
    var today = new Date().toISOString().slice(0, 10);
    lines.push("- date: " + today);

    var tools = payload.tools || [];
    lines.push("");
    lines.push("## Tools");
    if (tools.length === 0) {
      lines.push("No tools are available in this invocation. Answer from the request alone and say plainly what you cannot do.");
    } else {
      for (var i = 0; i < tools.length; i++) {
        var t = tools[i];
        lines.push("- " + t.name + (t.description ? ": " + t.description : ""));
      }
    }
    return lines.join("\n");
  }

  // Priority 10 runs after presets plugin (priority 5): appends AGENTS.md, summary, and Environment/Tools block.
  cdx.hooks.on("session:before_turn", 10, function(payload) {
    var cfg = getSettings();
    var parts = [];

    if (payload.system_prompt) {
      parts.push(payload.system_prompt);
    } else {
      parts.push("You are cdx, a concise and capable command-line coding assistant.");
    }

    if (cfg.load_agents_md && payload.workdir) {
      var files = tryReadContextFiles(payload.workdir);
      for (var i = 0; i < files.length; i++) {
        parts.push("## " + files[i].name + " (" + files[i].dir + ")\n\n" + files[i].content);
      }
    }

    if (payload.summary) {
      parts.push("## Previous conversation summary\n\n" + payload.summary);
    }

    parts.push(buildEnvironmentBlock(payload));
    payload.system_prompt = parts.join("\n\n");

    // Compact message list if it exceeds max_messages.
    var maxMsgs = cfg.max_messages || 40;
    var msgs = payload.messages || [];
    if (msgs.length > maxMsgs) {
      var dropped = msgs.length - maxMsgs;
      // Never start the kept history with a tool result: OpenAI-compatible
      // providers reject a tool message without the assistant tool_calls
      // message that produced it.
      while (dropped < msgs.length && msgs[dropped] && msgs[dropped].role === "tool") {
        dropped++;
      }
      if (dropped >= msgs.length) {
        return payload;
      }
      var oldSlice = msgs.slice(0, dropped);
      var summaryLines = [];
      for (var j = 0; j < oldSlice.length; j++) {
        var m = oldSlice[j];
        if (m.role && m.content) {
          var snippet = String(m.content).slice(0, 160);
          summaryLines.push(m.role + ": " + snippet);
        }
      }
      payload.summary = (payload.summary ? payload.summary + "\n" : "") + summaryLines.join("\n");
      payload.messages = msgs.slice(dropped);
    }

    return payload;
  });

  var api = cdx.http.group("/api/plugins/context");
  api.get("/config", function(ctx) {
    ctx.json(200, getSettings());
  });
  api.post("/config", function(ctx) {
    var body = ctx.bindJSON() || {};
    var cfg = getSettings();
    if (typeof body.load_agents_md === "boolean") cfg.load_agents_md = body.load_agents_md;
    if (typeof body.max_messages === "number" && body.max_messages >= 4) cfg.max_messages = body.max_messages;
    cdx.storage.set("config", cfg);
    ctx.json(200, cfg);
  });

  cdx.ui.register({
    title: "Context & Memory",
    description: "Controls AGENTS.md loading and conversation history compaction",
    order: 15,
    config_url: "/api/plugins/context/config",
    schema: {
      type: "object",
      title: "Context settings",
      properties: {
        load_agents_md: {
          type: "boolean",
          title: "Load AGENTS.md",
          description: "Read AGENTS.md or CLAUDE.md from the working directory before each turn.",
          default: true
        },
        max_messages: {
          type: "integer",
          title: "Recent messages limit",
          description: "Messages kept before older history is compacted into a summary.",
          minimum: 4,
          maximum: 500,
          default: 40
        }
      }
    }
  });
}
