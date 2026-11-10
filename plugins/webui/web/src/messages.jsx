import { memo } from "react";
import { useUI } from "./ui.jsx";

// MessageRow renders one stored conversation message.
export const MessageRow = memo(function MessageRow({ message, session }) {
  const { t } = useUI();
  const role = message.role;
  const toolCalls = message.tool_calls || [];
  const content = String(message.content || "");
  const reasoning = String(message.reasoning || "");
  if (!content.trim() && !reasoning.trim() && toolCalls.length === 0) return null;

  const roleKey = "webui.Role." + role.charAt(0).toUpperCase() + role.slice(1);
  const roleText = t(roleKey);
  let roleLabel = roleText === roleKey ? role : roleText;
  if (role === "assistant") {
    let model = message.model || session.model || "";
    if (message.provider && model && model.indexOf("/") === -1) model = message.provider + "/" + model;
    if (model) roleLabel = t("webui.Role.Assistant") + " (" + model + ")";
  } else if (role === "tool" && message.name) {
    roleLabel = t("webui.Role.Tool") + ": " + message.name;
  }
  const badgeClass = role === "assistant" ? "badge-teal" : role === "user" ? "badge-accent" : "badge-warn";

  return (
    <div className="panel">
      <div className="row-between mb-xs">
        <span className={"badge " + badgeClass}>{roleLabel}</span>
        {message.tokens_in || message.tokens_out ? (
          <span className="text-xs muted">
            {t("webui.Messages.Tokens", { input: message.tokens_in || 0, output: message.tokens_out || 0 })}
          </span>
        ) : null}
      </div>
      {content.trim() ? <pre className="code-block">{content}</pre> : null}
      {reasoning.trim() ? (
        <details className="reasoning-block" open={!content.trim()}>
          <summary>{t("webui.Live.Reasoning")}</summary>
          <pre className="code-block">{reasoning}</pre>
        </details>
      ) : null}
      {role === "assistant" && toolCalls.length > 0 ? (
        <div className="tool-calls">
          {toolCalls.map((call, index) => {
            let args = "";
            try {
              args = JSON.stringify(call.arguments || {});
            } catch {
              args = "";
            }
            if (args.length > 240) args = args.slice(0, 240) + "...";
            return (
              <div key={index} className="mono text-xs">
                {call.name} {args}
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
});
