package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yourok/cdx/internal/logx"
)

const (
	maxErrorBody = 4096
	legacyProbe  = 20 * time.Second
)

// httpTransport talks to a server over Streamable HTTP and falls back to the
// deprecated HTTP+SSE transport when the endpoint does not accept POST.
type httpTransport struct {
	cfg    Config
	log    *logx.Scope
	client *http.Client

	handler func([]byte)

	baseCtx    context.Context
	baseCancel context.CancelFunc

	closed     chan struct{}
	closedOnce sync.Once
	closeOnce  sync.Once

	mu        sync.Mutex
	revision  string
	session   string
	postURL   string
	legacySSE bool
	streaming bool
}

// newHTTPTransport creates a transport for the configured endpoint.
func newHTTPTransport(cfg Config) *httpTransport {
	return &httpTransport{
		cfg:    cfg,
		log:    cfg.logger().Tagged("MCP:" + cfg.ID),
		closed: make(chan struct{}),
	}
}

// Start validates the endpoint and prepares the HTTP client.
func (t *httpTransport) Start(ctx context.Context, handler func([]byte)) error {
	endpoint := strings.TrimSpace(t.cfg.URL)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("mcp: invalid url %q", endpoint)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("mcp: unsupported url scheme %q", parsed.Scheme)
	}
	t.handler = handler
	t.baseCtx, t.baseCancel = context.WithCancel(context.Background())
	t.client = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          8,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return nil
}

// Send posts one frame and feeds the reply to the client.
func (t *httpTransport) Send(ctx context.Context, frame []byte, extra map[string]string) error {
	select {
	case <-t.closed:
		return ErrClosed
	default:
	}

	resp, cancel, err := t.post(ctx, t.endpoint(), frame, extra)
	if err != nil {
		return err
	}

	if fallbackStatus(resp.StatusCode) && !t.isLegacySSE() {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		cancel()
		if rpcErr := parseRPCError(body); rpcErr != nil {
			return rpcErr
		}
		if probeErr := t.openLegacySSE(ctx); probeErr == nil {
			return t.Send(ctx, frame, extra)
		} else {
			t.log.Debug("Legacy http+sse probe failed: %v", probeErr)
		}
		return statusError(resp.StatusCode, body)
	}
	return t.consume(ctx, resp, cancel)
}

// post sends one frame to the endpoint.
func (t *httpTransport) post(ctx context.Context, endpoint string, frame []byte, extra map[string]string) (*http.Response, func(), error) {
	reqCtx, cancel := t.derivedContext(ctx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(frame))
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("mcp: build request: %w", err)
	}
	t.applyHeaders(req, frame, extra)

	resp, err := t.client.Do(req)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("mcp: post %s: %w", endpoint, err)
	}
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		t.setSession(session)
	}
	return resp, cancel, nil
}

// applyHeaders fills the protocol, authentication, and mirror headers.
func (t *httpTransport) applyHeaders(req *http.Request, frame []byte, extra map[string]string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range t.cfg.Headers {
		req.Header.Set(name, value)
	}

	revision, session, legacy := t.state()
	if isModernRevision(revision) {
		req.Header.Set("MCP-Protocol-Version", revision)
		for name, value := range deriveHeaders(frame) {
			req.Header.Set(name, value)
		}
	}
	if session != "" && !legacy {
		req.Header.Set("Mcp-Session-Id", session)
	}
	for name, value := range extra {
		req.Header.Set(name, value)
	}
}

// applyStreamHeaders fills the headers of a notification stream request.
func (t *httpTransport) applyStreamHeaders(req *http.Request) {
	req.Header.Set("Accept", "text/event-stream")
	for name, value := range t.cfg.Headers {
		req.Header.Set(name, value)
	}
	_, session, legacy := t.state()
	if session != "" && !legacy {
		req.Header.Set("Mcp-Session-Id", session)
	}
}

// consume dispatches the response body to the client.
func (t *httpTransport) consume(ctx context.Context, resp *http.Response, cancel func()) error {
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		cancel()
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		cancel()
		if rpcErr := parseRPCError(body); rpcErr != nil {
			return rpcErr
		}
		return statusError(resp.StatusCode, body)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		go t.consumeSSE(ctx, resp.Body, cancel, false)
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageSize))
	_ = resp.Body.Close()
	cancel()
	if err != nil {
		return fmt.Errorf("mcp: read response: %w", err)
	}
	return t.deliverBody(body)
}

