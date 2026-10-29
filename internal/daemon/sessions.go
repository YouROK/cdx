package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/sessions"
	"github.com/yourok/cdx/internal/store"
)

// Session control methods.
const (
	MethodSessionNew    = "session.new"
	MethodSessionEnd    = "session.end"
	MethodSessionList   = "session.list"
	MethodSessionShow   = "session.show"
	MethodSessionUse    = "session.use"
	MethodSessionDelete = "session.delete"
	MethodSessionWriter = "session.write"
)

// SessionNewRequest holds session creation parameters.
type SessionNewRequest struct {
	CWD   string `json:"cwd"`
	Title string `json:"title"`
}

// SessionRef refers to a session by directory or id.
type SessionRef struct {
	CWD string `json:"cwd"`
	ID  string `json:"id"`
}

// sessionView is a session as the client sees it.
type sessionView struct {
	ID        string `json:"id"`
	Root      string `json:"root"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Messages  int    `json:"messages"`
	Active    bool   `json:"active"`
	Inherited bool   `json:"inherited"`
}

// handleSession runs a session control method.
func (d *Daemon) handleSession(c *session, f *ipc.Frame) {
	switch f.Method {
	case MethodSessionNew:
		d.sessionNew(c, f)
	case MethodSessionEnd:
		d.sessionEnd(c, f)
	case MethodSessionList:
		d.sessionList(c, f)
	case MethodSessionShow:
		d.sessionShow(c, f)
	case MethodSessionUse:
		d.sessionUse(c, f)
	case MethodSessionDelete:
		d.sessionDelete(c, f)
	case MethodSessionWriter:
		d.sessionWrite(c, f)
	default:
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeUnknownMethod,
			fmt.Sprintf("method %q is not supported", f.Method))))
	}
}

// sessionNew creates a session for a directory.
func (d *Daemon) sessionNew(c *session, f *ipc.Frame) {
	if !d.cfg.Sessions.Enabled {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, "sessions are disabled in config")))
		return
	}

	var req SessionNewRequest
	if err := decodePayload(f, &req); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}

	s, err := d.sess.Create(req.CWD, req.Title)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}
	if err := d.writeMarker(s); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}

	_ = c.conn.Write(ipc.Success(f.ID, map[string]any{"session": toView(s, 0, true, false)}))
}

// sessionEnd clears the directory's active session.
func (d *Daemon) sessionEnd(c *session, f *ipc.Frame) {
	var req SessionRef
	if err := decodePayload(f, &req); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}

	done, err := d.sess.End(req.CWD)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}
	_ = d.removeMarker(req.CWD)
	_ = c.conn.Write(ipc.Success(f.ID, map[string]any{"ended": done}))
}

// sessionList returns the directory's sessions.
func (d *Daemon) sessionList(c *session, f *ipc.Frame) {
	var req SessionRef
	if err := decodePayload(f, &req); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}

	list, err := d.sess.List(req.CWD)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}

	active, err := d.sess.Active(req.CWD, d.cfg.Sessions.InheritFromParents)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}

	views := make([]sessionView, 0, len(list))
	for i := range list {
		v := toView(&list[i], list[i].LastSeq, false, false)
		if active != nil && active.ID == list[i].ID {
			v.Active = true
		}
		views = append(views, v)
	}

	// An inherited session is listed separately with its root.
	inherited := ""
	if active != nil && sessions.NormalizePath(req.CWD) != active.Root {
		views = append(views, toView(active, active.LastSeq, true, true))
		inherited = active.Root
	}

	_ = c.conn.Write(ipc.Success(f.ID, map[string]any{
		"sessions":       views,
		"inherited_from": inherited,
	}))
}

// sessionShow returns a session history.
func (d *Daemon) sessionShow(c *session, f *ipc.Frame) {
	var req SessionRef
	if err := decodePayload(f, &req); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}

	target, err := d.resolveSession(req)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}
	if target == nil {
		_ = c.conn.Write(ipc.Success(f.ID, map[string]any{"session": nil, "messages": []sessions.Message{}}))
		return
	}

	msgs, err := d.sess.Messages(target.ID)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}

	_ = c.conn.Write(ipc.Success(f.ID, map[string]any{
		"session":  toView(target, target.LastSeq, false, false),
		"messages": msgs,
	}))
}

// sessionUse makes a session active.
func (d *Daemon) sessionUse(c *session, f *ipc.Frame) {
	var req SessionRef
	if err := decodePayload(f, &req); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}
	if req.ID == "" {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, "session id is required")))
		return
	}

	s, err := d.sess.Use(req.ID)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}
	if err := d.writeMarker(s); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}

	_ = c.conn.Write(ipc.Success(f.ID, map[string]any{"session": toView(s, s.LastSeq, true, false)}))
}

// sessionDelete removes a session.
func (d *Daemon) sessionDelete(c *session, f *ipc.Frame) {
	var req SessionRef
	if err := decodePayload(f, &req); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}

	target, err := d.resolveSession(req)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}
	if target == nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, "no session to delete")))
		return
	}

	if err := d.sess.Delete(target.ID); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}
	_ = d.removeMarker(target.Root)

	_ = c.conn.Write(ipc.Success(f.ID, map[string]any{"deleted": target.ID}))
}

// sessionWrite appends a message to the history.
func (d *Daemon) sessionWrite(c *session, f *ipc.Frame) {
	var req struct {
		SessionID string           `json:"session_id"`
		Message   sessions.Message `json:"message"`
	}
	if err := decodePayload(f, &req); err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, err.Error())))
		return
	}
	if req.SessionID == "" {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeBadParams, "session_id is required")))
		return
	}

	msg, err := d.sess.Append(req.SessionID, req.Message)
	if err != nil {
		_ = c.conn.Write(ipc.Failure(f.ID, ipc.NewError(ipc.CodeInternal, err.Error())))
		return
	}
	_ = c.conn.Write(ipc.Success(f.ID, map[string]any{"message": msg}))
}

// resolveSession finds a session by id, or the directory's active one.
func (d *Daemon) resolveSession(req SessionRef) (*sessions.Session, error) {
	if req.ID != "" {
		s, found, err := d.sess.Get(req.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("session %q not found", req.ID)
		}
		return s, nil
	}
	return d.sess.Active(req.CWD, d.cfg.Sessions.InheritFromParents)
}

// toView converts a session for the client.
func toView(s *sessions.Session, messages int, active, inherited bool) sessionView {
	return sessionView{
		ID:        s.ID,
		Root:      s.Root,
		Title:     s.Title,
		Model:     s.Model,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		Messages:  messages,
		Active:    active,
		Inherited: inherited,
	}
}

// writeMarker writes the pointer file when the marker mode is on.
func (d *Daemon) writeMarker(s *sessions.Session) error {
	if d.cfg.Sessions.Marker != "pointer" {
		return nil
	}
	return writeSessionMarker(s.Root, s.ID)
}

// removeMarker removes the pointer file.
func (d *Daemon) removeMarker(dir string) error {
	if d.cfg.Sessions.Marker != "pointer" {
		return nil
	}
	return removeSessionMarker(dir)
}

// appendHistory stores turn messages when a session is active.
func (d *Daemon) appendHistory(s *sessions.Session, messages []sessions.Message) {
	if s == nil {
		return
	}
	for _, msg := range messages {
		if _, err := d.sess.Append(s.ID, msg); err != nil {
			// A history failure must not break a finished turn.
			continue
		}
	}
}

func nowUnix() int64 { return time.Now().Unix() }

var _ = store.BucketSessions
var _ = strings.TrimSpace
