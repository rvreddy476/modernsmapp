package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Lane D10 — a selfie video media-service has not finished processing is
// refused with ErrSelfieMediaNotReady (409 MEDIA_NOT_READY) before an
// attempt is consumed.

// selfieMediaStub answers PhotoOwnerStatus for the video media only.
type selfieMediaStub struct {
	status *MediaPhotoStatus
	err    error
	calls  int
}

func (s *selfieMediaStub) PhotoOwnerStatus(_ context.Context, mediaID, _ uuid.UUID) (*MediaPhotoStatus, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	st := *s.status
	st.MediaID = mediaID
	return &st, nil
}

func (s *selfieMediaStub) PreparePhoto(_ context.Context, mediaID, _ uuid.UUID, _ bool) (*MediaPhotoStatus, error) {
	return s.PhotoOwnerStatus(context.Background(), mediaID, uuid.Nil)
}
func (s *selfieMediaStub) PhotoDeliveryURL(_ context.Context, _, _ uuid.UUID, _ string) (string, error) {
	return "", nil
}
func (s *selfieMediaStub) DeletePhotoMedia(_ context.Context, _, _ uuid.UUID) error { return nil }

func TestSelfie_MediaStillProcessingIsNotReady(t *testing.T) {
	svc, st, _ := newSelfieSvc(t)
	ctx := context.Background()
	user, video := uuid.New(), uuid.New()
	seedPendingSelfie(t, st, user)
	svc.SetLivenessClient(scripted(blinked(97)))

	// Still transcoding: 409-shaped refusal, and no attempt is spent.
	media := &selfieMediaStub{status: &MediaPhotoStatus{OwnerMatches: true, Kind: "video", Status: "processing", ModerationStatus: "passed"}}
	svc.SetMediaPhotoClient(media)
	challenge := newChallenge(t, svc, user)
	if _, err := svc.SubmitSelfie(ctx, user, video, challenge); !errors.Is(err, ErrSelfieMediaNotReady) {
		t.Fatalf("processing video: err=%v, want ErrSelfieMediaNotReady", err)
	}
	used, err := st.CountSelfieAttemptsInWindow(ctx, user)
	if err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if used != 0 {
		t.Fatalf("a not-ready video spent %d attempts", used)
	}

	// Moderation not decided yet is the same answer.
	media.status = &MediaPhotoStatus{OwnerMatches: true, Kind: "video", Status: "ready", ModerationStatus: "pending"}
	if _, err := svc.SubmitSelfie(ctx, user, video, challenge); !errors.Is(err, ErrSelfieMediaNotReady) {
		t.Fatalf("unmoderated video: err=%v, want ErrSelfieMediaNotReady", err)
	}

	// A media that is not the caller's stays the not-found answer.
	media.status, media.err = nil, ErrPhotoMediaNotFound
	if _, err := svc.SubmitSelfie(ctx, user, video, challenge); !errors.Is(err, ErrSelfieMediaNotFound) {
		t.Fatalf("foreign video: err=%v, want ErrSelfieMediaNotFound", err)
	}

	// A ready video goes through: the challenge is consumed and decided.
	media.err = nil
	media.status = &MediaPhotoStatus{OwnerMatches: true, Kind: "video", Status: "ready", ModerationStatus: "passed"}
	res, err := svc.SubmitSelfie(ctx, user, video, challenge)
	if err != nil {
		t.Fatalf("ready video: %v", err)
	}
	if !res.Passed {
		t.Fatalf("ready video result = %+v", res)
	}

	// media-service being unreachable never blocks a selfie.
	other := uuid.New()
	seedPendingSelfie(t, st, other)
	media.err = ErrPhotoMediaUnavailable
	if _, err := svc.SubmitSelfie(ctx, other, uuid.New(), newChallenge(t, svc, other)); err != nil {
		t.Fatalf("media unavailable blocked the selfie: %v", err)
	}
}
