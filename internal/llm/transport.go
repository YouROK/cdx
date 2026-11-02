package llm

// Shared HTTP behaviour for the protocol clients.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultRetries is how many times a transient error is retried.
//
// Free gateways often answer 429 when busy: a single attempt almost always
// hits the limit, while several in a row wait out the load.
const defaultRetries = 10

// maxRetryWait caps the wait between retries.
const maxRetryWait = 10 * time.Second

// sender posts JSON to one endpoint and retries transient errors.
type sender struct {
	client        *http.Client
	endpoint      string
	headers       map[string]string
	maxRetries    int
	retryWaitBase time.Duration
}

// post sends the body, retrying a busy or failing server.
//
// The returned response belongs to the caller: a retried attempt closes the
// previous one before trying again.
func (s *sender) post(ctx context.Context, body []byte) (*http.Response, error) {
	retries := s.maxRetries
	if retries <= 0 {
		retries = defaultRetries
	}
	wait := s.retryWaitBase
	if wait <= 0 {
		wait = time.Second
	}

	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			// Growth is capped: otherwise by the tenth attempt the wait would
			// reach eight minutes and the turn timeout would hit first.
			wait *= 2
			if wait > maxRetryWait {
				wait = maxRetryWait
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		for name, value := range s.headers {
			req.Header.Set(name, value)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, retryHint(resp))
			// The server may state its own retry delay: it wins over the
			// computed one, but is still capped.
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if sec, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && sec > 0 {
					delay := time.Duration(sec) * time.Second
					if delay > maxRetryWait {
						delay = maxRetryWait
					}
					wait = delay
				}
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("model request failed after %d attempts: %w", retries, lastErr)
}

// retryHint extracts the error message for a retry.
func retryHint(resp *http.Response) string {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Message != "" {
		msg := payload.Error.Message
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		return msg
	}
	return shortBody(body)
}

// shortBody clips the response body for an error message.
func shortBody(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
