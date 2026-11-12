package plugin

import (
	"fmt"
	"reflect"
	"strings"
)

// DocEntry describes a single plugin API signature or file requirement.
type DocEntry struct {
	Signature string `json:"signature"`
	Summary   string `json:"summary"`
}

// DocFileNames lists the files a cdx plugin consists of.
var DocFileNames = []DocEntry{
	{Signature: "manifest.json", Summary: "Declares the plugin identity and entry script"},
	{Signature: "<entry>.js", Summary: "JavaScript entry loaded into an isolated Goja VM; defines function setup(cdx) { ... }"},
}

// DocCDXObject lists the APIs available on the cdx object inside a plugin VM.
var DocCDXObject = []DocEntry{
	{Signature: "cdx.id / cdx.dir / cdx.workdir()", Summary: "Plugin ID, absolute directory path, and the active session working directory"},
	{Signature: "cdx.after(ms, fn) / cdx.yield() / cdx.sleep(ms)", Summary: "Run a callback after a delay; yield returns a promise resolved on the next event-loop turn, and sleep returns a promise resolved after the delay"},
	{Signature: "cdx.apiDoc()", Summary: "Returns the live Markdown Plugin API Reference generated from the running binary"},
	{Signature: "cdx.log.debug|info|warn|error(msg)", Summary: "Write leveled log messages tagged with [Plugin:<id>]"},
	{Signature: "cdx.storage.get|set|delete|keys|list()", Summary: "Isolated per-plugin plain JSON key-value store in BoltDB"},
	{Signature: "cdx.secrets.get|set|delete|has|keys|masked()", Summary: "Isolated per-plugin AES-256-GCM encrypted store in BoltDB for API keys and secrets"},
	{Signature: "cdx.settings.register(schema) / save(values)", Summary: "Register a JSON Schema for declarative settings; format: 'secret' fields are routed to cdx.secrets and exposed through the plugin settings endpoint"},
	{Signature: "cdx.tools.register(spec, handler) / remove(name) / list() / call(name, args, opts?)", Summary: "Register an LLM tool ({ name, description, permission, input_schema }, function(args) {}), remove one by name, list them, or run one through the core pipeline (before hook, approval, handler, after hook) and get { output, error }; call() returns a promise and the handler may be async"},
	{Signature: "cdx.providers.register({ listModels, complete }) / listModels()", Summary: "Register as an LLM provider; listModels returns available models, complete(req, emit) runs turns; listModels may be sync or async, complete may be async and awaited by the core"},
	{Signature: "cdx.hooks.on(hookName, priority, handler) / run(hookName, payload)", Summary: "Intercept lifecycle pipelines: session:before_turn, tool:before_call, tool:decided, tool:after_call"},
	{Signature: "cdx.bus.publish|subscribe|handle|request(topic, payload)", Summary: "Communicate across plugins and core via events and 1-to-1 RPC"},
	{Signature: "plugin:enabled / plugin:disabled / plugin:removed / plugin:installed / plugins:reloaded", Summary: "Core lifecycle events on the bus; payloads carry the plugin id, name, version, scope, order, directory, and state"},
	{Signature: "cdx.bus.request(\"i18n:register\", { id: cdx.id, tables: { en: {...}, ru: {...} } })", Summary: "Register or replace the plugin translation tables; call it once from setup(cdx)"},
	{Signature: "cdx.bus.request(\"i18n:catalog\", {})", Summary: "Return the merged catalog { locale, revision, strings } for the selected language; cache it and fall back to English text when a key is missing"},
	{Signature: "cdx.questions.ask(question)", Summary: "Ask the active client a question ({ kind, title, body, details, options, default, text }) and return a promise for { choice, text, cancelled, reason }; await it from an async handler or hook"},
	{Signature: "cdx.tasks.start({ prompt, workdir, preset, model, max_steps, timeout_s, allowed_tools, owner }) / get(id) / list() / answer(id, question_id, answer) / cancel(id)", Summary: "Run a turn in the background and return { task_id, session_id }: the turn outlives the caller, may wait for an answer, and is limited to allowed_tools when the list is set"},
	{Signature: "cdx.agents.run({ prompt, preset, model, max_steps })", Summary: "Run a delegated turn in a fresh child session and return a promise for { session_id, content, steps, stopped_reason, usage }; depth 1"},
	{Signature: "cdx.agents.runMany({ prompts, preset, model, max_steps })", Summary: "Run up to 8 independent delegated turns in parallel and return a promise for { results } in input order"},
	{Signature: "cdx.mcp.connect(config) / call(server, tool, args) / listTools(server) / listResources(server) / readResource(server, uri) / status(server) / servers() / close(server)", Summary: "Talk to Model Context Protocol servers over stdio or HTTP: connect, list and call tools, read resources, report status; blocking calls return promises and connections are closed on plugin unload"},
	{Signature: "cdx.http.handle|get|post|put|delete|patch|use|group|static()", Summary: "Register HTTP endpoints, Gin middleware (ctx.bindJSON, ctx.next, ctx.abortWithJSON, ctx.set/get), and static assets"},
	{Signature: "cdx.ui.register({ title, description, order, schema, config_url, entry, ui_api, styles }) / list()", Summary: "Register a declarative JSON Schema panel or a versioned ESM page panel mounted in an isolated shadow root"},
	{Signature: "cdx.plugins.list|installZip|setEnabled|remove()", Summary: "Inspect, install from .zip URL, enable/disable, or remove plugins"},
	{Signature: "cdx.sys.readFile|writeFile|stat|readDir|random|env|exec|fetch()", Summary: "Low-level OS capabilities: filesystem I/O, cryptographic random bytes, environment lookup, shell execution, and HTTP/SSE streaming fetch; exec and fetch return promises"},
}

