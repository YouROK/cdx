package daemon

import (
	"testing"
)

// TestCoreHasNoTools checks the core ships no tools.
//
// Tools come only from plugins and MCP servers: an empty profile gives
// the agent no tools at all.
func TestCoreHasNoTools(t *testing.T) {
	d := newTestDaemon(t)

	if got := len(d.reg.List()); got != 1 {
		t.Fatalf("expected only the test tool, got %d", got)
	}

	// A daemon built without plugins must have an empty registry.
	fresh := &Daemon{reg: newEmptyRegistry()}
	if got := len(fresh.reg.List()); got != 0 {
		t.Fatalf("core registry must be empty, got %d tools", got)
	}
}

// TestBusHasNoCoreTools checks core topics are not registered.
func TestBusHasNoCoreTools(t *testing.T) {
	d := newTestDaemon(t)
	d.pluginsDir = t.TempDir()
	d.initBus()

	for _, topic := range d.bus.Topics() {
		if len(topic) > 5 && topic[:5] == "core." {
			t.Fatalf("core must not register tools, got topic %q", topic)
		}
	}
}
