package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// The media-authority gate — C-LB-4, and the assertions NC-C4A and NC-C4B
// mutate.
//
// ## WHY THIS FILE EXISTS
//
// This gate decides whether one user can publish another user's asset, and
// whether an asset that is still uploading, still being processed, or still
// unreviewed by moderation can be made public. It had NO test. `verifyMediaAuthority`
// reaches for `s.pgStore`, a concrete `*postgres.Store`, so every one of those
// four rules was reachable only through a live database — and so none of them
// was covered by anything the gate actually runs.
//
// The decision is now a pure function over one row, which is what makes the
// table below possible. The batching and the fail-closed store error stay in
// the caller; they are a different concern and a different failure mode.
func TestCheckMediaAuthority(t *testing.T) {
	author := uuid.New()
	stranger := uuid.New()
	mediaID := uuid.New()

	// A row that passes every gate. Each case below breaks exactly one thing,
	// so a failure names the rule that broke rather than "the fixture".
	publishable := postgres.MediaOwnership{
		UploaderID:       author,
		Kind:             "image",
		ProcessingStatus: "ready",
		ModerationStatus: "passed",
	}

	withProcessing := func(status string) postgres.MediaOwnership {
		m := publishable
		m.ProcessingStatus = status
		return m
	}
	withModeration := func(status string) postgres.MediaOwnership {
		m := publishable
		m.ModerationStatus = status
		return m
	}

	tests := []struct {
		name        string
		row         postgres.MediaOwnership
		found       bool
		contentType string
		postType    string
		wantErr     error
		why         string
	}{
		{
			name:        "a ready, passed, owned image publishes",
			row:         publishable,
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     nil,
			why:         "the one combination that is allowed",
		},
		{
			name:        "a missing row is refused",
			row:         postgres.MediaOwnership{},
			found:       false,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotFound,
			why: "a typo'd or already-reclaimed id used to be treated as an image, " +
				"producing a post with a permanently broken attachment",
		},
		{
			name: "another user's media is refused",
			row: postgres.MediaOwnership{
				UploaderID:       stranger,
				Kind:             "image",
				ProcessingStatus: "ready",
				ModerationStatus: "passed",
			},
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotOwned,
			why: "media ids are UUIDs in URLs; without this, holding one is enough " +
				"to attach a stranger's photo to your own post",
		},

		// PROCESSING. The column is CHECK-constrained to
		// (pending_upload, uploaded, processing, ready, failed). Every value
		// that is not `ready` is listed, so adding a state to the enum without
		// deciding about it here shows up as an untested value rather than as
		// an accidental allow.
		{
			name:        "pending_upload is refused",
			row:         withProcessing("pending_upload"),
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotReady,
			why:         "the bytes have not arrived; the post would show a broken image",
		},
		{
			name:        "uploaded is refused",
			row:         withProcessing("uploaded"),
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotReady,
			why:         "bytes landed but derivatives do not exist yet",
		},
		{
			name:        "processing is refused",
			row:         withProcessing("processing"),
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotReady,
			why:         "in flight is not finished",
		},
		{
			name:        "failed is refused",
			row:         withProcessing("failed"),
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotReady,
			why:         "processing gave up; this asset will never be publishable",
		},
		{
			name:        "an empty processing status is refused",
			row:         withProcessing(""),
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotReady,
			why: "empty was previously accepted as legacy synchronous media, which " +
				"turned the gate into a denylist that admitted every pre-ready state",
		},

		// MODERATION. CHECK-constrained to (pending, passed, rejected) and
		// DEFAULTING to `pending`, so `pending` is the state of every asset
		// between upload and the safety verdict — the exact population the old
		// not-rejected check let through.
		{
			name:        "pending moderation is refused",
			row:         withModeration("pending"),
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotReady,
			why:         "publishing on pending publishes content review has not looked at",
		},
		{
			name:        "rejected moderation is refused",
			row:         withModeration("rejected"),
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotReady,
			why:         "publishing exactly what review already refused",
		},
		{
			name:        "an empty moderation status is refused",
			row:         withModeration(""),
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaNotReady,
			why:         "unset is not a verdict",
		},

		// TYPE. The full matrix lives in TestCheckMediaCompatibility; this
		// proves the authority gate actually consults it, which it did not —
		// `Kind` was read and then never used.
		{
			name: "a video cannot back an image post",
			row: postgres.MediaOwnership{
				UploaderID:       author,
				Kind:             "video",
				ProcessingStatus: "ready",
				ModerationStatus: "passed",
			},
			found:       true,
			contentType: "post",
			postType:    "image",
			wantErr:     ErrMediaTypeMismatch,
			why:         "the post's own reader would resolve the wrong renderer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMediaAuthority(
				mediaID, author, tt.row, tt.found, tt.contentType, tt.postType,
			)

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("expected the asset to publish, got %v (%s)", err, tt.why)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v (%s)", tt.wantErr, err, tt.why)
			}
		})
	}
}

// A refusal about someone else's asset must reveal nothing about it.
//
// Not a style preference. If the message named the uploader, the kind, or even
// confirmed the row exists, then probing ids would be a way to enumerate what
// other people have uploaded — and the id is the only thing an attacker needs
// to start.
func TestForeignMediaRefusalLeaksNothing(t *testing.T) {
	author := uuid.New()
	stranger := uuid.New()
	mediaID := uuid.New()

	err := checkMediaAuthority(
		mediaID,
		author,
		postgres.MediaOwnership{
			UploaderID:       stranger,
			Kind:             "video",
			ProcessingStatus: "failed",
			ModerationStatus: "rejected",
		},
		true,
		"post",
		"image",
	)

	if !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("expected ErrMediaNotOwned, got %v", err)
	}

	message := err.Error()
	for _, leak := range []string{
		stranger.String(),
		mediaID.String(),
		"video",
		"failed",
		"rejected",
	} {
		if strings.Contains(message, leak) {
			t.Fatalf("refusal leaked %q: %q", leak, message)
		}
	}
}

// Ownership is checked BEFORE readiness, so a foreign asset never produces a
// readiness message.
//
// Order is the whole point: "not ready" about someone else's media confirms the
// asset exists and tells the caller what state it is in.
func TestOwnershipIsCheckedBeforeReadiness(t *testing.T) {
	author := uuid.New()

	err := checkMediaAuthority(
		uuid.New(),
		author,
		postgres.MediaOwnership{
			UploaderID:       uuid.New(),
			Kind:             "image",
			ProcessingStatus: "processing",
			ModerationStatus: "pending",
		},
		true,
		"post",
		"image",
	)

	if !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("expected the ownership refusal to win, got %v", err)
	}
	if errors.Is(err, ErrMediaNotReady) {
		t.Fatal("a foreign asset must not produce a readiness verdict")
	}
}
