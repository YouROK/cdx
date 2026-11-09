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
	{Signature: "cdx.id / cdx.dir / cdx.worker / cdx.workdir()", Summary: "Plugin ID, absolute directory path, worker-copy flag, and the active session working directory"},
	{Signature: "cdx.inWorker(fn)", Summary: "Mark a provider complete handler or tool handler for execution in an isolated worker VM"},
	{Signature: "cdx.apiDoc()", Summary: "Returns the live Markdown Plugin API Reference generated from the running binary"},
	{Signature: "cdx.log.debug|info|warn|error(msg)", Summary: "Write leveled log messages tagged with [Plugin:<id>]"},
	{Signature: "cdx.storage.get|set|delete|keys|list()", Summary: "Isolated per-plugin plain JSON key-value store in BoltDB"},
	{Signature: "cdx.secrets.get|set|delete|has|keys|masked()", Summary: "Isolated per-plugin AES-256-GCM encrypted store in BoltDB for API keys and secrets"},
	{Signature: "cdx.settings.register(schema) / save(values)", Summary: "Register a JSON Schema for declarative settings; format: 'secret' fields are routed to cdx.secrets and exposed through the plugin settings endpoint"},
	{Signature: "cdx.tools.register(spec, handler) / list()", Summary: "Register an LLM tool ({ name, description, permission, input_schema }, function(args) {}); wrap handler with cdx.inWorker to use a worker VM"},
	{Signature: "cdx.providers.register({ listModels, complete }) / listModels()", Summary: "Register as an LLM provider; listModels returns available models, complete(req, emit) runs turns; wrap complete with cdx.inWorker to use a worker VM"},
	{Signature: "cdx.hooks.on(hookName, priority, handler) / run(hookName, payload)", Summary: "Intercept lifecycle pipelines: session:before_turn, tool:before_call, tool:decided, tool:after_call"},
	{Signature: "cdx.bus.publish|subscribe|handle|request(topic, payload)", Summary: "Communicate across plugins and core via events and 1-to-1 RPC"},
	{Signature: "cdx.questions.ask(question)", Summary: "Ask the active client a question ({ kind, title, body, details, options, default, text }) and return { choice, text, cancelled, reason }; available in worker VMs"},
	{Signature: "cdx.agents.run({ prompt, preset, model, max_steps })", Summary: "Run a delegated turn in a fresh child session and return { session_id, content, steps, stopped_reason, usage }; available in worker VMs, depth 1"},
	{Signature: "cdx.http.handle|get|post|put|delete|patch|use|group|static()", Summary: "Register HTTP endpoints, Gin middleware (ctx.bindJSON, ctx.next, ctx.abortWithJSON, ctx.set/get), and static assets"},
	{Signature: "cdx.ui.register({ title, description, order, schema, config_url, entry, ui_api, styles }) / list()", Summary: "Register a declarative JSON Schema panel or a versioned ESM page panel mounted in an isolated shadow root"},
	{Signature: "cdx.plugins.list|installZip|setEnabled|remove()", Summary: "Inspect, install from .zip URL, enable/disable, or remove plugins"},
	{Signature: "cdx.sys.readFile|writeFile|stat|readDir|random|env|exec|fetch()", Summary: "Low-level OS capabilities: filesystem I/O, cryptographic random bytes, environment lookup, shell execution, and HTTP/SSE streaming fetch"},
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
