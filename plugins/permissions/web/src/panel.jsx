import { useCallback, useEffect, useRef, useState } from "react";

const POLICIES = ["allow", "ask", "deny"];
const OUTSIDE_POLICIES = ["ask", "allow", "deny"];

const POLICY_FIELDS = [
  {
    name: "read_policy",
    title: "Read tools",
    hint: "read_file, list_dir",
    details:
      "Read tools inspect files without changing them: read_file, list_dir, grep, glob. " +
      "allow runs them silently, ask confirms every call, deny blocks them."
  },
  {
    name: "edit_policy",
    title: "Edit tools",
    hint: "write_file, edit_file",
    details:
      "Edit tools create and change files: write_file, edit_file. " +
      "allow runs them silently, ask confirms every call, deny blocks them."
  },
  {
    name: "dangerous_policy",
    title: "Dangerous tools",
    hint: "shell execution",
    details:
      "Dangerous tools run commands on the machine: shell. " +
      "allow runs them silently, ask confirms every command, deny blocks them."
  }
];

const OUTSIDE_DETAILS =
  "ask shows a confirmation for every action that reaches outside the working folder, allow skips that confirmation, " +
  "and deny blocks such actions. Deny is not a sandbox: commands, symlinks, and absolute paths can still leave the folder, " +
  "so run cdx in an isolated environment when a hard limit is required.";

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
      api.toast("Permission policies saved");
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setSaving(false);
    }
  }

  async function resetGrants() {
    const ok = await api.confirm(
      "Reset remembered grants",
      "Clear all remembered always, session, and daemon permission grants?"
    );
    if (!ok) return;
    setResetting(true);
    try {
      await api.postJSON("/api/plugins/permissions/reset-grants", {});
      api.toast("Remembered grants cleared");
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
        <h4 className="m-0 mb-xs">{busy ? "Waiting for the plugin" : "Permissions are not available"}</h4>
        <p className="muted m-0">{error}</p>
        {busy ? (
          <p className="muted mt-2">
            The plugin is waiting for an answer in the terminal. This page refreshes by itself once the answer is
            given.
          </p>
        ) : null}
        <button type="button" className="btn btn-default mt-3" onClick={load}>
          Retry
        </button>
      </div>
    );
  }

  if (!config || !form) {
    return <p className="muted">Loading permissions...</p>;
  }

  const alwaysRules = Object.keys(config.tool_rules || {});
  const daemonGrants = Object.keys(config.daemon_grants || {});
  const sessionGrants = Object.keys(config.session_grants || {});

  return (
    <div>
      <div className="panel mb-md">
        <h4 className="m-0 mb-md">Tool policies</h4>
        <form className="form-stack" onSubmit={save}>
          <div className="grid-3">
            {POLICY_FIELDS.map((field) => (
              <label key={field.name}>
                <span className="field-title">
                  {field.title}
                  <Hint text={field.details} />
                </span>
                <span className="field-hint">{field.hint}</span>
                <select
                  value={form[field.name]}
                  onChange={(event) => setForm({ ...form, [field.name]: event.target.value })}
                >
                  {POLICIES.map((policy) => (
                    <option key={policy} value={policy}>
                      {policy}
                    </option>
                  ))}
                </select>
              </label>
            ))}
          </div>

          <div className="grid-2">
            <label>
              <span className="field-title">
                Outside the working folder
                <Hint text={OUTSIDE_DETAILS} />
              </span>
              <span className="field-hint">Ask, allow, or deny actions that leave the working folder</span>
              <select
                value={form.outside_policy}
                onChange={(event) => setForm({ ...form, outside_policy: event.target.value })}
              >
                {OUTSIDE_POLICIES.map((policy) => (
                  <option key={policy} value={policy}>
                    {policy}
                  </option>
                ))}
              </select>
              <span className="field-hint">
                Deny is not a sandbox; run cdx in an isolated environment for a hard limit.
              </span>
            </label>
          </div>

          <div>
            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? "Saving..." : "Save policies"}
            </button>
          </div>
        </form>
      </div>

      <div className="panel">
        <div className="row-between mb-xs">
          <h4 className="m-0">Remembered grants</h4>
          <button
            type="button"
            className="btn btn-danger btn-xs"
            disabled={resetting || alwaysRules.length + daemonGrants.length + sessionGrants.length === 0}
            onClick={resetGrants}
          >
            {resetting ? "Resetting..." : "Reset grants"}
          </button>
        </div>
        <p className="field-hint mb-md">
          Grants remembered after answering a permission prompt: "always" is stored in the plugin config,
          "daemon" lasts until the daemon stops, "session" lasts for one session.
        </p>
        <GrantRow title="Always" detail="Stored tool rules" items={alwaysRules} />
        <GrantRow title="Daemon" detail="Until the daemon stops" items={daemonGrants} />
        <GrantRow title="Sessions" detail="Per session" items={sessionGrants} />
      </div>
    </div>
  );
}

function GrantRow({ title, detail, items }) {
  return (
    <div className="grant-row">
      <div>
        <div className="text-sm">{title}</div>
        <div className="field-hint">{detail}</div>
      </div>
      <div className="grant-values">
        {items.length === 0 ? (
          <span className="badge badge-outline mono text-xs">none</span>
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
