package processing

import (
	"encoding/binary"
	"errors"
	"testing"
)

func mp4Box(typ string, payload []byte) []byte {
	b := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(b[0:4], uint32(8+len(payload)))
	copy(b[4:8], typ)
	return append(b, payload...)
}

// mp4LargeBox is a box with a 64-bit size whose payload is NOT stored: the
// reader serves zeros for it, as an mdat the probe must skip unread.
func mp4LargeBoxHeader(typ string, payloadLen uint64) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint32(b[0:4], 1)
	copy(b[4:8], typ)
	binary.BigEndian.PutUint64(b[8:16], 16+payloadLen)
	return b
}

func mvhdV0(timescale, duration uint32) []byte {
	p := make([]byte, 100)
	binary.BigEndian.PutUint32(p[12:16], timescale)
	binary.BigEndian.PutUint32(p[16:20], duration)
	return mp4Box("mvhd", p)
}

func mvhdV1(timescale uint32, duration uint64) []byte {
	p := make([]byte, 112)
	p[0] = 1
	binary.BigEndian.PutUint32(p[20:24], timescale)
	binary.BigEndian.PutUint64(p[24:32], duration)
	return mp4Box("mvhd", p)
}

// sparseObject is an object of size bytes whose only stored bytes are the
// given segments; it counts what the probe reads.
type sparseObject struct {
	size  int64
	parts map[int64][]byte
	read  int64
	calls int
}

func (o *sparseObject) reader() RangeReader {
	return func(off, n int64) ([]byte, error) {
		o.calls++
		if off >= o.size {
			return nil, nil
		}
		if off+n > o.size {
			n = o.size - off
		}
		o.read += n
		out := make([]byte, n)
		for at, b := range o.parts {
			for i := range b {
				if p := at + int64(i); p >= off && p < off+n {
					out[p-off] = b[i]
				}
			}
		}
		return out, nil
	}
}

func contiguous(bs ...[]byte) *sparseObject {
	var all []byte
	for _, b := range bs {
		all = append(all, b...)
	}
	return &sparseObject{size: int64(len(all)), parts: map[int64][]byte{0: all}}
}

func TestMP4DurationFaststart(t *testing.T) {
	o := contiguous(mp4Box("ftyp", []byte("isom0000")), mp4Box("moov", append(mvhdV0(1000, 754_500), mp4Box("trak", make([]byte, 40))...)), mp4Box("mdat", make([]byte, 64)))
	got, err := MP4Duration(o.reader(), o.size)
	if err != nil || got != 754.5 {
		t.Fatalf("MP4Duration = %v, %v; want 754.5", got, err)
	}
}

// moov after a 20 GiB mdat with a 64-bit size: found by skipping the mdat,
// reading only headers.
func TestMP4DurationMoovAtTheEndPastALargeMdat(t *testing.T) {
	ftyp := mp4Box("ftyp", []byte("qt  0000"))
	const mdatPayload = 20 << 30
	mdat := mp4LargeBoxHeader("mdat", mdatPayload)
	moov := mp4Box("moov", mvhdV1(600, 600*4*3600))
	moovAt := int64(len(ftyp)) + 16 + mdatPayload
	o := &sparseObject{
		size:  moovAt + int64(len(moov)),
		parts: map[int64][]byte{0: ftyp, int64(len(ftyp)): mdat, moovAt: moov},
	}
	got, err := MP4Duration(o.reader(), o.size)
	if err != nil || got != 4*3600 {
		t.Fatalf("MP4Duration = %v, %v; want 14400", got, err)
	}
	if o.read > 4096 || o.calls > 8 {
		t.Fatalf("probe read %d bytes in %d calls; it must read headers only", o.read, o.calls)
	}
}

func TestMP4DurationUnknown(t *testing.T) {
	cases := map[string]*sparseObject{
		"webm":                       contiguous([]byte{0x1A, 0x45, 0xDF, 0xA3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}),
		"no moov":                    contiguous(mp4Box("ftyp", []byte("isom")), mp4Box("mdat", make([]byte, 32))),
		"moov without mvhd":          contiguous(mp4Box("moov", mp4Box("trak", make([]byte, 16)))),
		"zero duration (fragmented)": contiguous(mp4Box("moov", mvhdV0(1000, 0))),
		"zero timescale":             contiguous(mp4Box("moov", mvhdV0(0, 5000))),
		"all-ones duration":          contiguous(mp4Box("moov", mvhdV0(1000, 0xFFFFFFFF))),
		"box overruns the file": func() *sparseObject {
			b := mp4Box("ftyp", []byte("isom"))
			binary.BigEndian.PutUint32(b[0:4], 1<<20)
			return contiguous(b)
		}(),
		"box smaller than its header": func() *sparseObject {
			b := mp4Box("ftyp", []byte("isom"))
			binary.BigEndian.PutUint32(b[0:4], 4)
			return contiguous(b)
		}(),
		// A moov that claims more than the file holds is refused even when
		// its mvhd is readable: the walk never trusts a box past the object.
		"moov overruns the file": func() *sparseObject {
			b := mp4Box("moov", mvhdV0(1000, 5000))
			binary.BigEndian.PutUint32(b[0:4], uint32(len(b)+1))
			return contiguous(b)
		}(),
		"64-bit size past 2^63": func() *sparseObject {
			b := mp4LargeBoxHeader("moov", 0)
			binary.BigEndian.PutUint64(b[8:16], 1<<63+16)
			return contiguous(b, mvhdV0(1000, 5000))
		}(),
		"empty": contiguous(),
	}
	for name, o := range cases {
		if d, err := MP4Duration(o.reader(), o.size); !errors.Is(err, ErrNoMP4Duration) {
			t.Fatalf("%s: MP4Duration = %v, %v; want ErrNoMP4Duration", name, d, err)
		}
	}
}

// A file of endless tiny boxes costs a bounded number of reads.
func TestMP4DurationBoundsTheWalk(t *testing.T) {
	var many []byte
	for i := 0; i < 10_000; i++ {
		many = append(many, mp4Box("free", nil)...)
	}
	o := contiguous(many)
	if _, err := MP4Duration(o.reader(), o.size); !errors.Is(err, ErrNoMP4Duration) {
		t.Fatalf("err = %v", err)
	}
	if o.calls > mp4MaxBoxes+1 {
		t.Fatalf("%d reads for one probe", o.calls)
	}
}
