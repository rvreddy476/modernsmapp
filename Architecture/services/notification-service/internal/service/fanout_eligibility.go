package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Module 1 fixes-v1 — Codex P1-8: one eligibility decision, applied per
// recipient immediately before the inbox write.
//
// Enqueue-time checks are not sufficient: a fan-out job can sit in the
// queue (or be retried hours later) while the post is deleted, moderated,
// or its author blocks a subscriber. Everything time-sensitive is
// therefore re-evaluated here.
//
// Two levels (long-video audit lv-1, lv-18, lv-56, lv-71; reels-30):
//
//  1. JOB level, once per pass (uploadStateFor): is the post there, and
//     can anyone but its author see it yet? The post is read AS ITS
//     AUTHOR, the one viewer post-service never hides it from, so the
//     answer says what state it is in rather than a bare 404:
//     ready, wait (processing, pending review, scheduled, held, private or
//     unlisted for now: try again later, decide nobody), or skip (deleted,
//     the creator turned notifications off, or a policy flag says no).
//     Before this, the read was anonymous, and the 404 of a still-
//     processing post was taken for "deleted" and every subscriber was
//     marked done for good.
//  2. RECIPIENT level (eligible): blocks in both directions, then, when a
//     signed-out stranger could not open the post (followers-only,
//     friends, a private account), post-service's own per-viewer answer
//     for THAT recipient, so a follower is told about a followers-only
//     upload and nobody gets a link they cannot open.
//
// Failure policy: a check that cannot be evaluated returns an error, which
// counts the recipient as FAILED (retried) rather than silently
// delivering or silently dropping.

// eligibilityDeps is the small surface the checks need. Nil clients make
// the corresponding check a no-op (dev / partial deployments).
type eligibilityDeps struct {
	graphURL    string
	postURL     string
	internalKey string
	http        *http.Client
}

// SetEligibilityDeps wires the per-recipient eligibility checks.
func (f *SubscriberFanout) SetEligibilityDeps(graphURL, postURL, internalKey string) {
	f.elig = &eligibilityDeps{
		graphURL:    graphURL,
		postURL:     postURL,
		internalKey: internalKey,
		http:        &http.Client{Timeout: 5 * time.Second},
	}
}

// UploadPolicy holds the two product decisions the founder has not taken
// yet. Both default to true, which is today's behaviour for members-only
// uploads (every subscriber is told, with the title) and the finding's
// recommended answer for 18+ uploads (subscribers are told; the watch page
// applies the age gate when they open it).
type UploadPolicy struct {
	// NotifyMembersOnly: false skips the whole fan-out for an upload behind
	// a membership tier (env UPLOAD_FANOUT_NOTIFY_MEMBERS_ONLY).
	NotifyMembersOnly bool
	// NotifyAgeRestricted: false skips the whole fan-out for an 18+ upload
	// (env UPLOAD_FANOUT_NOTIFY_AGE_RESTRICTED).
	NotifyAgeRestricted bool
}

// DefaultUploadPolicy is the policy when nothing is configured.
func DefaultUploadPolicy() UploadPolicy {
	return UploadPolicy{NotifyMembersOnly: true, NotifyAgeRestricted: true}
}

// SetUploadPolicy replaces the upload policy flags.
func (f *SubscriberFanout) SetUploadPolicy(p UploadPolicy) {
	f.policy = p
	f.policySet = true
}

func (f *SubscriberFanout) uploadPolicy() UploadPolicy {
	if !f.policySet {
		return DefaultUploadPolicy()
	}
	return f.policy
}

// uploadGate is the job-level answer for an upload fan-out.
type uploadGate int

const (
	// gateReady: the post is published and approved; decide per recipient.
	gateReady uploadGate = iota
	// gateWait: the post exists but nobody but its author can see it yet.
	// The job is deferred and nobody is decided, so a later pass (or the
	// "post became visible" trigger) still tells everyone.
	gateWait
	// gateSkip: terminal for the whole job (deleted, creator opted out, or
	// a policy flag). Nobody is told.
	gateSkip
)

