import { useEffect, useState } from "react";

export function mount(container, api) {
  return api.render(<PresetsPanel api={api} />);
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

function PresetsPanel({ api }) {
  const [presets, setPresets] = useState([]);
  const [modal, setModal] = useState(null);

  async function load() {
    try {
      const data = await api.fetchJSON("/api/plugins/presets/state");
      setPresets(data.presets || []);
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  const builtin = presets.filter((preset) => preset.builtin);
  const custom = presets.filter((preset) => !preset.builtin);

  async function activate(id) {
    try {
      await api.postJSON("/api/plugins/presets/active", { id });
      api.toast("Preset activated: " + id);
      load();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  async function remove(id) {
    const ok = await api.confirm("Delete preset", "Delete custom preset '" + id + "'?");
    if (!ok) return;
    try {
      await api.fetchJSON("/api/plugins/presets/custom/" + encodeURIComponent(id), { method: "DELETE" });
      api.toast("Preset deleted");
      load();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <div>
      <div className="row-between mb-md">
        <p className="page-intro m-0">
          A preset changes how the agent behaves and answers. The list of tools and environment are collected
          automatically on every request.
        </p>
        <button type="button" className="btn btn-primary" onClick={() => setModal({ type: "edit" })}>
          Create preset
        </button>
      </div>

      <div className="stack-md">
        <div>
          <div className="section-caption mb-xs">Built in</div>
          <div className="grid-3">
            {builtin.map((preset) => (
              <PresetTile
                key={preset.id}
                preset={preset}
                onActivate={() => activate(preset.id)}
                onView={() => setModal({ type: "view", preset })}
                onEdit={() => setModal({ type: "edit", preset })}
                onDelete={() => remove(preset.id)}
              />
            ))}
          </div>
        </div>
        {custom.length > 0 ? (
          <div>
            <div className="section-caption mb-xs">Yours</div>
            <div className="grid-3">
              {custom.map((preset) => (
                <PresetTile
                  key={preset.id}
                  preset={preset}
                  onActivate={() => activate(preset.id)}
                  onView={() => setModal({ type: "view", preset })}
                  onEdit={() => setModal({ type: "edit", preset })}
                  onDelete={() => remove(preset.id)}
                />
              ))}
            </div>
          </div>
        ) : null}
      </div>

      {modal?.type === "edit" ? (
        <PresetModal
          api={api}
          preset={modal.preset}
          onClose={() => setModal(null)}
          onSaved={() => {
            setModal(null);
            load();
          }}
        />
      ) : null}
      {modal?.type === "view" ? (
        <Modal title={"Prompt: " + (modal.preset.name || modal.preset.id)} onClose={() => setModal(null)}>
          <p className="field-hint mb-xs">{modal.preset.builtin ? "Built-in preset (read-only)" : "Custom preset"}</p>
          <pre className="code-block">{modal.preset.prompt || ""}</pre>
          <div className="modal-actions mt-md">
            <button type="button" className="btn btn-default" onClick={() => setModal(null)}>
              Close
            </button>
          </div>
        </Modal>
      ) : null}
    </div>
  );
}

function PresetTile({ preset, onActivate, onView, onEdit, onDelete }) {
  return (
    <div
      className="provider-card"
      style={{
        borderColor: preset.active ? "var(--accent)" : undefined,
        borderWidth: preset.active ? 2 : undefined,
        display: "flex",
        flexDirection: "column",
        gap: 10
      }}
    >
      <div>
        <div className="row-gap-xs">
          <h4 className="m-0">{preset.name || preset.id}</h4>
          {preset.active ? <span className="badge badge-accent">active</span> : null}
        </div>
        <div className="mono text-xs muted mt-2">{preset.id}</div>
      </div>
      <div className="text-sm" style={{ flex: 1 }}>
        {preset.description || "-"}
      </div>
      <div className="row-gap-xs">
        <button type="button" className="btn btn-light btn-xs" disabled={preset.active} onClick={onActivate}>
          Use this preset
        </button>
        <button type="button" className="btn btn-default btn-xs" onClick={onView}>
          Show prompt
        </button>
        {!preset.builtin ? (
          <>
            <button type="button" className="btn btn-default btn-xs" onClick={onEdit}>
              Edit
            </button>
            <button type="button" className="btn btn-danger btn-xs" onClick={onDelete}>
              Delete
            </button>
          </>
        ) : null}
      </div>
    </div>
  );
}

function PresetModal({ api, preset, onClose, onSaved }) {
  const existing = Boolean(preset);
  const [form, setForm] = useState(
    preset || { id: "", name: "", description: "", prompt: "" }
  );

  async function submit(event) {
    event.preventDefault();
    try {
      await api.postJSON("/api/plugins/presets/custom", {
        original: existing ? preset.id : "",
        id: form.id,
        name: form.name,
        description: form.description,
        prompt: form.prompt
      });
      api.toast("Preset saved");
      onSaved();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <Modal title={existing ? "Edit preset: " + preset.name : "New preset"} wide onClose={onClose}>
      <form className="form-stack" onSubmit={submit}>
        <div className="grid-2">
          <label>
            Name
            <span className="field-hint">Shown on the tile</span>
            <input
              placeholder="Review"
              required
              value={form.name || ""}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
            />
          </label>
          <label>
            Identifier
            <span className="field-hint">Used by --preset</span>
            <input
              placeholder="review"
              required
              readOnly={existing}
              value={form.id || ""}
              onChange={(event) => setForm({ ...form, id: event.target.value })}
            />
          </label>
        </div>
        <label>
          Description
          <span className="field-hint">One line telling when to use this preset</span>
          <input
            value={form.description || ""}
            onChange={(event) => setForm({ ...form, description: event.target.value })}
          />
        </label>
        <label>
          Prompt
          <span className="field-hint">The explaining part of the system prompt</span>
          <textarea
            rows="12"
            className="mono text-xs"
            required
            value={form.prompt || ""}
            onChange={(event) => setForm({ ...form, prompt: event.target.value })}
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
