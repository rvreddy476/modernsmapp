package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

func TestNoteVideo_LimitsFollowPremium(t *testing.T) {
	std, prem := noteVideoLimitFor(false), noteVideoLimitFor(true)
	if std.MaxMs != 140_000 || std.MaxBytes != 512<<20 {
		t.Fatalf("standard limit %+v", std)
	}
	if prem.MaxMs != 600_000 || prem.MaxBytes != 0 {
		t.Fatalf("premium limit %+v", prem)
	}
	v := func(ms int, size int64) postgres.NoteMediaRow {
		return postgres.NoteMediaRow{Kind: "video", DurationMs: ms, SizeBytes: size}
	}
	if err := checkNoteVideo(v(140_000, 512<<20), std); err != nil {
		t.Fatalf("exactly at the limit: %v", err)
	}
	if err := checkNoteVideo(v(0, 1), std); err != nil {
		t.Fatalf("length not measured yet: %v", err)
	}
	wantErr(t, checkNoteVideo(v(140_001, 1), std), ErrNoteVideoTooLong)
	wantErr(t, checkNoteVideo(v(1, 512<<20+1), std), ErrNoteVideoTooLarge)
	if err := checkNoteVideo(v(599_000, 4<<30), prem); err != nil {
		t.Fatalf("premium 10 minutes: %v", err)
	}
	wantErr(t, checkNoteVideo(v(600_001, 1), prem), ErrNoteVideoTooLong)
	if err := checkNoteVideo(postgres.NoteMediaRow{Kind: "image", SizeBytes: 1 << 40}, std); err != nil {
		t.Fatalf("images are not videos: %v", err)
	}
}

func (r *noteRig) video(owner uuid.UUID, ms int, size int64) uuid.UUID {
	id := uuid.New()
	r.store.Media[id] = postgres.NoteMediaRow{UploaderID: owner, Kind: "video", MimeType: "video/mp4",
		ProcessingStatus: "uploaded", ModerationStatus: "pending", DurationMs: ms, SizeBytes: size}
	return id
}

func TestNoteVideo_CreateStoresTheLimitAndRefusesWhatIsKnownTooBig(t *testing.T) {
	r := newNoteRig(t)
	// Premium note: 10 minutes, stored on the note, post_type video.
	v, err := r.create(r.alice, func(in *CreateNoteInput) { in.MediaIDs = []uuid.UUID{r.video(r.alice, 0, 900<<20)} })
	if err != nil {
		t.Fatal(err)
	}
	n := r.store.Notes[v.Post.ID]
	if n.VideoMaxMs == nil || *n.VideoMaxMs != NoteVideoMaxMsPremium || n.Post.PostType != "video" {
		t.Fatalf("stored limit %v, post_type %q", n.VideoMaxMs, n.Post.PostType)
	}
	// A text note stores no limit.
	t2, err := r.create(r.alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.store.Notes[t2.Post.ID].VideoMaxMs != nil {
		t.Fatal("a note without video stored a video limit")
	}
	// A reply by someone without Premium: 140 s and 512 MB.
	reply := func(media uuid.UUID) (*NoteView, error) {
		return r.create(r.bob, func(in *CreateNoteInput) {
			in.ReplyToID = &v.Post.ID
			in.MediaIDs = []uuid.UUID{media}
		})
	}
	_, err = reply(r.video(r.bob, 0, 513<<20))
	wantErr(t, err, ErrNoteVideoTooLarge)
	_, err = reply(r.video(r.bob, 141_000, 1))
	wantErr(t, err, ErrNoteVideoTooLong)
	ok, err := reply(r.video(r.bob, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.store.Notes[ok.Post.ID].VideoMaxMs; got == nil || *got != NoteVideoMaxMsStandard {
		t.Fatalf("reply limit %v", got)
	}
}

func TestNoteVideo_TooLongAfterTranscodeShowsOnlyToTheAuthor(t *testing.T) {
	r := newNoteRig(t)
	limit := NoteVideoMaxMsStandard
	n := r.note(r.alice, func(n *postgres.Note) {
		n.VideoMaxMs = &limit
		n.Post.Media = []postgres.PostMedia{{MediaID: uuid.New(), Kind: "video", DurationMs: limit + 1}}
	})
	ctx := context.Background()
	if _, err := r.svc.GetNote(ctx, n.Post.ID, &r.bob); err == nil {
		t.Fatal("someone else saw a note whose video is over its limit")
	}
	mine, err := r.svc.GetNote(ctx, n.Post.ID, &r.alice)
	if err != nil {
		t.Fatal(err)
	}
	if !mine.VideoTooLong {
		t.Fatal("the author's view is not marked video_too_long")
	}
	// Within the limit: everyone sees it, unmarked.
	r.store.Notes[n.Post.ID].Post.Media[0].DurationMs = limit
	theirs, err := r.svc.GetNote(ctx, n.Post.ID, &r.bob)
	if err != nil || theirs.VideoTooLong {
		t.Fatalf("within the limit: %v %+v", err, theirs)
	}
}
