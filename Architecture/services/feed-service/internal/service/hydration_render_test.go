package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnrichRenderDataBatchesAuthorAndMedia(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	viewerID := uuid.New()
	authorID := uuid.New()
	mediaID := uuid.New()
	username := "author"
	avatarID := uuid.New()
	expires := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Second)

	profileCalls := 0
	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		profileCalls++
		if got := r.Header.Get("X-User-Id"); got != viewerID.String() {
			t.Errorf("profile X-User-Id=%q want %q", got, viewerID)
		}
		if got := r.Header.Get("X-Internal-Service-Key"); got != "test-internal" {
			t.Errorf("profile internal key=%q", got)
		}
		var body struct {
			UserIDs []string `json:"user_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode profile request: %v", err)
		}
		if len(body.UserIDs) != 1 || body.UserIDs[0] != authorID.String() {
			t.Fatalf("profile ids=%v", body.UserIDs)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			authorID.String(): map[string]any{
				"user_id": authorID, "display_name": "Author Name",
				"username": username, "avatar_media_id": avatarID,
			},
		})
	}))
	defer profileServer.Close()

	mediaCalls := 0
	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaCalls++
		if got := r.Header.Get("X-User-Id"); got != viewerID.String() {
			t.Errorf("media X-User-Id=%q want %q", got, viewerID)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			mediaID.String(): map[string]any{
				"media_id": mediaID, "kind": "image", "status": "ready",
				"width": 640, "height": 480,
				"duration_ms": 5070,
				"variants":    map[string]string{"thumb_150": "https://cdn.test/thumb"},
				"expires_at":  expires,
			},
		}})
	}))
	defer mediaServer.Close()

	svc := &Service{
		profileServiceURL: profileServer.URL,
		mediaServiceURL:   mediaServer.URL,
		profileClient:     profileServer.Client(),
		mediaClient:       mediaServer.Client(),
	}
	posts := []HydratedPost{{
		ID: uuid.New(), AuthorID: authorID,
		Media: []HydratedMedia{{MediaID: mediaID, Kind: "image"}},
	}}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("enrichRenderData: %v", err)
	}
	if profileCalls != 1 || mediaCalls != 1 {
		t.Fatalf("calls profile=%d media=%d want one each", profileCalls, mediaCalls)
	}
	if posts[0].Author.ID != authorID || posts[0].Author.DisplayName != "Author Name" || posts[0].Author.Username == nil || *posts[0].Author.Username != username {
		t.Fatalf("author=%+v", posts[0].Author)
	}
	if posts[0].Author.AvatarMediaID == nil || *posts[0].Author.AvatarMediaID != avatarID {
		t.Fatalf("avatar=%v want %s", posts[0].Author.AvatarMediaID, avatarID)
	}
	media := posts[0].Media[0]
	if media.Status != "ready" || media.Variants["thumb_150"] == "" || media.ExpiresAt == nil || !media.ExpiresAt.Equal(expires) {
		t.Fatalf("media=%+v", media)
	}
	if media.DurationMs != 5070 {
		t.Fatalf("duration_ms not carried from the delivery DTO: %+v", media)
	}
}

func TestEnrichRenderDataOmitsDeniedMedia(t *testing.T) {
	viewerID := uuid.New()
	authorID := uuid.New()
	mediaID := uuid.New()
	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			authorID.String(): map[string]any{"user_id": authorID, "display_name": "Author"},
		})
	}))
	defer profileServer.Close()
	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer mediaServer.Close()
	svc := &Service{
		profileServiceURL: profileServer.URL, mediaServiceURL: mediaServer.URL,
		profileClient: profileServer.Client(), mediaClient: mediaServer.Client(),
	}
	posts := []HydratedPost{{AuthorID: authorID, Media: []HydratedMedia{{MediaID: mediaID, Kind: "image"}}}}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("denied media must not fail hydration: %v", err)
	}
	if len(posts[0].Media) != 0 {
		t.Fatalf("denied media was not omitted from post: %+v", posts[0].Media)
	}
}

func TestEnrichRenderDataDefect1Regression(t *testing.T) {
	ownerID := uuid.New()
	nonOwnerViewerID := uuid.New()
	allowedMedia1 := uuid.New()
	allowedMedia2 := uuid.New()
	deniedMedia := uuid.New()
	expires := time.Now().UTC().Add(5 * time.Minute)

	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			ownerID.String(): map[string]any{"user_id": ownerID, "display_name": "Author"},
		})
	}))
	defer profileServer.Close()

	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewerHeader := r.Header.Get("X-User-Id")
		data := map[string]any{}
		if viewerHeader == ownerID.String() {
			// Owner gets all assets
			data[allowedMedia1.String()] = map[string]any{
				"media_id": allowedMedia1, "kind": "image", "status": "ready",
				"variants": map[string]string{"original": "https://cdn.test/orig1", "thumb_150": "https://cdn.test/thumb1"},
				"expires_at": expires,
			}
		} else if viewerHeader == nonOwnerViewerID.String() {
			// Permitted non-owner receives allowedMedia1 and allowedMedia2, but deniedMedia is omitted
			data[allowedMedia1.String()] = map[string]any{
				"media_id": allowedMedia1, "kind": "image", "status": "ready",
				"variants": map[string]string{"original": "https://cdn.test/orig1", "thumb_150": "https://cdn.test/thumb1"},
				"expires_at": expires,
			}
			data[allowedMedia2.String()] = map[string]any{
				"media_id": allowedMedia2, "kind": "image", "status": "ready",
				"variants": map[string]string{"original": "https://cdn.test/orig2", "thumb_150": "https://cdn.test/thumb2"},
				"expires_at": expires,
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer mediaServer.Close()

	svc := &Service{
		profileServiceURL: profileServer.URL,
		mediaServiceURL:   mediaServer.URL,
		profileClient:     profileServer.Client(),
		mediaClient:       mediaServer.Client(),
	}

	// 1. Case: Viewer is Owner
	t.Run("ViewerIsOwner", func(t *testing.T) {
		posts := []HydratedPost{{
			ID: uuid.New(), AuthorID: ownerID,
			Media: []HydratedMedia{{MediaID: allowedMedia1, Kind: "image"}},
		}}
		if err := svc.enrichRenderData(context.Background(), posts, ownerID); err != nil {
			t.Fatalf("enrichRenderData owner: %v", err)
		}
		if len(posts[0].Media) != 1 || posts[0].Media[0].MediaID != allowedMedia1 {
			t.Fatalf("expected 1 hydrated media for owner, got: %+v", posts[0].Media)
		}
		if posts[0].Media[0].Variants["thumb_150"] == "" {
			t.Fatalf("expected thumb_150 variant for owner, got: %+v", posts[0].Media[0].Variants)
		}
	})

	// 2. Case: Viewer is Permitted Non-Owner
	t.Run("ViewerIsPermittedNonOwner", func(t *testing.T) {
		posts := []HydratedPost{{
			ID: uuid.New(), AuthorID: ownerID,
			Media: []HydratedMedia{{MediaID: allowedMedia1, Kind: "image"}},
		}}
		if err := svc.enrichRenderData(context.Background(), posts, nonOwnerViewerID); err != nil {
			t.Fatalf("enrichRenderData non-owner: %v", err)
		}
		if len(posts[0].Media) != 1 || posts[0].Media[0].MediaID != allowedMedia1 {
			t.Fatalf("expected 1 hydrated media for permitted non-owner, got: %+v", posts[0].Media)
		}
		if posts[0].Media[0].Variants["thumb_150"] == "" {
			t.Fatalf("expected thumb_150 variant for permitted non-owner, got: %+v", posts[0].Media[0].Variants)
		}
	})

	// 3. Case: Multi-Asset Post with One Denied Asset
	t.Run("MultiAssetPostWithOneDeniedAsset", func(t *testing.T) {
		posts := []HydratedPost{{
			ID: uuid.New(), AuthorID: ownerID,
			Media: []HydratedMedia{
				{MediaID: allowedMedia2, Kind: "image"},
				{MediaID: deniedMedia, Kind: "image"},
			},
		}}
		if err := svc.enrichRenderData(context.Background(), posts, nonOwnerViewerID); err != nil {
			t.Fatalf("enrichRenderData multi-asset: %v", err)
		}
		if len(posts[0].Media) != 1 {
			t.Fatalf("expected exactly 1 permitted media remaining, got %d: %+v", len(posts[0].Media), posts[0].Media)
		}
		if posts[0].Media[0].MediaID != allowedMedia2 {
			t.Fatalf("expected allowedMedia2 to be retained, got: %s", posts[0].Media[0].MediaID)
		}
	})
}

func TestEnrichRenderDataThreeOutcomes(t *testing.T) {
	viewerID := uuid.New()
	authorID := uuid.New()

	allowedMediaID := uuid.New()
	notReadyMediaID := uuid.New()
	deniedMediaID := uuid.New()
	expires := time.Now().UTC().Add(5 * time.Minute)

	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			authorID.String(): map[string]any{"user_id": authorID, "display_name": "Test Author"},
		})
	}))
	defer profileServer.Close()

	mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data := map[string]any{
			// 1. ALLOWED: ready with signed variants and TTL
			allowedMediaID.String(): map[string]any{
				"media_id": allowedMediaID, "kind": "image", "status": "ready",
				"width": 1920, "height": 1080, "blurhash": "L6PZfSi_.AyE_3t7t7R**0o#DgR4",
				"variants":   map[string]string{"original": "https://cdn.test/orig", "thumb_150": "https://cdn.test/thumb"},
				"expires_at": expires,
			},
			// 2. NOT READY: in processing state, delivered with real status and no variants
			notReadyMediaID.String(): map[string]any{
				"media_id": notReadyMediaID, "kind": "video", "status": "processing",
				"width": 1280, "height": 720, "blurhash": "L5H2EC=~pQ5800t7_3s:E1bI-pt7",
			},
			// 3. DENIED: omitted from data map entirely
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer mediaServer.Close()

	svc := &Service{
		profileServiceURL: profileServer.URL,
		mediaServiceURL:   mediaServer.URL,
		profileClient:     profileServer.Client(),
		mediaClient:       mediaServer.Client(),
	}

	posts := []HydratedPost{
		{
			ID: uuid.New(), AuthorID: authorID, Text: "Post with allowed media",
			Media: []HydratedMedia{{MediaID: allowedMediaID, Kind: "image"}},
		},
		{
			ID: uuid.New(), AuthorID: authorID, Text: "Post with not-ready media",
			Media: []HydratedMedia{{MediaID: notReadyMediaID, Kind: "video"}},
		},
		{
			ID: uuid.New(), AuthorID: authorID, Text: "Post with denied media",
			Media: []HydratedMedia{{MediaID: deniedMediaID, Kind: "image"}},
		},
	}

	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("enrichRenderData: %v", err)
	}

	// Assert Outcome 1: ALLOWED post retains media with status="ready" and delivery URLs
	if len(posts[0].Media) != 1 {
		t.Fatalf("allowed post lost its media: %+v", posts[0].Media)
	}
	allowedM := posts[0].Media[0]
	if allowedM.Status != "ready" {
		t.Errorf("allowed media status=%q want 'ready'", allowedM.Status)
	}
	if allowedM.Variants["original"] == "" || allowedM.Variants["thumb_150"] == "" {
		t.Errorf("allowed media missing variants: %+v", allowedM.Variants)
	}
	if allowedM.ExpiresAt == nil {
		t.Errorf("allowed media missing expires_at")
	}

	// Assert Outcome 2: NOT READY post retains media with status="processing" and NO delivery URLs
	if len(posts[1].Media) != 1 {
		t.Fatalf("not-ready post lost its media (silent media loss bug): %+v", posts[1].Media)
	}
	notReadyM := posts[1].Media[0]
	if notReadyM.Status != "processing" {
		t.Errorf("not-ready media status=%q want 'processing'", notReadyM.Status)
	}
	if len(notReadyM.Variants) != 0 {
		t.Errorf("not-ready media must not have delivery URLs: %+v", notReadyM.Variants)
	}
	if notReadyM.ExpiresAt != nil {
		t.Errorf("not-ready media must not have expires_at")
	}

	// Assert Outcome 3: DENIED post has media omitted entirely
	if len(posts[2].Media) != 0 {
		t.Fatalf("denied media must be omitted from post, got: %+v", posts[2].Media)
	}

	// Verify serialization produces different client-visible JSON
	data0, _ := json.Marshal(posts[0])
	data1, _ := json.Marshal(posts[1])
	data2, _ := json.Marshal(posts[2])

	// Post 0 has media with variants
	var json0 map[string]any
	_ = json.Unmarshal(data0, &json0)
	if _, ok := json0["media"]; !ok {
		t.Errorf("post 0 json missing 'media' key")
	}

	// Post 1 has media with status="processing" and no variants
	var json1 map[string]any
	_ = json.Unmarshal(data1, &json1)
	mediaList1, ok := json1["media"].([]any)
	if !ok || len(mediaList1) != 1 {
		t.Fatalf("post 1 json missing 'media' list: %s", string(data1))
	}
	mediaItem1 := mediaList1[0].(map[string]any)
	if mediaItem1["status"] != "processing" {
		t.Errorf("post 1 json media status=%v want 'processing'", mediaItem1["status"])
	}
	if _, hasVariants := mediaItem1["variants"]; hasVariants {
		t.Errorf("post 1 json media should not contain 'variants' key")
	}

	// Post 2 has NO 'media' key in JSON (empty slice omitted by omitempty)
	var json2 map[string]any
	_ = json.Unmarshal(data2, &json2)
	if _, hasMedia := json2["media"]; hasMedia {
		t.Errorf("post 2 json should omit 'media' key for denied media: %s", string(data2))
	}
}

