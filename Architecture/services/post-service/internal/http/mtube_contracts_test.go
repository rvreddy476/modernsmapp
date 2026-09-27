package http

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Golden JSON contracts for the MTube web client (2026-09-27).
//
// One fixture per new or widened response under testdata/contracts/mtube/.
// Each is the `data` member of the api.JSON envelope ({"data": ..., "meta":
// {...}}), marshalled from the very structs the handlers return with fixed
// ids and times, so the file IS the wire shape: a struct change that does
// not update the fixture fails here. The web copies these verbatim.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestMTubeContracts
//
// regenerates them; review the diff before committing.

var (
	fxTime     = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	fxPost     = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	fxAuthor   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	fxViewer   = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	fxMedia    = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	fxCover    = uuid.MustParse("55555555-5555-4555-8555-555555555555")
	fxComment  = uuid.MustParse("66666666-6666-4666-8666-666666666666")
	fxReply    = uuid.MustParse("77777777-7777-4777-8777-777777777777")
	fxPlaylist = uuid.MustParse("88888888-8888-4888-8888-888888888888")
	fxBanner   = uuid.MustParse("99999999-9999-4999-8999-999999999999")
	fxStream   = uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
)

func fixturePost() *postgres.Post {
	publishedAt := fxTime.Add(-time.Hour)
	return &postgres.Post{
		ID: fxPost, AuthorID: fxAuthor, Text: "0:00 Intro\n1:23 Setup\n12:05 The build", Visibility: "public",
		ContentType: "long_video", PostType: "video", AppOrigin: "tube", ShareToPostbook: false,
		ReviewStatus: "approved", Title: "Friday build", Tags: []string{"go", "kafka"}, Category: "science-tech",
		Language: "en", AllowEmbedding: true, PublishToFeed: true, CoverMediaID: &fxCover, AllowDownload: true,
		ContentTypeExplicit: true, Hashtags: []string{"build"}, CreatedAt: fxTime.Add(-2 * time.Hour), UpdatedAt: fxTime,
		PublishedAt: &publishedAt,
		Media: []postgres.PostMedia{{MediaID: fxMedia, Kind: "video", Position: 0, ProcessingStatus: "ready", ModerationStatus: "passed",
			DurationMs: 725000, HLSURL: "/v1/media/" + fxMedia.String() + "/hls/master.m3u8"}},
	}
}

func fixtureComment() postgres.Comment {
	heart := "❤️"
	return postgres.Comment{
		ID: fxComment, PostID: fxPost, AuthorID: fxViewer, Body: "Great walkthrough!", LikeCount: 2, ReplyCount: 1,
		CreatedAt: fxTime.Add(-30 * time.Minute), UpdatedAt: fxTime.Add(-30 * time.Minute),
		Reactions: []postgres.CommentReaction{{Emoji: "❤️", Count: 2}}, ReactionCount: 2, ViewerReaction: &heart,
		Pinned: true, HeartedByAuthor: true,
		Reply: &postgres.Comment{ID: fxReply, PostID: fxPost, AuthorID: fxAuthor, ParentID: &fxComment, Body: "Thanks!", IsReply: true,
			CreatedAt: fxTime.Add(-20 * time.Minute), UpdatedAt: fxTime.Add(-20 * time.Minute), Reactions: []postgres.CommentReaction{}},
	}
}

