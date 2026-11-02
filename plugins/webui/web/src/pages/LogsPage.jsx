import { useEffect, useState } from "react";
import { apiFetch } from "../api.js";
import { useUI } from "../ui.jsx";

export function LogsPage() {
  const { toast } = useUI();
  const [lines, setLines] = useState([]);
  const [loading, setLoading] = useState(true);

  async function loadLogs() {
    setLoading(true);
    try {
      const data = await apiFetch("/api/core/logs");
      setLines(data.lines || []);
    } catch (err) {
      toast(String(err.message || err), false);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    loadLogs();
  }, []);

  return (
    <div>
      <div className="page-header">
        <h3>Logs</h3>
        <button type="button" className="btn btn-default btn-xs" onClick={loadLogs}>
          Refresh
        </button>
      </div>
      {loading ? (
        <p className="muted">Loading logs...</p>
      ) : lines.length === 0 ? (
        <div className="panel">
          <p className="muted m-0">Log is empty.</p>
        </div>
      ) : (
        <div className="panel panel-flush">
          <div className="panel-pad text-xs muted panel-divider">Lines: {lines.length}</div>
          <pre className="code-block log-block">{lines.join("\n")}</pre>
        </div>
      )}
    </div>
  );
}
