package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/store"
)

// newManager creates a manager on a temporary database.
func newManager(t *testing.T) *Manager {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	m := New(st)
	// Fixed time keeps the ordering checks deterministic.
	base := time.Unix(1700000000, 0)
	tick := 0
	m.now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Second)
	}
	return m
}

// TestCreateMakesActive checks creation and activation of a session.
func TestCreateMakesActive(t *testing.T) {
	m := newManager(t)
	dir := t.TempDir()

	s, err := m.Create(dir, "первый запрос")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.ID == "" {
		t.Fatal("session id must not be empty")
	}
	if s.Root != NormalizePath(dir) {
		t.Fatalf("unexpected root %q", s.Root)
	}

	active, err := m.Active(dir, true)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active == nil || active.ID != s.ID {
		t.Fatalf("created session must be active, got %+v", active)
	}
}

// TestActiveWithoutSession checks there is no session by default.
func TestActiveWithoutSession(t *testing.T) {
	m := newManager(t)

	active, err := m.Active(t.TempDir(), true)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no session, got %+v", active)
	}
}

// TestInheritFromParent checks the walk up the tree.
func TestInheritFromParent(t *testing.T) {
	m := newManager(t)
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := mkdirAll(sub); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s, err := m.Create(root, "корень")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	active, err := m.Active(sub, true)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active == nil || active.ID != s.ID {
		t.Fatalf("subdirectory must inherit parent session, got %+v", active)
	}

	// Without inheritance a subdirectory has no session.
	active, err = m.Active(sub, false)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != nil {
		t.Fatalf("inheritance disabled must return nil, got %+v", active)
	}
}

// TestSubdirectorySessionWins checks that its own session wins.
func TestSubdirectorySessionWins(t *testing.T) {
	m := newManager(t)
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := mkdirAll(sub); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	parent, err := m.Create(root, "родитель")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	child, err := m.Create(sub, "ребёнок")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	active, err := m.Active(sub, true)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active == nil || active.ID != child.ID {
		t.Fatalf("own session must win, got %+v", active)
	}
	if active.ID == parent.ID {
		t.Fatal("parent session must not be selected")
	}
}

// TestEndClearsActive checks clearing the active session.
func TestEndClearsActive(t *testing.T) {
	m := newManager(t)
	dir := t.TempDir()

	if _, err := m.Create(dir, "x"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	done, err := m.End(dir)
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if !done {
		t.Fatal("End must report that a session was ended")
	}

	active, err := m.Active(dir, true)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != nil {
		t.Fatal("session must not be active after End")
	}

	// The history stays intact.
	if _, err := m.End(dir); err != nil {
		t.Fatalf("second End: %v", err)
	}
}

// TestListOnlyOwnDirectory checks the directory filter.
func TestListOnlyOwnDirectory(t *testing.T) {
	m := newManager(t)
	dirA := t.TempDir()
	dirB := t.TempDir()

	if _, err := m.Create(dirA, "a"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Create(dirB, "b"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	list, err := m.List(dirA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 session for dirA, got %d", len(list))
	}
	if list[0].Root != NormalizePath(dirA) {
		t.Fatalf("unexpected root %q", list[0].Root)
	}
}

// TestUseSwitchesActive checks switching the active session.
func TestUseSwitchesActive(t *testing.T) {
	m := newManager(t)
	dir := t.TempDir()

	first, err := m.Create(dir, "первая")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := m.Create(dir, "вторая")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := m.Use(first.ID); err != nil {
		t.Fatalf("Use: %v", err)
	}

	active, err := m.Active(dir, false)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active == nil || active.ID != first.ID {
		t.Fatalf("expected first session active, got %+v", active)
	}
	if second.ID == first.ID {
		t.Fatal("sessions must have distinct ids")
	}

	if _, err := m.Use("missing"); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

// TestDeleteRemovesHistory checks deleting a session with its history.
func TestDeleteRemovesHistory(t *testing.T) {
	m := newManager(t)
	dir := t.TempDir()

	s, err := m.Create(dir, "удаляемая")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Append(s.ID, Message{Kind: KindUser, Content: "привет"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := m.Delete(s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, found, err := m.Get(s.ID); err != nil || found {
		t.Fatalf("session must be gone, found=%v err=%v", found, err)
	}
	msgs, err := m.Messages(s.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("history must be removed, got %d messages", len(msgs))
	}

	active, err := m.Active(dir, true)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != nil {
		t.Fatal("deleted session must not stay active")
	}
}

// TestDeleteMissing checks the error for a missing session.
func TestDeleteMissing(t *testing.T) {
	m := newManager(t)
	if err := m.Delete("nope"); err == nil {
		t.Fatal("expected error for missing session")
	}
}

// TestDanglingActivePointer checks a pointer to a deleted session.
func TestDanglingActivePointer(t *testing.T) {
	m := newManager(t)
	dir := t.TempDir()

	s, err := m.Create(dir, "x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate the mismatch: the record is gone, the pointer remains.
	if err := m.st.Delete(store.BucketSessions, s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	active, err := m.Active(dir, true)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != nil {
		t.Fatalf("dangling pointer must be ignored, got %+v", active)
	}

	// The pointer is cleaned up and the next call finds no session either.
	if active, err := m.Active(dir, true); err != nil || active != nil {
		t.Fatalf("second Active: %+v err=%v", active, err)
	}
}

// TestTitleFromPrompt checks the short title.
func TestTitleFromPrompt(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "untitled"},
		{"  привет  ", "привет"},
		{"первая строка\nвторая", "первая строка"},
	}
	for _, tc := range cases {
		if got := titleFrom(tc.input); got != tc.want {
			t.Fatalf("titleFrom(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}

	long := make([]rune, 100)
	for i := range long {
		long[i] = 'я'
	}
	got := titleFrom(string(long))
	if len([]rune(got)) != 63 {
		t.Fatalf("long title must be cut to 63 runes, got %d", len([]rune(got)))
	}
}

// TestNormalizePath checks directory normalization.
func TestNormalizePath(t *testing.T) {
	got := NormalizePath(".")
	if got == "" || got == "." {
		t.Fatalf("expected absolute path, got %q", got)
	}
	if filepath.Base(got) == "" {
		t.Fatalf("path must have a base, got %q", got)
	}
}

// mkdirAll creates a directory for a test.
func mkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}
