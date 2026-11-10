import { useCallback, useEffect, useRef, useState } from "react";

const POLICIES = ["allow", "ask", "deny"];
const OUTSIDE_POLICIES = ["ask", "allow", "deny"];

const POLICY_FIELDS = [
  {
    name: "read_policy",
    titleKey: "permissions.ReadTools",
    hintKey: "permissions.ReadToolsHint",
    detailsKey: "permissions.ReadToolsDetails"
  },
  {
    name: "edit_policy",
    titleKey: "permissions.EditTools",
    hintKey: "permissions.EditToolsHint",
    detailsKey: "permissions.EditToolsDetails"
  },
  {
    name: "dangerous_policy",
    titleKey: "permissions.DangerousTools",
    hintKey: "permissions.DangerousToolsHint",
    detailsKey: "permissions.DangerousToolsDetails"
  }
];

export function mount(container, api) {
  return api.render(<PermissionsPanel api={api} />);
}

// The popover opens on hover, on focus, and on a click, so the same hint
// works on a desktop and on a touch screen.
function Hint({ text }) {
  const [hovered, setHovered] = useState(false);
  const [pinned, setPinned] = useState(false);
  const boxRef = useRef(null);
  const open = hovered || pinned;

  useEffect(() => {
    if (!pinned) return;
    function onPointerDown(event) {
      if (boxRef.current && !boxRef.current.contains(event.target)) setPinned(false);
    }
    function onKey(event) {
      if (event.key === "Escape") setPinned(false);
    }
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [pinned]);

  return (
    <span className="hint" ref={boxRef}>
      <button
        type="button"
        className="hint-button"
        aria-label={text}
        aria-expanded={open}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setPinned((current) => !current);
        }}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        onFocus={() => setHovered(true)}
        onBlur={() => setHovered(false)}
      >
        ?
      </button>
      {open ? (
        <span className="hint-pop" role="tooltip">
          {text}
        </span>
      ) : null}
    </span>
  );
}

