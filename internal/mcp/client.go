package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/version"
)

// ErrClosed reports a connection that ended before the request completed.
var ErrClosed = errors.New("mcp: connection closed")

// errFallbackLegacy marks a server that does not implement the modern discovery call.
var errFallbackLegacy = errors.New("mcp: server does not speak the modern protocol")

// maxInputRounds bounds the retries of a multi round-trip request.
const maxInputRounds = 5

// rpcResult carries a reply from the reader goroutine to a waiting request.
type rpcResult struct {
	result json.RawMessage
	err    error
}

// InputRequest is one server request inside an input-required result.
type InputRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// InputHandler gathers the answers a server asks for during a multi round-trip request.
type InputHandler func(ctx context.Context, requests map[string]InputRequest) (map[string]any, error)

// streamStarter is implemented by transports with a server notification stream.
type streamStarter interface {
	StartStream(ctx context.Context) error
}

// Client is one live MCP connection.
type Client struct {
	cfg    Config
	log    *logx.Scope
	tr     transport
	isHTTP bool

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan rpcResult
	tools   map[string]Tool
	sem     chan struct{}

	infoMu sync.RWMutex
	info   ServerInfo

	notifyMu sync.RWMutex
	onNotify func(method string, params json.RawMessage)

	inputMu sync.RWMutex
	onInput InputHandler

	done      chan struct{}
	closeOnce sync.Once
}

// Dial connects to the configured server and completes protocol discovery.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.ID) == "" {
		return nil, errors.New("mcp: server id is required")
	}
	cfg.Transport = strings.ToLower(strings.TrimSpace(cfg.Transport))
	if cfg.Transport == "" {
		cfg.Transport = TransportStdio
	}
	c := &Client{
		cfg:     cfg,
		log:     cfg.logger().Tagged("MCP:" + cfg.ID),
		pending: map[string]chan rpcResult{},
		tools:   map[string]Tool{},
		sem:     make(chan struct{}, maxInFlight),
		done:    make(chan struct{}),
	}
	c.isHTTP = cfg.Transport == TransportHTTP

	switch cfg.Transport {
	case TransportStdio:
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, errors.New("mcp: command is required for the stdio transport")
		}
		c.tr = newStdioTransport(cfg)
	case TransportHTTP:
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, errors.New("mcp: url is required for the http transport")
		}
		c.tr = newHTTPTransport(cfg)
	default:
		return nil, fmt.Errorf("mcp: unknown transport %q", cfg.Transport)
	}

	startCtx, cancel := context.WithTimeout(ctx, StartTimeout)
	defer cancel()
	if err := c.tr.Start(startCtx, c.deliver); err != nil {
		return nil, err
	}
	go c.watch()
	if err := c.initialize(startCtx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// initialize discovers the server and settles on a protocol revision.
func (c *Client) initialize(ctx context.Context) error {
	protocol := strings.TrimSpace(c.cfg.Protocol)
	if protocol == "" || strings.EqualFold(protocol, "auto") {
		err := c.initializeModern(ctx, RevisionModern)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errFallbackLegacy) {
			return err
		}
		return c.initializeLegacy(ctx, RevisionLegacy)
	}
	if isModernRevision(protocol) {
		return c.initializeModern(ctx, protocol)
	}
	return c.initializeLegacy(ctx, protocol)
}

// initializeModern runs server/discover and stores the advertised capabilities.
func (c *Client) initializeModern(ctx context.Context, revision string) error {
	c.setRevision(revision)
	raw, err := c.request(ctx, "server/discover", map[string]any{"_meta": c.meta(revision)}, nil)
	if err != nil {
		var rpcErr *Error
		if errors.As(err, &rpcErr) && (rpcErr.IsMethodNotFound() || rpcErr.Code == -32602 || strings.Contains(strings.ToLower(rpcErr.Message), "protocol version")) {
			return errFallbackLegacy
		}
		return err
	}

	var res struct {
		ResultType        string         `json:"resultType"`
		SupportedVersions []string       `json:"supportedVersions"`
		Capabilities      map[string]any `json:"capabilities"`
		ServerInfo        *ServerInfo    `json:"serverInfo"`
		Instructions      string         `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp: parse server/discover: %w", err)
	}

	info := ServerInfo{
		ProtocolVersion: revision,
		Revision:        revision,
		Capabilities:    res.Capabilities,
		Instructions:    res.Instructions,
	}
	if res.ServerInfo != nil {
		info.Name = res.ServerInfo.Name
		info.Title = res.ServerInfo.Title
		info.Version = res.ServerInfo.Version
	}
	c.setInfo(info)
	c.log.Info("Connected to %s %s using revision %s", info.Name, info.Version, revision)
	return nil
}

// initializeLegacy performs the initialize handshake used through revision 2025-11-25.
func (c *Client) initializeLegacy(ctx context.Context, revision string) error {
	c.setRevision(revision)
	raw, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": revision,
		"capabilities":    c.clientCapabilities(),
		"clientInfo":      clientInfo(),
	}, nil)
	if err != nil {
		return err
	}

	var res struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      *ServerInfo    `json:"serverInfo"`
		Instructions    string         `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp: parse initialize: %w", err)
	}

	info := ServerInfo{
		ProtocolVersion: res.ProtocolVersion,
		Revision:        revision,
		Capabilities:    res.Capabilities,
		Instructions:    res.Instructions,
	}
	if res.ServerInfo != nil {
		info.Name = res.ServerInfo.Name
		info.Title = res.ServerInfo.Title
		info.Version = res.ServerInfo.Version
	}
	c.setInfo(info)

	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return err
	}
	c.log.Info("Connected to %s %s using revision %s", info.Name, info.Version, revision)
	return nil
}

