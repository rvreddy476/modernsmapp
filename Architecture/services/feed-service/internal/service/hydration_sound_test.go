package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Original sounds on reels (2026-09-29).
//
// HydratedPost is decoded from post-service's batch answer by field name, so
// a field it does not declare is dropped between post-service and the reel
// player without an error anywhere. These tests pin the sound all the way
// through — the decode, the hydration cache and back, and the page
// HydratePosts returns — and pin the other direction too: a post that plays
// no added sound must not grow a single key.
//
// Everything here goes through JSON, never through the Go field names, on
// purpose: the contract is the wire, and a field deleted from the struct must
// fail an assertion rather than the build.

const (
	soundTestPostID   = "5b1c6a52-3f0e-4c1d-9a56-0d2f7c1e8a01"
	soundTestAuthorID = "8e0d2b7a-64c3-4f7b-8f0a-2b9d5c3e7f02"
	soundTestMediaID  = "c4a9e1f0-7b2d-4e8a-b3c6-1f5d9a2e6b03"
	soundTestSoundID  = "2f7d4c9b-1a6e-4b3f-8d05-6e9c3a7b1d04"
	soundTestSourceID = "9a3e5d1c-8f4b-4a2e-b7d6-3c1f0e8a5b05"
	soundTestOwnerID  = "d6b2f8a4-0c5e-4d9a-a1f3-7e4b2c9d6f06"
)

// soundKeys is the whole of what this change adds to a feed item.
var soundKeys = []string{
	"audio_track_id",
	"audio_start_ms",
	"sound",
	"original_audio_volume",
	"overlay_audio_volume",
}

// soundObjectKeys is contract 2.1's `sound`, all of it and nothing else.
var soundObjectKeys = []string{
	"artist",
	"creator_user_id",
	"duration_ms",
	"id",
	"source_post_id",
	"start_ms",
	"title",
	"use_count",
}

// reelWithSoundUpstream is one entry of POST /v1/posts/batch's `data` map as
// post-service writes it for a reel that plays another creator's sound. It
// carries fields the feed has never declared (seo_title, no_likes,
// age_restricted, ...) and two nobody has declared at all (`sound_waveform`
// on the post, `status` inside the sound): an upstream that grows a field
// must never cost a feed page.
const reelWithSoundUpstream = `{
  "id": "` + soundTestPostID + `",
  "author_id": "` + soundTestAuthorID + `",
  "text": "monsoon walk #rain",
  "visibility": "public",
  "content_type": "flick",
  "post_type": "video",
  "app_origin": "postbook",
  "share_to_postbook": true,
  "review_status": "approved",
  "no_comments": false,
  "no_likes": false,
  "hide_share": false,
  "allow_download": true,
  "remix_setting": "allow_audio_only",
  "original_audio_volume": 0.25,
  "overlay_audio_volume": 0.8,
  "audio_track_id": "` + soundTestSoundID + `",
  "audio_start_ms": 1500,
  "sound": {
    "id": "` + soundTestSoundID + `",
    "title": "Original sound - Asha",
    "artist": "Asha",
    "duration_ms": 28400,
    "start_ms": 1500,
    "use_count": 3,
    "source_post_id": "` + soundTestSourceID + `",
    "creator_user_id": "` + soundTestOwnerID + `",
    "status": "ready"
  },
  "sound_waveform": [0.1, 0.4, 0.2],
  "seo_title": "ignored by the feed",
  "age_restricted": false,
  "hide_like_count": false,
  "distribution_rev": 0,
  "hashtags": ["rain"],
  "media": [
    {"media_id": "` + soundTestMediaID + `", "kind": "video", "position": 0,
     "alt_text": "", "alt_decorative": false,
     "processing_status": "ready", "moderation_status": "passed"}
  ],
  "counts": {"likes": 4, "comments": 1, "shares": 0},
  "has_reacted": false,
  "is_bookmarked": false,
  "is_processing": false,
  "is_scheduled": false,
  "created_at": "2026-09-29T08:30:00Z",
  "updated_at": "2026-09-29T08:30:00Z"
}`

