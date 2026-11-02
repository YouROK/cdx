import { Component, useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { apiFetch, postJSON } from "./api.js";
import { SchemaForm } from "./SchemaForm.jsx";
import { useUI } from "./ui.jsx";
import baseCss from "../style.css?inline";

class PluginErrorBoundary extends Component {
  constructor(props) {
    super(props);
    this.state = { error: "" };
  }

  static getDerivedStateFromError(error) {
    return { error: String(error?.message || error) };
  }

  render() {
    if (this.state.error) {
      return (
        <div className="panel error-panel">
          <h4 className="m-0 mb-xs">Plugin panel crashed</h4>
          <p className="muted m-0">{this.state.error}</p>
        </div>
      );
    }
    return this.props.children;
  }
}

export function PluginPage({ descriptor }) {
  if (descriptor.entry) {
    return <PagePanel descriptor={descriptor} />;
  }
  if (descriptor.schema) {
    return <SchemaForm descriptor={descriptor} />;
  }
  return (
    <div>
      <div className="page-header">
        <h3>{descriptor.title}</h3>
      </div>
      <div className="panel">
        <p className="muted m-0">{descriptor.description || "This plugin has no settings yet."}</p>
      </div>
    </div>
  );
}

function PagePanel({ descriptor }) {
  const hostRef = useRef(null);
  const rootRef = useRef(null);
  const [error, setError] = useState("");
  const { toast, confirm } = useUI();

  const api = useMemo(
    () => ({
      plugin: descriptor,
      toast,
      confirm,
      fetchJSON: apiFetch,
      postJSON,
      addStyles(css) {
        const shadow = hostRef.current?.shadowRoot;
        if (!shadow) return () => {};
        const tag = document.createElement("style");
        tag.textContent = String(css || "");
        shadow.appendChild(tag);
        return () => tag.remove();
      },
      render(element) {
        const shadow = hostRef.current?.shadowRoot;
        if (!shadow) return () => {};
        if (!rootRef.current) {
          const container = document.createElement("div");
          container.className = "plugin-root";
          shadow.appendChild(container);
          rootRef.current = createRoot(container);
        }
        rootRef.current.render(<PluginErrorBoundary>{element}</PluginErrorBoundary>);
        return () => {
          if (rootRef.current) {
            rootRef.current.unmount();
            rootRef.current = null;
          }
        };
      }
    }),
    [descriptor, toast, confirm]
  );

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    setError("");
    if ((descriptor.ui_api || 0) > 1) {
      setError("This panel requires a newer cdx web UI (ui_api " + descriptor.ui_api + ").");
      return;
    }

    let disposed = false;
    let cleanup = null;

    const shadow = host.shadowRoot || host.attachShadow({ mode: "open" });
    shadow.replaceChildren();
    const base = document.createElement("style");
    base.textContent = baseCss;
    shadow.appendChild(base);

    for (const href of descriptor.styles || []) {
      const link = document.createElement("link");
      link.rel = "stylesheet";
      link.href = href;
      shadow.appendChild(link);
    }

    (async () => {
      try {
        const mod = await import(/* @vite-ignore */ descriptor.entry);
        const mount = typeof mod.mount === "function" ? mod.mount : mod.default;
        if (typeof mount !== "function") {
          throw new Error("Panel module must export a mount(container, api) function");
        }
        const result = await mount(shadow, api);
        if (typeof result === "function") {
          if (disposed) result();
          else cleanup = result;
        }
      } catch (err) {
        if (!disposed) setError(String(err?.message || err));
      }
    })();

    return () => {
      disposed = true;
      if (typeof cleanup === "function") cleanup();
      if (rootRef.current) {
        rootRef.current.unmount();
        rootRef.current = null;
      }
    };
  }, [descriptor, api]);

  return (
    <div>
      <div className="page-header">
        <div>
          <h3>{descriptor.title}</h3>
          {descriptor.description ? <p className="page-intro m-0">{descriptor.description}</p> : null}
        </div>
      </div>
      {error ? (
        <div className="panel error-panel mb-md">
          <h4 className="m-0 mb-xs">Panel failed to load</h4>
          <p className="muted m-0">{error}</p>
        </div>
      ) : null}
      <div ref={hostRef} className="plugin-host" />
    </div>
  );
}
