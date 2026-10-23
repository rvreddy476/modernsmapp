package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Golden JSON contracts for video credits (2026-10-23). The web and Android
// clients copy these. Unlike the older contract folders, every RESPONSE
// fixture here is the whole envelope the route writes ({data, meta} or
// {error, meta}), produced by the route's own writer, because the credits
// routes carry meta the clients read (credit_tags_skipped, next_cursor).
// Request fixtures are the bodies the handlers bind.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestCreditsContracts
//
// regenerates them; review the diff before committing.

var (
	crTime    = time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	crPost    = uuid.MustParse("d1111111-1111-4111-8111-111111111111")
	crOwner   = uuid.MustParse("d2222222-2222-4222-8222-222222222222")
	crAsha    = uuid.MustParse("d3333333-3333-4333-8333-333333333333")
	crRavi    = uuid.MustParse("d4444444-4444-4444-8444-444444444444")
	crDev     = uuid.MustParse("d5555555-5555-4555-8555-555555555555")
	crMedia   = uuid.MustParse("d6666666-6666-4666-8666-666666666666")
	crCover   = uuid.MustParse("d7777777-7777-4777-8777-777777777777")
	crAvatar  = uuid.MustParse("d8888888-8888-4888-8888-888888888888")
	crCredit1 = uuid.MustParse("e1111111-1111-4111-8111-111111111111")
	crCredit2 = uuid.MustParse("e2222222-2222-4222-8222-222222222222")
	crCredit3 = uuid.MustParse("e3333333-3333-4333-8333-333333333333")
	crCredit4 = uuid.MustParse("e4444444-4444-4444-8444-444444444444")
	crCredit5 = uuid.MustParse("e5555555-5555-4555-8555-555555555555")
	crDraft   = uuid.MustParse("e6666666-6666-4666-8666-666666666666")
	crChannel = "Blue Hour Films"
	crTitle   = "Monsoon Letters"
	crHouse   = "Blue Hour Films"
	crYear    = 2024
)

func crLongVideo() *postgres.Post {
	year, house := crYear, crHouse
	return &postgres.Post{
		ID: crPost, AuthorID: crOwner, Text: "A short film about letters that never arrive.", Visibility: "public",
		ContentType: "long_video", ContentTypeExplicit: true, PostType: "video", AppOrigin: "posttube", ShareToPostbook: true,
		ReviewStatus: "approved", Title: crTitle, Category: "film_animation", Language: "en", License: "standard",
		AllowEmbedding: true, PublishToFeed: true, RemixSetting: "allow", CommentModeration: "none", CommentAccess: "everyone",
		CoverMediaID: &crCover, OriginalAudioVol: 1, OverlayAudioVol: 1, AllowDownload: true, DefaultCommentSort: "top",
		Source: "upload", Genres: []string{"drama", "romance"}, ReleaseYear: &year, ProductionHouse: &house,
		CreatedAt: crTime, UpdatedAt: crTime,
		Media: []postgres.PostMedia{{MediaID: crMedia, Kind: "video", Position: 0, ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 1260000}},
	}
}

// crCredits is one of every status: Asha accepted, Ravi pending, Dev
// declined, a plain-text credit, and the owner's own (accepted) credit.
func crCredits() []postgres.PostCredit {
	answered := crTime.Add(time.Hour)
	return []postgres.PostCredit{
		{ID: crCredit1, PostID: crPost, Position: 0, Name: "Asha Rao", Role: "Director", UserID: &crAsha, Status: postgres.CreditAccepted, CreatedAt: crTime, RespondedAt: &answered},
		{ID: crCredit2, PostID: crPost, Position: 1, Name: "Ravi Kumar", Role: "Actor", UserID: &crRavi, Status: postgres.CreditPending, CreatedAt: crTime},
		{ID: crCredit3, PostID: crPost, Position: 2, Name: "Dev Menon", Role: "Cinematographer", UserID: &crDev, Status: postgres.CreditDeclined, CreatedAt: crTime, RespondedAt: &answered},
		{ID: crCredit4, PostID: crPost, Position: 3, Name: "Meera Iyer", Role: "Music", Status: postgres.CreditUnlinked, CreatedAt: crTime},
		{ID: crCredit5, PostID: crPost, Position: 4, Name: "Kiran (me)", Role: "Producer", UserID: &crOwner, Status: postgres.CreditAccepted, CreatedAt: crTime, RespondedAt: &crTime},
	}
}

