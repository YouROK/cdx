import { useEffect, useMemo, useState } from "react";

const PROTOCOLS = [
  ["auto", "Auto detect"],
  ["2026-07-28", "2026-07-28 (stateless)"],
  ["2025-11-25", "2025-11-25 (initialize)"],
  ["2025-06-18", "2025-06-18 (initialize)"]
];

const EXPOSURES = [
  ["auto", "Automatic"],
  ["direct", "Direct tools"],
  ["gateway", "Gateway tool"],
  ["off", "Hidden"]
];

const PERMISSIONS = [
  ["auto", "From tool annotations"],
  ["read", "Read"],
  ["edit", "Edit"],
  ["dangerous", "Dangerous"]
];

export function mount(container, api) {
  return api.render(<McpPanel api={api} />);
}

function Modal({ title, wide, onClose, children }) {
  useEffect(() => {
    function onKey(event) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="modal-backdrop"
      onClick={(event) => {
        if (event.target.classList.contains("modal-backdrop")) onClose();
      }}
    >
      <div className={"modal-box" + (wide ? " modal-wide" : "")} role="dialog" aria-modal="true" aria-label={title}>
        <div className="modal-header">
          <h4>{title}</h4>
          <button type="button" className="btn-icon" aria-label="Close" onClick={onClose}>
            &times;
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  );
}

function objectToLines(value) {
  if (!value) return "";
  return Object.keys(value)
    .sort()
    .map((key) => key + "=" + value[key])
    .join("\n");
}

function linesToObject(text) {
  const out = {};
  String(text || "")
    .split("\n")
    .forEach((line) => {
      const trimmed = line.trim();
      if (!trimmed || trimmed.charAt(0) === "#") return;
      const index = trimmed.indexOf("=");
      if (index <= 0) return;
      const key = trimmed.slice(0, index).trim();
      if (key) out[key] = trimmed.slice(index + 1).trim();
    });
  return out;
}

function secretObject(rows) {
  const out = {};
  (rows || []).forEach((row) => {
    const key = String(row.key || "").trim();
    if (!key) return;
    out[key] = row.value || "";
  });
  return out;
}

function emptyForm() {
  return {
    id: "",
    name: "",
    enabled: true,
    transport: "stdio",
    command: "",
    argsText: "",
    cwd: "",
    envText: "",
    url: "",
    headersText: "",
    protocol: "auto",
    timeout_ms: 120000,
    expose: "auto",
    permission: "auto",
    envSecrets: [],
    headerSecrets: []
  };
}

function formFromServer(server) {
  return {
    id: server.id,
    name: server.name || "",
    enabled: server.enabled !== false,
    transport: server.transport || "stdio",
    command: server.command || "",
    argsText: (server.args || []).join("\n"),
    cwd: server.cwd || "",
    envText: objectToLines(server.env),
    url: server.url || "",
    headersText: objectToLines(server.headers),
    protocol: server.protocol || "auto",
    timeout_ms: server.timeout_ms || 120000,
    expose: server.expose || "auto",
    permission: server.permission || "auto",
    envSecrets: (server.env_secrets || []).map((key) => ({ key: key, value: "" })),
    headerSecrets: (server.header_secrets || []).map((key) => ({ key: key, value: "" }))
  };
}

function formToServer(form) {
  const server = {
    id: String(form.id || "").trim().toLowerCase(),
    name: String(form.name || "").trim(),
    enabled: form.enabled !== false,
    transport: form.transport === "http" ? "http" : "stdio",
    protocol: form.protocol || "auto",
    timeout_ms: Number(form.timeout_ms) || 0,
    expose: form.expose || "direct",
    permission: form.permission || "auto"
  };
  if (server.transport === "http") {
    server.url = String(form.url || "").trim();
    server.headers = linesToObject(form.headersText);
    server.header_secrets = secretObject(form.headerSecrets);
  } else {
    server.command = String(form.command || "").trim();
    server.args = String(form.argsText || "")
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean);
    server.cwd = String(form.cwd || "").trim();
    server.env = linesToObject(form.envText);
    server.env_secrets = secretObject(form.envSecrets);
  }
  return server;
}

