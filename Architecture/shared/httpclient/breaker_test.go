package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slowServer answers after delay (or when the client goes away).
func slowServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func statusServer(t *testing.T, status *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(ctx context.Context, c *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return errors.New(resp.Status)
	}
	return nil
}

func isBreakerOpen(err error) bool {
	return err != nil && strings.Contains(err.Error(), "circuit breaker open")
}

func breakerClient(requestTimeout, openFor time.Duration) (*http.Client, *BreakerTransport) {
	c := NewWithBreaker(requestTimeout, "test")
	bt := c.Transport.(*BreakerTransport)
	bt.timeout = openFor
	return c, bt
}

func TestBreaker_CallerCancelDoesNotOpen(t *testing.T) {
	srv := slowServer(t, 300*time.Millisecond)
	c, _ := breakerClient(5*time.Second, time.Minute)

	// Cancelled mid-flight.
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(10*time.Millisecond, cancel)
		if err := get(ctx, c, srv.URL); !errors.Is(err, context.Canceled) {
			t.Fatalf("call %d: want context.Canceled, got %v", i, err)
		}
	}
	// Cancelled before the call.
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := get(ctx, c, srv.URL); isBreakerOpen(err) {
			t.Fatalf("pre-cancelled call %d refused by breaker: %v", i, err)
		}
	}
	if err := get(context.Background(), c, srv.URL); err != nil {
		t.Fatalf("breaker opened on caller cancellations: %v", err)
	}
}

func TestBreaker_CallerDeadlineDoesNotOpen(t *testing.T) {
	srv := slowServer(t, 300*time.Millisecond)
	c, _ := breakerClient(5*time.Second, time.Minute)

	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		err := get(ctx, c, srv.URL)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("call %d: want caller deadline, got %v", i, err)
		}
	}
	if err := get(context.Background(), c, srv.URL); err != nil {
		t.Fatalf("breaker opened on caller deadlines: %v", err)
	}
}

func TestBreaker_OwnTimeoutOpens(t *testing.T) {
	srv := slowServer(t, 2*time.Second)
	c, _ := breakerClient(20*time.Millisecond, time.Minute)

	for i := 0; i < 5; i++ {
		err := get(context.Background(), c, srv.URL)
		if isBreakerOpen(err) || err == nil {
			t.Fatalf("call %d: want upstream timeout, got %v", i, err)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("call %d: transport timeout should still match DeadlineExceeded, got %v", i, err)
		}
		var ne interface{ Timeout() bool }
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("call %d: transport timeout should report Timeout(), got %v", i, err)
		}
	}
	if err := get(context.Background(), c, srv.URL); !isBreakerOpen(err) {
		t.Fatalf("want breaker open after 5 transport timeouts, got %v", err)
	}
}

func TestBreaker_5xxOpensAndSuccessResets(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	srv := statusServer(t, &status)
	c, _ := breakerClient(time.Second, time.Minute)

	// 4 failures, a success, 4 failures: never 5 consecutive, stays closed.
	for round := 0; round < 2; round++ {
		status.Store(http.StatusServiceUnavailable)
		for i := 0; i < 4; i++ {
			if err := get(context.Background(), c, srv.URL); isBreakerOpen(err) {
				t.Fatalf("round %d call %d: opened early", round, i)
			}
		}
		status.Store(http.StatusOK)
		if err := get(context.Background(), c, srv.URL); err != nil {
			t.Fatalf("round %d: success refused: %v", round, err)
		}
	}

	// 4xx is the caller's problem, not an upstream failure.
	status.Store(http.StatusNotFound)
	for i := 0; i < 10; i++ {
		if err := get(context.Background(), c, srv.URL); isBreakerOpen(err) {
			t.Fatalf("4xx opened the breaker")
		}
	}

	status.Store(http.StatusInternalServerError)
	for i := 0; i < 5; i++ {
		_ = get(context.Background(), c, srv.URL)
	}
	status.Store(http.StatusOK)
	if err := get(context.Background(), c, srv.URL); !isBreakerOpen(err) {
		t.Fatalf("want open after 5 consecutive 5xx, got %v", err)
	}
}

func TestBreaker_ConnectionErrorOpens(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := breakerClient(time.Second, time.Minute)
	for i := 0; i < 5; i++ {
		if err := get(context.Background(), c, url); err == nil || isBreakerOpen(err) {
			t.Fatalf("call %d: want connection error, got %v", i, err)
		}
	}
	if err := get(context.Background(), c, url); !isBreakerOpen(err) {
		t.Fatalf("want open after 5 connection errors, got %v", err)
	}
}

func TestBreaker_HalfOpenRecovery(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusInternalServerError)
	srv := statusServer(t, &status)
	c, _ := breakerClient(time.Second, 50*time.Millisecond)

	for i := 0; i < 5; i++ {
		_ = get(context.Background(), c, srv.URL)
	}
	if err := get(context.Background(), c, srv.URL); !isBreakerOpen(err) {
		t.Fatalf("want open, got %v", err)
	}

	// Failed probe re-opens for the full window.
	time.Sleep(70 * time.Millisecond)
	if err := get(context.Background(), c, srv.URL); err == nil || isBreakerOpen(err) {
		t.Fatalf("probe should reach upstream and fail, got %v", err)
	}
	if err := get(context.Background(), c, srv.URL); !isBreakerOpen(err) {
		t.Fatalf("failed probe should re-open, got %v", err)
	}

	// Successful probe closes it.
	time.Sleep(70 * time.Millisecond)
	status.Store(http.StatusOK)
	if err := get(context.Background(), c, srv.URL); err != nil {
		t.Fatalf("probe: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := get(context.Background(), c, srv.URL); err != nil {
			t.Fatalf("after recovery call %d: %v", i, err)
		}
	}
}

