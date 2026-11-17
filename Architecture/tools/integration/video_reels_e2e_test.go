//go:build integration

package integration

// Reels, end to end through the gateway on the e2e "video" tier
// (api-gateway, media-service + media-worker with ffmpeg, Redpanda,
// post-service, feed-service, graph, identity-profile, trust-safety, user):
// the journey no test covered (reels-10) and the server half of the
// resumable upload (reels-13).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

// reelsFeedPath is feed-service's reels feed (internal/http/handler.go).
const reelsFeedPath = "/v1/feed/reels?limit=50"

// TestE2E_Video_Reels_UploadTranscodePublishPlay:
//
//	A: resumable upload, interrupted after parts 1 and 3 and resumed
//	   (status lists exactly {1,3}; complete refused while 2 is missing;
//	   part 1 re-sent is idempotent; B cannot read A's session; a second
//	   complete is a no-op)
//	A: creates a public reel (flick) while the worker transcodes it
//	B and a signed-out stranger: the post and its master playlist are 404
//	   while the author still sees it pending
//	worker + Kafka: transcode, relay, review gate → approved, not processing
//	B: sees the reel in the reels feed, fetches the master, a child playlist
//	   and a segment (200 + the right content types)
func TestE2E_Video_Reels_UploadTranscodePublishPlay(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, bID := E2EUsers(t)
	t.Parallel()

	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)
	stranger := NewHTTPClient(urls.APIGateway, uuid.Nil)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// B follows A, so the reel is in B's world whichever source the reels
	// feed ranks from.
	b.MustOK(t, ctx, "POST", "/v1/graph/follow", map[string]any{"user_id": aID.String()})

	// 5 s portrait clip, padded with a free box to two full 5 MiB parts and
	// a remainder: three parts.
	clip := reelFixture(t)
	payload := padMP4(t, clip.Bytes, 2*5<<20+512<<10)

	// ── reels-13: the resumable server half ──
	in := resumableStart(t, ctx, a, payload)
	if in.TotalParts != 3 {
		t.Fatalf("resumable init: total_parts %d (chunk_size %d) for %d bytes, want 3", in.TotalParts, in.ChunkSize, len(payload))
	}
	resumablePut(t, ctx, a, in, payload, 1)
	ack3 := resumablePut(t, ctx, a, in, payload, 3) // part 2 "lost": the connection dropped
	if ack3.AllPartsIn {
		t.Fatalf("chunk 3: all_parts_in true with part 2 missing")
	}

	statusPath := "/v1/media/upload/resumable/" + in.UploadID
	var st resumableStatus
	if err := json.Unmarshal(a.MustOK(t, ctx, "GET", statusPath, nil), &st); err != nil {
		t.Fatalf("resumable status: %v", err)
	}
	got := append([]int(nil), st.UploadedParts...)
	sort.Ints(got)
	if fmt.Sprint(got) != "[1 3]" {
		t.Fatalf("resumable status after the interruption: uploaded_parts %v, want [1 3]", st.UploadedParts)
	}
	wantBytes := int64(len(partBytes(payload, in, 1)) + len(partBytes(payload, in, 3)))
	if st.UploadedBytes != wantBytes {
		t.Fatalf("resumable status: uploaded_bytes %d, want %d", st.UploadedBytes, wantBytes)
	}

	// Another account cannot read (or learn of) A's session.
	if env := b.MustDo(t, ctx, "GET", statusPath, nil); env.Status != http.StatusNotFound {
		t.Fatalf("B reading A's resumable session: got %d, want 404", env.Status)
	}
	// Complete is refused while a part is missing.
	if env := a.MustDo(t, ctx, "POST", statusPath+"/complete", nil); env.Status < 400 || env.Status >= 500 {
		t.Fatalf("complete with part 2 missing: got %d, want a 4xx refusal", env.Status)
	}
	// Resume: part 1 again (the client could not tell it landed) is
	// idempotent, then the missing part.
	if again := resumablePut(t, ctx, a, in, payload, 1); again.UploadedBytes != wantBytes {
		t.Fatalf("re-sent part 1: uploaded_bytes %d, want %d (double counted?)", again.UploadedBytes, wantBytes)
	}
	if ack2 := resumablePut(t, ctx, a, in, payload, 2); !ack2.AllPartsIn || ack2.UploadedBytes != int64(len(payload)) {
		t.Fatalf("chunk 2: all_parts_in=%v uploaded_bytes=%d, want true/%d", ack2.AllPartsIn, ack2.UploadedBytes, len(payload))
	}
	a.MustOK(t, ctx, "POST", statusPath+"/complete", nil)
	// A retried complete (the edge timed out) re-confirms; it does not fail.
	a.MustOK(t, ctx, "POST", statusPath+"/complete", nil)
	mediaID := in.MediaID

	// ── the reel ──
	post := createVideoPost(t, ctx, a, "flick", "public", "E2E reel "+uuid.NewString()[:8], mediaID)

	// Before approval nobody else can read or play it. Strict: approval
	// needs the transcode, the media outbox relay's next 2 s poll and
	// post-service's consumer, so it cannot land within these few reads;
	// the author's state is read after them to prove the window held.
	t.Run("hidden before approval", func(t *testing.T) {
		bPost, _ := postAs(ctx, b, post.ID)
		sPost, _ := postAs(ctx, stranger, post.ID)
		bMaster := rawDo(t, ctx, b, "GET", masterPath(mediaID), nil, "")
		sMaster := rawDo(t, ctx, stranger, "GET", masterPath(mediaID), nil, "")
		_, after := postAs(ctx, a, post.ID)
		if after.ReviewStatus == "approved" && !after.IsProcessing {
			t.Fatalf("reel approved before the stranger reads finished: the pre-approval window was not observed")
		}
		if bPost != http.StatusNotFound || sPost != http.StatusNotFound {
			t.Fatalf("unapproved reel: GET post as B=%d stranger=%d, want 404/404 (author sees review_status=%q)", bPost, sPost, after.ReviewStatus)
		}
		if bMaster.Status != http.StatusNotFound || sMaster.Status != http.StatusNotFound {
			t.Fatalf("unapproved reel: master playlist as B=%d stranger=%d, want 404/404", bMaster.Status, sMaster.Status)
		}
	})

	waitMediaReady(t, ctx, a, mediaID, 5*time.Minute)
	waitPostPlayable(t, ctx, a, post.ID, 2*time.Minute)

	// B sees it in the reels feed.
	lastFeed := "no request completed"
	Eventually(t, 90*time.Second, "reel reaches B's reels feed", func() bool {
		env, err := b.Do(ctx, "GET", reelsFeedPath, nil)
		if err != nil {
			lastFeed = err.Error()
			return false
		}
		ids, err := listIDs(env.Data)
		lastFeed = fmt.Sprintf("status %d, %d items, decode error %v, error %s", env.Status, len(ids), err, envelopeError(env))
		return env.Status == http.StatusOK && containsID(ids, post.ID)
	})
	t.Logf("reels feed: %s", lastFeed)

	// B plays it: the url route names the HLS master, then every rung.
	var u struct {
		HLSURL       string `json:"hls_url"`
		PlaybackKind string `json:"playback_kind"`
	}
	if err := json.Unmarshal(b.MustOK(t, ctx, "GET", "/v1/media/"+mediaID+"/url", nil), &u); err != nil || u.HLSURL != masterPath(mediaID) {
		t.Fatalf("GET /v1/media/:id/url as B: hls_url %q, want %q (err=%v)", u.HLSURL, masterPath(mediaID), err)
	}
	rungs := playAllRungs(t, ctx, b, mediaID)
	if _, ok := rungs["360p"]; !ok {
		t.Fatalf("reel ladder %v has no 360p rung", keys(rungs))
	}
	if _, ok := rungs["1080p"]; ok {
		t.Fatalf("reel ladder %v has a 1080p rung (reels cap at 720p)", keys(rungs))
	}
}