function PermissionsPanel({ api }) {
  const t = api.t;
  const [config, setConfig] = useState(null);
  const [form, setForm] = useState(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [saving, setSaving] = useState(false);
  const [resetting, setResetting] = useState(false);

  const load = useCallback(async () => {
    setError("");
    try {
      const data = await api.fetchJSON("/api/plugins/permissions/config");
      setConfig(data);
      setForm({
        read_policy: data.read_policy || "allow",
        edit_policy: data.edit_policy || "ask",
        dangerous_policy: data.dangerous_policy || "ask",
        outside_policy: data.outside_policy || "ask"
      });
      setBusy(false);
    } catch (err) {
      setError(String(err.message || err));
      if (err && err.status === 503) {
        // The plugin VM waits for the terminal answer: retry until it is free.
        setBusy(true);
        setAttempt((current) => current + 1);
      }
    }
  }, [api]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (!busy) return;
    const timer = setTimeout(load, 3000);
    return () => clearTimeout(timer);
  }, [busy, attempt, load]);

  async function save(event) {
    event.preventDefault();
    if (!form) return;
    setSaving(true);
    try {
      const data = await api.postJSON("/api/plugins/permissions/config", form);
      setConfig((current) => ({ ...current, ...data }));
      setForm({
        read_policy: data.read_policy,
        edit_policy: data.edit_policy,
        dangerous_policy: data.dangerous_policy,
        outside_policy: data.outside_policy || "ask"
      });
      api.toast(t("permissions.Saved"));
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setSaving(false);
    }
  }

  async function resetGrants() {
    const ok = await api.confirm(t("permissions.ResetTitle"), t("permissions.ResetConfirm"));
    if (!ok) return;
    setResetting(true);
    try {
      await api.postJSON("/api/plugins/permissions/reset-grants", {});
      api.toast(t("permissions.ResetDone"));
      await load();
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setResetting(false);
    }
  }

  if (error) {
    return (
      <div className="panel error-panel">
        <h4 className="m-0 mb-xs">{busy ? t("webui.Schema.Waiting") : t("permissions.NotAvailable")}</h4>
        <p className="muted m-0">{error}</p>
        {busy ? (
          <p className="muted mt-2">{t("webui.Schema.WaitingHint")}</p>
        ) : null}
        <button type="button" className="btn btn-default mt-3" onClick={load}>
          {t("webui.Action.Retry")}
        </button>
      </div>
    );
  }

  if (!config || !form) {
    return <p className="muted">{t("permissions.Loading")}</p>;
  }

  const alwaysRules = Object.keys(config.tool_rules || {});
  const daemonGrants = Object.keys(config.daemon_grants || {});
  const sessionGrants = Object.keys(config.session_grants || {});

  return (
    <div>
      <div className="panel mb-md">
        <h4 className="m-0 mb-md">{t("permissions.ToolPolicies")}</h4>
        <form className="form-stack" onSubmit={save}>
          <div className="grid-3">
            {POLICY_FIELDS.map((field) => (
              <label key={field.name}>
                <span className="field-title">
                  {t(field.titleKey)}
                  <Hint text={t(field.detailsKey)} />
                </span>
                <span className="field-hint">{t(field.hintKey)}</span>
                <select
                  value={form[field.name]}
                  onChange={(event) => setForm({ ...form, [field.name]: event.target.value })}
                >
                  {POLICIES.map((policy) => (
                    <option key={policy} value={policy}>
                      {t("permissions.Policy" + policy.charAt(0).toUpperCase() + policy.slice(1))}
                    </option>
                  ))}
                </select>
              </label>
            ))}
          </div>

          <div className="grid-2">
            <label>
              <span className="field-title">
                {t("permissions.Outside")}
                <Hint text={t("permissions.OutsideDetails")} />
              </span>
              <span className="field-hint">{t("permissions.OutsideHint")}</span>
              <select
                value={form.outside_policy}
                onChange={(event) => setForm({ ...form, outside_policy: event.target.value })}
              >
                {OUTSIDE_POLICIES.map((policy) => (
                  <option key={policy} value={policy}>
                    {t("permissions.Policy" + policy.charAt(0).toUpperCase() + policy.slice(1))}
                  </option>
                ))}
              </select>
              <span className="field-hint">{t("permissions.OutsideFieldHint")}</span>
            </label>
          </div>

          <div>
            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? t("webui.Schema.Saving") : t("permissions.Save")}
            </button>
          </div>
        </form>
      </div>

      <div className="panel">
        <div className="row-between mb-xs">
          <h4 className="m-0">{t("permissions.Grants")}</h4>
          <button
            type="button"
            className="btn btn-danger btn-xs"
            disabled={resetting || alwaysRules.length + daemonGrants.length + sessionGrants.length === 0}
            onClick={resetGrants}
          >
            {resetting ? t("permissions.Resetting") : t("permissions.Reset")}
          </button>
        </div>
        <p className="field-hint mb-md">{t("permissions.GrantsHint")}</p>
        <GrantRow title={t("permissions.Always")} detail={t("permissions.AlwaysHint")} items={alwaysRules} noneLabel={t("permissions.None")} />
        <GrantRow title={t("permissions.Daemon")} detail={t("permissions.DaemonHint")} items={daemonGrants} noneLabel={t("permissions.None")} />
        <GrantRow title={t("permissions.Sessions")} detail={t("permissions.SessionsHint")} items={sessionGrants} noneLabel={t("permissions.None")} />
      </div>
    </div>
  );
}

function GrantRow({ title, detail, items, noneLabel }) {
  return (
    <div className="grant-row">
      <div>
        <div className="text-sm">{title}</div>
        <div className="field-hint">{detail}</div>
      </div>
      <div className="grant-values">
        {items.length === 0 ? (
          <span className="badge badge-outline mono text-xs">{noneLabel}</span>
        ) : (
          items.map((item) => (
            <span key={item} className="badge badge-outline mono text-xs">
              {item}
            </span>
          ))
        )}
      </div>
    </div>
  );
}
