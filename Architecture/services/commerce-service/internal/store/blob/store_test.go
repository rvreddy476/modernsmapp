package blob

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// fakeS3 answers the three bucket calls Open makes (location, HEAD, PUT)
// and records every request so a test can assert that no bucket was
// created.
type fakeS3 struct {
	mu      sync.Mutex
	buckets map[string]bool
	calls   []string // "METHOD /path"
}

func newFakeS3(t *testing.T, existing ...string) (*fakeS3, *httptest.Server) {
	t.Helper()
	f := &fakeS3{buckets: map[string]bool{}}
	for _, b := range existing {
		f.buckets[b] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bucket := strings.Trim(r.URL.Path, "/")
	f.calls = append(f.calls, r.Method+" /"+bucket)
	if r.Method == http.MethodGet && r.URL.Query().Has("location") {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`))
		return
	}
	switch r.Method {
	case http.MethodHead:
		if f.buckets[bucket] {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	case http.MethodPut:
		f.buckets[bucket] = true
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func (f *fakeS3) made(bucket string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == "PUT /"+bucket {
			return true
		}
	}
	return false
}

func (f *fakeS3) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func minioConfig(srv *httptest.Server, bucket string, production bool) Config {
	return Config{
		Backend:    BackendMinIO,
		Bucket:     bucket,
		Production: production,
		Endpoint:   strings.TrimPrefix(srv.URL, "http://"),
		AccessKey:  "minioadmin",
		SecretKey:  "minioadmin",
	}
}

// TestOpenMinIO_CreatesMissingBucketInDev is today's dev behaviour, kept.
func TestOpenMinIO_CreatesMissingBucketInDev(t *testing.T) {
	f, srv := newFakeS3(t)
	s, err := Open(context.Background(), minioConfig(srv, "commerce-invoices", false))
	if err != nil {
		t.Fatal(err)
	}
	if !f.made("commerce-invoices") {
		t.Fatalf("dev MinIO must create the missing bucket; calls %v", f.calls)
	}
	if s.Bucket() != "commerce-invoices" {
		t.Fatalf("bucket %q", s.Bucket())
	}
}

// TestOpen_RefusesMinIOInProduction: a hand-built production Config on the
// MinIO backend is refused by Open itself, before any request — even when
// the bucket exists and nothing would need creating.
func TestOpen_RefusesMinIOInProduction(t *testing.T) {
	f, srv := newFakeS3(t, "commerce-invoices")
	_, err := Open(context.Background(), minioConfig(srv, "commerce-invoices", true))
	if !errors.Is(err, ErrProductionRefused) {
		t.Fatalf("err = %v, want ErrProductionRefused", err)
	}
	if n := f.requests(); n != 0 {
		t.Fatalf("production MinIO config reached the store (%d requests: %v)", n, f.calls)
	}
}

// TestOpenMinIO_NeverCreatesBucketInProduction guards the inner rule too:
// should the outer refusal ever be bypassed, a missing bucket is still an
// error and not a MakeBucket.
func TestOpenMinIO_NeverCreatesBucketInProduction(t *testing.T) {
	f, srv := newFakeS3(t)
	_, err := openMinIO(context.Background(), minioConfig(srv, "commerce-invoices", true))
	if !errors.Is(err, ErrProductionRefused) {
		t.Fatalf("err = %v, want ErrProductionRefused", err)
	}
	if f.made("commerce-invoices") {
		t.Fatalf("bucket created in production; calls %v", f.calls)
	}
}

func s3TestConfig(srv *httptest.Server, bucket string) Config {
	return Config{Backend: BackendS3, Bucket: bucket, Region: "ap-south-1", S3Endpoint: srv.URL}
}

// TestOpenS3_RequiresExistingBucket: S3 buckets are Terraform's. A missing
// one is reported, never created.
func TestOpenS3_RequiresExistingBucket(t *testing.T) {
	f, srv := newFakeS3(t)
	creds := credentials.NewStaticV4("AKIATEST", "secret", "")
	_, err := openS3WithCredentials(context.Background(), s3TestConfig(srv, "atpost-prod-commerce-invoices"), creds)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want a does-not-exist error", err)
	}
	if f.made("atpost-prod-commerce-invoices") {
		t.Fatalf("S3 bucket created by the service; calls %v", f.calls)
	}

	f2, srv2 := newFakeS3(t, "atpost-prod-commerce-invoices")
	s, err := openS3WithCredentials(context.Background(), s3TestConfig(srv2, "atpost-prod-commerce-invoices"), creds)
	if err != nil {
		t.Fatal(err)
	}
	if s.Bucket() != "atpost-prod-commerce-invoices" || f2.made("atpost-prod-commerce-invoices") {
		t.Fatalf("store %+v, calls %v", s, f2.calls)
	}
}

// TestOpen_S3EndpointOverrideParses: a malformed test override is an error
// from s3Options, not a panic inside minio-go.
func TestOpen_S3EndpointOverrideParses(t *testing.T) {
	_, _, err := s3Options(Config{Backend: BackendS3, Region: "ap-south-1", S3Endpoint: "http://"}, credentials.NewStaticV4("a", "b", ""))
	if err == nil {
		t.Fatal("malformed override accepted")
	}
}

// presignedHost builds a presign-only Store the way Open would for cfg and
// returns the URL PresignedGetURL hands to a browser. Presigning is pure
// client-side signing, so no server is involved — which is why the S3 case
// can use the real amazonaws.com endpoint with throwaway static test keys
// instead of IRSA.
func presignedURL(t *testing.T, cfg Config) *url.URL {
	t.Helper()
	creds := credentials.NewStaticV4("AKIATEST", "secret", "")
	var (
		endpoint string
		opts     *minio.Options
		err      error
	)
	switch cfg.Backend {
	case BackendS3:
		endpoint, opts, err = s3Options(cfg, creds)
		if err != nil {
			t.Fatal(err)
		}
	case BackendMinIO:
		endpoint, opts = cfg.Endpoint, &minio.Options{Creds: creds, Secure: cfg.UseSSL, Region: "us-east-1"}
	}
	c, err := minio.New(endpoint, opts)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{client: c, presignClient: c, bucket: cfg.Bucket}
	raw, err := s.PresignedGetURL(context.Background(), "invoices/FY26/INV-1.pdf", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestPresignedURL_S3IsVirtualHostedAndRegional: against S3 the download
// link is https://<bucket>.s3.<region>.amazonaws.com/<key> signed for the
// region, which is what a browser outside the VPC can fetch.
func TestPresignedURL_S3IsVirtualHostedAndRegional(t *testing.T) {
	u := presignedURL(t, Config{Backend: BackendS3, Bucket: "atpost-prod-commerce-invoices", Region: "ap-south-1"})
	if u.Scheme != "https" {
		t.Fatalf("scheme %q, want https: %s", u.Scheme, u)
	}
	// minio-go rewrites the AWS endpoint to its dual-stack form, so the
	// host is <bucket>.s3.dualstack.<region>.amazonaws.com; both forms are
	// real virtual-hosted S3 endpoints.
	if !strings.HasPrefix(u.Host, "atpost-prod-commerce-invoices.s3.") || !strings.HasSuffix(u.Host, ".ap-south-1.amazonaws.com") {
		t.Fatalf("host %q is not virtual-hosted style for ap-south-1: %s", u.Host, u)
	}
	if u.Path != "/invoices/FY26/INV-1.pdf" {
		t.Fatalf("path %q must not repeat the bucket: %s", u.Path, u)
	}
	cred := u.Query().Get("X-Amz-Credential")
	if !strings.Contains(cred, "/ap-south-1/s3/aws4_request") {
		t.Fatalf("X-Amz-Credential %q is not signed for ap-south-1", cred)
	}
	if u.Query().Get("X-Amz-Expires") != "900" {
		t.Fatalf("X-Amz-Expires %q, want 900", u.Query().Get("X-Amz-Expires"))
	}
}

// TestPresignedURL_MinIOIsPathStyle: the dev stack keeps today's
// http://<host>:<port>/<bucket>/<key> links.
func TestPresignedURL_MinIOIsPathStyle(t *testing.T) {
	u := presignedURL(t, Config{Backend: BackendMinIO, Bucket: "commerce-invoices", Endpoint: "localhost:9000"})
	if u.Scheme != "http" || u.Host != "localhost:9000" || u.Path != "/commerce-invoices/invoices/FY26/INV-1.pdf" {
		t.Fatalf("minio presigned URL changed shape: %s", u)
	}
}

// TestS3Options_DerivesRegionalEndpoint: no override means the regional
// AWS endpoint over TLS; an http override is honoured only because
// ConfigFromEnv has already refused it in production.
func TestS3Options_DerivesRegionalEndpoint(t *testing.T) {
	creds := credentials.NewStaticV4("a", "b", "")
	endpoint, opts, err := s3Options(Config{Backend: BackendS3, Region: "ap-south-1"}, creds)
	if err != nil || endpoint != "s3.ap-south-1.amazonaws.com" || !opts.Secure || opts.Region != "ap-south-1" {
		t.Fatalf("endpoint %q secure %v region %q err %v", endpoint, opts.Secure, opts.Region, err)
	}
	endpoint, opts, err = s3Options(Config{Backend: BackendS3, Region: "ap-south-1", S3Endpoint: "http://127.0.0.1:9000"}, creds)
	if err != nil || endpoint != "127.0.0.1:9000" || opts.Secure {
		t.Fatalf("override: endpoint %q secure %v err %v", endpoint, opts.Secure, err)
	}
}
