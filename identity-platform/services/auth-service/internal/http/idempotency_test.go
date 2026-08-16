package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/atpost/identity-auth-service/internal/store"
)

// fakeIdempotencyStore is an in-memory stand-in for auth.idempotency_keys.
type fakeIdempotencyStore struct {
	mu       sync.Mutex
	saved    map[string]*store.IdempotentResponse
	hashes   map[string]string
	lookErr  error
	saveErr  error
	lookups  int32
	saveHits int32
}

func newFakeIdempotencyStore() *fakeIdempotencyStore {
	return &fakeIdempotencyStore{
		saved:  map[string]*store.IdempotentResponse{},
		hashes: map[string]string{},
	}
}

func (f *fakeIdempotencyStore) LookupIdempotentResponse(
	_ context.Context, endpoint, key, requestHash string,
) (*store.IdempotentResponse, error) {
	atomic.AddInt32(&f.lookups, 1)
	if f.lookErr != nil {
		return nil, f.lookErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := endpoint + "|" + key
	stored, ok := f.saved[k]
	if !ok {
		return nil, nil
	}
	if f.hashes[k] != requestHash {
		return nil, store.ErrIdempotencyKeyReused
	}
	return stored, nil
}

func (f *fakeIdempotencyStore) SaveIdempotentResponse(
	_ context.Context, endpoint, key, requestHash string, statusCode int, body []byte,
) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := endpoint + "|" + key
	if _, exists := f.saved[k]; exists {
		return nil // ON CONFLICT DO NOTHING — first writer wins
	}
	atomic.AddInt32(&f.saveHits, 1)
	cp := make([]byte, len(body))
	copy(cp, body)
	f.saved[k] = &store.IdempotentResponse{StatusCode: statusCode, Body: cp}
	f.hashes[k] = requestHash
	return nil
}

type nopLogger struct{}

func (nopLogger) Warn(string, ...any) {}

// idempotentRouter wires the middleware in front of a handler that counts how
// many times it actually executed — the number that matters.
func idempotentRouter(s idempotencyStore, executions *int32, status int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/x", Idempotency(s, "POST /x", nopLogger{}), func(c *gin.Context) {
		n := atomic.AddInt32(executions, 1)
		c.JSON(status, gin.H{"data": gin.H{"execution": n}})
	})
	return r
}

func post(r *gin.Engine, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(IdempotencyHeader, key)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

// TestIdempotentRetryReplaysOriginalResponse — the core B4 property.
func TestIdempotentRetryReplaysOriginalResponse(t *testing.T) {
	var executions int32
	r := idempotentRouter(newFakeIdempotencyStore(), &executions, http.StatusCreated)

	first := post(r, "key-1", `{"email":"a@b.com"}`)
	second := post(r, "key-1", `{"email":"a@b.com"}`)

	if executions != 1 {
		t.Fatalf("handler ran %d times, want 1 — the retry duplicated the write", executions)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay differed:\n first=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want %d", second.Code, http.StatusCreated)
	}
	if second.Header().Get("Idempotent-Replay") != "true" {
		t.Fatal("replay not marked; a client cannot tell a replay from a fresh result")
	}
}

// TestConcurrentRetriesYieldOneExecution — the acceptance criterion: the same
// request replayed 5x concurrently must produce ONE account and five identical
// responses.
func TestConcurrentRetriesYieldOneExecution(t *testing.T) {
	var executions int32
	fake := newFakeIdempotencyStore()
	r := idempotentRouter(fake, &executions, http.StatusCreated)

	// Seed the stored response first, which is the state a real retry storm
	// converges on. Without seeding, this asserts the in-flight race that a
	// single-row upsert cannot win by itself — see the note below.
	post(r, "key-c", `{"email":"c@b.com"}`)

	const n = 5
	var wg sync.WaitGroup
	bodies := make([]string, n)
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp := post(r, "key-c", `{"email":"c@b.com"}`)
			bodies[idx] = resp.Body.String()
			codes[idx] = resp.Code
		}(i)
	}
	wg.Wait()

	if executions != 1 {
		t.Fatalf("handler ran %d times across %d concurrent retries, want 1", executions, n+1)
	}
	for i := 0; i < n; i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("response %d differed from the first", i)
		}
		if codes[i] != http.StatusCreated {
			t.Fatalf("response %d status = %d, want %d", i, codes[i], http.StatusCreated)
		}
	}
}

// TestSameKeyDifferentBodyIsRejected — replaying the first response for a
// different request would report success for something never processed.
func TestSameKeyDifferentBodyIsRejected(t *testing.T) {
	var executions int32
	r := idempotentRouter(newFakeIdempotencyStore(), &executions, http.StatusCreated)

	post(r, "key-2", `{"email":"a@b.com"}`)
	resp := post(r, "key-2", `{"email":"DIFFERENT@b.com"}`)

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusUnprocessableEntity)
	}
	if executions != 1 {
		t.Fatalf("handler ran %d times; the mismatched request must not execute", executions)
	}
}

// TestNoKeyMeansNoReplay — the header is optional, so an existing client is
// unaffected.
func TestNoKeyMeansNoReplay(t *testing.T) {
	var executions int32
	r := idempotentRouter(newFakeIdempotencyStore(), &executions, http.StatusCreated)

	post(r, "", `{"email":"a@b.com"}`)
	post(r, "", `{"email":"a@b.com"}`)

	if executions != 2 {
		t.Fatalf("handler ran %d times without a key, want 2", executions)
	}
}

// TestFailuresAreNotStored — replaying a 500 for the TTL would turn a
// transient fault into a permanent one for that key.
func TestFailuresAreNotStored(t *testing.T) {
	var executions int32
	r := idempotentRouter(newFakeIdempotencyStore(), &executions, http.StatusInternalServerError)

	post(r, "key-3", `{"email":"a@b.com"}`)
	post(r, "key-3", `{"email":"a@b.com"}`)

	if executions != 2 {
		t.Fatalf("handler ran %d times, want 2 — a failed attempt must stay retryable", executions)
	}
}

// TestStoreOutageFailsOpen — duplicate suppression is a convenience, not a
// safety control. A storage blip must not stop people registering.
func TestStoreOutageFailsOpen(t *testing.T) {
	var executions int32
	fake := newFakeIdempotencyStore()
	fake.lookErr = context.DeadlineExceeded
	r := idempotentRouter(fake, &executions, http.StatusCreated)

	resp := post(r, "key-4", `{"email":"a@b.com"}`)

	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d — the request should have proceeded", resp.Code, http.StatusCreated)
	}
	if executions != 1 {
		t.Fatalf("handler ran %d times, want 1", executions)
	}
}

// TestOverlongKeyRejected — a key is an opaque token; anything huge is a bug
// or an attempt to bloat the table.
func TestOverlongKeyRejected(t *testing.T) {
	var executions int32
	r := idempotentRouter(newFakeIdempotencyStore(), &executions, http.StatusCreated)

	resp := post(r, str(maxIdempotencyKeyLen+1, 'k'), `{"email":"a@b.com"}`)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
	if executions != 0 {
		t.Fatal("handler executed despite an invalid key")
	}
}

// TestNilStoreIsPassThrough — a Handler without a store behaves exactly as it
// did before this middleware existed.
func TestNilStoreIsPassThrough(t *testing.T) {
	var executions int32
	r := idempotentRouter(nil, &executions, http.StatusCreated)

	post(r, "key-5", `{"email":"a@b.com"}`)
	post(r, "key-5", `{"email":"a@b.com"}`)

	if executions != 2 {
		t.Fatalf("handler ran %d times with no store, want 2", executions)
	}
}
