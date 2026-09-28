package service

import (
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestPostMediaVisibilityMatrix(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	base := postgres.Post{AuthorID: author, Visibility: "public", ReviewStatus: "approved"}
	tests := []struct {
		name   string
		post   postgres.Post
		rel    ViewerRelationship
		shared bool
		want   bool
	}{
		{"public", base, ViewerRelationship{}, false, true},
		{"unlisted PostTube direct watch", withVisibility(base, "unlisted"), ViewerRelationship{}, false, true},
		{"pending safety", withReview(base, "pending"), ViewerRelationship{}, false, false},
		{"rejected safety", withReview(base, "rejected"), ViewerRelationship{}, false, false},
		{"blocked", base, ViewerRelationship{Blocked: true}, false, false},
		{"blocked reverse", base, ViewerRelationship{BlockedBy: true}, false, false},
		{"muted", base, ViewerRelationship{Muted: true}, false, false},
		{"followers eligible", withVisibility(base, "followers"), ViewerRelationship{Follows: true}, false, true},
		{"followers stranger", withVisibility(base, "followers"), ViewerRelationship{}, false, false},
		// The close-friends audience was retired on 21 Sep: no relationship
		// satisfies it any more, so such a row is author-only.
		{"close friends retired, even a follower is denied", withVisibility(base, "close_friends"), ViewerRelationship{Follows: true}, false, false},
		{"private", withVisibility(base, "private"), ViewerRelationship{Follows: true}, false, false},
		// Private sharing (2026-09-28): the share list opens a private post,
		// after the block / mute checks and the safety verdict.
		{"private, viewer on the share list", withVisibility(base, "private"), ViewerRelationship{}, true, true},
		{"private shared but blocked", withVisibility(base, "private"), ViewerRelationship{BlockedBy: true}, true, false},
		{"private shared but pending safety", withReview(withVisibility(base, "private"), "pending"), ViewerRelationship{}, true, false},
		{"shared flag never widens followers", withVisibility(base, "followers"), ViewerRelationship{}, true, false},
		{"unknown", withVisibility(base, "future_scope"), ViewerRelationship{Follows: true}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluatePostMediaVisibility(viewer, &tt.post, tt.rel, tt.shared); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	owner := base
	owner.AuthorID = viewer
	owner.ReviewStatus = "pending"
	owner.Visibility = "private"
	if !evaluatePostMediaVisibility(viewer, &owner, ViewerRelationship{BlockedBy: true}, false) {
		t.Fatal("owner preview was denied")
	}
}

func withVisibility(p postgres.Post, visibility string) postgres.Post {
	p.Visibility = visibility
	return p
}
func withReview(p postgres.Post, review string) postgres.Post { p.ReviewStatus = review; return p }
