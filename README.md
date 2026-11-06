# cdx

A command-line coding agent with a microkernel architecture: the core provides
the IPC transport, storage, the HTTP router, the plugin runtime, and the session
loop; every feature - LLM providers, tools, permissions, context assembly, and
the web dashboard - is a plugin.

The core never embeds plugins: the default ones live in `plugins/` and are
installed next to the binary by `make install`.

## Requirements

- Go 1.27 or newer.
- Node.js 22 or newer with npm, for building the web interface and plugin panels.

## Build

```bash
make            # interface, plugin panels, binary
make build      # same without cleaning
make test       # go test ./...
make vet        # go vet ./...
npm run build   # interface and panels only
npm test        # frontend unit tests
```

Modules and build caches are kept in `.cache/` inside the project, because the
system cache may be read-only.

## Install

Build as a normal user first, then install with elevated rights:

```bash
make
sudo make install
```

The binary goes to `/usr/bin/cdx`, the plugins to
`/usr/share/cdx/plugins`. User data stays in `~/.local/share/cdx`,
`~/.config/cdx`, and `~/.local/state/cdx`.

## Releases

Pushing any tag makes GitHub Actions test the project and publish a release. The
tag becomes the version: a leading `v` is dropped, and characters that package
formats do not accept are replaced with dots. There are assets for `i386`,
`x86_64`, `armv7`, and `aarch64`:

* `.deb` for Debian and Ubuntu;
* `.rpm` for Fedora, RHEL, and openSUSE;
* `.apk` for Alpine;
* `cdx_<version>_linux_<arch>.zip` with the binary, the default plugins, and the
  `install.sh` and `uninstall.sh` scripts;
* `SHA256SUMS` with the checksums of every asset.

The package architecture names follow the convention of each format:

| Machine | deb | rpm | apk | zip |
|---|---|---|---|---|
| x86 | i386 | i386 | x86 | i386 |
| x86_64 | amd64 | x86_64 | x86_64 | x86_64 |
| armv7 | armhf | armv7hl | armv7 | armv7 |
| arm64 | arm64 | aarch64 | aarch64 | aarch64 |

Install a package with the distribution tool, for example on x86_64:

```bash
sudo apt install ./cdx_<version>_amd64.deb
sudo dnf install ./cdx-<version>-1.x86_64.rpm
sudo apk add --allow-untrusted ./cdx_<version>_x86_64.apk
```

The Alpine package is unsigned, hence `--allow-untrusted`.

The zip installs into the same locations as `make install`:

```bash
unzip cdx_<version>_linux_x86_64.zip -d cdx
sudo ./cdx/install.sh          # /usr/bin/cdx and /usr/share/cdx/plugins
sudo ./cdx/uninstall.sh        # keeps the user data
```

`install.sh` and `uninstall.sh` accept `--prefix`, `--bindir`, and
`--plugins-dir`. With a prefix other than `/usr`, start `cdx` with
`CDX_SYSTEM_PLUGIN_DIR` pointing at the plugin directory.

## Use

```bash
cdx daemon run --http 127.0.0.1:8787   # start the background daemon
cdx daemon status                     # state, plugins, HTTP address
cdx daemon stop
cdx "explain what this package does"  # one-shot request in the current directory
cdx -f task.md                        # take the prompt from a file
cdx session list                      # sessions bound to directories
cdx plugins list                      # installed plugins and their tools
```

## Web interface

Open `http://127.0.0.1:8787` while the daemon is running. The dashboard renders
declarative JSON Schema panels and custom plugin page panels, and it contains
the pages for the core (general settings, sessions, plugins, logs).

### Access token

The `auth` plugin owns the token:

* it generates a random token on first start and stores it encrypted in
  `cdx.db` (`cdx.secrets`), so it survives restarts;
* it registers middleware on `/api` and answers `401` unless the
  `X-CDX-Token` header matches;
