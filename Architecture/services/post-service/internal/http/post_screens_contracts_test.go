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

// Golden JSON contracts for the post screens parity lane (2026-10-18):
// the photo-post details screen's create request and response, the post
// detail with counts {likes, comments, shares, saves}, the draft payload
// round trip and the PATCH body. Android copies these byte for byte.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestPostScreensContracts
//
// regenerates them; review the diff before committing. Response fixtures
// are the `data` member of the api.JSON envelope.

var (
	psTime     = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	psPost     = uuid.MustParse("c1111111-1111-4111-8111-111111111111")
	psAuthor   = uuid.MustParse("c2222222-2222-4222-8222-222222222222")
	psPhotoA   = uuid.MustParse("c3333333-3333-4333-8333-333333333333")
	psPhotoB   = uuid.MustParse("c4444444-4444-4444-8444-444444444444")
	psPhotoC   = uuid.MustParse("c5555555-5555-4555-8555-555555555555")
	psDraft    = uuid.MustParse("c6666666-6666-4666-8666-666666666666")
	psLink     = "https://example.com/sunday-market"
	psPlace    = "ChIJLbZ-NFv9DDkRQJY4FbcFcgM"
	psLocation = "Banjara Hills, Hyderabad"
)

func psCarouselPost() *postgres.Post {
	lat, lng := 17.4126, 78.4387
	loc, place, link := psLocation, psPlace, psLink
	return &postgres.Post{
		ID: psPost, AuthorID: psAuthor, Text: "Sunday market haul #food @call.b", Visibility: "friends",
		ContentType: "post", PostType: "image", AppOrigin: "postbook", ShareToPostbook: true, ReviewStatus: "approved",
		Title: "Sunday market", Hashtags: []string{"food", "market"}, MentionUsernames: []string{"call.b"},
		Language: "en", License: "standard", AllowEmbedding: true, PublishToFeed: true,
		RemixSetting: "disallow", CommentModeration: "none", CommentAccess: "followers",
		CoverMediaID: &psPhotoB, OriginalAudioVol: 1, OverlayAudioVol: 1, AllowDownload: true,
		DefaultCommentSort: "top", Source: "upload",
		PaidPromotion: false, IsBranded: true, AlteredContent: false, AIGenerated: true,
		LinkURL: &link, LocationName: &loc, LocationLat: &lat, LocationLng: &lng, LocationPlaceID: &place,
		CreatedAt: psTime, UpdatedAt: psTime,
		Media: []postgres.PostMedia{
			{MediaID: psPhotoA, Kind: "image", AltText: "Stalls under string lights", Position: 0, ProcessingStatus: "ready", ModerationStatus: "passed"},
			{MediaID: psPhotoB, Kind: "image", AltText: "A basket of mangoes", Position: 1, ProcessingStatus: "ready", ModerationStatus: "passed"},
			{MediaID: psPhotoC, Kind: "image", AltText: "", AltDecorative: true, Position: 2, ProcessingStatus: "ready", ModerationStatus: "passed"},
		},
	}
}

func psDraftPayload() json.RawMessage {
	return json.RawMessage(`{"text":"Sunday market haul #food @call.b","visibility":"friends","post_type":"image",` +
		`"media_ids":["` + psPhotoA.String() + `","` + psPhotoB.String() + `","` + psPhotoC.String() + `"],` +
		`"alt_texts":{"` + psPhotoA.String() + `":"Stalls under string lights","` + psPhotoB.String() + `":"A basket of mangoes"},` +
		`"title":"Sunday market","cover_media_id":"` + psPhotoB.String() + `","cover_index":1,` +
		`"hashtags":["food","market"],"mentions":["call.b"],"remix_setting":"disallow","comment_access":"followers",` +
		`"link_url":"` + psLink + `","ai_generated":true,"is_branded":true,"paid_promotion":false,` +
		`"location_name":"` + psLocation + `","location_place_id":"` + psPlace + `","location_lat":17.4126,"location_lng":78.4387}`)
}

