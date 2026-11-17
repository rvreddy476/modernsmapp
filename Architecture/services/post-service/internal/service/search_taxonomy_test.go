package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// 2026-11-17: post-service's own trending counter counted every post's
// hashtags at birth, hidden ones included; it now counts only a post search
// may show. The same rule decides whether PostCreated carries the taxonomy.

func openPost() *postgres.Post {
	return &postgres.Post{
		Visibility: "public", ReviewStatus: "approved",
		Tags: []string{"tag"}, Hashtags: []string{"momentum"}, Category: "music", Language: "te",
	}
}

func TestTrendingCountsOnlyPubliclySearchablePosts(t *testing.T) {
	if got := publicHashtags(openPost()); len(got) != 1 {
		t.Fatalf("public approved post hashtags = %v, want counted", got)
	}
	now, tier := time.Now(), uuid.New()
	for name, mut := range map[string]func(*postgres.Post){
		"private":   func(p *postgres.Post) { p.Visibility = "private" },
		"unlisted":  func(p *postgres.Post) { p.Visibility = "unlisted" },
		"pending":   func(p *postgres.Post) { p.ReviewStatus = "pending" },
		"held":      func(p *postgres.Post) { p.ActiveRestrictionCount = 1 },
		"deleted":   func(p *postgres.Post) { p.DeletedAt = &now },
		"scheduled": func(p *postgres.Post) { p.PublishAt = &now },
		"gated":     func(p *postgres.Post) { p.TierRequiredID = &tier },
	} {
		p := openPost()
		mut(p)
		if got := publicHashtags(p); got != nil {
			t.Errorf("%s post hashtags counted: %v", name, got)
		}
	}
	if publiclySearchable(nil) {
		t.Error("nil post is searchable")
	}
}

// The comment path applies the same rule to the parent post.
func TestCommentHashtagsGatedOnTheParentPost(t *testing.T) {
	src, err := os.ReadFile("post_comments.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "len(commentTags) > 0 && publiclySearchable(post) {") {
		t.Fatal("comment hashtags feed trending without the parent post's visibility check")
	}
}

func TestPostCreatedCarriesTaxonomyOnlyWhenSearchable(t *testing.T) {
	svc := &Service{}
	pc := svc.buildPostCreatedPayload(context.Background(), openPost(), nil, 0, 0, 1)
	if len(pc.Tags) != 1 || len(pc.Hashtags) != 1 || pc.Category != "music" || pc.Language != "te" {
		t.Fatalf("searchable post taxonomy = %+v", pc.PostSearchTaxonomy)
	}
	hidden := openPost()
	hidden.ReviewStatus = "pending"
	if pc := svc.buildPostCreatedPayload(context.Background(), hidden, nil, 0, 0, 1); pc.Category != "" || pc.Tags != nil {
		t.Fatalf("pending post taxonomy = %+v, want none", pc.PostSearchTaxonomy)
	}
	tier := uuid.New()
	gated := openPost()
	gated.TierRequiredID = &tier
	if pc := svc.buildPostCreatedPayload(context.Background(), gated, nil, 0, 0, 1); pc.Hashtags != nil {
		t.Fatalf("gated post taxonomy = %+v, want none", pc.PostSearchTaxonomy)
	}
}
