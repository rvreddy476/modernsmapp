package main

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	postID  = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	authorA = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	authorB = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	created = time.Date(2026, 8, 22, 4, 9, 6, 0, time.UTC)
)

// base is a plain, healthy, unprojected post by a known author.
func base() postRow {
	return postRow{
		ID:           postID,
		AuthorID:     authorA,
		ContentType:  "post",
		CreatedAt:    created,
		Visibility:   "public",
		ReviewStatus: "approved",
		AuthorKnown:  true,
	}
}

func TestQualify(t *testing.T) {
	defaults := policy{}

	tests := []struct {
		name string
		row  func(postRow) postRow
		pol  policy
		want verdict
		typ  string
	}{
		{
			name: "unprojected post by a known author is projected",
			row:  func(p postRow) postRow { return p },
			pol:  defaults,
			want: verdictProject, typ: "post",
		},
		{
			// Visibility is not a filter: it is mutable, and an ownership
			// row grants no access.
			name: "private post still qualifies",
			row:  func(p postRow) postRow { p.Visibility = "private"; return p },
			pol:  defaults,
			want: verdictProject, typ: "post",
		},
		{
			name: "close_friends post still qualifies",
			row:  func(p postRow) postRow { p.Visibility = "close_friends"; return p },
			pol:  defaults,
			want: verdictProject, typ: "post",
		},
		{
			name: "rejected post still qualifies",
			row:  func(p postRow) postRow { p.ReviewStatus = "rejected"; return p },
			pol:  defaults,
			want: verdictProject, typ: "post",
		},
		{
			name: "scheduled post still qualifies",
			row: func(p postRow) postRow {
				p.PublishAt = created.Add(30 * 24 * time.Hour)
				return p
			},
			pol:  defaults,
			want: verdictProject, typ: "post",
		},
		{
			name: "soft-deleted post qualifies by default",
			row:  func(p postRow) postRow { p.Deleted = true; return p },
			pol:  defaults,
			want: verdictProject, typ: "post",
		},
		{
			name: "soft-deleted post is skipped under -skip-deleted",
			row:  func(p postRow) postRow { p.Deleted = true; return p },
			pol:  policy{skipDeleted: true},
			want: verdictSkipDeleted, typ: "post",
		},
		{
			// The identityrolebackfill trap, in this table: creator_id has
			// no foreign key, so nothing else would stop it.
			name: "unknown author is skipped by default",
			row:  func(p postRow) postRow { p.AuthorKnown = false; return p },
			pol:  defaults,
			want: verdictSkipUnknownAuthor, typ: "post",
		},
		{
			name: "unknown author is projected under -include-unknown-authors",
			row:  func(p postRow) postRow { p.AuthorKnown = false; return p },
			pol:  policy{includeUnknownAuthors: true},
			want: verdictProject, typ: "post",
		},
		{
			name: "identical existing row is a no-op",
			row: func(p postRow) postRow {
				p.HasOwnership = true
				p.OwnerCreatorID = authorA
				p.OwnerContentType = "post"
				return p
			},
			pol:  defaults,
			want: verdictAlreadyProjected, typ: "post",
		},
		{
			name: "same creator with a stale content_type is corrected",
			row: func(p postRow) postRow {
				p.ContentType = "flick"
				p.HasOwnership = true
				p.OwnerCreatorID = authorA
				p.OwnerContentType = "long_video"
				return p
			},
			pol:  defaults,
			want: verdictRetype, typ: "flick",
		},
		{
			name: "a different creator already owning it is a conflict",
			row: func(p postRow) postRow {
				p.HasOwnership = true
				p.OwnerCreatorID = authorB
				p.OwnerContentType = "post"
				return p
			},
			pol:  defaults,
			want: verdictConflict, typ: "post",
		},
		{
			// A conflict must never be hidden behind a skip: it is a
			// disagreement about authorship and a human has to see it.
			name: "conflict outranks the unknown-author skip",
			row: func(p postRow) postRow {
				p.AuthorKnown = false
				p.HasOwnership = true
				p.OwnerCreatorID = authorB
				return p
			},
			pol:  defaults,
			want: verdictConflict, typ: "post",
		},
		{
			name: "conflict outranks the soft-delete skip",
			row: func(p postRow) postRow {
				p.Deleted = true
				p.HasOwnership = true
				p.OwnerCreatorID = authorB
				return p
			},
			pol:  policy{skipDeleted: true},
			want: verdictConflict, typ: "post",
		},
		{
			name: "nil author id is unusable",
			row:  func(p postRow) postRow { p.AuthorID = uuid.Nil; return p },
			pol:  defaults,
			want: verdictSkipInvalid, typ: "",
		},
		{
			name: "nil content id is unusable",
			row:  func(p postRow) postRow { p.ID = uuid.Nil; return p },
			pol:  defaults,
			want: verdictSkipInvalid, typ: "",
		},
		{
			// created_at is what dates the row on
			// idx_content_ownership_creator (creator_id, created_at DESC).
			// A zero here would misdate it, so it is refused rather than
			// papered over with time.Now().
			name: "zero created_at is unusable",
			row:  func(p postRow) postRow { p.CreatedAt = time.Time{}; return p },
			pol:  defaults,
			want: verdictSkipInvalid, typ: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, typ := qualify(tc.row(base()), tc.pol)
			if got != tc.want {
				t.Fatalf("verdict = %v, want %v", got, tc.want)
			}
			if typ != tc.typ {
				t.Fatalf("content_type = %q, want %q", typ, tc.typ)
			}
		})
	}
}

