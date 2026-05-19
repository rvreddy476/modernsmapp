package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Store struct {
	client       *minio.Client
	core         *minio.Core   // low-level client — exposes the multipart upload API
	presignClient *minio.Client // separate client for presigned URL generation (uses public endpoint)
	bucket       string
	// cdnBaseURL, when set (MEDIA_CDN_BASE_URL), fronts object reads with
	// a CDN so bytes are served from the edge instead of MinIO directly.
	cdnBaseURL string
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

	// Low-level Core client for multipart (resumable) uploads. Same
	// credentials/endpoint as the internal client.
	core, err := minio.NewCore(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create multipart client: %w", err)
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
		core:         core,
		presignClient: presignClient,
		bucket:       bucket,
		cdnBaseURL:   strings.TrimRight(os.Getenv("MEDIA_CDN_BASE_URL"), "/"),
	}, nil
}

// ObjectURL returns a URL for reading objectKey. When a CDN base is
// configured (MEDIA_CDN_BASE_URL) it returns a stable CDN-fronted URL so
// bytes are served from the edge; otherwise it falls back to a short-lived
// presigned URL straight from the object store.
func (s *Store) ObjectURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	if s.cdnBaseURL != "" {
		return s.cdnBaseURL + "/" + s.bucket + "/" + objectKey, nil
	}
	u, err := s.GeneratePresignedGetURL(ctx, objectKey, expiry)
	if err != nil {
		return "", err
	}
	return u.String(), nil
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

// MultipartPart is one finished part of a multipart upload.
type MultipartPart struct {
	PartNumber int
	ETag       string
	Size       int64
}

// InitMultipartUpload starts an S3/MinIO multipart upload and returns the
// object-store upload id that subsequent parts must reference.
func (s *Store) InitMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error) {
	return s.core.NewMultipartUpload(ctx, s.bucket, objectKey, minio.PutObjectOptions{
		ContentType: contentType,
	})
}

// UploadPart streams one part's bytes into an in-progress multipart upload
// and returns the ETag the object store assigns it.
func (s *Store) UploadPart(ctx context.Context, objectKey, storageUploadID string, partNumber int, data io.Reader, size int64) (MultipartPart, error) {
	p, err := s.core.PutObjectPart(ctx, s.bucket, objectKey, storageUploadID, partNumber, data, size, minio.PutObjectPartOptions{})
	if err != nil {
		return MultipartPart{}, fmt.Errorf("upload part %d: %w", partNumber, err)
	}
	return MultipartPart{PartNumber: p.PartNumber, ETag: p.ETag, Size: p.Size}, nil
}

// CompleteMultipartUpload assembles the uploaded parts into the final object.
// parts must be ordered by PartNumber.
func (s *Store) CompleteMultipartUpload(ctx context.Context, objectKey, storageUploadID string, parts []MultipartPart) error {
	cps := make([]minio.CompletePart, len(parts))
	for i, p := range parts {
		cps[i] = minio.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag}
	}
	_, err := s.core.CompleteMultipartUpload(ctx, s.bucket, objectKey, storageUploadID, cps, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("complete multipart upload: %w", err)
	}
	return nil
}

// AbortMultipartUpload discards an in-progress multipart upload so the
// object store does not retain its orphaned parts.
func (s *Store) AbortMultipartUpload(ctx context.Context, objectKey, storageUploadID string) error {
	return s.core.AbortMultipartUpload(ctx, s.bucket, objectKey, storageUploadID)
}
