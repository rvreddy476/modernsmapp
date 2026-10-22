package service

import (
	"context"
	"io"
	"testing"

	"github.com/atpost/media-service/internal/store/blob"
)

// Security sweep #5: the anonymous-scope stream used to io.ReadAll the
// whole object on an unranged GET, so N viewers of a long anonymous video
// held N copies of it in this process. readObjectRange now opens the
// window as a stream and reads nothing until the handler copies it.

// countingReader hands out n zero bytes and records how it was read.
type countingReader struct {
	n, read  int64
	maxChunk int
	closed   bool
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.read >= r.n {
		return 0, io.EOF
	}
	left := r.n - r.read
	if int64(len(p)) > left {
		p = p[:left]
	}
	for i := range p {
		p[i] = 0
	}
	r.read += int64(len(p))
	if len(p) > r.maxChunk {
		r.maxChunk = len(p)
	}
	return len(p), nil
}

func (r *countingReader) Close() error { r.closed = true; return nil }

// bigObject is an object store holding one object of `size` bytes that is
// never materialised: every open returns a countingReader over the window.
type bigObject struct {
	size       int64
	opens      int
	start, end int64
	reader     *countingReader
}

func (o *bigObject) StatObject(context.Context, string) (blob.ObjectInfo, error) {
	return blob.ObjectInfo{Size: o.size, ContentType: "video/mp4"}, nil
}

func (o *bigObject) OpenObjectRange(_ context.Context, _ string, start, end int64) (io.ReadCloser, error) {
	o.opens++
	o.start, o.end = start, end
	o.reader = &countingReader{n: end - start + 1}
	return o.reader, nil
}

func TestAnonymousStreamNeverBuffersTheObject(t *testing.T) {
	const size = int64(1)<<32 + 7 // past int32: a long video, not a thumbnail
	obj := &bigObject{size: size}
	s := (&Service{}).WithObjectReader(obj)

	res, err := s.readObjectRange(context.Background(), "user/u/m/original", "video/mp4", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Body == nil || res.Data != nil {
		t.Fatalf("an unranged GET must come back as a stream, not a buffer: body=%v data=%d bytes", res.Body != nil, len(res.Data))
	}
	if obj.opens != 1 || obj.start != 0 || obj.end != size-1 {
		t.Fatalf("opened %d time(s) for [%d,%d]; want once for [0,%d]", obj.opens, obj.start, obj.end, size-1)
	}
	if obj.reader.read != 0 {
		t.Fatalf("%d bytes were read before the handler asked for any", obj.reader.read)
	}
	if res.Partial || res.Start != 0 || res.End != size-1 || res.Size != size || res.Length() != size {
		t.Fatalf("result window: partial=%v %d-%d/%d len=%d", res.Partial, res.Start, res.End, res.Size, res.Length())
	}
	// Draining the stream moves every byte through a bounded buffer.
	n, err := io.Copy(io.Discard, res.Body)
	if err != nil || n != size {
		t.Fatalf("copied %d bytes, err %v; want %d", n, err, size)
	}
	if obj.reader.maxChunk > 1<<20 {
		t.Fatalf("largest single read was %d bytes; the copy must stay bounded", obj.reader.maxChunk)
	}
}

func TestAnonymousStreamRangeOpensOnlyTheWindow(t *testing.T) {
	obj := &bigObject{size: 1_000_000}
	s := (&Service{}).WithObjectReader(obj)

	res, err := s.readObjectRange(context.Background(), "k", "", "bytes=100-199")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial || res.Start != 100 || res.End != 199 || res.Length() != 100 {
		t.Fatalf("window: partial=%v %d-%d len=%d", res.Partial, res.Start, res.End, res.Length())
	}
	if obj.start != 100 || obj.end != 199 {
		t.Fatalf("object opened for [%d,%d], want [100,199]", obj.start, obj.end)
	}
	if res.ContentType != "video/mp4" {
		t.Fatalf("content type %q did not fall back to the object's", res.ContentType)
	}
	n, _ := io.Copy(io.Discard, res.Body)
	if n != 100 {
		t.Fatalf("the window carried %d bytes, want 100", n)
	}

	// An open-ended range is the tail window, still a stream.
	res, err = s.readObjectRange(context.Background(), "k", "", "bytes=999_000-")
	if err == nil && res.Body == nil {
		t.Fatal("an open-ended range came back buffered")
	}
	if _, err := s.readObjectRange(context.Background(), "k", "", "bytes=5000000-"); err != ErrRangeNotSatisfiable {
		t.Fatalf("a range past the end: %v, want ErrRangeNotSatisfiable", err)
	}
}

func TestAnonymousStreamEmptyObjectHasNoBody(t *testing.T) {
	s := (&Service{}).WithObjectReader(&bigObject{size: 0})
	res, err := s.readObjectRange(context.Background(), "k", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Body != nil || res.Length() != 0 {
		t.Fatalf("an empty object answered a %d-byte stream", res.Length())
	}
}