func crProfiles() map[uuid.UUID]service.CreditProfile {
	return map[uuid.UUID]service.CreditProfile{
		crAsha:  {Username: "asha", DisplayName: "Asha Rao", AvatarMediaID: &crAvatar},
		crRavi:  {Username: "ravi.k", DisplayName: "Ravi Kumar"},
		crDev:   {Username: "dev.m", DisplayName: "Dev Menon"},
		crOwner: {Username: "kiran", DisplayName: "Kiran"},
	}
}

func crDetail(owner bool) service.PostDetail {
	views := service.BuildCreditViews(crCredits(), owner, crProfiles())
	return service.PostDetail{
		Post: crLongVideo(), Counts: &scylla.Counts{Likes: 412, Comments: 37, Shares: 12, Saves: 58},
		ViewCount: 9120, IsRepostable: true, Chapters: []service.ChapterRef{}, Credits: &views,
		Channel: &service.ChannelRef{UserID: crOwner, Name: crChannel, Handle: "bluehour"},
	}
}

// envelope runs one of the route writers and returns its bytes, indented.
func envelope(t *testing.T, write func(c *gin.Context)) json.RawMessage {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	write(c)
	var out bytes.Buffer
	if err := json.Indent(&out, bytes.TrimSpace(w.Body.Bytes()), "", "  "); err != nil {
		t.Fatalf("envelope: %v (%s)", err, w.Body.String())
	}
	return out.Bytes()
}

