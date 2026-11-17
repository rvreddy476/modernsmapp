//go:build integration

package integration

// Reels, the moderation half (e2e "video" tier):
//
//	journey 6   a reel reported by a viewer and upheld by a moderator is
//	            taken down for everyone but its author (reels-29)
//	gate line 3 nobody but the author can like, comment on or save a reel
//	            that is not yet approved (reels-18)
//
// Both behaviours are being fixed in post-service; until each fix lands
// its assertion is a tracked check (ATPOST_E2E_KNOWN_OPEN):
//
//	reels-report-takedown  the upheld report removes the reel (reels-29)
//	reels-18               engagement on an unapproved or removed reel is
//	                       the same 404 as on a post that does not exist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// engageAttempt is one engagement write and its answer.
type engageAttempt struct {
	What   string
	Status int
	Code   string
}

// engageAll tries like, comment and save on postID as c.
func engageAll(t *testing.T, ctx context.Context, c *HTTPClient, postID string) []engageAttempt {
	t.Helper()
	do := func(what string, h *HTTPClient, method, path string, body any) engageAttempt {
		env := h.MustDo(t, ctx, method, path, body)
		return engageAttempt{What: what, Status: env.Status, Code: envelopeCode(env)}
	}
	return []engageAttempt{
		do("like", c, "POST", "/v1/posts/"+postID+"/like", nil),
		do("comment", c.WithHeader("Idempotency-Key", uuid.NewString()), "POST", "/v1/posts/"+postID+"/comments",
			map[string]any{"text": "E2E comment on a reel nobody may see"}),
		do("save", c, "POST", "/v1/posts/"+postID+"/bookmark", nil),
	}
}

// sameAsMissing reports whether every attempt on the hidden reel got the
// 404 (status AND code) the same write gets on a post that does not exist,
// and describes both.
func sameAsMissing(hidden, missing []engageAttempt) (bool, string) {
	holds := true
	var parts []string
	for i := range hidden {
		h, m := hidden[i], missing[i]
		ok := m.Status == http.StatusNotFound && h.Status == m.Status && h.Code == m.Code
		holds = holds && ok
		parts = append(parts, fmt.Sprintf("%s: reel %d %q, missing post %d %q", h.What, h.Status, h.Code, m.Status, m.Code))
	}
	return holds, strings.Join(parts, "; ")
}

