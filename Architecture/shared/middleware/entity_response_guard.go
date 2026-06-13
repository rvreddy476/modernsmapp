// Package middleware — entity_response_guard.go.
//
// Belt-and-suspenders runtime canary for relationship-separation spec
// §2.1 / §2.2. The User and BusinessPage struct definitions in user-service
// already omit the prohibited cross-type fields (User has no canFollow /
// followerCount / followingCount / isFollowing; BusinessPage has no
// canAddFriend / friendRequestStatus / friendCount / etc.), so this
// middleware should never fire in healthy code paths.
//
// When it DOES fire it means a future change added a forbidden field to
// a response struct. The middleware logs a warning per occurrence so we
// notice in dev / staging logs before the field reaches the UI. It does
// NOT rewrite the response body — silent mutation hides real bugs and
// adds an awkward streaming-buffer dance to every JSON response. If a
// drop-on-detect behaviour is ever needed it can layer on top of this
// canary later.
package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"
)

// Fields that must never appear on a response whose entityType == "user".
var userProhibitedFields = []string{
	"canFollow",
	"followerCount",
	"followingCount",
	"isFollowing",
}

// Fields that must never appear on a response whose entityType == "page".
var pageProhibitedFields = []string{
	"canAddFriend",
	"friendRequestStatus",
	"friendCount",
	"canSendFriendRequest",
	"mutualFriendCount",
}

// EntityResponseGuard returns a Gin middleware that scans JSON responses
// for relationship-separation spec violations. Detects entity kind via
// the `entityType` field embedded in the data envelope (`{"data":{...}}`)
// and warns when a prohibited field appears on the wrong side.
//
// Apply to any service that emits user or page profile responses
// (currently user-service). Safe on services that don't — non-JSON or
// envelopes without an entityType discriminator are ignored.
func EntityResponseGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		cap := &captureWriter{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = cap
		c.Next()

		// Always flush the captured body before we return — we never mutate.
		defer cap.flush()

		if cap.status >= 300 || !strings.Contains(cap.Header().Get("Content-Type"), "application/json") {
			return
		}
		auditEntityFields(cap.body.Bytes(), c.Request.URL.Path)
	}
}

// captureWriter buffers the response body so we can read it after the
// handler returns. It does not modify what eventually goes back to the
// client — flush() replays the captured bytes verbatim.
type captureWriter struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func (w *captureWriter) WriteHeader(status int) {
	w.status = status
}

func (w *captureWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	return w.body.WriteString(s)
}

func (w *captureWriter) flush() {
	if w.status == 0 {
		w.status = 200
	}
	w.ResponseWriter.WriteHeader(w.status)
	if w.body.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
	}
}

// auditEntityFields parses the response envelope and emits a warning if a
// prohibited field appears next to the wrong entityType.
func auditEntityFields(raw []byte, path string) {
	if len(raw) == 0 {
		return
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data) == 0 {
		return
	}
	// Two shapes are common: a single object and a list. Check both.
	if isJSONObject(envelope.Data) {
		warnIfProhibited(envelope.Data, path)
		return
	}
	if isJSONArray(envelope.Data) {
		var items []json.RawMessage
		if err := json.Unmarshal(envelope.Data, &items); err != nil {
			return
		}
		for _, item := range items {
			if isJSONObject(item) {
				warnIfProhibited(item, path)
			}
		}
	}
}

func warnIfProhibited(obj json.RawMessage, path string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(obj, &fields); err != nil {
		return
	}
	etRaw, ok := fields["entityType"]
	if !ok {
		return
	}
	var et string
	if err := json.Unmarshal(etRaw, &et); err != nil {
		return
	}
	var prohibited []string
	switch et {
	case "user":
		prohibited = userProhibitedFields
	case "page":
		prohibited = pageProhibitedFields
	default:
		return
	}
	for _, f := range prohibited {
		if _, present := fields[f]; present {
			slog.Warn("entity_response_guard: prohibited field on response",
				"path", path,
				"entityType", et,
				"field", f,
			)
		}
	}
}

func isJSONObject(raw json.RawMessage) bool {
	trim := bytes.TrimSpace(raw)
	return len(trim) > 0 && trim[0] == '{'
}

func isJSONArray(raw json.RawMessage) bool {
	trim := bytes.TrimSpace(raw)
	return len(trim) > 0 && trim[0] == '['
}
