window.CDXPluginPanels = window.CDXPluginPanels || {};

window.cdxToast = function(ok, message) {
  var wrap = document.getElementById("toast-container");
  if (!wrap) return;
  var el = document.createElement("div");
  el.className = ok ? "toast" : "toast err";
  el.textContent = message;
  wrap.appendChild(el);
  setTimeout(function() {
    el.remove();
  }, 3000);
};

window.cdxConfirm = function(title, message, onConfirm) {
  var root = document.getElementById("cdx-modal-root");
  if (!root) {
    root = document.createElement("div");
    root.id = "cdx-modal-root";
    document.body.appendChild(root);
  }
  root.innerHTML =
    '<div class="modal-backdrop">' +
    '  <div class="modal-box" style="max-width:440px">' +
    '    <div class="modal-header">' +
    '      <h4>' + title + '</h4>' +
    '      <button class="btn-icon" id="confirm-close-x">&times;</button>' +
    '    </div>' +
    '    <div class="form-stack">' +
    '      <div class="text-sm">' + message + '</div>' +
    '      <div class="modal-actions">' +
    '        <button type="button" class="btn btn-default" id="confirm-cancel">Cancel</button>' +
    '        <button type="button" class="btn btn-danger" id="confirm-ok">Confirm</button>' +
    '      </div>' +
    '    </div>' +
    '  </div>' +
    '</div>';
  function close() {
    root.innerHTML = "";
  }
  root.querySelector("#confirm-close-x").onclick = close;
  root.querySelector("#confirm-cancel").onclick = close;
  root.querySelector(".modal-backdrop").onclick = function(e) {
    if (e.target.classList.contains("modal-backdrop")) close();
  };
  root.querySelector("#confirm-ok").onclick = function() {
    close();
    if (onConfirm) onConfirm();
  };
};

var icons = {
  general: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-2.9 1.2v.2a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-2.9-1.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0-1.2-2.9H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.3-2.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 2.9-1.2V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 2.9 1.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0 1.2 2.9H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1Z",
  models: "M4 7h16 M4 12h16 M4 17h10",
  presets: "M12 3l8 4.5v9L12 21l-8-4.5v-9Z M12 12l8-4.5 M12 12v9 M12 12 4 7.5",
  sessions: "M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z",
  plugins: "M9 3v2 M15 3v2 M4 9h2 M4 15h2 M9 21v-2 M15 21v-2 M20 9h-2 M20 15h-2 M9 5h6a4 4 0 0 1 4 4v6a4 4 0 0 1-4 4H9a4 4 0 0 1-4-4V9a4 4 0 0 1 4-4Z",
  logs: "M4 4h16v16H4z M8 8h8 M8 12h8 M8 16h5"
};

function svgIcon(name) {
  var path = icons[name] || icons.plugins;
  return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="' + path + '"/></svg>';
}

