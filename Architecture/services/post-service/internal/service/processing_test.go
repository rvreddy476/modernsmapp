package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Instant publish (processing.go). The create gate accepts any CONFIRMED
// asset; the exact ready+passed rule moved to the read side, where a
// processing post is the author's alone. These pin both halves without a
// database.

func TestMediaConfirmed(t *testing.T) {
	for status, want := range map[string]bool{
		"uploaded":       true,
		"processing":     true,
		"ready":          true,
		"pending_upload": false, // bytes never arrived
		"failed":         false, // processing gave up
		"rejected":       false, // confirm refused the file
		"":               false, // allowlist, not denylist
		"done":           false, // unknown is not confirmed
	} {
		if got := mediaConfirmed(status); got != want {
			t.Errorf("mediaConfirmed(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestMediaPublishableIsExactReadyAndPassed(t *testing.T) {
	cases := []struct {
		proc, mod string
		want      bool
	}{
		{"ready", "passed", true},
		{"ready", "pending", false},
		{"ready", "manual_review", false},
		{"ready", "rejected", false},
		{"processing", "passed", false},
		{"uploaded", "passed", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := mediaPublishable(c.proc, c.mod); got != c.want {
			t.Errorf("mediaPublishable(%q,%q) = %v, want %v", c.proc, c.mod, got, c.want)
		}
	}
}

func TestApplyMediaStateDerivesIsProcessing(t *testing.T) {
	ready := uuid.New()
	transcoding := uuid.New()
	state := map[uuid.UUID]postgres.MediaOwnership{
		ready:       {ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 5070},
		transcoding: {ProcessingStatus: "processing", ModerationStatus: "pending"},
	}

	t.Run("all media ready+passed is not processing", func(t *testing.T) {
		p := &postgres.Post{Media: []postgres.PostMedia{{MediaID: ready}}}
		applyMediaState(p, state)
		if p.IsProcessing {
			t.Fatal("ready+passed media must not mark the post processing")
		}
		if p.Media[0].ProcessingStatus != "ready" || p.Media[0].ModerationStatus != "passed" {
			t.Fatalf("per-media state not overlaid: %+v", p.Media[0])
		}
		if p.Media[0].DurationMs != 5070 {
			t.Fatalf("duration_ms not overlaid from media_assets: %+v", p.Media[0])
		}
	})

	t.Run("one processing asset marks the whole post", func(t *testing.T) {
		p := &postgres.Post{Media: []postgres.PostMedia{{MediaID: ready}, {MediaID: transcoding}}}
		applyMediaState(p, state)
		if !p.IsProcessing {
			t.Fatal("a post with any unready asset is processing")
		}
		if p.Media[1].ProcessingStatus != "processing" || p.Media[1].ModerationStatus != "pending" {
			t.Fatalf("per-media state not overlaid: %+v", p.Media[1])
		}
	})

	t.Run("a missing media row fails closed", func(t *testing.T) {
		p := &postgres.Post{Media: []postgres.PostMedia{{MediaID: uuid.New()}}}
		applyMediaState(p, state)
		if !p.IsProcessing {
			t.Fatal("an absent media_assets row must hide the post, not publish it")
		}
	})

	t.Run("no media is never processing", func(t *testing.T) {
		p := &postgres.Post{}
		applyMediaState(p, state)
		if p.IsProcessing {
			t.Fatal("a text post cannot be processing")
		}
	})

	t.Run("a stale cached flag is overwritten", func(t *testing.T) {
		p := &postgres.Post{IsProcessing: true, Media: []postgres.PostMedia{{MediaID: ready, ProcessingStatus: "processing"}}}
		applyMediaState(p, state)
		if p.IsProcessing || p.Media[0].ProcessingStatus != "ready" {
			t.Fatal("the live row must win over whatever the post-body cache carried")
		}
	})
}

// The read gate: GetPost answers nil (404) and the batch drops the row for
// everyone but the author. Both funnel through this predicate.
func TestHiddenWhileProcessingIsAuthorOnly(t *testing.T) {
	author := uuid.New()
	stranger := uuid.New()
	processing := &postgres.Post{AuthorID: author, IsProcessing: true}
	published := &postgres.Post{AuthorID: author, IsProcessing: false}

	if hiddenWhileProcessing(processing, &author) {
		t.Fatal("the author must see their own processing post")
	}
	if !hiddenWhileProcessing(processing, &stranger) {
		t.Fatal("another viewer must get 404 / be dropped while the post processes")
	}
	if !hiddenWhileProcessing(processing, nil) {
		t.Fatal("an anonymous viewer is never the author")
	}
	if hiddenWhileProcessing(published, &stranger) || hiddenWhileProcessing(published, nil) {
		t.Fatal("a published post is visible to everyone (subject to the other gates)")
	}
}