// reelWithoutSoundUpstream is the same reel as every reel was before sounds
// existed: post-service sent none of the five keys.
const reelWithoutSoundUpstream = `{
  "id": "` + soundTestPostID + `",
  "author_id": "` + soundTestAuthorID + `",
  "text": "monsoon walk #rain",
  "visibility": "public",
  "content_type": "flick",
  "post_type": "video",
  "app_origin": "postbook",
  "share_to_postbook": true,
  "no_comments": false,
  "hide_share": false,
  "allow_download": true,
  "remix_setting": "allow",
  "hashtags": ["rain"],
  "media": [
    {"media_id": "` + soundTestMediaID + `", "kind": "video", "position": 0,
     "alt_text": "", "alt_decorative": false,
     "processing_status": "ready", "moderation_status": "passed"}
  ],
  "counts": {"likes": 4, "comments": 1, "shares": 0},
  "has_reacted": false,
  "is_bookmarked": false,
  "is_processing": false,
  "is_scheduled": false,
  "created_at": "2026-09-29T08:30:00Z",
  "updated_at": "2026-09-29T08:30:00Z"
}`

// reelWithoutSoundGolden is what the feed wrote for reelWithoutSoundUpstream
// BEFORE this change, byte for byte (taken from the struct as it stood at
// 1d55e3fa). A post that plays no added sound must still produce exactly
// this.
const reelWithoutSoundGolden = `{"id":"` + soundTestPostID + `","author_id":"` + soundTestAuthorID + `",` +
	`"text":"monsoon walk #rain","visibility":"public","content_type":"flick","is_pinned":false,` +
	`"created_at":"2026-09-29T08:30:00Z","updated_at":"2026-09-29T08:30:00Z",` +
	`"media":[{"media_id":"` + soundTestMediaID + `","kind":"video","position":0,"alt_text":"","alt_decorative":false,` +
	`"processing_status":"ready","moderation_status":"passed"}],` +
	`"counts":{"likes":4,"comments":1,"shares":0},"view_count":0,"has_reacted":false,"is_bookmarked":false,` +
	`"repost_count":0,"has_reposted":false,"is_repostable":false,"hashtags":["rain"],"post_type":"video",` +
	`"app_origin":"postbook","share_to_postbook":true,"no_comments":false,"hide_share":false,` +
	`"allow_download":true,"remix_setting":"allow","is_processing":false,"is_scheduled":false,` +
	`"author":{"id":"00000000-0000-0000-0000-000000000000","display_name":""}}`

// feedItemJSON decodes an upstream post the way HydratePosts does and
// re-encodes it the way the handler does.
func feedItemJSON(t *testing.T, upstream string) []byte {
	t.Helper()
	var hp HydratedPost
	if err := json.Unmarshal([]byte(upstream), &hp); err != nil {
		t.Fatalf("decode the batch entry as HydratedPost: %v", err)
	}
	out, err := json.Marshal(hp)
	if err != nil {
		t.Fatalf("marshal the feed item: %v", err)
	}
	return out
}

func asObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return obj
}

func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// assertCarriesTheSound is the one statement of "every field intact", used
// on the plain decode, on the row read back from the cache and on the page
// HydratePosts returns.
func assertCarriesTheSound(t *testing.T, where string, item map[string]any) {
	t.Helper()
	want := map[string]any{
		"audio_track_id":        soundTestSoundID,
		"audio_start_ms":        float64(1500),
		"original_audio_volume": 0.25,
		"overlay_audio_volume":  0.8,
		"remix_setting":         "allow_audio_only",
	}
	for key, val := range want {
		got, ok := item[key]
		if !ok {
			t.Errorf("%s: feed item is missing %q", where, key)
			continue
		}
		if got != val {
			t.Errorf("%s: %s = %v, want %v", where, key, got, val)
		}
	}

	sound, ok := item["sound"].(map[string]any)
	if !ok {
		t.Fatalf("%s: feed item has no sound object: %v", where, item["sound"])
	}
	if got := sortedKeys(sound); !reflect.DeepEqual(got, soundObjectKeys) {
		t.Errorf("%s: sound keys = %v, want exactly %v", where, got, soundObjectKeys)
	}
	wantSound := map[string]any{
		"id":              soundTestSoundID,
		"title":           "Original sound - Asha",
		"artist":          "Asha",
		"duration_ms":     float64(28400),
		"start_ms":        float64(1500),
		"use_count":       float64(3),
		"source_post_id":  soundTestSourceID,
		"creator_user_id": soundTestOwnerID,
	}
	for key, val := range wantSound {
		if got := sound[key]; got != val {
			t.Errorf("%s: sound.%s = %v, want %v", where, key, got, val)
		}
	}
}

