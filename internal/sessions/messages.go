package sessions

import (
	"fmt"
	"strconv"

	"github.com/yourok/cdx/internal/store"
)

// keySep separates parts of a composite key.
const keySep = "\x00"

// seqWidth pads the sequence in the key: without leading zeros the
// lexicographic order would break between 9 and 10.
const seqWidth = 12

// messageKey builds the message key.
func messageKey(sessionID string, seq int) string {
	return fmt.Sprintf("%s%s%0*d", sessionID, keySep, seqWidth, seq)
}

// Append adds a message and returns its sequence number.
//
// The counter and the record change in one transaction, so a number is
// never reused even after a crash.
func (m *Manager) Append(sessionID string, msg Message) (*Message, error) {
	msg.TS = m.now().Unix()

	var out *Message
	err := m.st.Update(func(tx store.TxWriter) error {
		s, found, err := m.Get(sessionID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("session %q not found", sessionID)
		}

		msg.Seq = s.LastSeq + 1
		s.LastSeq = msg.Seq
		s.UpdatedAt = msg.TS
		if s.Title == "" || s.Title == "untitled" {
			if msg.Kind == KindUser && msg.Content != "" {
				s.Title = titleFrom(msg.Content)
			}
		}

		if err := tx.PutJSON(store.BucketMessages, messageKey(sessionID, msg.Seq), &msg); err != nil {
			return err
		}
		if err := tx.PutJSON(store.BucketSessions, sessionID, s); err != nil {
			return err
		}
		cp := msg
		out = &cp
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Messages returns the session history in ascending sequence order.
func (m *Manager) Messages(sessionID string) ([]Message, error) {
	keys, err := m.st.ListPrefix(store.BucketMessages, sessionID+keySep)
	if err != nil {
		return nil, err
	}

	out := make([]Message, 0, len(keys))
	for _, key := range keys {
		var msg Message
		found, err := m.st.GetJSON(store.BucketMessages, key, &msg)
		if err != nil {
			// Skip the damaged record: the session stays usable.
			continue
		}
		if found {
			out = append(out, msg)
		}
	}
	return out, nil
}

// LastMessages returns the last n messages.
func (m *Manager) LastMessages(sessionID string, n int) ([]Message, error) {
	all, err := m.Messages(sessionID)
	if err != nil {
		return nil, err
	}
	if n <= 0 || len(all) <= n {
		return all, nil
	}
	return all[len(all)-n:], nil
}

// SetModel stores the model the session answered with.
func (m *Manager) SetModel(sessionID, model string) error {
	s, found, err := m.Get(sessionID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("session %q not found", sessionID)
	}
	s.Model = model
	return m.st.PutJSON(store.BucketSessions, sessionID, s)
}

// Count returns the number of messages in the session.
func (m *Manager) Count(sessionID string) (int, error) {
	s, found, err := m.Get(sessionID)
	if err != nil || !found {
		return 0, err
	}
	return s.LastSeq, nil
}

// ParseSeq extracts the sequence number from a message key.
func ParseSeq(key string) (int, error) {
	if idx := indexOfSep(key); idx >= 0 && idx+1 < len(key) {
		return strconv.Atoi(key[idx+1:])
	}
	return 0, fmt.Errorf("key %q has no sequence", key)
}

// indexOfSep finds the separator in a key.
func indexOfSep(key string) int {
	for i := 0; i < len(key); i++ {
		if key[i] == keySep[0] {
			return i
		}
	}
	return -1
}