// TestE2E_Video_Reels_DeleteDuringProcessing: removing a reel while the worker
// is still on it leaves nothing playable, for the author (media deleted) or
// for anyone else (post deleted).
func TestE2E_Video_Reels_DeleteDuringProcessing(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, bID := E2EUsers(t)
	t.Parallel()

	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	clip := reelFixture(t)

	t.Run("media deleted mid-transcode", func(t *testing.T) {
		mediaID := mediaInitAndPut(t, ctx, a, "video", "video/mp4", clip.Bytes)
		a.MustOK(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": mediaID})
		a.MustOK(t, ctx, "DELETE", "/v1/media/"+mediaID, nil)
		// Long enough for the worker to pick the request up and finish or
		// skip it: it must not resurrect the asset or leave a playlist.
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			if code, s, err := mediaStatusOf(ctx, a, mediaID); err == nil && code != http.StatusNotFound {
				t.Fatalf("deleted media: author status read %d (%s), want 404", code, s.ProcessingStatus)
			}
			if r := rawDo(t, ctx, a, "GET", masterPath(mediaID), nil, ""); r.Status != http.StatusNotFound {
				t.Fatalf("deleted media: master playlist for the author %d, want 404", r.Status)
			}
			time.Sleep(3 * time.Second)
		}
	})

	t.Run("post deleted mid-transcode", func(t *testing.T) {
		mediaID := mediaInitAndPut(t, ctx, a, "video", "video/mp4", clip.Bytes)
		a.MustOK(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": mediaID})
		post := createVideoPost(t, ctx, a, "flick", "public", "E2E deleted reel "+uuid.NewString()[:8], mediaID)
		if env := a.MustDo(t, ctx, "DELETE", "/v1/posts/"+post.ID, nil); env.Status != http.StatusNoContent && env.Status != http.StatusOK {
			t.Fatalf("delete post: got %d, want 204", env.Status)
		}
		// Let the transcode finish (the asset is kept for restore), then
		// nobody but the author may reach it.
		Eventually(t, 4*time.Minute, "transcode of the deleted reel settles", func() bool {
			code, s, err := mediaStatusOf(ctx, a, mediaID)
			return err == nil && (code == http.StatusNotFound || s.ProcessingStatus == "ready" || s.ProcessingStatus == "failed")
		})
		if code, _ := postAs(ctx, b, post.ID); code != http.StatusNotFound {
			t.Fatalf("deleted reel: GET post as B %d, want 404", code)
		}
		if r := rawDo(t, ctx, b, "GET", masterPath(mediaID), nil, ""); r.Status != http.StatusNotFound {
			t.Fatalf("deleted reel: master playlist as B %d, want 404", r.Status)
		}
		if env := b.MustDo(t, ctx, "GET", "/v1/media/"+mediaID+"/url", nil); env.Status != http.StatusNotFound {
			t.Fatalf("deleted reel: url as B %d, want 404", env.Status)
		}
		env, err := b.Do(ctx, "GET", reelsFeedPath, nil)
		if err == nil && env.Status == http.StatusOK {
			if ids, _ := listIDs(env.Data); containsID(ids, post.ID) {
				t.Fatalf("deleted reel is in B's reels feed")
			}
		}
	})
}

// TestE2E_Video_Reels_RefusedUploads: each bad file is refused with a 4xx
// and a stable code (never a 5xx), and never becomes playable.
//
//	invalid type      init 415 UNSUPPORTED_MEDIA_TYPE (resumable init 4xx)
//	oversized         init and resumable init 413 VIDEO_TOO_LARGE
//	disguised         a PNG declared video/mp4, and an MP4 declared
//	                  video/webm: confirm 409 UPLOAD_INTEGRITY_ERROR
//	over-long         confirm accepts (length is unknown until probed); the
//	                  worker refuses it: processing_status failed, a post
//	                  refused with MEDIA_NOT_READY, no playlist
func TestE2E_Video_Reels_RefusedUploads(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, _ := E2EUsers(t)
	t.Parallel()

	// A fresh account for the init/confirm refusals: they spend no budget of
	// A's, and need no profile.
	fresh := NewHTTPClient(urls.APIGateway, uuid.New())
	a := NewHTTPClient(urls.APIGateway, aID)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	expectCode := func(t *testing.T, env *Envelope, status int, code, what string) {
		t.Helper()
		got := ""
		if env.Error != nil {
			got = env.Error.Code
		}
		if env.Status != status || got != code {
			t.Fatalf("%s: got %d %q, want %d %s", what, env.Status, got, status, code)
		}
	}

	t.Run("invalid type", func(t *testing.T) {
		env := fresh.MustDo(t, ctx, "POST", "/v1/media/init", map[string]any{
			"file_type": "video", "mime_type": "application/x-msdownload", "file_size_bytes": 1024,
		})
		expectCode(t, env, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "init of an .exe as a video")
		env = fresh.MustDo(t, ctx, "POST", "/v1/media/upload/resumable/init", map[string]any{
			"file_type": "video", "mime_type": "application/x-msdownload", "total_bytes": 1024,
		})
		if env.Status < 400 || env.Status >= 500 {
			t.Fatalf("resumable init of an .exe as a video: got %d, want a 4xx", env.Status)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		const huge = int64(17) << 30 // over the 16 GiB processing cap
		env := fresh.MustDo(t, ctx, "POST", "/v1/media/init", map[string]any{
			"file_type": "video", "mime_type": "video/mp4", "file_size_bytes": huge,
		})
		expectCode(t, env, http.StatusRequestEntityTooLarge, "VIDEO_TOO_LARGE", "init of a 17 GiB video")
		env = fresh.MustDo(t, ctx, "POST", "/v1/media/upload/resumable/init", map[string]any{
			"file_type": "video", "mime_type": "video/mp4", "total_bytes": huge,
		})
		expectCode(t, env, http.StatusRequestEntityTooLarge, "VIDEO_TOO_LARGE", "resumable init of a 17 GiB video")
	})

	t.Run("disguised extension", func(t *testing.T) {
		pngID := mediaInitAndPut(t, ctx, fresh, "video", "video/mp4", e2ePNG(t, 64))
		expectCode(t, fresh.MustDo(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": pngID}),
			http.StatusConflict, "UPLOAD_INTEGRITY_ERROR", "confirm of a PNG declared video/mp4")

		mp4 := reelFixture(t).Bytes
		webmID := mediaInitAndPut(t, ctx, fresh, "video", "video/webm", mp4)
		expectCode(t, fresh.MustDo(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": webmID}),
			http.StatusConflict, "UPLOAD_INTEGRITY_ERROR", "confirm of an MP4 declared video/webm")
		for _, id := range []string{pngID, webmID} {
			if r := rawDo(t, ctx, fresh, "GET", masterPath(id), nil, ""); r.Status != http.StatusNotFound {
				t.Fatalf("refused upload %s: master playlist %d, want 404", id, r.Status)
			}
		}
	})

	t.Run("over-long", func(t *testing.T) {
		// The worker's cap is MEDIA_MAX_VIDEO_DURATION_SEC (default 4 h);
		// the e2e tier sets it low so this clip stays tiny. Tell the flow
		// the same number.
		limit := envInt("ATPOST_E2E_MAX_VIDEO_SECONDS", 14400)
		clip := overLongFixture(t, limit+30)
		mediaID := mediaInitAndPut(t, ctx, a, "video", "video/mp4", clip.Bytes)
		a.MustOK(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": mediaID})
		var last mediaState
		Eventually(t, 4*time.Minute, "worker refuses the over-long video", func() bool {
			_, s, err := mediaStatusOf(ctx, a, mediaID)
			last = s
			if err == nil && s.ProcessingStatus == "ready" {
				t.Fatalf("over-long video (%d s, cap %d s) was transcoded", clip.Seconds, limit)
			}
			return err == nil && (s.ProcessingStatus == "failed" || s.ProcessingStatus == "rejected")
		})
		t.Logf("over-long video ended processing_status=%s moderation_status=%s", last.ProcessingStatus, last.ModerationStatus)
		env := a.WithHeader("Idempotency-Key", uuid.NewString()).MustDo(t, ctx, "POST", "/v1/posts", map[string]any{
			"title": "E2E over-long", "visibility": "public", "content_type": "flick", "media_ids": []string{mediaID},
		})
		expectCode(t, env, http.StatusBadRequest, "MEDIA_NOT_READY", "post from a refused video")
		if r := rawDo(t, ctx, a, "GET", masterPath(mediaID), nil, ""); r.Status != http.StatusNotFound {
			t.Fatalf("over-long video: master playlist %d, want 404", r.Status)
		}
	})
}

func keys(m map[string]playedRung) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
