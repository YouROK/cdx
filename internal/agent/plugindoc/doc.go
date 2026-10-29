// Package plugindoc describes the plugin API for plugin development mode.
//
// The text comes from program constants rather than being written separately,
// so any API change updates the reference too. A test checks it stays current,
// which keeps the instruction from drifting away from the code.
package plugindoc

import (
	"fmt"
	"strings"
)

// Entry is one plugin capability.
type Entry struct {
	// Signature is how it is called.
	Signature string
	// Summary says what it does, in one line.
	Summary string
}

// FileNames lists the files a plugin consists of.
var FileNames = []Entry{
	{Signature: "manifest.json", Summary: "declares the plugin: name, version, entry file, permission, capabilities, tools"},
	{Signature: "<entry>.js", Summary: "JavaScript loaded into a fresh goja VM at startup; must leave the exported tools in cdx.exports"},
}

// BridgeCalls is the cdx object available inside the VM.
var BridgeCalls = []Entry{
	{Signature: `cdx.request(topic, payload)`, Summary: "call another subsystem through the bus; the topic must be covered by a declared capability"},
	{Signature: `cdx.publish(topic, payload)`, Summary: "publish an event; only topics under the plugin's own prefix are writable"},
	{Signature: `cdx.log(message)`, Summary: "write one line to the daemon log"},
	{Signature: `cdx.exports`, Summary: "object holding the registered tools; the runtime reads it after init"},
}

// Limits are the rules the daemon enforces.
var Limits = []Entry{
	{Signature: "capabilities", Summary: "every cdx.request topic must be listed here or the call is rejected; wildcards match a prefix, and core.* is never granted"},
	{Signature: "tools[].name", Summary: "short verb-like name the model sees; must be unique across all plugins"},
	{Signature: "tools[].description", Summary: "one sentence telling the model when to use the tool"},
	{Signature: "tools[].input_schema", Summary: "JSON Schema for the arguments; the model receives it verbatim"},
	{Signature: "tools[].permission", Summary: "read, edit or dangerous; a tool cannot run with fewer rights than declared"},
	{Signature: "timeouts", Summary: "init, per-tool and shutdown limits are enforced; a plugin that exceeds them is unloaded"},
}

// ToolPermissions are the permission levels.
var ToolPermissions = []Entry{
	{Signature: "read", Summary: "inspection without side effects; runs without asking"},
	{Signature: "edit", Summary: "changes files; asks the user unless permissions allow otherwise"},
	{Signature: "dangerous", Summary: "destructive or irreversible; matched against the danger rules"},
}

// Render builds the reference as one markdown block.
func Render() string {
	var sb strings.Builder
	sb.WriteString("## Plugin API reference\n")
	sb.WriteString("\nGenerated from the program itself; it matches the installed version.\n")

	sb.WriteString("\n### Files\n")
	writeEntries(&sb, FileNames)
	sb.WriteString("\n### The cdx object\n")
	writeEntries(&sb, BridgeCalls)
	sb.WriteString("\n### Permission levels\n")
	writeEntries(&sb, ToolPermissions)
	sb.WriteString("\n### Rules the daemon enforces\n")
	writeEntries(&sb, Limits)

	return strings.TrimRight(sb.String(), "\n") + "\n"
}

// writeEntries prints a capability list.
func writeEntries(sb *strings.Builder, list []Entry) {
	for _, e := range list {
		fmt.Fprintf(sb, "- `%s` - %s\n", e.Signature, e.Summary)
	}
}
