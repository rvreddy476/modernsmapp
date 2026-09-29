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
	fxRelated  = uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
)

func fixturePost() *postgres.Post {
	publishedAt := fxTime.Add(-time.Hour)
	return &postgres.Post{
		ID: fxPost, AuthorID: fxAuthor, Text: "0:00 Intro\n1:23 Setup\n12:05 The build", Visibility: "public",
		ContentType: "long_video", PostType: "video", AppOrigin: "tube", ShareToPostbook: false,
		ReviewStatus: "approved", Title: "Friday build", Tags: []string{"go", "kafka"}, Category: "science-tech",
		Language: "en", AllowEmbedding: true, PublishToFeed: true, CoverMediaID: &fxCover, AllowDownload: true,
		ContentTypeExplicit: true, Hashtags: []string{"build"}, CreatedAt: fxTime.Add(-2 * time.Hour), UpdatedAt: fxTime,
		DefaultCommentSort: "top",
		PublishedAt:        &publishedAt,
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
	post.RelatedPostID = &fxRelated
	detail := service.PostDetail{
		Post: post, Counts: &scylla.Counts{Likes: 12, Comments: 3}, ViewCount: 480, IsRepostable: true,
		Channel:        &service.ChannelRef{UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds"},
		ViewerDisliked: false, ViewerQueued: true,
		Chapters:    []service.ChapterRef{{StartMs: 0, Title: "Intro"}, {StartMs: 83000, Title: "Setup"}, {StartMs: 725000, Title: "The build"}},
		LikeCount:   &service.LikeCount{Value: 12},
		RelatedPost: &service.RelatedPostField{Card: fixtureRelatedCard()},
	}
	// Creator Hub (2026-09-28): the owner's read carries notify_subscribers
	// and every owner setting; a viewer of a post whose author hid the like
	// count gets like_count null and counts.likes 0, and a related post they
	// cannot open is not named (related_post null, related_post_id null).
	ownerPost := fixturePost()
	ownerPost.RelatedPostID, ownerPost.PaidPromotion, ownerPost.AlteredContent = &fxRelated, true, false
	ownerPost.License, ownerPost.RemixSetting, ownerPost.CommentModeration, ownerPost.CommentAccess = "creative_commons", "allow_audio_only", "basic", "followers"
	recorded := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	ownerPost.RecordingDate, ownerPost.RecordingLocation = &recorded, "Hyderabad"
	ownerPost.AgeRestricted, ownerPost.HideLikeCount, ownerPost.DefaultCommentSort = true, true, "newest"
	notifyOwner := false
	ownerDetail := service.PostDetail{
		Post: ownerPost, Counts: &scylla.Counts{Likes: 12, Comments: 3}, ViewCount: 480, IsRepostable: true,
		Channel:           &service.ChannelRef{UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds"},
		Chapters:          []service.ChapterRef{},
		LikeCount:         &service.LikeCount{Value: 12},
		RelatedPost:       &service.RelatedPostField{Card: fixtureRelatedCard()},
		NotifySubscribers: &notifyOwner,
	}
	hiddenPost := fixturePost()
	hiddenPost.HideLikeCount = true
	hiddenDetail := service.PostDetail{
		Post: hiddenPost, Counts: &scylla.Counts{Likes: 0, Comments: 3}, ViewCount: 480, IsRepostable: true,
		Chapters: []service.ChapterRef{}, LikeCount: &service.LikeCount{Hidden: true}, RelatedPost: &service.RelatedPostField{},
	}
	scheduledAt := fxTime.Add(24 * time.Hour)
	scheduled := fixturePost()
	scheduled.PublishAt, scheduled.PublishedAt, scheduled.IsScheduled, scheduled.Visibility = &scheduledAt, nil, true, "private"
	scheduled.AgeRestricted, scheduled.RelatedPostID = true, &fxRelated
	upload := service.UploadDetail{
		PostDetail:    service.PostDetail{Post: scheduled, Counts: &scylla.Counts{Likes: 0, Comments: 0}, ViewCount: 0},
		VideoMetadata: &postgres.VideoMetadata{PostID: fxPost, DurationSeconds: 725, Orientation: "landscape", ComputedCategory: "long_video", FinalCategory: "long_video", UploadStatus: "ready", MediaAssetID: &fxMedia, CreatedAt: fxTime, UpdatedAt: fxTime},
		ScheduledAt:   &scheduledAt, CommentCount: 0, ProcessingStatus: "ready", Flags: []string{"scheduled"},
		Restrictions: []service.RestrictionNotice{}, // always an array (migration 056)
		Description:  scheduled.Text, MadeForKids: false,
	}
	reel := fixturePost()
	reel.ContentType, reel.PostType, reel.Text, reel.Title = "flick", "video", "quick one #build", "Quick one"
	// GET /v1/posts/live-recordings: a promoted live recording, public
	// after its creator flipped it from the 'unlisted' it is created with.
	recording := fixturePost()
	recording.AppOrigin, recording.Source, recording.LiveStreamID = "live", "live", &fxStream
	recording.Text, recording.Tags, recording.Hashtags, recording.Category = "", nil, nil, ""
	recording.Title, recording.CoverMediaID, recording.ContentTypeExplicit = "Friday build (live)", nil, true
	liveRecordings := []service.PostDetail{{Post: recording, Counts: &scylla.Counts{Likes: 4, Comments: 1}, ViewCount: 96,
		Channel: &service.ChannelRef{UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds"}}}
	bannerURL := "/v1/media/" + fxBanner.String() + "/original"
	subscribed := true
	notify := "all"
	return map[string]any{
		"categories.json":               service.VideoCategories(),
		"post_detail.json":              detail,
		"post_detail_owner.json":        ownerDetail,
		"post_detail_hidden_likes.json": hiddenDetail,
		"post_edit_request.json": updatePostRequest{Title: strp("Friday build (final)"), Text: strp("0:00 Intro\n1:23 Setup\n12:05 The build"),
			Tags: &[]string{"go", "kafka"}, Hashtags: &[]string{"build"}, Category: strp("science-tech"), Visibility: strp("public"),
			CoverMediaID: strp(fxCover.String()), AllowDownload: boolp(true), NoComments: boolp(false), MadeForKids: boolp(false), Language: strp("en"),
			PaidPromotion: boolp(true), AlteredContent: boolp(false), License: strp("creative_commons"), AllowEmbedding: boolp(true),
			RecordingDate: strp("2026-09-20"), RecordingLocation: strp("Hyderabad"), RemixSetting: strp("allow_audio_only"),
			CommentModeration: strp("basic"), CommentAccess: strp("followers"), NotifySubscribers: boolp(false),
			AgeRestricted: boolp(true), HideLikeCount: boolp(true), DefaultCommentSort: strp("newest"), RelatedPostID: strp(fxRelated.String())},
		"reel_feed_item.json":     service.ReelFeedItem{Post: reel, ViewerReaction: "like", IsSaved: false, ViewerDisliked: false},
		"playlist.json":           postgres.Playlist{ID: fxPlaylist, CreatorID: fxAuthor, Title: "Build logs", Description: "Every Friday", Visibility: "public", ItemCount: 4, CreatedAt: fxTime, UpdatedAt: fxTime, Kind: "user"},
		"system_playlist.json":    postgres.Playlist{ID: fxPlaylist, CreatorID: fxViewer, Title: "Queue", Visibility: "private", ItemCount: 1, CreatedAt: fxTime, UpdatedAt: fxTime, Kind: "watch_later"},
		"watch_later.json":        service.WatchLaterResult{Queued: true},
		"comment.json":            fixtureComment(),
		"comments_inbox_row.json": postgres.InboxRow{Comment: fixtureComment(), Post: postgres.InboxPostRef{ID: fxPost, Title: "Friday build", ContentType: "long_video", CoverMediaID: &fxCover}, AuthorReplied: true},
		"comment_heart.json":      service.CommentHeartResult{CommentID: fxComment, HeartedByAuthor: true},
		"comment_pin.json":        service.CommentPinResult{CommentID: fxComment, PostID: fxPost, Pinned: true},
		"upload_row.json":         upload,
		"uploads_bulk_request.json": bulkUploadsRequest{PostIDs: []string{fxPost.String(), fxRelated.String()}, Patch: bulkPatchRequest{
			Visibility: strp("private"), AgeRestricted: boolp(true), DefaultCommentSort: strp("top"), License: strp("standard"),
			Tags: &[]string{"kafka"}, TagsMode: "add"}},
		"uploads_bulk.json":                bulkUploadsResponse{Results: []service.BulkOutcome{{ID: fxPost, OK: true}, {ID: fxRelated, OK: false, Error: "INVALID_TAGS"}}},
		"uploads_bulk_delete_request.json": bulkDeleteRequest{PostIDs: []string{fxPost.String(), fxStream.String()}},
		"uploads_bulk_delete.json":         bulkUploadsResponse{Results: []service.BulkOutcome{{ID: fxPost, OK: true}, {ID: fxStream, OK: false, Error: "NOT_FOUND"}}},
		"private_shares_request.json":      privateSharesRequest{UserIDs: []string{fxViewer.String()}},
		"private_shares.json": service.PrivateSharesView{Users: []service.PrivateShareUser{{UserID: fxViewer, Username: "call.b", DisplayName: "Call B",
			AvatarURL: "/v1/media/" + fxBanner.String() + "/serve/avatar", AddedAt: fxTime}}},
		"live_recordings.json": liveRecordings,
		"creator_summary.json": service.CreatorSummary{Videos: 12, Shorts: 30, Live: 2, Collections: 3, Followers: 1200},
		"channel.json": service.ChannelView{UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds", About: "Weekly builds", AvatarMediaID: nil, AvatarURL: nil,
			VideoCount: 12, SubscriberCount: 1200, IsSubscribed: &subscribed, NotifyOn: &notify, CreatedAt: fxTime.Add(-72 * time.Hour), UpdatedAt: fxTime,
			BannerMediaID: &fxBanner, BannerURL: &bannerURL, Links: []postgres.ChannelLink{{Title: "Site", URL: "https://example.com"}},
			ContactEmail: "hello@example.com", FeaturedPostID: &fxPost, ShortCount: 30, LiveCount: 2, CollectionCount: 3},
		// GET /v1/channels/:ref/feed (RSS publishing, 2026-09-29): one
		// document for everyone, enclosure paths gateway-relative.
		"channel_feed.json": fixtureChannelFeed(),
		"channel_update_request.json": map[string]any{"banner_media_id": fxBanner.String(), "links": []postgres.ChannelLink{{Title: "Site", URL: "https://example.com"}},
			"contact_email": "hello@example.com", "featured_post_id": fxPost.String()},
	}
}

// fixtureRelatedCard is the related_post block the direct read builds.
func fixtureRelatedCard() *service.RelatedPostCard {
	return &service.RelatedPostCard{ID: fxRelated, Title: "Thursday build", ThumbnailURL: "/v1/media/" + fxCover.String() + "/serve",
		DurationSeconds: 640, ChannelName: "Raghu Builds"}
}

// fixtureChannelFeed is the feed of a channel with an avatar and two
// episodes: one served as the 720p rendition with a cover, one as the
// author's original with neither cover nor hashtags.
func fixtureChannelFeed() service.ChannelFeed {
	avatarURL := "/v1/media/" + fxBanner.String() + "/serve"
	return service.ChannelFeed{
		Channel: service.ChannelFeedChannel{UserID: fxAuthor, Name: "Raghu Builds", Handle: "raghu.builds", About: "Weekly builds",
			AvatarMediaID: &fxBanner, AvatarURL: &avatarURL, ContactEmail: "hello@example.com", Language: "en", DominantCategory: "science-tech"},
		Category:  "",
		UpdatedAt: fxTime,
		Items: []service.ChannelFeedItem{
			{ID: fxPost, Title: "Friday build", Text: "0:00 Intro\n1:23 Setup\n12:05 The build", Category: "science-tech", Language: "en",
				Hashtags: []string{"build"}, PublishedAt: fxTime.Add(-time.Hour), MediaID: fxMedia, DurationMs: 725000, CoverMediaID: &fxCover,
				Enclosure: service.ChannelFeedEnclosure{Variant: "720p", Path: "/v1/media/" + fxMedia.String() + "/serve/720p", Mime: "video/mp4", SizeBytes: 184320000}},
			{ID: fxRelated, Title: "Thursday talk", Text: "", Category: "podcasts", Language: "",
				Hashtags: []string{}, PublishedAt: fxTime.Add(-25 * time.Hour), MediaID: fxStream, DurationMs: 3600000, CoverMediaID: nil,
				Enclosure: service.ChannelFeedEnclosure{Variant: "original", Path: "/v1/media/" + fxStream.String() + "/serve", Mime: "video/quicktime", SizeBytes: 912680550}},
		},
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
	all := mtubeContracts()
	for name, value := range endScreenContracts() {
		all[name] = value
	}
	for name, value := range all {
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
	if b, err := os.ReadFile(filepath.Join(dir, "post_edit_request.json")); err != nil || json.Unmarshal(b, &edit) != nil || edit.Title == nil ||
		edit.AgeRestricted == nil || edit.RelatedPostID == nil || edit.DefaultCommentSort == nil || edit.NotifySubscribers == nil {
		t.Fatalf("post_edit_request.json: %v", err)
	}
	var bulk bulkUploadsRequest
	if b, err := os.ReadFile(filepath.Join(dir, "uploads_bulk_request.json")); err != nil || json.Unmarshal(b, &bulk) != nil ||
		bulk.Patch.Visibility == nil || *bulk.Patch.Visibility != "private" || bulk.Patch.TagsMode != "add" || bulk.Patch.toService().Empty() {
		t.Fatalf("uploads_bulk_request.json: %v", err)
	}
	var del bulkDeleteRequest
	if b, err := os.ReadFile(filepath.Join(dir, "uploads_bulk_delete_request.json")); err != nil || json.Unmarshal(b, &del) != nil || len(del.PostIDs) != 2 {
		t.Fatalf("uploads_bulk_delete_request.json: %v", err)
	}
	var shares privateSharesRequest
	if b, err := os.ReadFile(filepath.Join(dir, "private_shares_request.json")); err != nil || json.Unmarshal(b, &shares) != nil || len(shares.UserIDs) != 1 {
		t.Fatalf("private_shares_request.json: %v", err)
	}
	var ch updateChannelRequest
	if b, err := os.ReadFile(filepath.Join(dir, "channel_update_request.json")); err != nil || json.Unmarshal(b, &ch) != nil || ch.Links == nil {
		t.Fatalf("channel_update_request.json: %v", err)
	}
	if id, clear, err := parseNullableID(ch.BannerMediaID); err != nil || clear || id == nil || *id != fxBanner {
		t.Fatalf("banner id: %v %v %v", id, clear, err)
	}
}
