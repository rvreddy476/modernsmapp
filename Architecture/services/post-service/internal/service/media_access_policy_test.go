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
		name string
		post postgres.Post
		rel  ViewerRelationship
		want bool
	}{
		{"public", base, ViewerRelationship{}, true},
		{"unlisted PostTube direct watch", withVisibility(base, "unlisted"), ViewerRelationship{}, true},
		{"pending safety", withReview(base, "pending"), ViewerRelationship{}, false},
		{"rejected safety", withReview(base, "rejected"), ViewerRelationship{}, false},
		{"blocked", base, ViewerRelationship{Blocked: true}, false},
		{"blocked reverse", base, ViewerRelationship{BlockedBy: true}, false},
		{"muted", base, ViewerRelationship{Muted: true}, false},
		{"followers eligible", withVisibility(base, "followers"), ViewerRelationship{Follows: true}, true},
		{"followers stranger", withVisibility(base, "followers"), ViewerRelationship{}, false},
		{"close friends eligible", withVisibility(base, "close_friends"), ViewerRelationship{ViewerIsCloseFriendOfTarget: true}, true},
		{"private", withVisibility(base, "private"), ViewerRelationship{Follows: true}, false},
		{"unknown", withVisibility(base, "future_scope"), ViewerRelationship{Follows: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluatePostMediaVisibility(viewer, &tt.post, tt.rel); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	owner := base
	owner.AuthorID = viewer
	owner.ReviewStatus = "pending"
	owner.Visibility = "private"
	if !evaluatePostMediaVisibility(viewer, &owner, ViewerRelationship{BlockedBy: true}) {
		t.Fatal("owner preview was denied")
	}
}

func withVisibility(p postgres.Post, visibility string) postgres.Post {
	p.Visibility = visibility
	return p
}
func withReview(p postgres.Post, review string) postgres.Post { p.ReviewStatus = review; return p }