// meta builds the per-request metadata of the modern protocol.
func (c *Client) meta(revision string) map[string]any {
	if revision == "" {
		revision = RevisionModern
	}
	return map[string]any{
		"io.modelcontextprotocol/protocolVersion":    revision,
		"io.modelcontextprotocol/clientCapabilities": c.clientCapabilities(),
		"io.modelcontextprotocol/clientInfo":         clientInfo(),
	}
}

// clientCapabilities lists the client features advertised to servers.
func (c *Client) clientCapabilities() map[string]any {
	return map[string]any{
		"roots":       map[string]any{"listChanged": false},
		"elicitation": map[string]any{},
	}
}

// clientInfo identifies cdx to the server.
func clientInfo() map[string]any {
	return map[string]any{"name": "cdx", "version": version.Version}
}

// request sends one JSON-RPC request and waits for its reply.
func (c *Client) request(ctx context.Context, method string, params any, headers map[string]string) (json.RawMessage, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.closedError()
	}
	defer func() { <-c.sem }()

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	idRaw, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	frame, err := requestFrame(idRaw, method, params)
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%d", id)
	ch := make(chan rpcResult, 1)
	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()

	waitCtx, cancel := context.WithTimeout(ctx, c.cfg.timeout())
	defer cancel()

	if err := c.tr.Send(waitCtx, frame, headers); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		return res.result, res.err
	case <-waitCtx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		c.cancelRequest(id, waitCtx.Err())
		return nil, fmt.Errorf("mcp: %s: %w", method, waitCtx.Err())
	case <-c.done:
		return nil, c.closedError()
	}
}

// call sends a request with the metadata the negotiated revision requires.
func (c *Client) call(ctx context.Context, method string, params map[string]any, headers map[string]string) (json.RawMessage, error) {
	if c.Revision() == RevisionModern {
		merged := make(map[string]any, len(params)+1)
		for key, value := range params {
			merged[key] = value
		}
		merged["_meta"] = c.meta(RevisionModern)
		params = merged
	}
	return c.request(ctx, method, params, headers)
}

