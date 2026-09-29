package service

import (
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The per-post playback rule for a SIGNED-IN viewer (evaluatePostMediaVisibility).
// Every row breaks exactly one thing against a public, approved, live post
// by a visible account, so a failure names the rule.
func TestPostMediaVisibilityMatrix(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	base := postgres.Post{AuthorID: author, Visibility: "public", ReviewStatus: "approved"}
	open := postMediaInputs{authorVisible: true}
	with := func(rel ViewerRelationship, shared bool) postMediaInputs {
		return postMediaInputs{rel: rel, shared: shared, authorVisible: true}
	}
	tests := []struct {
		name string
		post postgres.Post
		in   postMediaInputs
		want bool
	}{
		{"public", base, open, true},
		{"unlisted PostTube direct watch", withVisibility(base, "unlisted"), open, true},
		{"pending safety", withReview(base, "pending"), open, false},
		{"rejected safety", withReview(base, "rejected"), open, false},
		{"blocked", base, with(ViewerRelationship{Blocked: true}, false), false},
		{"blocked reverse", base, with(ViewerRelationship{BlockedBy: true}, false), false},
		{"muted", base, with(ViewerRelationship{Muted: true}, false), false},
		{"followers eligible", withVisibility(base, "followers"), with(ViewerRelationship{Follows: true}, false), true},
		{"followers stranger", withVisibility(base, "followers"), open, false},
		// The close-friends audience was retired on 21 Sep: no relationship
		// satisfies it any more, so such a row is author-only — and since
		// 2026-09-29 the detail gate agrees (TestDetailGateCircleIsAuthorOnly).
		{"close friends retired, even a follower is denied", withVisibility(base, "close_friends"), with(ViewerRelationship{Follows: true}, false), false},
		{"circle retired, even a follower is denied", withVisibility(base, "circle"), with(ViewerRelationship{Follows: true}, false), false},
		{"staged is author-only", withVisibility(base, "staged"), with(ViewerRelationship{Follows: true}, true), false},
		{"private", withVisibility(base, "private"), with(ViewerRelationship{Follows: true}, false), false},
		// Private sharing (2026-09-28): the share list opens a private post,
		// after the block / mute checks and the safety verdict.
		{"private, viewer on the share list", withVisibility(base, "private"), with(ViewerRelationship{}, true), true},
		{"private shared but blocked", withVisibility(base, "private"), with(ViewerRelationship{BlockedBy: true}, true), false},
		{"private shared but pending safety", withReview(withVisibility(base, "private"), "pending"), with(ViewerRelationship{}, true), false},
		{"shared flag never widens followers", withVisibility(base, "followers"), with(ViewerRelationship{}, true), false},
		{"unknown", withVisibility(base, "future_scope"), with(ViewerRelationship{Follows: true}, true), false},
		// P-8 (a), 2026-09-29: the account gate and the schedule bind the
		// byte gate as they bind the detail.
		{"private account not followed / hidden author", base, postMediaInputs{}, false},
		{"scheduled (publish_at set) is author-only", withPublishAt(base, time.Now().Add(time.Hour)), open, false},
		{"scheduled in the past but not yet published is still author-only", withPublishAt(base, time.Now().Add(-time.Hour)), open, false},
		{"soft-deleted row never plays", withDeleted(base), open, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluatePostMediaVisibility(viewer, &tt.post, tt.in); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	owner := base
	owner.AuthorID = viewer
	owner.ReviewStatus = "pending"
	owner.Visibility = "private"
	owner.PublishAt = dayp(2030, 1, 1)
	if !evaluatePostMediaVisibility(viewer, &owner, postMediaInputs{rel: ViewerRelationship{BlockedBy: true}}) {
		t.Fatal("owner preview was denied")
	}
	if evaluatePostMediaVisibility(viewer, nil, open) {
		t.Fatal("a missing post played")
	}
}

// The SIGNED-OUT rule (anonymousMayAccessPost, founder decision 2): exactly
// public, approved, live, unscheduled, not 18+, not members-only, by a
// public and unhidden account. Each case flips one clause of an allowed post.
func TestAnonymousPostMediaRule(t *testing.T) {
	author := uuid.New()
	tier := uuid.New()
	base := postgres.Post{AuthorID: author, Visibility: "public", ReviewStatus: "approved"}
	tests := []struct {
		name          string
		post          postgres.Post
		authorVisible bool
		want          bool
	}{
		{"public approved live post by a public account", base, true, true},
		{"case and whitespace do not matter", postgres.Post{AuthorID: author, Visibility: " Public ", ReviewStatus: "APPROVED"}, true, true},
		{"unlisted is link-only, never anonymous", withVisibility(base, "unlisted"), true, false},
		{"blank visibility is not a public one", withVisibility(base, ""), true, false},
		{"followers", withVisibility(base, "followers"), true, false},
		{"private", withVisibility(base, "private"), true, false},
		{"staged", withVisibility(base, "staged"), true, false},
		{"circle", withVisibility(base, "circle"), true, false},
		{"pending review", withReview(base, "pending"), true, false},
		{"flagged", withReview(base, "flagged"), true, false},
		{"rejected", withReview(base, "rejected"), true, false},
		{"blank review status", withReview(base, ""), true, false},
		{"soft-deleted", withDeleted(base), true, false},
		{"scheduled", withPublishAt(base, time.Now().Add(time.Hour)), true, false},
		{"age-restricted needs a date of birth a stranger has not got", withAge(base), true, false},
		{"members-only", withTier(base, tier), true, false},
		{"private account, hidden (deactivated / pending deletion) author", base, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := anonymousMayAccessPost(&tt.post, tt.authorVisible); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	if anonymousMayAccessPost(nil, true) {
		t.Fatal("a missing post played anonymously")
	}
}

func withVisibility(p postgres.Post, visibility string) postgres.Post {
	p.Visibility = visibility
	return p
}
func withReview(p postgres.Post, review string) postgres.Post { p.ReviewStatus = review; return p }
func withPublishAt(p postgres.Post, at time.Time) postgres.Post {
	p.PublishAt = &at
	p.IsScheduled = true
	return p
}
func withDeleted(p postgres.Post) postgres.Post {
	now := time.Now()
	p.DeletedAt = &now
	return p
}
func withAge(p postgres.Post) postgres.Post { p.AgeRestricted = true; return p }
func withTier(p postgres.Post, tier uuid.UUID) postgres.Post {
	p.TierRequiredID = &tier
	return p
}