func creditsContracts(t *testing.T) map[string]any {
	ashaID := crAsha
	createReq := CreatePostRequest{
		Text: "A short film about letters that never arrive.", Visibility: "public", ContentType: "long_video", PostType: "video",
		MediaIDs: []string{crMedia.String()}, Title: crTitle, Category: "film_animation", Language: "en",
		Credits: []postgres.CreditInput{
			{Name: "Asha Rao", Role: "Director", UserID: &ashaID},
			{Name: "Ravi Kumar", Role: "Actor", UserID: &crRavi},
			{Name: "Meera Iyer", Role: "Music"},
		},
		Genres: []string{"drama", "romance"}, ReleaseYear: &[]int{crYear}[0], ProductionHouse: crHouse,
	}
	created := crLongVideo()
	ownerViews := service.BuildCreditViews(crCredits()[:4], true, crProfiles())
	requests := []service.CreditRequest{{
		ID: crCredit2, Role: "Actor", Name: "Ravi Kumar", CreatedAt: crTime,
		Post: service.CreditRequestPost{ID: crPost, Title: crTitle, CoverURL: strp("/v1/media/" + crCover.String() + "/serve"), ChannelName: strp(crChannel)},
		From: service.CreditRequestFrom{ID: crOwner, Username: "kiran", DisplayName: "Kiran", AvatarURL: "/v1/media/" + crAvatar.String() + "/serve/avatar"},
	}}
	appearance := crDetail(false)
	appearance.Credits = nil
	appearance.Chapters = nil
	draftCredits := []postgres.CreditInput{{Name: "Asha Rao", Role: "Director", UserID: &ashaID}, {Name: "Meera Iyer", Role: "Music"}}
	kind := "long_video"
	reelDraft := postgres.ReelDraft{
		ID: crDraft, AuthorID: crOwner, MediaID: &crMedia, Title: crTitle, Caption: "A short film.", Hashtags: []string{}, Tags: []string{},
		Visibility: "public", Category: "film_animation", Language: "en", PublishToFeed: true, AutoChapters: true, FeaturedPlaces: true,
		AutoConcepts: true, License: "standard", AllowEmbedding: true, RemixSetting: "allow", LikesEnabled: true, CommentsEnabled: true,
		CommentModeration: "basic", CommentAccess: "everyone", OriginalAudioVol: 1, OverlayAudioVol: 1, Status: "draft",
		ModerationStatus: "pending", CreatedAt: crTime, UpdatedAt: crTime, ContentType: &kind,
		Credits: draftCredits, Genres: []string{"drama"}, ReleaseYear: &[]int{crYear}[0], ProductionHouse: strp(crHouse),
	}
	postDraftPayload := json.RawMessage(`{"text":"A short film.","visibility":"public","content_type":"long_video","post_type":"video",` +
		`"media_ids":["` + crMedia.String() + `"],"title":"` + crTitle + `",` +
		`"credits":[{"name":"Asha Rao","role":"Director","user_id":"` + crAsha.String() + `"},{"name":"Meera Iyer","role":"Music"}],` +
		`"genres":["drama"],"release_year":2024,"production_house":"` + crHouse + `"}`)
	errorEnv := func(err error) json.RawMessage {
		return envelope(t, func(c *gin.Context) { writePostEditError(c, err) })
	}
	return map[string]any{
		"create_request.json": createReq,
		"create_response.json": envelope(t, func(c *gin.Context) {
			writeWithCreditsMeta(c, http.StatusCreated, createPostResponse{Post: created, Credits: &ownerViews}, 1)
		}),
		"patch_request.json": updatePostRequest{
			Credits: &[]postgres.CreditInput{{Name: "Asha Rao", Role: "Director", UserID: &ashaID}, {Name: "Meera Iyer", Role: "Music"}},
			Genres:  &[]string{"drama", "romance"}, ReleaseYear: json.RawMessage(`2024`), ProductionHouse: json.RawMessage(`"Blue Hour Films"`),
		},
		"patch_clear_request.json": updatePostRequest{
			Credits: &[]postgres.CreditInput{}, Genres: &[]string{}, ReleaseYear: json.RawMessage(`null`), ProductionHouse: json.RawMessage(`null`),
		},
		"post_detail_owner.json": envelope(t, func(c *gin.Context) {
			d := crDetail(true)
			writeWithCreditsMeta(c, http.StatusOK, &d, 0)
		}),
		"post_detail_viewer.json": envelope(t, func(c *gin.Context) {
			api.JSON(c.Writer, http.StatusOK, crDetail(false), nil)
		}),
		"genres.json": envelope(t, func(c *gin.Context) { api.JSON(c.Writer, http.StatusOK, service.VideoGenres(), nil) }),
		"requests.json": envelope(t, func(c *gin.Context) {
			api.JSON(c.Writer, http.StatusOK, requests, &api.Meta{NextCursor: postgres.EncodeCreditCursor(postgres.CreditCursor{At: crTime, ID: crCredit2})})
		}),
		"accept_response.json":  envelope(t, func(c *gin.Context) { api.JSON(c.Writer, 200, service.CreditAnswer{Status: "accepted"}, nil) }),
		"decline_response.json": envelope(t, func(c *gin.Context) { api.JSON(c.Writer, 200, service.CreditAnswer{Status: "declined"}, nil) }),
		"remove_response.json":  envelope(t, func(c *gin.Context) { api.JSON(c.Writer, 200, service.CreditAnswer{Status: "unlinked"}, nil) }),
		"appearances.json": envelope(t, func(c *gin.Context) {
			api.JSON(c.Writer, http.StatusOK, []service.PostDetail{appearance},
				&api.Meta{NextCursor: postgres.EncodeCreditCursor(postgres.CreditCursor{At: crTime, ID: crPost})})
		}),
		"reel_draft_patch_request.json": json.RawMessage(`{"content_type":"long_video",` +
			`"credits":[{"name":"Asha Rao","role":"Director","user_id":"` + crAsha.String() + `"},{"name":"Meera Iyer","role":"Music"}],` +
			`"genres":["drama"],"release_year":2024,"production_house":"` + crHouse + `"}`),
		"reel_draft_response.json":       envelope(t, func(c *gin.Context) { api.JSON(c.Writer, 200, reelDraft, nil) }),
		"post_draft_request.json":        postDraftRequest{PostType: "video", Payload: postDraftPayload},
		"error_credits_not_allowed.json": errorEnv(service.ErrCreditsNotAllowed),
		"error_invalid_genre.json":       errorEnv(service.ErrInvalidGenre),
		"error_credit_not_found.json":    envelope(t, func(c *gin.Context) { writeCreditError(c, service.ErrCreditNotFound) }),
	}
}

