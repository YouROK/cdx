// Package ipc describes the exchange protocol between client and daemon.
package ipc

import "encoding/json"

// Frame types.
const (
	TypeHello   = "hello"
	TypeHelloOK = "hello_ok"
	TypeHelloEr = "hello_err"
	TypeReq     = "req"
	TypeRes     = "res"
	TypeEvent   = "event"
	TypeCancel  = "cancel"
	TypePing    = "ping"
	TypePong    = "pong"
)

// Protocol error codes.
const (
	CodeBadFrame      = "bad_frame"
	CodeFrameTooLarge = "frame_too_large"
	CodeNotHandshaken = "not_handshaken"
	CodeProtoMismatch = "proto_mismatch"
	CodeUnknownMethod = "unknown_method"
	CodeBadParams     = "bad_params"
	CodeDuplicateID   = "duplicate_id"
	CodeInternal      = "internal"
)

// Frame is the common frame envelope.
type Frame struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Method  string `json:"method,omitempty"`
	Payload any    `json:"payload,omitempty"`
	OK      *bool  `json:"ok,omitempty"`
	Error   *Error `json:"error,omitempty"`
	Topic   string `json:"topic,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// Error is an execution or protocol error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Hint != "" {
		return e.Message + " (" + e.Hint + ")"
	}
	return e.Message
}

// NewError builds an error with a code.
func NewError(code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// HelloPayload is the client handshake data.
type HelloPayload struct {
	Proto  int      `json:"proto"`
	Client Endpoint `json:"client"`
}

// HelloOKPayload is the daemon handshake reply.
type HelloOKPayload struct {
	Proto  int      `json:"proto"`
	Daemon Endpoint `json:"daemon"`
	State  string   `json:"state"`
	Uptime int64    `json:"uptime_s"`
}

// Endpoint describes one side of a connection.
type Endpoint struct {
	Version string `json:"version"`
	OS      string `json:"os,omitempty"`
	PID     int    `json:"pid"`
}

// Success builds a successful reply.
func Success(id string, payload any) *Frame {
	ok := true
	return &Frame{Type: TypeRes, ID: id, OK: &ok, Payload: payload}
}

// Failure builds an error reply.
func Failure(id string, err *Error) *Frame {
	ok := false
	return &Frame{Type: TypeRes, ID: id, OK: &ok, Error: err}
}

// Event builds an event.
func Event(id, topic string, data any) *Frame {
	return &Frame{Type: TypeEvent, ID: id, Topic: topic, Data: data}
}

// MarshalPayload serializes a frame payload.
func MarshalPayload(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal decodes JSON into a value.
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
