import { useEffect, useMemo, useRef, useState } from "react";
const DEFAULT_CONTEXT = 128000;
const DEFAULT_MAX_TOKENS = 4096;

function fmtNum(value) {
  if (!value) return "not set";
  return String(value).replace(/\B(?=(\d{3})+(?!\d))/g, " ");
}

function formatThousands(value) {
  const digits = String(value ?? "").replace(/[^\d]/g, "");
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, " ");
}

function NumberInput({ value, onChange, placeholder }) {
  const ref = useRef(null);
  const [text, setText] = useState(() => formatThousands(value));

  useEffect(() => {
    setText(formatThousands(value));
  }, [value]);

  function handleChange(event) {
    const input = event.target;
    const raw = input.value;
    const caretDigits = raw.slice(0, input.selectionStart ?? raw.length).replace(/[^\d]/g, "").length;
    const digits = raw.replace(/[^\d]/g, "");
    const formatted = formatThousands(digits);
    setText(formatted);
    onChange(digits);
    requestAnimationFrame(() => {
      const node = ref.current;
      if (!node) return;
      if (digits.length === 0) {
        node.setSelectionRange(0, 0);
        return;
      }
      let seen = 0;
      let position = formatted.length;
      for (let i = 0; i < formatted.length; i++) {
        if (/\d/.test(formatted[i])) seen++;
        if (seen === caretDigits) {
          position = i + 1;
          break;
        }
      }
      node.setSelectionRange(position, position);
    });
  }

  return (
    <input
      ref={ref}
      type="text"
      inputMode="numeric"
      value={text}
      placeholder={placeholder}
      onChange={handleChange}
    />
  );
}

function Modal({ title, wide, onClose, children }) {
  return (
    <div
      className="modal-backdrop"
      onClick={(event) => {
        if (event.target.classList.contains("modal-backdrop")) onClose();
      }}
    >
      <div className={"modal-box" + (wide ? " modal-wide" : "")}>
        <div className="modal-header">
          <h4>{title}</h4>
          <button type="button" className="btn-icon" onClick={onClose}>
            &times;
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  );
}

export function mount(container, api) {
  return api.render(<ProvidersPanel api={api} />);
}

