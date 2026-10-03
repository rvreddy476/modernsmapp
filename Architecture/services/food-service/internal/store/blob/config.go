package blob

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Backend names the object store the settlement files are written to.
type Backend string

const (
	// BackendMinIO is the dev stack: a MinIO container reached with static
	// keys. Never accepted in production.
	BackendMinIO Backend = "minio"
	// BackendS3 is AWS S3 reached with the pod's IAM web-identity role
	// (IRSA). Static AWS keys are refused on every environment.
	BackendS3 Backend = "s3"
)

// Environment variable names. The chart sets FOOD_BLOB_BACKEND=s3 and
// FOOD_BLOB_BUCKET=<terraform output>; the dev compose file keeps the
// MINIO_* names, which still work.
const (
	EnvBackend = "FOOD_BLOB_BACKEND"
	EnvBucket  = "FOOD_BLOB_BUCKET"
	// EnvS3Endpoint overrides the AWS endpoint for tests against a local
	// S3 fake. Unset in every real deployment: the endpoint is derived from
	// AWS_REGION.
	EnvS3Endpoint = "FOOD_S3_ENDPOINT"

	EnvAWSRegion = "AWS_REGION"

	EnvMinIOEndpoint       = "MINIO_ENDPOINT"
	EnvMinIOAccessKey      = "MINIO_ACCESS_KEY"
	EnvMinIOSecretKey      = "MINIO_SECRET_KEY"
	EnvMinIOUseSSL         = "MINIO_USE_SSL"
	EnvMinIOPublicEndpoint = "MINIO_PUBLIC_ENDPOINT"

	// DefaultBucket is the dev bucket. S3 has no default: the production
	// bucket name is a Terraform output and must be passed in.
	DefaultBucket = "food"
)

// ErrProductionRefused is wrapped by every ConfigFromEnv refusal that exists
// only because the environment is production. main treats it as fatal.
var ErrProductionRefused = errors.New("blob: refused in production")

// ErrNotConfigured means no object store is wanted: a development stack
// with neither FOOD_BLOB_BACKEND nor MINIO_ENDPOINT set. Settlement files
// then stay inline in food.settlement_files.body, as they always have.
var ErrNotConfigured = errors.New("blob: no object store configured")

// Config is everything Open needs. ConfigFromEnv fills it from the
// environment; tests build it directly.
type Config struct {
	Backend    Backend
	Bucket     string
	Production bool

	// MinIO only.
	Endpoint       string
	AccessKey      string
	SecretKey      string
	UseSSL         bool
	PublicEndpoint string

	// S3 only.
	Region string
	// S3Endpoint, when set, replaces s3.<region>.amazonaws.com. Tests only.
	S3Endpoint string
}

// ConfigFromEnv reads the blob configuration. production is
// foodpii.IsProduction(ENV) — every ENV except local, dev and development,
// a blank ENV included — passed in rather than re-derived so the blob rule
// can never disagree with the PII, pricing and payments rules.
//
// Unset FOOD_BLOB_BACKEND means minio in development when MINIO_ENDPOINT is
// set, ErrNotConfigured in development when it is not, and an error in
// production. minio in production is an error whatever else is set, as is
// any static AWS key when the backend is s3.
func ConfigFromEnv(getenv func(string) string, production bool) (Config, error) {
	get := func(key string) string { return strings.TrimSpace(getenv(key)) }
	backend := Backend(strings.ToLower(get(EnvBackend)))
	cfg := Config{Backend: backend, Production: production}

	if backend == "" {
		if production {
			return Config{}, fmt.Errorf("%w: %s is unset; production requires %s=s3 with IRSA, "+
				"MinIO/static credentials are not allowed", ErrProductionRefused, EnvBackend, EnvBackend)
		}
		if get(EnvMinIOEndpoint) == "" {
			return Config{}, ErrNotConfigured
		}
		cfg.Backend = BackendMinIO
	}

	switch cfg.Backend {
	case BackendS3:
		cfg.Bucket = get(EnvBucket)
		if cfg.Bucket == "" {
			return Config{}, fmt.Errorf("blob: %s=s3 requires %s to name the bucket", EnvBackend, EnvBucket)
		}
		cfg.Region = get(EnvAWSRegion)
		if cfg.Region == "" {
			return Config{}, fmt.Errorf("blob: %s=s3 requires %s", EnvBackend, EnvAWSRegion)
		}
		if get("AWS_ACCESS_KEY_ID") != "" || get("AWS_SECRET_ACCESS_KEY") != "" {
			return Config{}, errors.New("blob: static AWS credentials are forbidden; the S3 backend uses the IAM web-identity role (IRSA) only")
		}
		if get("AWS_WEB_IDENTITY_TOKEN_FILE") == "" || get("AWS_ROLE_ARN") == "" {
			return Config{}, errors.New("blob: AWS_WEB_IDENTITY_TOKEN_FILE and AWS_ROLE_ARN are required for the S3 backend; node-role fallback is forbidden")
		}
		cfg.S3Endpoint = get(EnvS3Endpoint)
		if cfg.S3Endpoint != "" {
			if production {
				return Config{}, fmt.Errorf("%w: %s is a test-only override and must be unset in production", ErrProductionRefused, EnvS3Endpoint)
			}
			if _, _, err := splitEndpoint(cfg.S3Endpoint); err != nil {
				return Config{}, fmt.Errorf("blob: %s: %w", EnvS3Endpoint, err)
			}
		}
		return cfg, nil

	case BackendMinIO:
		if production {
			return Config{}, fmt.Errorf("%w: %s=minio uses static keys; production requires %s=s3 with IRSA, "+
				"MinIO/static credentials are not allowed", ErrProductionRefused, EnvBackend, EnvBackend)
		}
		cfg.Bucket = firstNonEmpty(get(EnvBucket), DefaultBucket)
		cfg.Endpoint = get(EnvMinIOEndpoint)
		if cfg.Endpoint == "" {
			return Config{}, fmt.Errorf("blob: %s=minio requires %s", EnvBackend, EnvMinIOEndpoint)
		}
		cfg.AccessKey = get(EnvMinIOAccessKey)
		cfg.SecretKey = get(EnvMinIOSecretKey)
		cfg.UseSSL = strings.EqualFold(get(EnvMinIOUseSSL), "true")
		cfg.PublicEndpoint = get(EnvMinIOPublicEndpoint)
		return cfg, nil

	default:
		return Config{}, fmt.Errorf("blob: %s=%q is not a backend; expected minio or s3", EnvBackend, string(backend))
	}
}

// s3Endpoint is the AWS endpoint for region: virtual-hosted-style URLs
// (https://<bucket>.s3.<region>.amazonaws.com/<key>) follow from it because
// minio-go recognises the amazonaws.com host and signs for that region.
func s3Endpoint(region string) string {
	return "s3." + region + ".amazonaws.com"
}

// splitEndpoint accepts "host[:port]" or "scheme://host[:port]" and returns
// the host and whether TLS is used (true unless the scheme is http).
func splitEndpoint(raw string) (host string, secure bool, err error) {
	if !strings.Contains(raw, "://") {
		return raw, true, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, err
	}
	if u.Host == "" {
		return "", false, fmt.Errorf("%q has no host", raw)
	}
	return u.Host, u.Scheme != "http", nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
