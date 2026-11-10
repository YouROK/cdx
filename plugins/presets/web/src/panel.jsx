import { useEffect, useState } from "react";

export function mount(container, api) {
  return api.render(<PresetsPanel api={api} />);
}

function Modal({ title, wide, onClose, children, t }) {
  useEffect(() => {
    function onKey(event) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="modal-backdrop"
      onClick={(event) => {
        if (event.target.classList.contains("modal-backdrop")) onClose();
      }}
    >
      <div className={"modal-box" + (wide ? " modal-wide" : "")} role="dialog" aria-modal="true" aria-label={title}>
        <div className="modal-header">
          <h4>{title}</h4>
          <button type="button" className="btn-icon" aria-label={t("webui.Action.Close")} onClick={onClose}>
            &times;
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  );
}

function PresetsPanel({ api }) {
  const t = api.t;
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
      api.toast(t("presets.Activated", { id: id }));
      load();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  async function remove(id) {
    const ok = await api.confirm(t("presets.DeleteTitle"), t("presets.DeleteConfirm", { id: id }));
    if (!ok) return;
    try {
      await api.fetchJSON("/api/plugins/presets/custom/" + encodeURIComponent(id), { method: "DELETE" });
      api.toast(t("presets.Deleted"));
      load();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <div>
      <div className="row-between mb-md">
        <p className="page-intro m-0">{t("presets.Intro")}</p>
        <button type="button" className="btn btn-primary" onClick={() => setModal({ type: "edit" })}>
          {t("presets.Create")}
        </button>
      </div>

      <div className="stack-md">
        <div>
          <div className="section-caption mb-xs">{t("presets.BuiltinSection")}</div>
          <div className="grid-3">
            {builtin.map((preset) => (
              <PresetTile
                key={preset.id}
                preset={preset}
                onActivate={() => activate(preset.id)}
                onView={() => setModal({ type: "view", preset })}
                onEdit={() => setModal({ type: "edit", preset })}
                onDelete={() => remove(preset.id)}
                t={t}
              />
            ))}
          </div>
        </div>
        {custom.length > 0 ? (
          <div>
            <div className="section-caption mb-xs">{t("presets.Yours")}</div>
            <div className="grid-3">
              {custom.map((preset) => (
                <PresetTile
                  key={preset.id}
                  preset={preset}
                  onActivate={() => activate(preset.id)}
                  onView={() => setModal({ type: "view", preset })}
                  onEdit={() => setModal({ type: "edit", preset })}
                  onDelete={() => remove(preset.id)}
                  t={t}
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
        <Modal title={t("presets.PromptTitle", { name: modal.preset.name || modal.preset.id })} onClose={() => setModal(null)} t={t}>
          <p className="field-hint mb-xs">{modal.preset.builtin ? t("presets.BuiltinReadonly") : t("presets.Custom")}</p>
          <pre className="code-block">{modal.preset.prompt || ""}</pre>
          <div className="modal-actions mt-md">
            <button type="button" className="btn btn-default" onClick={() => setModal(null)}>
              {t("webui.Action.Close")}
            </button>
          </div>
        </Modal>
      ) : null}
    </div>
  );
}

function PresetTile({ preset, onActivate, onView, onEdit, onDelete, t }) {
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
          {preset.active ? <span className="badge badge-accent">{t("presets.Active")}</span> : null}
        </div>
        <div className="mono text-xs muted mt-2">{preset.id}</div>
      </div>
      <div className="text-sm" style={{ flex: 1 }}>
        {preset.description || "-"}
      </div>
      <div className="row-gap-xs">
        <button type="button" className="btn btn-light btn-xs" disabled={preset.active} onClick={onActivate}>
          {t("presets.Use")}
        </button>
        <button type="button" className="btn btn-default btn-xs" onClick={onView}>
          {t("presets.ShowPrompt")}
        </button>
        {!preset.builtin ? (
          <>
            <button type="button" className="btn btn-default btn-xs" onClick={onEdit}>
              {t("presets.Edit")}
            </button>
            <button type="button" className="btn btn-danger btn-xs" onClick={onDelete}>
              {t("presets.Delete")}
            </button>
          </>
        ) : null}
      </div>
    </div>
  );
}

function PresetModal({ api, preset, onClose, onSaved }) {
  const t = api.t;
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
      api.toast(t("presets.Saved"));
      onSaved();
    } catch (err) {
      api.toast(String(err.message || err), false);
    }
  }

  return (
    <Modal title={existing ? t("presets.EditTitle", { name: preset.name }) : t("presets.New")} wide onClose={onClose} t={t}>
      <form className="form-stack" onSubmit={submit}>
        <div className="grid-2">
          <label>
            {t("presets.Name")}
            <span className="field-hint">{t("presets.NameHint")}</span>
            <input
              placeholder="Review"
              required
              value={form.name || ""}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
            />
          </label>
          <label>
            {t("presets.Identifier")}
            <span className="field-hint">{t("presets.IdentifierHint")}</span>
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
          {t("presets.FormDescription")}
          <span className="field-hint">{t("presets.DescriptionHint")}</span>
          <input
            value={form.description || ""}
            onChange={(event) => setForm({ ...form, description: event.target.value })}
          />
        </label>
        <label>
          {t("presets.Prompt")}
          <span className="field-hint">{t("presets.PromptHint")}</span>
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
            {t("webui.Action.Cancel")}
          </button>
          <button type="submit" className="btn btn-primary">
            {t("presets.Save")}
          </button>
        </div>
      </form>
    </Modal>
  );
}
