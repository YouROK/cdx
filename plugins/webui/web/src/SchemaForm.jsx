import { useEffect, useMemo, useState } from "react";
import { apiFetch, postJSON } from "./api.js";
import { useUI } from "./ui.jsx";

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
  const { toast } = useUI();
  const [loadedSchema, setLoadedSchema] = useState(schema);
  const [values, setValues] = useState(() => defaultsOf(schema));
  const [secrets, setSecrets] = useState({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const properties = useMemo(() => {
    return Object.entries(loadedSchema?.properties || {});
  }, [loadedSchema]);

  const required = useMemo(() => new Set(loadedSchema?.required || []), [loadedSchema]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
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
      })
      .catch((err) => {
        if (!alive) return;
        setError(String(err.message || err));
        setValues(defaultsOf(schema));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [descriptor.plugin_id, endpoint, generic, schema]);

  function update(name, value) {
    setValues((current) => ({ ...current, [name]: value }));
  }

  async function submit(event) {
    event.preventDefault();
    const payload = {};
    for (const [name, prop] of properties) {
      const value = values[name];
      if (prop.format === "secret" && (value === undefined || value === "")) continue;
      let parsed;
      try {
        parsed = parseValue(value, prop);
      } catch {
        toast("Field " + (prop.title || name) + " contains invalid JSON", false);
        return;
      }
      if (parsed === undefined) {
        if (required.has(name)) {
          toast("Field " + (prop.title || name) + " is required", false);
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
      toast("Settings saved");
    } catch (err) {
      const message = String(err.message || err);
      setError(message);
      toast(message, false);
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return <p className="muted">Loading settings...</p>;
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h3>{loadedSchema?.title || descriptor.title}</h3>
          {descriptor.description ? <p className="page-intro m-0">{descriptor.description}</p> : null}
        </div>
      </div>
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
              {saving ? "Saving..." : "Save settings"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function SchemaField({ name, prop, value, secretSet, required, onChange }) {
  const title = prop.title || name;
  const hint = prop.description ? <span className="field-hint">{prop.description}</span> : null;

  if (prop.type === "boolean") {
    return (
      <label className="switch-card">
        <input type="checkbox" checked={Boolean(value)} onChange={(event) => onChange(event.target.checked)} />
        <div>
          <div>{title}</div>
          {hint}
        </div>
      </label>
    );
  }

  if (prop.type === "string" && Array.isArray(prop.enum)) {
    const labels = Array.isArray(prop.enum_labels) ? prop.enum_labels : prop.enum;
    return (
      <label>
        {title}
        {hint}
        <select value={value == null ? "" : String(value)} onChange={(event) => onChange(event.target.value)}>
          {prop.enum.map((option, index) => (
            <option key={String(option)} value={String(option)}>
              {String(labels[index] ?? option)}
            </option>
          ))}
        </select>
      </label>
    );
  }

  if (prop.type === "integer" || prop.type === "number") {
    return (
      <label>
        {title}
        {hint}
        <input
          type="number"
          value={value == null ? "" : value}
          min={prop.minimum}
          max={prop.maximum}
          step={prop.type === "integer" ? 1 : "any"}
          required={required}
          onChange={(event) => onChange(event.target.value)}
        />
      </label>
    );
  }

  if (prop.type === "array" || prop.type === "object") {
    return (
      <label>
        {title}
        {hint}
        <textarea
          rows="6"
          className="mono text-xs"
          value={displayValue(value, prop)}
          required={required}
          onChange={(event) => onChange(event.target.value)}
        />
      </label>
    );
  }

  return (
    <label>
      {title}
      {hint}
      <input
        type={prop.format === "secret" ? "password" : "text"}
        value={value == null ? "" : String(value)}
        placeholder={prop.format === "secret" && secretSet ? "Stored - leave blank to keep" : ""}
        required={required && !secretSet}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}
