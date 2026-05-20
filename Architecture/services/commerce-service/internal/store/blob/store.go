// Package blob is a thin MinIO wrapper for invoice HTML/PDF storage.
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

func New(endpoint, accessKey, secretKey, bucket string, useSSL bool, publicEndpoint string) (*Store, error) {
	// Phase F3.2 — instrument the MinIO transport so blob uploads /
	// presigned-URL calls appear as child spans of the invoice / asset
	// flow. MinIO's SDK accepts a custom http.Client via Options.Transport.
	tracedTransport := otelhttp.NewTransport(http.DefaultTransport)
	c, err := minio.New(endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:    useSSL,
		Transport: tracedTransport,
	})
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	exists, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("bucket check: %w", err)
	}
	if !exists {
		if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("make bucket: %w", err)
		}
	}

	presign := c
	if publicEndpoint != "" {
		pub, err := url.Parse(publicEndpoint)
		if err == nil && pub.Host != "" {
			presign, err = minio.New(pub.Host, &minio.Options{
				Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
				Secure:    pub.Scheme == "https",
				Region:    "us-east-1",
				Transport: tracedTransport,
			})
			if err != nil {
				presign = c
			}
		}
	}
	return &Store{client: c, presignClient: presign, bucket: bucket}, nil
}

// Upload writes bytes at key with content-type.
func (s *Store) Upload(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
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