function McpPanel({ api }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(null);
  const [toolsFor, setToolsFor] = useState(null);
  const [importing, setImporting] = useState(false);
  const [busy, setBusy] = useState(false);

  async function load() {
    try {
      setData(await api.fetchJSON("/api/plugins/mcp/servers"));
      setError("");
    } catch (err) {
      setError(String(err.message || err));
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function saveList(servers, message) {
    setBusy(true);
    try {
      const next = await api.postJSON("/api/plugins/mcp/servers", { servers });
      setData(next);
      if (message) api.toast(message);
      return true;
    } catch (err) {
      api.toast(String(err.message || err), false);
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function refreshAll() {
    setBusy(true);
    try {
      setData(await api.postJSON("/api/plugins/mcp/refresh", {}));
      api.toast("Tool catalogs refreshed");
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setBusy(false);
    }
  }

  async function remove(server) {
    const ok = await api.confirm(
      "Delete MCP server",
      "Delete server '" + server.id + "' and its stored secrets?"
    );
    if (!ok) return;
    await saveList(
      data.servers.filter((item) => item.id !== server.id),
      "Server deleted"
    );
  }

  if (error && !data) {
    return (
      <div className="panel">
        <p className="muted">{error}</p>
        <button type="button" className="btn btn-default mt-md" onClick={load}>
          Retry
        </button>
      </div>
    );
  }

  if (!data) {
    return <p className="muted">Loading MCP servers...</p>;
  }

  const servers = data.servers || [];
  const status = data.status || {};

  return (
    <div>
      <div className="panel mb-md">
        <div className="row-between">
          <h4 className="m-0">Model Context Protocol servers</h4>
          <div className="row-actions">
            <button type="button" className="btn btn-default" onClick={() => setImporting(true)} disabled={busy}>
              Import mcp.json
            </button>
            <button
              type="button"
              className="btn btn-default"
              title="Reconnect to every server and read its tool catalog again"
              onClick={refreshAll}
              disabled={busy}
            >
              Reconnect
            </button>
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => setEditing({ server: null })}
              disabled={busy}
            >
              Add server
            </button>
          </div>
        </div>
      </div>

      {servers.length === 0 ? (
        <div className="panel">
          <p className="muted m-0">
            No MCP servers yet. Add one with its command, such as{" "}
            <span className="mono">npx -y @modelcontextprotocol/server-filesystem .</span>, or import an existing
            <span className="mono"> mcp.json</span>.
          </p>
        </div>
      ) : (
        servers.map((server) => (
          <ServerCard
            key={server.id}
            server={server}
            status={status[server.id]}
            busy={busy}
            onEdit={() => setEditing({ server })}
            onDelete={() => remove(server)}
            onTools={() => setToolsFor(server)}
          />
        ))
      )}

      {editing ? (
        <ServerModal
          api={api}
          server={editing.server}
          servers={servers}
          onClose={() => setEditing(null)}
          onSaved={(next) => {
            setData(next);
            setEditing(null);
          }}
        />
      ) : null}

      {toolsFor ? <ToolsModal api={api} server={toolsFor} onClose={() => setToolsFor(null)} /> : null}

      {importing ? (
        <ImportModal
          api={api}
          onClose={() => setImporting(false)}
          onImported={(next) => {
            setData(next);
            setImporting(false);
          }}
        />
      ) : null}
    </div>
  );
}

function statusBadge(status) {
  if (!status) return <span className="badge badge-outline">not connected</span>;
  if (status.disabled) return <span className="badge badge-outline">disabled</span>;
  if (status.connected) {
    return (
      <span className="badge badge-teal">
        connected{status.tools !== undefined ? " - " + status.tools + " tools" : ""}
      </span>
    );
  }
  return <span className="badge badge-warn">error</span>;
}

function ServerCard({ server, status, busy, onEdit, onDelete, onTools }) {
  const revision = status && status.revision ? status.revision : "";
  return (
    <div className="panel mb-md">
      <div className="row-between">
        <div>
          <h4 className="m-0">
            {server.name || server.id}{" "}
            <span className="badge badge-outline">{server.transport === "http" ? "HTTP" : "stdio"}</span>{" "}
            <span className="badge badge-outline">{server.expose}</span>
          </h4>
          <div className="field-hint mono">
            {server.id}
            {revision ? " - " + revision : ""}
          </div>
        </div>
        <div className="row-actions">
          {statusBadge(status)}
          <button type="button" className="btn btn-default" onClick={onTools} disabled={busy}>
            Tools
          </button>
          <button type="button" className="btn btn-default" onClick={onEdit} disabled={busy}>
            Edit
          </button>
          <button type="button" className="btn btn-danger" onClick={onDelete} disabled={busy}>
            Delete
          </button>
        </div>
      </div>
      {server.transport === "http" ? (
        <p className="field-hint mono mt-2">{server.url}</p>
      ) : (
        <p className="field-hint mono mt-2">
          {server.command} {(server.args || []).join(" ")}
        </p>
      )}
      {status && status.error ? <p className="field-hint mt-2">Error: {status.error}</p> : null}
      {status && status.server && status.server.name ? (
        <p className="field-hint mt-2">
          Server: {status.server.name} {status.server.version || ""}
        </p>
      ) : null}
    </div>
  );
}

function SecretRows({ title, hint, rows, placeholder, onChange }) {
  function update(index, field, value) {
    const next = rows.slice();
    next[index] = { ...next[index], [field]: value };
    onChange(next);
  }
  function remove(index) {
    onChange(rows.filter((_, position) => position !== index));
  }
  function add() {
    onChange([...rows, { key: "", value: "" }]);
  }

  return (
    <div>
      <div className="field-title">{title}</div>
      <div className="field-hint">{hint}</div>
      {rows.map((row, index) => (
        <div
          key={index}
          style={{ display: "grid", gridTemplateColumns: "1fr 1fr auto", gap: "8px", marginTop: "8px" }}
        >
          <input
            value={row.key}
            placeholder="NAME"
            onChange={(event) => update(index, "key", event.target.value)}
          />
          <input
            type="password"
            value={row.value}
            placeholder={row.key ? "leave blank to keep" : placeholder}
            onChange={(event) => update(index, "value", event.target.value)}
          />
          <button type="button" className="btn btn-default" onClick={() => remove(index)}>
            Remove
          </button>
        </div>
      ))}
      <button type="button" className="btn btn-default mt-2" onClick={add}>
        Add variable
      </button>
    </div>
  );
}

function ServerModal({ api, server, servers, onClose, onSaved }) {
  const existing = Boolean(server);
  const [form, setForm] = useState(server ? formFromServer(server) : emptyForm());
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState(null);

  function update(name, value) {
    setForm((current) => ({ ...current, [name]: value }));
  }

  async function submit(event) {
    event.preventDefault();
    const payload = formToServer(form);
    const next = servers.filter((item) => item.id !== payload.id);
    next.push(payload);
    next.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    setSaving(true);
    try {
      const result = await api.postJSON("/api/plugins/mcp/servers", { servers: next });
      api.toast(existing ? "Server saved" : "Server added");
      onSaved(result);
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setSaving(false);
    }
  }

  async function test() {
    setTesting(true);
    setTestResult(null);
    try {
      const result = await api.postJSON("/api/plugins/mcp/servers/test", { server: formToServer(form) });
      setTestResult(result);
    } catch (err) {
      setTestResult({ ok: false, error: String(err.message || err) });
    } finally {
      setTesting(false);
    }
  }

  return (
    <Modal title={existing ? "Edit server: " + server.id : "Add MCP server"} wide onClose={onClose}>
      <form className="form-stack" onSubmit={submit}>
        <div className="grid-2">
          <label>
            <span className="field-title">Server id</span>
            <span className="field-hint">Used in tool names and permission rules; cannot change later</span>
            <input
              value={form.id}
              readOnly={existing}
              required
              placeholder="github"
              onChange={(event) => update("id", event.target.value)}
            />
          </label>
          <label>
            <span className="field-title">Name</span>
            <span className="field-hint">Shown in this page</span>
            <input value={form.name} placeholder="GitHub" onChange={(event) => update("name", event.target.value)} />
          </label>
        </div>

        <div className="grid-3">
          <label>
            <span className="field-title">Transport</span>
            <span className="field-hint">A local program or an HTTP endpoint</span>
            <select value={form.transport} onChange={(event) => update("transport", event.target.value)}>
              <option value="stdio">stdio</option>
              <option value="http">HTTP</option>
            </select>
          </label>
          <label>
            <span className="field-title">Protocol</span>
            <span className="field-hint">Auto detects the server revision</span>
            <select value={form.protocol} onChange={(event) => update("protocol", event.target.value)}>
              {PROTOCOLS.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-title">Request timeout</span>
            <span className="field-hint">Milliseconds, 0 uses the default of 120000</span>
            <input
              type="number"
              min="0"
              value={form.timeout_ms}
              onChange={(event) => update("timeout_ms", event.target.value)}
            />
          </label>
        </div>

        {form.transport === "http" ? (
          <div className="form-stack">
            <label>
              <span className="field-title">Endpoint URL</span>
              <span className="field-hint">The MCP endpoint, for example https://mcp.example.com/mcp</span>
              <input
                value={form.url}
                required
                placeholder="https://mcp.example.com/mcp"
                onChange={(event) => update("url", event.target.value)}
              />
            </label>
            <label>
              <span className="field-title">Headers</span>
              <span className="field-hint">One KEY=VALUE per line, stored as plain values</span>
              <textarea
                rows={3}
                value={form.headersText}
                placeholder="X-Tenant=acme"
                onChange={(event) => update("headersText", event.target.value)}
              />
            </label>
            <SecretRows
              title="Secret headers"
              hint="Stored in the encrypted secrets; leave a value blank to keep the saved one"
              rows={form.headerSecrets}
              placeholder="Bearer token"
              onChange={(rows) => update("headerSecrets", rows)}
            />
          </div>
        ) : (
          <div className="form-stack">
            <div className="grid-2">
              <label>
                <span className="field-title">Command</span>
                <span className="field-hint">The program cdx starts, for example npx</span>
                <input
                  value={form.command}
                  required
                  placeholder="npx"
                  onChange={(event) => update("command", event.target.value)}
                />
              </label>
              <label>
                <span className="field-title">Working directory</span>
                <span className="field-hint">Empty uses the session working directory</span>
                <input
                  value={form.cwd}
                  placeholder="empty = session directory"
                  onChange={(event) => update("cwd", event.target.value)}
                />
              </label>
            </div>
            <label>
              <span className="field-title">Arguments</span>
              <span className="field-hint">One argument per line</span>
              <textarea
                rows={3}
                value={form.argsText}
                placeholder={"-y\n@modelcontextprotocol/server-filesystem\n."}
                onChange={(event) => update("argsText", event.target.value)}
              />
            </label>
            <label>
              <span className="field-title">Environment</span>
              <span className="field-hint">One KEY=VALUE per line, stored as plain values</span>
              <textarea
                rows={3}
                value={form.envText}
                placeholder="LOG_LEVEL=info"
                onChange={(event) => update("envText", event.target.value)}
              />
            </label>
            <SecretRows
              title="Secret environment"
              hint="Stored in the encrypted secrets; leave a value blank to keep the saved one"
              rows={form.envSecrets}
              placeholder="token value"
              onChange={(rows) => update("envSecrets", rows)}
            />
          </div>
        )}

        <div className="grid-3">
          <label>
            <span className="field-title">Exposure</span>
            <span className="field-hint">
              Automatic uses direct tools up to 20 per server and the gateway tool above that
            </span>
            <select value={form.expose} onChange={(event) => update("expose", event.target.value)}>
              {EXPOSURES.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-title">Permission</span>
            <span className="field-hint">Overrides the level derived from tool annotations</span>
            <select value={form.permission} onChange={(event) => update("permission", event.target.value)}>
              {PERMISSIONS.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-title">Connect this server</span>
            <span className="check-field">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(event) => update("enabled", event.target.checked)}
              />
              <span>Enabled</span>
            </span>
            <span className="field-hint">
              A disabled server stays in the list, but cdx does not start it and does not offer its tools to the
              model
            </span>
          </label>
        </div>

        {testResult ? (
          <div className="panel">
            {testResult.ok ? (
              <p className="m-0">
                Connected to {testResult.server && testResult.server.name ? testResult.server.name : "the server"}
                {testResult.revision ? " with revision " + testResult.revision : ""}:{" "}
                {(testResult.tools || []).length} tools.
              </p>
            ) : (
              <p className="m-0">Connection failed: {testResult.error}</p>
            )}
            {testResult.ok && (testResult.tools || []).length > 0 ? (
              <p className="field-hint mt-2 mono">
                {(testResult.tools || []).map((tool) => tool.name).join(", ")}
              </p>
            ) : null}
          </div>
        ) : null}

        <div className="row-between">
          <div className="row-actions">
            <button type="button" className="btn btn-default" onClick={test} disabled={testing || saving}>
              {testing ? "Testing..." : "Test connection"}
            </button>
          </div>
          <div className="row-actions">
            <button type="button" className="btn btn-default" onClick={onClose} disabled={saving}>
              Cancel
            </button>
            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? "Saving..." : "Save server"}
            </button>
          </div>
        </div>
      </form>
    </Modal>
  );
}

function ToolsModal({ api, server, onClose }) {
  const [tools, setTools] = useState(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .fetchJSON("/api/plugins/mcp/tools?server=" + encodeURIComponent(server.id))
      .then((data) => setTools(data.tools || []))
      .catch((err) => setError(String(err.message || err)));
  }, []);

  return (
    <Modal title={"Tools of " + server.id} wide onClose={onClose}>
      {error ? <p className="muted">{error}</p> : null}
      {!tools && !error ? <p className="muted">Loading tools...</p> : null}
      {tools && tools.length === 0 ? (
        <p className="muted">
          The catalog is empty. Connect or refresh the server to read its tools.
        </p>
      ) : null}
      {(tools || []).map((tool) => (
        <div key={tool.name} className="panel mb-xs">
          <div className="mono text-sm">{tool.name}</div>
          {tool.description ? <p className="field-hint mt-2">{tool.description}</p> : null}
        </div>
      ))}
    </Modal>
  );
}

function ImportModal({ api, onClose, onImported }) {
  const [text, setText] = useState("");
  const [importing, setImporting] = useState(false);

  const preview = useMemo(() => {
    if (!text.trim()) return null;
    try {
      const parsed = JSON.parse(text);
      const source = parsed.mcpServers || parsed.servers || {};
      return Object.keys(source).map((name) => {
        const entry = source[name] || {};
        return {
          name: name,
          transport: entry.url ? "HTTP" : "stdio",
          command: entry.url || [entry.command || "", ...(entry.args || [])].join(" ")
        };
      });
    } catch {
      return null;
    }
  }, [text]);

  async function submit(event) {
    event.preventDefault();
    setImporting(true);
    try {
      const result = await api.postJSON("/api/plugins/mcp/import", { json: text });
      api.toast("Imported " + (result.imported || 0) + " servers");
      onImported(result);
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setImporting(false);
    }
  }

  return (
    <Modal title="Import MCP servers" wide onClose={onClose}>
      <form className="form-stack" onSubmit={submit}>
        <label>
          <span className="field-title">mcp.json</span>
          <span className="field-hint">
            Paste the file used by Claude Desktop or Cursor. Values from env and headers are moved into the
            encrypted secrets; review the commands before saving.
          </span>
          <textarea
            rows={10}
            value={text}
            placeholder={'{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"],"env":{"GITHUB_TOKEN":"..."}}}}'}
            onChange={(event) => setText(event.target.value)}
          />
        </label>
        {text.trim() && preview === null ? (
          <p className="field-hint">The text is not valid JSON.</p>
        ) : null}
        {preview && preview.length > 0 ? (
          <div className="panel">
            <div className="field-title">Servers found: {preview.length}</div>
            {preview.map((entry) => (
              <div key={entry.name} className="mt-2">
                <span className="badge badge-outline">{entry.transport}</span>{" "}
                <span className="mono text-sm">{entry.name}</span>
                <div className="field-hint mono">{entry.command}</div>
              </div>
            ))}
          </div>
        ) : null}
        <div className="row-between">
          <span className="field-hint">Existing servers with the same id are replaced by the imported entry.</span>
          <div className="row-actions">
            <button type="button" className="btn btn-default" onClick={onClose} disabled={importing}>
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={importing || !preview || preview.length === 0}
            >
              {importing ? "Importing..." : "Import"}
            </button>
          </div>
        </div>
      </form>
    </Modal>
  );
}