// postState is the cached per-job view of the post's current state.
type postState struct {
	gate   uploadGate
	reason string
	// visibility is the post's current audience.
	visibility string
	// publicReadable: a signed-out stranger could open the post (age gate
	// aside). When false, each recipient is checked with post-service.
	publicReadable bool
	// title fills in a job whose triggering event carried none.
	title string
}

// fanoutWaitError: the post is not visible yet; defer the job.
type fanoutWaitError struct{ reason string }

func (e *fanoutWaitError) Error() string { return "post not visible yet: " + e.reason }

// fanoutSkipError: the job ends without telling anyone.
type fanoutSkipError struct{ reason string }

func (e *fanoutSkipError) Error() string { return "upload fan-out skipped: " + e.reason }

// eligible decides whether one recipient should receive this notification
// right now.
func (f *SubscriberFanout) eligible(ctx context.Context, job *postgres.FanoutJob, userID uuid.UUID) (bool, error) {
	if f.elig == nil {
		// No dependencies wired: fall back to the enqueue-time decision.
		return true, nil
	}

	// A "creator is live" job has no post behind it: PostID is the stream
	// id, which post-service would answer 404 for and so mark every
	// recipient ineligible. Its audience is the visibility captured from
	// the event; the block and follow checks below still run per recipient.
	visibility := job.Visibility
	askPostService := false
	if job.NotifType == LiveNotifType {
		if !LiveVisibilityNotifies(visibility) {
			return false, nil
		}
	} else {
		state, err := f.postStateFor(ctx, job)
		if err != nil {
			return false, err
		}
		switch state.gate {
		case gateSkip:
			// Deleted (or opted out) mid-walk: terminal for this recipient.
			return false, nil
		case gateWait:
			// No longer visible mid-walk (made private, re-held): decide
			// nobody. The error holds the recipient and the job retries.
			return false, &fanoutWaitError{reason: state.reason}
		}
		visibility = state.visibility
		askPostService = !state.publicReadable
	}

	// One graph round-trip yields both the block state and the follow
	// state we need below.
	rel, err := f.relationship(ctx, userID, job.AuthorID)
	if err != nil {
		return false, err
	}
	if rel != nil {
		// fixes-v2 / Codex P1-6/P1-7: BOTH block directions.
		if rel.Blocked || rel.BlockedBy {
			return false, nil
		}
		// Audience checks: never send a deep link the recipient cannot open.
		switch visibility {
		case "followers":
			if !rel.Follows {
				return false, nil
			}
		case "trusted", "close_friends":
			// The close-friends audience was retired on 21 Sep (graph-service
			// migration 012) along with the "Trusted Circle" tier. No such
			// audience can be created any more, and any row that still carries
			// the value has no audience to resolve against — so nothing is
			// ever delivered for it.
			return false, nil
		}
	} else if job.NotifType == LiveNotifType {
		// No graph client wired: fall back to the enqueue-time decision.
		return true, nil
	}

	if askPostService {
		// Followers-only, friends, or a private account: post-service's own
		// read decision for this recipient (lv-18).
		return f.viewerMayView(ctx, job.PostID, userID)
	}
	return true, nil
}

// graphRelationship mirrors graph-service's /v1/graph/relationship
// response (service.Relationship). Field names must stay in sync.
type graphRelationship struct {
	Follows    bool `json:"follows"`
	FollowedBy bool `json:"followed_by"`
	// Blocked: author blocked viewer. BlockedBy: viewer blocked author.
	// Both suppress delivery (fixes-v2 / Codex P1-6).
	Blocked      bool `json:"blocked"`
	BlockedBy    bool `json:"blocked_by"`
	IsMuted      bool `json:"is_muted"`
	IsConnection bool `json:"is_connection"`
}

