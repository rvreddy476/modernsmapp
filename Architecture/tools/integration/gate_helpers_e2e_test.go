//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Helpers for the reviewer, live, trust-safety and discovery flows: each of
// those asserts an exact answer (status and error code) from the gateway.

// gatewayForFlow applies the usual gates (ATPOST_RUN_INTEGRATION, a gateway
// that answers) and returns the gateway base URL with a context that ends
// with the test.
func gatewayForFlow(t *testing.T) (string, context.Context) {
	t.Helper()
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return urls.APIGateway, ctx
}

// envelopeCode is the error code of an answer, "" when it carries none.
func envelopeCode(env *Envelope) string {
	if env == nil || env.Error == nil {
		return ""
	}
	return env.Error.Code
}

// wantRefusal fails the test unless env is exactly the refusal named: the
// HTTP status AND the error code. It reports whether it was.
func wantRefusal(t *testing.T, env *Envelope, what string, status int, code string) bool {
	t.Helper()
	if env.Status != status || envelopeCode(env) != code {
		t.Errorf("%s: want %d %s, got %d %q", what, status, code, env.Status, envelopeCode(env))
		return false
	}
	return true
}

// wantData fails the test unless env has the given status, and decodes its
// data into out (when out is not nil). It reports whether both held.
func wantData(t *testing.T, env *Envelope, what string, status int, out any) bool {
	t.Helper()
	if env.Status != status {
		t.Errorf("%s: want %d, got %d %q", what, status, env.Status, envelopeCode(env))
		return false
	}
	if out == nil {
		return true
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Errorf("%s: data does not decode: %v", what, err)
		return false
	}
	return true
}
