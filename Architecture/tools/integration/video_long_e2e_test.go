//go:build integration

package integration

// Long video, end to end through the gateway on the e2e "video" tier (the
// reels tier plus notification-service): lv-10, and in the same journey the
// P2 test gaps lv-35 (going private stops playback; was LV-F-2), lv-37
// (unlisted absent from every listing; was LV-F-5), lv-36 (resume on a
// second device) and the subscriber notification of lv-1.
//
// Tracked skips (trackedCheck, ATPOST_E2E_KNOWN_OPEN):
//
//	lv-1  B's "new upload" notification after processing
//	lv-2  a segment URL issued before "private" stops within the window

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Video_LongVideo_UploadRoutePublishPlay:
//
//	A has a channel; B subscribes with the bell on (notify_on=all)
//	A resumable-uploads two long-class clips (605 s, 1280x720): V1 public,
//	  V2 unlisted
//	V1 is hidden from B and a stranger until the review gate approves it
//	the short consumer forwards both to media.events.long (worker log)
//	B plays every rung of V1: 360p and 720p, no 1080p (above the source)
//	watch progress written on B's phone resumes on B's second device
//	V2 (unlisted) opens by link but is absent from trending, the channel
//	  listing and feed, the video feeds and search; V1 is the control
//	A's playlist: create, add V1 and V2, move V2 first
//	B is notified of V1 after processing                     (lv-1)
//	A makes V1 private: B's master and child playlists are refused at once;
//	  a segment URL B already holds expires within the window (lv-2)
func TestE2E_Video_LongVideo_UploadRoutePublishPlay(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, bID := E2EUsers(t)
	t.Parallel()

	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)
	stranger := NewHTTPClient(urls.APIGateway, uuid.Nil)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	// ── A's channel, B subscribes ──
	handle := ensureChannel(t, ctx, a)
	b.MustOK(t, ctx, "POST", "/v1/channels/"+url.PathEscape(handle)+"/subscribe", map[string]any{"notify_on": "all"})

	// ── upload both before waiting, so the two transcodes overlap ──
	v1Clip := longFixture(t, "v1", "testsrc2")
	v2Clip := longFixture(t, "v2", "testsrc")
	t.Logf("long fixtures: %d and %d bytes, %d s, %dx%d", len(v1Clip.Bytes), len(v2Clip.Bytes), v1Clip.Seconds, v1Clip.Width, v1Clip.Height)
	v1Media := resumableUploadAll(t, ctx, a, v1Clip.Bytes)
	run := uuid.NewString()[:8]
	v1 := createVideoPost(t, ctx, a, "long_video", "public", "E2E long public "+run, v1Media)

	// Strict here: a 605 s transcode cannot finish between these reads.
	t.Run("hidden before approval", func(t *testing.T) {
		bCode, _ := postAs(ctx, b, v1.ID)
		sCode, _ := postAs(ctx, stranger, v1.ID)
		bMaster := rawDo(t, ctx, b, "GET", masterPath(v1Media), nil, "")
		sMaster := rawDo(t, ctx, stranger, "GET", masterPath(v1Media), nil, "")
		_, after := postAs(ctx, a, v1.ID)
		if after.ReviewStatus == "approved" && !after.IsProcessing {
			t.Fatalf("long video approved before the stranger reads finished: the pre-approval window was not observed")
		}
		if bCode != http.StatusNotFound || sCode != http.StatusNotFound {
			t.Fatalf("unapproved long video: GET post as B=%d stranger=%d, want 404/404", bCode, sCode)
		}
		if bMaster.Status != http.StatusNotFound || sMaster.Status != http.StatusNotFound {
			t.Fatalf("unapproved long video: master as B=%d stranger=%d, want 404/404", bMaster.Status, sMaster.Status)
		}
	})

	v2Media := resumableUploadAll(t, ctx, a, v2Clip.Bytes)
	v2 := createVideoPost(t, ctx, a, "long_video", "unlisted", "E2E long unlisted "+run, v2Media)

	waitMediaReady(t, ctx, a, v1Media, 12*time.Minute)
	waitMediaReady(t, ctx, a, v2Media, 6*time.Minute)
	waitPostPlayable(t, ctx, a, v1.ID, 2*time.Minute)
	waitPostPlayable(t, ctx, a, v2.ID, 2*time.Minute)

	// ── routed on the long class ──
	t.Run("routed on media.events.long", func(t *testing.T) {
		if serviceLogPath() == "" {
			missingStack(t, "set ATPOST_E2E_SERVICE_LOG to the stack's service log to check the long-class routing")
			return
		}
		for _, id := range []string{v1Media, v2Media} {
			// cmd/worker/transcode_classes.go forwardIfLong.
			if !serviceLogContains(t, "Transcode for media "+id+" forwarded to media.events.long") {
				t.Fatalf("media %s: no \"forwarded to media.events.long\" line in %s: the short consumer transcoded a %d s video itself",
					id, serviceLogPath(), longClassSeconds)
			}
		}
	})

	// ── B plays every rung ──
	playedAt := time.Now()
	rungs := playAllRungs(t, ctx, b, v1Media)
	for _, want := range []string{"360p", "720p"} {
		if _, ok := rungs[want]; !ok {
			t.Fatalf("long-video ladder %v has no %s rung", keys(rungs), want)
		}
	}
	if _, ok := rungs["1080p"]; ok {
		t.Fatalf("long-video ladder %v has 1080p for a 720p source (upscale)", keys(rungs))
	}

	// ── resume on a second device ──
	t.Run("watch progress resumes on a second device", func(t *testing.T) {
		phone, laptop := deviceClient(b, "phone"), deviceClient(b, "laptop")
		const pos = int64(120_000)
		phone.MustOK(t, ctx, "POST", "/v1/videos/"+v1.ID+"/progress", map[string]any{
			"position_ms": pos, "duration_ms": int64(longClassSeconds) * 1000, "completed": false,
			"sent_at_ms": time.Now().UnixMilli(),
		})
		var wp struct {
			PostID     string `json:"post_id"`
			PositionMs int64  `json:"position_ms"`
		}
		Eventually(t, 15*time.Second, "the laptop reads the phone's position", func() bool {
			env, err := laptop.Do(ctx, "GET", "/v1/videos/"+v1.ID+"/progress", nil)
			if err != nil || env.Status != http.StatusOK {
				return false
			}
			return json.Unmarshal(env.Data, &wp) == nil && wp.PositionMs == pos
		})
		// Progress is the viewer's own: A has none for B's position.
		if env := a.MustDo(t, ctx, "GET", "/v1/videos/"+v1.ID+"/progress", nil); env.Status == http.StatusOK {
			var other struct {
				PositionMs int64 `json:"position_ms"`
			}
			if json.Unmarshal(env.Data, &other) == nil && other.PositionMs == pos {
				t.Fatalf("A reads B's watch position")
			}
		}
	})

	// ── unlisted is absent everywhere but its link ──
	t.Run("unlisted absent from listings", func(t *testing.T) {
		if code, p := postAs(ctx, b, v2.ID); code != http.StatusOK || p.Visibility != "unlisted" {
			t.Fatalf("unlisted video by link as B: %d visibility %q, want 200 unlisted", code, p.Visibility)
		}
		byAuthor := "/v1/posts/by-author/" + aID.String() + "?type=long_video&sort=latest"
		channelFeed := "/v1/channels/" + url.PathEscape(handle) + "/feed"
		// Positive controls: the public V1 is listed where V2 must not be.
		for _, path := range []string{byAuthor, channelFeed} {
			path := path
			var ids []string
			Eventually(t, 60*time.Second, "public V1 is listed at "+path, func() bool {
				env, err := b.Do(ctx, "GET", path, nil)
				if err != nil || env.Status != http.StatusOK {
					return false
				}
				ids, _ = listIDs(env.Data)
				return containsID(ids, v1.ID)
			})
			if containsID(ids, v2.ID) {
				t.Fatalf("unlisted V2 is listed to B at %s", path)
			}
		}
		for _, path := range []string{
			"/v1/posts/trending?content_type=long_video&limit=50",
			"/v1/posts/recent?content_type=long_video&limit=50",
			"/v1/feed/videos?limit=50",
			"/v1/feed/videos?subscribed_only=true&limit=50",
			"/v1/feed/home?feed_mode=chronological&limit=50",
		} {
			env := b.MustDo(t, ctx, "GET", path, nil)
			if env.Status != http.StatusOK {
				t.Fatalf("GET %s as B: %d %s", path, env.Status, envelopeError(env))
			}
			ids, err := listIDs(env.Data)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			if containsID(ids, v2.ID) {
				t.Fatalf("unlisted V2 is listed to B at %s", path)
			}
			t.Logf("%s: %d items, public V1 listed=%v", path, len(ids), containsID(ids, v1.ID))
		}
	})

	t.Run("unlisted absent from search", func(t *testing.T) {
		// search-service needs OpenSearch, which the video tier does not
		// start unless asked (ATPOST_E2E_REQUIRE_SEARCH=1).
		required := envInt("ATPOST_E2E_REQUIRE_SEARCH", 0) == 1
		q := "/v1/search/posts?q=" + url.QueryEscape("E2E long "+run)
		var ids []string
		found := false
		deadline := time.Now().Add(60 * time.Second)
		lastStatus := 0
		for time.Now().Before(deadline) {
			env, err := b.Do(ctx, "GET", q, nil)
			if err == nil {
				lastStatus = env.Status
				if env.Status == http.StatusOK {
					ids, _ = listIDs(env.Data)
					if containsID(ids, v1.ID) {
						found = true
						break
					}
				}
			}
			time.Sleep(3 * time.Second)
		}
		if !found {
			if required {
				t.Fatalf("search never indexed the public V1 (last status %d)", lastStatus)
			}
			t.Skipf("TIER GAP: search did not answer for the public control (last status %d); run with search-service + OpenSearch and ATPOST_E2E_REQUIRE_SEARCH=1", lastStatus)
		}
		if containsID(ids, v2.ID) {
			t.Fatalf("unlisted V2 is in B's search results")
		}
	})

	// ── playlist: create, add, reorder ──
	t.Run("playlist create and reorder", func(t *testing.T) {
		var pl struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(a.MustOK(t, ctx, "POST", "/v1/playlists", map[string]any{
			"title": "E2E playlist " + run, "visibility": "public",
		}), &pl); err != nil || pl.ID == "" {
			t.Fatalf("create playlist: id missing (err=%v)", err)
		}
		a.MustOK(t, ctx, "POST", "/v1/playlists/"+pl.ID+"/items", map[string]any{"post_id": v1.ID})
		a.MustOK(t, ctx, "POST", "/v1/playlists/"+pl.ID+"/items", map[string]any{"post_id": v2.ID})
		order := func(raw json.RawMessage) []string {
			var items []struct {
				PostID   string `json:"post_id"`
				Position int    `json:"position"`
			}
			if err := json.Unmarshal(raw, &items); err != nil {
				var wrapped struct {
					Items []struct {
						PostID   string `json:"post_id"`
						Position int    `json:"position"`
					} `json:"items"`
				}
				if err2 := json.Unmarshal(raw, &wrapped); err2 != nil {
					t.Fatalf("playlist items unreadable: %v", err)
				}
				for _, it := range wrapped.Items {
					items = append(items, it)
				}
			}
			sort.SliceStable(items, func(i, j int) bool { return items[i].Position < items[j].Position })
			out := make([]string, 0, len(items))
			for _, it := range items {
				out = append(out, it.PostID)
			}
			return out
		}
		if got := order(a.MustOK(t, ctx, "GET", "/v1/playlists/"+pl.ID+"/items", nil)); fmt.Sprint(got) != fmt.Sprint([]string{v1.ID, v2.ID}) {
			t.Fatalf("playlist order after adds %v, want [V1 V2]", got)
		}
		a.MustOK(t, ctx, "PATCH", "/v1/playlists/"+pl.ID+"/items/"+v2.ID, map[string]any{"position": 0})
		if got := order(a.MustOK(t, ctx, "GET", "/v1/playlists/"+pl.ID+"/items", nil)); fmt.Sprint(got) != fmt.Sprint([]string{v2.ID, v1.ID}) {
			t.Fatalf("playlist order after moving V2 first %v, want [V2 V1]", got)
		}
	})

	// ── lv-1: the subscriber hears about the upload once it is playable ──
	t.Run("subscriber notified after processing (lv-1)", func(t *testing.T) {
		notified := false
		deadline := time.Now().Add(60 * time.Second)
		for !notified && time.Now().Before(deadline) {
			env, err := b.Do(ctx, "GET", "/v1/notifications?limit=50", nil)
			if err == nil && env.Status == http.StatusOK {
				var ns []struct {
					Type     string `json:"type"`
					EntityID string `json:"entity_id"`
				}
				if json.Unmarshal(env.Data, &ns) == nil {
					for _, n := range ns {
						if n.Type == "creator_uploaded_video" && n.EntityID == v1.ID {
							notified = true
						}
					}
				}
			}
			if !notified {
				time.Sleep(3 * time.Second)
			}
		}
		trackedCheck(t, "lv-1", notified,
			"subscriber B (notify_on=all) got no creator_uploaded_video notification for V1 within 60 s of it becoming playable")
	})

	// ── private: refused at once; issued segments stop within the window ──
	seg := rungs["360p"]
	a.MustOK(t, ctx, "PATCH", "/v1/posts/"+v1.ID, map[string]any{"visibility": "private"})
	if r := rawDo(t, ctx, b, "GET", masterPath(v1Media), nil, ""); r.Status != http.StatusNotFound {
		t.Fatalf("private V1: master playlist as B %d, want 404 immediately", r.Status)
	}
	if r := rawDo(t, ctx, b, "GET", seg.ChildPath, nil, ""); r.Status != http.StatusNotFound {
		t.Fatalf("private V1: child playlist as B %d, want 404 immediately", r.Status)
	}
	if env := b.MustDo(t, ctx, "GET", "/v1/media/"+v1Media+"/url", nil); env.Status != http.StatusNotFound {
		t.Fatalf("private V1: url as B %d, want 404 immediately", env.Status)
	}
	if code, _ := postAs(ctx, stranger, v1.ID); code != http.StatusNotFound {
		t.Fatalf("private V1: GET post signed out %d, want 404", code)
	}

	t.Run("issued segment URL stops within the window (lv-2)", func(t *testing.T) {
		// The delivery lane's bound (media-service delivery/sign_cache.go,
		// MEDIA_HLS_SEGMENT_TTL, at most 15 min). The tier sets both to 1m.
		window := envDuration("ATPOST_E2E_HLS_SEGMENT_WINDOW", 15*time.Minute)
		expiry, ok := segmentExpiry(seg)
		if !ok {
			t.Fatalf("cannot read the expiry of the issued segment URL (no X-HLS-Expires-At, no X-Amz-Date/X-Amz-Expires)")
		}
		lifetime := expiry.Sub(playedAt)
		holds := lifetime <= window+5*time.Second
		detail := fmt.Sprintf("segment URL issued at play time expires %s later, window %s", lifetime.Round(time.Second), window)
		if holds && window <= 3*time.Minute {
			wait := time.Until(expiry) + 5*time.Second
			if wait > 0 {
				t.Logf("waiting %s for the issued segment URL to expire", wait.Round(time.Second))
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					t.Fatalf("context ended before the segment window passed")
				}
			}
			r := rawDo(t, ctx, b, "GET", seg.SegmentURI, nil, "")
			holds = r.Status == http.StatusForbidden || r.Status == http.StatusNotFound || r.Status == http.StatusGone
			detail = fmt.Sprintf("%s; fetched after expiry: HTTP %d", detail, r.Status)
		}
		trackedCheck(t, "lv-2", holds, detail)
	})
}

// ensureChannel returns the handle of c's channel, creating one if the
// lazy default could not (post-service channels.go: GET /v1/channels/me
// creates it from the profile; POST /v1/channels otherwise).
func ensureChannel(t *testing.T, ctx context.Context, c *HTTPClient) string {
	t.Helper()
	var ch struct {
		Handle string `json:"handle"`
	}
	env := c.MustDo(t, ctx, "GET", "/v1/channels/me", nil)
	if env.Status == http.StatusOK && json.Unmarshal(env.Data, &ch) == nil && ch.Handle != "" {
		return strings.TrimPrefix(ch.Handle, "@")
	}
	h := "e2e" + strings.ReplaceAll(uuid.NewString()[:10], "-", "")
	raw := c.MustOK(t, ctx, "POST", "/v1/channels", map[string]any{"name": "E2E channel", "handle": h})
	if err := json.Unmarshal(raw, &ch); err != nil || ch.Handle == "" {
		t.Fatalf("create channel: handle missing (err=%v raw=%s)", err, string(raw))
	}
	return strings.TrimPrefix(ch.Handle, "@")
}