// callWithInput runs a request and answers the input requests a server may return.
func (c *Client) callWithInput(ctx context.Context, method string, params map[string]any, headers map[string]string) (json.RawMessage, error) {
	current := params
	for round := 0; round < maxInputRounds; round++ {
		raw, err := c.call(ctx, method, current, headers)
		if err != nil {
			return nil, err
		}
		var probe struct {
			ResultType    string                  `json:"resultType"`
			InputRequests map[string]InputRequest `json:"inputRequests"`
			RequestState  string                  `json:"requestState"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("mcp: parse %s result: %w", method, err)
		}
		if probe.ResultType != "input_required" {
			return raw, nil
		}

		next := make(map[string]any, len(params)+2)
		for key, value := range params {
			next[key] = value
		}
		if probe.RequestState != "" {
			next["requestState"] = probe.RequestState
		}
		if len(probe.InputRequests) > 0 {
			handler := c.inputHandler()
			if handler == nil {
				return nil, fmt.Errorf("mcp: %s needs client input, which is not supported", method)
			}
			responses, err := handler(ctx, probe.InputRequests)
			if err != nil {
				return nil, err
			}
			next["inputResponses"] = responses
		}
		current = next
	}
	return nil, fmt.Errorf("mcp: %s kept asking for input", method)
}

// notify sends a notification without waiting for a reply.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	frame, err := requestFrame(nil, method, params)
	if err != nil {
		return err
	}
	return c.tr.Send(ctx, frame, nil)
}

// Subscribe opens the server notification stream of the negotiated revision.
func (c *Client) Subscribe(ctx context.Context, filters map[string]any) error {
	if c.Revision() != RevisionModern {
		starter, ok := c.tr.(streamStarter)
		if !ok {
			return nil
		}
		return starter.StartStream(ctx)
	}
	if filters == nil {
		filters = map[string]any{}
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	idRaw, err := json.Marshal(id)
	if err != nil {
		return err
	}
	params := map[string]any{"notifications": filters, "_meta": c.meta(RevisionModern)}
	frame, err := requestFrame(idRaw, "subscriptions/listen", params)
	if err != nil {
		return err
	}
	return c.tr.Send(ctx, frame, nil)
}

// closedError explains why the connection ended.
func (c *Client) closedError() error {
	reporter, ok := c.tr.(failureReporter)
	if !ok {
		return ErrClosed
	}
	exitErr, lines := reporter.Failure()
	detail := strings.TrimSpace(strings.Join(lines, "; "))
	if len(detail) > 400 {
		detail = detail[:400] + "..."
	}
	switch {
	case exitErr != nil && detail != "":
		return fmt.Errorf("%w: server exited (%v): %s", ErrClosed, exitErr, detail)
	case exitErr != nil:
		return fmt.Errorf("%w: server exited (%v)", ErrClosed, exitErr)
	case detail != "":
		return fmt.Errorf("%w: server closed the connection: %s", ErrClosed, detail)
	}
	return ErrClosed
}

// cancelRequest tells the server that a request was abandoned.
func (c *Client) cancelRequest(id int64, cause error) {
	if c.isHTTP {
		return
	}
	frame, err := requestFrame(nil, "notifications/cancelled", map[string]any{
		"requestId": id,
		"reason":    cause.Error(),
	})
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.tr.Send(ctx, frame, nil)
	}()
}

// deliver routes one received frame to its request, to a server request handler, or to the notification handler.
func (c *Client) deliver(frame []byte) {
	var msg message
	if err := json.Unmarshal(frame, &msg); err != nil {
		c.log.Debug("Ignoring malformed frame: %v", err)
		return
	}
	switch {
	case msg.Method != "" && len(msg.ID) > 0:
		c.answerServerRequest(msg)
	case len(msg.ID) > 0:
		key := string(msg.ID)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch == nil {
			c.log.Debug("Ignoring reply for unknown request %s", key)
			return
		}
		res := rpcResult{result: msg.Result}
		if msg.Error != nil {
			res.err = msg.Error
		}
		ch <- res
	case msg.Method != "":
		c.handleNotification(msg.Method, msg.Params)
	}
}

// answerServerRequest replies to the requests a legacy server may send to the client.
func (c *Client) answerServerRequest(msg message) {
	switch msg.Method {
	case "roots/list":
		c.respond(msg.ID, map[string]any{"roots": c.roots()}, nil)
	case "ping":
		c.respond(msg.ID, map[string]any{}, nil)
	default:
		c.respond(msg.ID, nil, &Error{Code: -32601, Message: "method not found: " + msg.Method})
	}
}

// respond sends a reply to a server request.
func (c *Client) respond(id json.RawMessage, result any, rpcErr *Error) {
	frame, err := responseFrame(id, result, rpcErr)
	if err != nil {
		c.log.Debug("Failed to encode a reply: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.tr.Send(ctx, frame, nil); err != nil {
		c.log.Debug("Failed to send a reply: %v", err)
	}
}

// roots advertises the working directory of the turn.
func (c *Client) roots() []map[string]string {
	root := strings.TrimSpace(c.cfg.Root)
	if root == "" {
		return []map[string]string{}
	}
	uri := (&url.URL{Scheme: "file", Path: root}).String()
	return []map[string]string{{"uri": uri, "name": filepath.Base(root)}}
}

// handleNotification forwards a server notification to the registered handler.
func (c *Client) handleNotification(method string, params json.RawMessage) {
	c.notifyMu.RLock()
	handler := c.onNotify
	c.notifyMu.RUnlock()
	if handler != nil {
		handler(method, params)
	}
}

// SetNotificationHandler installs the callback for server notifications. The callback
// runs on the reader goroutine, so it must return quickly.
func (c *Client) SetNotificationHandler(handler func(method string, params json.RawMessage)) {
	c.notifyMu.Lock()
	c.onNotify = handler
	c.notifyMu.Unlock()
}

// SetInputHandler installs the callback that answers input requests. The callback
// runs on the caller goroutine and may ask the user.
func (c *Client) SetInputHandler(handler InputHandler) {
	c.inputMu.Lock()
	c.onInput = handler
	c.inputMu.Unlock()
}

// inputHandler returns the registered input callback.
func (c *Client) inputHandler() InputHandler {
	c.inputMu.RLock()
	defer c.inputMu.RUnlock()
	return c.onInput
}

// watch closes the client when the transport ends.
func (c *Client) watch() {
	select {
	case <-c.tr.Closed():
		c.shutdown()
	case <-c.done:
	}
}

// shutdown marks the client closed.
func (c *Client) shutdown() {
	c.closeOnce.Do(func() { close(c.done) })
}

// Close stops the connection and its server process.
func (c *Client) Close() error {
	c.shutdown()
	return c.tr.Close()
}

// IsClosed reports whether the connection has ended.
func (c *Client) IsClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Info returns what the server reported about itself.
func (c *Client) Info() ServerInfo {
	c.infoMu.RLock()
	defer c.infoMu.RUnlock()
	return c.info
}

// Revision returns the negotiated protocol revision.
func (c *Client) Revision() string {
	return c.Info().Revision
}

// Capabilities returns the capabilities the server declared.
func (c *Client) Capabilities() map[string]any {
	return c.Info().Capabilities
}

// HasCapability reports whether the server declared a capability.
func (c *Client) HasCapability(name string) bool {
	caps := c.Capabilities()
	value, ok := caps[name]
	if !ok {
		return false
	}
	return value != nil
}

// Config returns the connection configuration.
func (c *Client) Config() Config {
	return c.cfg
}

// Tool returns a cached tool definition.
func (c *Client) Tool(name string) (Tool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tool, ok := c.tools[name]
	return tool, ok
}

// ListTools returns the whole tool catalog, following pagination cursors.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if !c.HasCapability("tools") {
		return nil, nil
	}
	var tools []Tool
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params, nil)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("mcp: parse tools/list: %w", err)
		}
		for _, tool := range res.Tools {
			if c.isHTTP {
				if err := validateToolHeaders(tool); err != nil {
					c.log.Warn("Excluding tool %q: %v", tool.Name, err)
					continue
				}
			}
			tools = append(tools, tool)
			c.mu.Lock()
			c.tools[tool.Name] = tool
			c.mu.Unlock()
		}
		if res.NextCursor == "" {
			return tools, nil
		}
		cursor = res.NextCursor
	}
	return tools, nil
}

// ListResources returns the resource catalog, following pagination cursors.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	if !c.HasCapability("resources") {
		return nil, nil
	}
	var resources []Resource
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "resources/list", params, nil)
		if err != nil {
			return nil, err
		}
		var res struct {
			Resources  []Resource `json:"resources"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("mcp: parse resources/list: %w", err)
		}
		resources = append(resources, res.Resources...)
		if res.NextCursor == "" {
			return resources, nil
		}
		cursor = res.NextCursor
	}
	return resources, nil
}

