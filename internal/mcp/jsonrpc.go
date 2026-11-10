package mcp

import "encoding/json"

// message is one JSON-RPC 2.0 frame. A frame with an id is a request or a response,
// a frame with a method and no id is a notification.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// requestFrame builds a request or notification frame. A nil id produces a notification.
func requestFrame(id json.RawMessage, method string, params any) ([]byte, error) {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	return json.Marshal(message{JSONRPC: "2.0", ID: id, Method: method, Params: raw})
}

// responseFrame builds a response frame for a server request.
func responseFrame(id json.RawMessage, result any, rpcErr *Error) ([]byte, error) {
	frame := message{JSONRPC: "2.0", ID: id, Error: rpcErr}
	if rpcErr == nil && result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		frame.Result = data
	}
	return json.Marshal(frame)
}
