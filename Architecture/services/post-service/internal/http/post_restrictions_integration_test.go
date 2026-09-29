//go:build integration

// Restriction routes on the real schema (M7_POSTGRES_DSN, post_it_test):
// the signed command end to end through the router and the real store,
// then P-2 at the route level — the LEGACY moderator route, the
// admin-token route, the review-status routes and the author's restore
// each change only the base and leave the hold in place — and the
// viewer's absence versus the owner's notice.
//
// The direct read and the Hub list read engagement counters from Scylla;
// they are exercised when M7_SCYLLA_HOSTS names the stack's Scylla
// (keyspace social_engagement) and logged as skipped otherwise. The
// visibility answer (GET /v1/internal/posts/:id/visibility, the ws-room and
// media gate) needs no counters and always runs.

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

type restrictionIT struct {
	*adminIT
	verifier *moderationcap.RestrictionVerifier
	signer   *moderationcap.RestrictionSigner
	author   uuid.UUID
	counters bool
}

// scyllaForTests connects to the stack Scylla when M7_SCYLLA_HOSTS is set
// (as main.go does); nil otherwise.
func scyllaForTests(t *testing.T) *scylla.InteractionStore {
	t.Helper()
	hosts := os.Getenv("M7_SCYLLA_HOSTS")
	if hosts == "" {
		return nil
	}
	cluster := gocql.NewCluster(hosts)
	cluster.Keyspace = "social_engagement"
	cluster.Consistency = gocql.Quorum
	session, err := cluster.CreateSession()
	if err != nil {
		t.Fatalf("M7_SCYLLA_HOSTS set but Scylla unreachable: %v", err)
	}
	t.Cleanup(session.Close)
	return scylla.New(session)
}

