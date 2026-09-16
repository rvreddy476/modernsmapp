//go:build integration

// Admin-service token integration tests (admin console Wave 2 — Content) on
// the real schema (M7_POSTGRES_DSN, a *_test database: post_it_test). Every
// audited write on the token path writes exactly one row whose actor is the
// signed act claim, never the forged X-User-Id riding along; a refused write
// writes nothing. The LEGACY moderator routes audit their header actor. Stats
// are proven as deltas over rows this test seeds.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/post-service/internal/service"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminIT struct {
	pool  *pgxpool.Pool
	r     *gin.Engine
	admin *servicetoken.Signer
	actor uuid.UUID
}

func newAdminIT(t *testing.T) *adminIT {
	t.Helper()
	pool := openReviewAuditDB(t)
	v, aPriv, _, _ := adminTestVerifier(t)
	signer, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", aPriv)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(store.New(pool), nil, nil), nil).WithInternalKey(adminTestKey).WithServiceAuth(v)
	h.RegisterRoutes(r)
	h.RegisterReelDiscoveryRoutes(r)
	h.RegisterReportRoutes(r)
	return &adminIT{pool: pool, r: r, admin: signer, actor: uuid.New()}
}

// as returns headers for a token with scope, plus a forged edge identity and
// the key: the actor must still come out as act.
func (it *adminIT) as(t *testing.T, scope ...string) map[string]string {
	t.Helper()
	tok, err := it.admin.Mint(AudiencePost, "admin-console", scope, nil, 60e9, servicetoken.WithActor(it.actor.String()))
	if err != nil {
		t.Fatal(err)
	}
	hdr := edgeModerator(uuid.New())
	hdr[ServiceAuthHeader] = "Bearer " + tok
	return hdr
}

func (it *adminIT) seedPost(t *testing.T, contentType, visibility, reviewStatus string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := it.pool.Exec(context.Background(), `
		INSERT INTO posts (id,author_id,text,visibility,content_type,review_status,search_rev,created_at,updated_at)
		VALUES ($1,$2,'admin console proof',$3,$4,$5,1,NOW(),NOW())
	`, id, uuid.New(), visibility, contentType, reviewStatus); err != nil {
		t.Fatal(err)
	}
	return id
}

func (it *adminIT) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := it.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (it *adminIT) decisionActors(t *testing.T, postID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := store.New(it.pool).ListModerationDecisions(context.Background(), postID)
	if err != nil {
		t.Fatal(err)
	}
	var out []uuid.UUID
	for _, d := range rows {
		out = append(out, d.ActorID)
	}
	return out
}

func allActors(got []uuid.UUID, want uuid.UUID) bool {
	for _, a := range got {
		if a != want {
			return false
		}
	}
	return true
}

func decisionBody(action string) string {
	return `{"decision_id":"` + uuid.NewString() + `","action":"` + action + `","reason":"admin console proof"}`
}

