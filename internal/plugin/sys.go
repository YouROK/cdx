package plugin

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SysAPI provides low-level host capabilities (files, process execution, HTTP/SSE fetch) to plugins.
type SysAPI struct {
	HTTPClient    *http.Client
	MaxRetries    int
	RetryWaitBase time.Duration
}

// NewSysAPI creates a SysAPI instance.
func NewSysAPI() *SysAPI {
	return &SysAPI{
		HTTPClient:    &http.Client{Timeout: 0},
		MaxRetries:    10,
		RetryWaitBase: time.Second,
	}
}

// ReadFile reads a file as a UTF-8 string.
func (s *SysAPI) ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteFile writes content to path, creating parent directories if needed.
func (s *SysAPI) WriteFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// Stat returns basic file metadata.
func (s *SysAPI) Stat(path string) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":   info.Name(),
		"size":   info.Size(),
		"is_dir": info.IsDir(),
		"mode":   info.Mode().String(),
	}, nil
}

// ReadDir lists directory entries.
func (s *SysAPI) ReadDir(path string) ([]map[string]any, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"name":   e.Name(),
			"is_dir": e.IsDir(),
		})
	}
	return out, nil
}

// Exec runs a shell command and returns stdout, stderr, and exit_code.
func (s *SysAPI) Exec(ctx context.Context, command, workdir string, timeoutMS int) (map[string]any, error) {
	if timeoutMS <= 0 {
		timeoutMS = 60000
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	if workdir != "" {
		cmd.Dir = workdir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}

	return map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": exitCode,
		"timed_out": runCtx.Err() == context.DeadlineExceeded,
		"cancelled": runCtx.Err() == context.Canceled,
	}, nil
}

// RandomHex returns n cryptographically random bytes as a lowercase hex string.
func (s *SysAPI) RandomHex(n int) (string, error) {
	if n < 1 || n > 64 {
		return "", fmt.Errorf("random length must be between 1 and 64 bytes")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Env returns the value of an environment variable.
func (s *SysAPI) Env(name string) string {
	return os.Getenv(name)
}

// Fetch performs an HTTP request. If onLine is non-nil, the response body is read line by line (useful for SSE streaming).
func (s *SysAPI) Fetch(ctx context.Context, url string, opts map[string]any, onLine func(string) error) (map[string]any, error) {
	method := "GET"
	if m, ok := opts["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	bodyStr, _ := opts["body"].(string)

	timeout := 120 * time.Second
	if t, ok := opts["timeout_ms"].(int64); ok && t > 0 {
		timeout = time.Duration(t) * time.Millisecond
	} else if t, ok := opts["timeout_ms"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Millisecond
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	headers := map[string]string{}
	if hdrs, ok := opts["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			headers[k] = fmt.Sprint(v)
		}
	}

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	retries := s.MaxRetries
	if retries <= 0 {
		retries = 10
	}
	wait := s.RetryWaitBase
	if wait <= 0 {
		wait = time.Second
	}

	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			select {
			case <-reqCtx.Done():
				return nil, reqCtx.Err()
			case <-time.After(wait):
			}
			wait *= 2
			if wait > 10*time.Second {
				wait = 10 * time.Second
			}
		}

		var bodyReader io.Reader
		if bodyStr != "" {
			bodyReader = strings.NewReader(bodyStr)
		}
		req, err := http.NewRequestWithContext(reqCtx, method, url, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if reqCtx.Err() != nil {
				return nil, reqCtx.Err()
			}
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, shortResponseBody(resp.Body))
			if delay, ok := retryAfter(resp); ok {
				wait = delay
			}
			_ = resp.Body.Close()
			continue
		}

		respHeaders := make(map[string]string, len(resp.Header))
		for k, vals := range resp.Header {
			if len(vals) > 0 {
				respHeaders[strings.ToLower(k)] = vals[0]
			}
		}

		if onLine != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			scanner := bufio.NewScanner(resp.Body)
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			for scanner.Scan() {
				if err := onLine(scanner.Text()); err != nil {
					_ = resp.Body.Close()
					return nil, err
				}
			}
			if err := scanner.Err(); err != nil {
				_ = resp.Body.Close()
				return nil, err
			}
			_ = resp.Body.Close()
			return map[string]any{
				"status":  resp.StatusCode,
				"ok":      true,
				"headers": respHeaders,
				"body":    "",
			}, nil
		}

		rawBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"status":  resp.StatusCode,
			"ok":      resp.StatusCode >= 200 && resp.StatusCode < 300,
			"headers": respHeaders,
			"body":    string(rawBody),
		}, nil
	}

	return nil, fmt.Errorf("request failed after %d attempts: %w", retries, lastErr)
}

func shortResponseBody(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, 1024))
	text := strings.TrimSpace(string(data))
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return text
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		delay := time.Duration(seconds) * time.Second
		if delay > 10*time.Second {
			delay = 10 * time.Second
		}
		return delay, true
	}
	if when, err := http.ParseTime(raw); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			delay = 0
		}
		if delay > 10*time.Second {
			delay = 10 * time.Second
		}
		return delay, true
	}
	return 0, false
}
