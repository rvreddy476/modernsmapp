// Package blob is a thin MinIO wrapper for FiGo settlement-file CSVs.
//
// Mirrors commerce-service/internal/store/blob/store.go — same MinIO
// SDK + presigned-URL pattern. Bucket defaults to `food` (override via
// FOOD_BLOB_BUCKET). On first use the bucket is created if missing.
//
// On a MinIO outage the settlement-file generator falls back to
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

// New configures a MinIO client + ensures the bucket exists. The
// presignClient may use a different host than the SDK client so that
// presigned URLs returned to browsers point at a public endpoint.
func New(endpoint, accessKey, secretKey, bucket string, useSSL bool, publicEndpoint string) (*Store, error) {
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
		pub, perr := url.Parse(publicEndpoint)
		if perr == nil && pub.Host != "" {
			if alt, aerr := minio.New(pub.Host, &minio.Options{
				Creds:     credentials.NewStaticV4(accessKey, secretKey, ""),
				Secure:    pub.Scheme == "https",
				Region:    "us-east-1",
				Transport: tracedTransport,
			}); aerr == nil {
				presign = alt
			}
		}
	}
	return &Store{client: c, presignClient: presign, bucket: bucket}, nil
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
