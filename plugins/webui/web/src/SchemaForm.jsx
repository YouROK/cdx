import { useCallback, useEffect, useMemo, useState } from "react";
import { apiFetch, postJSON } from "./api.js";
import { useUI } from "./ui.jsx";
import { Hint } from "./hint.jsx";

// Descriptions longer than this move into the hint popover, so a form with
// several fields stays compact.
const INLINE_HINT_LIMIT = 70;

function defaultsOf(schema) {
  const out = {};
  for (const [key, prop] of Object.entries(schema?.properties || {})) {
    if (prop && prop.default !== undefined) out[key] = prop.default;
  }
  return out;
}

function displayValue(value, prop) {
  if (value === undefined || value === null) return "";
  if (prop.type === "object" || prop.type === "array") {
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return "";
    }
  }
  return String(value);
}

function parseValue(value, prop) {
  if (prop.type === "boolean") return Boolean(value);
  if (prop.type === "integer") {
    if (value === "" || value === undefined || value === null) return undefined;
    return Math.trunc(Number(value));
  }
  if (prop.type === "number") {
    if (value === "" || value === undefined || value === null) return undefined;
    return Number(value);
  }
  if (prop.type === "array" || prop.type === "object") {
    if (value === "" || value === undefined || value === null) return undefined;
    if (typeof value !== "string") return value;
    return JSON.parse(value);
  }
  return String(value ?? "");
}

