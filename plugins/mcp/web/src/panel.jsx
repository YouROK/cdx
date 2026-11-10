import { useEffect, useMemo, useState } from "react";

const PROTOCOLS = [
  ["auto", "mcp.ProtocolAuto"],
  ["2026-07-28", "mcp.Protocol728"],
  ["2025-11-25", "mcp.Protocol525"],
  ["2025-06-18", "mcp.Protocol518"]
];

const EXPOSURES = [
  ["auto", "mcp.ExposureAuto"],
  ["direct", "mcp.ExposureDirect"],
  ["gateway", "mcp.ExposureGateway"],
  ["off", "mcp.ExposureOff"]
];

const PERMISSIONS = [
  ["auto", "mcp.PermissionAuto"],
  ["read", "mcp.PermissionRead"],
  ["edit", "mcp.PermissionEdit"],
  ["dangerous", "mcp.PermissionDangerous"]
];

export function mount(container, api) {
  return api.render(<McpPanel api={api} />);
}

function Modal({ title, wide, onClose, children, t }) {
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
          <button type="button" className="btn-icon" aria-label={t("webui.Action.Close")} onClick={onClose}>
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
  const t = api.t;
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
      api.toast(t("mcp.Refreshed"));
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setBusy(false);
    }
  }

  async function remove(server) {
    const ok = await api.confirm(t("mcp.DeleteTitle"), t("mcp.DeleteConfirm", { id: server.id }));
    if (!ok) return;
    await saveList(
      data.servers.filter((item) => item.id !== server.id),
      t("mcp.Deleted")
    );
  }

  if (error && !data) {
    return (
      <div className="panel">
        <p className="muted">{error}</p>
        <button type="button" className="btn btn-default mt-md" onClick={load}>
          {t("webui.Action.Retry")}
        </button>
      </div>
    );
  }

  if (!data) {
    return <p className="muted">{t("mcp.Loading")}</p>;
  }

  const servers = data.servers || [];
  const status = data.status || {};

  return (
    <div>
      <div className="panel mb-md">
        <div className="row-between">
          <h4 className="m-0">{t("mcp.ServersTitle")}</h4>
          <div className="row-actions">
            <button type="button" className="btn btn-default" onClick={() => setImporting(true)} disabled={busy}>
              {t("mcp.ImportJson")}
            </button>
            <button
              type="button"
              className="btn btn-default"
              title={t("mcp.ReconnectTitle")}
              onClick={refreshAll}
              disabled={busy}
            >
              {t("mcp.Reconnect")}
            </button>
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => setEditing({ server: null })}
              disabled={busy}
            >
              {t("mcp.AddServer")}
            </button>
          </div>
        </div>
      </div>

      {servers.length === 0 ? (
        <div className="panel">
          <p className="muted m-0">
            {t("mcp.EmptyBefore")}
            <span className="mono">npx -y @modelcontextprotocol/server-filesystem .</span>
            {t("mcp.EmptyAfter")}
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
            t={t}
          />
        ))
      )}

      {editing ? (
        <ServerModal
          api={api}
          server={editing.server}
          servers={servers}
          t={t}
          onClose={() => setEditing(null)}
          onSaved={(next) => {
            setData(next);
            setEditing(null);
          }}
        />
      ) : null}

      {toolsFor ? <ToolsModal api={api} server={toolsFor} onClose={() => setToolsFor(null)} t={t} /> : null}

      {importing ? (
        <ImportModal
          api={api}
          t={t}
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

function statusBadge(status, t) {
  if (!status) return <span className="badge badge-outline">{t("mcp.NotConnected")}</span>;
  if (status.disabled) return <span className="badge badge-outline">{t("mcp.Disabled")}</span>;
  if (status.connected) {
    return (
      <span className="badge badge-teal">
        {status.tools !== undefined
          ? t("mcp.ConnectedTools", { count: status.tools })
          : t("mcp.Connected")}
      </span>
    );
  }
  return <span className="badge badge-warn">{t("mcp.Error")}</span>;
}

function ServerCard({ server, status, busy, onEdit, onDelete, onTools, t }) {
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
          {statusBadge(status, t)}
          <button type="button" className="btn btn-default" onClick={onTools} disabled={busy}>
            {t("mcp.Tools")}
          </button>
          <button type="button" className="btn btn-default" onClick={onEdit} disabled={busy}>
            {t("mcp.Edit")}
          </button>
          <button type="button" className="btn btn-danger" onClick={onDelete} disabled={busy}>
            {t("mcp.Delete")}
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
      {status && status.error ? <p className="field-hint mt-2">{t("mcp.ErrorPrefix")}: {status.error}</p> : null}
      {status && status.server && status.server.name ? (
        <p className="field-hint mt-2">
          {t("mcp.Server")}: {status.server.name} {status.server.version || ""}
        </p>
      ) : null}
    </div>
  );
}

function SecretRows({ title, hint, rows, placeholder, onChange, t }) {
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
            placeholder={row.key ? t("mcp.LeaveBlank") : placeholder}
            onChange={(event) => update(index, "value", event.target.value)}
          />
          <button type="button" className="btn btn-default" onClick={() => remove(index)}>
            {t("mcp.Remove")}
          </button>
        </div>
      ))}
      <button type="button" className="btn btn-default mt-2" onClick={add}>
        {t("mcp.AddVariable")}
      </button>
    </div>
  );
}