func postScreensContracts() map[string]any {
	idx := 1
	place := psPlace
	lat, lng := 17.4126, 78.4387
	loc := psLocation
	cover := psPhotoB.String()
	create := CreatePostRequest{
		Text: "Sunday market haul #food @call.b", Visibility: "friends", ContentType: "post", PostType: "image",
		MediaIDs: []string{psPhotoA.String(), psPhotoB.String(), psPhotoC.String()}, Title: "Sunday market",
		LocationName: &loc, LocationLat: &lat, LocationLng: &lng, LocationPlaceID: &place,
		CoverMediaID: &cover, CoverIndex: &idx, Hashtags: []string{"food", "market"}, Mentions: []string{"call.b"},
		RemixSetting: "disallow", CommentAccess: "followers", NoComments: false,
		LinkURL: psLink, AIGenerated: true, IsBranded: true, PaidPromotion: false, Language: "en",
	}
	detail := service.PostDetail{
		Post: psCarouselPost(), Counts: &scylla.Counts{Likes: 128, Comments: 14, Shares: 6, Saves: 21},
		ViewCount: 2048, IsRepostable: false, Chapters: []service.ChapterRef{},
	}
	draft := postgres.PostDraft{ID: psDraft, AuthorID: psAuthor, PostType: "post", Payload: psDraftPayload(),
		Status: "draft", CreatedAt: psTime, UpdatedAt: psTime}
	return map[string]any{
		"create_request.json":  create,
		"create_response.json": psCarouselPost(),
		"post_detail.json":     detail,
		"draft_request.json":   postDraftRequest{PostType: "post", Payload: psDraftPayload()},
		"draft_response.json":  draft,
		"drafts_list.json":     []postgres.PostDraft{draft},
		"patch_request.json": updatePostRequest{Visibility: strp("friends"), IsBranded: boolp(true), AIGenerated: boolp(false),
			LinkURL: strp(psLink), RemixSetting: strp("allow_audio_only"), CommentAccess: strp("nobody"), PaidPromotion: boolp(true)},
	}
}

func TestPostScreensContracts(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "posts")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range postScreensContracts() {
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

// The request fixtures decode into the very structs the handlers bind, and
// the draft payload passes the draft validation, so the documented shapes
// are the ones the server reads.
func TestPostScreensRequestFixturesDecode(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "posts")
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var create CreatePostRequest
	if err := json.Unmarshal(read("create_request.json"), &create); err != nil {
		t.Fatal(err)
	}
	if create.Visibility != "friends" || create.CoverIndex == nil || *create.CoverIndex != 1 || create.LinkURL != psLink ||
		!create.AIGenerated || !create.IsBranded || create.LocationPlaceID == nil || create.RemixSetting != "disallow" ||
		create.CommentAccess != "followers" || len(create.MediaIDs) != 3 {
		t.Fatalf("create_request.json = %+v", create)
	}
	var draftReq postDraftRequest
	if err := json.Unmarshal(read("draft_request.json"), &draftReq); err != nil {
		t.Fatal(err)
	}
	payload, err := service.ValidateDraftPayload(draftReq.PostType, draftReq.Payload)
	if err != nil {
		t.Fatalf("draft_request.json payload: %v", err)
	}
	if payload.PostType != "image" || payload.LinkURL == nil || payload.CoverIndex == nil || len(payload.AltTexts) != 2 {
		t.Fatalf("draft payload = %+v", payload)
	}
	var patch updatePostRequest
	if err := json.Unmarshal(read("patch_request.json"), &patch); err != nil {
		t.Fatal(err)
	}
	if patch.IsBranded == nil || patch.AIGenerated == nil || patch.LinkURL == nil || patch.Visibility == nil || *patch.Visibility != "friends" {
		t.Fatalf("patch_request.json = %+v", patch)
	}
	// The detail carries all four counts, by name.
	var detail struct {
		Counts map[string]int64 `json:"counts"`
	}
	if err := json.Unmarshal(read("post_detail.json"), &detail); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"likes", "comments", "shares", "saves"} {
		if _, ok := detail.Counts[k]; !ok {
			t.Errorf("post_detail.json counts has no %q", k)
		}
	}
}