export function SchemaForm({ descriptor }) {
  const schema = descriptor.schema || {};
  const endpoint = descriptor.config_url || "/api/core/plugins/" + encodeURIComponent(descriptor.plugin_id) + "/settings";
  const generic = !descriptor.config_url;
  const { toast, t } = useUI();
  const [loadedSchema, setLoadedSchema] = useState(schema);
  const [values, setValues] = useState(() => defaultsOf(schema));
  const [secrets, setSecrets] = useState({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState(0);

  const properties = useMemo(() => {
    return Object.entries(loadedSchema?.properties || {});
  }, [loadedSchema]);

  const required = useMemo(() => new Set(loadedSchema?.required || []), [loadedSchema]);

  const load = useCallback(() => {
    let alive = true;
    setLoading(true);
    setFailed(false);
    setError("");
    apiFetch(endpoint)
      .then((data) => {
        if (!alive) return;
        if (generic) {
          setLoadedSchema(data.schema || schema);
          setValues({ ...defaultsOf(data.schema || schema), ...(data.values || {}) });
          setSecrets(data.secrets || {});
        } else {
          setValues({ ...defaultsOf(schema), ...(data || {}) });
          setSecrets({});
        }
        setBusy(false);
      })
      .catch((err) => {
        if (!alive) return;
        setFailed(true);
        setError(String(err.message || err));
        if (err && err.status === 503) {
          // The plugin VM waits for the terminal answer: retry until it is free.
          setBusy(true);
          setAttempt((current) => current + 1);
        }
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [descriptor.plugin_id, endpoint, generic, schema]);

  useEffect(() => {
    return load();
  }, [load]);

  useEffect(() => {
    if (!busy) return;
    const timer = setTimeout(load, 3000);
    return () => clearTimeout(timer);
  }, [busy, attempt, load]);

  function update(name, value) {
    setValues((current) => ({ ...current, [name]: value }));
  }

  async function submit(event) {
    event.preventDefault();
    if (failed) return;
    const payload = {};
    for (const [name, prop] of properties) {
      const value = values[name];
      if (prop.format === "secret" && (value === undefined || value === "")) continue;
      let parsed;
      try {
        parsed = parseValue(value, prop);
      } catch {
        toast(t("webui.Schema.FieldInvalidJSON", { name: t(prop.title || name) }), false);
        return;
      }
      if (parsed === undefined) {
        if (required.has(name)) {
          toast(t("webui.Schema.FieldRequired", { name: t(prop.title || name) }), false);
          return;
        }
        continue;
      }
      payload[name] = parsed;
    }

    setSaving(true);
    setError("");
    try {
      const data = await postJSON(endpoint, payload);
      if (generic) {
        setValues({ ...defaultsOf(loadedSchema), ...(data.values || {}) });
        setSecrets(data.secrets || {});
      } else {
        setValues({ ...defaultsOf(schema), ...(data || payload) });
      }
      toast(t("webui.Schema.Saved"));
    } catch (err) {
      const message = String(err.message || err);
      setError(message);
      toast(message, false);
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return <p className="muted">{t("webui.Schema.Loading")}</p>;
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h3>{t(loadedSchema?.title || descriptor.title)}</h3>
          {descriptor.description ? <p className="page-intro m-0">{t(descriptor.description)}</p> : null}
        </div>
      </div>
      {failed ? (
        <div className="panel error-panel">
          <h4 className="m-0 mb-xs">{busy ? t("webui.Schema.Waiting") : t("webui.Schema.Unavailable")}</h4>
          <p className="muted m-0">{error}</p>
          {busy ? (
            <p className="muted mt-2">{t("webui.Schema.WaitingHint")}</p>
          ) : null}
          <p className="muted mt-2">{t("webui.Schema.DisabledHint")}</p>
          <button type="button" className="btn btn-default" onClick={load}>
            {t("webui.Action.Retry")}
          </button>
        </div>
      ) : (
      <div className="panel">
        {error ? <div className="error-text mb-md">{error}</div> : null}
        <form className="form-stack" onSubmit={submit}>
          <div className="schema-grid">
            {properties.map(([name, prop]) => (
              <SchemaField
                key={name}
                name={name}
                prop={prop}
                value={values[name]}
                secretSet={Boolean(secrets[name])}
                required={required.has(name)}
                onChange={(value) => update(name, value)}
              />
            ))}
          </div>
          <div>
            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? t("webui.Schema.Saving") : t("webui.Schema.Save")}
            </button>
          </div>
        </form>
      </div>
      )}
    </div>
  );
}

function SchemaField({ name, prop, value, secretSet, required, onChange }) {
  const { t } = useUI();
  const title = t(prop.title || name);
  const description = prop.description ? t(String(prop.description)) : "";
  const inlineHint = description && description.length <= INLINE_HINT_LIMIT ? description : "";
  const titleNode = (
    <span className="field-title">
      {title}
      {description ? <Hint text={description} label={t("webui.Schema.About") + " " + title} /> : null}
    </span>
  );
  const hint = inlineHint ? <span className="field-hint">{inlineHint}</span> : null;

  if (prop.type === "boolean") {
    return (
      <label>
        {titleNode}
        <span className="check-field">
          <input type="checkbox" checked={Boolean(value)} onChange={(event) => onChange(event.target.checked)} />
          <span>{t("webui.Schema.Enabled")}</span>
        </span>
        {hint}
      </label>
    );
  }

  if (prop.type === "string" && Array.isArray(prop.enum)) {
    const labels = Array.isArray(prop.enum_labels) ? prop.enum_labels : prop.enum;
    return (
      <label>
        {titleNode}
        <select value={value == null ? "" : String(value)} onChange={(event) => onChange(event.target.value)}>
          {prop.enum.map((option, index) => (
            <option key={String(option)} value={String(option)}>
              {t(String(labels[index] ?? option))}
            </option>
          ))}
        </select>
        {hint}
      </label>
    );
  }

  if (prop.type === "integer" || prop.type === "number") {
    return (
      <label>
        {titleNode}
        <input
          type="number"
          value={value == null ? "" : value}
          min={prop.minimum}
          max={prop.maximum}
          step={prop.type === "integer" ? 1 : "any"}
          required={required}
          onChange={(event) => onChange(event.target.value)}
        />
        {hint}
      </label>
    );
  }

  if (prop.type === "array" || prop.type === "object") {
    return (
      <label>
        {titleNode}
        <textarea
          rows="6"
          className="mono text-xs"
          value={displayValue(value, prop)}
          required={required}
          onChange={(event) => onChange(event.target.value)}
        />
        {hint}
      </label>
    );
  }

  return (
    <label>
      {title}
      <input
        type={prop.format === "secret" ? "password" : "text"}
        value={value == null ? "" : String(value)}
        placeholder={prop.format === "secret" && secretSet ? t("webui.Schema.Stored") : ""}
        required={required && !secretSet}
        onChange={(event) => onChange(event.target.value)}
      />
      {hint}
    </label>
  );
}
