package sessionrevocation

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const sid = "6f1d2c3b-4a5e-4f60-8b7c-9d0e1f2a3b4c"

func newChecker(t *testing.T) (*miniredis.Miniredis, RedisChecker) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, RedisChecker{RDB: rdb, Timeout: 200 * time.Millisecond}
}

type result struct {
	status  int
	code    string
	reached bool
}

func serve(checker Checker, path, sessionID string, prepare func(*http.Request)) result {
	var reached bool
	h := Middleware(checker, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if sessionID != "" {
		req = req.WithContext(WithSessionID(req.Context(), sessionID))
	}
	if prepare != nil {
		prepare(req)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	code := ""
	if i := strings.Index(res.Body.String(), `"code":"`); i >= 0 {
		rest := res.Body.String()[i+8:]
		code = rest[:strings.IndexByte(rest, '"')]
	}
	return result{status: res.Code, code: code, reached: reached}
}

// The key must be exactly what auth-service writes:
// "sess_revoked:" + sessionID.String().
func TestRevokedSessionIsRefused(t *testing.T) {
	mr, checker := newChecker(t)
	if err := mr.Set("sess_revoked:"+sid, "1"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/v1/feed/home", "/v1/admin/commerce/sellers/queue"} {
		got := serve(checker, p, sid, nil)
		if got.status != http.StatusUnauthorized || got.code != "SESSION_REVOKED" || got.reached {
			t.Errorf("%s: %+v, want 401 SESSION_REVOKED not reached", p, got)
		}
	}
}

func TestUnrevokedSessionPasses(t *testing.T) {
	mr, checker := newChecker(t)
	_ = mr.Set("sess_revoked:another-session", "1")
	got := serve(checker, "/v1/admin/commerce/sellers/queue", sid, nil)
	if got.status != http.StatusOK || !got.reached {
		t.Fatalf("unrevoked session: %+v, want 200 reached", got)
	}
}

func TestRequestsWithoutSessionSkipTheLookup(t *testing.T) {
	mr, checker := newChecker(t)
	mr.Close() // any lookup would fail
	for _, p := range []string{"/v1/feed/home", "/v1/admin/x"} {
		if got := serve(checker, p, "", nil); got.status != http.StatusOK {
			t.Errorf("%s anonymous: %+v, want 200", p, got)
		}
	}
}

func TestRedisDownFailsClosedForPrivilegedAndOpenForConsumers(t *testing.T) {
	mr, checker := newChecker(t)
	mr.Close()

	closedBefore, openBefore := FailClosedCount(), FailOpenCount()

	privileged := []struct {
		path    string
		prepare func(*http.Request)
	}{
		{"/v1/admin/commerce/sellers/queue", nil},
		{"/v1/admin", nil},
		{"/v1/dating/admin/reports", nil},
		{"/v1/rider/admin/partners", nil},
		{"/V1/ADMIN/flags", nil},
		// A token holding a platform role is privileged on any path.
		{"/v1/feed/home", func(r *http.Request) { r.Header.Set("X-Admin-Role", "moderator") }},
	}
	for _, c := range privileged {
		got := serve(checker, c.path, sid, c.prepare)
		if got.status != http.StatusServiceUnavailable || got.code != "SESSION_CHECK_UNAVAILABLE" || got.reached {
			t.Errorf("%s: %+v, want 503 SESSION_CHECK_UNAVAILABLE not reached", c.path, got)
		}
	}
	got := serve(checker, "/v1/feed/home", sid, nil)
	if got.status != http.StatusOK || !got.reached {
		t.Errorf("consumer path with redis down: %+v, want 200 reached (fail open)", got)
	}

	if d := FailClosedCount() - closedBefore; d != float64(len(privileged)) {
		t.Errorf("fail_closed counter moved by %v, want %d", d, len(privileged))
	}
	if d := FailOpenCount() - openBefore; d != 1 {
		t.Errorf("fail_open counter moved by %v, want 1", d)
	}
}

func TestNilCheckerDisablesTheLookup(t *testing.T) {
	if got := serve(nil, "/v1/admin/x", sid, nil); got.status != http.StatusOK {
		t.Fatalf("nil checker: %+v, want 200", got)
	}
}

func TestSessionIDCannotComeFromAHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/feed", nil)
	req.Header.Set("X-Session-Id", sid)
	if SessionID(req.Context()) != "" {
		t.Fatal("a session id appeared without WithSessionID")
	}
}