// DocHTTPContext lists what a cdx.http handler receives as its context object.
var DocHTTPContext = []DocEntry{
	{Signature: "ctx.method / ctx.path / ctx.host()", Summary: "Request method, path, and the Host header"},
	{Signature: "ctx.param(name) / ctx.query(name) / ctx.getHeader(name)", Summary: "Path parameter, query parameter, and request header"},
	{Signature: "ctx.bindJSON()", Summary: "Parsed JSON request body as an object; an empty object when the body is empty. Read bodies through it, not with ctx.body"},
	{Signature: "ctx.body()", Summary: "Raw request body string, for a body that is not JSON"},
	{Signature: "ctx.json(code, value) / ctx.status(code) / ctx.string(code, text) / ctx.html(code, html) / ctx.header(name, value)", Summary: "Write the response: JSON payload, bare status, text, HTML, or a response header"},
	{Signature: "ctx.abortWithJSON(code, value) / ctx.next()", Summary: "Middleware control: stop the chain with a JSON response, or continue to the next handler"},
	{Signature: "ctx.set(key, value) / ctx.get(key)", Summary: "Per-request values shared between middleware and handlers"},
}

// DocPermissionLevels lists the tool permission levels.
var DocPermissionLevels = []DocEntry{
	{Signature: "read", Summary: "Read-only inspection without side effects; allowed by default"},
	{Signature: "edit", Summary: "Creates or modifies files; asks the user unless policy allows"},
	{Signature: "dangerous", Summary: "Executes shell commands or destructive operations; asks the user"},
}

// RenderAPIDoc generates the markdown Plugin API Reference directly from the Go Manifest struct reflection and host definitions.
func RenderAPIDoc() string {
	var sb strings.Builder
	sb.WriteString("## Plugin API reference\n\n")
	sb.WriteString("Generated dynamically from the cdx Go core structures and host runtime; matches the running binary.\n\n")

	sb.WriteString("### Plugin files\n")
	writeDocEntries(&sb, DocFileNames)

	sb.WriteString("\n### `manifest.json` fields (reflected from Go `plugin.Manifest`)\n")
	t := reflect.TypeOf(Manifest{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		optional := len(parts) > 1 && parts[1] == "omitempty"
		reqLabel := "required"
		if optional || name == "order" {
			reqLabel = "optional"
		}
		fmt.Fprintf(&sb, "- `%s` (`%s`, %s)\n", name, f.Type.Name(), reqLabel)
	}

	sb.WriteString("\nOrder bands: `0`-`99` vital, `100`-`999` built-in, `1000` and above external. A user-installed plugin below `1000` is shifted by `1000`; a missing value becomes `5000`.\n")
	sb.WriteString("\n### The `cdx` host object\n")
	writeDocEntries(&sb, DocCDXObject)

	sb.WriteString("\n### HTTP handler context (`cdx.http`)\n")
	writeDocEntries(&sb, DocHTTPContext)

	sb.WriteString("\n### Tool permission levels\n")
	writeDocEntries(&sb, DocPermissionLevels)

	return strings.TrimRight(sb.String(), "\n") + "\n"
}

func writeDocEntries(sb *strings.Builder, entries []DocEntry) {
	for _, e := range entries {
		fmt.Fprintf(sb, "- `%s` - %s\n", e.Signature, e.Summary)
	}
}
