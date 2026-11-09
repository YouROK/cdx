import { Fragment, useEffect, useState } from "react";
import { apiFetch, postJSON } from "../api.js";
import { MessageRow } from "../messages.jsx";
import { groupSessions } from "../sessionGroups.js";
import { useUI } from "../ui.jsx";

export function SessionsPage({ sessionId, onOpen, onLive, onBack }) {
  const { toast, confirm } = useUI();
  const [data, setData] = useState({ sessions: [], temp_ttl_hours: 24, live: [] });
  const [expanded, setExpanded] = useState({});
  const [detail, setDetail] = useState(null);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);

  async function loadSessions(quiet) {
    if (!quiet) setLoading(true);
    try {
      const next = await apiFetch("/api/core/sessions");
      setData(next);
      return next;
    } catch (err) {
      if (!quiet) toast(String(err.message || err), false);
      return null;
    } finally {
      if (!quiet) setLoading(false);
    }
  }

  useEffect(() => {
    loadSessions();
  }, []);

  // Keep the live markers fresh while the session list is open.
  useEffect(() => {
    if (sessionId) return;
    const timer = setInterval(() => loadSessions(true), 5000);
    return () => clearInterval(timer);
  }, [sessionId]);

  useEffect(() => {
    if (!sessionId) {
      setDetail(null);
      return;
    }
    let alive = true;
    setDetailLoading(true);
    apiFetch("/api/core/sessions/" + encodeURIComponent(sessionId))
      .then((next) => {
        if (alive) setDetail(next);
      })
      .catch((err) => {
        if (!alive) return;
        toast(String(err.message || err), false);
        onBack();
      })
      .finally(() => {
        if (alive) setDetailLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [sessionId]);

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

  function toggleExpanded(id) {
    setExpanded((current) => ({ ...current, [id]: !current[id] }));
  }

  async function removeSession(session) {
    const name = session.title || session.id;
    const ok = await confirm(
      "Delete session",
      "Delete '" + name + "' and its message history? Sub-sessions are deleted with it.",
    );
    if (!ok) return;
    try {
      await apiFetch("/api/core/sessions/" + encodeURIComponent(session.id), { method: "DELETE" });
      toast("Session deleted");
      await loadSessions();
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  if (sessionId) {
    if (!detail) {
      return (
        <div>
          <div className="page-header">
            <h3>
              Session: <span className="mono">{sessionId}</span>
            </h3>
            <button type="button" className="btn btn-default btn-xs" onClick={onBack}>
              &larr; Back to sessions
            </button>
          </div>
          <p className="muted">{detailLoading ? "Loading session..." : "Session was not found."}</p>
        </div>
      );
    }
    return (
      <SessionDetail
        detail={detail}
        onBack={onBack}
        onLive={() => onLive(sessionId)}
        onOpen={onOpen}
      />
    );
  }

  const list = data.sessions || [];
  const groups = groupSessions(list);
  const liveIDs = new Set(data.live || []);
  const ttl = data.temp_ttl_hours || 24;
  return (
    <div>
      <div className="page-header">
        <h3>Sessions</h3>
      </div>
      <p className="page-intro">
        Sessions are bound to a directory and inherited by subdirectories. Temporary sessions expire after{" "}
        {ttl} hours of inactivity; persistent sessions never expire. Subagent sessions live under their
        parent and are deleted with it.
      </p>
      <div className="panel panel-flush">
        {loading ? (
          <p className="muted panel-pad">Loading sessions...</p>
        ) : list.length === 0 ? (
          <p className="muted panel-pad">No active sessions.</p>
        ) : (
          <table className="table sessions-table">
            <colgroup>
              <col style={{ width: "30%" }} />
              <col style={{ width: "13%" }} />
              <col style={{ width: "6%" }} />
              <col style={{ width: "16%" }} />
              <col style={{ width: "35%" }} />
            </colgroup>
            <thead>
              <tr>
                <th>Session</th>
                <th>Directory</th>
                <th>Msg</th>
                <th>Updated</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {groups.map(({ session, children }) => (
                <Fragment key={session.id}>
                  <SessionRow
                    session={session}
                    ttl={ttl}
                    live={liveIDs.has(session.id)}
                    isChild={false}
                    childCount={children.length}
                    expanded={Boolean(expanded[session.id])}
                    onToggle={() => toggleExpanded(session.id)}
                    onOpen={onOpen}
                    onLive={onLive}
                    onToggleMode={toggleMode}
                    onDelete={removeSession}
                  />
                  {expanded[session.id]
                    ? children.map((child) => (
                        <SessionRow
                          key={child.id}
                          session={child}
                          ttl={ttl}
                          live={liveIDs.has(child.id)}
                          isChild
                          childCount={0}
                          expanded={false}
                          onToggle={() => {}}
                          onOpen={onOpen}
                          onLive={onLive}
                          onToggleMode={toggleMode}
                          onDelete={removeSession}
                        />
                      ))
                    : null}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function kindLabel(session, ttl) {
  if (session.kind === "persistent") return "persistent";
  if (session.kind === "subagent") return "subagent";
  return "temp (" + ttl + "h)";
}

function SessionRow({
  session,
  ttl,
  live,
  isChild,
  childCount,
  expanded,
  onToggle,
  onOpen,
  onLive,
  onToggleMode,
  onDelete,
}) {
  const title = session.title || "Untitled session";
  const canChangeMode = session.kind === "temp" || session.kind === "persistent";
  return (
    <tr className={isChild ? "session-child" : undefined}>
      <td>
        <div className="sess-title-cell">
          <div className="sess-title-lines">
            <div className="row-gap-xs">
              <button
                type="button"
                className="link-button text-sm session-title"
                title={title}
                onClick={() => onOpen(session.id)}
              >
                {title}
              </button>
              {live ? (
                <span className="badge badge-teal live-badge">
                  <span className="live-dot" />
                  live
                </span>
              ) : null}
            </div>
            <div className="mono text-xs muted sess-id">{session.id}</div>
          </div>
          <span className={session.kind === "persistent" ? "badge badge-teal" : "badge badge-accent"}>
            {kindLabel(session, ttl)}
          </span>
        </div>
      </td>
      <td className="mono text-xs muted sess-workdir" title={session.workdir}>{session.workdir}</td>
      <td>{session.message_count}</td>
      <td className="text-xs muted sess-updated">{new Date((session.updated_at || 0) * 1000).toLocaleString()}</td>
      <td className="sess-actions">
        <div className="row-actions">
          {childCount > 0 ? (
            <button type="button" className="btn btn-light btn-xs" onClick={onToggle}>
              {expanded ? "Hide sub-sessions" : "Sub-sessions (" + childCount + ")"}
            </button>
          ) : null}
          <button type="button" className="btn btn-light btn-xs" onClick={() => onLive(session.id)}>
            Live
          </button>
          {!isChild && canChangeMode ? (
            <button type="button" className="btn btn-light btn-xs" onClick={() => onToggleMode(session)}>
              {session.kind === "persistent" ? "Temp" : "Persist"}
            </button>
          ) : null}
          {!isChild ? (
            <button type="button" className="btn btn-danger btn-xs" onClick={() => onDelete(session)}>
              Delete
            </button>
          ) : null}
        </div>
      </td>
    </tr>
  );
}

function SessionDetail({ detail, onBack, onLive, onOpen }) {
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
          <h3>{session.title || "Session"}</h3>
          <div className="mono text-xs muted">
            {session.id}
            {session.workdir ? " \u00b7 " + session.workdir : ""}
          </div>
          {session.parent_id ? (
            <div className="text-xs muted">
              Sub-session of{" "}
              <button
                type="button"
                className="link-button mono text-xs"
                onClick={() => onOpen && onOpen(session.parent_id)}
              >
                {session.parent_id}
              </button>
            </div>
          ) : null}
          {session.stopped_reason ? (
            <div className="badge badge-warn mt-2">stopped: {session.stopped_reason}</div>
          ) : null}
        </div>
        <div className="row-gap-xs">
          <button type="button" className="btn btn-light btn-xs" onClick={onLive}>
            Live
          </button>
          <button type="button" className="btn btn-default btn-xs" onClick={onBack}>
            &larr; Back to sessions
          </button>
        </div>
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
