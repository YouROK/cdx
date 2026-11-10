import { createContext, useCallback, useContext, useEffect, useState } from "react";

const UIContext = createContext(null);
const identity = (key) => key;

export function UIProvider({ children, t = identity, locale = "en" }) {
  const [toasts, setToasts] = useState([]);
  const [confirmState, setConfirmState] = useState(null);

  const toast = useCallback((message, ok = true) => {
    const id = Math.random().toString(36).slice(2);
    setToasts((list) => [...list, { id, message: String(message || ""), ok }]);
    setTimeout(() => {
      setToasts((list) => list.filter((item) => item.id !== id));
    }, 3200);
  }, []);

  const confirm = useCallback((title, message) => {
    return new Promise((resolve) => {
      setConfirmState({ title, message, resolve });
    });
  }, []);

  const settle = useCallback((value) => {
    setConfirmState((current) => {
      if (current) current.resolve(value);
      return null;
    });
  }, []);

  useEffect(() => {
    if (!confirmState) return;
    const onKey = (event) => {
      if (event.key === "Escape") settle(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [confirmState, settle]);

  return (
    <UIContext.Provider value={{ toast, confirm, t, locale }}>
      {children}
      <div className="toast-container">
        {toasts.map((item) => (
          <div key={item.id} className={item.ok ? "toast" : "toast err"}>
            {item.message}
          </div>
        ))}
      </div>
      {confirmState ? (
        <div
          className="modal-backdrop"
          onClick={(event) => {
            if (event.target.classList.contains("modal-backdrop")) settle(false);
          }}
        >
          <div className="modal-box confirm-box" role="dialog" aria-modal="true" aria-label={confirmState.title}>
            <div className="modal-header">
              <h4>{confirmState.title}</h4>
              <button type="button" className="btn-icon" aria-label={t("webui.Action.Close")} onClick={() => settle(false)}>
                &times;
              </button>
            </div>
            <div className="form-stack">
              <div className="text-sm">{confirmState.message}</div>
              <div className="modal-actions">
                <button type="button" className="btn btn-default" onClick={() => settle(false)}>
                  {t("webui.Action.Cancel")}
                </button>
                <button type="button" className="btn btn-danger" onClick={() => settle(true)}>
                  {t("webui.Action.Confirm")}
                </button>
              </div>
            </div>
          </div>
        </div>
      ) : null}
    </UIContext.Provider>
  );
}

export function useUI() {
  const value = useContext(UIContext);
  if (!value) throw new Error("UI context is not available");
  return value;
}
