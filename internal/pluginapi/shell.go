package pluginapi

// Running commands and making network requests.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ExecResult is the outcome of a command.
type ExecResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
	// TimedOut reports that the command was cut by the time limit.
	TimedOut bool `json:"timed_out"`
	// Truncated reports that the output was cut by the size limit.
	Truncated bool `json:"truncated"`
}

// Exec runs a shell command.
//
// The command goes through sh -c as one string, the way a user would write
// it. Output and time are capped so a stuck command cannot hold the turn.
func (a *API) Exec(command string) (*ExecResult, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("command is required")
	}
	if err := a.allow("system.shell.exec", "edit", "run: "+command); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(a.limits.TimeoutMS)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = a.root

	stdout := &cappedBuffer{limit: a.limits.MaxOutputBytes}
	stderr := &cappedBuffer{limit: a.limits.MaxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	res := &ExecResult{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: stdout.truncated || stderr.truncated,
	}

	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.Code = -1
		return res, nil
	}
	if err != nil {
		// A non-zero exit code is a result, not a failed call.
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.Code = exitErr.ExitCode()
			return res, nil
		}
		return nil, err
	}
	return res, nil
}

// cappedBuffer collects output up to a limit.
type cappedBuffer struct {
	limit     int
	buf       strings.Builder
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - c.buf.Len()
	if room <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }

// HTTPRequest configures a network request.
type HTTPRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	// TimeoutMS caps the request; zero means the default limit.
	TimeoutMS int `json:"timeout_ms"`
	// FollowRedirects allows redirects; enabled by default.
	FollowRedirects *bool `json:"follow_redirects"`
}

// HTTPResponse is a network reply.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	// Truncated reports that the body was cut by the size limit.
	Truncated bool `json:"truncated"`
}

// HTTPClient serves plugin requests.
//
// A separate client keeps plugin settings from changing the behaviour of the
// rest of the program.
var HTTPClient = &http.Client{
	// Redirects are handled by hand to enforce a depth limit.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		// Another scheme is refused, so file:// and the like never reach
		// the handler.
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
		}
		return nil
	},
}

// HTTP performs a network request.
func (a *API) HTTP(req HTTPRequest) (*HTTPResponse, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		return nil, fmt.Errorf("url must start with http:// or https://")
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead:
	default:
		return nil, fmt.Errorf("method %q is not supported", method)
	}

	if err := a.allow("system.http.request", "edit", method+" "+req.URL); err != nil {
		return nil, err
	}

	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(a.limits.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return nil, err
	}

	// Compression is requested explicitly: API replies can be large.
	httpReq.Header.Set("Accept-Encoding", "gzip")
	for name, value := range req.Headers {
		httpReq.Header.Set(name, value)
	}

	client := HTTPClient
	if req.FollowRedirects != nil && !*req.FollowRedirects {
		noRedirect := *HTTPClient
		noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		client = &noRedirect
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(a.limits.MaxOutputBytes)))
	if err != nil {
		return nil, err
	}

	headers := make(map[string]string, len(resp.Header))
	for name, values := range resp.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}

	// The transport decompresses only what it asked for itself, so an
	// explicit header leaves the body compressed.
	return &HTTPResponse{
		Status:    resp.StatusCode,
		Headers:   headers,
		Body:      string(data),
		Truncated: int64(len(data)) >= int64(a.limits.MaxOutputBytes),
	}, nil
}
