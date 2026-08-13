package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Module 3 M3-P0-1 / SR-3 — the source guard on canonical graph writes.
//
// Retiring profile-service's routes closes the second writer that exists
// today. This guard is about the third one: a service that grows its own
// follow/block implementation later because calling graph-service was
// inconvenient, and which nobody notices until a user reports that blocking
// did nothing.
//
// The internal service key authenticates but is SHARED, so it cannot tell one
// caller from another. Attribution is what makes an unreviewed writer visible.

func guardRouter(strict bool) (*gin.Engine, *[]string) {
	gin.SetMode(gin.TestMode)
	var warnings []string
	r := gin.New()
	r.Use(RequireCanonicalWriteSource(strict, func(format string, args ...any) {
		warnings = append(warnings, format)
	}))
	r.POST("/v1/graph/block", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.DELETE("/v1/graph/block", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/v1/graph/follow", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/v1/graph/relationship", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r, &warnings
}

func doGuarded(r *gin.Engine, method, path, source string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if source != "" {
		req.Header.Set(GraphWriteSourceHeader, source)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func TestStrictGuardRefusesUnattributedAndUnknownWriters(t *testing.T) {
	r, _ := guardRouter(true)

	for _, tc := range []struct{ name, source string }{
		{"no header at all", ""},
		{"empty header", "   "},
		{"a service nobody reviewed", "some-new-service"},
		// The one that matters: profile-service kept a duplicate graph. If it
		// ever appears in the allowlist again, the shadow graph is back.
		{"the retired duplicate writer", "identity-profile-service"},
		{"near-miss on an allowed name", "api-gateway-v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doGuarded(r, http.MethodPost, "/v1/graph/block", tc.source)
			if resp.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403. An unreviewed service can create "+
					"follows and blocks that feed, search and chat will honour.", resp.Code)
			}
			if !strings.Contains(resp.Body.String(), "UNRECOGNISED_GRAPH_WRITER") {
				t.Errorf("body does not explain the refusal: %s", resp.Body.String())
			}
		})
	}
}

func TestStrictGuardAdmitsEveryApprovedWriter(t *testing.T) {
	r, _ := guardRouter(true)
	if len(allowedGraphWriteSources) == 0 {
		t.Fatal("no approved writers configured; every graph write would be refused")
	}
	for source := range allowedGraphWriteSources {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			resp := doGuarded(r, method, "/v1/graph/block", source)
			if resp.Code != http.StatusOK {
				t.Errorf("%s %s from %q: status %d, want 200", method, "/v1/graph/block", source, resp.Code)
			}
		}
	}
}

// Reads must not be gated. A guard that also blocks GETs would break every
// consumer of the graph, which is a far bigger outage than the hole it closes.
func TestGuardDoesNotGateReads(t *testing.T) {
	r, _ := guardRouter(true)
	resp := doGuarded(r, http.MethodGet, "/v1/graph/relationship", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("an unattributed READ was refused (status %d); block enforcement "+
			"in feed, search and chat depends on these reads succeeding", resp.Code)
	}
}

// Permissive mode exists only to roll the guard out without an instant outage.
// It must let the write through AND say so, because it leaves the hole open.
func TestPermissiveModeWarnsInsteadOfRefusing(t *testing.T) {
	r, warnings := guardRouter(false)
	resp := doGuarded(r, http.MethodPost, "/v1/graph/follow", "an-unknown-service")
	if resp.Code != http.StatusOK {
		t.Fatalf("permissive mode refused the write (status %d); it would be a "+
			"strict mode with a misleading name", resp.Code)
	}
	if len(*warnings) == 0 {
		t.Fatal("permissive mode admitted an unattributed write SILENTLY: nobody " +
			"would ever discover the second writer")
	}
}

// The allowlist is a security boundary, so its contents are asserted, not
// assumed. This fails loudly if someone re-adds the retired duplicate writer.
func TestProfileServiceIsNotAnApprovedGraphWriter(t *testing.T) {
	for _, banned := range []string{
		"identity-profile-service", "profile-service", "identity-profile",
	} {
		if allowedGraphWriteSources[banned] {
			t.Errorf("%q is an approved graph writer again. Its graph routes were "+
				"retired in SR-3 precisely because the blocks it wrote were "+
				"enforced by nothing.", banned)
		}
	}
}
