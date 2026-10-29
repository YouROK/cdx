package sessions

import (
	"fmt"
	"testing"
)

// TestAppendSeqIncrements checks that sequence numbers grow.
func TestAppendSeqIncrements(t *testing.T) {
	m := newManager(t)
	s, err := m.Create(t.TempDir(), "x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := 1; i <= 5; i++ {
		msg, err := m.Append(s.ID, Message{Kind: KindUser, Content: fmt.Sprintf("m%d", i)})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if msg.Seq != i {
			t.Fatalf("expected seq %d, got %d", i, msg.Seq)
		}
	}

	count, err := m.Count(s.ID)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 messages, got %d", count)
	}
}

// TestMessagesOrder checks the history order.
func TestMessagesOrder(t *testing.T) {
	m := newManager(t)
	s, err := m.Create(t.TempDir(), "x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := 0; i < 12; i++ {
		if _, err := m.Append(s.ID, Message{Kind: KindUser, Content: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	msgs, err := m.Messages(s.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 12 {
		t.Fatalf("expected 12 messages, got %d", len(msgs))
	}
	// The 9 -> 10 transition matters: keys are zero padded.
	for i, msg := range msgs {
		if msg.Seq != i+1 {
			t.Fatalf("message %d has seq %d", i, msg.Seq)
		}
	}
}

// TestMessageKeyOrder checks the lexicographic order of keys.
func TestMessageKeyOrder(t *testing.T) {
	prev := messageKey("s", 1)
	for i := 2; i <= 100; i++ {
		key := messageKey("s", i)
		if key <= prev {
			t.Fatalf("key for %d (%q) must sort after %q", i, key, prev)
		}
		prev = key
	}
}

// TestLastMessages checks the slice of the last messages.
func TestLastMessages(t *testing.T) {
	m := newManager(t)
	s, err := m.Create(t.TempDir(), "x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := 1; i <= 10; i++ {
		if _, err := m.Append(s.ID, Message{Kind: KindUser, Content: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	tail, err := m.LastMessages(s.ID, 3)
	if err != nil {
		t.Fatalf("LastMessages: %v", err)
	}
	if len(tail) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(tail))
	}
	if tail[0].Seq != 8 || tail[2].Seq != 10 {
		t.Fatalf("unexpected range %d..%d", tail[0].Seq, tail[2].Seq)
	}

	all, err := m.LastMessages(s.ID, 100)
	if err != nil {
		t.Fatalf("LastMessages: %v", err)
	}
	if len(all) != 10 {
		t.Fatalf("expected all messages, got %d", len(all))
	}
}

// TestAppendMissingSession checks the error for a missing session.
func TestAppendMissingSession(t *testing.T) {
	m := newManager(t)
	if _, err := m.Append("missing", Message{Kind: KindUser}); err == nil {
		t.Fatal("expected error for missing session")
	}
}

// TestTitleFromFirstMessage checks the title update.
func TestTitleFromFirstMessage(t *testing.T) {
	m := newManager(t)
	s, err := m.Create(t.TempDir(), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := m.Append(s.ID, Message{Kind: KindUser, Content: "объясни этот код"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, found, err := m.Get(s.ID)
	if err != nil || !found {
		t.Fatalf("Get: %v found=%v", err, found)
	}
	if got.Title != "объясни этот код" {
		t.Fatalf("expected title from first message, got %q", got.Title)
	}
}

// TestSetModel checks that the session model is stored.
func TestSetModel(t *testing.T) {
	m := newManager(t)
	s, err := m.Create(t.TempDir(), "x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.SetModel(s.ID, "gpt-test"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}

	got, _, err := m.Get(s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Model != "gpt-test" {
		t.Fatalf("expected model to be stored, got %q", got.Model)
	}
}

// TestParseSeq checks extracting the number from a key.
func TestParseSeq(t *testing.T) {
	key := messageKey("abc", 42)
	seq, err := ParseSeq(key)
	if err != nil {
		t.Fatalf("ParseSeq: %v", err)
	}
	if seq != 42 {
		t.Fatalf("expected 42, got %d", seq)
	}

	if _, err := ParseSeq("noseparator"); err == nil {
		t.Fatal("expected error for key without separator")
	}
}

// TestMessagesSurviveCorruptEntry checks that a damaged record is skipped.
func TestMessagesSurviveCorruptEntry(t *testing.T) {
	m := newManager(t)
	s, err := m.Create(t.TempDir(), "x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := m.Append(s.ID, Message{Kind: KindUser, Content: "первое"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := m.Append(s.ID, Message{Kind: KindUser, Content: "второе"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Break the middle record with invalid JSON.
	if err := m.st.PutString("messages", messageKey(s.ID, 1), "{broken"); err != nil {
		t.Fatalf("PutString: %v", err)
	}

	msgs, err := m.Messages(s.ID)
	if err != nil {
		t.Fatalf("Messages must not fail: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Seq != 2 {
		t.Fatalf("expected only intact message, got %+v", msgs)
	}
}