* it rejects requests whose `Host` header is not local (`127.0.0.1`,
  `localhost`, `::1`), which blocks DNS-rebinding attacks;
* `GET /` gets the token embedded by the `webui` plugin, so the page works
  without a token in the URL and only same-origin scripts can read it.

The `API access` page has a single setting: `Allow non-local host header`. It
is meant for a reverse proxy or an SSH tunnel; the token is still required.

If the page reports "Access denied", reload it - the page was loaded before the
token changed. If the `auth` plugin is disabled or removed, the API is open to
anyone who can reach the port, which is why it is enabled by default and loads
first (`order: 1`).

### Interface development

The daemon reads `web/dist/index.html` on every request and serves only the
built assets, so rebuilding is enough - a page reload picks up the new bundles:

```bash
npm run dev                                   # rebuilds on change
CDX_SYSTEM_PLUGIN_DIR="$PWD/plugins" ./bin/cdx daemon run --http 127.0.0.1:8787
```

`CDX_SYSTEM_PLUGIN_DIR` points the daemon at the repository plugins, so edits in
`plugins/*/web/src` reach the running instance without reinstalling.

## Permission prompts

Before an edit, a shell command, or any action that leaves the session directory,
the agent asks for confirmation. The prompt shows the full action (command, file,
or folder, wrapped to the terminal width), the working folder, the outside paths,
and the offered answers. Enter picks the default: `once` for permissions and
`continue` for the step-budget question. The Permissions page controls the
defaults: read, edit, and dangerous tools each have `allow`, `ask`, or `deny`,
and a separate option decides what happens outside the working folder.

That outside-folder option is not a sandbox: commands, symlinks, and absolute
paths can still leave the folder. Use an isolated environment when a hard limit
is required.

A turn is limited to 40 tool steps by default; the General page sets the budget
from 1 to 200 and chooses what happens at the limit: `No step limit` runs the
turn to its answer, `Ask to continue` offers another equal budget, and
`Stop the turn` ends it at the budget.

While a question waits in the terminal, that plugin is busy: opening its own
settings page shows "Waiting for the plugin" and refreshes by itself once the
answer is given. Other pages keep working.

## Plugins

| Plugin | Role |
|---|---|
| `auth` | API token and local-host check |
| `providers` | LLM connections, encrypted keys, models, streaming completions |
| `presets` | Built-in and custom agent presets |
| `permissions` | Tool call policies, outside-folder policy, remembered grants, approval prompts |
| `context` | System prompt, `AGENTS.md`, history compaction |
| `tools-local` | Filesystem and shell tools |
| `webui` | Dashboard shell: navigation, schema forms, page panels |

`PLUGINS.md` documents the manifest, the `cdx` host API, and both kinds of
panels. `ARCHITECTURE.md` describes the core subsystems, `ROADMAP.md` tracks
the implementation stages.

## Layout

```text
cmd/cdx/            entry point
internal/           core: bus, ipc, router, plugin runtime, session loop, storage
packaging/          nfpm config and the install/uninstall scripts for releases
plugins/            default plugins, each with an optional React panel in web/
scripts/dev.mjs     watch build for the interface and panels
scripts/package.sh  release artifacts for one architecture: zip, deb, rpm, apk
test/live/          integration tests against real models (CDX_LIVE_API_KEY)
```

## Live model checks

Integration tests call the real API and are skipped without a key:

```bash
CDX_LIVE_API_KEY=<key> go test -tags live -v ./test/live/
```

`TestLiveCreatorBuildsPanelPlugin` runs the creator preset against the real
model: it asks for a minimal plugin with one tool and one panel, then loads the
result and checks the tool, the panel entry, and the served panel file. It uses
`CDX_LIVE_BASE_URL` (default `https://opencode.ai/zen/go/v1`) and
`CDX_LIVE_MODEL` (default `deepseek-v4.1-flash`), and stops the turn after 14
tool steps so the run stays short.