func TestHydratedPostCarriesTheSound(t *testing.T) {
	item := asObject(t, feedItemJSON(t, reelWithSoundUpstream))
	assertCarriesTheSound(t, "decode", item)

	for _, key := range []string{"seo_title", "sound_waveform", "age_restricted", "no_likes", "review_status"} {
		if _, leaked := item[key]; leaked {
			t.Errorf("%q is not part of the feed contract and must not leak through", key)
		}
	}
}

// A post that plays no added sound is every post that existed before today.
// It must come out byte for byte as it did: no `"sound": null`, no
// `"audio_start_ms": 0`, no volume invented as zero.
func TestHydratedPostWithoutSoundIsUnchanged(t *testing.T) {
	out := feedItemJSON(t, reelWithoutSoundUpstream)
	if string(out) != reelWithoutSoundGolden {
		t.Errorf("a reel without a sound changed on the wire\n got: %s\nwant: %s", out, reelWithoutSoundGolden)
	}
	item := asObject(t, out)
	for _, key := range soundKeys {
		if v, ok := item[key]; ok {
			t.Errorf("a reel without a sound grew %q: %v", key, v)
		}
	}

	// `"sound": null` upstream is the same statement as no key at all.
	withNull := strings.Replace(reelWithoutSoundUpstream, `"hashtags"`, `"sound": null, "audio_start_ms": null, "hashtags"`, 1)
	if got := feedItemJSON(t, withNull); string(got) != reelWithoutSoundGolden {
		t.Errorf("an explicit null upstream changed the wire\n got: %s\nwant: %s", got, reelWithoutSoundGolden)
	}
}

// A zero post-service SENT is a value, not an absence. A creator who turns
// their own video's audio down to nothing under the sound must not have the
// player read "missing" and play it at full volume, and a sound that starts
// at its beginning starts at 0.
func TestHydratedPostKeepsSoundZeroesPostServiceSent(t *testing.T) {
	upstream := strings.NewReplacer(
		`"original_audio_volume": 0.25`, `"original_audio_volume": 0`,
		`"overlay_audio_volume": 0.8`, `"overlay_audio_volume": 0`,
		`"audio_start_ms": 1500`, `"audio_start_ms": 0`,
	).Replace(reelWithSoundUpstream)

	item := asObject(t, feedItemJSON(t, upstream))
	for _, key := range []string{"original_audio_volume", "overlay_audio_volume", "audio_start_ms"} {
		got, ok := item[key]
		if !ok {
			t.Errorf("%q was sent as 0 and came out missing", key)
			continue
		}
		if got != float64(0) {
			t.Errorf("%s = %v, want 0", key, got)
		}
	}
}

// The source reel can be gone while the sound lives on: post-service says
// `null`, and so does the feed — the key stays, the sound object keeps its
// eight keys.
func TestHydratedSoundKeepsNullSourceAndCreator(t *testing.T) {
	upstream := strings.NewReplacer(
		`"source_post_id": "`+soundTestSourceID+`"`, `"source_post_id": null`,
		`"creator_user_id": "`+soundTestOwnerID+`"`, `"creator_user_id": null`,
	).Replace(reelWithSoundUpstream)

	item := asObject(t, feedItemJSON(t, upstream))
	sound, ok := item["sound"].(map[string]any)
	if !ok {
		t.Fatalf("no sound object: %v", item["sound"])
	}
	if got := sortedKeys(sound); !reflect.DeepEqual(got, soundObjectKeys) {
		t.Fatalf("sound keys = %v, want exactly %v", got, soundObjectKeys)
	}
	for _, key := range []string{"source_post_id", "creator_user_id"} {
		if v := sound[key]; v != nil {
			t.Errorf("sound.%s = %v, want null", key, v)
		}
	}
}

