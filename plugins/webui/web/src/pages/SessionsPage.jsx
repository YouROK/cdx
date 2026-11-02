import { useEffect, useState } from "react";
import { apiFetch, postJSON } from "../api.js";
import { useUI } from "../ui.jsx";

export function SessionsPage() {
  const { toast, confirm } = useUI();
  const [data, setData] = useState({ sessions: [], temp_ttl_hours: 24 });
  const [selected, setSelected] = useState(null);
  const [loading, setLoading] = useState(true);

  async function loadSessions() {
    setLoading(true);
    try {
      const next = await apiFetch("/api/core/sessions");
      setData(next);
      setSelected(null);
    } catch (err) {
      toast(String(err.message || err), false);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    loadSessions();
  }, []);

  async function openSession(id) {
    try {
      const detail = await apiFetch("/api/core/sessions/" + encodeURIComponent(id));
      setSelected(detail);
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  async function toggleMode(session) {
    const kind = session.kind === "persistent" ? "temp" : "persistent";
    try {
      await postJSON("/api/core/sessions/" + encodeURIComponent(session.id) + "/mode", { kind });
      toast("Session is now " + kind);
      await loadSessions();
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  async function removeSession(session) {
    const ok = await confirm("Delete session", "Delete session '" + session.id + "' and its message history?");
    if (!ok) return;
    try {
      await apiFetch("/api/core/sessions/" + encodeURIComponent(session.id), { method: "DELETE" });
      toast("Session deleted");
      await loadSessions();
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  if (selected) {
    return <SessionDetail detail={selected} onBack={loadSessions} />;
  }

  const list = data.sessions || [];
  return (
    <div>
      <div className="page-header">
        <h3>Sessions</h3>
        <button type="button" className="btn btn-default btn-xs" onClick={loadSessions}>
          Refresh
        </button>
      </div>
      <p className="page-intro">
        Sessions are bound to a directory and inherited by subdirectories. Temporary sessions expire after{" "}
        {data.temp_ttl_hours || 24} hours of inactivity; persistent sessions never expire.
      </p>
      <div className="panel panel-flush">
        {loading ? (
          <p className="muted panel-pad">Loading sessions...</p>
        ) : list.length === 0 ? (
          <p className="muted panel-pad">No active sessions.</p>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>Session</th>
                <th>Directory</th>
                <th>Messages</th>
                <th>Updated</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.map((session) => (
                <tr key={session.id}>
                  <td>
                    <div className="row-gap-xs">
                      <button type="button" className="link-button mono text-sm" onClick={() => openSession(session.id)}>
                        {session.id}
                      </button>
                      <span className={session.kind === "persistent" ? "badge badge-teal" : "badge badge-accent"}>
                        {session.kind === "persistent" ? "persistent" : "temp (" + (data.temp_ttl_hours || 24) + "h)"}
                      </span>
                    </div>
                  </td>
                  <td className="mono text-xs muted">{session.workdir}</td>
                  <td>{session.message_count}</td>
                  <td className="text-xs muted">{new Date((session.updated_at || 0) * 1000).toLocaleString()}</td>
                  <td className="sess-actions">
                    <div className="row-actions">
                      <button type="button" className="btn btn-light btn-xs" onClick={() => toggleMode(session)}>
                        {session.kind === "persistent" ? "Make temporary" : "Make persistent"}
                      </button>
                      <button type="button" className="btn btn-danger btn-xs" onClick={() => removeSession(session)}>
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function SessionDetail({ detail, onBack }) {
  const session = detail.session || {};
  const messages = detail.messages || [];
  let totalIn = session.tokens_in || 0;
  let totalOut = session.tokens_out || 0;
  if (totalIn === 0 && totalOut === 0) {
    for (const message of messages) {
      totalIn += message.tokens_in || 0;
      totalOut += message.tokens_out || 0;
    }
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h3>
            Session: <span className="mono">{session.id}</span>
          </h3>
          <div className="mono text-xs muted">{session.workdir || ""}</div>
          {session.stopped_reason ? (
            <div className="badge badge-warn mt-2">stopped: {session.stopped_reason}</div>
          ) : null}
        </div>
        <button type="button" className="btn btn-default btn-xs" onClick={onBack}>
          &larr; Back to sessions
        </button>
      </div>
      <div className="stat-grid">
        <div className="stat-card">
          <div className="label">Messages</div>
          <div className="value">{messages.length}</div>
        </div>
        <div className="stat-card">
          <div className="label">Input tokens (est.)</div>
          <div className="value">{totalIn}</div>
        </div>
        <div className="stat-card">
          <div className="label">Output tokens (est.)</div>
          <div className="value">{totalOut}</div>
        </div>
        <div className="stat-card">
          <div className="label">Total tokens</div>
          <div className="value">{totalIn + totalOut}</div>
        </div>
      </div>
      <div className="stack-xs">
        {messages.map((message, index) => (
          <MessageRow key={index} message={message} session={session} />
        ))}
      </div>
    </div>
  );
}

function MessageRow({ message, session }) {
  const role = message.role;
  const toolCalls = message.tool_calls || [];
  const content = String(message.content || "");
  const reasoning = String(message.reasoning || "");
  if (!content.trim() && !reasoning.trim() && toolCalls.length === 0) return null;

  let roleLabel = role;
  if (role === "assistant") {
    let model = message.model || session.model || "";
    if (message.provider && model && model.indexOf("/") === -1) model = message.provider + "/" + model;
    if (model) roleLabel = "assistant (" + model + ")";
  } else if (role === "tool" && message.name) {
    roleLabel = "tool: " + message.name;
  }
  const badgeClass = role === "assistant" ? "badge-teal" : role === "user" ? "badge-accent" : "badge-warn";

  return (
    <div className="panel">
      <div className="row-between mb-xs">
        <span className={"badge " + badgeClass}>{roleLabel}</span>
        {message.tokens_in || message.tokens_out ? (
          <span className="text-xs muted">
            tokens: {message.tokens_in || 0}/{message.tokens_out || 0}
          </span>
        ) : null}
      </div>
      {content.trim() ? <pre className="code-block">{content}</pre> : null}
      {reasoning.trim() ? (
        <details className="reasoning-block" open={!content.trim()}>
          <summary>Reasoning</summary>
          <pre className="code-block">{reasoning}</pre>
        </details>
      ) : null}
      {role === "assistant" && toolCalls.length > 0 ? (
        <div className="tool-calls">
          {toolCalls.map((call, index) => {
            let args = "";
            try {
              args = JSON.stringify(call.arguments || {});
            } catch {
              args = "";
            }
            if (args.length > 240) args = args.slice(0, 240) + "...";
            return (
              <div key={index} className="mono text-xs">
                {call.name} {args}
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