function escapeHTML(v) {
  return String(v == null ? "" : v)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

(async function() {
  var navEl = document.getElementById("nav-list");
  var contentEl = document.getElementById("page-content");

  async function fetchStatus() {
    var r = await fetch("/api/core/status");
    return await r.json();
  }

  var status = await fetchStatus();

  // Render General page (daemon state + Permissions plugin + Context plugin + Session TTL).
  async function renderGeneralPage() {
    status = await fetchStatus();
    var permRes = await fetch("/api/plugins/permissions/config");
    var permCfg = permRes.ok ? await permRes.json() : {};
    var ctxRes = await fetch("/api/plugins/context/config");
    var ctxCfg = ctxRes.ok ? await ctxRes.json() : {};
    var sessRes = await fetch("/api/core/sessions");
    var sessData = sessRes.ok ? await sessRes.json() : {};
    var agentRes = await fetch("/api/core/agent");
    var agentCfg = agentRes.ok ? await agentRes.json() : {};

    var html =
      '<div class="page-header"><h3>General</h3></div>' +
      '<div class="stat-grid">' +
      '  <div class="stat-card"><div class="label">Version</div><div class="value">' + (status.version || "-") + '</div></div>' +
      '  <div class="stat-card"><div class="label">Protocol</div><div class="value">v' + (status.protocol || 1) + '</div></div>' +
      '  <div class="stat-card"><div class="label">PID</div><div class="value">' + (status.pid || "-") + '</div></div>' +
      '  <div class="stat-card"><div class="label">Loaded Plugins</div><div class="value">' + ((status.plugins || []).length) + '</div></div>' +
      '</div>';

    // Permissions card (backed by plugins/permissions)
    var alwaysRules = Object.keys(permCfg.tool_rules || {});
    var daemonRules = Object.keys(permCfg.daemon_grants || {});
    var sessionRuleCount = Object.keys(permCfg.session_grants || {}).length;
    var grantsSummary = "Active remembered grants: " +
      alwaysRules.length + " always" + (alwaysRules.length ? " (" + alwaysRules.join(", ") + ")" : "") +
      ", " + daemonRules.length + " daemon" +
      ", " + sessionRuleCount + " session(s)";

    html +=
      '<div class="stack-md">' +
      '<div class="panel">' +
      '  <h4 class="m-0 mb-md">Permissions</h4>' +
      '  <form id="general-perm-form" class="form-stack">' +
      '    <div class="grid-3">' +
      '      <label>Read tools<span class="field-hint">read_file, list_dir</span>' +
      '        <select name="read_policy">' +
                 ["allow", "ask", "deny"].map(function(v) { return '<option value="' + v + '"' + (permCfg.read_policy === v ? ' selected' : '') + '>' + v + '</option>'; }).join("") +
      '        </select></label>' +
      '      <label>Edit tools<span class="field-hint">write_file, edit_file</span>' +
      '        <select name="edit_policy">' +
                 ["allow", "ask", "deny"].map(function(v) { return '<option value="' + v + '"' + (permCfg.edit_policy === v ? ' selected' : '') + '>' + v + '</option>'; }).join("") +
      '        </select></label>' +
      '      <label>Dangerous tools<span class="field-hint">shell execution</span>' +
      '        <select name="dangerous_policy">' +
                 ["allow", "ask", "deny"].map(function(v) { return '<option value="' + v + '"' + (permCfg.dangerous_policy === v ? ' selected' : '') + '>' + v + '</option>'; }).join("") +
      '        </select></label>' +
      '    </div>' +
      '    <div class="text-xs muted">' + grantsSummary + '</div>' +
      '    <div class="row-gap-xs">' +
      '      <button type="submit" class="btn btn-primary">Save permissions</button>' +
      '      <button type="button" class="btn btn-default" id="btn-reset-grants">Reset remembered grants (always / session / daemon)</button>' +
      '    </div>' +
      '  </form>' +
      '</div>';

    // Agent limits card.
    html +=
      '<div class="panel">' +
      '  <h4 class="m-0 mb-md">Agent limits</h4>' +
      '  <form id="general-agent-form" class="form-stack">' +
      '    <div class="grid-2">' +
      '      <label>Maximum tool steps<span class="field-hint">Model requests per turn before stopping</span>' +
      '        <input type="number" name="max_steps" min="1" max="200" required value="' + (agentCfg.max_steps || 40) + '"></label>' +
      '      <label class="switch-card"><input type="checkbox" name="ask_on_max_steps"' + (agentCfg.ask_on_max_steps !== false ? ' checked' : '') + '>' +
      '        <div><div>Ask before continuing</div><div class="field-hint">When the step limit is reached, ask whether to give another budget.</div></div></label>' +
      '    </div>' +
      '    <div><button type="submit" class="btn btn-primary">Save agent limits</button></div>' +
      '  </form>' +
      '</div>';

    // Context & Session settings card (backed by plugins/context + core session TTL)
    html +=
      '<div class="panel">' +
      '  <h4 class="m-0 mb-md">Context & Temporary Sessions</h4>' +
      '  <form id="general-ctx-form" class="form-stack">' +
      '    <div class="grid-2">' +
      '      <label>Temporary session lifetime (hours)<span class="field-hint">Default directory session expires after inactivity</span>' +
      '        <input type="number" name="ttl_hours" min="1" value="' + (sessData.temp_ttl_hours || 24) + '"></label>' +
      '      <label>Recent messages limit<span class="field-hint">Messages kept before history compaction</span>' +
      '        <input type="number" name="max_messages" min="4" max="500" value="' + (ctxCfg.max_messages || 40) + '"></label>' +
      '    </div>' +
      '    <label class="checkbox-row"><input type="checkbox" name="load_agents_md"' + (ctxCfg.load_agents_md !== false ? ' checked' : '') + '> Automatically load AGENTS.md from working directory</label>' +
      '    <div><button type="submit" class="btn btn-primary">Save context settings</button></div>' +
      '  </form>' +
      '</div>' +
      '</div>';

    contentEl.innerHTML = html;

    document.getElementById("general-perm-form").onsubmit = async function(e) {
      e.preventDefault();
      var fd = new FormData(e.target);
      await fetch("/api/plugins/permissions/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          read_policy: fd.get("read_policy"),
          edit_policy: fd.get("edit_policy"),
          dangerous_policy: fd.get("dangerous_policy")
        })
      });
      window.cdxToast(true, "Permissions saved");
    };

    document.getElementById("btn-reset-grants").onclick = function() {
      window.cdxConfirm("Reset permissions", "Clear all remembered 'always', 'session', and 'daemon' permission grants?", async function() {
        await fetch("/api/plugins/permissions/reset-grants", { method: "POST" });
        window.cdxToast(true, "Remembered grants cleared");
        renderGeneralPage();
      });
    };

    document.getElementById("general-agent-form").onsubmit = async function(e) {
      e.preventDefault();
      var fd = new FormData(e.target);
      var res = await fetch("/api/core/agent", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          max_steps: Number(fd.get("max_steps")),
          ask_on_max_steps: fd.get("ask_on_max_steps") === "on"
        })
      });
      var out = await res.json();
      if (!res.ok || out.error) {
        window.cdxToast(false, out.error || "Failed to save agent limits");
        return;
      }
      window.cdxToast(true, "Agent limits saved");
    };

    document.getElementById("general-ctx-form").onsubmit = async function(e) {
      e.preventDefault();
      var fd = new FormData(e.target);
      await fetch("/api/core/sessions/ttl", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ hours: Number(fd.get("ttl_hours")) })
      });
      await fetch("/api/plugins/context/config", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          system_prompt: fd.get("system_prompt"),
          load_agents_md: fd.get("load_agents_md") === "on",
          max_messages: Number(fd.get("max_messages"))
        })
      });
      window.cdxToast(true, "Context settings saved");
    };
  }

  function renderModelsPage() {
    contentEl.innerHTML = '<p class="muted">Loading models...</p>';
    if (window.CDXPluginPanels["providers"]) {
      window.CDXPluginPanels["providers"](contentEl);
      return;
    }
    var s = document.createElement("script");
    s.src = "/ui/providers/panel.js";
    s.onload = function() {
      if (window.CDXPluginPanels["providers"]) {
        window.CDXPluginPanels["providers"](contentEl);
      }
    };
    document.body.appendChild(s);
  }

  async function renderSessionsPage() {
    var r = await fetch("/api/core/sessions");
    var data = await r.json();
    var list = data.sessions || [];

    var html =
      '<div class="page-header"><h3>Sessions</h3></div>' +
      '<p class="page-intro">Sessions are bound to a directory and inherited by subdirectories. Temporary sessions expire after ' + (data.temp_ttl_hours || 24) + ' hours of inactivity; persistent sessions never expire.</p>' +
      '<div class="panel" style="padding:0">';

    if (list.length === 0) {
      html += '<p class="muted" style="padding:20px">No active sessions.</p>';
    } else {
      html +=
        '<table class="table"><thead><tr>' +
        '  <th>Session</th><th>Directory</th><th>Messages</th><th>Updated</th><th></th>' +
        '</tr></thead><tbody>';
      list.forEach(function(s) {
        var dt = new Date((s.updated_at || 0) * 1000).toLocaleString();
        var badge = s.kind === "persistent"
          ? '<span class="badge badge-teal">persistent</span>'
          : '<span class="badge badge-accent">temp (' + (data.temp_ttl_hours || 24) + 'h)</span>';
        html +=
          '<tr>' +
          '  <td><div class="row-gap-xs"><a href="#" class="mono text-sm" data-view-sess="' + s.id + '" style="color:var(--accent-text);text-decoration:none">' + s.id + '</a> ' + badge + '</div></td>' +
          '  <td class="mono text-xs muted">' + s.workdir + '</td>' +
          '  <td>' + s.message_count + '</td>' +
          '  <td class="text-xs muted">' + dt + '</td>' +
          '  <td class="sess-actions">' +
          '    <div class="row-actions">' +
          '      <button class="btn btn-light btn-xs" data-mode-sess="' + s.id + '" data-mode-kind="' + (s.kind === "persistent" ? "temp" : "persistent") + '">' +
                   (s.kind === "persistent" ? 'Make temporary' : 'Make persistent') +
          '      </button>' +
          '      <button class="btn btn-danger btn-xs" data-del-sess="' + s.id + '">Delete</button>' +
          '    </div>' +
          '  </td>' +
          '</tr>';
      });
      html += '</tbody></table>';
    }
    html += '</div>';
    contentEl.innerHTML = html;

    contentEl.querySelectorAll("[data-view-sess]").forEach(function(a) {
      a.onclick = async function(e) {
        e.preventDefault();
        var id = a.getAttribute("data-view-sess");
        var res = await fetch("/api/core/sessions/" + id);
        var d = await res.json();
        var sess = d.session || {};
        var msgs = d.messages || [];

        var totalIn = sess.tokens_in || 0;
        var totalOut = sess.tokens_out || 0;
        if (totalIn === 0 && totalOut === 0) {
          msgs.forEach(function(m) {
            totalIn += (m.tokens_in || 0);
            totalOut += (m.tokens_out || 0);
          });
        }

        var h =
          '<div class="page-header">' +
          '  <div>' +
          '    <h3>Session: <span class="mono">' + id + '</span></h3>' +
          '    <div class="mono text-xs muted">' + (sess.workdir || "") + '</div>' +
               (sess.stopped_reason ? '<div class="badge badge-warn" style="margin-top:6px">stopped: ' + (sess.stopped_reason === "max_steps" ? "max tool steps" : (sess.stopped_reason === "empty_answer" ? "empty final answer" : String(sess.stopped_reason))) + '</div>' : '') +
          '  </div>' +
          '  <button class="btn btn-default btn-xs" id="back-sess">&larr; Back to sessions</button>' +
          '</div>' +
          '<div class="stat-grid">' +
          '  <div class="stat-card"><div class="label">Messages</div><div class="value">' + msgs.length + '</div></div>' +
          '  <div class="stat-card"><div class="label">Input tokens (est.)</div><div class="value">' + totalIn + '</div></div>' +
          '  <div class="stat-card"><div class="label">Output tokens (est.)</div><div class="value">' + totalOut + '</div></div>' +
          '  <div class="stat-card"><div class="label">Total tokens</div><div class="value">' + (totalIn + totalOut) + '</div></div>' +
          '</div>' +
          '<div class="stack-xs">';

        msgs.forEach(function(m) {
          var roleLabel = m.role;
          if (m.role === "assistant") {
            var modelLabel = m.model || sess.model || "";
            if (m.provider && modelLabel && modelLabel.indexOf("/") === -1) {
              modelLabel = m.provider + "/" + modelLabel;
            }
            if (modelLabel) {
              roleLabel = "assistant (" + modelLabel + ")";
            }
          } else if (m.role === "tool" && m.name) {
            roleLabel = "tool: " + m.name;
          }
          var tokMeta = (m.tokens_in || m.tokens_out)
            ? '<span class="text-xs muted">tokens: ' + (m.tokens_in || 0) + '/' + (m.tokens_out || 0) + '</span>'
            : '';
          var roleBadgeClass = m.role === "assistant" ? "badge-teal" : (m.role === "user" ? "badge-accent" : "badge-warn");
          var content = String(m.content || "");
          var reasoning = String(m.reasoning || "");
          var contentHTML = "";
          if (content.trim() !== "") {
            contentHTML = '<pre class="code-block">' + escapeHTML(content) + '</pre>';
          }
          var reasoningHTML = "";
          if (reasoning.trim() !== "") {
            reasoningHTML =
              '<details class="reasoning-block"' + (content.trim() === "" ? " open" : "") + '>' +
              '  <summary>Reasoning</summary>' +
              '  <pre class="code-block">' + escapeHTML(reasoning) + '</pre>' +
              '</details>';
          }
          var toolCallsHTML = "";
          var toolCalls = m.tool_calls || [];
          if (m.role === "assistant" && toolCalls.length > 0) {
            toolCallsHTML = '<div class="tool-calls">';
            toolCalls.forEach(function(tc) {
              var args = "";
              if (tc.arguments) {
                args = JSON.stringify(tc.arguments);
                if (args.length > 240) {
                  args = args.slice(0, 240) + "...";
                }
              }
              toolCallsHTML += '<div class="mono text-xs">' + escapeHTML(tc.name) + ' ' + escapeHTML(args) + '</div>';
            });
            toolCallsHTML += '</div>';
          }
          if (!contentHTML && !reasoningHTML && !toolCallsHTML) {
            return;
          }

          h +=
            '<div class="panel">' +
            '  <div class="row-between mb-xs">' +
            '    <span class="badge ' + roleBadgeClass + '">' + roleLabel + '</span>' +
                 tokMeta +
            '  </div>' +
                 contentHTML +
                 reasoningHTML +
                 toolCallsHTML +
            '</div>';
        });
        h += '</div>';
        contentEl.innerHTML = h;
        document.getElementById("back-sess").onclick = renderSessionsPage;
      };
    });

    contentEl.querySelectorAll("[data-mode-sess]").forEach(function(btn) {
      btn.onclick = async function() {
        var kind = btn.getAttribute("data-mode-kind");
        var res = await fetch("/api/core/sessions/" + btn.getAttribute("data-mode-sess") + "/mode", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ kind: kind })
        });
        var out = await res.json();
        if (!res.ok || out.error) {
          window.cdxToast(false, out.error || "Failed to change session mode");
          return;
        }
        window.cdxToast(true, "Session is now " + kind);
        renderSessionsPage();
      };
    });

    contentEl.querySelectorAll("[data-del-sess]").forEach(function(btn) {
      btn.onclick = function() {
        var id = btn.getAttribute("data-del-sess");
        window.cdxConfirm("Delete session", "Delete session '" + id + "' and its message history?", async function() {
          await fetch("/api/core/sessions/" + id, { method: "DELETE" });
          window.cdxToast(true, "Session deleted");
          renderSessionsPage();
        });
      };
    });
  }

  async function renderPluginsPage() {
    var r = await fetch("/api/core/plugins");
    var data = await r.json();
    var plugins = data.plugins || [];

    var html =
      '<div class="page-header"><h3>Plugins</h3></div>' +
      '<div class="panel mb-md">' +
      '  <h4 class="m-0 mb-xs">Install plugin</h4>' +
      '  <p class="field-hint mb-md">Provide a direct link to a plugin .zip archive. Before installation, cdx verifies the archive layout, manifest.json schema, version compatibility, and JS syntax.</p>' +
      '  <form id="form-install-plugin" class="row-gap-xs" style="align-items:stretch">' +
      '    <input type="text" name="url" placeholder="https://example.com/my-plugin-v1.0.0.zip" required style="flex:1">' +
      '    <button type="submit" class="btn btn-primary" id="btn-install-zip">Install from ZIP</button>' +
      '  </form>' +
      '</div>';

    html +=
      '<div class="panel" style="padding:0">' +
      '  <table class="table"><thead><tr>' +
      '    <th>Plugin</th><th>Version</th><th>Scope</th><th>Status</th><th>Tools</th><th style="text-align:right">Actions</th>' +
      '  </tr></thead><tbody>';

    plugins.forEach(function(p) {
      var statusBadge = p.status === "error"
        ? '<span class="badge badge-danger" title="' + String(p.last_error || "").replace(/"/g, "&quot;") + '">error</span>'
        : (p.enabled
            ? '<span class="badge badge-teal">enabled</span>'
            : '<span class="badge badge-warn">disabled</span>');
      var scopeBadge = p.scope === "user"
        ? '<span class="badge badge-accent">user</span>'
        : '<span class="badge badge-outline">system</span>';
      var toolCount = (p.tools || []).length;

      html +=
        '<tr>' +
        '  <td>' +
        '    <div class="fw-600">' + p.name + ' <span class="mono text-xs muted">(' + p.id + ')</span></div>' +
        '    <div class="text-xs muted">' + (p.description || "") + '</div>' +
             (p.last_error ? '<div class="text-xs" style="color:var(--danger);margin-top:2px">' + String(p.last_error).replace(/</g, "&lt;") + '</div>' : '') +
        '  </td>' +
        '  <td style="white-space:nowrap"><span class="mono text-xs">v' + p.version + '</span></td>' +
        '  <td style="white-space:nowrap">' + scopeBadge + '</td>' +
        '  <td style="white-space:nowrap">' + statusBadge + '</td>' +
        '  <td style="white-space:nowrap">' + (toolCount > 0 ? '<span class="badge badge-outline">' + toolCount + '</span>' : '<span class="muted">-</span>') + '</td>' +
        '  <td style="text-align:right;white-space:nowrap">' +
        '    <button class="btn btn-default btn-xs" data-details-plug="' + p.id + '">Details</button> ' +
        '    <button class="btn btn-light btn-xs" data-toggle-plug="' + p.id + '" data-enabled="' + (p.enabled ? "1" : "0") + '">' + (p.enabled ? "Disable" : "Enable") + '</button>' +
             (p.removable ? ' <button class="btn btn-danger btn-xs" data-del-plug="' + p.id + '">Remove</button>' : '') +
        '  </td>' +
        '</tr>';
    });

    html += '</tbody></table></div>';
    contentEl.innerHTML = html;

    var installForm = document.getElementById("form-install-plugin");
    if (installForm) {
      installForm.onsubmit = async function(e) {
        e.preventDefault();
        var btn = document.getElementById("btn-install-zip");
        var fd = new FormData(installForm);
        var url = String(fd.get("url") || "").trim();
        if (!url) return;
        btn.disabled = true;
        btn.textContent = "Verifying & Installing...";
        var res = await fetch("/api/core/plugins/install", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ url: url })
        });
        var out = await res.json();
        btn.disabled = false;
        btn.textContent = "Install from ZIP";
        if (!res.ok || out.error) {
          window.cdxToast(false, out.error || "Installation failed");
          return;
        }
        window.cdxToast(true, "Installed " + out.plugin.name + " v" + out.plugin.version);
        renderPluginsPage();
      };
    }

    contentEl.querySelectorAll("[data-toggle-plug]").forEach(function(btn) {
      btn.onclick = async function() {
        var id = btn.getAttribute("data-toggle-plug");
        var cur = btn.getAttribute("data-enabled") === "1";
        var res = await fetch("/api/core/plugins/" + encodeURIComponent(id) + "/enable", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ enabled: !cur })
        });
        var out = await res.json();
        if (!res.ok || out.error) {
          window.cdxToast(false, out.error || "Failed to toggle plugin");
          return;
        }
        window.cdxToast(true, id + (!cur ? " enabled" : " disabled"));
        renderPluginsPage();
      };
    });

    contentEl.querySelectorAll("[data-del-plug]").forEach(function(btn) {
      btn.onclick = function() {
        var id = btn.getAttribute("data-del-plug");
        window.cdxConfirm("Remove plugin", "Remove installed plugin '" + id + "' from disk?", async function() {
          var res = await fetch("/api/core/plugins/" + encodeURIComponent(id), { method: "DELETE" });
          var out = await res.json();
          if (!res.ok || out.error) {
            window.cdxToast(false, out.error || "Failed to remove plugin");
            return;
          }
          window.cdxToast(true, "Plugin " + id + " removed");
          renderPluginsPage();
        });
      };
    });

    contentEl.querySelectorAll("[data-details-plug]").forEach(function(btn) {
      btn.onclick = function() {
        var id = btn.getAttribute("data-details-plug");
        var p = plugins.find(function(x) { return x.id === id; });
        if (!p) return;
        var root = document.getElementById("cdx-modal-root");
        var toolsHTML = (p.tools || []).length === 0
          ? '<p class="muted text-sm">This plugin does not register LLM tools (it provides hooks, UI, or routes).</p>'
          : '<div class="stack-xs">' + (p.tools || []).map(function(t) {
              return '<div class="model-row"><div class="row-between"><span class="mono fw-600">' + t.name + '</span><span class="badge badge-outline">' + (t.permission || "edit") + '</span></div><div class="text-xs muted mt-2">' + (t.description || "") + '</div></div>';
            }).join("") + '</div>';

        root.innerHTML =
          '<div class="modal-backdrop">' +
          '  <div class="modal-box">' +
          '    <div class="modal-header">' +
          '      <h4>' + p.name + ' <span class="mono text-xs muted">v' + p.version + '</span></h4>' +
          '      <button class="btn-icon" id="plug-modal-close">&times;</button>' +
          '    </div>' +
          '    <div class="form-stack">' +
          '      <div class="text-sm">' + (p.description || "") + '</div>' +
          '      <div class="grid-2 text-xs">' +
          '        <div><span class="muted">ID:</span> <span class="mono">' + p.id + '</span></div>' +
          '        <div><span class="muted">Scope:</span> <span class="mono">' + p.scope + '</span></div>' +
          '        <div><span class="muted">Entry:</span> <span class="mono">' + p.entry + '</span></div>' +
          '        <div><span class="muted">Min cdx:</span> <span class="mono">' + (p.min_cdx_version || "any") + '</span></div>' +
          '      </div>' +
          '      <div class="text-xs"><span class="muted">Directory:</span> <span class="mono">' + p.dir + '</span></div>' +
                 (p.source_url ? '<div class="text-xs"><span class="muted">Source ZIP:</span> <span class="mono">' + p.source_url + '</span></div>' : '') +
          '      <div class="section-caption mt-md">Tools</div>' +
                 toolsHTML +
          '    </div>' +
          '  </div>' +
          '</div>';
        root.querySelector("#plug-modal-close").onclick = function() { root.innerHTML = ""; };
        root.querySelector(".modal-backdrop").onclick = function(e) {
          if (e.target.classList.contains("modal-backdrop")) root.innerHTML = "";
        };
      };
    });
  }

  async function renderLogsPage() {
    var r = await fetch("/api/core/logs");
    var data = await r.json();
    var lines = data.lines || [];

    var html =
      '<div class="page-header">' +
      '  <h3>Logs</h3>' +
      '  <button class="btn btn-default btn-xs" id="btn-refresh-logs">Refresh</button>' +
      '</div>';

    if (lines.length === 0) {
      html += '<div class="panel"><p class="muted m-0">Log is empty.</p></div>';
    } else {
      html +=
        '<div class="panel" style="padding:0">' +
        '  <div style="padding:10px 14px;border-bottom:1px solid var(--border)" class="text-xs muted">Lines: ' + lines.length + '</div>' +
        '  <pre class="code-block" style="border:0;border-radius:0;background:transparent">' + lines.join("\n").replace(/</g, "&lt;") + '</pre>' +
        '</div>';
    }
    contentEl.innerHTML = html;
    document.getElementById("btn-refresh-logs").onclick = renderLogsPage;
  }

  function renderPresetsPage() {
    contentEl.innerHTML = '<p class="muted">Loading presets...</p>';
    if (window.CDXPluginPanels["presets"]) {
      window.CDXPluginPanels["presets"](contentEl);
      return;
    }
    var s = document.createElement("script");
    s.src = "/ui/presets/panel.js";
    s.onload = function() {
      if (window.CDXPluginPanels["presets"]) {
        window.CDXPluginPanels["presets"](contentEl);
      }
    };
    document.body.appendChild(s);
  }

  var navItems = [
    { id: "general", label: "General", icon: "general", render: renderGeneralPage },
    { id: "models", label: "Models", icon: "models", render: renderModelsPage },
    { id: "presets", label: "Agent presets", icon: "presets", render: renderPresetsPage },
    { gap: true },
    { id: "sessions", label: "Sessions", icon: "sessions", render: renderSessionsPage },
    { id: "plugins", label: "Plugins", icon: "plugins", render: renderPluginsPage },
    { id: "logs", label: "Logs", icon: "logs", render: renderLogsPage }
  ];

  // Append any custom third-party plugin panels automatically!
  var builtinIDs = { providers: true, presets: true, permissions: true, context: true, "tools-local": true, webui: true };
  (status.ui || []).forEach(function(u) {
    if (!builtinIDs[u.plugin_id]) {
      navItems.push({
        id: "plugin:" + u.plugin_id,
        label: u.title || u.plugin_id,
        icon: "plugins",
        render: function() {
          if (u.entry) {
            contentEl.innerHTML = '<p class="muted">Loading ' + (u.title || u.plugin_id) + '...</p>';
            if (window.CDXPluginPanels[u.plugin_id]) {
              window.CDXPluginPanels[u.plugin_id](contentEl);
              return;
            }
            var s = document.createElement("script");
            s.src = u.entry;
            s.onload = function() {
              if (window.CDXPluginPanels[u.plugin_id]) {
                window.CDXPluginPanels[u.plugin_id](contentEl);
              }
            };
            document.body.appendChild(s);
            return;
          }
          contentEl.innerHTML = '<div class="page-header"><h3>' + (u.title || u.plugin_id) + '</h3></div><div class="panel"><p class="muted">' + (u.description || "") + '</p></div>';
        }
      });
    }
  });

  function selectNav(item, btnEl) {
    navEl.querySelectorAll(".nav-item").forEach(function(b) { b.classList.remove("active"); });
    if (btnEl) btnEl.classList.add("active");
    item.render();
  }

  var firstBtn = null;
  var firstItem = null;
  navItems.forEach(function(item) {
    if (item.gap) {
      var gap = document.createElement("div");
      gap.className = "nav-gap";
      navEl.appendChild(gap);
      return;
    }
    var btn = document.createElement("button");
    btn.className = "nav-item";
    btn.innerHTML = svgIcon(item.icon) + '<span>' + item.label + '</span>';
    btn.onclick = function() { selectNav(item, btn); };
    navEl.appendChild(btn);
    if (!firstBtn) {
      firstBtn = btn;
      firstItem = item;
    }
  });

  if (firstItem && firstBtn) {
    selectNav(firstItem, firstBtn);
  }
})();
