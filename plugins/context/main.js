function setup(cdx) {
  function getSettings() {
    var cfg = cdx.storage.get("config");
    if (!cfg) {
      cfg = {
        load_agents_md: true,
        max_messages: 40,
        max_summary_chars: 8000
      };
      cdx.storage.set("config", cfg);
    }
    return cfg;
  }

  // The summary keeps growing as messages are compacted, so it is capped and
  // the newest part is kept.
  function clipSummary(text, limit) {
    if (!text) return "";
    if (limit < 1000) limit = 1000;
    if (text.length <= limit) return text;
    return "...[older summary trimmed]\n" + text.slice(text.length - limit);
  }

  function summaryLimit(cfg) {
    var limit = Number(cfg.max_summary_chars);
    return limit > 0 ? limit : 8000;
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

  // Workspace rules are a fallback for prompts that do not carry their own.
  function buildWorkspaceBlock(workdir) {
    return [
      "## Workspace",
      "- Working directory: " + workdir + ". It is the base for relative paths,",
      "  not a reason to read its contents.",
      "- Paths outside it are fine when the task needs them (system files, logs,",
      "  an explicit path in the request).",
      "- No broad scans (find /, ls -R, grep -r) unless the request asks for a",
      "  search."
    ].join("\n");
  }

  function hasWorkspaceBlock(prompt) {
    return typeof prompt === "string" && prompt.indexOf("## Workspace") !== -1;
  }

  function buildEnvironmentBlock(payload) {
    var lines = [];

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

  // Priority 10 runs after presets plugin (priority 5): appends AGENTS.md, summary,
  // and Environment/Tools block, plus workspace rules when the prompt has none.
  cdx.hooks.on("session:before_turn", 10, function(payload) {
    var cfg = getSettings();
    var parts = [];

    if (payload.system_prompt) {
      parts.push(payload.system_prompt);
    } else {
      parts.push("You are cdx, a concise and capable command-line coding assistant.");
    }

    if (payload.workdir && !hasWorkspaceBlock(payload.system_prompt)) {
      parts.push(buildWorkspaceBlock(payload.workdir));
    }

    if (cfg.load_agents_md && payload.workdir) {
      var files = tryReadContextFiles(payload.workdir);
      for (var i = 0; i < files.length; i++) {
        parts.push("## " + files[i].name + " (" + files[i].dir + ")\n\n" + files[i].content);
      }
    }

    if (payload.summary) {
      parts.push("## Previous conversation summary\n\n" + clipSummary(payload.summary, summaryLimit(cfg)));
    }

    parts.push(buildEnvironmentBlock(payload));
    payload.system_prompt = parts.join("\n\n");

    // Compact message list if it exceeds max_messages.
    var maxMsgs = cfg.max_messages || 40;
    var msgs = payload.messages || [];
    if (msgs.length > maxMsgs) {
      var keepFrom = msgs.length - maxMsgs;
      // Never start the kept history with a tool result: OpenAI-compatible
      // providers reject a tool message without the assistant tool_calls
      // message that produced it.
      while (keepFrom < msgs.length && msgs[keepFrom] && msgs[keepFrom].role === "tool") {
        keepFrom++;
      }
      if (keepFrom >= msgs.length) {
        return payload;
      }

      // Each turn passes the whole history, so the oldest messages must be
      // summarized once, not again and again.
      var counterKey = "summarized:" + String(payload.session_id || "default");
      var already = Number(cdx.storage.get(counterKey)) || 0;
      if (already > keepFrom) {
        already = keepFrom;
      }
      if (keepFrom > already) {
        var summaryLines = [];
        for (var j = already; j < keepFrom; j++) {
          var m = msgs[j];
          if (!m || !m.role) continue;
          if (m.role === "tool") {
            var body = String(m.content || "");
            var status = body.indexOf("error:") === 0 ? "error: " + body.slice(7, 107) : "ok";
            summaryLines.push("tool " + (m.name || "?") + ": " + status);
          } else if (m.content) {
            summaryLines.push(m.role + ": " + String(m.content).slice(0, 160));
          }
        }
        if (summaryLines.length > 0) {
          payload.summary = clipSummary(
            (payload.summary ? payload.summary + "\n" : "") + summaryLines.join("\n"),
            summaryLimit(cfg)
          );
        }
        cdx.storage.set(counterKey, keepFrom);
      }
      payload.messages = msgs.slice(keepFrom);
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
    if (typeof body.max_summary_chars === "number" && body.max_summary_chars >= 1000) {
      cfg.max_summary_chars = Math.min(body.max_summary_chars, 100000);
    }
    cdx.storage.set("config", cfg);
    ctx.json(200, cfg);
  });

  cdx.ui.register({
    title: "context.Title",
    description: "context.Description",
    order: 140,
    config_url: "/api/plugins/context/config",
    schema: {
      type: "object",
      title: "context.Schema.Title",
      properties: {
        load_agents_md: {
          type: "boolean",
          title: "context.Schema.LoadAgentsMD",
          description: "context.Schema.LoadAgentsMDHint",
          default: true
        },
        max_summary_chars: {
          type: "integer",
          title: "context.Schema.SummarySize",
          description: "context.Schema.SummarySizeHint",
          minimum: 1000,
          maximum: 100000,
          default: 8000
        },
        max_messages: {
          type: "integer",
          title: "context.Schema.RecentMessages",
          description: "context.Schema.RecentMessagesHint",
          minimum: 4,
          maximum: 500,
          default: 40
        }
      }
    }
  });
}
