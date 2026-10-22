package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/media-service/internal/service"
	"github.com/gin-gonic/gin"
)

// Security sweep #5: writeStream copies a streamed body to the response
// through a bounded buffer. The writer below discards the bytes and records
// how they arrived, so the test holds no copy of the "video" either.

type meteredBody struct {
	n, read  int64
	maxChunk int
	closed   bool
}

func (b *meteredBody) Read(p []byte) (int, error) {
	if b.read >= b.n {
		return 0, io.EOF
	}
	if left := b.n - b.read; int64(len(p)) > left {
		p = p[:left]
	}
	b.read += int64(len(p))
	if len(p) > b.maxChunk {
		b.maxChunk = len(p)
	}
	return len(p), nil
}

func (b *meteredBody) Close() error { b.closed = true; return nil }

// discardWriter is an http.ResponseWriter that keeps headers and counts.
type discardWriter struct {
	hdr      http.Header
	status   int
	written  int64
	maxWrite int
}

func (w *discardWriter) Header() http.Header { return w.hdr }
func (w *discardWriter) WriteHeader(s int)   { w.status = s }
func (w *discardWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.written += int64(len(p))
	if len(p) > w.maxWrite {
		w.maxWrite = len(p)
	}
	return len(p), nil
}

func TestWriteStreamCopiesABodyThroughABoundedBuffer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const size = int64(256) << 20 // 256 MiB: far more than any buffer this test allows
	body := &meteredBody{n: size}
	w := &discardWriter{hdr: http.Header{}}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/media/x/serve", nil)

	writeStream(c, &service.StreamResult{Body: body, ContentType: "video/mp4", Size: size, Start: 0, End: size - 1})

	if w.status != http.StatusOK {
		t.Fatalf("status %d", w.status)
	}
	if w.written != size || body.read != size {
		t.Fatalf("wrote %d of %d read bytes; want %d", w.written, body.read, size)
	}
	if body.maxChunk > 1<<20 || w.maxWrite > 1<<20 {
		t.Fatalf("largest read %d / write %d: the copy must stay bounded", body.maxChunk, w.maxWrite)
	}
	if !body.closed {
		t.Fatal("the object reader was not closed")
	}
	if got := w.hdr.Get("Content-Length"); got != "268435456" {
		t.Fatalf("Content-Length %q", got)
	}
	if got := w.hdr.Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type %q", got)
	}
	if w.hdr.Get("Cache-Control") != "private, no-store" || w.hdr.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("headers: %v", w.hdr)
	}
}

func TestWriteStreamPartialBodyCarriesTheWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := &meteredBody{n: 100}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/media/x/serve", nil)

	writeStream(c, &service.StreamResult{Body: body, ContentType: "video/mp2t", Size: 5000, Start: 100, End: 199, Partial: true})

	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Range") != "bytes 100-199/5000" {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	if rec.Body.Len() != 100 || rec.Header().Get("Content-Length") != "100" {
		t.Fatalf("body %d bytes, Content-Length %q", rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
	if !body.closed {
		t.Fatal("the object reader was not closed")
	}
}