func TestAdminTokenIT_PostDecisionNarrowsAndAuditsAct(t *testing.T) {
	it := newAdminIT(t)
	postID := it.seedPost(t, "post", "public", "flagged")
	path := InternalAdminPrefix + "/posts/" + postID.String() + "/moderation"

	if w := adminServe(it.r, http.MethodPost, path, decisionBody("reject"), it.as(t, PermPostsModerate)); w.Code != http.StatusForbidden {
		t.Fatalf("reject with moderate: status=%d body=%s, want 403", w.Code, w.Body.String())
	}
	if n := len(it.decisionActors(t, postID)); n != 0 {
		t.Fatalf("refused reject wrote %d decisions", n)
	}
	if w := adminServe(it.r, http.MethodPost, path, decisionBody("approve"), it.as(t, PermPostsModerate)); w.Code != http.StatusOK {
		t.Fatalf("approve with moderate: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := adminServe(it.r, http.MethodPost, path, decisionBody("reject"), it.as(t, PermPostsRemove)); w.Code != http.StatusOK {
		t.Fatalf("reject with remove: status=%d body=%s", w.Code, w.Body.String())
	}
	if actors := it.decisionActors(t, postID); len(actors) != 2 || !allActors(actors, it.actor) {
		t.Fatalf("decision actors=%v, want two rows by act %s", actors, it.actor)
	}

	// A video is Tube: Social's remove does not reach it.
	videoID := it.seedPost(t, "long_video", "public", "flagged")
	vpath := InternalAdminPrefix + "/posts/" + videoID.String() + "/moderation"
	if w := adminServe(it.r, http.MethodPost, vpath, decisionBody("reject"), it.as(t, PermPostsRemove, PermReelsRemove)); w.Code != http.StatusForbidden {
		t.Fatalf("video reject with social remove: status=%d, want 403", w.Code)
	}
	if w := adminServe(it.r, http.MethodPost, vpath, decisionBody("reject"), it.as(t, PermVideosRemove)); w.Code != http.StatusOK {
		t.Fatalf("video reject with tube remove: status=%d body=%s", w.Code, w.Body.String())
	}
	if actors := it.decisionActors(t, videoID); len(actors) != 1 || actors[0] != it.actor {
		t.Fatalf("video decision actors=%v, want [act]", actors)
	}

	// Missing post: 404, nothing written.
	missing := InternalAdminPrefix + "/posts/" + uuid.NewString() + "/moderation"
	if w := adminServe(it.r, http.MethodPost, missing, decisionBody("approve"), it.as(t, PermPostsModerate)); w.Code != http.StatusNotFound {
		t.Fatalf("missing post: status=%d, want 404", w.Code)
	}

	// The history route reads both rows back.
	hw := adminServe(it.r, http.MethodGet, InternalAdminPrefix+"/posts/"+postID.String()+"/moderation-history", "", it.as(t, PermPostsModerate))
	if hw.Code != http.StatusOK {
		t.Fatalf("history: status=%d body=%s", hw.Code, hw.Body.String())
	}
	var hist struct {
		Data service.PostModerationHistory `json:"data"`
	}
	if err := json.Unmarshal(hw.Body.Bytes(), &hist); err != nil || len(hist.Data.Decisions) != 2 || hist.Data.ReviewStatus != "rejected" {
		t.Fatalf("history body=%s", hw.Body.String())
	}
}

func TestAdminTokenIT_ReviewStatusAndVisibilityAuditAct(t *testing.T) {
	it := newAdminIT(t)
	reelID := it.seedPost(t, "flick", "public", "flagged")
	body := `{"post_id":"` + reelID.String() + `","status":"rejected","reason":"admin console proof"}`
	if w := adminServe(it.r, http.MethodPost, InternalAdminPrefix+"/posts/review-status", body, it.as(t, PermReelsModerate)); w.Code != http.StatusForbidden {
		t.Fatalf("reel reject with moderate: status=%d body=%s, want 403", w.Code, w.Body.String())
	}
	if rows := auditRows(t, it.pool, reelID); len(rows) != 0 {
		t.Fatalf("refused change audited %d rows", len(rows))
	}
	if w := adminServe(it.r, http.MethodPost, InternalAdminPrefix+"/posts/review-status", body, it.as(t, PermReelsRemove)); w.Code != http.StatusOK {
		t.Fatalf("reel reject with remove: status=%d body=%s", w.Code, w.Body.String())
	}
	rows := auditRows(t, it.pool, reelID)
	if len(rows) != 1 || rows[0].ActorType != "user" || rows[0].ActorUserID == nil || *rows[0].ActorUserID != it.actor ||
		rows[0].Field != "review_status" || rows[0].NewValue != "rejected" {
		t.Fatalf("review audit=%+v, want one user row by act %s", rows, it.actor)
	}

	videoID := it.seedPost(t, "long_video", "staged", "approved")
	vbody := `{"post_id":"` + videoID.String() + `","visibility":"public"}`
	if w := adminServe(it.r, http.MethodPost, InternalAdminPrefix+"/posts/visibility", vbody, it.as(t, PermPostsModerate)); w.Code != http.StatusForbidden {
		t.Fatalf("video visibility with social moderate: status=%d, want 403", w.Code)
	}
	if w := adminServe(it.r, http.MethodPost, InternalAdminPrefix+"/posts/visibility", vbody, it.as(t, PermVideosModerate)); w.Code != http.StatusOK {
		t.Fatalf("video visibility with tube moderate: status=%d body=%s", w.Code, w.Body.String())
	}
	rows = auditRows(t, it.pool, videoID)
	if len(rows) != 1 || rows[0].ActorUserID == nil || *rows[0].ActorUserID != it.actor || rows[0].Field != "visibility" {
		t.Fatalf("visibility audit=%+v, want one row by act", rows)
	}
	if vis, _ := postState(t, it.pool, videoID); vis != "public" {
		t.Fatalf("visibility=%s, want public", vis)
	}
}

func (it *adminIT) seedReport(t *testing.T, targetType string, targetID uuid.UUID) uuid.UUID {
	t.Helper()
	r := &store.ContentReport{ReporterID: uuid.New(), TargetType: targetType, TargetID: targetID, Reason: "spam"}
	if err := store.New(it.pool).InsertContentReport(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func (it *adminIT) adminAudit(t *testing.T, targetType string, id uuid.UUID) []store.AdminAuditEntry {
	t.Helper()
	rows, err := store.New(it.pool).ListAdminAudit(context.Background(), targetType, id)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAdminTokenIT_ReportReviewAuditsAct(t *testing.T) {
	it := newAdminIT(t)
	reportID := it.seedReport(t, "video", uuid.New())
	path := InternalAdminPrefix + "/reports/" + reportID.String()
	body := `{"status":"resolved","review_note":"handled"}`
	if w := adminServe(it.r, http.MethodPatch, path, body, it.as(t, PermSocialReportsAct)); w.Code != http.StatusForbidden {
		t.Fatalf("video report with social: status=%d body=%s, want 403", w.Code, w.Body.String())
	}
	if rows := it.adminAudit(t, "content_report", reportID); len(rows) != 0 {
		t.Fatalf("refused review audited %d rows", len(rows))
	}
	if w := adminServe(it.r, http.MethodPatch, path, body, it.as(t, PermTubeReportsAct)); w.Code != http.StatusOK {
		t.Fatalf("video report with tube: status=%d body=%s", w.Code, w.Body.String())
	}
	rows := it.adminAudit(t, "content_report", reportID)
	if len(rows) != 1 || rows[0].ActorUserID != it.actor || rows[0].PreviousValue != "pending" || rows[0].NewValue != "resolved" {
		t.Fatalf("report audit=%+v, want one row by act", rows)
	}
	var reviewer string
	if err := it.pool.QueryRow(context.Background(), `SELECT reviewer_id FROM content_reports WHERE id=$1`, reportID).Scan(&reviewer); err != nil || reviewer != it.actor.String() {
		t.Fatalf("reviewer_id=%q err=%v, want act", reviewer, err)
	}
	if w := adminServe(it.r, http.MethodPatch, InternalAdminPrefix+"/reports/"+uuid.NewString(), body, it.as(t, PermTubeReportsAct)); w.Code != http.StatusNotFound {
		t.Fatalf("missing report: status=%d, want 404", w.Code)
	}

	// Listing is confined to the token's kinds.
	lw := adminServe(it.r, http.MethodGet, InternalAdminPrefix+"/reports?limit=100", "", it.as(t, PermSocialReportsAct))
	var list struct {
		Data []store.ContentReport `json:"data"`
	}
	if lw.Code != http.StatusOK || json.Unmarshal(lw.Body.Bytes(), &list) != nil {
		t.Fatalf("list: status=%d body=%s", lw.Code, lw.Body.String())
	}
	for _, rep := range list.Data {
		if rep.TargetType == "video" {
			t.Fatalf("social token listed a video report %s", rep.ID)
		}
	}
}

func (it *adminIT) seedComment(t *testing.T, postID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := it.pool.Exec(context.Background(),
		`INSERT INTO comments (id, post_id, author_id, body) VALUES ($1, $2, $3, 'admin console proof')`,
		id, postID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAdminTokenIT_CommentModerationAuditsAct(t *testing.T) {
	it := newAdminIT(t)
	commentID := it.seedComment(t, it.seedPost(t, "post", "public", "approved"))
	path := InternalAdminPrefix + "/comments/" + commentID.String() + "/moderation"
	if w := adminServe(it.r, http.MethodPatch, path, `{"status":"removed"}`, it.as(t, PermCommentsModerate)); w.Code != http.StatusForbidden {
		t.Fatalf("remove with moderate: status=%d, want 403", w.Code)
	}
	if w := adminServe(it.r, http.MethodPatch, path, `{"status":"removed"}`, it.as(t, PermCommentsRemove)); w.Code != http.StatusOK {
		t.Fatalf("remove with remove: status=%d body=%s", w.Code, w.Body.String())
	}
	rows := it.adminAudit(t, "comment", commentID)
	if len(rows) != 1 || rows[0].ActorUserID != it.actor || rows[0].PreviousValue != "visible" || rows[0].NewValue != "removed" {
		t.Fatalf("comment audit=%+v, want one row by act", rows)
	}
	if w := adminServe(it.r, http.MethodPatch, InternalAdminPrefix+"/comments/"+uuid.NewString()+"/moderation", `{"status":"visible"}`, it.as(t, PermCommentsModerate)); w.Code != http.StatusNotFound {
		t.Fatalf("missing comment: status=%d, want 404", w.Code)
	}
	aw := adminServe(it.r, http.MethodGet, InternalAdminPrefix+"/comments/"+commentID.String()+"/audit", "", it.as(t, PermCommentsModerate))
	if aw.Code != http.StatusOK {
		t.Fatalf("comment audit route: status=%d", aw.Code)
	}
}

// The LEGACY moderator routes keep working and now audit their header actor.
func TestAdminTokenIT_LegacyRoutesAuditModerator(t *testing.T) {
	it := newAdminIT(t)
	mod := uuid.New()
	commentID := it.seedComment(t, it.seedPost(t, "post", "public", "approved"))
	if w := adminServe(it.r, http.MethodPatch, "/v1/admin/comments/"+commentID.String()+"/moderation", `{"status":"hidden"}`, edgeModerator(mod)); w.Code != http.StatusOK {
		t.Fatalf("legacy comment: status=%d body=%s", w.Code, w.Body.String())
	}
	if rows := it.adminAudit(t, "comment", commentID); len(rows) != 1 || rows[0].ActorUserID != mod {
		t.Fatalf("legacy comment audit=%+v, want one row by moderator", rows)
	}
	reportID := it.seedReport(t, "post", uuid.New())
	if w := adminServe(it.r, http.MethodPatch, "/v1/admin/reports/"+reportID.String(), `{"status":"dismissed"}`, edgeModerator(mod)); w.Code != http.StatusOK {
		t.Fatalf("legacy report: status=%d body=%s", w.Code, w.Body.String())
	}
	if rows := it.adminAudit(t, "content_report", reportID); len(rows) != 1 || rows[0].ActorUserID != mod {
		t.Fatalf("legacy report audit=%+v, want one row by moderator", rows)
	}
}

func (it *adminIT) socialStats(t *testing.T) store.SocialAdminStats {
	t.Helper()
	w := adminServe(it.r, http.MethodGet, InternalAdminPrefix+"/stats", "", it.as(t, PermSocialStatsRead))
	var out struct {
		Data store.SocialAdminStats `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("social stats: status=%d body=%s", w.Code, w.Body.String())
	}
	return out.Data
}

func (it *adminIT) tubeStats(t *testing.T) store.TubeAdminStats {
	t.Helper()
	w := adminServe(it.r, http.MethodGet, InternalAdminPrefix+"/tube/stats", "", it.as(t, PermTubeStatsRead))
	var out struct {
		Data store.TubeAdminStats `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("tube stats: status=%d body=%s", w.Code, w.Body.String())
	}
	return out.Data
}

func TestAdminTokenIT_StatsCountSeededRows(t *testing.T) {
	it := newAdminIT(t)
	s0, t0 := it.socialStats(t), it.tubeStats(t)

	flaggedPost := it.seedPost(t, "post", "public", "flagged")
	it.seedPost(t, "poll", "staged", "approved")
	takedown := it.seedPost(t, "post", "public", "approved")
	flaggedReel := it.seedPost(t, "flick", "public", "flagged")
	it.seedPost(t, "long_video", "public", "flagged")
	it.seedPost(t, "video", "staged", "approved")
	it.seedReport(t, "post", flaggedPost)
	it.seedReport(t, "video", uuid.New())
	if _, err := it.pool.Exec(context.Background(),
		`INSERT INTO moderation_reviews (reel_id, reviewer_type, decision) VALUES ($1, 'auto', 'flagged')`, flaggedReel); err != nil {
		t.Fatal(err)
	}
	if w := adminServe(it.r, http.MethodPost, InternalAdminPrefix+"/posts/"+takedown.String()+"/moderation", decisionBody("reject"), it.as(t, PermPostsRemove)); w.Code != http.StatusOK {
		t.Fatalf("takedown: status=%d body=%s", w.Code, w.Body.String())
	}
	commentID := it.seedComment(t, flaggedPost)
	if w := adminServe(it.r, http.MethodPatch, InternalAdminPrefix+"/comments/"+commentID.String()+"/moderation", `{"status":"removed"}`, it.as(t, PermCommentsRemove)); w.Code != http.StatusOK {
		t.Fatalf("comment takedown: status=%d", w.Code)
	}

	s1, t1 := it.socialStats(t), it.tubeStats(t)
	social := map[string][2]int{
		"flagged_posts_pending":            {s1.FlaggedPostsPending - s0.FlaggedPostsPending, 1},
		"flagged_reels_pending":            {s1.FlaggedReelsPending - s0.FlaggedReelsPending, 1},
		"reel_review_queue_pending":        {s1.ReelReviewQueuePending - s0.ReelReviewQueuePending, 1},
		"open_content_reports":             {s1.OpenContentReports - s0.OpenContentReports, 1},
		"posts_created_today":              {s1.PostsCreatedToday - s0.PostsCreatedToday, 3},
		"posts_created_last_7_days":        {s1.PostsCreatedLast7Days - s0.PostsCreatedLast7Days, 3},
		"reels_created_today":              {s1.ReelsCreatedToday - s0.ReelsCreatedToday, 1},
		"reels_created_last_7_days":        {s1.ReelsCreatedLast7Days - s0.ReelsCreatedLast7Days, 1},
		"post_takedowns_last_7_days":       {s1.PostTakedownsLast7Days - s0.PostTakedownsLast7Days, 1},
		"reel_takedowns_last_7_days":       {s1.ReelTakedownsLast7Days - s0.ReelTakedownsLast7Days, 0},
		"comment_takedowns_last_7_days":    {s1.CommentTakedownsLast7Days - s0.CommentTakedownsLast7Days, 1},
		"staged_posts_awaiting_visibility": {s1.StagedPostsAwaitingVisibility - s0.StagedPostsAwaitingVisibility, 1},
	}
	for name, got := range social {
		if got[0] != got[1] {
			t.Errorf("social %s delta=%d, want %d", name, got[0], got[1])
		}
	}
	tube := map[string][2]int{
		"flagged_videos_pending":            {t1.FlaggedVideosPending - t0.FlaggedVideosPending, 1},
		"open_video_reports":                {t1.OpenVideoReports - t0.OpenVideoReports, 1},
		"videos_created_today":              {t1.VideosCreatedToday - t0.VideosCreatedToday, 2},
		"videos_created_last_7_days":        {t1.VideosCreatedLast7Days - t0.VideosCreatedLast7Days, 2},
		"video_takedowns_last_7_days":       {t1.VideoTakedownsLast7Days - t0.VideoTakedownsLast7Days, 0},
		"staged_videos_awaiting_visibility": {t1.StagedVideosAwaitingVisibility - t0.StagedVideosAwaitingVisibility, 1},
	}
	for name, got := range tube {
		if got[0] != got[1] {
			t.Errorf("tube %s delta=%d, want %d", name, got[0], got[1])
		}
	}
	if s1.DayStartsAt.IsZero() || s1.DayStartsAt.After(s1.GeneratedAt) {
		t.Errorf("day_starts_at=%s generated_at=%s", s1.DayStartsAt, s1.GeneratedAt)
	}
}