function ServerModal({ api, server, servers, onClose, onSaved, t }) {
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
      api.toast(existing ? t("mcp.ServerSaved") : t("mcp.ServerAdded"));
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
    <Modal title={existing ? t("mcp.EditServer", { id: server.id }) : t("mcp.AddServerTitle")} wide onClose={onClose} t={t}>
      <form className="form-stack" onSubmit={submit}>
        <div className="grid-2">
          <label>
            <span className="field-title">{t("mcp.ServerId")}</span>
            <span className="field-hint">{t("mcp.ServerIdHint")}</span>
            <input
              value={form.id}
              readOnly={existing}
              required
              placeholder="github"
              onChange={(event) => update("id", event.target.value)}
            />
          </label>
          <label>
            <span className="field-title">{t("mcp.Name")}</span>
            <span className="field-hint">{t("mcp.NameHint")}</span>
            <input value={form.name} placeholder="GitHub" onChange={(event) => update("name", event.target.value)} />
          </label>
        </div>

        <div className="grid-3">
          <label>
            <span className="field-title">{t("mcp.Transport")}</span>
            <span className="field-hint">{t("mcp.TransportHint")}</span>
            <select value={form.transport} onChange={(event) => update("transport", event.target.value)}>
              <option value="stdio">stdio</option>
              <option value="http">HTTP</option>
            </select>
          </label>
          <label>
            <span className="field-title">{t("mcp.Protocol")}</span>
            <span className="field-hint">{t("mcp.ProtocolHint")}</span>
            <select value={form.protocol} onChange={(event) => update("protocol", event.target.value)}>
              {PROTOCOLS.map(([value, label]) => (
                <option key={value} value={value}>
                  {t(label)}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-title">{t("mcp.Timeout")}</span>
            <span className="field-hint">{t("mcp.TimeoutHint")}</span>
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
              <span className="field-title">{t("mcp.Endpoint")}</span>
              <span className="field-hint">{t("mcp.EndpointHint")}</span>
              <input
                value={form.url}
                required
                placeholder="https://mcp.example.com/mcp"
                onChange={(event) => update("url", event.target.value)}
              />
            </label>
            <label>
              <span className="field-title">{t("mcp.Headers")}</span>
              <span className="field-hint">{t("mcp.HeadersHint")}</span>
              <textarea
                rows={3}
                value={form.headersText}
                placeholder="X-Tenant=acme"
                onChange={(event) => update("headersText", event.target.value)}
              />
            </label>
            <SecretRows
              title={t("mcp.SecretHeaders")}
              hint={t("mcp.SecretHeadersHint")}
              t={t}
              rows={form.headerSecrets}
              placeholder="Bearer token"
              onChange={(rows) => update("headerSecrets", rows)}
            />
          </div>
        ) : (
          <div className="form-stack">
            <div className="grid-2">
              <label>
                <span className="field-title">{t("mcp.Command")}</span>
                <span className="field-hint">{t("mcp.CommandHint")}</span>
                <input
                  value={form.command}
                  required
                  placeholder="npx"
                  onChange={(event) => update("command", event.target.value)}
                />
              </label>
              <label>
                <span className="field-title">{t("mcp.Workdir")}</span>
                <span className="field-hint">{t("mcp.WorkdirHint")}</span>
                <input
                  value={form.cwd}
                  placeholder={t("mcp.WorkdirPlaceholder")}
                  onChange={(event) => update("cwd", event.target.value)}
                />
              </label>
            </div>
            <label>
              <span className="field-title">{t("mcp.Arguments")}</span>
              <span className="field-hint">{t("mcp.ArgumentsHint")}</span>
              <textarea
                rows={3}
                value={form.argsText}
                placeholder={"-y\n@modelcontextprotocol/server-filesystem\n."}
                onChange={(event) => update("argsText", event.target.value)}
              />
            </label>
            <label>
              <span className="field-title">{t("mcp.Environment")}</span>
              <span className="field-hint">{t("mcp.EnvironmentHint")}</span>
              <textarea
                rows={3}
                value={form.envText}
                placeholder="LOG_LEVEL=info"
                onChange={(event) => update("envText", event.target.value)}
              />
            </label>
            <SecretRows
              title={t("mcp.SecretEnvironment")}
              hint={t("mcp.SecretHeadersHint")}
              t={t}
              rows={form.envSecrets}
              placeholder="token value"
              onChange={(rows) => update("envSecrets", rows)}
            />
          </div>
        )}

        <div className="grid-3">
          <label>
            <span className="field-title">{t("mcp.Exposure")}</span>
            <span className="field-hint">{t("mcp.ExposureHint")}</span>
            <select value={form.expose} onChange={(event) => update("expose", event.target.value)}>
              {EXPOSURES.map(([value, label]) => (
                <option key={value} value={value}>
                  {t(label)}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-title">{t("mcp.Permission")}</span>
            <span className="field-hint">{t("mcp.PermissionHint")}</span>
            <select value={form.permission} onChange={(event) => update("permission", event.target.value)}>
              {PERMISSIONS.map(([value, label]) => (
                <option key={value} value={value}>
                  {t(label)}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-title">{t("mcp.ConnectServer")}</span>
            <span className="check-field">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(event) => update("enabled", event.target.checked)}
              />
              <span>{t("webui.Schema.Enabled")}</span>
            </span>
            <span className="field-hint">{t("mcp.EnabledHint")}</span>
          </label>
        </div>

        {testResult ? (
          <div className="panel">
            {testResult.ok ? (
              <p className="m-0">
                {t("mcp.ConnectedTo", {
                  server: testResult.server && testResult.server.name ? testResult.server.name : t("mcp.TheServer"),
                  revision: testResult.revision ? " (" + t("mcp.Revision", { revision: testResult.revision }) + ")" : ""
                })}{" "}
                {t("mcp.ToolsCount", { count: (testResult.tools || []).length })}
              </p>
            ) : (
              <p className="m-0">{t("mcp.ConnectionFailed", { error: testResult.error })}</p>
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
              {testing ? t("mcp.Testing") : t("mcp.TestConnection")}
            </button>
          </div>
          <div className="row-actions">
            <button type="button" className="btn btn-default" onClick={onClose} disabled={saving}>
              {t("webui.Action.Cancel")}
            </button>
            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? t("webui.Schema.Saving") : t("mcp.SaveServer")}
            </button>
          </div>
        </div>
      </form>
    </Modal>
  );
}

function ToolsModal({ api, server, onClose, t }) {
  const [tools, setTools] = useState(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .fetchJSON("/api/plugins/mcp/tools?server=" + encodeURIComponent(server.id))
      .then((data) => setTools(data.tools || []))
      .catch((err) => setError(String(err.message || err)));
  }, []);

  return (
    <Modal title={t("mcp.ToolsOf", { id: server.id })} wide onClose={onClose} t={t}>
      {error ? <p className="muted">{error}</p> : null}
      {!tools && !error ? <p className="muted">{t("mcp.LoadingTools")}</p> : null}
      {tools && tools.length === 0 ? (
        <p className="muted">{t("mcp.EmptyCatalog")}</p>
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

function ImportModal({ api, onClose, onImported, t }) {
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
      api.toast(t("mcp.Imported", { count: result.imported || 0 }));
      onImported(result);
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setImporting(false);
    }
  }

  return (
    <Modal title={t("mcp.ImportTitle")} wide onClose={onClose} t={t}>
      <form className="form-stack" onSubmit={submit}>
        <label>
          <span className="field-title">mcp.json</span>
          <span className="field-hint">{t("mcp.ImportHint")}</span>
          <textarea
            rows={10}
            value={text}
            placeholder={'{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"],"env":{"GITHUB_TOKEN":"..."}}}}'}
            onChange={(event) => setText(event.target.value)}
          />
        </label>
        {text.trim() && preview === null ? (
          <p className="field-hint">{t("mcp.InvalidJson")}</p>
        ) : null}
        {preview && preview.length > 0 ? (
          <div className="panel">
            <div className="field-title">{t("mcp.ServersFound", { count: preview.length })}</div>
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
          <span className="field-hint">{t("mcp.ReplaceHint")}</span>
          <div className="row-actions">
            <button type="button" className="btn btn-default" onClick={onClose} disabled={importing}>
              {t("webui.Action.Cancel")}
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={importing || !preview || preview.length === 0}
            >
              {importing ? t("mcp.Importing") : t("mcp.Import")}
            </button>
          </div>
        </div>
      </form>
    </Modal>
  );
}