// TestE2E_Video_Reels_PendingEngagementRefused (reels-18, gate line 3): a
// stranger cannot like, comment on or save a reel the review gate has not
// approved, and learns nothing from the answer: it is the same 404 as for a
// post that does not exist.
func TestE2E_Video_Reels_PendingEngagementRefused(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, _ := E2EUsers(t)
	t.Parallel()

	a := NewHTTPClient(urls.APIGateway, aID)
	stranger := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	mediaID := mediaInitAndPut(t, ctx, a, "video", "video/mp4", reelFixture(t).Bytes)
	a.MustOK(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": mediaID})
	post := createVideoPost(t, ctx, a, "flick", "public", "E2E pending reel "+uuid.NewString()[:8], mediaID)

	// Strict window, as in "hidden before approval": three writes cannot
	// outrun a transcode, the outbox relay and the review consumer; the
	// author's state is read after them to prove the reel was still pending.
	pending := engageAll(t, ctx, stranger, post.ID)
	_, after := postAs(ctx, a, post.ID)
	if after.ReviewStatus == "approved" && !after.IsProcessing {
		t.Fatalf("reel approved before the stranger's writes finished: the pending window was not observed")
	}
	missing := engageAll(t, ctx, stranger, uuid.NewString())

	t.Run("stranger cannot like, comment on or save a pending reel (reels-18)", func(t *testing.T) {
		holds, detail := sameAsMissing(pending, missing)
		trackedCheck(t, "reels-18", holds, "engagement on a pending reel (review_status="+after.ReviewStatus+"): "+detail)
	})
}

// TestE2E_Video_Reels_ReportTakedown (journey 6, reels-29):
//
//	A publishes a public reel; it reaches B's reels feed (the control)
//	B reports it; a moderator (admin-scoped token, the legacy console
//	  path trust-safety honours) moves the report to reviewing, then
//	  resolves it: upheld
//	B and a signed-out stranger: the post and its master playlist are 404
//	  and the reel leaves B's reels feed
//	A still opens the reel, no longer approved, with moderation_reason set
//	  and moderation_appealable true
//	A can appeal the decision (POST /v1/appeals)
//	B cannot like, comment on or save the removed reel (reels-18)
func TestE2E_Video_Reels_ReportTakedown(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, bID := E2EUsers(t)
	t.Parallel()

	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)
	stranger := NewHTTPClient(urls.APIGateway, uuid.Nil)
	moderator := NewHTTPClient(urls.APIGateway, uuid.New()).WithAdminRole()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// B follows A so the reel is in B's world (another flow may have done it
	// already: a repeat follow is not this flow's concern).
	if env := b.MustDo(t, ctx, "POST", "/v1/graph/follow", map[string]any{"user_id": aID.String()}); env.Status >= 500 {
		t.Fatalf("B follows A: %d %s", env.Status, envelopeError(env))
	}

	mediaID := mediaInitAndPut(t, ctx, a, "video", "video/mp4", reelFixture(t).Bytes)
	a.MustOK(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": mediaID})
	post := createVideoPost(t, ctx, a, "flick", "public", "E2E reported reel "+uuid.NewString()[:8], mediaID)
	waitMediaReady(t, ctx, a, mediaID, 5*time.Minute)
	waitPostPlayable(t, ctx, a, post.ID, 2*time.Minute)

	// The control: before the report, B sees it and can play it.
	Eventually(t, 90*time.Second, "reel reaches B's reels feed", func() bool {
		env, err := b.Do(ctx, "GET", reelsFeedPath, nil)
		if err != nil || env.Status != http.StatusOK {
			return false
		}
		ids, _ := listIDs(env.Data)
		return containsID(ids, post.ID)
	})
	if r := rawDo(t, ctx, b, "GET", masterPath(mediaID), nil, ""); r.Status != http.StatusOK {
		t.Fatalf("approved reel: master playlist as B %d, want 200", r.Status)
	}

	// ── B reports, a moderator upholds ──
	var report tsReport
	if !wantData(t, b.MustDo(t, ctx, "POST", "/v1/reports", map[string]any{
		"entity_type": "post", "entity_id": post.ID, "reason": "spam", "details": "e2e: report through moderation",
	}), "B reports the reel", http.StatusOK, &report) {
		t.FailNow()
	}
	if report.ID == uuid.Nil || report.Status != "open" {
		t.Fatalf("filed report: %+v, want a new id and status open", report)
	}
	// trust-safety moderation.go validTransitions: open → reviewing →
	// resolved (upheld) or dismissed.
	for _, step := range []map[string]any{
		{"status": "reviewing"},
		{"status": "resolved", "resolution_notes": "e2e: upheld, spam reel removed"},
	} {
		var got tsReport
		if !wantData(t, moderator.MustDo(t, ctx, "PATCH", "/v1/reports/"+report.ID.String(), step),
			fmt.Sprintf("moderator sets the report %v", step["status"]), http.StatusOK, &got) {
			t.FailNow()
		}
		if got.Status != step["status"] {
			t.Fatalf("report after the moderator's %v: status %q", step["status"], got.Status)
		}
	}

	// ── the takedown, for everyone but the author ──
	takedown := false
	t.Run("upheld report takes the reel down (reels-report-takedown)", func(t *testing.T) {
		var state string
		deadline := time.Now().Add(60 * time.Second)
		for {
			bPost, _ := postAs(ctx, b, post.ID)
			sPost, _ := postAs(ctx, stranger, post.ID)
			bMaster := rawDo(t, ctx, b, "GET", masterPath(mediaID), nil, "")
			sMaster := rawDo(t, ctx, stranger, "GET", masterPath(mediaID), nil, "")
			inFeed := true
			if env, err := b.Do(ctx, "GET", reelsFeedPath, nil); err == nil && env.Status == http.StatusOK {
				ids, _ := listIDs(env.Data)
				inFeed = containsID(ids, post.ID)
			}
			av := authorView(ctx, a, post.ID)
			appealable := "absent"
			if av.Appealable != nil {
				appealable = fmt.Sprint(*av.Appealable)
			}
			takedown = bPost == http.StatusNotFound && sPost == http.StatusNotFound &&
				bMaster.Status == http.StatusNotFound && sMaster.Status == http.StatusNotFound && !inFeed &&
				av.Status == http.StatusOK && av.ReviewStatus != "" && av.ReviewStatus != "approved" &&
				strings.TrimSpace(av.Reason) != "" && appealable == "true"
			state = fmt.Sprintf("post as B %d, signed out %d; master as B %d, signed out %d; in B's reels feed %v; author %d review_status=%q moderation_reason=%q moderation_appealable=%s",
				bPost, sPost, bMaster.Status, sMaster.Status, inFeed, av.Status, av.ReviewStatus, av.Reason, appealable)
			if takedown || time.Now().After(deadline) {
				break
			}
			time.Sleep(3 * time.Second)
		}
		trackedCheck(t, "reels-report-takedown", takedown, "60 s after the moderator upheld report "+report.ID.String()+": "+state)
	})

	t.Run("author can appeal the takedown", func(t *testing.T) {
		if !takedown {
			trackedCheck(t, "reels-report-takedown", false, "appeal not attempted: the reel was not taken down")
			return
		}
		env := a.MustDo(t, ctx, "POST", "/v1/appeals", map[string]any{
			"content_type": "post", "content_id": post.ID, "appeal_reason": "e2e: this reel is not spam",
		})
		holds := env.Status == http.StatusCreated
		trackedCheck(t, "reels-report-takedown", holds,
			fmt.Sprintf("author's appeal of the removed reel: %d %q, want 201", env.Status, envelopeCode(env)))
		if holds {
			if other := b.MustDo(t, ctx, "POST", "/v1/appeals", map[string]any{
				"content_type": "post", "content_id": post.ID, "appeal_reason": "e2e: not mine",
			}); other.Status < 400 || other.Status >= 500 {
				t.Fatalf("B appealing A's removed reel: %d, want a 4xx refusal", other.Status)
			}
		}
	})

	t.Run("nobody can like, comment on or save the removed reel (reels-18)", func(t *testing.T) {
		if !takedown {
			trackedCheck(t, "reels-report-takedown", false, "not attempted: the reel was not taken down")
			return
		}
		holds, detail := sameAsMissing(engageAll(t, ctx, b, post.ID), engageAll(t, ctx, b, uuid.NewString()))
		trackedCheck(t, "reels-18", holds, "engagement on a removed reel: "+detail)
	})
}

// authorPostView is the author's read of their own post. moderation_reason
// and moderation_appealable are the author-only fields post-service gives a
// rejected post (reels-29): the moderator's reason, and whether POST
// /v1/appeals will take it. Appealable is nil when the field is absent.
type authorPostView struct {
	Status       int
	ReviewStatus string
	Reason       string
	Appealable   *bool
}

func authorView(ctx context.Context, author *HTTPClient, postID string) authorPostView {
	env, err := author.Do(ctx, "GET", "/v1/posts/"+postID, nil)
	if err != nil {
		return authorPostView{}
	}
	out := authorPostView{Status: env.Status}
	if env.Status == http.StatusOK {
		var p struct {
			ReviewStatus string `json:"review_status"`
			Reason       string `json:"moderation_reason"`
			Appealable   *bool  `json:"moderation_appealable"`
		}
		_ = json.Unmarshal(env.Data, &p)
		out.ReviewStatus, out.Reason, out.Appealable = p.ReviewStatus, p.Reason, p.Appealable
	}
	return out
}
