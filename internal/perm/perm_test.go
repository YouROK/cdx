package perm

import (
	"testing"

	"github.com/yourok/cdx/internal/config"
)

// TestCheckLevels checks the basic permission levels.
func TestCheckLevels(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Read = "allow"
	cfg.Perms.Edit = "ask"

	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if d := m.Check("system.fs.readFile", "read", "cat file"); !d.Allowed {
		t.Fatalf("read must be allowed, got %+v", d)
	}
	if d := m.Check("system.fs.writeFile", "edit", "write file"); !d.NeedAsk {
		t.Fatalf("edit must require confirmation, got %+v", d)
	}
}

// TestDangerousRuleMatches checks that a dangerous rule matches.
func TestDangerousRuleMatches(t *testing.T) {
	cfg := config.Default()
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	d := m.Check("system.shell.exec", "edit", `{"command":"rm -rf /tmp/x"}`)
	if !d.NeedAsk || d.Level != "dangerous" {
		t.Fatalf("dangerous command must be escalated, got %+v", d)
	}
	if d.Rule != "delete-files" {
		t.Fatalf("expected delete-files rule, got %q", d.Rule)
	}
}

// TestAllowOverridesAsk checks the one-shot permission.
func TestAllowOverridesAsk(t *testing.T) {
	cfg := config.Default()
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	m.SetAllow([]string{"edit"})
	if d := m.Check("system.fs.writeFile", "edit", "write"); !d.Allowed {
		t.Fatalf("allow must permit edit, got %+v", d)
	}
}

// TestSessionChoice checks remembering per session.
func TestSessionChoice(t *testing.T) {
	cfg := config.Default()
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	m.Remember("edit", "system.fs.writeFile", ChoiceSession)
	if d := m.Check("system.fs.writeFile", "edit", "write"); !d.Allowed {
		t.Fatalf("session choice must be applied, got %+v", d)
	}
	if d := m.Check("system.fs.remove", "edit", "remove"); !d.NeedAsk {
		t.Fatalf("other tool must still ask, got %+v", d)
	}
}

// TestAllowDoesNotAccumulate checks that a permission does not carry
// over to the next call.
//
// Accumulating them made a past --allow edit permanent, so any later call
// could write files without asking.
func TestAllowDoesNotAccumulate(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "ask"
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// First call: reads and writes are allowed.
	m.SetAllow([]string{"read", "edit"})
	if d := m.Check("system.fs.writeFile", "edit", "write"); !d.Allowed {
		t.Fatal("edit must be allowed within the turn")
	}

	// Second call: only reads are allowed.
	m.SetAllow([]string{"read"})
	if d := m.Check("system.fs.writeFile", "edit", "write"); d.Allowed {
		t.Fatal("edit from the previous turn must not carry over")
	}
}

// TestClearAllow checks dropping the one-shot permissions.
func TestClearAllow(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "ask"
	m, _ := New(cfg)

	m.SetAllow([]string{"edit"})
	m.ClearAllow()

	if d := m.Check("system.fs.writeFile", "edit", "write"); d.Allowed {
		t.Fatal("cleared allow must not grant access")
	}
}

// TestAlwaysIsRemembered checks that "always" is remembered.
//
// Without it the answer would be asked again on every call.
func TestAlwaysIsRemembered(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "ask"
	m, _ := New(cfg)

	if d := m.Check("plugin.local-tools.run_command", "edit", "git log"); !d.NeedAsk {
		t.Fatal("first call must ask")
	}

	m.Remember("edit", "plugin.local-tools.run_command", ChoiceAlways)

	if d := m.Check("plugin.local-tools.run_command", "edit", "git log"); !d.Allowed {
		t.Fatal("always must be remembered")
	}
}

// TestSessionIsRemembered checks that "session" is remembered too.
func TestSessionIsRemembered(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "ask"
	m, _ := New(cfg)

	m.Remember("edit", "tool", ChoiceSession)

	if d := m.Check("tool", "edit", "x"); !d.Allowed {
		t.Fatal("session must be remembered")
	}
}

// TestOnceIsNotRemembered checks that "once" covers a single call.
func TestOnceIsNotRemembered(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "ask"
	m, _ := New(cfg)

	m.Remember("edit", "tool", ChoiceOnce)

	if d := m.Check("tool", "edit", "x"); !d.NeedAsk {
		t.Fatal("once must not be remembered")
	}
}

// TestDangerousAlwaysAsks checks that a dangerous action asks every time.
//
// A remembered permission must not cover rm -rf.
func TestDangerousAlwaysAsks(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "ask"
	m, _ := New(cfg)

	// The user allowed the tool for good.
	m.Remember("edit", "tool", ChoiceAlways)
	if d := m.Check("tool", "edit", "git log"); !d.Allowed {
		t.Fatal("the granted tool must be allowed")
	}

	// A dangerous command in the same tool still asks again.
	d := m.Check("tool", "edit", "rm -rf /tmp/x")
	if !d.NeedAsk {
		t.Fatalf("dangerous action must ask even when the tool is granted, got %+v", d)
	}
	if d.Level != "dangerous" {
		t.Fatalf("dangerous level is expected, got %q", d.Level)
	}
}

// TestGrantTurnCoversNested checks that an allowed call covers the nested
// plugin operations.
func TestGrantTurnCoversNested(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "ask"
	m, _ := New(cfg)

	m.GrantTurn()
	defer m.EndTurn()

	// Inside an allowed tool the API does not ask again.
	if d := m.Check("system.shell.exec", "edit", "git log"); !d.Allowed {
		t.Fatalf("nested operation must be allowed, got %+v", d)
	}

	// The dangerous rule still asks.
	if d := m.Check("system.shell.exec", "edit", "rm -rf /"); !d.NeedAsk {
		t.Fatal("dangerous rule must still ask inside a granted call")
	}
}

// TestGrantTurnEnded checks clearing the mark of an allowed call.
func TestGrantTurnEnded(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "ask"
	m, _ := New(cfg)

	m.GrantTurn()
	m.EndTurn()

	if d := m.Check("system.shell.exec", "edit", "git log"); !d.NeedAsk {
		t.Fatal("after the call ends the permission must be asked again")
	}
}

// TestOptionsHasTwoChoices checks the set of answer choices.
func TestOptionsHasTwoChoices(t *testing.T) {
	for _, hasSession := range []bool{true, false} {
		opts := Options(hasSession)
		if len(opts) != 3 {
			t.Fatalf("expected three options, got %v", opts)
		}
		if opts[0] != ChoiceOnce || opts[1] != ChoiceAlways || opts[2] != ChoiceDeny {
			t.Fatalf("unexpected options %v", opts)
		}
	}
}
