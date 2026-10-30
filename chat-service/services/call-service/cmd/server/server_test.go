package main

import (
	"net/http"
	"testing"
)

// Audit L1: the listener must carry every timeout; gin.Engine.Run set none.
func TestNewHTTPServerSetsTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NewServeMux())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("missing timeout: header=%v read=%v write=%v idle=%v",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
	if srv.ReadHeaderTimeout > srv.ReadTimeout {
		t.Fatalf("header timeout %v exceeds read timeout %v", srv.ReadHeaderTimeout, srv.ReadTimeout)
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Fatal("MaxHeaderBytes unset")
	}
}