func newRestrictionIT(t *testing.T) *restrictionIT {
	t.Helper()
	base := newAdminIT(t)
	verifier, err := moderationcap.NewRestrictionVerifier(restrictionTestKey, nil, moderationcap.MaxRestrictionTTL)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := moderationcap.NewRestrictionSigner(restrictionTestKey, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The admin signer must match THIS rig's verifier, not newAdminIT's.
	v, aPriv, _, _ := adminTestVerifier(t)
	adminSigner, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", aPriv)
	if err != nil {
		t.Fatal(err)
	}
	base.admin = adminSigner
	counters := scyllaForTests(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(store.New(base.pool), counters, nil), nil).WithInternalKey(adminTestKey).WithServiceAuth(v).WithRestrictionVerifier(verifier)
	h.RegisterRoutes(r)
	h.RegisterReelDiscoveryRoutes(r)
	h.RegisterReportRoutes(r)
	h.RegisterMyUploadsRoutes(r)
	base.r = r
	return &restrictionIT{adminIT: base, verifier: verifier, signer: signer, author: uuid.New(), counters: counters != nil}
}

func (it *restrictionIT) seedOwned(t *testing.T, review string) uuid.UUID {
	return it.seedOwnedType(t, review, "post")
}

func (it *restrictionIT) seedOwnedType(t *testing.T, review, contentType string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := it.pool.Exec(context.Background(), `
		INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,search_rev,created_at,updated_at,published_at)
		VALUES ($1,$2,'restriction route proof','public',$4,$3,1,NOW(),NOW(),NOW())
	`, id, it.author, review, contentType); err != nil {
		t.Fatal(err)
	}
	return id
}

func (it *restrictionIT) command(t *testing.T, post, caseID uuid.UUID, action, expected string, rev int64) string {
	t.Helper()
	reason := "removal_upheld"
	if action == "release_hold" {
		reason = "claim_withdrawn"
	}
	claims, sig, err := it.signer.Sign(moderationcap.RestrictionClaims{
		Action: action, Source: "copyright", CaseID: caseID.String(), CaseRevision: rev,
		SubjectID: post.String(), SubjectAuthorID: it.author.String(), ExpectedState: expected,
		DecisionID: uuid.NewString(), PolicyVersion: "copyright-v1", ReasonCode: reason, ActorID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return commandBody(t, claims, sig)
}

func (it *restrictionIT) post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return adminServe(it.r, http.MethodPost, "/v1/posts/internal/restrictions", body, withKey())
}

func (it *restrictionIT) held(t *testing.T, post uuid.UUID) (count int, effective, base string) {
	t.Helper()
	if err := it.pool.QueryRow(context.Background(), `SELECT active_restriction_count, effective_review_status, review_status FROM posts WHERE id=$1`, post).Scan(&count, &effective, &base); err != nil {
		t.Fatal(err)
	}
	return
}

// assertVisible asks the ws-gateway / media answer for one viewer
// (uuid.Nil = anonymous).
func (it *restrictionIT) assertVisible(t *testing.T, post, viewer uuid.UUID, want bool) {
	t.Helper()
	path := "/v1/internal/posts/" + post.String() + "/visibility"
	if viewer != uuid.Nil {
		path += "?viewer_id=" + viewer.String()
	}
	w := adminServe(it.r, http.MethodGet, path, "", withKey())
	var body struct {
		Visible bool `json:"visible"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusOK || body.Visible != want {
		t.Fatalf("visibility for viewer %s: status=%d visible=%v, want %v (%s)", viewer, w.Code, body.Visible, want, w.Body.String())
	}
}

func (it *restrictionIT) ownerHeaders() map[string]string {
	return map[string]string{"X-Internal-Service-Key": adminTestKey, "X-User-Id": it.author.String()}
}

// directReadAndHub is the counter-backed half: the viewer's 404, the
// anonymous 404, the owner's 200 with the notice, and the Hub row's flag.
func (it *restrictionIT) directReadAndHub(t *testing.T, post, caseID uuid.UUID) {
	t.Helper()
	if w := adminServe(it.r, http.MethodGet, "/v1/posts/"+post.String(), "", map[string]string{"X-Internal-Service-Key": adminTestKey, "X-User-Id": uuid.NewString()}); w.Code != http.StatusNotFound {
		t.Fatalf("viewer: status=%d body=%s, want 404", w.Code, w.Body.String())
	}
	if w := adminServe(it.r, http.MethodGet, "/v1/posts/"+post.String(), "", withKey()); w.Code != http.StatusNotFound {
		t.Fatalf("anonymous: status=%d, want 404", w.Code)
	}
	owner := adminServe(it.r, http.MethodGet, "/v1/posts/"+post.String(), "", it.ownerHeaders())
	if owner.Code != http.StatusOK {
		t.Fatalf("owner: status=%d body=%s", owner.Code, owner.Body.String())
	}
	var detail struct {
		Data struct {
			ReviewStatus string                      `json:"review_status"`
			Restrictions []service.RestrictionNotice `json:"restrictions"`
		} `json:"data"`
	}
	_ = json.Unmarshal(owner.Body.Bytes(), &detail)
	if detail.Data.ReviewStatus != "approved" || len(detail.Data.Restrictions) != 1 || detail.Data.Restrictions[0].CaseID != caseID || detail.Data.Restrictions[0].ReasonCode != "removal_upheld" {
		t.Fatalf("owner detail: %s", owner.Body.String())
	}
	// The Hub list flags a held video and names the case.
	video := it.seedOwnedType(t, "approved", "long_video")
	videoCase := uuid.New()
	if w := it.post(t, it.command(t, video, videoCase, "place_hold", "absent", 1)); w.Code != http.StatusOK {
		t.Fatalf("hold video: %d %s", w.Code, w.Body.String())
	}
	hub := adminServe(it.r, http.MethodGet, "/v1/uploads/videos?limit=50", "", it.ownerHeaders())
	if hub.Code != http.StatusOK {
		t.Fatalf("hub: status=%d body=%s", hub.Code, hub.Body.String())
	}
	var rows struct {
		Data []service.UploadDetail `json:"data"`
	}
	_ = json.Unmarshal(hub.Body.Bytes(), &rows)
	seen := false
	for _, row := range rows.Data {
		if row.Post == nil || row.Post.ID != video {
			continue
		}
		seen = true
		if len(row.Flags) != 1 || row.Flags[0] != service.UploadFlagCopyrightHold || len(row.Restrictions) != 1 || row.Restrictions[0].CaseID != videoCase {
			t.Fatalf("hub row: flags=%v restrictions=%+v", row.Flags, row.Restrictions)
		}
	}
	if !seen {
		t.Fatalf("hub list did not return the owner's held video: %s", hub.Body.String())
	}
}

func TestRestrictionIT_CommandThroughTheRouter(t *testing.T) {
	it := newRestrictionIT(t)
	post := it.seedOwned(t, "approved")
	caseID := uuid.New()
	it.assertVisible(t, post, uuid.New(), true)
	w := it.post(t, it.command(t, post, caseID, "place_hold", "absent", 1))
	if w.Code != http.StatusOK {
		t.Fatalf("place: status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Data store.RestrictionOutcome `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Data.State != "active" || out.Data.ActiveRestrictionCount != 1 || out.Data.EffectiveReviewStatus != "restricted" || out.Data.Replayed {
		t.Fatalf("outcome %+v", out.Data)
	}
	if n, eff, base := it.held(t, post); n != 1 || eff != "restricted" || base != "approved" {
		t.Fatalf("row: count=%d effective=%s base=%s", n, eff, base)
	}
	// Viewer and anonymous: the same absence a rejected post gets. Owner:
	// still visible.
	it.assertVisible(t, post, uuid.New(), false)
	it.assertVisible(t, post, uuid.Nil, false)
	it.assertVisible(t, post, it.author, true)
	if it.counters {
		it.directReadAndHub(t, post, caseID)
	} else {
		t.Log("M7_SCYLLA_HOSTS unset: the direct read and the Hub list (counter-backed) are not exercised")
	}
	// The extended moderation subject.
	subj := adminServe(it.r, http.MethodGet, "/v1/posts/internal/moderation-subject/"+post.String(), "", withKey())
	if subj.Code != http.StatusOK {
		t.Fatalf("subject: status=%d", subj.Code)
	}
	var subject struct {
		Data store.ModerationSubject `json:"data"`
	}
	_ = json.Unmarshal(subj.Body.Bytes(), &subject)
	if subject.Data.BaseReviewStatus != "approved" || subject.Data.EffectiveReviewStatus != "restricted" || subject.Data.ReviewStatus != "approved" || len(subject.Data.ActiveRestrictions) != 1 {
		t.Fatalf("subject: %s", subj.Body.String())
	}
	// An active copyright hold names copyright as the source whatever the
	// base decision says; the id is the base decision's (none yet here).
	if subject.Data.LastDecisionSource != "copyright" || subject.Data.LatestBaseDecisionSource != "copyright" || subject.Data.LastDecisionID != nil {
		t.Fatalf("subject under hold: %s", subj.Body.String())
	}
	it.assertSubjectAfterDecision(t, post)
	// Release with a stale revision: 409; then correctly: 200 and the
	// viewer sees the post again.
	if w := it.post(t, it.command(t, post, caseID, "release_hold", "active", 1)); w.Code != http.StatusConflict || adminErrorCode(t, w) != CodeStaleCaseRevision {
		t.Fatalf("stale: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := it.post(t, it.command(t, post, caseID, "release_hold", "active", 4)); w.Code != http.StatusOK {
		t.Fatalf("release: status=%d body=%s", w.Code, w.Body.String())
	}
	it.assertVisible(t, post, uuid.New(), true)
	it.assertVisible(t, post, uuid.Nil, true)
}

// P-2 at the route level: each writer answers 200 and changes only the base.
func TestRestrictionIT_RoutesNeverClearAHold(t *testing.T) {
	it := newRestrictionIT(t)
	hold := func(post uuid.UUID) {
		if w := it.post(t, it.command(t, post, uuid.New(), "place_hold", "absent", 1)); w.Code != http.StatusOK {
			t.Fatalf("hold: %d %s", w.Code, w.Body.String())
		}
	}
	assertHeld := func(name string, post uuid.UUID, wantBase string) {
		t.Helper()
		n, eff, base := it.held(t, post)
		if n != 1 || eff != "restricted" || base != wantBase {
			t.Fatalf("%s: count=%d effective=%s base=%s, want 1/restricted/%s", name, n, eff, base, wantBase)
		}
		it.assertVisible(t, post, uuid.New(), false)
	}
	// 1. LEGACY moderator route (gateway superadmin headers).
	p1 := it.seedOwned(t, "flagged")
	hold(p1)
	if w := adminServe(it.r, http.MethodPost, "/v1/posts/"+p1.String()+"/moderation", decisionBody("approve"), edgeModerator(uuid.New())); w.Code != http.StatusOK {
		t.Fatalf("legacy approve: %d %s", w.Code, w.Body.String())
	}
	assertHeld("legacy moderator approve", p1, "approved")

	// 2. admin-token route.
	p2 := it.seedOwned(t, "flagged")
	hold(p2)
	if w := adminServe(it.r, http.MethodPost, InternalAdminPrefix+"/posts/"+p2.String()+"/moderation", decisionBody("approve"), it.as(t, PermPostsModerate)); w.Code != http.StatusOK {
		t.Fatalf("admin token approve: %d %s", w.Code, w.Body.String())
	}
	assertHeld("admin-token approve", p2, "approved")

	// 3. AdminSetReviewStatus (token) and the internal review-status route (ML).
	p3 := it.seedOwned(t, "flagged")
	hold(p3)
	if w := adminServe(it.r, http.MethodPost, InternalAdminPrefix+"/posts/review-status", `{"post_id":"`+p3.String()+`","status":"approved","reason":"ok"}`, it.as(t, PermPostsModerate)); w.Code != http.StatusOK {
		t.Fatalf("admin review-status: %d %s", w.Code, w.Body.String())
	}
	assertHeld("AdminSetReviewStatus", p3, "approved")
	p3b := it.seedOwned(t, "flagged")
	hold(p3b)
	if w := adminServe(it.r, http.MethodPost, "/v1/posts/internal/review-status", `{"post_id":"`+p3b.String()+`","status":"approved"}`, edgeModerator(uuid.New())); w.Code != http.StatusOK {
		t.Fatalf("internal review-status: %d %s", w.Code, w.Body.String())
	}
	assertHeld("SetReviewStatusInternal", p3b, "approved")

	// 4. Author delete then restore.
	p4 := it.seedOwned(t, "approved")
	hold(p4)
	if w := adminServe(it.r, http.MethodDelete, "/v1/posts/"+p4.String(), "", it.ownerHeaders()); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := adminServe(it.r, http.MethodPost, "/v1/posts/"+p4.String()+"/restore", "", it.ownerHeaders()); w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
	assertHeld("RestorePost", p4, "approved")
	var deleted *time.Time
	_ = it.pool.QueryRow(context.Background(), `SELECT deleted_at FROM posts WHERE id=$1`, p4).Scan(&deleted)
	if deleted != nil {
		t.Fatal("restore did not undelete")
	}
	it.assertVisible(t, p4, it.author, true)
}

// assertSubjectAfterDecision: a base decision under the hold sets
// last_decision_id to THAT decision (never a restriction command id), the
// source stays copyright while the hold is active, and once the hold is
// released the source is the base decision's ("admin", then "appeal").
func (it *restrictionIT) assertSubjectAfterDecision(t *testing.T, post uuid.UUID) {
	t.Helper()
	read := func() store.ModerationSubject {
		w := adminServe(it.r, http.MethodGet, "/v1/posts/internal/moderation-subject/"+post.String(), "", withKey())
		if w.Code != http.StatusOK {
			t.Fatalf("subject: %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Data store.ModerationSubject `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return body.Data
	}
	adminDecision := uuid.New()
	if w := adminServe(it.r, http.MethodPost, "/v1/posts/"+post.String()+"/moderation",
		`{"decision_id":"`+adminDecision.String()+`","action":"reject","reason":"policy"}`, edgeModerator(uuid.New())); w.Code != http.StatusOK {
		t.Fatalf("admin reject: %d %s", w.Code, w.Body.String())
	}
	s := read()
	if s.LastDecisionID == nil || *s.LastDecisionID != adminDecision || s.LastDecisionSource != "copyright" || s.BaseReviewStatus != "rejected" || s.EffectiveReviewStatus != "restricted" {
		t.Fatalf("after admin reject under hold: %+v", s)
	}
	caseID := s.ActiveRestrictions[0].CaseID
	if w := it.post(t, it.command(t, post, caseID, "release_hold", "active", 2)); w.Code != http.StatusOK {
		t.Fatalf("release: %d %s", w.Code, w.Body.String())
	}
	s = read()
	if s.LastDecisionID == nil || *s.LastDecisionID != adminDecision || s.LastDecisionSource != "admin" || s.LatestBaseDecisionSource != "admin" || len(s.ActiveRestrictions) != 0 {
		t.Fatalf("after release: %+v", s)
	}
	// An appeal overturn (source appeal, revision-checked) becomes the last decision.
	appealDecision := uuid.New()
	if _, err := store.New(it.pool).ModeratePost(context.Background(), store.ModeratePostInput{
		DecisionID: appealDecision, PostID: post, ActorID: uuid.New(), Action: "approve", Reason: "overturned",
		Source: "appeal", ExpectedRevision: s.SearchRev}); err != nil {
		t.Fatal(err)
	}
	s = read()
	if s.LastDecisionID == nil || *s.LastDecisionID != appealDecision || s.LastDecisionSource != "appeal" || s.BaseReviewStatus != "approved" || s.EffectiveReviewStatus != "approved" {
		t.Fatalf("after appeal overturn: %+v", s)
	}
	// Re-place the hold at a higher revision: copyright again, id unchanged.
	if w := it.post(t, it.command(t, post, caseID, "place_hold", "released", 3)); w.Code != http.StatusOK {
		t.Fatalf("re-place: %d %s", w.Code, w.Body.String())
	}
	s = read()
	if *s.LastDecisionID != appealDecision || s.LastDecisionSource != "copyright" {
		t.Fatalf("after re-place: %+v", s)
	}
}
