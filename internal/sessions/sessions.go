// Package sessions stores conversations bound to directories.
package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yourok/cdx/internal/store"
)

// History entry kinds.
const (
	KindUser       = "user"
	KindAssistant  = "assistant"
	KindToolCall   = "tool_call"
	KindToolResult = "tool_result"
	KindSummary    = "summary"
	KindError      = "error"
)

// Session message roles.
const (
	KindMain = "main"
	KindSub  = "sub"
)

// Session holds conversation metadata.
type Session struct {
	ID        string `json:"id"`
	Root      string `json:"root"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	ParentID  string `json:"parent_session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	LastSeq   int    `json:"last_seq"`
}

// Message is a single history entry.
type Message struct {
	Seq        int    `json:"seq"`
	Kind       string `json:"kind"`
	Role       string `json:"role,omitempty"`
	Content    string `json:"content,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	OK         bool   `json:"ok,omitempty"`
	Model      string `json:"model,omitempty"`
	TokensIn   int    `json:"tokens_in,omitempty"`
	TokensOut  int    `json:"tokens_out,omitempty"`
	// PromptHash identifies the system prompt the reply was produced with.
	PromptHash string `json:"prompt_hash,omitempty"`
	// CoversFrom and CoversTo bound the messages replaced by this summary.
	CoversFrom int   `json:"covers_from,omitempty"`
	CoversTo   int   `json:"covers_to,omitempty"`
	TS         int64 `json:"ts"`
}

// Manager manages sessions on top of the store.
type Manager struct {
	st  *store.Store
	now func() time.Time
}

// New creates a session manager.
func New(st *store.Store) *Manager {
	return &Manager{st: st, now: time.Now}
}

// NewID generates a session identifier.
func NewID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// NormalizePath returns an absolute directory path without a trailing slash.
func NormalizePath(dir string) string {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

// Create creates a session for the directory and makes it active.
func (m *Manager) Create(dir, title string) (*Session, error) {
	root := NormalizePath(dir)
	now := m.now()

	s := &Session{
		ID:        NewID(),
		Root:      root,
		Title:     titleFrom(title),
		Kind:      KindMain,
		CreatedAt: now.Unix(),
		UpdatedAt: now.Unix(),
	}
	if err := m.st.PutJSON(store.BucketSessions, s.ID, s); err != nil {
		return nil, err
	}
	if err := m.st.PutString(store.BucketSessionsIndex, s.ID, root); err != nil {
		return nil, err
	}
	if err := m.setActive(root, s.ID); err != nil {
		return nil, err
	}
	return s, nil
}

// titleFrom builds a short title from the request.
func titleFrom(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "untitled"
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	runes := []rune(text)
	if len(runes) > 60 {
		return string(runes[:60]) + "..."
	}
	return text
}

// Get reads a session by id.
func (m *Manager) Get(id string) (*Session, bool, error) {
	var s Session
	found, err := m.st.GetJSON(store.BucketSessions, id, &s)
	if err != nil || !found {
		return nil, found, err
	}
	return &s, true, nil
}

// Active returns the session active for the directory.
//
// The search walks up the tree like a .git lookup: a subdirectory without
// its own session continues the session of the nearest ancestor.
func (m *Manager) Active(dir string, inherit bool) (*Session, error) {
	for _, candidate := range m.candidates(NormalizePath(dir), inherit) {
		id, found, err := m.st.GetString(store.BucketSessionsActiv, candidate)
		if err != nil {
			return nil, err
		}
		if !found || id == "" {
			continue
		}
		s, ok, err := m.Get(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			// Stale pointer to a deleted session: drop it.
			_ = m.st.Delete(store.BucketSessionsActiv, candidate)
			continue
		}
		return s, nil
	}
	return nil, nil
}

// candidates lists the directories searched for an active session.
func (m *Manager) candidates(dir string, inherit bool) []string {
	out := []string{dir}
	if !inherit {
		return out
	}
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		out = append(out, parent)
		dir = parent
	}
	return out
}

// setActive binds the session to the directory.
func (m *Manager) setActive(root, id string) error {
	return m.st.PutString(store.BucketSessionsActiv, root, id)
}

// End clears the active session for the directory.
func (m *Manager) End(dir string) (bool, error) {
	root := NormalizePath(dir)
	_, found, err := m.st.GetString(store.BucketSessionsActiv, root)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return true, m.st.Delete(store.BucketSessionsActiv, root)
}

// Use makes the session active for its own directory.
func (m *Manager) Use(id string) (*Session, error) {
	s, found, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("session %q not found", id)
	}
	if err := m.setActive(s.Root, s.ID); err != nil {
		return nil, err
	}
	return s, nil
}

// List returns the sessions of a directory, newest first.
func (m *Manager) List(dir string) ([]Session, error) {
	root := NormalizePath(dir)
	keys, err := m.st.List(store.BucketSessionsIndex)
	if err != nil {
		return nil, err
	}

	out := make([]Session, 0, len(keys))
	for _, id := range keys {
		s, found, err := m.Get(id)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		// Subagent sessions are not listed.
		if s.Kind == KindSub {
			continue
		}
		if s.Root != root {
			continue
		}
		out = append(out, *s)
	}
	sortSessions(out)
	return out, nil
}

// sortSessions orders sessions by update time.
func sortSessions(list []Session) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].UpdatedAt > list[j-1].UpdatedAt; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// Delete removes a session with its history.
func (m *Manager) Delete(id string) error {
	s, found, err := m.Get(id)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("session %q not found", id)
	}

	if err := m.st.DeleteRange(store.BucketMessages, id+keySep); err != nil {
		return err
	}
	if err := m.st.Delete(store.BucketSessionsIndex, id); err != nil {
		return err
	}
	if err := m.st.Delete(store.BucketSessions, id); err != nil {
		return err
	}

	// The pointer is cleared only when it leads to this session.
	if active, ok, err := m.st.GetString(store.BucketSessionsActiv, s.Root); err == nil && ok && active == id {
		_ = m.st.Delete(store.BucketSessionsActiv, s.Root)
	}
	return nil
}
