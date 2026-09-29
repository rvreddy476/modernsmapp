package fingerprint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"
)

// Extraction: ffmpeg decodes the input to 2 fps grey frames of
// ExtractSide×ExtractSide on its stdout; Go votes the crop rectangle on the
// first frames, then crops, resizes to 64×64 and hashes every frame as it
// arrives. Nothing but the frames buffered during the crop vote (bounded by
// CropMaxLookahead) is ever held in memory, whatever the video's length.

// ExtractSide is the side of the frames ffmpeg hands over. Larger than the
// hash frame so the crop is decided on real borders, small enough that the
// crop-vote buffer stays a few megabytes.
const ExtractSide = 128

// Extractor runs ffmpeg.
type Extractor struct {
	// FFmpeg is the binary; "ffmpeg" when empty.
	FFmpeg string
	// Threads is passed as -threads; 1 when zero. The worker runs
	// fingerprinting at low priority beside transcodes.
	Threads int
	// MaxFrames bounds a run; 0 means DefaultMaxFrames.
	MaxFrames int
}

// DefaultMaxFrames is 12 hours at 2 fps.
const DefaultMaxFrames = 12 * 3600 * FPS

// Result is one extracted sequence.
type Result struct {
	Frames []Frame
	Crop   Rect
	// MeasuredMs is the duration ffmpeg produced frames for.
	MeasuredMs int
}

// ErrTooManyFrames means the input ran past MaxFrames.
var ErrTooManyFrames = errors.New("fingerprint: input exceeds the frame cap")

func (e Extractor) filterArgs() []string {
	threads := e.Threads
	if threads <= 0 {
		threads = 1
	}
	return []string{
		"-nostdin", "-hide_banner", "-v", "error",
		"-threads", strconv.Itoa(threads),
		"-vf", fmt.Sprintf("fps=%d,scale=%d:%d:flags=area,format=gray", FPS, ExtractSide, ExtractSide),
		"-an", "-sn", "-dn",
		"-f", "rawvideo", "-pix_fmt", "gray", "pipe:1",
	}
}

// FromFile hashes a local file (an MP4 rendition or the original).
func (e Extractor) FromFile(ctx context.Context, path string) (*Result, error) {
	args := append([]string{"-i", path}, e.filterArgs()...)
	return e.run(ctx, args, nil)
}

// FromStream hashes a byte stream on ffmpeg's stdin. format is the demuxer
// name ("mpegts" for concatenated HLS segments).
func (e Extractor) FromStream(ctx context.Context, format string, in io.Reader) (*Result, error) {
	args := append([]string{"-f", format, "-i", "pipe:0"}, e.filterArgs()...)
	return e.run(ctx, args, in)
}

func (e Extractor) run(ctx context.Context, args []string, in io.Reader) (*Result, error) {
	bin := e.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	// After the process ends (a deadline kills it), Wait gives its I/O
	// goroutines this long before closing the pipes and returning.
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("fingerprint: ffmpeg stdout: %w", err)
	}
	var stdin io.WriteCloser
	if in != nil {
		// Not cmd.Stdin: exec would then feed the reader from a goroutine
		// that Wait waits for, and a reader blocked on a stalled segment
		// stream would hold Wait — and the job — past any deadline. The
		// copy runs in this package's own goroutine instead; closing the
		// pipe ends ffmpeg's input, and the caller's reader is its own
		// to unblock.
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("fingerprint: ffmpeg stdin: %w", err)
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("fingerprint: start ffmpeg: %w", err)
	}
	if stdin != nil {
		go func() {
			_, _ = io.Copy(stdin, in)
			_ = stdin.Close()
		}()
	}
	res, hashErr := HashRawGrey(stdout, ExtractSide, e.maxFrames())
	if hashErr != nil {
		// Stop ffmpeg; the cap or a read error already ended the stream.
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if stdin != nil {
		_ = stdin.Close()
	}
	if hashErr != nil {
		return nil, hashErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("fingerprint: ffmpeg: %w: %s", waitErr, lastLine(stderr.Bytes()))
	}
	return res, nil
}

func (e Extractor) maxFrames() int {
	if e.MaxFrames > 0 {
		return e.MaxFrames
	}
	return DefaultMaxFrames
}

func lastLine(b []byte) string {
	b = bytes.TrimSpace(b)
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		b = b[i+1:]
	}
	return string(b)
}

// HashRawGrey consumes side×side 8-bit grey frames from r (2 fps, in
// order) and returns the hashed sequence. Frames arriving before the crop
// vote decides are buffered (at most CropMaxLookahead of them) and hashed
// with the decided rectangle, so every frame of the video gets the same
// crop. maxFrames bounds the run (≤ 0 means DefaultMaxFrames); exceeding it
// is ErrTooManyFrames.
func HashRawGrey(r io.Reader, side, maxFrames int) (*Result, error) {
	if maxFrames <= 0 {
		maxFrames = DefaultMaxFrames
	}
	frameLen := side * side
	vote := NewCropVote(side)
	var pending []Grey
	var crop *Rect
	var frames []Frame
	n := 0
	hashOne := func(g Grey, t int32) error {
		small := Resample(g, crop.X0, crop.Y0, crop.X1, crop.Y1, FrameSide)
		h, st, err := PHash(small)
		if err != nil {
			return err
		}
		f := Frame{TMs: t, Hash: h}
		if FlatFromStats(st) {
			f.Flags |= FlagFlat
		}
		if IsDegenerate(h) {
			f.Flags |= FlagDegenerate
		}
		frames = append(frames, f)
		return nil
	}
	flushPending := func() error {
		for i, g := range pending {
			if err := hashOne(g, int32((n-len(pending)+i)*FrameMs)); err != nil {
				return err
			}
		}
		pending = nil
		return nil
	}
	buf := make([]byte, frameLen)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				break // a truncated tail frame is dropped
			}
			return nil, fmt.Errorf("fingerprint: read frame %d: %w", n, err)
		}
		if n >= maxFrames {
			return nil, ErrTooManyFrames
		}
		g := Grey{Side: side, Pix: append([]uint8(nil), buf...)}
		n++
		if crop == nil {
			// Flatness for the vote is judged on the uncropped frame; the
			// stored flag is recomputed after the crop.
			_, st, _ := PHash(Resample(g, 0, 0, side, side, FrameSide))
			pending = append(pending, g)
			if vote.Offer(g, FlatFromStats(st)) {
				c := vote.Decide()
				crop = &c
				if err := flushPending(); err != nil {
					return nil, err
				}
			}
			continue
		}
		if err := hashOne(g, int32((n-1)*FrameMs)); err != nil {
			return nil, err
		}
	}
	if crop == nil {
		c := vote.Decide()
		crop = &c
		if err := flushPending(); err != nil {
			return nil, err
		}
	}
	FlagStatics(frames)
	return &Result{Frames: frames, Crop: *crop, MeasuredMs: n * FrameMs}, nil
}

// DurationConsistent is the input-contract check (plan 12.1): the measured
// duration must be within 2% or 1 s of the recorded one.
func DurationConsistent(measuredMs, recordedMs int) bool {
	if recordedMs <= 0 {
		return true // nothing recorded to disagree with
	}
	diff := measuredMs - recordedMs
	if diff < 0 {
		diff = -diff
	}
	tol := recordedMs / 50
	if tol < 1000 {
		tol = 1000
	}
	return diff <= tol
}
