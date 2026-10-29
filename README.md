# cdx

A command-line agent: one call, one request, one answer.

## Live model checks

Integration tests hit the real API and are skipped by default: without a key
they do nothing.

```bash
CDX_LIVE_API_KEY=<key> go test -tags live -v ./test/live/
```

Three models of the `inference.dahl.global` provider are covered: MiniMax,
DeepSeek and GLM. For each one the tests check an answer, a tool call, the
refusal of an unknown tool and token usage. Separately they check the write
refusal without permission, context compaction and the token estimate against
the real count.

## Build

```bash
make            # clean, interface, binary
make build      # interface and binary without cleaning
make bin        # binary only
make test       # tests
make vet        # code check
make clean      # remove bin and assets
```

Modules are cached in `.cache` inside the project, because the system cache
may be read-only.

## Install

Build as a normal user first, then install with elevated rights:

```bash
make                # build
sudo make install   # install into /usr/local/bin and /etc/systemd/system
```

The split is required: a build under root would leave root-owned files in
`bin`, `.cache` and `node_modules`. So `install` never builds, it needs a
ready binary and fails with a hint when there is none. A build under root is
additionally refused and can be forced with `ALLOW_ROOT_BUILD=1`.

Install without root:

```bash
make
make install PREFIX=$HOME/.local SUDO=
```

The unit goes into `/etc/systemd/system` with `PREFIX=/usr/local` and into
`$PREFIX/lib/systemd/system` with any other prefix.

```bash
sudo make uninstall   # remove the binary and the unit
```

`uninstall` leaves user data alone: settings and history stay in
`~/.config/cdx` and `~/.local/share/cdx`.

Variables: `PREFIX`, `BINDIR`, `UNIT_DIR`, `DESTDIR` for packaging, `SUDO` to
override the privilege command.

## Usage

The argument scheme is the same for every call: flags first, data after. The
first word without a dash starts the request, and everything after it counts
as the request, so quotes are not needed.

```bash
cdx --model glm explain the code in main.go
cdx --json --quiet what is in go.mod
cdx explain @main.go
```

A lone `--` stops flag parsing, so a request may start with a dash.

```bash
cdx -- --json is a flag
cdx --model glm -- explain -v
```

Commands use the same flags:

```bash
cdx --daemon start|stop|status|restart
cdx --session new|end|list|show|use|delete
cdx --plugin list|add|remove
cdx --mcp list|add|remove|restart
cdx --config      # open settings
cdx --doctor      # check the environment
cdx --compact     # compact the history of the active session
cdx --version
cdx --help
```

Request modifiers: `--model`, `--preset`, `--no-session`, `--allow`, `--lang`,
`--json`, `--quiet`, `--no-color`.

While the answer is being written the progress line shows what the model is
thinking: the reasoning text streams in as it is generated, in dim grey. In
`--json` and `--quiet` modes the stream is off, so it does not get in the way
of machine output.

The program can ring a bell when it needs you: `output.bell` in the config or
the "Sound when the answer ends" switch in the General section. It is not only
the answer: the bell calls you whenever the program has stopped and waits for
you, which covers an answer, a failure and a permission request.

The ordinary BEL character is used, and the terminal makes the sound itself.

Frequent ringing is suppressed: no sound when less than two seconds passed
since the start of the program or since the previous sound. An instant answer
or failure therefore stays silent while the person is still at the terminal,
and two reasons in a row (an answer and an immediate permission request) give
one sound.

The bell only sounds when the output goes to a terminal: not into a pipe and
not in `--json`.

The answer is marked up for the terminal: headings, bold text, code, lists and
quotes are printed styled rather than as raw markdown. Styling is enabled only
when the output goes to a terminal: a file and a pipe receive plain text with
no control sequences. The `--no-color` flag turns styling off regardless.

The daemon starts automatically. A separate daemon binary is not needed:
`cdx --daemon start` launches the hidden `--daemon serve` command in the
background, while `cdx --daemon start --foreground` runs in the current
process for systemd.

The spawned process gets `CDX_NO_AUTOSTART=1` and cannot start a daemon
itself: a mistake in the launch arguments would otherwise turn into an endless
chain of daemons.

## Autostart through systemd

Installing the service for a user:

```bash
sudo cp contrib/cdx@.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now cdx@$USER
```

`--foreground` does not detach the process from the terminal, so systemd sees
it correctly and can restart it.

## Models

Models are configured only in the web interface: `cdx --config`.

The structure has two levels. A provider comes first and stands for the
connection: address, protocol and API key. Models are then added inside the
provider: they differ only by their identifier at the provider, the context
window and the output limit.

An example for the Dahl provider: the address
`https://inference.dahl.global/v1`, the key from `DAHL_KEY`, the protocol
`openai-chat`. Its models:

| Name | Identifier at the provider | Context |
|---|---|---|
| minimax | MiniMaxAI/MiniMax-M2.7 | 180000 |
| deepseek | deepseek-ai/DeepSeek-V4-Flash-0731 | 400000 |
| glm | zai-org/GLM-5.3-Flash | 400000 |

The default model is marked in its card: there is one for the whole program.
Without a mark, a single model in the program is used.

Choosing a model for one call:

```bash
cdx --model glm "check"
```

