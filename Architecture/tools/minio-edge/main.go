// minio-edge is a DEV-ONLY reverse proxy between the Cloudflare tunnel and
// MinIO (media-dev.cleestudio.com → minio-edge → minio:9000).
//
// Why it exists: Cloudflare rewrites the Accept-Encoding request header on
// its way to the origin (a request sent with "identity" reaches MinIO as
// "gzip, br" — measured 1 Oct 2026 with `mc admin trace`). The AWS Go SDK v2,
// which LiveKit's egress uses to upload recordings, sends
// "Accept-Encoding: identity" for S3 and includes accept-encoding in the
// SigV4 SignedHeaders, so MinIO recomputes the signature over the rewritten
// value and answers 403 SignatureDoesNotMatch. minio-go (mc, media-service)
// does not sign that header, which is why only LiveKit Cloud's uploads broke.
//
// The fix is narrow: when a request's SigV4 signature (Authorization header
// or presigned X-Amz-SignedHeaders) covers accept-encoding, the header is
// put back to "identity", the only value the SDK sends for S3. Every other
// request passes through untouched; Host is preserved because MinIO signs it.
//
// Production uses real S3/MinIO without Cloudflare in front, so it needs no
// equivalent. Standard library only.
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func main() {
	upstream := envOr("MINIO_EDGE_UPSTREAM", "http://minio:9000")
	listen := envOr("MINIO_EDGE_LISTEN", ":9002")
	target, err := url.Parse(upstream)
	if err != nil {
		log.Fatalf("minio-edge: bad MINIO_EDGE_UPSTREAM: %v", err)
	}
	log.Printf("minio-edge: %s → %s", listen, target.Redacted())
	log.Fatal(http.ListenAndServe(listen, newProxy(target)))
}

func newProxy(target *url.URL) http.Handler {
	rp := httputil.NewSingleHostReverseProxy(target)
	base := rp.Director
	rp.Director = func(r *http.Request) {
		host := r.Host // MinIO verifies the signature over the public Host
		base(r)
		r.Host = host
		restoreSignedAcceptEncoding(r)
	}
	rp.FlushInterval = -1 // stream large uploads and media without buffering
	return rp
}

// restoreSignedAcceptEncoding puts Accept-Encoding back to "identity" when
// the SigV4 signature covers it.
func restoreSignedAcceptEncoding(r *http.Request) {
	if signsAcceptEncoding(r) {
		r.Header.Set("Accept-Encoding", "identity")
	}
}

func signsAcceptEncoding(r *http.Request) bool {
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "AWS4-HMAC-SHA256") {
		if i := strings.Index(a, "SignedHeaders="); i >= 0 {
			list := a[i+len("SignedHeaders="):]
			if j := strings.IndexByte(list, ','); j >= 0 {
				list = list[:j]
			}
			return hasHeader(list, "accept-encoding")
		}
	}
	if q := r.URL.Query().Get("X-Amz-SignedHeaders"); q != "" {
		return hasHeader(q, "accept-encoding")
	}
	return false
}

func hasHeader(semicolonList, name string) bool {
	for _, h := range strings.Split(semicolonList, ";") {
		if strings.EqualFold(strings.TrimSpace(h), name) {
			return true
		}
	}
	return false
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