// relationship fetches viewer→author relationship state. Returns
// (nil, nil) when no graph client is configured.
func (f *SubscriberFanout) relationship(ctx context.Context, viewerID, authorID uuid.UUID) (*graphRelationship, error) {
	if f.elig.graphURL == "" {
		return nil, nil
	}
	// Contract: user_id = the actor whose perspective we evaluate,
	// other_id = the counterparty.
	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s",
		f.elig.graphURL, viewerID, authorID)
	body, status, err := f.elig.get(ctx, url, uuid.Nil)
	if err != nil {
		return nil, fmt.Errorf("relationship: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("graph-service returned %d", status)
	}
	var env struct {
		Data graphRelationship `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("relationship decode: %w", err)
	}
	return &env.Data, nil
}

// forgetPostState drops the cached job-level state so the next read is
// fresh. Each pass over an upload job starts with one.
func (f *SubscriberFanout) forgetPostState() {
	f.stateMu.Lock()
	f.stateCache = nil
	f.stateMu.Unlock()
}

// postStateFor fetches and caches the post's current state for the life
// of one job pass (one call per batch rather than per recipient).
func (f *SubscriberFanout) postStateFor(ctx context.Context, job *postgres.FanoutJob) (*postState, error) {
	f.stateMu.Lock()
	if f.stateCache != nil && f.stateCachePost == job.PostID &&
		time.Since(f.stateCacheAt) < 30*time.Second {
		st := f.stateCache
		f.stateMu.Unlock()
		return st, nil
	}
	f.stateMu.Unlock()

	st, err := f.uploadStateFor(ctx, job)
	if err != nil {
		return nil, err
	}

	f.stateMu.Lock()
	f.stateCache, f.stateCachePost, f.stateCacheAt = st, job.PostID, time.Now()
	f.stateMu.Unlock()
	return st, nil
}

// authorView is the slice of post-service's GET /v1/posts/:id body, as the
// post's author receives it, that the fan-out decides on. Field names
// follow post-service's postgres.Post and service.PostDetail JSON.
type authorView struct {
	Visibility     string  `json:"visibility"`
	ReviewStatus   string  `json:"review_status"`
	DeletedAt      *string `json:"deleted_at"`
	IsProcessing   bool    `json:"is_processing"`
	IsScheduled    bool    `json:"is_scheduled"`
	PublishAt      *string `json:"publish_at"`
	AgeRestricted  bool    `json:"age_restricted"`
	TierRequiredID *string `json:"tier_required_id"`
	// NotifySubscribers is the creator's resolved per-upload choice, sent
	// to the owner only. Absent (nil) means no policy: notify.
	NotifySubscribers *bool `json:"notify_subscribers"`
	// Restrictions lists active case holds (copyright), owner only.
	Restrictions []json.RawMessage `json:"restrictions"`
	Title        string            `json:"title"`
}

// uploadStateFor reads the post as its author and classifies it.
//
// Reading as the author is what tells "not visible yet" from "gone" with
// the routes post-service has today: the author is exempt from the review,
// processing, scheduled, audience and age gates, so a 404 means no live
// row. That 404 is still confirmed against the internal moderation-subject
// read before anything is dropped, so a post-service that stops honouring
// X-User-Id from an internal caller degrades to "wait", never to "drop".
func (f *SubscriberFanout) uploadStateFor(ctx context.Context, job *postgres.FanoutJob) (*postState, error) {
	url := fmt.Sprintf("%s/v1/posts/%s", f.elig.postURL, job.PostID)
	body, status, err := f.elig.get(ctx, url, job.AuthorID)
	if err != nil {
		return nil, fmt.Errorf("post state: %w", err)
	}
	var st *postState
	switch status {
	case http.StatusOK:
		var env struct {
			Data authorView `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("post state decode: %w", err)
		}
		st = classifyUpload(env.Data, f.uploadPolicy())
	case http.StatusUnauthorized, http.StatusForbidden:
		// post-service answers its age refusal only after the viewer may
		// see the post at all (lv-71), so the post exists and is readable.
		// Deterministic: the age policy decides; never an endless retry.
		st = classifyUpload(authorView{
			Visibility:    job.Visibility,
			ReviewStatus:  "approved",
			AgeRestricted: true,
		}, f.uploadPolicy())
	case http.StatusNotFound:
		return f.confirmGone(ctx, job)
	default:
		return nil, fmt.Errorf("post-service returned %d", status)
	}

	if st.gate == gateReady {
		readable, err := f.publicReadable(ctx, job.PostID, st.visibility)
		if err != nil {
			return nil, err
		}
		st.publicReadable = readable
	}
	return st, nil
}

