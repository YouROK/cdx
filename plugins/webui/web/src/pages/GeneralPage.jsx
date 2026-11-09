import { useEffect, useState } from "react";
import { apiFetch, postJSON } from "../api.js";
import { useUI } from "../ui.jsx";
import { Hint } from "../hint.jsx";

export function GeneralPage({ status }) {
  const { toast } = useUI();
  const [agent, setAgent] = useState({ max_steps: 40, step_limit_mode: "ask" });
  const [sessions, setSessions] = useState({ temp_ttl_hours: 24 });
  const [loadError, setLoadError] = useState("");
  const [loading, setLoading] = useState(true);

  async function loadSettings() {
    setLoading(true);
    try {
      const [agentData, sessionData] = await Promise.all([
        apiFetch("/api/core/agent"),
        apiFetch("/api/core/sessions")
      ]);
      setAgent(agentData);
      setSessions(sessionData);
      setLoadError("");
    } catch (err) {
      setLoadError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    loadSettings();
  }, []);

  async function saveAgent(event) {
    event.preventDefault();
    try {
      const data = await postJSON("/api/core/agent", {
        max_steps: Number(agent.max_steps),
        step_limit_mode: agent.step_limit_mode || "ask"
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

      {loadError ? (
        <div className="panel error-panel">
          <h4 className="m-0 mb-xs">Settings are not available</h4>
          <p className="muted m-0">{loadError}</p>
          <p className="muted mt-2">
            Saving is disabled until the current values are loaded, so defaults do not overwrite them.
          </p>
          <button type="button" className="btn btn-default" onClick={loadSettings}>
            Retry
          </button>
        </div>
      ) : loading ? (
        <p className="muted">Loading settings...</p>
      ) : (
        <div className="stack-md">
          <div className="panel">
            <h4 className="m-0 mb-md">Agent limits</h4>
            <form className="form-stack" onSubmit={saveAgent}>
              <div className="grid-2">
                <label>
                  <span className="field-title">
                    Maximum tool steps
                    <Hint
                      text="How many model requests one turn may make. Used when the step limit mode is not no limit."
                      label="About maximum tool steps"
                    />
                  </span>
                  <input
                    type="number"
                    min="1"
                    max="200"
                    required
                    disabled={agent.step_limit_mode === "none"}
                    value={agent.max_steps ?? 40}
                    onChange={(event) => setAgent((current) => ({ ...current, max_steps: event.target.value }))}
                  />
                  <span className="field-hint">Model requests per turn, 1 to 200</span>
                </label>
                <label>
                  <span className="field-title">
                    When the limit is reached
                    <Hint
                      text="No step limit runs the turn until the model answers. Ask to continue offers another equal budget. Stop the turn ends it with the answer collected so far."
                      label="About the step limit"
                    />
                  </span>
                  <select
                    value={agent.step_limit_mode || "ask"}
                    onChange={(event) =>
                      setAgent((current) => ({ ...current, step_limit_mode: event.target.value }))
                    }
                  >
                    <option value="none">No step limit</option>
                    <option value="ask">Ask to continue</option>
                    <option value="stop">Stop the turn</option>
                  </select>
                  <span className="field-hint">What happens when the model exhausts the step budget</span>
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
                  <span className="field-title">
                    Lifetime (hours)
                    <Hint
                      text="A temporary session expires after this many hours without activity. Persistent sessions never expire."
                      label="About session lifetime"
                    />
                  </span>
                  <input
                    type="number"
                    min="1"
                    value={sessions.temp_ttl_hours || 24}
                    onChange={(event) => setSessions((current) => ({ ...current, temp_ttl_hours: event.target.value }))}
                  />
                  <span className="field-hint">Default directory session expires after inactivity</span>
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
      )}
    </div>
  );
}
