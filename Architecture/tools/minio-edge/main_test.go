package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRestoresAcceptEncodingOnlyWhenSigned(t *testing.T) {
	cases := []struct {
		name, auth, query, want string
	}{
		{"sdk v2 signs accept-encoding", "AWS4-HMAC-SHA256 Credential=k/20261001/us-east-1/s3/aws4_request, SignedHeaders=accept-encoding;amz-sdk-invocation-id;host;x-amz-content-sha256;x-amz-date, Signature=abc", "", "identity"},
		{"minio-go does not sign it", "AWS4-HMAC-SHA256 Credential=k/20261001/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=abc", "", "gzip, br"},
		{"presigned covering it", "", "X-Amz-SignedHeaders=accept-encoding%3Bhost", "identity"},
		{"presigned host only", "", "X-Amz-SignedHeaders=host", "gzip, br"},
		{"unsigned browser request", "", "", "gzip, br"},
		{"signature value mentioning the name is not the list", "AWS4-HMAC-SHA256 Credential=k/x, SignedHeaders=host;x-amz-date, Signature=accept-encoding", "", "gzip, br"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "https://media-dev.cleestudio.com/live-recordings/recordings/x.mp4?uploads&"+tc.query, nil)
			r.Header.Set("Accept-Encoding", "gzip, br") // what Cloudflare forwards
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			restoreSignedAcceptEncoding(r)
			if got := r.Header.Get("Accept-Encoding"); got != tc.want {
				t.Fatalf("Accept-Encoding = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProxyKeepsPublicHostAndRestoresHeader(t *testing.T) {
	var gotHost, gotAE string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotAE = r.Host, r.Header.Get("Accept-Encoding")
	}))
	defer up.Close()
	target, _ := url.Parse(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/live-recordings/recordings/x.mp4?uploads", nil)
	req.Host = "media-dev.cleestudio.com"
	req.Header.Set("Accept-Encoding", "gzip, br")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=k/x, SignedHeaders=accept-encoding;host, Signature=s")
	newProxy(target).ServeHTTP(httptest.NewRecorder(), req)
	if gotHost != "media-dev.cleestudio.com" {
		t.Fatalf("upstream Host = %q, want the public host (MinIO signs it)", gotHost)
	}
	if gotAE != "identity" {
		t.Fatalf("upstream Accept-Encoding = %q, want identity", gotAE)
	}
}
