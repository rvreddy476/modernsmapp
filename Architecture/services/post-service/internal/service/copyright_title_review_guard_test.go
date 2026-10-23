package service

import "testing"

// The copyright title review's wiring into the two publication moments a
// long video can go live at (CreatePost, the scheduled flip), derived from
// the source like the standing guard (publish_standing_guard_test.go). The
// draft paths publish through CreatePost and the "publish now" / video
// publish routes through PublishScheduled, so these two cover all of them.
// The behaviour itself runs on Postgres in
// copyright_title_review_service_integration_test.go.
func TestTitleReviewIsWiredIntoEveryLongVideoPublication(t *testing.T) {
	funcs := serviceFuncs(parsePackage(t, "."))
	for fn, want := range map[string][]string{
		// The pause, the hold decision (members-only refusal included) and
		// the PostCreated it withholds.
		"CreatePost": {"requireLongVideoUploads", "applyCreateTitleHold", "createdEventFor"},
		// The pause and the hold decision at flip time, and the store write
		// that places the hold in the flip's own transaction.
		"PublishScheduled":   {"scheduledTitleHold", "pgStore.PublishScheduledPostWithHold"},
		"scheduledTitleHold": {"requireLongVideoUploads", "decideTitleHold"},
		// The duration rule and the reviewer's clear both announce through
		// the one builder, which stamps eligible_from.
		"ResolveTitleReviewMeasurement": {"titleReviewAnnounce"},
		"DecideCopyrightReview":         {"titleReviewAnnounce", "afterTitleReviewRelease"},
	} {
		f, ok := funcs[fn]
		if !ok {
			t.Errorf("%s not found (renamed? update this guard)", fn)
			continue
		}
		calls := calledNames(f.decl.Body)
		for _, w := range want {
			if !calls[w] {
				t.Errorf("%s (%s) no longer calls %s", fn, f.file, w)
			}
		}
	}
	if calls := calledNames(funcs["PublishScheduled"].decl.Body); calls["pgStore.PublishScheduledPost"] {
		t.Error("PublishScheduled flips through PublishScheduledPost, which cannot hold the post")
	}
}
