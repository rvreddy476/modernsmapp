package blob

import (
	"errors"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/foodpii"
)

// webIdentity is the IRSA environment a production pod carries.
var webIdentity = map[string]string{
	"AWS_REGION":                  "ap-south-1",
	"AWS_WEB_IDENTITY_TOKEN_FILE": "/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
	"AWS_ROLE_ARN":                "arn:aws:iam::000000000000:role/food-service",
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

func getenvFrom(vars ...map[string]string) func(string) string {
	merged := getenvMap(vars...)
	return func(key string) string { return merged[key] }
}

// TestConfigFromEnv is the backend × environment table. The production
// column is where the money is: a production pod must never end up on a
// MinIO with static keys, whether by choice or by an unset variable.
func TestConfigFromEnv(t *testing.T) {
	withMinIO := map[string]string{EnvMinIOEndpoint: "minio:9000", EnvMinIOAccessKey: "minioadmin", EnvMinIOSecretKey: "minioadmin"}
	cases := []struct {
		name       string
		env        map[string]string
		production bool
		wantBack   Backend
		wantErr    error  // errors.Is target, or nil
		wantMsg    string // substring of the error; "" means success
	}{
		{"unset dev without MINIO_ENDPOINT is not configured", nil, false, "", ErrNotConfigured, "no object store configured"},
		{"unset dev with MINIO_ENDPOINT defaults to minio", withMinIO, false, BackendMinIO, nil, ""},
		{"unset prod is refused", withMinIO, true, "", ErrProductionRefused, "FOOD_BLOB_BACKEND is unset; production requires FOOD_BLOB_BACKEND=s3 with IRSA"},
		{"unset prod without MinIO is refused too", nil, true, "", ErrProductionRefused, "FOOD_BLOB_BACKEND is unset; production requires FOOD_BLOB_BACKEND=s3 with IRSA"},
		{"minio dev", getenvMap(withMinIO, map[string]string{EnvBackend: "minio"}), false, BackendMinIO, nil, ""},
		{"minio dev needs an endpoint", map[string]string{EnvBackend: "minio"}, false, "", nil, "requires MINIO_ENDPOINT"},
		{"minio prod is refused", getenvMap(withMinIO, map[string]string{EnvBackend: "minio"}), true, "", ErrProductionRefused, "static keys"},
		{"MINIO prod (case) is refused", getenvMap(withMinIO, map[string]string{EnvBackend: " MinIO "}), true, "", ErrProductionRefused, "static keys"},
		{"s3 dev", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b"}), false, BackendS3, nil, ""},
		{"s3 prod", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b"}), true, BackendS3, nil, ""},
		{"s3 without a region", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_REGION": ""}), true, "", nil, "requires AWS_REGION"},
		{"s3 without a bucket", getenvMap(webIdentity, map[string]string{EnvBackend: "s3"}), true, "", nil, "to name the bucket"},
		{"s3 with a static access key", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_ACCESS_KEY_ID": "AKIAEXAMPLE"}), true, "", nil, "static AWS credentials are forbidden"},
		{"s3 with a static secret key", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_SECRET_ACCESS_KEY": "x"}), false, "", nil, "static AWS credentials are forbidden"},
		{"s3 without the web-identity token", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_WEB_IDENTITY_TOKEN_FILE": ""}), true, "", nil, "node-role fallback is forbidden"},
		{"s3 without the role arn", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", "AWS_ROLE_ARN": ""}), true, "", nil, "node-role fallback is forbidden"},
		{"s3 endpoint override in dev", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", EnvS3Endpoint: "http://127.0.0.1:9000"}), false, BackendS3, nil, ""},
		{"s3 endpoint override in prod is refused", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", EnvS3Endpoint: "http://127.0.0.1:9000"}), true, "", ErrProductionRefused, "test-only override"},
		{"s3 endpoint override must parse", getenvMap(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: "b", EnvS3Endpoint: "http://"}), false, "", nil, "has no host"},
		{"unknown backend", map[string]string{EnvBackend: "gcs"}, false, "", nil, "is not a backend"},
		{"unknown backend in prod", map[string]string{EnvBackend: "gcs"}, true, "", nil, "is not a backend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ConfigFromEnv(getenvFrom(tc.env), tc.production)
			if tc.wantMsg != "" {
				if err == nil {
					t.Fatalf("got config %+v, want error containing %q", cfg, tc.wantMsg)
				}
				if !strings.Contains(err.Error(), tc.wantMsg) {
					t.Fatalf("error %q does not mention %q", err, tc.wantMsg)
				}
				for _, target := range []error{ErrNotConfigured, ErrProductionRefused} {
					if errors.Is(err, target) != (tc.wantErr == target) {
						t.Fatalf("errors.Is(%v) wrong for %q", target, err)
					}
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

// TestConfigFromEnv_ProductionVerdict pins the verdict main passes in:
// foodpii.IsProduction treats everything but local/dev/development, a
// blank ENV included, as production — and that is what refuses MinIO.
func TestConfigFromEnv_ProductionVerdict(t *testing.T) {
	for _, backend := range []string{"", "minio"} {
		minio := map[string]string{EnvBackend: backend, EnvMinIOEndpoint: "minio:9000"}
		for _, env := range []string{"local", "dev", "DEV", "development"} {
			if _, err := ConfigFromEnv(getenvFrom(minio), foodpii.IsProduction(env)); err != nil {
				t.Fatalf("backend %q ENV=%q: %v", backend, env, err)
			}
		}
		for _, env := range []string{"", "prod", "production", "staging", "qa", "test"} {
			if _, err := ConfigFromEnv(getenvFrom(minio), foodpii.IsProduction(env)); !errors.Is(err, ErrProductionRefused) {
				t.Fatalf("backend %q ENV=%q: err = %v, want ErrProductionRefused", backend, env, err)
			}
		}
	}
}

// TestConfigFromEnv_DevMinIOFields pins the dev-stack reading of MINIO_*:
// no default keys (food never had them), bucket `food` unless named.
func TestConfigFromEnv_DevMinIOFields(t *testing.T) {
	cfg, err := ConfigFromEnv(getenvFrom(map[string]string{EnvMinIOEndpoint: "localhost:9100"}), false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "localhost:9100" || cfg.AccessKey != "" || cfg.SecretKey != "" || cfg.UseSSL || cfg.Bucket != DefaultBucket || cfg.PublicEndpoint != "" {
		t.Fatalf("dev defaults changed: %+v", cfg)
	}
	cfg, err = ConfigFromEnv(getenvFrom(map[string]string{
		EnvMinIOEndpoint: "localhost:9100", EnvMinIOAccessKey: "a", EnvMinIOSecretKey: "s",
		EnvMinIOUseSSL: "TRUE", EnvMinIOPublicEndpoint: "http://localhost:9100", EnvBucket: "food-dev",
	}), false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessKey != "a" || cfg.SecretKey != "s" || !cfg.UseSSL || cfg.PublicEndpoint != "http://localhost:9100" || cfg.Bucket != "food-dev" {
		t.Fatalf("MINIO_* not honoured: %+v", cfg)
	}
}

// TestConfigFromEnv_S3Fields pins what the S3 backend carries: region, no
// endpoint override, and no MinIO leftovers.
func TestConfigFromEnv_S3Fields(t *testing.T) {
	cfg, err := ConfigFromEnv(getenvFrom(webIdentity, map[string]string{
		EnvBackend: "s3", EnvBucket: "atpost-prod-food-files",
		// MinIO variables present from a copied env file must be ignored.
		EnvMinIOEndpoint: "minio:9000", EnvMinIOAccessKey: "minioadmin", EnvMinIOSecretKey: "minioadmin",
	}), true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "ap-south-1" || cfg.S3Endpoint != "" || cfg.Bucket != "atpost-prod-food-files" {
		t.Fatalf("s3 config: %+v", cfg)
	}
	if cfg.Endpoint != "" || cfg.AccessKey != "" || cfg.SecretKey != "" || cfg.PublicEndpoint != "" {
		t.Fatalf("MinIO fields leaked into the S3 config: %+v", cfg)
	}
}

// TestConfigFromEnv_BucketFallback: FOOD_BLOB_BUCKET names the bucket on
// both backends; only MinIO has a default.
func TestConfigFromEnv_BucketFallback(t *testing.T) {
	cfg, err := ConfigFromEnv(getenvFrom(webIdentity, map[string]string{EnvBackend: "s3", EnvBucket: " named "}), false)
	if err != nil || cfg.Bucket != "named" {
		t.Fatalf("s3 bucket %q err %v", cfg.Bucket, err)
	}
	if _, err := ConfigFromEnv(getenvFrom(webIdentity, map[string]string{EnvBackend: "s3"}), false); err == nil {
		t.Fatal("s3 must not default the bucket name: it is a Terraform output")
	}
	cfg, err = ConfigFromEnv(getenvFrom(map[string]string{EnvBackend: "minio", EnvMinIOEndpoint: "minio:9000"}), false)
	if err != nil || cfg.Bucket != DefaultBucket {
		t.Fatalf("minio default bucket %q err %v", cfg.Bucket, err)
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
