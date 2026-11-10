import { useEffect, useState } from "react";
import { apiFetch, postJSON } from "../api.js";
import { useUI } from "../ui.jsx";
import { Hint } from "../hint.jsx";

export function GeneralPage({ status }) {
  const { toast, t } = useUI();
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
      toast(t("webui.General.AgentSaved"));
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
      toast(t("webui.General.SessionsSaved"));
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <h3>{t("webui.Nav.General")}</h3>
      </div>
      <div className="stat-grid">
        <div className="stat-card">
          <div className="label">{t("webui.General.Version")}</div>
          <div className="value">{status.version || "-"}</div>
        </div>
        <div className="stat-card">
          <div className="label">{t("webui.General.Protocol")}</div>
          <div className="value">v{status.protocol || 1}</div>
        </div>
        <div className="stat-card">
          <div className="label">{t("webui.General.PID")}</div>
          <div className="value">{status.pid || "-"}</div>
        </div>
        <div className="stat-card">
          <div className="label">{t("webui.General.LoadedPlugins")}</div>
          <div className="value">{(status.plugins || []).length}</div>
        </div>
      </div>

      {loadError ? (
        <div className="panel error-panel">
          <h4 className="m-0 mb-xs">{t("webui.Schema.Unavailable")}</h4>
          <p className="muted m-0">{loadError}</p>
          <p className="muted mt-2">{t("webui.Schema.DisabledHint")}</p>
          <button type="button" className="btn btn-default" onClick={loadSettings}>
            Retry
          </button>
        </div>
      ) : loading ? (
        <p className="muted">{t("webui.Schema.Loading")}</p>
      ) : (
        <div className="stack-md">
          <div className="panel">
            <h4 className="m-0 mb-md">{t("webui.General.AgentLimits")}</h4>
            <form className="form-stack" onSubmit={saveAgent}>
              <div className="grid-2">
                <label>
                  <span className="field-title">
                    {t("webui.General.MaxSteps")}
                    <Hint
                      text={t("webui.General.MaxStepsHint")}
                      label={t("webui.General.MaxStepsAbout")}
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
                  <span className="field-hint">{t("webui.General.MaxStepsFieldHint")}</span>
                </label>
                <label>
                  <span className="field-title">
                    {t("webui.General.StepLimit")}
                    <Hint
                      text={t("webui.General.StepLimitHint")}
                      label={t("webui.General.StepLimitAbout")}
                    />
                  </span>
                  <select
                    value={agent.step_limit_mode || "ask"}
                    onChange={(event) =>
                      setAgent((current) => ({ ...current, step_limit_mode: event.target.value }))
                    }
                  >
                    <option value="none">{t("webui.General.NoStepLimit")}</option>
                    <option value="ask">{t("webui.General.AskToContinue")}</option>
                    <option value="stop">{t("webui.General.StopTurn")}</option>
                  </select>
                  <span className="field-hint">{t("webui.General.StepLimitFieldHint")}</span>
                </label>
              </div>
              <div>
                <button type="submit" className="btn btn-primary">
                  {t("webui.General.SaveAgent")}
                </button>
              </div>
            </form>
          </div>

          <div className="panel">
            <h4 className="m-0 mb-md">{t("webui.General.TemporarySessions")}</h4>
            <form className="form-stack" onSubmit={saveTTL}>
              <div className="grid-2">
                <label>
                  <span className="field-title">
                    {t("webui.General.Lifetime")}
                    <Hint
                      text={t("webui.General.LifetimeHint")}
                      label={t("webui.General.LifetimeAbout")}
                    />
                  </span>
                  <input
                    type="number"
                    min="1"
                    value={sessions.temp_ttl_hours || 24}
                    onChange={(event) => setSessions((current) => ({ ...current, temp_ttl_hours: event.target.value }))}
                  />
                  <span className="field-hint">{t("webui.General.LifetimeFieldHint")}</span>
                </label>
              </div>
              <div>
                <button type="submit" className="btn btn-primary">
                  {t("webui.General.SaveSessions")}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
