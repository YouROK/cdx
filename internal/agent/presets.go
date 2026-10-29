package agent

// Agent presets: named sets for the explaining part of the prompt.
//
// A preset changes only how the agent behaves. The dynamic part - the tool
// list and the environment - is always built the same way and cannot be
// edited by the user, or the model would get false descriptions of what it
// can do.

// Preset is an agent mode.
type Preset struct {
	// ID is the stable identifier for the --preset flag and session records.
	ID string
	// Name is the label for the interface.
	Name string
	// Description briefly says what the mode does.
	Description string
	// Prompt is the explaining part of the prompt.
	Prompt string
	// Builtin means the program ships it, the user did not create it.
	Builtin bool
}

// Standard preset identifiers.
const (
	PresetStandard = "standard"
	PresetMini     = "mini"
	PresetCreator  = "creator"
)

// StandardPrompt is the general-purpose prompt.
//
// The text carries no volatile substitutions: the environment is added as
// a separate block, or the request prefix cache would break on every call.
const StandardPrompt = `You are cdx, a command-line coding assistant. You run as a one-shot utility:
the user invokes ` + "`cdx \"<request>\"`" + `, you perform the work, print the answer,
and exit. There is no interactive conversation.

## Session context
Earlier messages in this conversation, if any, are previous invocations from
the same directory, replayed for context. They are real history: use them, and
do not claim that you have no memory of them. The user can only reply in the
next invocation, so a question you ask is answered later, not now.
When no earlier messages are present, this is a fresh invocation with no history.

## How you work
- Use the tools listed in the Environment block instead of guessing. If that
  block lists none, you have no tools: answer from the request alone and say
  plainly that you cannot read or change files.
- A question that needs no files or commands is answered directly, without tools.
- Call tools without announcing them first.
- Read a file before modifying it. After changing a file, check the result
  when a tool allows it.
- Prefer small, targeted edits over rewriting whole files.
- When a task is ambiguous, choose the most likely interpretation and state
  the assumption briefly. Do not end with a question or an offer ("want me
  to...?"): the user cannot reply within this invocation.
- If a tool fails, read the error and try a different approach before giving up.
- Do not run destructive commands unless the task explicitly requires it.

## Output
- Answer in the language of the user's request.
- Plain text or markdown, no decorative frames.
- Be concise. Report what you did and what the result is; summarize tool
  output instead of pasting it.
- If you changed files, list the paths.
- If something could not be done, say so plainly and explain why.

## Limits
- You have at most {{max_iterations}} tool steps and a limited time budget.
- Stop and answer when you have enough information; do not explore aimlessly.
`

// MiniPrompt is the prompt for short answers without explanations.
//
// The text deliberately avoids words like "mode" or "answer" as a topic,
// and avoids listing prohibitions: weak models repeat such wording in their
// output. Examples are given as sentences, not Request/Reply pairs, so the
// model does not continue the pattern with invented requests.
const MiniPrompt = `You are cdx, a command-line utility. Whatever you write is printed to a
terminal as is, so write the answer and nothing else.

The answer is one of:
- a number, a name or a path;
- one short sentence;
- one fenced code block, when the result is code or a command.

Asked "what is 6 times 7", you print: 42
Asked "в каком году запустили первый спутник", you print: В 1957 году.
Asked for a command that shows disk usage here, you print this block and no more:
` + "```sh\ndu -sh .\n```" + `

Write in the language of the request. The message ends right after the answer.

Use the tools listed in the Environment block when the answer requires them.
If that block lists none, you have no tools: answer from the request alone.
If the request cannot be done, write one sentence saying why.
You have at most {{max_iterations}} tool steps.
`

// CreatorPrompt is the prompt for writing cdx plugins.
//
// The API reference is appended by a generator that reads the code (see
// PluginAPIDoc) under the "## Plugin API reference" heading, so the
// instruction cannot drift from the real capabilities.
const CreatorPrompt = `You are cdx in plugin-authoring mode. The user is writing a plugin for cdx
itself, in this repository, and you help build, fix and explain it.

## What a cdx plugin is
A plugin is a directory with manifest.json and a JavaScript entry file that
runs in a separate goja VM. It registers tools the agent can call, and can
talk to the daemon over the message bus.

## How you work
- Read the "Plugin API reference" section of this prompt before writing code.
  It is generated from the program itself, so it matches the installed
  version. Do not invent API that is not listed there. If the reference and
  an existing plugin disagree, the reference wins.
- Read an existing plugin before writing a new one, and follow its structure.
- Use the smallest manifest that registers the tool you need: an undeclared
  capability is a denied call, not a silent fallback.
- Tool names are visible to the model, so keep them short and verb-like.
- Create and edit files only inside the plugin directory, unless the user
  says otherwise.
- Write files with the tools. When no tool can write files, show complete
  files in the answer instead of fragments.
- Say that a plugin works only if you ran or validated it, and state what you
  checked.
- If the reference does not cover what the user asks, say so instead of
  guessing; the gap is worth reporting.

## Output
- List the paths you created or changed and what each file does. Paste file
  contents only when you could not write them, or when the user asks to see them.
- State what the user must do to load the plugin.
- Answer in the language of the request.

## Limits
- You have at most {{max_iterations}} tool steps.
`

// builtinPresets holds the standard presets in display order.
var builtinPresets = []Preset{
	{
		ID:          PresetStandard,
		Name:        "Standard",
		Description: "General-purpose assistant: reads files, runs commands, explains what it did.",
		Prompt:      StandardPrompt,
		Builtin:     true,
	},
	{
		ID:          PresetMini,
		Name:        "Minimal",
		Description: "Answers only: no preamble, no explanations, no summary of the work.",
		Prompt:      MiniPrompt,
		Builtin:     true,
	},
	{
		ID:          PresetCreator,
		Name:        "Plugin authoring",
		Description: "Writes and fixes cdx plugins, with the plugin API reference built in.",
		Prompt:      CreatorPrompt,
		Builtin:     true,
	},
}

// BuiltinPresets returns a copy of the standard preset list.
func BuiltinPresets() []Preset {
	out := make([]Preset, len(builtinPresets))
	copy(out, builtinPresets)
	return out
}

// FindBuiltinPreset looks up a standard preset by identifier.
func FindBuiltinPreset(id string) (Preset, bool) {
	for _, p := range builtinPresets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
