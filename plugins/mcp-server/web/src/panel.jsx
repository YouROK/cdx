import { useCallback, useEffect, useState } from "react";

const CLASSES = ["read", "edit", "dangerous", "task"];
const POLICIES = ["allow", "ask", "deny"];
const CLASS_KEYS = {
  read: "mcpserver.ClassRead",
  edit: "mcpserver.ClassEdit",
  dangerous: "mcpserver.ClassDangerous",
  task: "mcpserver.ClassTask"
};
const CLASS_HINTS = {
  read: "mcpserver.ClassReadHint",
  edit: "mcpserver.ClassEditHint",
  dangerous: "mcpserver.ClassDangerousHint",
  task: "mcpserver.ClassTaskHint"
};
const POLICY_KEYS = {
  allow: "mcpserver.PolicyAllow",
  ask: "mcpserver.PolicyAsk",
  deny: "mcpserver.PolicyDeny"
};

// Only alignment and a couple of rows: the shell stylesheet already styles the
// fields, labels, and buttons inside the shadow root.
const STYLES = `
.inline-form { display: flex; align-items: center; gap: 8px; }
.inline-form input { flex: 1 1 auto; }
.token-line { display: flex; align-items: center; gap: 8px; }
.token-line code { flex: 1 1 auto; overflow-wrap: anywhere; }
.client-row {
  display: flex; align-items: flex-end; justify-content: space-between;
  gap: 16px; padding: 10px 0; border-top: 1px solid var(--border); flex-wrap: wrap;
}
.client-meta { min-width: 220px; }
.perm-row { display: flex; align-items: flex-end; gap: 10px; flex-wrap: wrap; }
.perm-row label { min-width: 96px; }
.perm-row select { min-width: 96px; }
/* The button is shorter than a select, so half the difference lifts its centre
   to the centre of the fields next to it. */
.perm-row > .btn { margin-bottom: 5px; }
.warn-text { color: var(--warning); }
`;

export function mount(container, api) {
  return api.render(<MCPServerPanel api={api} />);
}

function CopyIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="9" y="9" width="12" height="12" rx="2" />
      <path d="M5 15V5a2 2 0 0 1 2-2h8" />
    </svg>
  );
}

