package blob

import (
	"errors"
	"strings"
	"testing"
)

// webIdentity is the IRSA environment a production pod carries.
var webIdentity = map[string]string{
	"AWS_REGION":                  "ap-south-1",
	"AWS_WEB_IDENTITY_TOKEN_FILE": "/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
	"AWS_ROLE_ARN":                "arn:aws:iam::000000000000:role/commerce-service",
}

func getenvFrom(vars ...map[string]string) func(string) string {
	merged := map[string]string{}
	for _, m := range vars {
		for k, v := range m {
			merged[k] = v
		}
	}
	return func(key string) string { return merged[key] }
}

// TestConfigFromEnv is the backend × environment table. The production
// column is where the money is: a production pod must never end up on a
// MinIO with static keys, whether by choice or by an unset variable.
func TestConfigFromEnv(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		production bool
		wantBack   Backend
		wantErr    string // substring; "" means success
		wantRefuse bool   // errors.Is(err, ErrProductionRefused)
	}{
		{"unset dev defaults to minio", nil, false, BackendMinIO, "", false},
		{"unset prod is refused", nil, true, "", "COMMERCE_BLOB_BACKEND is unset; production requires COMMERCE_BLOB_BACKEND=s3 with IRSA", true},
		{"minio dev", map[string]string{EnvBackend: "minio"}, false, BackendMinIO, "", false},
		{"minio prod is refused", map[string]string{EnvBackend: "minio"}, true, "", "static keys", true},
		{"MINIO prod (case) is refused", map[string]string{EnvBackend: " MinIO "}, true, "", "static keys", true},
		{"s3 dev", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b"}), false, BackendS3, "", false},
		{"s3 prod", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b"}), true, BackendS3, "", false},
		{"s3 without a region", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_REGION": ""}), true, "", "requires AWS_REGION", false},
		{"s3 without a bucket", getenvMap(webIdentity, map[string]string{EnvBackend: "s3"}), true, "", "to name the bucket", false},
		{"s3 with a static access key", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_ACCESS_KEY_ID": "AKIAEXAMPLE"}), true, "", "static AWS credentials are forbidden", false},
		{"s3 with a static secret key", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_SECRET_ACCESS_KEY": "x"}), false, "", "static AWS credentials are forbidden", false},
		{"s3 without the web-identity token", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_WEB_IDENTITY_TOKEN_FILE": ""}), true, "", "node-role fallback is forbidden", false},
		{"s3 without the role arn", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_ROLE_ARN": ""}), true, "", "node-role fallback is forbidden", false},
		{"s3 endpoint override in dev", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", EnvS3Endpoint: "http://127.0.0.1:9000"}), false, BackendS3, "", false},
		{"s3 endpoint override in prod is refused", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", EnvS3Endpoint: "http://127.0.0.1:9000"}), true, "", "test-only override", true},
		{"s3 endpoint override must parse", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", EnvS3Endpoint: "http://"}), false, "", "has no host", false},
		{"unknown backend", map[string]string{EnvBackend: "gcs"}, false, "", "is not a backend", false},
		{"unknown backend in prod", map[string]string{EnvBackend: "gcs"}, true, "", "is not a backend", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ConfigFromEnv(getenvFrom(tc.env), tc.production)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("got config %+v, want error containing %q", cfg, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not mention %q", err, tc.wantErr)
				}
				if errors.Is(err, ErrProductionRefused) != tc.wantRefuse {
					t.Fatalf("errors.Is(ErrProductionRefused) = %v, want %v (err %q)", !tc.wantRefuse, tc.wantRefuse, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Backend != tc.wantBack {
				t.Fatalf("backend %q, want %q", cfg.Backend, tc.wantBack)
			}
			if cfg.Production != tc.production {
				t.Fatalf("Production %v not carried into the config", tc.production)
			}
		})
	}
}

func getenvMap(vars ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, m := range vars {
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}

// TestConfigFromEnv_DevMinIODefaults pins the dev-stack defaults the
// compose file relies on: unset means minio:9000 with the MinIO default
// keys and the commerce-invoices bucket.
func TestConfigFromEnv_DevMinIODefaults(t *testing.T) {
	cfg, err := ConfigFromEnv(getenvFrom(nil), false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "minio:9000" || cfg.AccessKey != "minioadmin" || cfg.SecretKey != "minioadmin" || cfg.UseSSL || cfg.Bucket != DefaultBucket || cfg.PublicEndpoint != "" {
		t.Fatalf("dev defaults changed: %+v", cfg)
	}
	cfg, err = ConfigFromEnv(getenvFrom(map[string]string{
		EnvMinIOEndpoint: "localhost:9100", EnvMinIOAccessKey: "a", EnvMinIOSecretKey: "s",
		EnvMinIOUseSSL: "TRUE", EnvMinIOPublicEndpoint: "http://localhost:9100",
	}), false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "localhost:9100" || cfg.AccessKey != "a" || cfg.SecretKey != "s" || !cfg.UseSSL || cfg.PublicEndpoint != "http://localhost:9100" {
		t.Fatalf("MINIO_* not honoured: %+v", cfg)
	}
}

// TestConfigFromEnv_S3Fields pins what the S3 backend carries: region, no
// endpoint override, and no MinIO leftovers.
func TestConfigFromEnv_S3Fields(t *testing.T) {
	cfg, err := ConfigFromEnv(getenvFrom(webIdentity, map[string]string{
		EnvBackend: "s3", EnvBucket: "atpost-prod-commerce-invoices",
		// MinIO variables present from a copied env file must be ignored.
		EnvMinIOEndpoint: "minio:9000", EnvMinIOAccessKey: "minioadmin", EnvMinIOSecretKey: "minioadmin",
	}), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "ap-south-1" || cfg.S3Endpoint != "" || cfg.Bucket != "atpost-prod-commerce-invoices" {
		t.Fatalf("s3 config: %+v", cfg)
	}
	if cfg.Endpoint != "" || cfg.AccessKey != "" || cfg.SecretKey != "" || cfg.PublicEndpoint != "" {
		t.Fatalf("MinIO fields leaked into the S3 config: %+v", cfg)
	}
}

// TestConfigFromEnv_BucketFallback: COMMERCE_BLOB_BUCKET wins, the older
// COMMERCE_INVOICE_BUCKET still works, and only MinIO has a default.
func TestConfigFromEnv_BucketFallback(t *testing.T) {
	cases := []struct {
		name    string
		backend string
		env     map[string]string
		want    string
	}{
		{"s3 new name", "s3", map[string]string{EnvBucket: "new", EnvLegacyBucket: "old"}, "new"},
		{"s3 legacy name", "s3", map[string]string{EnvLegacyBucket: "old"}, "old"},
		{"s3 blank new name falls to legacy", "s3", map[string]string{EnvBucket: "  ", EnvLegacyBucket: "old"}, "old"},
		{"minio new name", "minio", map[string]string{EnvBucket: "new", EnvLegacyBucket: "old"}, "new"},
		{"minio legacy name", "minio", map[string]string{EnvLegacyBucket: "old"}, "old"},
		{"minio default", "minio", nil, DefaultBucket},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ConfigFromEnv(getenvFrom(webIdentity, tc.env, map[string]string{EnvBackend: tc.backend}), false)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Bucket != tc.want {
				t.Fatalf("bucket %q, want %q", cfg.Bucket, tc.want)
			}
		})
	}
	if _, err := ConfigFromEnv(getenvFrom(webIdentity, map[string]string{EnvBackend: "s3"}), false); err == nil {
		t.Fatal("s3 must not default the bucket name: it is a Terraform output")
	}
}

func TestSplitEndpoint(t *testing.T) {
	cases := []struct {
		in     string
		host   string
		secure bool
	}{
		{"s3.ap-south-1.amazonaws.com", "s3.ap-south-1.amazonaws.com", true},
		{"127.0.0.1:9000", "127.0.0.1:9000", true},
		{"http://127.0.0.1:9000", "127.0.0.1:9000", false},
		{"https://s3.example.test", "s3.example.test", true},
	}
	for _, tc := range cases {
		host, secure, err := splitEndpoint(tc.in)
		if err != nil || host != tc.host || secure != tc.secure {
			t.Fatalf("splitEndpoint(%q) = %q,%v,%v; want %q,%v", tc.in, host, secure, err, tc.host, tc.secure)
		}
	}
	if _, _, err := splitEndpoint("http://"); err == nil {
		t.Fatal("empty host accepted")
	}
}
