import { useEffect, useMemo, useState } from "react";
import { apiFetch } from "./api.js";
import { Icon } from "./icons.jsx";
import { PluginPage } from "./PluginPage.jsx";
import { GeneralPage } from "./pages/GeneralPage.jsx";
import { SessionsPage } from "./pages/SessionsPage.jsx";
import { PluginsPage } from "./pages/PluginsPage.jsx";
import { LogsPage } from "./pages/LogsPage.jsx";

export function App() {
  const [status, setStatus] = useState(null);
  const [error, setError] = useState("");
  const [active, setActive] = useState("general");

  async function refreshStatus() {
    try {
      const data = await apiFetch("/api/core/status");
      setStatus(data);
      setError("");
    } catch (err) {
      setError(String(err.message || err));
    }
  }

  useEffect(() => {
    refreshStatus();
  }, []);

  const navItems = useMemo(() => {
    const items = [
      { id: "general", label: "General", icon: "general", order: 0 },
      { id: "sessions", label: "Sessions", icon: "sessions", order: 1000 },
      { id: "plugins", label: "Plugins", icon: "plugins", order: 1010 },
      { id: "logs", label: "Logs", icon: "logs", order: 1020 }
    ];
    for (const ui of status?.ui || []) {
      items.push({
        id: "plugin:" + ui.plugin_id,
        label: ui.title || ui.plugin_id,
        icon: ui.icon || "plugins",
        order: ui.order == null ? 500 : Number(ui.order),
        ui
      });
    }
    return items.sort((a, b) => {
      if (a.order !== b.order) return a.order - b.order;
      return a.label.localeCompare(b.label);
    });
  }, [status]);

  useEffect(() => {
    if (!navItems.some((item) => item.id === active)) {
      setActive(navItems[0]?.id || "general");
    }
  }, [navItems, active]);

  if (error) {
    return (
      <div className="app-shell">
        <main className="main-wrap">
          <div className="page">
            <div className="panel error-panel">
              <h4 className="m-0 mb-xs">cdx is not available</h4>
              <p className="muted m-0">{error}</p>
            </div>
          </div>
        </main>
      </div>
    );
  }

  if (!status) {
    return (
      <div className="app-shell">
        <main className="main-wrap">
          <div className="page">
            <p className="muted">Loading cdx...</p>
          </div>
        </main>
      </div>
    );
  }

  const current = navItems.find((item) => item.id === active);

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <svg width="22" height="22" viewBox="0 0 64 64" role="img" aria-label="cdx" style={{ flex: "none" }}>
            <rect width="64" height="64" rx="14" fill="var(--raised)" />
            <path
              d="M14 24a4 4 0 0 1 4-4h9l4 5h15a4 4 0 0 1 4 4v17a4 4 0 0 1-4 4H18a4 4 0 0 1-4-4Z"
              fill="none"
              stroke="var(--accent)"
              strokeWidth="4.5"
              strokeLinejoin="round"
            />
            <path
              d="M28 30.5 L35 37 L28 43.5"
              fill="none"
              stroke="var(--text)"
              strokeWidth="4.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
          <h4>cdx</h4>
        </div>
        <nav>
          {navItems.map((item) => (
            <button
              key={item.id}
              type="button"
              className={"nav-item" + (item.id === active ? " active" : "")}
              onClick={() => setActive(item.id)}
            >
              <Icon name={item.icon} />
              <span>{item.label}</span>
            </button>
          ))}
        </nav>
      </aside>
      <main className="main-wrap">
        <div className="page">
          {active === "general" ? <GeneralPage status={status} onRefresh={refreshStatus} /> : null}
          {active === "sessions" ? <SessionsPage /> : null}
          {active === "plugins" ? <PluginsPage /> : null}
          {active === "logs" ? <LogsPage /> : null}
          {current?.ui ? <PluginPage key={current.id} descriptor={current.ui} /> : null}
        </div>
      </main>
    </div>
  );
}