function MCPServerPanel({ api }) {
  const t = api.t;
  const [state, setState] = useState(null);
  const [approvals, setApprovals] = useState([]);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [issued, setIssued] = useState(null);
  const [creating, setCreating] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => api.addStyles(STYLES), [api]);

  const fail = useCallback((err) => {
    setError(String((err && err.message) || err));
  }, []);

  const load = useCallback(async () => {
    try {
      const data = await api.fetchJSON("/api/plugins/mcp-server/clients");
      setState(data);
      setError("");
    } catch (err) {
      fail(err);
    }
  }, [api, fail]);

  const loadApprovals = useCallback(async () => {
    try {
      const data = await api.fetchJSON("/api/plugins/mcp-server/approvals");
      setApprovals(Array.isArray(data.approvals) ? data.approvals : []);
    } catch (err) {
      fail(err);
    }
  }, [api, fail]);

  useEffect(() => {
    load();
    loadApprovals();
  }, [load, loadApprovals]);

  // A pending approval waits for the operator, so the list refreshes by itself.
  useEffect(() => {
    const timer = setInterval(loadApprovals, 4000);
    return () => clearInterval(timer);
  }, [loadApprovals]);

  async function toggleEnabled() {
    if (!state) return;
    setSaving(true);
    try {
      const next = !state.enabled;
      await api.postJSON("/api/core/plugins/mcp-server/settings", { enabled: next });
      setState({ ...state, enabled: next });
      api.toast(t("mcpserver.Saved"));
    } catch (err) {
      fail(err);
    } finally {
      setSaving(false);
    }
  }

  async function createClient(event) {
    event.preventDefault();
    const label = name.trim();
    if (!label) return;
    setCreating(true);
    setError("");
    try {
      const created = await api.postJSON("/api/plugins/mcp-server/clients", { name: label });
      setIssued(created);
      setName("");
      await load();
    } catch (err) {
      fail(err);
    } finally {
      setCreating(false);
    }
  }

  async function copyToken() {
    if (!issued || !issued.token) return;
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(issued.token);
      } else {
        const area = document.createElement("textarea");
        area.value = issued.token;
        document.body.appendChild(area);
        area.select();
        document.execCommand("copy");
        area.remove();
      }
      api.toast(t("mcpserver.Copied"));
    } catch (err) {
      api.toast(String((err && err.message) || err), false);
    }
  }

  async function setPermission(client, className, value) {
    try {
      const data = await api.postJSON(
        "/api/plugins/mcp-server/clients/" + client.id + "/permissions",
        { [className]: value }
      );
      setState((current) => ({
        ...current,
        clients: current.clients.map((item) => (item.id === client.id ? data.client : item))
      }));
    } catch (err) {
      fail(err);
    }
  }

  async function revoke(client) {
    const ok = await api.confirm(
      t("mcpserver.RevokeTitle"),
      t("mcpserver.RevokeConfirm").replace("{name}", client.name)
    );
    if (!ok) return;
    try {
      await api.fetchJSON("/api/plugins/mcp-server/clients/" + client.id, { method: "DELETE" });
      if (issued && issued.client && issued.client.id === client.id) setIssued(null);
      await load();
    } catch (err) {
      fail(err);
    }
  }

  async function decide(approval, decision) {
    try {
      await api.postJSON("/api/plugins/mcp-server/approvals/" + approval.id + "/" + decision, {});
      await loadApprovals();
    } catch (err) {
      fail(err);
    }
  }

  function lastSeen(client) {
    if (!client.last_seen_at) return t("mcpserver.Never");
    return new Date(client.last_seen_at * 1000).toLocaleString();
  }

  if (!state) {
    return <p className="muted m-0">{error || t("webui.Schema.Loading")}</p>;
  }

  return (
    <div className="form-stack">
      {error ? <div className="panel error-panel">{error}</div> : null}

      <div className="panel">
        <label>
          <span className="field-title">{t("mcpserver.Enabled")}</span>
          <span className="check-field">
            <input type="checkbox" checked={!!state.enabled} disabled={saving} onChange={toggleEnabled} />
            <span>{t("mcpserver.EnabledBox")}</span>
          </span>
          <span className="field-hint">{t("mcpserver.EnabledHint")}</span>
          <span className="field-hint warn-text">{t("mcpserver.AskNote")}</span>
        </label>
      </div>

      <div className="panel">
        <h4 className="m-0 mb-xs">{t("mcpserver.Clients")}</h4>
        <p className="field-hint mb-md">{t("mcpserver.ClientsHint")}</p>

        <form className="inline-form mb-md" onSubmit={createClient}>
          <input
            type="text"
            value={name}
            placeholder={t("mcpserver.ClientName")}
            maxLength={64}
            onChange={(event) => setName(event.target.value)}
          />
          <button type="submit" className="btn btn-primary" disabled={creating || !name.trim()}>
            {creating ? t("mcpserver.Creating") : t("mcpserver.Create")}
          </button>
        </form>

        {issued ? (
          <div className="panel mb-md">
            <div className="text-sm mb-xs">{t("mcpserver.TokenOnce")}</div>
            <div className="token-line">
              <code>{issued.token}</code>
              <button
                type="button"
                className="btn btn-default btn-xs"
                onClick={copyToken}
                title={t("mcpserver.CopyToken")}
                aria-label={t("mcpserver.CopyToken")}
              >
                <CopyIcon />
              </button>
            </div>
            <p className="field-hint m-0">{t("mcpserver.TokenOnceHint")}</p>
          </div>
        ) : null}

        {state.clients.length === 0 ? (
          <p className="muted m-0">{t("mcpserver.NoClients")}</p>
        ) : (
          state.clients.map((client) => (
            <div className="client-row" key={client.id}>
              <div className="client-meta">
                <div className="text-sm">{client.name}</div>
                <div className="field-hint m-0">
                  {t("mcpserver.TokenId")} <code>{client.id}</code> · {lastSeen(client)}
                </div>
              </div>
              <div className="perm-row">
                {CLASSES.map((className) => (
                  <label key={className} title={t(CLASS_HINTS[className])}>
                    <span className="field-title">{t(CLASS_KEYS[className])}</span>
                    <select
                      value={client.permissions[className] || "deny"}
                      onChange={(event) => setPermission(client, className, event.target.value)}
                    >
                      {POLICIES.map((policy) => (
                        <option key={policy} value={policy}>
                          {t(POLICY_KEYS[policy])}
                        </option>
                      ))}
                    </select>
                  </label>
                ))}
                <button type="button" className="btn btn-danger btn-xs" onClick={() => revoke(client)}>
                  {t("mcpserver.Revoke")}
                </button>
              </div>
            </div>
          ))
        )}
      </div>

      <div className="panel">
        <h4 className="m-0 mb-xs">{t("mcpserver.Approvals")}</h4>
        <p className="field-hint mb-md">{t("mcpserver.ApprovalsHint")}</p>
        {approvals.length === 0 ? (
          <p className="muted m-0">{t("mcpserver.NoApprovals")}</p>
        ) : (
          approvals.map((approval) => (
            <div className="client-row" key={approval.id}>
              <div className="client-meta">
                <div className="text-sm">
                  {approval.client_name} · <code>{approval.tool}</code>
                </div>
                <div className="field-hint m-0">
                  <code>{approval.arguments}</code>
                </div>
              </div>
              <div className="row-actions">
                <button type="button" className="btn btn-primary btn-xs" onClick={() => decide(approval, "approve")}>
                  {t("mcpserver.Approve")}
                </button>
                <button type="button" className="btn btn-danger btn-xs" onClick={() => decide(approval, "deny")}>
                  {t("mcpserver.Deny")}
                </button>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