// deliverBody hands one JSON object or a batch to the client.
func (t *httpTransport) deliverBody(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return fmt.Errorf("mcp: parse response batch: %w", err)
		}
		for _, item := range batch {
			t.handler(item)
		}
		return nil
	}
	var single json.RawMessage
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return fmt.Errorf("mcp: parse response: %w", err)
	}
	t.handler(single)
	return nil
}

// consumeSSE reads an event stream until it ends or the request is cancelled.
func (t *httpTransport) consumeSSE(ctx context.Context, body io.ReadCloser, cancel func(), legacy bool) {
	defer body.Close()
	defer cancel()

	reader := newSSEReader(body)
	for {
		event, data, err := reader.next()
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				t.log.Debug("Event stream ended: %v", err)
			}
			break
		}
		if event == "endpoint" || data == "" {
			continue
		}
		t.handler([]byte(data))
	}
	if legacy {
		t.markClosed()
	}
}

// openLegacySSE switches to the deprecated HTTP+SSE transport.
func (t *httpTransport) openLegacySSE(ctx context.Context) error {
	// The stream outlives the call that discovered it, so it is tied to the transport.
	reqCtx, cancel := context.WithCancel(t.baseCtx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, t.cfg.URL, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("mcp: build probe request: %w", err)
	}
	t.applyStreamHeaders(req)

	resp, err := t.client.Do(req)
	if err != nil {
		cancel()
		return fmt.Errorf("mcp: probe %s: %w", t.cfg.URL, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return fmt.Errorf("mcp: probe returned %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		_ = resp.Body.Close()
		cancel()
		return errors.New("mcp: probe is not an event stream")
	}

	reader := newSSEReader(resp.Body)
	endpoint := ""
	for attempt := 0; attempt < 100 && endpoint == ""; attempt++ {
		event, data, readErr := reader.nextCtx(ctx)
		if readErr != nil {
			_ = resp.Body.Close()
			cancel()
			return fmt.Errorf("mcp: read probe stream: %w", readErr)
		}
		if event == "endpoint" {
			endpoint = data
		}
	}
	if endpoint == "" {
		_ = resp.Body.Close()
		cancel()
		return errors.New("mcp: endpoint event is missing")
	}
	endpointURL, err := resolveEndpoint(t.cfg.URL, endpoint)
	if err != nil {
		_ = resp.Body.Close()
		cancel()
		return fmt.Errorf("mcp: resolve legacy endpoint: %w", err)
	}

	t.mu.Lock()
	t.postURL = endpointURL
	t.legacySSE = true
	t.mu.Unlock()
	t.log.Info("Using the legacy http+sse transport")

	go func() {
		defer cancel()
		defer resp.Body.Close()
		for {
			event, data, readErr := reader.next()
			if readErr != nil {
				break
			}
			if event == "endpoint" || data == "" {
				continue
			}
			t.handler([]byte(data))
		}
		t.markClosed()
	}()
	return nil
}

// StartStream opens the notification stream of the negotiated revision.
func (t *httpTransport) StartStream(ctx context.Context) error {
	if t.isLegacySSE() {
		return nil
	}
	t.mu.Lock()
	if t.streaming {
		t.mu.Unlock()
		return nil
	}
	t.streaming = true
	t.mu.Unlock()

	reqCtx, cancel := t.derivedContext(ctx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, t.cfg.URL, nil)
	if err != nil {
		cancel()
		t.resetStreaming()
		return fmt.Errorf("mcp: build stream request: %w", err)
	}
	t.applyStreamHeaders(req)

	resp, err := t.client.Do(req)
	if err != nil {
		cancel()
		t.resetStreaming()
		return fmt.Errorf("mcp: open notification stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		t.resetStreaming()
		if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("mcp: notification stream returned %d", resp.StatusCode)
	}
	go t.consumeSSE(ctx, resp.Body, cancel, false)
	return nil
}

// resetStreaming allows a later retry after a failed stream request.
func (t *httpTransport) resetStreaming() {
	t.mu.Lock()
	t.streaming = false
	t.mu.Unlock()
}

// SetRevision records the negotiated protocol revision for the headers.
func (t *httpTransport) SetRevision(revision string) {
	t.mu.Lock()
	t.revision = revision
	t.mu.Unlock()
}

// endpoint returns the current POST endpoint.
func (t *httpTransport) endpoint() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.postURL != "" {
		return t.postURL
	}
	return t.cfg.URL
}

// state returns the revision, session, and legacy flag together.
func (t *httpTransport) state() (string, string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.revision, t.session, t.legacySSE
}

// setSession stores the session id a legacy server assigned.
func (t *httpTransport) setSession(session string) {
	t.mu.Lock()
	t.session = session
	t.mu.Unlock()
}

// isLegacySSE reports whether the deprecated transport is in use.
func (t *httpTransport) isLegacySSE() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.legacySSE
}

// Closed reports that the connection ended.
func (t *httpTransport) Closed() <-chan struct{} {
	return t.closed
}

// Close cancels the streams and terminates a legacy session.
func (t *httpTransport) Close() error {
	t.closeOnce.Do(func() {
		if t.baseCancel != nil {
			t.baseCancel()
		}
		revision, session, legacy := t.state()
		if t.client != nil {
			if session != "" && !legacy && !isModernRevision(revision) {
				t.deleteSession(session)
			}
			t.client.CloseIdleConnections()
		}
		t.markClosed()
	})
	return nil
}

// deleteSession ends a legacy streamable HTTP session.
func (t *httpTransport) deleteSession(session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.cfg.URL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", session)
	resp, err := t.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// markClosed closes the transport closed channel once.
func (t *httpTransport) markClosed() {
	t.closedOnce.Do(func() { close(t.closed) })
}

// derivedContext cancels the request when either the caller or the transport ends.
func (t *httpTransport) derivedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	reqCtx, cancel := context.WithCancel(t.baseCtx)
	stop := context.AfterFunc(ctx, cancel)
	return reqCtx, func() {
		stop()
		cancel()
	}
}

// isModernRevision reports whether the revision uses per-request metadata.
func isModernRevision(revision string) bool {
	return revision >= RevisionModern
}

// fallbackStatus reports a status that may mean the endpoint is a legacy server.
func fallbackStatus(status int) bool {
	return status == http.StatusBadRequest || status == http.StatusNotFound || status == http.StatusMethodNotAllowed
}

// looksLikeRPCError reports whether the body is a JSON-RPC error response.
func looksLikeRPCError(body []byte) bool {
	return parseRPCError(body) != nil
}

// parseRPCError extracts a JSON-RPC error from a response body.
func parseRPCError(body []byte) *Error {
	var envelope struct {
		Error *Error `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &envelope); err != nil {
		return nil
	}
	if envelope.Error == nil || envelope.Error.Message == "" {
		return nil
	}
	return envelope.Error
}

// statusError builds an error for an unexpected HTTP status.
func statusError(status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	if len(message) > 300 {
		message = message[:300] + "..."
	}
	if message == "" {
		return fmt.Errorf("mcp: server returned %d", status)
	}
	return fmt.Errorf("mcp: server returned %d: %s", status, message)
}

// resolveEndpoint resolves a relative legacy endpoint against the connection URL.
func resolveEndpoint(base, reference string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	referenceURL, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(referenceURL).String(), nil
}

// sseReader parses Server-Sent Events one event at a time.
type sseReader struct {
	reader *bufio.Reader
	eof    bool
}

// newSSEReader wraps a response body.
func newSSEReader(body io.Reader) *sseReader {
	return &sseReader{reader: bufio.NewReaderSize(body, 64*1024)}
}

// next returns the next event name and data. A missing event name defaults to "message".
func (s *sseReader) next() (string, string, error) {
	if s.eof {
		return "", "", io.EOF
	}
	event := ""
	data := ""
	hasData := false

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			s.eof = true
			if hasData {
				return event, data, nil
			}
			return "", "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if hasData {
				return event, data, nil
			}
			event = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			event = value
		case "data":
			if hasData {
				data += "\n"
			}
			data += value
			hasData = true
		}
	}
}

// nextCtx reads the next event and gives up when the context ends.
func (s *sseReader) nextCtx(ctx context.Context) (string, string, error) {
	type result struct {
		event string
		data  string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		event, data, err := s.next()
		ch <- result{event: event, data: data, err: err}
	}()
	select {
	case res := <-ch:
		return res.event, res.data, res.err
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}