func mtubeContracts() map[string]any {
	post := fixturePost()
	detail := service.PostDetail{
		Post: post, Counts: &scylla.Counts{Likes: 12, Comments: 3}, ViewCount: 480, IsRepostable: true,
		Channel:        &service.ChannelRef{UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds"},
		ViewerDisliked: false, ViewerQueued: true,
		Chapters: []service.ChapterRef{{StartMs: 0, Title: "Intro"}, {StartMs: 83000, Title: "Setup"}, {StartMs: 725000, Title: "The build"}},
	}
	scheduledAt := fxTime.Add(24 * time.Hour)
	scheduled := fixturePost()
	scheduled.PublishAt, scheduled.PublishedAt, scheduled.IsScheduled, scheduled.Visibility = &scheduledAt, nil, true, "private"
	upload := service.UploadDetail{
		PostDetail:    service.PostDetail{Post: scheduled, Counts: &scylla.Counts{Likes: 0, Comments: 0}, ViewCount: 0},
		VideoMetadata: &postgres.VideoMetadata{PostID: fxPost, DurationSeconds: 725, Orientation: "landscape", ComputedCategory: "long_video", FinalCategory: "long_video", UploadStatus: "ready", MediaAssetID: &fxMedia, CreatedAt: fxTime, UpdatedAt: fxTime},
		ScheduledAt:   &scheduledAt, CommentCount: 0, ProcessingStatus: "ready", Flags: []string{"scheduled"},
	}
	reel := fixturePost()
	reel.ContentType, reel.PostType, reel.Text, reel.Title = "flick", "video", "quick one #build", "Quick one"
	bannerURL := "/v1/media/" + fxBanner.String() + "/original"
	subscribed := true
	notify := "all"
	return map[string]any{
		"categories.json":  service.VideoCategories(),
		"post_detail.json": detail,
		"post_edit_request.json": updatePostRequest{Title: strp("Friday build (final)"), Text: strp("0:00 Intro\n1:23 Setup\n12:05 The build"),
			Tags: &[]string{"go", "kafka"}, Hashtags: &[]string{"build"}, Category: strp("science-tech"), Visibility: strp("public"),
			CoverMediaID: strp(fxCover.String()), AllowDownload: boolp(true), NoComments: boolp(false), MadeForKids: boolp(false), Language: strp("en")},
		"reel_feed_item.json":     service.ReelFeedItem{Post: reel, ViewerReaction: "like", IsSaved: false, ViewerDisliked: false},
		"playlist.json":           postgres.Playlist{ID: fxPlaylist, CreatorID: fxAuthor, Title: "Build logs", Description: "Every Friday", Visibility: "public", ItemCount: 4, CreatedAt: fxTime, UpdatedAt: fxTime, Kind: "user"},
		"system_playlist.json":    postgres.Playlist{ID: fxPlaylist, CreatorID: fxViewer, Title: "Queue", Visibility: "private", ItemCount: 1, CreatedAt: fxTime, UpdatedAt: fxTime, Kind: "watch_later"},
		"watch_later.json":        service.WatchLaterResult{Queued: true},
		"comment.json":            fixtureComment(),
		"comments_inbox_row.json": postgres.InboxRow{Comment: fixtureComment(), Post: postgres.InboxPostRef{ID: fxPost, Title: "Friday build", ContentType: "long_video", CoverMediaID: &fxCover}, AuthorReplied: true},
		"comment_heart.json":      service.CommentHeartResult{CommentID: fxComment, HeartedByAuthor: true},
		"comment_pin.json":        service.CommentPinResult{CommentID: fxComment, PostID: fxPost, Pinned: true},
		"upload_row.json":         upload,
		"uploads_bulk_request.json": bulkUploadsRequest{PostIDs: []string{fxPost.String()}, Patch: struct {
			Visibility string `json:"visibility"`
		}{Visibility: "private"}},
		"uploads_bulk.json":    bulkUploadsResponse{Results: []postgres.BulkVisibilityOutcome{{ID: fxPost, OK: true}, {ID: fxStream, OK: false, Error: "NOT_FOUND"}}},
		"creator_summary.json": service.CreatorSummary{Videos: 12, Shorts: 30, Live: 2, Collections: 3, Followers: 1200},
		"channel.json": service.ChannelView{UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds", About: "Weekly builds", AvatarMediaID: nil, AvatarURL: nil,
			VideoCount: 12, SubscriberCount: 1200, IsSubscribed: &subscribed, NotifyOn: &notify, CreatedAt: fxTime.Add(-72 * time.Hour), UpdatedAt: fxTime,
			BannerMediaID: &fxBanner, BannerURL: &bannerURL, Links: []postgres.ChannelLink{{Title: "Site", URL: "https://example.com"}},
			ContactEmail: "hello@example.com", FeaturedPostID: &fxPost, ShortCount: 30, LiveCount: 2, CollectionCount: 3},
		"channel_update_request.json": map[string]any{"banner_media_id": fxBanner.String(), "links": []postgres.ChannelLink{{Title: "Site", URL: "https://example.com"}},
			"contact_email": "hello@example.com", "featured_post_id": fxPost.String()},
	}
}

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func TestMTubeContracts(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "mtube")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range mtubeContracts() {
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join(dir, name)
			if update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v (UPDATE_CONTRACTS=1 to generate)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Fatalf("fixture %s is stale.\n--- want\n%s\n--- got\n%s", name, want, got)
			}
		})
	}
}

// The request fixtures decode into the handlers' request structs, so the
// documented request shape is the one the server reads.
func TestMTubeRequestFixturesDecode(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "mtube")
	var edit updatePostRequest
	if b, err := os.ReadFile(filepath.Join(dir, "post_edit_request.json")); err != nil || json.Unmarshal(b, &edit) != nil || edit.Title == nil {
		t.Fatalf("post_edit_request.json: %v", err)
	}
	var bulk bulkUploadsRequest
	if b, err := os.ReadFile(filepath.Join(dir, "uploads_bulk_request.json")); err != nil || json.Unmarshal(b, &bulk) != nil || bulk.Patch.Visibility != "private" {
		t.Fatalf("uploads_bulk_request.json: %v", err)
	}
	var ch updateChannelRequest
	if b, err := os.ReadFile(filepath.Join(dir, "channel_update_request.json")); err != nil || json.Unmarshal(b, &ch) != nil || ch.Links == nil {
		t.Fatalf("channel_update_request.json: %v", err)
	}
	if id, clear, err := parseNullableID(ch.BannerMediaID); err != nil || clear || id == nil || *id != fxBanner {
		t.Fatalf("banner id: %v %v %v", id, clear, err)
	}
}