// classifyUpload is the job-level decision on what the author sees. Pure,
// so every branch is pinned by a table test.
func classifyUpload(v authorView, policy UploadPolicy) *postState {
	st := &postState{visibility: strings.ToLower(v.Visibility), title: v.Title}
	if st.visibility == "" {
		st.visibility = "public"
	}
	wait := func(reason string) *postState { st.gate, st.reason = gateWait, reason; return st }
	skip := func(reason string) *postState { st.gate, st.reason = gateSkip, reason; return st }

	if v.DeletedAt != nil {
		return skip("deleted")
	}
	// The creator's per-upload switch. Enforced here too, because the
	// "post became visible" trigger does not carry it.
	if v.NotifySubscribers != nil && !*v.NotifySubscribers {
		return skip("creator_opted_out")
	}
	// "" is a legacy row that predates review_status; it was always read
	// as approved here.
	if rs := strings.ToLower(v.ReviewStatus); rs != "" && rs != "approved" {
		return wait("review_" + rs)
	}
	if len(v.Restrictions) > 0 {
		return wait("restricted")
	}
	if v.IsProcessing {
		return wait("processing")
	}
	if v.IsScheduled || v.PublishAt != nil {
		return wait("scheduled")
	}
	switch st.visibility {
	case "private", "unlisted", "staged":
		// May be made public later; that edit re-triggers the job.
		return wait("visibility_" + st.visibility)
	case "trusted", "close_friends":
		return skip("retired_audience")
	}
	if v.TierRequiredID != nil && *v.TierRequiredID != "" && !policy.NotifyMembersOnly {
		return skip("members_only")
	}
	if v.AgeRestricted && !policy.NotifyAgeRestricted {
		return skip("age_restricted")
	}
	st.gate = gateReady
	return st
}

