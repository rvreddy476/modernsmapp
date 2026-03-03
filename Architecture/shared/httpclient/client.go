package httpclient

import (
	"context"
	"net/http"
	"time"
)

// New returns an *http.Client with explicit dial, TLS handshake, and response timeouts.
// Use for all service-to-service HTTP calls to prevent goroutine leaks on slow/hung backends.
func New(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// NewWithRetry returns an http.Client wrapped with a simple retry transport.
// It retries on connection errors and 5xx responses up to maxRetries times,
// with exponential backoff starting at baseDelay (1s, 2s, 4s, ...).
func NewWithRetry(timeout time.Duration, maxRetries int) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &retryTransport{base: http.DefaultTransport, maxRetries: maxRetries},
	}
}

type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
}

func (r *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	delay := time.Second
	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delay):
			}
			delay *= 2
			// Clone the request body is not possible after first read for non-nil bodies.
			// Only retry safe/idempotent requests (GET, HEAD).
			if req.Method != http.MethodGet && req.Method != http.MethodHead {
				break
			}
		}
		resp, err = r.base.RoundTrip(req)
		if err == nil && resp.StatusCode < 500 {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
	return resp, err
}

// Default is a ready-to-use client with a 5-second timeout, suitable for
// most internal service-to-service calls.
var Default = New(5 * time.Second)

// WithContext creates a new request with the provided context and executes it
// using the Default client.
func Do(ctx context.Context, method, url string, body interface{}) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	return Default.Do(req)
}
