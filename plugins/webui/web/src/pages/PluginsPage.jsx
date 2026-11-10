import { useEffect, useMemo, useState } from "react";
import { apiFetch, postJSON } from "../api.js";
import { useUI } from "../ui.jsx";

export function PluginsPage({ onChanged }) {
  const { toast, confirm, t } = useUI();
  const [plugins, setPlugins] = useState([]);
  const [url, setUrl] = useState("");
  const [installing, setInstalling] = useState(false);
  const [details, setDetails] = useState(null);

  // External and user plugins first, built-ins next, zero-priority plugins last.
  const sortedPlugins = useMemo(() => {
    return [...plugins].sort((a, b) => {
      const byOrder = (Number(b.order) || 0) - (Number(a.order) || 0);
      if (byOrder !== 0) return byOrder;
      const byName = String(a.name || a.id).localeCompare(String(b.name || b.id));
      if (byName !== 0) return byName;
      return String(a.id).localeCompare(String(b.id));
    });
  }, [plugins]);

  async function loadPlugins() {
    try {
      const data = await apiFetch("/api/core/plugins");
      setPlugins(data.plugins || []);
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  useEffect(() => {
    loadPlugins();
  }, []);

  async function install(event) {
    event.preventDefault();
    const value = url.trim();
    if (!value) return;
    setInstalling(true);
    try {
      const data = await postJSON("/api/core/plugins/install", { url: value });
      toast(t("webui.Plugins.Installed", { name: data.plugin.name, version: data.plugin.version }));
      setUrl("");
      await loadPlugins();
      onChanged?.();
    } catch (err) {
      toast(String(err.message || err), false);
    } finally {
      setInstalling(false);
    }
  }

  async function toggle(plugin) {
    try {
      await postJSON(
        "/api/core/plugins/" + encodeURIComponent(plugin.id) + "/enable",
        { enabled: !plugin.enabled }
      );
      toast(t(plugin.enabled ? "webui.Plugins.Disabled" : "webui.Plugins.Enabled", { id: plugin.id }));
      await loadPlugins();
      onChanged?.();
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  async function remove(plugin) {
    const ok = await confirm(t("webui.Plugins.RemoveTitle"), t("webui.Plugins.RemoveConfirm", { id: plugin.id }));
    if (!ok) return;
    try {
      await apiFetch("/api/core/plugins/" + encodeURIComponent(plugin.id), { method: "DELETE" });
      toast(t("webui.Plugins.Removed", { id: plugin.id }));
      await loadPlugins();
      onChanged?.();
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <h3>{t("webui.Nav.Plugins")}</h3>
      </div>

      <div className="panel mb-md">
        <h4 className="m-0 mb-xs">{t("webui.Plugins.InstallTitle")}</h4>
        <p className="field-hint mb-md">{t("webui.Plugins.InstallHint")}</p>
        <form className="row-gap-xs" style={{ alignItems: "stretch" }} onSubmit={install}>
          <input
            type="text"
            placeholder="https://example.com/my-plugin-v1.0.0.zip"
            required
            style={{ flex: 1 }}
            value={url}
            onChange={(event) => setUrl(event.target.value)}
          />
          <button type="submit" className="btn btn-primary" disabled={installing}>
            {installing ? t("webui.Plugins.Installing") : t("webui.Plugins.InstallButton")}
          </button>
        </form>
      </div>

      <div className="panel panel-flush">
        <table className="table">
          <thead>
            <tr>
              <th>{t("webui.Plugins.TablePlugin")}</th>
              <th>{t("webui.Plugins.TableVersion")}</th>
              <th>{t("webui.Plugins.TableScope")}</th>
              <th>{t("webui.Plugins.TableStatus")}</th>
              <th>{t("webui.Plugins.TableTools")}</th>
              <th style={{ textAlign: "right" }}>{t("webui.Plugins.TableActions")}</th>
            </tr>
          </thead>
          <tbody>
            {sortedPlugins.map((plugin) => (
              <tr key={plugin.id}>
                <td>
                  <div className="fw-600">
                    {plugin.name} <span className="mono text-xs muted">({plugin.id})</span>
                  </div>
                  <div className="text-xs muted">{plugin.description || ""}</div>
                  {plugin.last_error ? <div className="text-xs error-text mt-2">{plugin.last_error}</div> : null}
                </td>
                <td style={{ whiteSpace: "nowrap" }}>
                  <span className="mono text-xs">v{plugin.version}</span>
                </td>
                <td style={{ whiteSpace: "nowrap" }}>
                  <span className={plugin.scope === "user" ? "badge badge-accent" : "badge badge-outline"}>
                    {plugin.scope === "user" ? t("webui.Plugins.ScopeUser") : t("webui.Plugins.ScopeSystem")}
                  </span>
                </td>
                <td style={{ whiteSpace: "nowrap" }}>
                  <span
                    className={
                      plugin.status === "error"
                        ? "badge badge-danger"
                        : plugin.enabled
                          ? "badge badge-teal"
                          : "badge badge-warn"
                    }
                  >
                    {plugin.status === "error"
                      ? t("webui.Plugins.StatusError")
                      : plugin.enabled
                        ? t("webui.Plugins.StatusEnabled")
                        : t("webui.Plugins.StatusDisabled")}
                  </span>
                </td>
                <td style={{ whiteSpace: "nowrap" }}>
                  {(plugin.tools || []).length > 0 ? (
                    <span className="badge badge-outline">{(plugin.tools || []).length}</span>
                  ) : (
                    <span className="muted">-</span>
                  )}
                </td>
                <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                  <button type="button" className="btn btn-default btn-xs" onClick={() => setDetails(plugin)}>
                    {t("webui.Plugins.Details")}
                  </button>{" "}
                  <button type="button" className="btn btn-light btn-xs" onClick={() => toggle(plugin)}>
                    {plugin.enabled ? t("webui.Plugins.Disable") : t("webui.Plugins.Enable")}
                  </button>
                  {plugin.removable ? (
                    <>
                      {" "}
                      <button type="button" className="btn btn-danger btn-xs" onClick={() => remove(plugin)}>
                        {t("webui.Plugins.Remove")}
                      </button>
                    </>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {plugins.length === 0 ? <p className="muted panel-pad">{t("webui.Plugins.Empty")}</p> : null}
      </div>

      {details ? <PluginDetails plugin={details} onClose={() => setDetails(null)} /> : null}
    </div>
  );
}

function PluginDetails({ plugin, onClose }) {
  const { t } = useUI();
  const tools = plugin.tools || [];
  return (
    <div
      className="modal-backdrop"
      onClick={(event) => {
        if (event.target.classList.contains("modal-backdrop")) onClose();
      }}
    >
      <div className="modal-box">
        <div className="modal-header">
          <h4>
            {plugin.name} <span className="mono text-xs muted">v{plugin.version}</span>
          </h4>
          <button type="button" className="btn-icon" onClick={onClose}>
            &times;
          </button>
        </div>
        <div className="form-stack">
          <div className="text-sm">{plugin.description || ""}</div>
          <div className="grid-2 text-xs">
            <div>
              <span className="muted">{t("webui.Plugins.Id")}:</span> <span className="mono">{plugin.id}</span>
            </div>
            <div>
              <span className="muted">{t("webui.Plugins.Scope")}:</span> <span className="mono">{plugin.scope === "user" ? t("webui.Plugins.ScopeUser") : t("webui.Plugins.ScopeSystem")}</span>
            </div>
            <div>
              <span className="muted">{t("webui.Plugins.Entry")}:</span> <span className="mono">{plugin.entry}</span>
            </div>
            <div>
              <span className="muted">{t("webui.Plugins.MinVersion")}:</span> <span className="mono">{plugin.min_cdx_version || t("webui.Plugins.AnyVersion")}</span>
            </div>
          </div>
          <div className="text-xs">
            <span className="muted">{t("webui.Plugins.Directory")}:</span> <span className="mono">{plugin.dir}</span>
          </div>
          {plugin.source_url ? (
            <div className="text-xs">
              <span className="muted">{t("webui.Plugins.SourceZip")}:</span> <span className="mono">{plugin.source_url}</span>
            </div>
          ) : null}
          <div className="section-caption mt-md">{t("webui.Plugins.TableTools")}</div>
          {tools.length === 0 ? (
            <p className="muted text-sm">{t("webui.Plugins.NoTools")}</p>
          ) : (
            <div className="stack-xs">
              {tools.map((tool) => (
                <div key={tool.name} className="model-row">
                  <div className="row-between">
                    <span className="mono fw-600">{tool.name}</span>
                    <span className="badge badge-outline">{tool.permission || "edit"}</span>
                  </div>
                  <div className="text-xs muted mt-2">{tool.description || ""}</div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
