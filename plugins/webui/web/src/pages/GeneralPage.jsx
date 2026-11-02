import { useEffect, useState } from "react";
import { apiFetch, postJSON } from "../api.js";
import { useUI } from "../ui.jsx";

export function GeneralPage({ status, onRefresh }) {
  const { toast } = useUI();
  const [agent, setAgent] = useState({ max_steps: 40, ask_on_max_steps: true });
  const [sessions, setSessions] = useState({ temp_ttl_hours: 24 });

  useEffect(() => {
    apiFetch("/api/core/agent").then(setAgent).catch(() => {});
    apiFetch("/api/core/sessions").then(setSessions).catch(() => {});
  }, []);

  async function saveAgent(event) {
    event.preventDefault();
    try {
      const data = await postJSON("/api/core/agent", {
        max_steps: Number(agent.max_steps),
        ask_on_max_steps: Boolean(agent.ask_on_max_steps)
      });
      setAgent(data);
      toast("Agent limits saved");
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  async function saveTTL(event) {
    event.preventDefault();
    try {
      const data = await postJSON("/api/core/sessions/ttl", {
        hours: Number(sessions.temp_ttl_hours)
      });
      setSessions((current) => ({ ...current, ...data }));
      toast("Session settings saved");
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <h3>General</h3>
        <button type="button" className="btn btn-default btn-xs" onClick={onRefresh}>
          Refresh
        </button>
      </div>
      <div className="stat-grid">
        <div className="stat-card">
          <div className="label">Version</div>
          <div className="value">{status.version || "-"}</div>
        </div>
        <div className="stat-card">
          <div className="label">Protocol</div>
          <div className="value">v{status.protocol || 1}</div>
        </div>
        <div className="stat-card">
          <div className="label">PID</div>
          <div className="value">{status.pid || "-"}</div>
        </div>
        <div className="stat-card">
          <div className="label">Loaded plugins</div>
          <div className="value">{(status.plugins || []).length}</div>
        </div>
      </div>

      <div className="stack-md">
        <div className="panel">
          <h4 className="m-0 mb-md">Agent limits</h4>
          <form className="form-stack" onSubmit={saveAgent}>
            <div className="grid-2">
              <label>
                Maximum tool steps
                <span className="field-hint">Model requests per turn before stopping</span>
                <input
                  type="number"
                  min="1"
                  max="200"
                  required
                  value={agent.max_steps || 40}
                  onChange={(event) => setAgent((current) => ({ ...current, max_steps: event.target.value }))}
                />
              </label>
              <label className="switch-card">
                <input
                  type="checkbox"
                  checked={agent.ask_on_max_steps !== false}
                  onChange={(event) => setAgent((current) => ({ ...current, ask_on_max_steps: event.target.checked }))}
                />
                <div>
                  <div>Ask before continuing</div>
                  <div className="field-hint">When the step limit is reached, ask whether to give another budget.</div>
                </div>
              </label>
            </div>
            <div>
              <button type="submit" className="btn btn-primary">
                Save agent limits
              </button>
            </div>
          </form>
        </div>

        <div className="panel">
          <h4 className="m-0 mb-md">Temporary sessions</h4>
          <form className="form-stack" onSubmit={saveTTL}>
            <div className="grid-2">
              <label>
                Lifetime (hours)
                <span className="field-hint">Default directory session expires after inactivity</span>
                <input
                  type="number"
                  min="1"
                  value={sessions.temp_ttl_hours || 24}
                  onChange={(event) => setSessions((current) => ({ ...current, temp_ttl_hours: event.target.value }))}
                />
              </label>
            </div>
            <div>
              <button type="submit" className="btn btn-primary">
                Save session settings
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
