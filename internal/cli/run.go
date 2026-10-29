package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/i18n"
	"github.com/yourok/cdx/internal/ipc"
)

// turnResponse is the daemon's answer to a turn.
type turnResponse struct {
	Result     string       `json:"result"`
	SessionID  string       `json:"session_id"`
	Error      *turnFailure `json:"error"`
	Model      string       `json:"model"`
	DurationMS int64        `json:"duration_ms"`
	Tokens     turnTokens   `json:"tokens"`
	ToolsUsed  []string     `json:"tools_used"`
}

type turnFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

type turnTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// progressEvent is a progress update.
type progressEvent struct {
	Seq     int    `json:"seq"`
	Phase   string `json:"phase"`
	Step    int    `json:"step"`
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Message string `json:"message"`
	Text    string `json:"text"`
}

// startedAt is the program start time, from which the pause before the
// first bell is measured: an instant answer should not ring while the user
// is still looking at the terminal.
var startedAt = time.Now()

// runPrompt sends a request to the agent.

func runPrompt(o *Options, cfg *config.Config) int {
	if o.Prompt == "" {
		fmt.Fprintln(os.Stderr, "request is empty")
		return ExitGeneral
	}

	c, err := Dial(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitDaemon
	}
	defer c.Close()

	interactive := isTerminal(os.Stdout) && !o.Quiet && !o.JSON
	showProgress := interactive && cfg.Output.Spinner

	req := map[string]any{
		"prompt": o.Prompt,
		"cwd":    CWD(),
		"model":  o.Model,
		"preset": o.Preset,
		"allow":  o.Allow,
		"session": map[string]any{
			"mode": o.sessionMode(),
		},
		"output": map[string]any{
			"json":     o.JSON,
			"color":    !o.NoColor && isTerminal(os.Stdout),
			"progress": showProgress,
		},
	}

	// A permission denial changes the exit code even when the model still
	// answers with text.
	permDenied := false

	// Ctrl+C cancels the turn but does not stop the daemon.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT)
	go func() {
		<-sig
		// The cancel frame has to reach the daemon: without the pause the
		// client would close the socket first, and the request would keep
		// running for nothing.
		_ = c.Write(&ipc.Frame{Type: ipc.TypeCancel, ID: "t1"})
		time.Sleep(150 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "interrupted")
		os.Exit(130)
	}()

	if err := c.Write(&ipc.Frame{Type: ipc.TypeReq, ID: "t1", Method: "turn.run", Payload: req}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitDaemon
	}

	bell := newBell(cfg, o)

	if o.NoColor {
		disableColor()
	}

	var spinner *progressLine
	if showProgress {
		// The progress line is dimmed by the same rule as the answer.
		spinner = newProgressLine(!o.NoColor)
		// The closure reads the current value: the spinner is recreated
		// after every permission prompt, and the last one must be stopped.
		defer func() {
			if spinner != nil {
				spinner.Stop()
			}
		}()
	}

	for {
		frame, err := c.Read()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitDaemon
		}

		if frame.Type == ipc.TypeReq && frame.Method == "permission.ask" {
			// The model is calling the user: ring unless they were already
			// looking at the terminal.
			bell.ring()

			// The progress line stops for the question: redrawing it
			// would overwrite the answer being typed.
			if spinner != nil {
				spinner.Stop()
				spinner = nil
			}

			answer := askPermission(frame, cfg)
			if answer == "deny" {
				permDenied = true
			}
			_ = c.Write(ipc.Success(frame.ID, map[string]any{"choice": answer}))

			// Work continues, so the progress line comes back.
			if showProgress {
				spinner = newProgressLine(!o.NoColor)
			}
			continue
		}

		if frame.Type == ipc.TypeEvent {
			if spinner != nil {
				spinner.Update(frame)
			}
			continue
		}

		if frame.Type != ipc.TypeRes || frame.ID != "t1" {
			continue
		}

		if spinner != nil {
			spinner.Stop()
			spinner = nil
		}

		if frame.Error != nil {
			fmt.Fprintln(os.Stderr, frame.Error.Message)
			return codeForError(frame.Error.Code)
		}

		var out turnResponse
		if err := Decode(frame, &out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitGeneral
		}

		if o.JSON {
			emitJSON(out)
		} else if out.Result != "" {
			// Styles are for the terminal only: in a file or a pipe the
			// control sequences would be garbage.
			color := !o.NoColor && isTerminal(os.Stdout)
			fmt.Print(newRenderer(color).Render(out.Result))
		}

		// The bell rings after the answer and only to a terminal: in a
		// pipe the character would end up in the data, and it would corrupt
		// the JSON output.
		bell.ring()

		if out.Error != nil {
			fmt.Fprintln(os.Stderr, out.Error.Message)
			if out.Error.Hint != "" {
				fmt.Fprintln(os.Stderr, out.Error.Hint)
			}
			// An error needs attention just as an answer does.
			bell.ring()
			return codeForError(out.Error.Code)
		}
		if permDenied {
			return ExitPerms
		}
		return ExitOK
	}
}

