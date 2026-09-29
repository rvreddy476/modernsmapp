package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// The cover decision on the write paths, as a table (cover_guard.go). The
// routes themselves are proven against Postgres in
// cover_guard_integration_test.go; this pins every branch of the rule.
func TestCheckCoverAuthority(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	cover := uuid.New()
	row := func(uploader uuid.UUID, kind, processing, moderation string) postgres.MediaOwnership {
		return postgres.MediaOwnership{UploaderID: uploader, Kind: kind, ProcessingStatus: processing, ModerationStatus: moderation}
	}
	cases := []struct {
		name  string
		m     postgres.MediaOwnership
		found bool
		want  error
	}{
		{"own image", row(author, mediaKindImage, "ready", "passed"), true, nil},
		{"own image, still processing, not yet scanned", row(author, mediaKindImage, "processing", "pending"), true, nil},

		{"a stranger's image", row(stranger, mediaKindImage, "ready", "passed"), true, ErrMediaNotOwned},
		{"a stranger's video", row(stranger, mediaKindVideo, "ready", "passed"), true, ErrMediaNotOwned},
		{"no such asset", postgres.MediaOwnership{}, false, ErrMediaNotFound},
		{"own video", row(author, mediaKindVideo, "ready", "passed"), true, ErrMediaTypeMismatch},
		{"own audio", row(author, "audio", "ready", "passed"), true, ErrMediaTypeMismatch},
		{"own image, never confirmed", row(author, mediaKindImage, "pending_upload", "pending"), true, ErrMediaNotReady},
		{"own image, refused by review", row(author, mediaKindImage, "ready", "rejected"), true, ErrMediaNotReady},
		{"own video, refused by review", row(author, mediaKindVideo, "ready", "rejected"), true, ErrMediaNotReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCoverAuthority(cover, author, tc.m, tc.found)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

// A refused cover keeps its sentinel through the wrapper and maps to the
// statuses PATCH /v1/posts/:id answers with.
func TestCoverMediaErrorKeepsThePatchStatuses(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrMediaNotOwned, 403, "MEDIA_NOT_OWNED"},
		{ErrMediaNotFound, 422, "MEDIA_NOT_FOUND"},
		{ErrMediaNotReady, 422, "MEDIA_NOT_READY"},
		{ErrMediaTypeMismatch, 422, "MEDIA_TYPE_MISMATCH"},
	} {
		wrapped := error(&CoverMediaError{Err: tc.err})
		if !errors.Is(wrapped, tc.err) {
			t.Fatalf("%v is lost inside CoverMediaError", tc.err)
		}
		status, code := PostEditErrorStatus(wrapped)
		wantStatus, wantCode := PostEditErrorStatus(tc.err)
		if status != tc.status || code != tc.code || status != wantStatus || code != wantCode {
			t.Fatalf("%v: got %d %s, want %d %s", tc.err, status, code, tc.status, tc.code)
		}
	}
}
