package blob

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// boundedReader serves size pseudo-random bytes and fails the test's
// premise loudly if anyone asks it to fill a buffer bigger than maxRead in
// one call — which is exactly what io.ReadAll does as its buffer grows
// past the first few hundred KiB. It deliberately does not implement
// io.WriterTo, so io.Copy has to pull through a fixed buffer.
type boundedReader struct {
	remaining int64
	maxRead   int
	seed      byte
	biggest   int
	h         io.Writer
}

var errBufferTooBig = errors.New("read into a buffer larger than the streaming bound")

func (r *boundedReader) Read(p []byte) (int, error) {
	if len(p) > r.biggest {
		r.biggest = len(p)
	}
	if len(p) > r.maxRead {
		return 0, fmt.Errorf("%w: %d > %d", errBufferTooBig, len(p), r.maxRead)
	}
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		r.seed = r.seed*31 + 7
		p[i] = r.seed
	}
	_, _ = r.h.Write(p[:n])
	r.remaining -= int64(n)
	return n, nil
}

// A 48 MiB "original" goes to disk without ever being read into a buffer
// larger than 1 MiB, and arrives byte-for-byte.
func TestCopyToFileStreamsWithoutBufferingWhole(t *testing.T) {
	const size = 48 << 20
	want := sha256.New()
	src := &boundedReader{remaining: size, maxRead: 1 << 20, h: want}
	path := filepath.Join(t.TempDir(), "original")

	n, err := copyToFile(path, src)
	if err != nil {
		t.Fatalf("copyToFile: %v (largest read buffer %d bytes)", err, src.biggest)
	}
	if n != size {
		t.Fatalf("wrote %d bytes, want %d", n, size)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := sha256.New()
	if _, err := io.Copy(got, f); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatal("file content differs from the streamed object")
	}
	if src.biggest > 1<<20 {
		t.Fatalf("largest read buffer was %d bytes", src.biggest)
	}
}

type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("connection reset")
}

// A stream that breaks part-way leaves no half-written original behind for
// the transcoder to pick up.
func TestCopyToFileRemovesPartialFileOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original")
	if _, err := copyToFile(path, &failingReader{}); err == nil {
		t.Fatal("broken stream reported success")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind: stat err = %v", err)
	}
}
