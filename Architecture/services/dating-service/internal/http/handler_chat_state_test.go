package http

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Audit H2: the chat-state route is service-only, like the rest of the
// internal family. No database: every case stops in the auth layer or in the
// handler's parameter parsing (which proves admission).
func TestMatchChatStateRoute_ServiceCallerOnly(t *testing.T) {
	path := strings.Replace(InternalMatchChatStatePath, ":id", "not-a-uuid", 1)
	r := newAuthRouter(t, testInternalKey, nil)

	for _, scopes := range []string{"", "admin"} {
		w := serve(r, http.MethodGet, path, "", gatewayUser(uuid.New(), scopes))
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeUserCallerRefused {
			t.Fatalf("user scopes=%q: status=%d code=%q, want 403 %s", scopes, w.Code, errorCode(t, w), CodeUserCallerRefused)
		}
	}
	for _, hdr := range []map[string]string{{}, {headerInternalKey: "wrong"}} {
		w := serve(r, http.MethodGet, path, "", hdr)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeServiceCredentialRequired {
			t.Fatalf("headers=%v: status=%d code=%q, want 401 %s", hdr, w.Code, errorCode(t, w), CodeServiceCredentialRequired)
		}
	}
	if w := serve(r, http.MethodGet, path, "", map[string]string{headerInternalKey: testInternalKey}); w.Code != http.StatusBadRequest {
		t.Fatalf("legacy key: status=%d body=%s, want 400 from the handler", w.Code, w.Body.String())
	}
	// No key configured: nobody gets in without a token.
	if w := serve(newAuthRouter(t, "", nil), http.MethodGet, path, "", map[string]string{headerInternalKey: ""}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key configured: status=%d, want 401", w.Code)
	}

	// Service token: only one scoped to OpMatchChatState is admitted.
	v, signer := mustCallerVerifier(t, OpMatchChatState+","+OpProfilePreview)
	rt := newAuthRouter(t, testInternalKey, v)
	ok, err := signer.Mint(AudienceDating, "", []string{OpMatchChatState}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(rt, http.MethodGet, path, "", map[string]string{ServiceAuthHeader: "Bearer " + ok}); w.Code != http.StatusBadRequest {
		t.Fatalf("scoped token: status=%d body=%s, want 400 from the handler", w.Code, w.Body.String())
	}
	other, err := signer.Mint(AudienceDating, "", []string{OpProfilePreview}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w := serve(rt, http.MethodGet, path, "", map[string]string{ServiceAuthHeader: "Bearer " + other, headerInternalKey: testInternalKey})
	if w.Code != http.StatusForbidden || errorCode(t, w) != CodeServiceTokenRejected {
		t.Fatalf("token for another op: status=%d code=%q, want 403 %s", w.Code, errorCode(t, w), CodeServiceTokenRejected)
	}
}
