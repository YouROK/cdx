package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/yourok/cdx/internal/logx"
)

// Transport names accepted by Config.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// Protocol revisions understood by the client.
const (
	RevisionModern = "2026-07-28"
	RevisionLegacy = "2025-06-18"
)

// Defaults applied to a Config that leaves the values zero.
const (
	DefaultTimeout = 120 * time.Second
	StartTimeout   = 30 * time.Second
	maxMessageSize = 16 << 20
	maxInFlight    = 8
)

// Config describes one MCP server connection.
type Config struct {
	ID        string
	Name      string
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	Dir       string
	URL       string
	Headers   map[string]string
	Protocol  string
	Timeout   time.Duration
	Root      string
	Logger    *logx.Logger
}

// timeout returns the per-request timeout with the default applied.
func (c Config) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

// ServerInfo is what a server reports about itself during discovery.
type ServerInfo struct {
	Name            string         `json:"name"`
	Title           string         `json:"title,omitempty"`
	Version         string         `json:"version"`
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	Instructions    string         `json:"instructions,omitempty"`
	Revision        string         `json:"-"`
}

// Tool is one tool advertised by a server.
type Tool struct {
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description,omitempty"`
	InputSchema  map[string]any   `json:"inputSchema,omitempty"`
	OutputSchema map[string]any   `json:"outputSchema,omitempty"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations carries the behavioral hints a server declares for a tool.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Content is one block of a tool result.
type Content struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MIMEType string          `json:"mimeType,omitempty"`
	URI      string          `json:"uri,omitempty"`
	Name     string          `json:"name,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

// Resource is one resource advertised by a server.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// ResourceContents is one resource payload.
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ReadResourceResult is the outcome of resources/read.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
}

// CallResult is the outcome of tools/call.
type CallResult struct {
	Content           []Content      `json:"content"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	ResultType        string         `json:"resultType,omitempty"`
}

// Error is a JSON-RPC error returned by the server.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("mcp: server error %d: %s", e.Code, e.Message)
}

// IsMethodNotFound reports whether the server rejected an unknown method.
func (e *Error) IsMethodNotFound() bool {
	return e != nil && e.Code == -32601
}
