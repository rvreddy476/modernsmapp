// Package blob stores the FiGo settlement-file CSVs in an S3-compatible
// object store: MinIO with static keys on the dev stack, AWS S3 through the
// pod's IAM web-identity role (IRSA) in production. See config.go for the
// environment contract.
//
// Mirrors commerce-service/internal/store/blob — same SDK, same
// presigned-URL pattern. On the dev MinIO a missing bucket is created; in
// production it never is.
//
// On an object-store outage the settlement-file generator falls back to
// inline body storage in food.settlement_files.body so admin downloads
// still work; the file_url stays empty for fallback rows.
package blob

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Store struct {
	client        *minio.Client
	presignClient *minio.Client
	bucket        string
}

// Open connects to the configured backend and verifies the bucket. On the
// dev MinIO a missing bucket is created; in production it never is — the
// bucket, its policy and its encryption belong to Terraform, and a service
// that could create buckets would also be able to create them in the
// wrong place.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	switch cfg.Backend {
	case BackendS3:
		return openS3(ctx, cfg)
	case BackendMinIO:
		if cfg.Production {
			// ConfigFromEnv already refuses this; a hand-built Config must
			// not get past it either.
			return nil, fmt.Errorf("%w: the MinIO backend uses static keys", ErrProductionRefused)
		}
		return openMinIO(ctx, cfg)
	default:
		return nil, fmt.Errorf("blob: backend %q is not minio or s3", string(cfg.Backend))
	}
}

// tracedTransport instruments the object-store transport so uploads and
// presign calls appear as child spans of the settlement flow.
func tracedTransport() http.RoundTripper {
	return otelhttp.NewTransport(http.DefaultTransport)
}

// s3Options is the client configuration for the S3 backend. It is shared by
// the real IRSA client and the URL-style test, so the endpoint, region and
// TLS decisions under test are the ones production runs.
func s3Options(cfg Config, creds *credentials.Credentials) (endpoint string, opts *minio.Options, err error) {
	endpoint, secure := s3Endpoint(cfg.Region), true
	if cfg.S3Endpoint != "" {
		if endpoint, secure, err = splitEndpoint(cfg.S3Endpoint); err != nil {
			return "", nil, fmt.Errorf("blob: %s: %w", EnvS3Endpoint, err)
		}
	}
	return endpoint, &minio.Options{
		Creds:     creds,
		Secure:    secure,
		Region:    cfg.Region,
		Transport: tracedTransport(),
	}, nil
}

func openS3(ctx context.Context, cfg Config) (*Store, error) {
	// credentials.NewIAM resolves AWS_WEB_IDENTITY_TOKEN_FILE + AWS_ROLE_ARN
	// through STS (IRSA). ConfigFromEnv has already refused static keys and
	// required the two web-identity variables, so the node role is never
	// what this ends up using.
	return openS3WithCredentials(ctx, cfg, credentials.NewIAM(""))
}

// openS3WithCredentials is openS3 with the credential provider injected, so
// the bucket rule can be tested against a local S3 fake without STS.
func openS3WithCredentials(ctx context.Context, cfg Config, creds *credentials.Credentials) (*Store, error) {
	endpoint, opts, err := s3Options(cfg, creds)
	if err != nil {
		return nil, err
	}
	c, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("blob: configure S3 client: %w", err)
	}
	exists, err := c.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("blob: verify S3 bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("blob: S3 bucket %q does not exist; buckets are created by Terraform, never by the service", cfg.Bucket)
	}
	// S3 presigns against the same regional endpoint: virtual-hosted style,
	// signed for cfg.Region, valid from anywhere.
	return &Store{client: c, presignClient: c, bucket: cfg.Bucket}, nil
}

// openMinIO configures the dev MinIO client and ensures the bucket exists.
// The presignClient may use a different host than the SDK client so that
// presigned URLs returned to browsers point at a public endpoint.
func openMinIO(ctx context.Context, cfg Config) (*Store, error) {
	transport := tracedTransport()
	c, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:    cfg.UseSSL,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}
	exists, err := c.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("bucket check: %w", err)
	}
	if !exists {
		if cfg.Production {
			return nil, fmt.Errorf("%w: bucket %q does not exist and the service never creates one in production", ErrProductionRefused, cfg.Bucket)
		}
		if err := c.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("make bucket: %w", err)
		}
	}
	presign := c
	if cfg.PublicEndpoint != "" {
		pub, perr := url.Parse(cfg.PublicEndpoint)
		if perr == nil && pub.Host != "" {
			if alt, aerr := minio.New(pub.Host, &minio.Options{
				Creds:     credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
				Secure:    pub.Scheme == "https",
				Region:    "us-east-1",
				Transport: transport,
			}); aerr == nil {
				presign = alt
			}
		}
	}
	return &Store{client: c, presignClient: presign, bucket: cfg.Bucket}, nil
}

// Upload writes bytes at key with the supplied content-type.
func (s *Store) Upload(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(
		ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType},
	)
	return err
}

// PresignedGetURL returns a time-limited download URL.
func (s *Store) PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := s.presignClient.PresignedGetObject(ctx, s.bucket, key, ttl, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Store) Bucket() string { return s.bucket }