func TestBreaker_HalfOpenSingleProbe(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer unblock() // never leave a handler blocked if the test fails early
	c, _ := breakerClient(5*time.Second, 30*time.Millisecond)
	for i := 0; i < 5; i++ {
		_ = get(context.Background(), c, srv.URL)
	}
	time.Sleep(50 * time.Millisecond)
	fail.Store(false)

	probeDone := make(chan error, 1)
	go func() { probeDone <- get(context.Background(), c, srv.URL) }()
	// Wait until the probe holds the slot.
	deadline := time.Now().Add(2 * time.Second)
	for {
		bt := c.Transport.(*BreakerTransport)
		bt.mu.Lock()
		probing := bt.hosts[strings.TrimPrefix(srv.URL, "http://")].probing
		bt.mu.Unlock()
		if probing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probe never started")
		}
		time.Sleep(time.Millisecond)
	}
	if err := get(context.Background(), c, srv.URL); !isBreakerOpen(err) {
		t.Fatalf("second request during probe must be refused, got %v", err)
	}
	unblock()
	if err := <-probeDone; err != nil {
		t.Fatalf("probe: %v", err)
	}
	if err := get(context.Background(), c, srv.URL); err != nil {
		t.Fatalf("after probe: %v", err)
	}
}

func TestBreaker_CancelledProbeKeepsOpenButFreesSlot(t *testing.T) {
	var mode atomic.Int32 // 0 = 500, 1 = slow ok
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, bt := breakerClient(5*time.Second, 30*time.Millisecond)
	for i := 0; i < 5; i++ {
		_ = get(context.Background(), c, srv.URL)
	}
	time.Sleep(50 * time.Millisecond)
	mode.Store(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := get(ctx, c, srv.URL); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe should hit caller deadline, got %v", err)
	}
	cancel()
	bt.mu.Lock()
	h := *bt.hosts[strings.TrimPrefix(srv.URL, "http://")]
	bt.mu.Unlock()
	if h.probing || h.openUntil.IsZero() {
		t.Fatalf("cancelled probe should free the slot and leave the breaker open: %+v", h)
	}
	// The next request probes and closes it.
	if err := get(context.Background(), c, srv.URL); err != nil {
		t.Fatalf("next probe: %v", err)
	}
}

// A request that started while closed and succeeds after the breaker opened
// must not close it: only the half-open probe decides that.
func TestBreaker_StaleSuccessDoesNotClose(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	c, _ := breakerClient(5*time.Second, time.Minute)
	slowDone := make(chan error, 1)
	go func() { slowDone <- get(context.Background(), c, srv.URL+"/slow") }()
	time.Sleep(50 * time.Millisecond) // let the slow request get past acquire
	for i := 0; i < 5; i++ {
		_ = get(context.Background(), c, srv.URL+"/fail")
	}
	unblock()
	if err := <-slowDone; err != nil {
		t.Fatalf("slow request: %v", err)
	}
	if err := get(context.Background(), c, srv.URL+"/slow"); !isBreakerOpen(err) {
		t.Fatalf("a stale success closed an open breaker: %v", err)
	}
}

func TestBreaker_PerHost(t *testing.T) {
	var bad, good atomic.Int32
	bad.Store(http.StatusInternalServerError)
	good.Store(http.StatusOK)
	badSrv := statusServer(t, &bad)
	goodSrv := statusServer(t, &good)
	c, _ := breakerClient(time.Second, time.Minute)
	for i := 0; i < 5; i++ {
		_ = get(context.Background(), c, badSrv.URL)
	}
	if err := get(context.Background(), c, badSrv.URL); !isBreakerOpen(err) {
		t.Fatalf("bad host should be open, got %v", err)
	}
	if err := get(context.Background(), c, goodSrv.URL); err != nil {
		t.Fatalf("good host refused because another host failed: %v", err)
	}
}

func TestBreaker_TimeoutCoversBodyButBodyReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	c, _ := breakerClient(time.Second, time.Minute)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != "hello" {
		t.Fatalf("body = %q, %v", b, err)
	}

	slowBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slowBody.Close()
	c2, _ := breakerClient(30*time.Millisecond, time.Minute)
	resp, err = c2.Get(slowBody.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := time.Now()
	_, err = io.ReadAll(resp.Body)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("body read should be cut by the request timeout, err=%v after %v", err, time.Since(start))
	}
}

// Run with -race: concurrent mixed outcomes must not race on breaker state.
func TestBreaker_ConcurrentNoRace(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1)%3 == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, _ := breakerClient(time.Second, 5*time.Millisecond)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				ctx, cancel := context.WithCancel(context.Background())
				if (g+i)%7 == 0 {
					cancel()
				}
				_ = get(ctx, c, srv.URL)
				cancel()
			}
		}(g)
	}
	wg.Wait()
}
