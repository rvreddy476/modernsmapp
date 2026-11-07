package processing

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MP4 / QuickTime duration from the container header, read in ranges
// (2026-11-07). The transcode worker sorts a request into the short or the
// long queue BEFORE it downloads an original that may be 16 GiB, so it reads
// only the box headers it walks past and the movie header (mvhd): a handful
// of small range reads, wherever moov sits (front or back of the file). No
// ffmpeg runs: the bytes are parsed here with every length bounds-checked.
//
// Only the ISO base media family (MP4, MOV) is understood; that is what
// phones and editors upload. Anything else, a fragmented file without a
// movie duration, or a malformed header is ErrNoMP4Duration, and the caller
// treats the length as unknown.

// ErrNoMP4Duration: the header does not say how long the movie is.
var ErrNoMP4Duration = errors.New("no MP4 movie duration")

// RangeReader reads n bytes at off of one object (fewer only at its end).
type RangeReader func(off, n int64) ([]byte, error)

const (
	// mp4MaxBoxes bounds the boxes walked at one level, and so the range
	// reads one probe can cost.
	mp4MaxBoxes = 64
	// mvhdReadBytes covers a version-1 mvhd up to its duration field.
	mvhdReadBytes = 8 + 4 + 8 + 8 + 4 + 8
)

// MP4Duration is the movie duration in seconds of the MP4/MOV object of
// size bytes that read reads.
func MP4Duration(read RangeReader, size int64) (float64, error) {
	moovOff, moovEnd, err := findMP4Box(read, 0, size, "moov")
	if err != nil {
		return 0, err
	}
	mvhdOff, mvhdEnd, err := findMP4Box(read, moovOff, moovEnd, "mvhd")
	if err != nil {
		return 0, err
	}
	n := int64(mvhdReadBytes)
	if mvhdEnd-mvhdOff < n {
		n = mvhdEnd - mvhdOff
	}
	b, err := read(mvhdOff, n)
	if err != nil {
		return 0, err
	}
	return parseMVHD(b)
}

// findMP4Box walks the boxes in [start, end) and returns the first one named
// typ as [payload start, box end).
func findMP4Box(read RangeReader, start, end int64, typ string) (int64, int64, error) {
	off := start
	for i := 0; i < mp4MaxBoxes && off+8 <= end; i++ {
		h, err := read(off, 16)
		if err != nil {
			return 0, 0, err
		}
		if len(h) < 8 {
			return 0, 0, fmt.Errorf("%w: truncated box header at %d", ErrNoMP4Duration, off)
		}
		size := int64(binary.BigEndian.Uint32(h[0:4]))
		name := string(h[4:8])
		header := int64(8)
		switch size {
		case 0: // to the end of the enclosing range
			size = end - off
		case 1: // 64-bit size follows the type
			if len(h) < 16 {
				return 0, 0, fmt.Errorf("%w: truncated large box at %d", ErrNoMP4Duration, off)
			}
			// Past 2^63 this wraps negative and is refused below.
			size = int64(binary.BigEndian.Uint64(h[8:16])) //nolint:gosec // G115: bounds-checked below
			header = 16
		}
		// A box must hold its own header and stay inside its parent: the
		// walk never reads outside the object.
		if size < header || size > end-off {
			return 0, 0, fmt.Errorf("%w: box %q at %d has size %d", ErrNoMP4Duration, name, off, size)
		}
		if name == typ {
			return off + header, off + size, nil
		}
		off += size
	}
	return 0, 0, fmt.Errorf("%w: no %q box", ErrNoMP4Duration, typ)
}

// parseMVHD reads timescale and duration from an mvhd payload.
func parseMVHD(b []byte) (float64, error) {
	if len(b) < 4 {
		return 0, fmt.Errorf("%w: short mvhd", ErrNoMP4Duration)
	}
	var timescale, duration uint64
	switch b[0] { // version
	case 0:
		if len(b) < 20 {
			return 0, fmt.Errorf("%w: short mvhd", ErrNoMP4Duration)
		}
		timescale = uint64(binary.BigEndian.Uint32(b[12:16]))
		duration = uint64(binary.BigEndian.Uint32(b[16:20]))
		if duration == 0xFFFFFFFF { // "unknown"
			duration = 0
		}
	case 1:
		if len(b) < 32 {
			return 0, fmt.Errorf("%w: short mvhd", ErrNoMP4Duration)
		}
		timescale = uint64(binary.BigEndian.Uint32(b[20:24]))
		duration = binary.BigEndian.Uint64(b[24:32])
		if duration == 0xFFFFFFFFFFFFFFFF {
			duration = 0
		}
	default:
		return 0, fmt.Errorf("%w: mvhd version %d", ErrNoMP4Duration, b[0])
	}
	if timescale == 0 || duration == 0 {
		return 0, fmt.Errorf("%w: timescale %d, duration %d", ErrNoMP4Duration, timescale, duration)
	}
	return float64(duration) / float64(timescale), nil
}
