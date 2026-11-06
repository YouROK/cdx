import { useCallback, useEffect, useRef, useState } from "react";
import { apiFetch } from "../api.js";
import { MessageRow } from "../messages.jsx";
import { EARLIER_PAGE_SIZE, newestSlice, nextVisibleCount, RECENT_MESSAGE_COUNT, splitHistory } from "../sessionView.js";
import { useUI } from "../ui.jsx";

const POLL_MS = 800;

// LiveSessionPage follows the turn running in a session: the current model
// steps at the bottom and a few stored messages above them.
export function LiveSessionPage({ sessionId, onBack, onOpenHistory }) {
  const { toast } = useUI();
  const [detail, setDetail] = useState(null);
  const [live, setLive] = useState(null);
  const [loading, setLoading] = useState(true);
  const [offline, setOffline] = useState(false);
  const [visibleCount, setVisibleCount] = useState(RECENT_MESSAGE_COUNT);
  const bottomRef = useRef(null);
  const stickRef = useRef(true);

  const loadDetail = useCallback(async () => {
    const next = await apiFetch("/api/core/sessions/" + encodeURIComponent(sessionId));
    setDetail(next);
    return next;
  }, [sessionId]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setDetail(null);
    setLive(null);
    setVisibleCount(RECENT_MESSAGE_COUNT);
    stickRef.current = true;
    loadDetail()
      .catch((err) => {
        if (!alive) return;
        toast(String(err.message || err), false);
        onBack();
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [sessionId]);

  useEffect(() => {
    let alive = true;
    let timer = null;
    let wasRunning = false;
    let idleKey = "";

    async function tick() {
      try {
        const next = await apiFetch("/api/core/sessions/" + encodeURIComponent(sessionId) + "/live");
        if (!alive) return;
        setOffline(false);
        const running = !!next.running;
        if (running) {
          idleKey = "";
          setLive(next);
        } else {
          const key = [next.finished_at || 0, next.stopped_reason || "", next.error || ""].join("|");
          if (key !== idleKey) {
            idleKey = key;
            setLive(next);
          }
        }
        if (running !== wasRunning) {
          wasRunning = running;
          loadDetail().catch(() => {});
        }
      } catch {
        if (alive) setOffline(true);
      } finally {
        if (alive) timer = setTimeout(tick, POLL_MS);
      }
    }

    tick();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [sessionId]);

  useEffect(() => {
    function onScroll() {
      const doc = document.documentElement;
      stickRef.current = window.innerHeight + window.scrollY >= doc.scrollHeight - 120;
    }
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  useEffect(() => {
    if (!live?.running) return;
    if (stickRef.current) bottomRef.current?.scrollIntoView({ block: "end" });
  }, [live]);

  if (!detail) {
    return (
      <div>
        <div className="page-header">
          <h3>
            Live session: <span className="mono">{sessionId}</span>
          </h3>
          <button type="button" className="btn btn-default btn-xs" onClick={onBack}>
            &larr; Back to sessions
          </button>
        </div>
        <p className="muted">{loading ? "Loading session..." : "Session was not found."}</p>
      </div>
    );
  }

  const session = detail.session || {};
  const messages = detail.messages || [];
  const running = !!live?.running;
  const split = splitHistory(messages, running ? live.history_count : null);
  const shown = newestSlice(split.earlier, visibleCount);
  const hidden = Math.max(0, split.earlier.length - shown.length);

  function showEarlier() {
    stickRef.current = false;
    setVisibleCount((count) => nextVisibleCount(count, split.earlier.length));
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h3>
            Live session: <span className="mono">{session.id || sessionId}</span>
          </h3>
          <div className="mono text-xs muted">{session.workdir || ""}</div>
        </div>
        <div className="row-gap-xs">
          <LiveStatus running={running} offline={offline} />
          <button type="button" className="btn btn-default btn-xs" onClick={onOpenHistory}>
            Full history
          </button>
          <button type="button" className="btn btn-default btn-xs" onClick={onBack}>
            &larr; Back to sessions
          </button>
        </div>
      </div>

      <p className="page-intro">
        {running
          ? "The running turn updates here every second. Only the newest messages are shown; open the full history for everything else."
          : "No turn is running. The newest messages are shown; open the full history for everything else."}
      </p>

      {split.earlier.length === 0 ? (
        <p className="muted">{running ? "No messages before this turn." : "No messages yet."}</p>
      ) : (
        <div className="live-section-head">
          <h4>{running ? "Earlier messages" : "Recent messages"}</h4>
          <span className="text-xs muted">
            {split.earlier.length} stored
            {hidden > 0 ? " · " + hidden + " hidden" : ""}
          </span>
        </div>
      )}

      {hidden > 0 ? (
        <button type="button" className="btn btn-default btn-xs mb-xs" onClick={showEarlier}>
          Show {Math.min(hidden, EARLIER_PAGE_SIZE)} earlier {hidden === 1 ? "message" : "messages"}
        </button>
      ) : null}

      <div className="stack-xs">
        {shown.map((message, index) => (
          <MessageRow key={index} message={message} session={session} />
        ))}
      </div>

      {running ? <LiveTurnCard live={live} /> : null}

      {!running && live?.error ? (
        <div className="panel error-panel mt-md">
          <h4 className="m-0 mb-xs">The last turn failed</h4>
          <p className="muted m-0">{live.error}</p>
        </div>
      ) : null}

      {!running && live?.finished_at ? (
        <p className="muted text-xs mt-md">
          Last turn {live.error ? "failed" : "finished"} at{" "}
          {new Date(live.finished_at * 1000).toLocaleTimeString()}
          {live.stopped_reason ? " · " + live.stopped_reason : ""}
        </p>
      ) : null}

      <div ref={bottomRef} className="live-bottom" />
    </div>
  );
}

function LiveStatus({ running, offline }) {
  if (offline) return <span className="badge badge-danger">offline</span>;
  if (running) {
    return (
      <span className="badge badge-teal live-badge">
        <span className="live-dot" />
        running
      </span>
    );
  }
  return <span className="badge badge-outline">idle</span>;
}

function LiveTurnCard({ live }) {
  const steps = live.steps || [];
  const started = live.started_at ? new Date(live.started_at * 1000).toLocaleTimeString() : "";
  return (
    <div className="panel live-turn mt-md">
      <div className="row-between mb-xs">
        <span className="badge badge-teal live-badge">
          <span className="live-dot" />
          running
        </span>
        <span className="text-xs muted">
          started {started}
          {live.model ? " · " + live.model : ""}
        </span>
      </div>
      {live.prompt ? (
        <div className="live-prompt">
          <span className="badge badge-accent">user</span>
          <pre className="code-block">{live.prompt}</pre>
        </div>
      ) : null}
      {steps.length === 0 ? <p className="muted text-sm">Waiting for the model...</p> : null}
      {steps.map((step) => (
        <LiveStepBlock key={step.index} step={step} />
      ))}
    </div>
  );
}

function LiveStepBlock({ step }) {
  const tools = step.tools || [];
  const text = String(step.text || "");
  const reasoning = String(step.reasoning || "");
  return (
    <div className="live-step">
      <div className="live-step-head">
        <span className="badge badge-outline">step {step.index}</span>
        {tools.some((tool) => tool.running) ? <span className="live-dot" /> : null}
      </div>
      {reasoning.trim() ? (
        <details className="reasoning-block" open>
          <summary>Reasoning</summary>
          <pre className="code-block">{reasoning}</pre>
        </details>
      ) : null}
      {text.trim() ? <pre className="code-block">{text}</pre> : null}
      {tools.map((tool, index) => (
        <LiveToolRow key={index} tool={tool} />
      ))}
    </div>
  );
}

function LiveToolRow({ tool }) {
  let args = "";
  try {
    args = JSON.stringify(tool.args || {});
  } catch {
    args = "";
  }
  if (args.length > 240) args = args.slice(0, 240) + "...";
  return (
    <div className="live-tool">
      <div className="row-between">
        <span className="mono text-xs">
          {tool.name} {args}
        </span>
        <span className={tool.running ? "badge badge-accent" : "badge badge-teal"}>
          {tool.running ? "running" : "done"}
        </span>
      </div>
      {tool.output ? (
        <details>
          <summary>Output</summary>
          <pre className="code-block">{tool.output}</pre>
        </details>
      ) : null}
    </div>
  );
}
