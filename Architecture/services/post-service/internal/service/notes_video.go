package service

import (
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Short video in notes (founder, 15 Nov 2026), after X's limits:
//
//   - without Premium (replies): 140 seconds and 512 MB;
//   - with Premium: 10 minutes (X's Android limit, and our Shorts rule).
//     Hours-long video stays on PostTube.
//
// media-service usually measures the length only after transcoding, so the
// limit is written on the note (posts.note_video_max_ms, migration 084)
// when it is created. A video that turns out longer than its note allows
// is shown to its author alone, marked video_too_long.
const (
	NoteVideoMaxMsStandard          = 140_000
	NoteVideoMaxMsPremium           = 600_000
	NoteVideoMaxBytesStandard int64 = 512 << 20
	postTypeVideo                   = "video"
)

var (
	ErrNoteVideoTooLong  = errors.New("note video is too long")
	ErrNoteVideoTooLarge = errors.New("note video file is too large")
)

// noteVideoLimit is what one note's video may be. MaxBytes 0 means only
// the platform's own upload cap applies.
type noteVideoLimit struct {
	MaxMs    int
	MaxBytes int64
}

func noteVideoLimitFor(premium bool) noteVideoLimit {
	if premium {
		return noteVideoLimit{MaxMs: NoteVideoMaxMsPremium}
	}
	return noteVideoLimit{MaxMs: NoteVideoMaxMsStandard, MaxBytes: NoteVideoMaxBytesStandard}
}

// checkNoteVideo refuses a video that is already known to break the
// limit: its size always, its length once media-service has measured it.
func checkNoteVideo(m postgres.NoteMediaRow, lim noteVideoLimit) error {
	if m.Kind != mediaKindVideo {
		return nil
	}
	if lim.MaxBytes > 0 && m.SizeBytes > lim.MaxBytes {
		return fmt.Errorf("%w: the file is over %d MB", ErrNoteVideoTooLarge, lim.MaxBytes>>20)
	}
	if m.DurationMs > lim.MaxMs {
		return fmt.Errorf("%w: the limit is %d seconds", ErrNoteVideoTooLong, lim.MaxMs/1000)
	}
	return nil
}

// noteVideoMax is the limit to store on a note: nil when it has no video.
func noteVideoMax(media []postgres.PostMedia, lim noteVideoLimit) *int {
	for _, m := range media {
		if m.Kind == mediaKindVideo {
			v := lim.MaxMs
			return &v
		}
	}
	return nil
}

// noteVideoTooLong reports whether a note's video, as measured, is longer
// than the limit stored on the note.
func noteVideoTooLong(p *postgres.Post, maxMs *int) bool {
	if p == nil || maxMs == nil {
		return false
	}
	for _, m := range p.Media {
		if m.Kind == mediaKindVideo && m.DurationMs > *maxMs {
			return true
		}
	}
	return false
}