// The struct is the contract: exactly contract 2.1's eight json names, none
// of them omitempty, and no place to put a URL. A presigned URL in a sound
// would sit in the hydration cache for five minutes and outlive the source
// video going private.
func TestHydratedSoundIsExactlyTheContract(t *testing.T) {
	typ := reflect.TypeOf(HydratedSound{})
	got := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if opts != "" {
			t.Errorf("sound.%s carries json option %q; the contract's keys are always present", name, opts)
		}
		if strings.Contains(strings.ToLower(name), "url") || strings.Contains(strings.ToLower(name), "key") {
			t.Errorf("sound.%s: a sound carries no URL and no storage key", name)
		}
		got = append(got, name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, soundObjectKeys) {
		t.Errorf("HydratedSound json names = %v, want exactly %v", got, soundObjectKeys)
	}
}

// soundUpstream stands in for every service HydratePosts talks to, like
// feedback_test.go's upstreamStub, except that post-service answers with the
// RAW body given — fields the feed has never heard of included — and counts
// how often it was asked. media-service answers with a signed delivery for
// the reel's video, so the test can tell a URL that belongs on the page from
// one that leaked into the cache.
func soundUpstream(t *testing.T, postBody string, batchCalls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/posts/batch":
			atomic.AddInt32(batchCalls, 1)
			_, _ = w.Write([]byte(`{"data":{"` + soundTestPostID + `":` + postBody + `}}`))
		case "/v1/internal/keyword-filters":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keywords": []string{}}})
		case "/v1/internal/graph/can":
			var req canReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := map[string]bool{}
			for _, id := range req.TargetIDs {
				data[id] = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/profiles/batch":
			_ = json.NewEncoder(w).Encode(map[string]any{
				soundTestAuthorID: map[string]any{
					"user_id": soundTestAuthorID, "display_name": "Ravi", "username": "ravi",
				},
			})
		case "/v1/media/batch":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				soundTestMediaID: map[string]any{
					"media_id": soundTestMediaID, "kind": "video", "status": "ready",
					"width": 1080, "height": 1920, "duration_ms": 21000,
					"hls_url":       "/v1/media/" + soundTestMediaID + "/hls/master.m3u8",
					"playback_url":  "/v1/media/" + soundTestMediaID + "/hls/master.m3u8",
					"playback_kind": "hls",
				},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func soundService(t *testing.T, upstreamURL string) (*Service, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	client := func(name string) *http.Client { return httpclient.NewWithBreaker(2e9, "sound-test->"+name) }
	return &Service{
		postServiceURL:    upstreamURL,
		postClient:        client("post"),
		trustSafetyURL:    upstreamURL,
		trustClient:       client("trust"),
		graphURL:          upstreamURL,
		graphClient:       client("graph"),
		profileServiceURL: upstreamURL,
		profileClient:     client("profile"),
		mediaServiceURL:   upstreamURL,
		mediaClient:       client("media"),
		feedback:          newFakeFeedbackStore(),
		rdb:               rdb,
	}, mr
}

// waitForCachedRow waits for storeHydratedCache's goroutine: the cache write
// is fire-and-forget by design, so the test has to let it land.
func waitForCachedRow(t *testing.T, mr *miniredis.Miniredis, key string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mr.Exists(key) {
			raw, err := mr.Get(key)
			if err != nil {
				t.Fatalf("read cached row: %v", err)
			}
			return raw
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hydration cache row %s was never written", key)
	return ""
}

// The whole path, twice: the first page comes from post-service and is
// cached, the second is served from the cache without asking post-service
// again. The sound must be on both, identically, and the cached row must
// hold no URL of any kind.
func TestHydratePostsCarriesTheSoundThroughTheCache(t *testing.T) {
	viewer := uuid.MustParse("71c4e9a3-5d2b-4f6e-9b08-4a7d1c3e5f07")
	postID := uuid.MustParse(soundTestPostID)
	authorID := uuid.MustParse(soundTestAuthorID)
	var batchCalls int32
	up := soundUpstream(t, reelWithSoundUpstream, &batchCalls)
	defer up.Close()
	s, mr := soundService(t, up.URL)
	items := []FeedItem{{PostID: postID, AuthorID: authorID, ContentType: "flick"}}

	fresh, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatalf("hydrate (fresh): %v", err)
	}
	if len(fresh) != 1 {
		t.Fatalf("fresh page has %d rows, want 1", len(fresh))
	}
	freshJSON, err := json.Marshal(fresh[0])
	if err != nil {
		t.Fatalf("marshal fresh row: %v", err)
	}
	assertCarriesTheSound(t, "fresh page", asObject(t, freshJSON))

	cachedRaw := waitForCachedRow(t, mr, hydrationCacheKey(viewer, postID))
	assertCarriesTheSound(t, "cached row", asObject(t, []byte(cachedRaw)))
	for _, needle := range []string{"://", "X-Amz", "Signature", "hls_url", "playback_url", "variants"} {
		if strings.Contains(cachedRaw, needle) {
			t.Errorf("the cached row holds %q; nothing signed or presigned may be cached: %s", needle, cachedRaw)
		}
	}

	fromCache, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil {
		t.Fatalf("hydrate (cached): %v", err)
	}
	if got := atomic.LoadInt32(&batchCalls); got != 1 {
		t.Fatalf("post-service was asked %d times; the second page must come from the cache", got)
	}
	if len(fromCache) != 1 {
		t.Fatalf("cached page has %d rows, want 1", len(fromCache))
	}
	cachedJSON, err := json.Marshal(fromCache[0])
	if err != nil {
		t.Fatalf("marshal cached row: %v", err)
	}
	assertCarriesTheSound(t, "page from cache", asObject(t, cachedJSON))
	if !bytes.Equal(freshJSON, cachedJSON) {
		t.Errorf("a cache hit answered differently from a miss\nfresh:  %s\ncached: %s", freshJSON, cachedJSON)
	}

	// The sound itself names no URL on the page either: the player asks
	// media-service for the bytes, which gates every request.
	sound := asObject(t, cachedJSON)["sound"]
	soundJSON, _ := json.Marshal(sound)
	if bytes.Contains(soundJSON, []byte("://")) || bytes.Contains(bytes.ToLower(soundJSON), []byte("url")) {
		t.Errorf("the sound carries a URL: %s", soundJSON)
	}

	t.Logf("hydrated reel carrying a sound:\n%s", cachedJSON)
}

// The same two pages for a reel with no added sound: neither the cache row
// nor the page may grow one of the five keys.
func TestHydratePostsWithoutSoundAddsNothing(t *testing.T) {
	viewer := uuid.MustParse("71c4e9a3-5d2b-4f6e-9b08-4a7d1c3e5f07")
	postID := uuid.MustParse(soundTestPostID)
	authorID := uuid.MustParse(soundTestAuthorID)
	var batchCalls int32
	up := soundUpstream(t, reelWithoutSoundUpstream, &batchCalls)
	defer up.Close()
	s, mr := soundService(t, up.URL)
	items := []FeedItem{{PostID: postID, AuthorID: authorID, ContentType: "flick"}}

	check := func(where string, raw []byte) {
		t.Helper()
		item := asObject(t, raw)
		for _, key := range soundKeys {
			if v, ok := item[key]; ok {
				t.Errorf("%s: a reel without a sound grew %q: %v", where, key, v)
			}
		}
	}

	fresh, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("hydrate (fresh): %v, %d rows", err, len(fresh))
	}
	freshJSON, _ := json.Marshal(fresh[0])
	check("fresh page", freshJSON)

	cachedRaw := waitForCachedRow(t, mr, hydrationCacheKey(viewer, postID))
	check("cached row", []byte(cachedRaw))
	if cachedRaw != reelWithoutSoundGolden {
		t.Errorf("the cached row of a reel without a sound changed\n got: %s\nwant: %s", cachedRaw, reelWithoutSoundGolden)
	}

	fromCache, err := s.HydratePosts(context.Background(), items, viewer)
	if err != nil || len(fromCache) != 1 {
		t.Fatalf("hydrate (cached): %v, %d rows", err, len(fromCache))
	}
	if got := atomic.LoadInt32(&batchCalls); got != 1 {
		t.Fatalf("post-service was asked %d times; the second page must come from the cache", got)
	}
	cachedJSON, _ := json.Marshal(fromCache[0])
	check("page from cache", cachedJSON)
}
