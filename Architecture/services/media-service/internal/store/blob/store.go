package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Store struct {
	client       *minio.Client
	presignClient *minio.Client // separate client for presigned URL generation (uses public endpoint)
	bucket       string
}

func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Store, error) {
	return NewWithPublicEndpoint(endpoint, accessKey, secretKey, bucket, useSSL, "")
}

func NewWithPublicEndpoint(endpoint, accessKey, secretKey, bucket string, useSSL bool, publicEndpoint string) (*Store, error) {
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}

	// Ensure bucket exists
	ctx := context.Background()
	exists, err := minioClient.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket existence: %w", err)
	}
	if !exists {
		if err := minioClient.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	// Create a second client for presigned URLs using the public endpoint.
	// Presigned URL generation is a client-side crypto operation — the URL
	// host must match what the browser sends so MinIO's signature check passes.
	presignClient := minioClient
	if publicEndpoint != "" {
		pub, err := url.Parse(publicEndpoint)
		if err == nil && pub.Host != "" {
			// Set Region to avoid a network call to look up bucket location.
			// The presign client can't reach MinIO via the public endpoint from
			// inside Docker, but it doesn't need to — presigned URL generation
			// is a pure crypto operation once the region is known.
			presignClient, err = minio.New(pub.Host, &minio.Options{
				Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
				Secure: pub.Scheme == "https",
				Region: "us-east-1",
			})
			if err != nil {
				// Fall back to internal client if public client creation fails
				presignClient = minioClient
			}
		}
	}

	return &Store{
		client:       minioClient,
		presignClient: presignClient,
		bucket:       bucket,
	}, nil
}

func (s *Store) GeneratePresignedPutURL(ctx context.Context, objectKey string, expiry time.Duration) (*url.URL, error) {
	return s.presignClient.PresignedPutObject(ctx, s.bucket, objectKey, expiry)
}

func (s *Store) GeneratePresignedGetURL(ctx context.Context, objectKey string, expiry time.Duration) (*url.URL, error) {
	reqParams := make(url.Values)
	return s.presignClient.PresignedGetObject(ctx, s.bucket, objectKey, expiry, reqParams)
}

// DownloadObject fetches an object's content from the bucket.
func (s *Store) DownloadObject(ctx context.Context, objectKey string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object %s: %w", objectKey, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read object %s: %w", objectKey, err)
	}
	return data, nil
}

// UploadObject puts data into the bucket at the given key.
func (s *Store) UploadObject(ctx context.Context, objectKey string, data []byte, contentType string) error {
	reader := bytes.NewReader(data)
	_, err := s.client.PutObject(ctx, s.bucket, objectKey, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to upload object %s: %w", objectKey, err)
	}
	return nil
}

// DeleteObject removes an object from the bucket.
func (s *Store) DeleteObject(ctx context.Context, objectKey string) error {
	return s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{})
}

func (s *Store) Bucket() string {
	return s.bucket
}
