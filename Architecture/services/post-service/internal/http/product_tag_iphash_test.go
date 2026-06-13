package http

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// hashClientIP precedence is security-relevant — the gateway sets
// X-Real-IP after extracting from the public connection. If we
// accidentally honoured a viewer-supplied X-Forwarded-For instead,
// a single IP could rotate spoofed XFF values to bypass dedup.

func TestHashClientIPPrefersXRealIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Real-IP", "203.0.113.7")
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.5") // ignored
	req.RemoteAddr = "10.0.0.99:54321"                     // ignored
	c.Request = req

	got := hashClientIP(c)
	want := hashClientIP(makeCtx(t, "203.0.113.7", "", ""))
	if got != want {
		t.Fatalf("X-Real-IP should win; got hash for different IP")
	}
}

func TestHashClientIPFallsBackToXFFFirstHop(t *testing.T) {
	c1 := makeCtx(t, "", "203.0.113.10, 10.0.0.5", "")
	c2 := makeCtx(t, "203.0.113.10", "", "")
	if hashClientIP(c1) != hashClientIP(c2) {
		t.Fatal("XFF first hop should equal X-Real-IP single value")
	}
}

func TestHashClientIPFallsBackToRemoteAddr(t *testing.T) {
	c := makeCtx(t, "", "", "203.0.113.20:54321")
	if hashClientIP(c) == "" {
		t.Fatal("RemoteAddr fallback returned empty hash")
	}
}

func TestHashClientIPIsStable(t *testing.T) {
	a := makeCtx(t, "203.0.113.30", "", "")
	b := makeCtx(t, "203.0.113.30", "", "")
	if hashClientIP(a) != hashClientIP(b) {
		t.Fatal("same IP twice produced different hashes")
	}
}

func TestHashClientIPDifferentIPsDifferentHashes(t *testing.T) {
	a := makeCtx(t, "203.0.113.40", "", "")
	b := makeCtx(t, "203.0.113.41", "", "")
	if hashClientIP(a) == hashClientIP(b) {
		t.Fatal("different IPs collided")
	}
}

func TestHashClientIPEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/", nil)
	// httptest.NewRequest defaults RemoteAddr to a non-empty value;
	// clear it so the fallback chain reaches its terminal case.
	req.RemoteAddr = ""
	c.Request = req
	if got := hashClientIP(c); got != "" {
		t.Fatalf("want empty hash, got %q", got)
	}
}

func makeCtx(t *testing.T, xRealIP, xff, remoteAddr string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("POST", "/", nil)
	if xRealIP != "" {
		req.Header.Set("X-Real-IP", xRealIP)
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	c.Request = req
	return c
}