// emitJSON prints the machine-readable result.
func emitJSON(out turnResponse) {
	type payload struct {
		Result     *string      `json:"result"`
		Error      *turnFailure `json:"error"`
		Model      string       `json:"model"`
		DurationMS int64        `json:"duration_ms"`
		SessionID  *string      `json:"session_id"`
		Tokens     turnTokens   `json:"tokens"`
		ToolsUsed  []string     `json:"tools_used"`
	}
	var sessionID *string
	if out.SessionID != "" {
		sessionID = &out.SessionID
	}
	p := payload{
		Error:      out.Error,
		Model:      out.Model,
		DurationMS: out.DurationMS,
		SessionID:  sessionID,
		Tokens:     out.Tokens,
		ToolsUsed:  out.ToolsUsed,
	}
	if out.Error == nil {
		p.Result = &out.Result
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Println(string(data))
}

// codeForError maps an error code to an exit code.
func codeForError(code string) int {
	switch code {
	case "permission_denied":
		return ExitPerms
	case "config", "context_overflow":
		return ExitConfig
	case "daemon", "connection":
		return ExitDaemon
	}
	return ExitGeneral
}

// askPermission asks for confirmation in the terminal.
func askPermission(f *ipc.Frame, cfg *config.Config) string {
	var req struct {
		Level   string   `json:"level"`
		Tool    string   `json:"tool"`
		Summary string   `json:"summary"`
		Options []string `json:"options"`
	}
	if err := Decode(f, &req); err != nil {
		return "deny"
	}

	// Without a terminal there is nobody to ask, so the config decides.
	if !isTerminal(os.Stdin) {
		if cfg.Perms.NonInteractive == "allow" {
			return "once"
		}
		fmt.Fprintln(os.Stderr, i18n.Tr("error.permission_denied", req.Tool))
		fmt.Fprintln(os.Stderr, i18n.Tr("error.permission_denied.hint", req.Level))
		return "deny"
	}

	// The action is highlighted and the level dimmed: what will happen
	// comes first, the detail second. Colors only in a terminal.
	// A long action is clipped so the question stays on one line, since a
	// wrapped one breaks the prompt.
	summary := clipText(req.Summary, terminalWidth()-30)
	fmt.Fprintf(os.Stderr, "\npermission required: %s %s\n",
		styleAction(summary), styleLevel(req.Level))
	for i, opt := range req.Options {
		fmt.Fprintf(os.Stderr, "  [%d] %s\n", i+1, opt)
	}
	fmt.Fprint(os.Stderr, "choose: ")

	var choice string
	if _, err := fmt.Fscan(os.Stdin, &choice); err != nil {
		return "deny"
	}

	for i, opt := range req.Options {
		if choice == fmt.Sprintf("%d", i+1) || choice == opt {
			return opt
		}
	}
	return "deny"
}