function ProvidersPanel({ api }) {
  const [state, setState] = useState({ providers: [], models: [] });
  const [modal, setModal] = useState(null);

  async function load() {
    try {
      setState(await api.fetchJSON("/api/plugins/providers/state"));
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  const providers = state.providers || [];
  const models = state.models || [];

  return (
    <div>
      <div className="row-between mb-md">
        <p className="page-intro m-0">
          A provider holds the connection: address, protocol and key. Models belong to a provider and differ only in
          their own settings.
        </p>
        <button type="button" className="btn btn-primary" onClick={() => setModal({ type: "provider" })}>
          Add provider
        </button>
      </div>

      {providers.length === 0 ? (
        <div className="panel empty-panel">
          <p className="muted">No providers configured yet.</p>
          <button type="button" className="btn btn-primary" onClick={() => setModal({ type: "provider" })}>
            Add provider
          </button>
        </div>
      ) : (
        <div className="stack-md">
          {providers.map((provider) => (
            <ProviderCard
              key={provider.name}
              provider={provider}
              models={models.filter((model) => model.provider === provider.name)}
              api={api}
              reload={load}
              setModal={setModal}
            />
          ))}
        </div>
      )}

      {modal?.type === "provider" ? (
        <ProviderModal
          api={api}
          provider={modal.provider}
          onClose={() => setModal(null)}
          onSaved={() => {
            setModal(null);
            load();
          }}
        />
      ) : null}
      {modal?.type === "key" ? (
        <KeyModal
          api={api}
          provider={modal.provider}
          onClose={() => setModal(null)}
          onSaved={() => {
            setModal(null);
            load();
          }}
        />
      ) : null}
      {modal?.type === "model" ? (
        <ModelModal
          api={api}
          provider={modal.provider}
          model={modal.model}
          isFirst={models.length === 0}
          onClose={() => setModal(null)}
          onSaved={() => {
            setModal(null);
            load();
          }}
        />
      ) : null}
      {modal?.type === "discover" ? (
        <DiscoverModal
          api={api}
          provider={modal.provider}
          onClose={() => setModal(null)}
          onSaved={() => {
            setModal(null);
            load();
          }}
        />
      ) : null}
    </div>
  );
}

function ProviderCard({ provider, models, api, reload, setModal }) {
  async function test() {
    try {
      const data = await api.postJSON("/api/plugins/providers/providers/test", { name: provider.name });
      api.toast(data.message || "Test finished", Boolean(data.ok));
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  async function remove() {
    const ok = await api.confirm("Delete provider", "Delete provider '" + provider.name + "' and its models?");
    if (!ok) return;
    try {
      await api.fetchJSON("/api/plugins/providers/providers?name=" + encodeURIComponent(provider.name), {
        method: "DELETE"
      });
      api.toast("Provider deleted");
      reload();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <div className="provider-card">
      <div className="row-between mb-md">
        <div>
          <div className="row-gap-xs">
            <h4 className="m-0">{provider.name}</h4>
            <span className={provider.has_api_key ? "badge badge-teal" : "badge badge-warn"}>
              {provider.has_api_key ? "key set" : "no key"}
            </span>
            <span className="badge badge-outline">{provider.protocol || "openai-chat"}</span>
          </div>
          <div className="mono text-xs muted mt-2">{provider.base_url || "-"}</div>
        </div>
        <div className="row-gap-xs">
          <button type="button" className="btn btn-light btn-xs" onClick={test}>
            Test
          </button>
          <button
            type="button"
            className="btn btn-light btn-xs"
            onClick={() => setModal({ type: "discover", provider: provider.name })}
          >
            Get models
          </button>
          <button
            type="button"
            className="btn btn-default btn-xs"
            onClick={() => setModal({ type: "key", provider: provider.name })}
          >
            Key
          </button>
          <button
            type="button"
            className="btn btn-default btn-xs"
            onClick={() => setModal({ type: "provider", provider })}
          >
            Edit
          </button>
          <button type="button" className="btn btn-danger btn-xs" onClick={remove}>
            Delete
          </button>
        </div>
      </div>
      <div className="row-between mb-xs">
        <span className="section-caption">MODELS: {models.length}</span>
        <button
          type="button"
          className="btn btn-subtle btn-xs"
          onClick={() => setModal({ type: "model", provider: provider.name })}
        >
          + Add model
        </button>
      </div>
      {models.length === 0 ? (
        <div className="muted text-sm">This provider has no models yet.</div>
      ) : (
        <div className="stack-xs">
          {models.map((model) => (
            <ModelRow
              key={model.id || model.name}
              model={model}
              api={api}
              reload={reload}
              setModal={setModal}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ModelRow({ model, api, reload, setModal }) {
  async function makeDefault() {
    try {
      await api.postJSON("/api/plugins/providers/models/default", {
        provider: model.provider,
        name: model.name
      });
      api.toast("Default model updated");
      reload();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  async function remove() {
    const ok = await api.confirm("Delete model", "Delete model '" + model.provider + "/" + model.name + "'?");
    if (!ok) return;
    try {
      await api.fetchJSON(
        "/api/plugins/providers/models?provider=" +
          encodeURIComponent(model.provider) +
          "&name=" +
          encodeURIComponent(model.name),
        { method: "DELETE" }
      );
      api.toast("Model deleted");
      reload();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <div className="model-row">
      <div className="row-between">
        <div>
          <div className="row-gap-xs">
            <span className="fw-600">{model.name}</span>
            {model.default ? <span className="badge badge-accent">default</span> : null}
          </div>
          <div className="mono text-xs muted">{model.remote || model.name}</div>
          <div className="row-gap-md text-xs muted mt-2">
            <span>Context: {fmtNum(model.context_window)}</span>
            <span>Max output: {fmtNum(model.max_tokens)}</span>
            <span>Temperature: {model.temperature !== undefined ? model.temperature : 0.7}</span>
          </div>
        </div>
        <div className="row-gap-xs">
          <button type="button" className="btn btn-light btn-xs" disabled={model.default} onClick={makeDefault}>
            Default
          </button>
          <button
            type="button"
            className="btn btn-default btn-xs"
            onClick={() => setModal({ type: "model", provider: model.provider, model })}
          >
            Edit
          </button>
          <button type="button" className="btn btn-danger btn-xs" onClick={remove}>
            Delete
          </button>
        </div>
      </div>
    </div>
  );
}

function ProviderModal({ api, provider, onClose, onSaved }) {
  const existing = Boolean(provider);
  const [form, setForm] = useState(
    provider || { name: "", protocol: "openai-chat", base_url: "", api_key: "" }
  );

  async function submit(event) {
    event.preventDefault();
    try {
      await api.postJSON("/api/plugins/providers/providers", {
        name: form.name,
        protocol: form.protocol,
        base_url: form.base_url,
        api_key: form.api_key || ""
      });
      api.toast("Provider saved");
      onSaved();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <Modal title={existing ? "Edit provider: " + provider.name : "New provider"} onClose={onClose}>
      <form className="form-stack" onSubmit={submit}>
        <label>
          Name
          <span className="field-hint">Unique provider connection name</span>
          <input
            value={form.name}
            readOnly={existing}
            required
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </label>
        <label>
          Protocol
          <select value={form.protocol} onChange={(event) => setForm({ ...form, protocol: event.target.value })}>
            <option value="openai-chat">openai-chat</option>
            <option value="anthropic">anthropic</option>
          </select>
        </label>
        <label>
          Base URL
          <span className="field-hint">API endpoint address, e.g. https://api.openai.com/v1</span>
          <input
            placeholder="https://api.openai.com/v1"
            value={form.base_url || ""}
            onChange={(event) => setForm({ ...form, base_url: event.target.value })}
          />
        </label>
        <label>
          API Key
          <span className="field-hint">
            {existing ? "Leave blank to keep existing encrypted key" : "Stored encrypted in BoltDB secrets"}
          </span>
          <input
            type="password"
            placeholder="sk-..."
            value={form.api_key || ""}
            onChange={(event) => setForm({ ...form, api_key: event.target.value })}
          />
        </label>
        <div className="modal-actions">
          <button type="button" className="btn btn-default" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary">
            Save
          </button>
        </div>
      </form>
    </Modal>
  );
}

function KeyModal({ api, provider, onClose, onSaved }) {
  const [key, setKey] = useState("");

  async function submit(event) {
    event.preventDefault();
    try {
      await api.postJSON("/api/plugins/providers/providers/key", { name: provider, api_key: key });
      api.toast("API key saved");
      onSaved();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <Modal title={"Key: " + provider} onClose={onClose}>
      <form className="form-stack" onSubmit={submit}>
        <label>
          API Key
          <span className="field-hint">Encrypted with AES-256-GCM inside cdx.db</span>
          <input type="password" placeholder="Enter new key..." value={key} onChange={(event) => setKey(event.target.value)} />
        </label>
        <div className="modal-actions">
          <button type="button" className="btn btn-default" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary">
            Save
          </button>
        </div>
      </form>
    </Modal>
  );
}

function ModelModal({ api, provider, model, isFirst, onClose, onSaved }) {
  const existing = Boolean(model);
  const [form, setForm] = useState(() => ({
    provider,
    name: model?.name || "",
    remote: model?.remote || "",
    context_window: String(model?.context_window || DEFAULT_CONTEXT),
    max_tokens: String(model?.max_tokens || DEFAULT_MAX_TOKENS),
    temperature: model?.temperature !== undefined ? Number(model.temperature) : 1,
    reasoning_levels: (model?.reasoning_levels || []).join(","),
    reasoning_default: model?.reasoning_default || "",
    supports_reasoning_effort: Boolean(model?.supports_reasoning_effort),
    skip_temperature: Boolean(model?.skip_temperature),
    default: model?.default ?? isFirst
  }));

  const levels = useMemo(
    () =>
      String(form.reasoning_levels || "")
        .split(/[,\n]/)
        .map((item) => item.trim())
        .filter(Boolean),
    [form.reasoning_levels]
  );

  function update(name, value) {
    setForm((current) => ({ ...current, [name]: value }));
  }

  async function submit(event) {
    event.preventDefault();
    try {
      await api.postJSON("/api/plugins/providers/models", {
        provider,
        name: form.name,
        remote: form.remote || form.name,
        context_window: Number(form.context_window) || DEFAULT_CONTEXT,
        max_tokens: Number(form.max_tokens) || DEFAULT_MAX_TOKENS,
        temperature: Number(form.temperature),
        reasoning_levels: levels,
        reasoning_default: form.reasoning_default,
        supports_reasoning_effort: form.supports_reasoning_effort,
        skip_temperature: form.skip_temperature,
        default: form.default
      });
      api.toast("Model saved");
      onSaved();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <Modal
      title={existing ? "Edit model: " + model.name : "New model: " + provider}
      wide
      onClose={onClose}
    >
      <form className="form-stack" onSubmit={submit}>
        <div className="grid-2">
          <label>
            Name
            <span className="field-hint">Unique across all providers, used by --model and in history.</span>
            <input value={form.name} required onChange={(event) => update("name", event.target.value)} />
          </label>
          <label>
            Remote model id
            <span className="field-hint">The identifier the provider expects.</span>
            <input
              placeholder="deepseek-ai/DeepSeek-V4-Flash"
              value={form.remote}
              onChange={(event) => update("remote", event.target.value)}
            />
          </label>
        </div>
        <div className="grid-3">
          <label>
            Context
            <span className="field-hint">Uses the provider value when available, otherwise 128000.</span>
            <NumberInput
              value={form.context_window}
              placeholder="128000"
              onChange={(value) => update("context_window", value)}
            />
          </label>
          <label>
            Max output
            <span className="field-hint">Uses the provider value when available, otherwise 4096.</span>
            <NumberInput
              value={form.max_tokens}
              placeholder="4096"
              onChange={(value) => update("max_tokens", value)}
            />
          </label>
          <label>
            <div>
              Temperature <span className="muted">{Number(form.temperature).toFixed(2)}</span>
            </div>
            <div className="slider-wrap">
              <input
                type="range"
                min="0"
                max="2"
                step="0.05"
                value={form.temperature}
                onChange={(event) => update("temperature", event.target.value)}
              />
              <div className="slider-marks">
                <span>0</span>
                <span>1</span>
                <span>2</span>
              </div>
            </div>
            <span className="field-hint">Lower is more predictable, higher is more creative.</span>
          </label>
        </div>
        <label>
          Reasoning levels
          <span className="field-hint">Comma separated, in order from weak to strong.</span>
          <input
            placeholder="low,medium,high,max"
            value={form.reasoning_levels}
            onChange={(event) => update("reasoning_levels", event.target.value)}
          />
        </label>
        <label>
          Default reasoning level
          <select
            value={form.reasoning_default}
            onChange={(event) => update("reasoning_default", event.target.value)}
          >
            <option value="">(none)</option>
            {levels.map((level) => (
              <option key={level} value={level}>
                {level}
              </option>
            ))}
          </select>
        </label>
        <div className="switch-grid">
          <label className="switch-card">
            <input
              type="checkbox"
              checked={form.default}
              onChange={(event) => update("default", event.target.checked)}
            />
            <div>
              <div>Use by default</div>
              <div className="field-hint">One model is the default for the whole program.</div>
            </div>
          </label>
          <label className="switch-card">
            <input
              type="checkbox"
              checked={form.supports_reasoning_effort}
              onChange={(event) => update("supports_reasoning_effort", event.target.checked)}
            />
            <div>
              <div>Send reasoning_effort</div>
              <div className="field-hint">Only when the provider accepts the parameter.</div>
            </div>
          </label>
          <label className="switch-card">
            <input
              type="checkbox"
              checked={form.skip_temperature}
              onChange={(event) => update("skip_temperature", event.target.checked)}
            />
            <div>
              <div>Don't send temperature</div>
              <div className="field-hint">Needed when the provider rejects the parameter.</div>
            </div>
          </label>
        </div>
        <div className="modal-actions">
          <button type="button" className="btn btn-default" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary">
            Save
          </button>
        </div>
      </form>
    </Modal>
  );
}

function DiscoverModal({ api, provider, onClose, onSaved }) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [models, setModels] = useState([]);
  const [selected, setSelected] = useState(() => new Set());
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const data = await api.postJSON("/api/plugins/providers/providers/discover", { name: provider });
        if (!alive) return;
        const list = (data.models || [])
          .map((item) => {
            if (typeof item === "string") {
              return { id: item, context_window: DEFAULT_CONTEXT, max_tokens: DEFAULT_MAX_TOKENS };
            }
            return {
              id: String(item.id || ""),
              context_window: Number(item.context_window) || DEFAULT_CONTEXT,
              max_tokens: Number(item.max_tokens) || DEFAULT_MAX_TOKENS
            };
          })
          .filter((item) => item.id !== "");
        setModels(list);
        setSelected(new Set(list.map((item) => item.id)));
      } catch (err) {
        if (alive) setError(String(err.message || err));
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => {
      alive = false;
    };
  }, [provider]);

  function toggle(id) {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function addSelected() {
    setSaving(true);
    try {
      const byId = new Map(models.map((item) => [item.id, item]));
      for (const id of selected) {
        const item = byId.get(id) || { context_window: DEFAULT_CONTEXT, max_tokens: DEFAULT_MAX_TOKENS };
        const local = id.replace(/[^a-zA-Z0-9._-]+/g, "-");
        await api.postJSON("/api/plugins/providers/models", {
          provider,
          name: local,
          remote: id,
          context_window: item.context_window,
          max_tokens: item.max_tokens,
          temperature: 0.7
        });
      }
      api.toast("Models added: " + selected.size);
      onSaved();
    } catch (err) {
      api.toast(String(err.message || err), false);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal title={"Get models: " + provider} onClose={onClose}>
      {loading ? (
        <p className="muted">Fetching models...</p>
      ) : error ? (
        <div className="error-text">{error}</div>
      ) : models.length === 0 ? (
        <p className="muted">The provider returned no models.</p>
      ) : (
        <>
          <div className="discover-controls">
            <button
              type="button"
              className="btn btn-light btn-xs"
              onClick={() => setSelected(new Set(models.map((item) => item.id)))}
            >
              Select all
            </button>
            <button type="button" className="btn btn-light btn-xs" onClick={() => setSelected(new Set())}>
              Clear all
            </button>
            <span className="text-xs muted">
              {selected.size} of {models.length} selected
            </span>
          </div>
          <div className="discover-list">
            {models.map((item) => (
              <label key={item.id} className="checkbox-row discover-item">
                <input type="checkbox" checked={selected.has(item.id)} onChange={() => toggle(item.id)} />
                <span className="mono text-sm">{item.id}</span>
              </label>
            ))}
          </div>
        </>
      )}
      <div className="modal-actions mt-md">
        <button type="button" className="btn btn-default" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="btn btn-primary"
          disabled={loading || saving || selected.size === 0}
          onClick={addSelected}
        >
          {saving ? "Adding..." : "Add selected"}
        </button>
      </div>
    </Modal>
  );
}
