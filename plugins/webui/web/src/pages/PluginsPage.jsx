import { useEffect, useState } from "react";
import { apiFetch, postJSON } from "../api.js";
import { useUI } from "../ui.jsx";

export function PluginsPage() {
  const { toast, confirm } = useUI();
  const [plugins, setPlugins] = useState([]);
  const [url, setUrl] = useState("");
  const [installing, setInstalling] = useState(false);
  const [details, setDetails] = useState(null);

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
      toast("Installed " + data.plugin.name + " v" + data.plugin.version);
      setUrl("");
      await loadPlugins();
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
      toast(plugin.id + (plugin.enabled ? " disabled" : " enabled"));
      await loadPlugins();
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  async function remove(plugin) {
    const ok = await confirm("Remove plugin", "Remove installed plugin '" + plugin.id + "' from disk?");
    if (!ok) return;
    try {
      await apiFetch("/api/core/plugins/" + encodeURIComponent(plugin.id), { method: "DELETE" });
      toast("Plugin " + plugin.id + " removed");
      await loadPlugins();
    } catch (err) {
      toast(String(err.message || err), false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <h3>Plugins</h3>
        <button type="button" className="btn btn-default btn-xs" onClick={loadPlugins}>
          Refresh
        </button>
      </div>

      <div className="panel mb-md">
        <h4 className="m-0 mb-xs">Install plugin</h4>
        <p className="field-hint mb-md">
          Provide a direct link to a plugin .zip archive. Before installation, cdx verifies the archive layout,
          manifest.json schema, version compatibility, and JS syntax.
        </p>
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
            {installing ? "Verifying & Installing..." : "Install from ZIP"}
          </button>
        </form>
      </div>

      <div className="panel panel-flush">
        <table className="table">
          <thead>
            <tr>
              <th>Plugin</th>
              <th>Version</th>
              <th>Scope</th>
              <th>Status</th>
              <th>Tools</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {plugins.map((plugin) => (
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
                    {plugin.scope}
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
                    {plugin.status === "error" ? "error" : plugin.enabled ? "enabled" : "disabled"}
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
                    Details
                  </button>{" "}
                  <button type="button" className="btn btn-light btn-xs" onClick={() => toggle(plugin)}>
                    {plugin.enabled ? "Disable" : "Enable"}
                  </button>
                  {plugin.removable ? (
                    <>
                      {" "}
                      <button type="button" className="btn btn-danger btn-xs" onClick={() => remove(plugin)}>
                        Remove
                      </button>
                    </>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {plugins.length === 0 ? <p className="muted panel-pad">No plugins found.</p> : null}
      </div>

      {details ? <PluginDetails plugin={details} onClose={() => setDetails(null)} /> : null}
    </div>
  );
}

function PluginDetails({ plugin, onClose }) {
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
              <span className="muted">ID:</span> <span className="mono">{plugin.id}</span>
            </div>
            <div>
              <span className="muted">Scope:</span> <span className="mono">{plugin.scope}</span>
            </div>
            <div>
              <span className="muted">Entry:</span> <span className="mono">{plugin.entry}</span>
            </div>
            <div>
              <span className="muted">Min cdx:</span> <span className="mono">{plugin.min_cdx_version || "any"}</span>
            </div>
          </div>
          <div className="text-xs">
            <span className="muted">Directory:</span> <span className="mono">{plugin.dir}</span>
          </div>
          {plugin.source_url ? (
            <div className="text-xs">
              <span className="muted">Source ZIP:</span> <span className="mono">{plugin.source_url}</span>
            </div>
          ) : null}
          <div className="section-caption mt-md">Tools</div>
          {tools.length === 0 ? (
            <p className="muted text-sm">This plugin does not register LLM tools.</p>
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
