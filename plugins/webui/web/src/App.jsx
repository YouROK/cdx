import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { ApiError, apiFetch } from "./api.js";
import { activeSection, navigate, parseRoute } from "./route.js";
import { Icon } from "./icons.jsx";
import { PluginPage } from "./PluginPage.jsx";
import { GeneralPage } from "./pages/GeneralPage.jsx";
import { LiveSessionPage } from "./pages/LiveSessionPage.jsx";
import { SessionsPage } from "./pages/SessionsPage.jsx";
import { PluginsPage } from "./pages/PluginsPage.jsx";
import { LogsPage } from "./pages/LogsPage.jsx";

// Built-in service pages start at this order value; plugin panels with a
// lower order belong to the first group of the menu.
const SERVICE_ORDER = 1000;

export function App() {
  const [status, setStatus] = useState(null);
  const [error, setError] = useState(null);
  const [route, setRoute] = useState(() => parseRoute(window.location.hash));

  const refreshStatus = useCallback(async () => {
    try {
      const data = await apiFetch("/api/core/status");
      setStatus(data);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    }
  }, []);

  useEffect(() => {
    refreshStatus();
  }, [refreshStatus]);

  useEffect(() => {
    function onHashChange() {
      setRoute(parseRoute(window.location.hash));
    }
    if (!window.location.hash) {
      window.history.replaceState(null, "", "#/");
    }
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const navItems = useMemo(() => {
    const items = [
      { id: "general", label: "General", icon: "general", order: 0, path: "/" },
      { id: "sessions", label: "Sessions", icon: "sessions", order: 1000, path: "/sessions" },
      { id: "plugins", label: "Plugins", icon: "plugins", order: 1010, path: "/plugins" },
      { id: "logs", label: "Logs", icon: "logs", order: 1020, path: "/logs" }
    ];
    for (const ui of status?.ui || []) {
      items.push({
        id: "plugin:" + ui.plugin_id,
        label: ui.title || ui.plugin_id,
        icon: ui.icon || "plugins",
        order: ui.order == null ? 500 : Number(ui.order),
        path: "/plugin/" + ui.plugin_id,
        ui
      });
    }
    return items.sort((a, b) => {
      if (a.order !== b.order) return a.order - b.order;
      return a.label.localeCompare(b.label);
    });
  }, [status]);

  const pluginIDs = useMemo(() => (status?.ui || []).map((ui) => ui.plugin_id), [status]);
  const section = activeSection(route, pluginIDs);
  const current = navItems.find((item) => item.id === section);

  if (error) {
    return <StartupError error={error} onRetry={refreshStatus} />;
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
          {navItems.map((item, index) => {
            const previous = navItems[index - 1];
            const gap = previous && previous.order < SERVICE_ORDER && item.order >= SERVICE_ORDER;
            return (
              <Fragment key={item.id}>
                {gap ? <div className="nav-gap" /> : null}
                <a
                  href={"#" + item.path}
                  className={"nav-item" + (item.id === section ? " active" : "")}
                  aria-current={item.id === section ? "page" : undefined}
                >
                  <Icon name={item.icon} />
                  <span>{item.label}</span>
                </a>
              </Fragment>
            );
          })}
        </nav>
      </aside>
      <main className="main-wrap">
        <div className="page">
          {section === "general" ? <GeneralPage status={status} /> : null}
          {section === "sessions" ? (
            route.parts[2] === "live" && route.parts[1] ? (
              <LiveSessionPage
                key={route.parts[1]}
                sessionId={route.parts[1]}
                onOpenHistory={() => navigate("/sessions/" + encodeURIComponent(route.parts[1]))}
                onBack={() => navigate("/sessions")}
              />
            ) : (
              <SessionsPage
                sessionId={route.parts[1] || ""}
                onOpen={(id) => navigate("/sessions/" + encodeURIComponent(id))}
                onLive={(id) => navigate("/sessions/" + encodeURIComponent(id) + "/live")}
                onBack={() => navigate("/sessions")}
              />
            )
          ) : null}
          {section === "plugins" ? <PluginsPage onChanged={refreshStatus} /> : null}
          {section === "logs" ? <LogsPage /> : null}
          {current?.ui ? <PluginPage key={current.id} descriptor={current.ui} /> : null}
        </div>
      </main>
    </div>
  );
}

function StartupError({ error, onRetry }) {
  const unauthorized = error instanceof ApiError && error.status === 401;
  return (
    <div className="app-shell">
      <main className="main-wrap">
        <div className="page">
          <div className="panel error-panel">
            <h4 className="m-0 mb-xs">{unauthorized ? "Access denied" : "cdx is not available"}</h4>
            <p className="muted m-0">{error.message}</p>
            {unauthorized ? (
              <p className="muted mt-2">
                The page does not carry a valid API token. Open the interface again from the address
                shown by <span className="mono">cdx daemon status</span>.
              </p>
            ) : null}
            <div className="row-gap-xs mt-3">
              <button type="button" className="btn btn-primary" onClick={() => window.location.reload()}>
                Reload page
              </button>
              <button type="button" className="btn btn-default" onClick={onRetry}>
                Retry
              </button>
            </div>
          </div>
        </div>
      </main>
    </div>
  );
}
