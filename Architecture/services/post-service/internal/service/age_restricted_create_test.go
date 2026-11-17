package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// lv-9 (long-video readiness audit): the 18+ gate is part of publishing.
// age_restricted used to be accepted only by PATCH /v1/posts/:id, so the web
// studio set it with a second call after the create, and a failed PATCH left
// an adult video published and ungated. CreatePost now writes it in the
// post's own insert, for the video kinds.

// errStopAfterWrite ends CreatePost right after its one write: the rig has
// no database for the video kinds' post-write steps (moderation record,
// video_metadata), and the row the write was handed is what is asserted.
var errStopAfterWrite = errors.New("stop after the create write")

// videoCreateRig is newCreateRig with the write captured and then failed,
// so a video kind can be created without a database.
func videoCreateRig(t *testing.T) *createRig {
	t.Helper()
	r := newCreateRig(t)
	capture := r.svc.createPostWriteSeam
	r.svc.createPostWriteSeam = func(ctx context.Context, p *postgres.Post, eventType string, payload interface{}, idem *postgres.CreateIdempotency) error {
		_ = capture(ctx, p, eventType, payload, idem)
		return errStopAfterWrite
	}
	chans := newFakeChannelStore()
	chans.byUser[r.author] = &postgres.Channel{ID: uuid.New(), UserID: r.author, Name: "Ch", Handle: "ch"}
	r.svc.channels = chans
	return r
}

func TestCreatePostStoresAgeRestrictionInTheCreateWrite(t *testing.T) {
	r := videoCreateRig(t)
	in := r.input("flick", nil)
	in.AgeRestricted = true
	if _, err := r.svc.CreatePost(context.Background(), in); !errors.Is(err, errStopAfterWrite) {
		t.Fatalf("create: %v", err)
	}
	if len(r.written) != 1 || !r.written[0].AgeRestricted {
		t.Fatalf("writes=%d; the create write did not carry age_restricted", len(r.written))
	}

	// The gate every read path runs refuses the stored row to an anonymous
	// and an under-age viewer, and lets the author and an adult in.
	row := r.written[0]
	minor, adult := uuid.New(), uuid.New()
	r.svc.birthDates = &fakeBirthDates{dob: map[uuid.UUID]*time.Time{
		minor: dayp(r.svc.clock().Year()-15, 1, 1),
		adult: dayp(r.svc.clock().Year()-30, 1, 1),
	}}
	ctx := context.Background()
	if err := r.svc.checkAgeGate(ctx, row, nil); !errors.Is(err, ErrAgeSignIn) {
		t.Fatalf("anonymous viewer: err=%v want ErrAgeSignIn", err)
	}
	if err := r.svc.checkAgeGate(ctx, row, &minor); !errors.Is(err, ErrAgeRestricted) {
		t.Fatalf("under-age viewer: err=%v want ErrAgeRestricted", err)
	}
	if err := r.svc.checkAgeGate(ctx, row, &adult); err != nil {
		t.Fatalf("adult viewer: %v", err)
	}
	author := r.author
	if err := r.svc.checkAgeGate(ctx, row, &author); err != nil {
		t.Fatalf("the author: %v", err)
	}
	if allow := r.svc.ageAllowance(ctx, &minor); allow(row) {
		t.Fatal("a listing let the under-age viewer see the restricted post")
	}

	// Without the flag the row is open, as before.
	r2 := videoCreateRig(t)
	if _, err := r2.svc.CreatePost(context.Background(), r2.input("flick", nil)); !errors.Is(err, errStopAfterWrite) {
		t.Fatal(err)
	}
	if len(r2.written) != 1 || r2.written[0].AgeRestricted {
		t.Fatal("a create without age_restricted stored it")
	}
}

// Long videos take the flag too (the legacy "video" spelling folds into
// long_video).
func TestCreatePostStoresAgeRestrictionOnALongVideo(t *testing.T) {
	r := videoCreateRig(t)
	for i, kind := range []string{"long_video", "video"} {
		in := r.input(kind, nil)
		in.Title = "Episode 1"
		in.AgeRestricted = true
		if _, err := r.svc.CreatePost(context.Background(), in); !errors.Is(err, errStopAfterWrite) {
			t.Fatalf("%s: %v", kind, err)
		}
		if len(r.written) != i+1 || !r.written[i].AgeRestricted {
			t.Fatalf("%s: age_restricted not stored", kind)
		}
	}
}

// On a kind that is not a video the request is refused, never published
// without the gate it asked for.
func TestCreatePostRefusesAgeRestrictionOnANonVideoKind(t *testing.T) {
	for _, kind := range []string{"post", "poll"} {
		t.Run(kind, func(t *testing.T) {
			r := newCreateRig(t)
			in := r.input(kind, nil)
			in.AgeRestricted = true
			p, err := r.svc.CreatePost(context.Background(), in)
			if !errors.Is(err, ErrAgeRestrictionNotAllowed) || p != nil {
				t.Fatalf("post=%v err=%v; want ErrAgeRestrictionNotAllowed", p, err)
			}
			if len(r.written) != 0 {
				t.Fatalf("a refused create wrote %d rows", len(r.written))
			}
		})
	}
}

// A composer draft carries the flag to its publication, and a draft that
// asks for it on a non-video kind is refused on save.
func TestPostDraftCarriesAgeRestriction(t *testing.T) {
	if _, err := ValidateDraftPayload("post", []byte(`{"text":"x","age_restricted":true}`)); !errors.Is(err, ErrAgeRestrictionNotAllowed) || !errors.Is(err, ErrInvalidDraft) {
		t.Fatalf("text draft with age_restricted: err=%v", err)
	}
	payload, err := ValidateDraftPayload("video", []byte(`{"text":"x","content_type":"long_video","title":"Ep 1","age_restricted":true}`))
	if err != nil {
		t.Fatalf("long-video draft with age_restricted: %v", err)
	}
	in := buildDraftCreateInput(&postgres.PostDraft{ID: uuid.New(), AuthorID: uuid.New(), PostType: "video"}, payload)
	if !in.AgeRestricted {
		t.Fatal("the draft's age_restricted did not reach CreatePost")
	}
}
