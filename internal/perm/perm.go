// Package perm checks permissions for agent actions.
package perm

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/registry"
)

// User decisions.
const (
	ChoiceOnce    = "once"
	ChoiceSession = "session"
	ChoiceAlways  = "always"
	ChoiceDeny    = "deny"
)

// Decision is the outcome of a check.
type Decision struct {
	Allowed bool
	NeedAsk bool
	Level   string
	Rule    string
}

// AskFunc asks the user for a decision.
type AskFunc func(level, tool, summary string) (string, error)

// Manager holds the rules and the remembered decisions.
type Manager struct {
	mu       sync.RWMutex
	levels   map[string]string
	rules    []*regexp.Regexp
	ruleMeta []config.DangerRule
	// session holds per-session decisions.
	session map[string]string
	// turnGranted reports that the current tool call is already allowed.
	//
	// A plugin reaches core capabilities under its own tool, which the
	// agent has already checked: asking again about the same action would
	// only duplicate the question.
	turnGranted bool
	// granted holds decisions made with the "always" choice.
	//
	// They live until the daemon restarts: the user answers once per run,
	// and a new run asks again.
	granted map[string]bool
	// allowed holds one-shot permissions from --allow.
	allowed map[string]bool
	// nonInteractive decides what happens when there is no terminal.
	nonInteractive string
}

// New creates a permission manager from the config.
func New(cfg *config.Config) (*Manager, error) {
	m := &Manager{
		levels: map[string]string{
			registry.LevelRead:      cfg.Perms.Read,
			registry.LevelEdit:      cfg.Perms.Edit,
			registry.LevelDangerous: cfg.Perms.Dangerous,
		},
		session:        map[string]string{},
		granted:        map[string]bool{},
		allowed:        map[string]bool{},
		nonInteractive: cfg.Perms.NonInteractive,
	}

	for _, r := range cfg.Danger {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("compile rule %s: %w", r.Name, err)
		}
		m.rules = append(m.rules, re)
		m.ruleMeta = append(m.ruleMeta, r)
	}
	return m, nil
}

// SetAllow sets the one-shot permissions for a call.
//
// The list replaces the previous one: a permission lasts one turn, not
// until the daemon restarts. Accumulating them would let a past
// --allow edit permit writes in every later call.
func (m *Manager) SetAllow(levels []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.allowed = make(map[string]bool, len(levels))
	for _, l := range levels {
		m.allowed[l] = true
	}
}

// GrantTurn marks the current tool call as allowed.
//
// It holds until the call ends: a plugin allowed for its own tool does not
// ask again about each operation inside.
func (m *Manager) GrantTurn() {
	m.mu.Lock()
	m.turnGranted = true
	m.mu.Unlock()
}

// EndTurn clears the mark of an allowed call.
func (m *Manager) EndTurn() {
	m.mu.Lock()
	m.turnGranted = false
	m.mu.Unlock()
}

// ClearAllow drops the one-shot permissions.
func (m *Manager) ClearAllow() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allowed = map[string]bool{}
}

// SetNonInteractive sets the ask behaviour without a terminal.
func (m *Manager) SetNonInteractive(v string) {
	m.mu.Lock()
	m.nonInteractive = v
	m.mu.Unlock()
}

// MatchRule matches the text against the dangerous rules.
func (m *Manager) MatchRule(text string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i, re := range m.rules {
		if re.MatchString(text) {
			return m.ruleMeta[i].Name, true
		}
	}
	return "", false
}

// Check decides what to do with a tool call.
func (m *Manager) Check(toolID, level, text string) Decision {
	if rule, hit := m.MatchRule(text); hit {
		level = registry.LevelDangerous
		// A dangerous action is always confirmed: a remembered
		// permission must not cover rm -rf.
		return m.decideAlwaysAsk(toolID, level, rule)
	}
	// Nothing to ask inside an allowed call: the user already answered
	// for this tool.
	if m.turnGrantedNow() {
		return Decision{Allowed: true, Level: level}
	}
	return m.decide(toolID, level, "")
}

// turnGrantedNow reports whether the current call is allowed.
func (m *Manager) turnGrantedNow() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.turnGranted
}

// decideAlwaysAsk decides on a dangerous rule without remembering it.
func (m *Manager) decideAlwaysAsk(toolID, level, rule string) Decision {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.allowed[level] || m.allowed[registry.LevelDangerous] {
		return Decision{Allowed: true, Level: level, Rule: rule}
	}
	if m.levels[level] == "allow" {
		return Decision{Allowed: true, Level: level, Rule: rule}
	}
	if m.levels[level] == "deny" {
		return Decision{Allowed: false, Level: level, Rule: rule}
	}
	return Decision{NeedAsk: true, Level: level, Rule: rule}
}

// decide applies the permission levels and the remembered decisions.
func (m *Manager) decide(toolID, level, rule string) Decision {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.allowed[level] || m.allowed[registry.LevelDangerous] {
		return Decision{Allowed: true, Level: level, Rule: rule}
	}
	key := level + ":" + toolID
	if m.granted[key] {
		return Decision{Allowed: true, Level: level, Rule: rule}
	}
	if choice, ok := m.session[key]; ok {
		return Decision{Allowed: choice != ChoiceDeny, Level: level, Rule: rule}
	}

	switch m.levels[level] {
	case "allow":
		return Decision{Allowed: true, Level: level, Rule: rule}
	case "deny":
		return Decision{Allowed: false, Level: level, Rule: rule}
	}
	return Decision{NeedAsk: true, Level: level, Rule: rule}
}

// NonInteractive reports the behaviour without a terminal.
func (m *Manager) NonInteractive() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nonInteractive
}

// Remember stores a user decision.
//
// "once" covers a single call and is not remembered. "session" and
// "always" are remembered per tool and level: they differ only in name,
// and both mean do not ask again until the program restarts.
func (m *Manager) Remember(level, toolID, choice string) {
	key := level + ":" + toolID

	m.mu.Lock()
	defer m.mu.Unlock()

	switch choice {
	case ChoiceSession, ChoiceAlways:
		m.granted[key] = true
		m.session[key] = choice
	case ChoiceDeny:
		m.session[key] = ChoiceDeny
	}
}

// Options returns the answer choices.
//
// There are two: allow one call, or stop asking until the program
// restarts. The separate "session" choice was dropped because users could
// not tell it from "always" and both behaved the same.
func Options(hasSession bool) []string {
	return []string{ChoiceOnce, ChoiceAlways, ChoiceDeny}
}
