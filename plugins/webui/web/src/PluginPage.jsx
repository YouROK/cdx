import { Component, useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { apiFetch, postJSON } from "./api.js";
import { SchemaForm } from "./SchemaForm.jsx";
import { useUI } from "./ui.jsx";

// Panels get the shell stylesheet so they look native. The built stylesheet is
// linked by URL; the inline copy is only a fallback for a dev server that
// injects styles instead of linking them.
async function createBaseStyles() {
  const link = document.querySelector('link[rel="stylesheet"]');
  if (link && link.href) {
    const clone = document.createElement("link");
    clone.rel = "stylesheet";
    clone.href = link.href;
    clone.dataset.cdxBase = "1";
    return [clone];
  }
  const { default: css } = await import("../style.css?inline");
  const tag = document.createElement("style");
  tag.textContent = css;
  tag.dataset.cdxBase = "1";
  return [tag];
}

// A panel that writes container.innerHTML replaces the style nodes of the
// shadow root. They are put back, so the panel keeps the dashboard look.
function keepStyles(shadow, nodes) {
  if (!nodes.length) return () => {};
  const restore = () => {
    const missing = nodes.filter((node) => !shadow.contains(node));
    if (missing.length === 0) return;
    for (let i = missing.length - 1; i >= 0; i -= 1) {
      shadow.prepend(missing[i]);
    }
  };
  const observer = new MutationObserver(restore);
  observer.observe(shadow, { childList: true });
  return () => observer.disconnect();
}

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
          <h4 className="m-0 mb-xs">{this.props.t("webui.Plugin.Crashed")}</h4>
          <p className="muted m-0">{this.state.error}</p>
        </div>
      );
    }
    return this.props.children;
  }
}

export function PluginPage({ descriptor }) {
  const { t } = useUI();
  if (descriptor.entry) {
    return <PagePanel descriptor={descriptor} />;
  }
  if (descriptor.schema) {
    return <SchemaForm descriptor={descriptor} />;
  }
  return (
    <div>
      <div className="page-header">
        <h3>{t(descriptor.title)}</h3>
      </div>
      <div className="panel">
        <p className="muted m-0">{t(descriptor.description || "webui.Plugin.NoSettings")}</p>
      </div>
    </div>
  );
}

function PagePanel({ descriptor }) {
  const hostRef = useRef(null);
  const rootRef = useRef(null);
  const [error, setError] = useState("");
  const { toast, confirm, t, locale } = useUI();

  const api = useMemo(
    () => ({
      plugin: descriptor,
      toast,
      confirm,
      t,
      locale,
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
        rootRef.current.render(<PluginErrorBoundary t={t}>{element}</PluginErrorBoundary>);
        return () => {
          if (rootRef.current) {
            rootRef.current.unmount();
            rootRef.current = null;
          }
        };
      }
    }),
    [descriptor, toast, confirm, t, locale]
  );

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    setError("");
    if ((descriptor.ui_api || 0) > 1) {
      setError(t("webui.Plugin.NeedsNewerUI", { version: descriptor.ui_api }));
      return;
    }

    let disposed = false;
    let cleanup = null;

    const shadow = host.shadowRoot || host.attachShadow({ mode: "open" });
    shadow.replaceChildren();

    let stopKeepingStyles = () => {};
    let styleNodes = [];

    (async () => {
      try {
        styleNodes = await createBaseStyles();
        for (const node of styleNodes) shadow.appendChild(node);
        for (const href of descriptor.styles || []) {
          const link = document.createElement("link");
          link.rel = "stylesheet";
          link.href = href;
          shadow.appendChild(link);
          styleNodes.push(link);
        }
        stopKeepingStyles = keepStyles(shadow, styleNodes);
        const mod = await import(/* @vite-ignore */ descriptor.entry);
        const mount = typeof mod.mount === "function" ? mod.mount : mod.default;
        if (typeof mount !== "function") {
          throw new Error(t("webui.Plugin.ModuleAPI"));
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
      stopKeepingStyles();
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
          <h3>{t(descriptor.title)}</h3>
          {descriptor.description ? <p className="page-intro m-0">{t(descriptor.description)}</p> : null}
        </div>
      </div>
      {error ? (
        <div className="panel error-panel mb-md">
          <h4 className="m-0 mb-xs">{t("webui.Plugin.PanelFailed")}</h4>
          <p className="muted m-0">{error}</p>
        </div>
      ) : null}
      <div ref={hostRef} className="plugin-host" />
    </div>
  );
}
