package server

import (
	"net/http"
	"testing"
	"time"
)

// Every service that does not ask keeps the defaults it always had.
func TestNewHTTPServerKeepsDefaults(t *testing.T) {
	srv := newHTTPServer(http.NewServeMux(), Config{Port: "8080"})
	if srv.ReadTimeout != 15*time.Second || srv.WriteTimeout != 30*time.Second {
		t.Fatalf("defaults changed: read=%s write=%s", srv.ReadTimeout, srv.WriteTimeout)
	}
	if srv.ReadHeaderTimeout != 5*time.Second || srv.IdleTimeout != 90*time.Second {
		t.Fatalf("header/idle defaults changed: header=%s idle=%s", srv.ReadHeaderTimeout, srv.IdleTimeout)
	}
	if srv.Addr != ":8080" {
		t.Fatalf("addr = %q", srv.Addr)
	}
}

// media-service's long uploads: the read/write overrides apply, the
// slow-header guard does not move.
func TestNewHTTPServerAppliesOverrides(t *testing.T) {
	srv := newHTTPServer(http.NewServeMux(), Config{Port: "8087", ReadTimeout: 10 * time.Minute, WriteTimeout: 11 * time.Minute})
	if srv.ReadTimeout != 10*time.Minute {
		t.Fatalf("read timeout = %s, want 10m", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 11*time.Minute {
		t.Fatalf("write timeout = %s, want 11m", srv.WriteTimeout)
	}
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("read-header timeout = %s, must stay 5s", srv.ReadHeaderTimeout)
	}
}