// TestQualifyPreservesCreatedAt guards the one value that is silently wrong
// forever if it is wrong once: the row is dated by the post, never by the
// run.
func TestQualifyPreservesCreatedAt(t *testing.T) {
	p := base()
	if _, _ = qualify(p, policy{}); p.CreatedAt != created {
		t.Fatalf("qualify mutated CreatedAt")
	}
	line := p.describe("post")
	if want := "created_at=2026-08-22T04:09:06Z"; !strings.Contains(line, want) {
		t.Fatalf("described row %q does not carry the post's own creation time (%s)", line, want)
	}
}

func TestOwnershipContentType(t *testing.T) {
	tests := []struct{ in, want string }{
		// Copied verbatim: posts.content_type is post-service's
		// shared/postclassify decision, already reclassified after
		// transcode. This tool is not a second classifier.
		{"post", "post"},
		{"poll", "poll"},
		{"flick", "flick"},
		{"long_video", "long_video"},
		{"voice", "voice"},
		{"video_embed", "video_embed"},
		// Legacy kinds are NOT remapped. postclassify already treats them
		// as synonyms for the view bar, and remapping would change which
		// content the RPM rate sheet prices.
		{"reel", "reel"},
		{"video", "video"},
		// Mirrors the consumer's own fallback.
		{"", "post"},
	}
	for _, tc := range tests {
		if got := ownershipContentType(tc.in); got != tc.want {
			t.Fatalf("ownershipContentType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDetectDrift(t *testing.T) {
	tests := []struct {
		name     string
		row      postRow
		wantDrif bool
	}{
		{
			name: "no video metadata is never drift",
			row:  postRow{ContentType: "post"},
		},
		{
			// A pending transcode's final_category is a placeholder.
			name: "pending transcode is not yet measured",
			row: postRow{ContentType: "flick", VideoUploadStatus: "pending",
				VideoFinalCategory: "long_video"},
		},
		{
			// reclassifyDecision: an explicit kind is the author's choice.
			name: "explicit kind that disagrees is not drift",
			row: postRow{ContentType: "long_video", ContentTypeExplicit: true,
				VideoUploadStatus: "ready", VideoFinalCategory: "flick"},
		},
		{
			// reclassifyDecision: a flick is never downgraded.
			name: "flick is never downgraded",
			row: postRow{ContentType: "flick", VideoUploadStatus: "ready",
				VideoFinalCategory: "long_video"},
		},
		{
			name: "agreeing measurement is not drift",
			row: postRow{ContentType: "long_video", VideoUploadStatus: "ready",
				VideoFinalCategory: "long_video"},
		},
		{
			// The one post-service WOULD have flipped: a plain post that
			// defaulted to long_video while transcode was pending.
			name: "unflipped default long_video measuring as flick is drift",
			row: postRow{ContentType: "long_video", VideoUploadStatus: "ready",
				VideoFinalCategory: "flick"},
			wantDrif: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectDrift(tc.row); got != tc.wantDrif {
				t.Fatalf("detectDrift = %v, want %v", got, tc.wantDrif)
			}
		})
	}
}

func TestIsScheduled(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	if (postRow{}).IsScheduled(now) {
		t.Fatal("a post with no publish_at is not scheduled")
	}
	if (postRow{PublishAt: now.Add(-time.Hour)}).IsScheduled(now) {
		t.Fatal("a publish_at in the past is not scheduled")
	}
	if !(postRow{PublishAt: now.Add(time.Hour)}).IsScheduled(now) {
		t.Fatal("a publish_at in the future is scheduled")
	}
}
