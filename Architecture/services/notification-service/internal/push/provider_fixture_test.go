package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// CALL-LB-4A adapter fixtures: the REAL FCMPusher.Send / APNSPusher.Send
// against scripted HTTP servers, proving that two responses sharing the SAME
// HTTP status produce DIFFERENT outcomes depending on the response body —
// invalid token retires (ErrDeviceRejected), invalid request propagates as
// an operational error and must never blame a healthy device.

func pemEncodePKCS8(t *testing.T, key interface{}) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// fixtureFCM builds a real FCMPusher whose token endpoint AND messages:send
// endpoint are served by one scripted httptest server.
func fixtureFCM(t *testing.T, status int, body string) (*FCMPusher, *httptest.Server) {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fixture-token", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/v1/projects/proj/messages:send", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	sa, err := json.Marshal(map[string]string{
		"client_email": "fixture@test",
		"private_key":  pemEncodePKCS8(t, rsaKey),
		"token_uri":    server.URL + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	pusher := NewFCMPusher("proj", string(sa))
	pusher.apiBaseURL = server.URL
	return pusher, server
}

func fcmDetails(code, extra string) string {
	details := `{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"` + code + `"}`
	if extra != "" {
		details += "," + extra
	}
	return `{"error":{"code":400,"status":"INVALID_ARGUMENT","details":[` + details + `]}}`
}

// The review's exact requirement: two HTTP 400 responses — invalid TOKEN
// versus invalid REQUEST — must produce different outcomes from the real
// adapter.
func TestFCMAdapterSameStatusDifferentVerdicts(t *testing.T) {
	badRequest := `{"@type":"type.googleapis.com/google.rpc.BadRequest",` +
		`"fieldViolations":[{"field":"message.data","description":"invalid value"}]}`

	invalidToken, _ := fixtureFCM(t, 400, fcmDetails("INVALID_ARGUMENT", ""))
	err := invalidToken.Send(context.Background(), "tok", "android", "t", "b", nil)
	if err == nil || !errors.Is(err, ErrDeviceRejected) {
		t.Fatalf("400 invalid-token did not reject the device: %v (CALL-LB-4A)", err)
	}

	invalidRequest, _ := fixtureFCM(t, 400, fcmDetails("INVALID_ARGUMENT", badRequest))
	err = invalidRequest.Send(context.Background(), "tok", "android", "t", "b", nil)
	if err == nil {
		t.Fatal("400 invalid-request reported success")
	}
	if errors.Is(err, ErrDeviceRejected) {
		t.Fatalf("400 invalid-REQUEST retired a healthy device: %v (CALL-LB-4A)", err)
	}
}

func TestFCMAdapterTerminalAndOperationalResponses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		rejected bool
	}{
		{"404 UNREGISTERED", 404, `{"error":{"code":404,"status":"NOT_FOUND","details":[` +
			`{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, true},
		{"403 SENDER_ID_MISMATCH", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[` +
			`{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"SENDER_ID_MISMATCH"}]}}`, true},
		{"401 UNAUTHENTICATED", 401, `{"error":{"code":401,"status":"UNAUTHENTICATED"}}`, false},
		{"429 QUOTA_EXCEEDED", 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[` +
			`{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"QUOTA_EXCEEDED"}]}}`, false},
		{"503 UNAVAILABLE", 503, `{"error":{"code":503,"status":"UNAVAILABLE"}}`, false},
		{"502 html body", 502, `<html>bad gateway</html>`, false},
	}
	for _, tc := range cases {
		pusher, _ := fixtureFCM(t, tc.status, tc.body)
		err := pusher.Send(context.Background(), "tok", "android", "t", "b", nil)
		if err == nil {
			t.Fatalf("%s reported success", tc.name)
		}
		if got := errors.Is(err, ErrDeviceRejected); got != tc.rejected {
			t.Fatalf("%s: rejected=%v, want %v (CALL-LB-4A): %v", tc.name, got, tc.rejected, err)
		}
	}
}

// fixtureAPNS builds a real APNSPusher pointed at a scripted server; the
// handler also captures request headers for the push-type assertion.
func fixtureAPNS(t *testing.T, status int, body string, gotHeaders *http.Header) *APNSPusher {
	t.Helper()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotHeaders != nil {
			*gotHeaders = r.Header.Clone()
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	pusher, err := NewAPNSPusher("team", "key", pemEncodePKCS8(t, ecKey), "com.atpost.app", false)
	if err != nil {
		t.Fatal(err)
	}
	pusher.hostOverride = server.URL
	return pusher
}

// Two APNs HTTP 400s: BadDeviceToken retires; BadTopic (a server-side
// request/config defect) must propagate without blaming the device.
func TestAPNSAdapterSameStatusDifferentVerdicts(t *testing.T) {
	badToken := fixtureAPNS(t, 400, `{"reason":"BadDeviceToken"}`, nil)
	err := badToken.Send(context.Background(), "tok", "ios", "t", "b", nil)
	if err == nil || !errors.Is(err, ErrDeviceRejected) {
		t.Fatalf("400 BadDeviceToken did not reject the device: %v (CALL-LB-4A)", err)
	}

	for _, requestReason := range []string{"BadTopic", "MissingPushType", "BadCollapseId"} {
		pusher := fixtureAPNS(t, 400, `{"reason":"`+requestReason+`"}`, nil)
		err := pusher.Send(context.Background(), "tok", "ios", "t", "b", nil)
		if err == nil {
			t.Fatalf("400 %s reported success", requestReason)
		}
		if errors.Is(err, ErrDeviceRejected) {
			t.Fatalf("400 %s retired a healthy device: %v (CALL-LB-4A)", requestReason, err)
		}
	}
}

func TestAPNSAdapterTerminalAndOperationalResponses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		rejected bool
	}{
		{"410 Unregistered", 410, `{"reason":"Unregistered"}`, true},
		{"403 ExpiredProviderToken", 403, `{"reason":"ExpiredProviderToken"}`, false},
		{"429 TooManyRequests", 429, `{"reason":"TooManyRequests"}`, false},
		{"500 InternalServerError", 500, `{"reason":"InternalServerError"}`, false},
		{"503 empty body", 503, ``, false},
	}
	for _, tc := range cases {
		pusher := fixtureAPNS(t, tc.status, tc.body, nil)
		err := pusher.Send(context.Background(), "tok", "ios", "t", "b", nil)
		if err == nil {
			t.Fatalf("%s reported success", tc.name)
		}
		if got := errors.Is(err, ErrDeviceRejected); got != tc.rejected {
			t.Fatalf("%s: rejected=%v, want %v (CALL-LB-4A): %v", tc.name, got, tc.rejected, err)
		}
	}
}

// The concrete server-side defect the review named: the request must carry
// apns-push-type, or APNs itself answers a request-level 400 that a
// status-only rule would have pinned on the device.
func TestAPNSAdapterSendsThePushTypeHeader(t *testing.T) {
	var headers http.Header
	pusher := fixtureAPNS(t, 200, ``, &headers)
	if err := pusher.Send(context.Background(), "tok", "ios", "t", "b",
		map[string]string{"collapse_key": "call:abc"}); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if got := headers.Get("apns-push-type"); got != "alert" {
		t.Fatalf("apns-push-type header = %q, want alert (CALL-LB-4A)", got)
	}
	if got := headers.Get("apns-collapse-id"); got != "call:abc" {
		t.Fatalf("apns-collapse-id lost: %q", got)
	}
}
