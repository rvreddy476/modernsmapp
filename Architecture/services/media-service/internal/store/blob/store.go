package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrObjectNotFound is returned when the backing object store resolves that
// an object does not exist. Callers use the distinction to reject an upload
// confirmation and to quarantine an impossible transcode instead of retrying
// it forever and stalling the Kafka partition.
var ErrObjectNotFound = errors.New("blob: object not found")

// ObjectInfo is the small, provider-neutral subset of object metadata needed
// by the service. It deliberately does not expose MinIO/S3 implementation
// types outside this package.
type ObjectInfo struct {
	Size        int64
	ContentType string
	ETag        string
}

type Store struct {
	client        *minio.Client
	core          *minio.Core   // low-level client — exposes the multipart upload API
	presignClient *minio.Client // separate client for presigned URL generation (uses public endpoint)
	bucket        string
	// cdnBaseURL, when set (MEDIA_CDN_BASE_URL), fronts object reads with
	// a CDN so bytes are served from the edge instead of MinIO directly.
	cdnBaseURL string
}

func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Store, error) {
	return NewWithPublicEndpoint(endpoint, accessKey, secretKey, bucket, useSSL, "")
}

// EnvAllowInstanceRole opts a local/dev host into the EC2 instance-profile
// credential when it has no web identity: the single-server dev stack
// (infra/devbox) runs on EC2 with an instance role and no EKS. It is honoured
// only when ENV is local/dev/development and no other environment variable
// names anything else, so a production or QA pod can never take it.
const EnvAllowInstanceRole = "S3_ALLOW_INSTANCE_ROLE"

// checkS3Credentials is NewS3IRSA's credential rule. Static keys are refused
// everywhere. A web identity (IRSA) is required, except on a local/dev host
// that set S3_ALLOW_INSTANCE_ROLE=true, where credentials.NewIAM falls
// through to the EC2 instance metadata service (IMDSv2).
func checkS3Credentials(getenv func(string) string) error {
	if getenv("AWS_ACCESS_KEY_ID") != "" || getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return fmt.Errorf("s3/irsa: static AWS credentials are forbidden")
	}
	if getenv("AWS_WEB_IDENTITY_TOKEN_FILE") != "" && getenv("AWS_ROLE_ARN") != "" {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(getenv(EnvAllowInstanceRole)), "true") && isLocalDevEnv(getenv) {
		return nil
	}
	return fmt.Errorf("s3/irsa: AWS_WEB_IDENTITY_TOKEN_FILE and AWS_ROLE_ARN are required; node-role fallback is forbidden " +
		"(a local/dev EC2 host may set " + EnvAllowInstanceRole + "=true)")
}

// isLocalDevEnv: ENV is local/dev/development and none of DEPLOY_ENV,
// APP_ENV, ENVIRONMENT names another environment. A blank ENV is not local.
func isLocalDevEnv(getenv func(string) string) bool {
	local := func(v string) bool {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "local", "dev", "development":
			return true
		}
		return false
	}
	if !local(getenv("ENV")) {
		return false
	}
	for _, k := range []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT"} {
		if v := strings.TrimSpace(getenv(k)); v != "" && !local(v) {
			return false
		}
	}
	return true
}

// NewS3IRSA configures the existing S3-compatible client for AWS S3 using an
// EKS web-identity credential. It deliberately refuses static keys and does
// not fall back to the node instance profile, except on an explicitly opted-in
// local/dev host (checkS3Credentials).
func NewS3IRSA(region, bucket string) (*Store, error) {
	if strings.TrimSpace(region) == "" || strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("s3/irsa: AWS_REGION and S3_BUCKET are required")
	}
	if err := checkS3Credentials(os.Getenv); err != nil {
		return nil, err
	}

	endpoint := "s3." + region + ".amazonaws.com"
	creds := credentials.NewIAM("")
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  creds,
		Secure: true,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("configure S3 client: %w", err)
	}
	core, err := minio.NewCore(endpoint, &minio.Options{
		Creds:  creds,
		Secure: true,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("configure S3 multipart client: %w", err)
	}
	// Production infrastructure owns bucket creation, public-access blocking,
	// KMS and OAC. The application verifies access but never mutates policy.
	if exists, err := client.BucketExists(context.Background(), bucket); err != nil {
		return nil, fmt.Errorf("verify S3 bucket: %w", err)
	} else if !exists {
		return nil, fmt.Errorf("S3 bucket %q does not exist", bucket)
	}
	return &Store{
		client:        client,
		core:          core,
		presignClient: client,
		bucket:        bucket,
		cdnBaseURL:    strings.TrimRight(os.Getenv("MEDIA_CDN_BASE_URL"), "/"),
	}, nil
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
		client:        minioClient,
		core:          core,
		presignClient: presignClient,
		bucket:        bucket,
		cdnBaseURL:    strings.TrimRight(os.Getenv("MEDIA_CDN_BASE_URL"), "/"),
	}, nil
}