// ReadResource reads one resource by URI.
func (c *Client) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	if strings.TrimSpace(uri) == "" {
		return nil, errors.New("mcp: resource uri is required")
	}
	if !c.HasCapability("resources") {
		return nil, errors.New("mcp: the server does not expose resources")
	}
	raw, err := c.callWithInput(ctx, "resources/read", map[string]any{"uri": uri}, nil)
	if err != nil {
		return nil, err
	}
	var res ReadResourceResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp: parse resources/read: %w", err)
	}
	return &res, nil
}

// CallTool invokes one tool and returns its result.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallResult, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("mcp: tool name is required")
	}
	if args == nil {
		args = map[string]any{}
	}
	var headers map[string]string
	if c.isHTTP {
		if tool, ok := c.Tool(name); ok {
			headers = toolHeaderValues(&tool, args)
		}
	}
	raw, err := c.callWithInput(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, headers)
	if err != nil {
		return nil, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/call: %w", err)
	}
	return &res, nil
}

// setInfo stores the discovery result.
func (c *Client) setInfo(info ServerInfo) {
	c.infoMu.Lock()
	c.info = info
	c.infoMu.Unlock()
}

// setRevision tells a transport which revision the headers must mirror.
func (c *Client) setRevision(revision string) {
	if setter, ok := c.tr.(revisionSetter); ok {
		setter.SetRevision(revision)
	}
}
