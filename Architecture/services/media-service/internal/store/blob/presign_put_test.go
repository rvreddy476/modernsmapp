package blob

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Reels audit 2026-10-30, finding 5: the presigned PUT signs the declared
// Content-Type and Content-Length, so the holder cannot store any other
// type (text/html on the media origin) or size. Presigning is local
// arithmetic; no object store is contacted.
func TestPresignedPutSignsContentTypeAndLength(t *testing.T) {
	c, err := minio.New("objects.example.test", &minio.Options{
		Creds: credentials.NewStaticV4("AKIDEXAMPLE", "not-a-real-secret", ""), Secure: true, Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{presignClient: c, bucket: "media"}
	u, err := s.GeneratePresignedPutURLFor(context.Background(), "user/u/m/original", 15*time.Minute, "video/mp4", 1234)
	if err != nil {
		t.Fatal(err)
	}
	signed := u.Query().Get("X-Amz-SignedHeaders")
	for _, h := range []string{"content-type", "content-length", "host"} {
		if !strings.Contains(";"+signed+";", ";"+h+";") {
			t.Fatalf("signed headers %q miss %s", signed, h)
		}
	}
	h, err := PresignedPutHeaders("video/mp4", 1234)
	if err != nil || h.Get("Content-Type") != "video/mp4" || h.Get("Content-Length") != "1234" {
		t.Fatalf("headers %v %v", h, err)
	}
}

func TestPresignedPutRefusesUnboundHeaders(t *testing.T) {
	for _, tc := range []struct {
		ct   string
		size int64
	}{{"", 10}, {"video/mp4\r\nX-Evil: 1", 10}, {"video/mp4", 0}, {"video/mp4", -1}} {
		if _, err := PresignedPutHeaders(tc.ct, tc.size); err == nil {
			t.Fatalf("%q/%d accepted", tc.ct, tc.size)
		}
	}
}
