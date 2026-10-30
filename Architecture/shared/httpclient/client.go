package httpclient

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
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

// BreakerTransport implements a 3-state circuit breaker (Closed -> Open ->
// Half-Open), kept separately for each upstream host (req.URL.Host), so one
// failing upstream never refuses calls to another.
//
// threshold consecutive upstream failures open a host's breaker for timeout;
// after that one probe is let through (half-open): success closes it, failure
// opens it again.
//
// Only real upstream failures count: connection errors, the per-request
// timeout this transport imposes itself (requestTimeout), and 5xx responses.
// A caller's own context cancellation or deadline is the caller's choice, not
// evidence the upstream is down, and leaves the breaker untouched.
type BreakerTransport struct {
	base           http.RoundTripper
	name           string
	threshold      int
	timeout        time.Duration
	requestTimeout time.Duration // 0 = no transport-imposed timeout

	mu    sync.Mutex
	hosts map[string]*breakerHost
}

// breakerHost is the breaker state for one upstream host, guarded by
// BreakerTransport.mu.
type breakerHost struct {
	failures  int
	openUntil time.Time // zero = closed
	probing   bool      // a half-open probe is in flight
}

// upstreamTimeoutError is the context cause set when the transport's own
// per-request timeout fires. It still reports as a timeout and matches
// context.DeadlineExceeded so callers' existing checks keep working.
type upstreamTimeoutError struct{ name string }

func (e *upstreamTimeoutError) Error() string {
	return "upstream timeout (" + e.name + "): context deadline exceeded"
}
func (e *upstreamTimeoutError) Timeout() bool   { return true }
func (e *upstreamTimeoutError) Temporary() bool { return true }
func (e *upstreamTimeoutError) Is(target error) bool {
	return target == context.DeadlineExceeded
}

type outcome int

const (
	outcomeSuccess outcome = iota
	outcomeFailure
	outcomeCallerGone // caller cancelled or its own deadline expired: not counted
)

// acquire decides whether a request to host may proceed. probe reports
// whether this request is the single half-open probe.
func (b *BreakerTransport) acquire(host string, now time.Time) (ok, probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hosts == nil {
		b.hosts = make(map[string]*breakerHost)
	}
	h := b.hosts[host]
	if h == nil {
		h = &breakerHost{}
		b.hosts[host] = h
	}
	if h.openUntil.IsZero() {
		return true, false
	}
	if now.Before(h.openUntil) || h.probing {
		return false, false
	}
	h.probing = true
	return true, true
}

// record applies a finished request's outcome to host's state.
func (b *BreakerTransport) record(host string, probe bool, o outcome) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.hosts[host]
	if probe {
		h.probing = false
		switch o {
		case outcomeSuccess:
			h.failures = 0
			h.openUntil = time.Time{}
			slog.Info("circuit breaker closed", "name", b.name, "host", host)
		case outcomeFailure:
			h.failures = 0
			h.openUntil = time.Now().Add(b.timeout)
			slog.Warn("circuit breaker re-opened after failed probe", "name", b.name, "host", host)
		}
		// outcomeCallerGone: the probe told us nothing; the next request probes.
		return
	}
	if !h.openUntil.IsZero() {
		// Started while closed, finished after the breaker opened: the open
		// state is newer information, leave it to the probe.
		return
	}
	switch o {
	case outcomeSuccess:
		h.failures = 0
	case outcomeFailure:
		h.failures++
		if h.failures >= b.threshold {
			h.failures = 0
			h.openUntil = time.Now().Add(b.timeout)
			slog.Warn("circuit breaker opened", "name", b.name, "host", host)
		}
	}
}

func (b *BreakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	ok, probe := b.acquire(host, time.Now())
	if !ok {
		return nil, fmt.Errorf("circuit breaker open: %s (%s)", b.name, host)
	}

	callerCtx := req.Context()
	cancel := context.CancelFunc(func() {})
	if b.requestTimeout > 0 {
		var ctx context.Context
		ctx, cancel = context.WithTimeoutCause(callerCtx, b.requestTimeout, &upstreamTimeoutError{name: b.name})
		req = req.WithContext(ctx)
	}

	resp, err := b.base.RoundTrip(req)
	switch {
	case err != nil:
		cancel()
		if callerCtx.Err() != nil {
			// The caller gave up (cancel or its own deadline); whatever the
			// transport returned, the upstream is not to blame.
			b.record(host, probe, outcomeCallerGone)
		} else {
			b.record(host, probe, outcomeFailure)
		}
		return nil, err
	case resp.StatusCode >= 500:
		b.record(host, probe, outcomeFailure)
	default:
		b.record(host, probe, outcomeSuccess)
	}
	// The timeout keeps covering the body read, as http.Client.Timeout does,
	// and is released when the body is done.
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnCloseBody releases the per-request timeout once the body is done.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnCloseBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if err == io.EOF {
		c.cancel()
	}
	return n, err
}

func (c *cancelOnCloseBody) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// NewWithBreaker returns an *http.Client with a per-host circuit breaker that
// opens for 30s after 5 consecutive upstream failures.
// Use name for logging/identification (e.g., "group->chat").
//
// The timeout is enforced by the breaker transport (covering the whole
// request including the body read, like http.Client.Timeout), not by
// http.Client.Timeout, so the breaker can tell its own timeout (an upstream
// failure) apart from the caller's context deadline (not a failure).
func NewWithBreaker(timeout time.Duration, name string) *http.Client {
	return &http.Client{
		Transport: newBreakerTransport(&http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		}, name, timeout),
	}
}

func newBreakerTransport(base http.RoundTripper, name string, requestTimeout time.Duration) *BreakerTransport {
	return &BreakerTransport{
		base:           base,
		name:           name,
		threshold:      5,
		timeout:        30 * time.Second,
		requestTimeout: requestTimeout,
	}
}

// WithContext creates a new request with the provided context and executes it
// using the Default client.
func Do(ctx context.Context, method, url string, body interface{}) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	return Default.Do(req)
}