// ObjectURL returns a URL for reading objectKey.
//
// Module 4 M4-P0-5 — THIS FUNCTION USED TO PUBLISH PROTECTED BYTES.
//
// It previously returned `<cdnBaseURL>/<bucket>/<objectKey>` for ANY key
// whenever MEDIA_CDN_BASE_URL was set. That URL is stable, unauthenticated and
// permanent, so every content-level authorization in the platform governed only
// the JSON: the bytes stayed reachable to anyone who had ever seen the link,
// and stayed reachable after a block, a takedown or a deletion.
//
// A stable URL is now issued ONLY for keys in the public prefix. A protected
// key must go through delivery.Signer.SignProtected, which requires content
// authorization first and bounds the URL to a short TTL. Returning an error is
// deliberate: the caller has to decide, and there is no fallback that quietly
// serves the bytes anyway.
func (s *Store) ObjectURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	if delivery.ClassForKey(objectKey) != delivery.ClassPublic {
		return "", fmt.Errorf(
			"blob: %q is protected; use the authorized signed-delivery path rather than a stable URL",
			objectKey)
	}
	if s.cdnBaseURL != "" {
		return s.cdnBaseURL + "/" + s.bucket + "/" + objectKey, nil
	}
	// No CDN configured: a bounded presigned URL is still bounded.
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

// GeneratePresignedDownloadURL is GeneratePresignedGetURL with the
// response-content-disposition override S3/MinIO honour, so the browser
// saves the object as an attachment named filename instead of rendering
// it. The override is part of the signature: it cannot be edited off.
func (s *Store) GeneratePresignedDownloadURL(ctx context.Context, objectKey string, expiry time.Duration, filename string) (*url.URL, error) {
	reqParams := make(url.Values)
	reqParams.Set("response-content-disposition", delivery.ContentDispositionAttachment(filename))
	return s.presignClient.PresignedGetObject(ctx, s.bucket, objectKey, expiry, reqParams)
}

// StatObject verifies that objectKey exists without downloading its bytes.
func (s *Store) StatObject(ctx context.Context, objectKey string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, normalizeObjectError(objectKey, err)
	}
	return ObjectInfo{Size: info.Size, ContentType: info.ContentType, ETag: info.ETag}, nil
}

// ReadObjectRange downloads only the inclusive byte range [start,end]. It is
// used for magic-byte validation so confirming a multi-gigabyte upload never
// buffers the entire object in the API process.
func (s *Store) ReadObjectRange(ctx context.Context, objectKey string, start, end int64) ([]byte, error) {
	if start < 0 || end < start {
		return nil, fmt.Errorf("blob: invalid byte range %d-%d", start, end)
	}
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(start, end); err != nil {
		return nil, fmt.Errorf("blob: set range for %s: %w", objectKey, err)
	}
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey, opts)
	if err != nil {
		return nil, normalizeObjectError(objectKey, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, normalizeObjectError(objectKey, err)
	}
	return data, nil
}

// OpenObjectRange returns a streaming reader over the inclusive byte range
// [start,end] of an object. The anonymous-scope serve path reads through it
// (security sweep #5): an unranged GET of a 10-minute video used to go
// through DownloadObject's io.ReadAll, so N parallel viewers held N whole
// objects in this process. The caller closes the reader.
func (s *Store) OpenObjectRange(ctx context.Context, objectKey string, start, end int64) (io.ReadCloser, error) {
	if start < 0 || end < start {
		return nil, fmt.Errorf("blob: invalid byte range %d-%d", start, end)
	}
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(start, end); err != nil {
		return nil, fmt.Errorf("blob: set range for %s: %w", objectKey, err)
	}
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey, opts)
	if err != nil {
		return nil, normalizeObjectError(objectKey, err)
	}
	// GetObject is lazy: Stat forces the first request, so a missing key is
	// reported here rather than on the first Read.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, normalizeObjectError(objectKey, err)
	}
	return obj, nil
}

// OpenObject returns a streaming reader over an object's bytes plus its
// metadata (size, ETag). The fingerprint worker reads HLS segments, MP4
// variants and originals through it so nothing is buffered whole in memory
// and no presigned URL is ever created for the purpose. The caller closes
// the reader.
func (s *Store) OpenObject(ctx context.Context, objectKey string) (io.ReadCloser, ObjectInfo, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, normalizeObjectError(objectKey, err)
	}
	// GetObject is lazy: Stat forces the first request, so a missing key is
	// reported here rather than on the first Read.
	st, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, ObjectInfo{}, normalizeObjectError(objectKey, err)
	}
	return obj, ObjectInfo{Size: st.Size, ContentType: st.ContentType, ETag: st.ETag}, nil
}

// DownloadObject fetches an object's content from the bucket into memory.
// Use it only for objects with a known small bound (images, playlists,
// audio); a video original or rendition goes through DownloadObjectToFile.
func (s *Store) DownloadObject(ctx context.Context, objectKey string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object %s: %w", objectKey, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, normalizeObjectError(objectKey, err)
	}
	return data, nil
}

