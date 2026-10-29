package pluginapi

// Asking the user for confirmation from a plugin.
//
// The agent already checks a plugin tool before calling it, but one tool can
// perform several operations in a row: a single button may read ten files and
// run a command. So every operation is checked again, and a plugin never gets
// more rights than it declared.

import (
	"fmt"
)

// Asker passes a confirmation request to the user.
//
// The implementation decides where it goes: to the client terminal when the
// turn comes from one, or a refusal when nobody can answer.
type Asker func(level, tool, summary string) (string, error)

// SetAsker installs the confirmation handler.
func (a *API) SetAsker(asker Asker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ask = asker
}

// askDecision asks the user about an operation.
func (a *API) askDecision(level, tool, summary string) error {
	a.mu.Lock()
	asker := a.ask
	a.mu.Unlock()

	if asker == nil {
		return fmt.Errorf("permission denied: %s needs confirmation, but nobody can answer", summary)
	}

	choice, err := asker(level, tool, summary)
	if err != nil {
		return fmt.Errorf("permission denied: %v", err)
	}
	if choice == "" || choice == "deny" {
		return fmt.Errorf("permission denied: %s", summary)
	}
	return nil
}