func TestCreditsContracts(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "credits")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range creditsContracts(t) {
		t.Run(name, func(t *testing.T) {
			var got []byte
			switch v := value.(type) {
			case json.RawMessage:
				var buf bytes.Buffer
				if err := json.Indent(&buf, v, "", "  "); err != nil {
					t.Fatal(err)
				}
				got = buf.Bytes()
			default:
				b, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				got = b
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

// The documented request bodies decode into the structs the handlers bind
// and pass the same validation; the owner / viewer fixtures say what the
// contract says each reader may see.
func TestCreditsFixturesMeanWhatTheContractSays(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "credits")
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var create CreatePostRequest
	if err := json.Unmarshal(read("create_request.json"), &create); err != nil || len(create.Credits) != 3 || create.Credits[0].UserID == nil {
		t.Fatalf("create_request.json: %+v %v", create, err)
	}
	if _, err := service.ValidateCredits(create.Credits); err != nil {
		t.Fatal(err)
	}
	var patch updatePostRequest
	if err := json.Unmarshal(read("patch_clear_request.json"), &patch); err != nil {
		t.Fatal(err)
	}
	var in service.PostEditInput
	if err := patch.applyCreditFields(&in); err != nil || !in.ClearReleaseYear || in.ProductionHouse == nil || *in.ProductionHouse != "" ||
		in.Credits == nil || len(*in.Credits) != 0 || in.Genres == nil || len(*in.Genres) != 0 {
		t.Fatalf("patch_clear_request.json does not clear everything: %+v %v", in, err)
	}
	var draftReq postDraftRequest
	if err := json.Unmarshal(read("post_draft_request.json"), &draftReq); err != nil {
		t.Fatal(err)
	}
	if p, err := service.ValidateDraftPayload(draftReq.PostType, draftReq.Payload); err != nil || len(p.Credits) != 2 {
		t.Fatalf("post_draft_request.json payload: %+v %v", p, err)
	}

	type row struct {
		UserID *string          `json:"user_id"`
		User   *json.RawMessage `json:"user"`
		Status *string          `json:"status"`
		Name   string           `json:"name"`
	}
	var owner, viewer struct {
		Data struct {
			Credits []row `json:"credits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read("post_detail_owner.json"), &owner); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(read("post_detail_viewer.json"), &viewer); err != nil {
		t.Fatal(err)
	}
	for i, r := range owner.Data.Credits {
		if r.Status == nil {
			t.Errorf("owner row %d has no status", i)
		}
		if (*r.Status == "unlinked") != (r.UserID == nil) {
			t.Errorf("owner row %d: user_id must be present exactly when a tag exists: %+v", i, r)
		}
		if (*r.Status == "accepted") != (r.User != nil) {
			t.Errorf("owner row %d: user only on an accepted credit", i)
		}
	}
	for i, r := range viewer.Data.Credits {
		if r.Status != nil || r.UserID != nil {
			t.Errorf("viewer row %d carries status / user_id", i)
		}
	}
	if viewer.Data.Credits[1].User != nil || viewer.Data.Credits[2].User != nil {
		t.Error("the viewer fixture shows a pending or declined account")
	}
	if viewer.Data.Credits[0].User == nil {
		t.Error("the viewer fixture does not show the accepted account")
	}
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(read("error_credits_not_allowed.json"), &errBody) != nil || errBody.Error.Code != "CREDITS_NOT_ALLOWED" {
		t.Fatalf("error fixture code = %q", errBody.Error.Code)
	}
	if !errors.Is(service.ErrCreditNotFound, postgres.ErrCreditNotFound) {
		t.Fatal("the consent refusal must be the store's not-found")
	}
}