// confirmGone settles an author-read 404. post-service's internal
// moderation-subject read sees the row whatever its state: 404 there, or
// deleted, is a post that is really gone. A row that exists is a post the
// author read could not see, so wait rather than drop.
func (f *SubscriberFanout) confirmGone(ctx context.Context, job *postgres.FanoutJob) (*postState, error) {
	url := fmt.Sprintf("%s/v1/posts/internal/moderation-subject/%s", f.elig.postURL, job.PostID)
	body, status, err := f.elig.get(ctx, url, uuid.Nil)
	if err != nil {
		return nil, fmt.Errorf("moderation subject: %w", err)
	}
	switch status {
	case http.StatusNotFound:
		return &postState{gate: gateSkip, reason: "deleted"}, nil
	case http.StatusOK:
		var env struct {
			Data struct {
				Deleted bool `json:"deleted"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("moderation subject decode: %w", err)
		}
		if env.Data.Deleted {
			return &postState{gate: gateSkip, reason: "deleted"}, nil
		}
		return &postState{gate: gateWait, reason: "not_readable"}, nil
	default:
		return nil, fmt.Errorf("moderation subject returned %d", status)
	}
}

// publicReadable asks post-service whether a signed-out stranger could
// open the post (GET /v1/internal/posts/:id/visibility, the same decision
// as the direct read, age gate aside). Only a public post can be; a
// private account's public post is not. A plain "no" or an unexpected
// answer falls back to asking per recipient, which is never wrong, only
// slower; a 503 (post-service could not decide) is a retry.
func (f *SubscriberFanout) publicReadable(ctx context.Context, postID uuid.UUID, visibility string) (bool, error) {
	if visibility != "public" {
		return false, nil
	}
	visible, status, err := f.visibilityFor(ctx, postID, nil)
	if err != nil {
		return false, err
	}
	if status == http.StatusServiceUnavailable {
		return false, fmt.Errorf("post visibility unresolved")
	}
	return status == http.StatusOK && visible, nil
}

// viewerMayView is post-service's per-viewer read decision for one
// recipient. 200 is the answer; 503 or anything unexpected is a retry.
func (f *SubscriberFanout) viewerMayView(ctx context.Context, postID, viewerID uuid.UUID) (bool, error) {
	visible, status, err := f.visibilityFor(ctx, postID, &viewerID)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("post visibility returned %d", status)
	}
	return visible, nil
}

func (f *SubscriberFanout) visibilityFor(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (bool, int, error) {
	url := fmt.Sprintf("%s/v1/internal/posts/%s/visibility", f.elig.postURL, postID)
	if viewerID != nil {
		url += "?viewer_id=" + viewerID.String()
	}
	body, status, err := f.elig.get(ctx, url, uuid.Nil)
	if err != nil {
		return false, 0, fmt.Errorf("post visibility: %w", err)
	}
	if status != http.StatusOK {
		return false, status, nil
	}
	var resp struct {
		Visible bool `json:"visible"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, 0, fmt.Errorf("post visibility decode: %w", err)
	}
	return resp.Visible, status, nil
}

// channelNameTTL bounds how long a resolved channel name is reused
// across retries of the same job. Names change rarely and a stale one
// only affects push copy, so this is generous next to the 30s post-state
// window.
const channelNameTTL = 10 * time.Minute

// channelNameFor resolves the display name for a job whose event did not
// carry channel_name (a producer older than the Tube launch, or the
// "post became visible" trigger). One call per job: the result is cached
// beside the post-state cache so a retry does not repeat it.
//
// Failure policy differs from eligible() on purpose: the name is copy,
// not a safety decision. A lookup that fails logs and returns "", and
// renderUpload falls back to neutral wording.
func (f *SubscriberFanout) channelNameFor(ctx context.Context, job *postgres.FanoutJob) string {
	if f.elig == nil || f.elig.postURL == "" {
		return ""
	}

	f.stateMu.Lock()
	if f.nameCacheAuthor == job.AuthorID && f.nameCacheAuthor != uuid.Nil &&
		time.Since(f.nameCacheAt) < channelNameTTL {
		name := f.nameCache
		f.stateMu.Unlock()
		return name
	}
	f.stateMu.Unlock()

	// post-service resolves /v1/channels/{ref} by handle or by owner user
	// id; the author id is the stable handle-independent key.
	url := fmt.Sprintf("%s/v1/channels/%s", f.elig.postURL, job.AuthorID)
	body, status, err := f.elig.get(ctx, url, uuid.Nil)
	if err != nil {
		slog.Warn("fanout: channel name lookup failed; using neutral copy",
			"author_id", job.AuthorID, "post_id", job.PostID, "error", err)
		return ""
	}
	name := ""
	switch status {
	case http.StatusOK:
		var env struct {
			Data struct {
				Name string `json:"name"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			slog.Warn("fanout: channel name decode failed; using neutral copy",
				"author_id", job.AuthorID, "error", err)
			return ""
		}
		name = env.Data.Name
	case http.StatusNotFound:
		// A channel deleted between enqueue and delivery: cache the
		// empty answer too so the page does not re-ask.
	default:
		slog.Warn("fanout: channel name lookup returned non-200; using neutral copy",
			"author_id", job.AuthorID, "status", status)
		return ""
	}

	f.stateMu.Lock()
	f.nameCacheAuthor, f.nameCache, f.nameCacheAt = job.AuthorID, name, time.Now()
	f.stateMu.Unlock()
	return name
}

// get issues one internal GET. asUser, when set, is sent as X-User-Id so
// post-service decides as that viewer (the post's author for the job-level
// state read); uuid.Nil sends no identity.
func (d *eligibilityDeps) get(ctx context.Context, url string, asUser uuid.UUID) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if d.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", d.internalKey)
	}
	if asUser != uuid.Nil {
		req.Header.Set("X-User-Id", asUser.String())
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}

// isWait / isSkip unwrap the job-level outcomes.
func isWait(err error) (*fanoutWaitError, bool) {
	var w *fanoutWaitError
	ok := errors.As(err, &w)
	return w, ok
}

func isSkip(err error) (*fanoutSkipError, bool) {
	var s *fanoutSkipError
	ok := errors.As(err, &s)
	return s, ok
}