// DownloadObjectToFile streams an object straight into a new file at path
// and returns the number of bytes written. Nothing is buffered whole: long
// videos have no size limit (founder, 2026-10-21), and an original of
// several GiB read with io.ReadAll would blow the worker's memory limit.
// The file is created (or truncated) with mode 0600; on any error the
// partial file is removed. The caller needs free disk for the whole object.
// A missing object is reported as ErrObjectNotFound.
func (s *Store) DownloadObjectToFile(ctx context.Context, objectKey, path string) (int64, error) {
	rc, _, err := s.OpenObject(ctx, objectKey)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	n, err := copyToFile(path, rc)
	if err != nil {
		return n, normalizeObjectError(objectKey, err)
	}
	return n, nil
}

// copyToFile is DownloadObjectToFile's streaming half, split out so it can
// be tested without an object store. io.Copy moves the bytes through a
// small fixed buffer (or the platform's file ReadFrom), never the whole
// stream at once.
func copyToFile(path string, r io.Reader) (int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return n, err
	}
	return n, nil
}

// UploadFile streams a local file into the bucket at objectKey and returns
// its size. The client reads the file by offset part by part (multipart for
// anything past 16 MiB), so a multi-GiB rendition is never held in memory
// the way UploadObject's []byte would hold it.
func (s *Store) UploadFile(ctx context.Context, objectKey, path, contentType string) (int64, error) {
	info, err := s.client.FPutObject(ctx, s.bucket, objectKey, path, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to upload file to object %s: %w", objectKey, err)
	}
	return info.Size, nil
}

func normalizeObjectError(objectKey string, err error) error {
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NoSuchObject", "NoSuchBucket", "NotFound":
		return fmt.Errorf("%w: %s", ErrObjectNotFound, objectKey)
	default:
		return fmt.Errorf("blob: object %s: %w", objectKey, err)
	}
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

// ── Reads from another bucket (live recording import, 1 Oct 2026) ──────
//
// The three methods below are the only ones that name a bucket other than
// this store's own, and they exist for one caller:
// service.RecordingImporter, which has already checked srcBucket against
// its configured allow-list. None of them writes to or deletes from the
// source bucket.

// ErrObjectChanged means a server-side copy was refused because the source
// object no longer has the ETag the caller validated.
var ErrObjectChanged = errors.New("blob: source object changed since it was validated")

// StatObjectIn is StatObject on srcBucket.
func (s *Store) StatObjectIn(ctx context.Context, srcBucket, objectKey string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, srcBucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, normalizeObjectError(objectKey, err)
	}
	return ObjectInfo{Size: info.Size, ContentType: info.ContentType, ETag: info.ETag}, nil
}

// ReadObjectRangeIn is ReadObjectRange on srcBucket.
func (s *Store) ReadObjectRangeIn(ctx context.Context, srcBucket, objectKey string, start, end int64) ([]byte, error) {
	if start < 0 || end < start {
		return nil, fmt.Errorf("blob: invalid byte range %d-%d", start, end)
	}
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(start, end); err != nil {
		return nil, fmt.Errorf("blob: set range for %s: %w", objectKey, err)
	}
	obj, err := s.client.GetObject(ctx, srcBucket, objectKey, opts)
	if err != nil {
		return nil, normalizeObjectError(objectKey, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, normalizeObjectError(objectKey, err)
	}
	return data, nil
}

// CopyObjectFrom copies srcBucket/srcKey into this store's bucket at dstKey
// entirely on the object store (S3 CopyObject; no bytes pass through this
// process). srcETag, when set, is a precondition: a source replaced after
// it was validated is refused with ErrObjectChanged rather than copied.
// The destination gets only contentType as metadata; nothing of the
// source's metadata is carried over. A single CopyObject is bounded at
// 5 GiB, above any size the importer accepts.
//
// The answer is a fresh stat of the DESTINATION (size and ETag as stored),
// because CopyObject's own response carries no size.
func (s *Store) CopyObjectFrom(ctx context.Context, srcBucket, srcKey, srcETag, dstKey, contentType string) (ObjectInfo, error) {
	src := minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey, MatchETag: srcETag}
	dst := minio.CopyDestOptions{
		Bucket:          s.bucket,
		Object:          dstKey,
		ReplaceMetadata: true,
		UserMetadata:    map[string]string{"Content-Type": contentType},
	}
	if _, err := s.client.CopyObject(ctx, dst, src); err != nil {
		if minio.ToErrorResponse(err).Code == "PreconditionFailed" {
			return ObjectInfo{}, fmt.Errorf("%w: %s", ErrObjectChanged, srcKey)
		}
		return ObjectInfo{}, normalizeObjectError(srcKey, err)
	}
	return s.StatObject(ctx, dstKey)
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

// ListObjectKeys returns every object key under prefix (recursive). Used by
// the asset purge to remove EVERYTHING an asset ever produced — original,
// variants, thumbnails, frames, hls/* — without trusting the row tables to
// have recorded each key.
func (s *Store) ListObjectKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return keys, fmt.Errorf("blob: list %s: %w", prefix, obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}