Keys are kept in an encrypted file and never returned through the API: the
interface only shows whether a key is set.

The "Fetch models" button asks the provider for its model list (`GET /models`)
and shows it in a window: entries can be ticked, with a "Select all" option.
Models already configured are marked as added and are not added twice.

The context window and the output limit come from the model only: the program
has no global values. When the list is fetched they are filled from the
provider answer; many gateways (Dahl among them) return only the identifier,
so the fields stay empty.

An empty field means "not set":

- **empty context window** - the history is not compacted ahead of time, the
  request goes as is. If the provider reports an overflow, the program shows
  it and offers a way out;
- **empty output limit** - the parameter is not sent, the provider picks the
  limit.

With a window set, the history is compacted as it fills.

On context overflow:

```bash
cdx --compact                  # compact the active session
cdx --compact --session <id>   # compact the named one
```

Or set the context window in the model card, and compaction happens on its own.

## Agent presets

A preset sets the mode: what the model does and how it answers. The prompt has
two parts, and only the first one is edited.

| Part | Set by | Contents |
|---|---|---|
| Explaining | preset | what the agent is responsible for, how it works and answers |
| Dynamic | program | the tool list from plugins and MCP, the environment, permissions |

The user does not edit the dynamic part: otherwise the model would receive a
false picture of its abilities. Without plugins the block says outright that
there are no tools, and the model does not promise file access.

Built-in presets:

| Preset | Purpose |
|---|---|
| `standard` | general assistant: reads files, runs commands, explains what it did |
| `mini` | the answer only: no preamble, no explanation, no retelling of the work |
| `creator` | cdx plugin development; the API reference is generated from code |

```bash
cdx --preset mini "what is 17 times 23"
cdx --preset creator "add a tool that reads .env"
```

The active preset and your own presets live in the "Agent presets" section of
the web interface. A custom preset has an identifier, a name, a description
and the prompt text.

The plugin API reference for the `creator` mode is generated from the program
constants (`internal/agent/plugindoc`), so it cannot drift away from the code.

## Cancelling a request

Ctrl+C cancels the current request. The daemon receives a cancel frame and
stops working on the turn, so the next request does not wait for the queue.

Running requests are visible in the "Sessions" section of the web interface,
including those that run without a session and never reach the history. They
can be cancelled from there with a button.

A model request is limited in time: `agent.llm_timeout_ms`. A stuck provider
does not hold the turn forever.

A looping answer is cut: some models start repeating the same fragment and
never close the stream by themselves. An answer that collapsed into a repeat
is stopped on the program side.

## Sessions

There is no history by default: every call stands alone. A session is created
explicitly and bound to a directory.

```bash
cdx --session new        # create a session in the current directory
cdx "question"           # the call continues the active session
cdx --session list       # sessions of the directory
cdx --session show       # history of the active session
cdx --session use <id>   # switch the active one
cdx --session end        # clear the active one
cdx --session delete [id] # delete a session with its history
cdx --no-session "..."   # a one-off call that bypasses the session
```

The active session is inherited by subdirectories, like `.git`: in `src/deep`
without its own session the call continues the session of the project root.

The context files `agent.md` and `skills.md` are read from the session root and
from the current directory.

## Web interface

```bash
cdx --config   # prints the address with a token and opens the browser
```

The React interface is built by Vite and embedded into the binary: there is no
separate static server.

```bash
make web       # build the interface into internal/webassets/dist
make build     # interface and binary
make dev       # interface development, Vite proxies /api to the daemon
```

The menu follows the order of the settings: general first, then connections,
then the service sections.

| Section | What it is for |
|---|---|
| General | daemon state, agent and context settings, language and colour, permissions, dangerous rules, system prompt |
| Models | add a provider with an address and a key, create models inside it, test the connection, mark the default model |
| Sessions | the list and the history of conversations |
| Plugins | install by address, enable, disable, remove |
| MCP | add, edit, remove, restart |
| Logs | the daemon log |

All shared settings sit on one tab: there is no practical reason to spread the
state, the permissions and the prompt over different pages, since they are
changed and checked together.

The command line is kept for frequent operations: `cdx <request>`,
`cdx --config`, `cdx --session`.

Access is by token only: the server embeds it into the served page as
`window.__CDX_TOKEN__`, so the token survives a tab reload and always matches
the current daemon.

## Localisation

Message language: `--lang`, then `output.language` in the config, then the
environment, otherwise English. `en` and `ru` are available.

```bash
cdx --lang ru doctor
```

## Plugins

A plugin is a directory with `manifest.json` and `plugin.js`. Plugin tools are
registered as `plugin.<name>.<tool>` and are available to the agent.

```bash
cdx --plugin list
```

Plugins live in `~/.local/share/cdx/plugins`. Each runs in its own goja VM and
reaches the core through `cdx.*` bindings, only when the capability is declared
in `capabilities`.

## MCP

External MCP servers are described in the config and connected when the daemon
starts:

```yaml
mcp_servers:
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
    enabled: true
```

```bash
cdx --mcp list
cdx --mcp restart filesystem
```

Server tools are registered as `mcp.<server>.<tool>`.

## Tools

The core holds no tools. Only plugins and MCP servers fill the registry: without
them the agent answers in text and cannot read files or run commands. This is
deliberate, so the core stays small and predictable.
