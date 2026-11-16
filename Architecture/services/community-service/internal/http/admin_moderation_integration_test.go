//go:build integration

// DB-backed suspend/unsuspend through the real route, service and store:
// every call writes one community_admin_audit row whose actor is the token's
// act claim, repeats are no-ops, and while suspended the community is out of
// Discover, closed to strangers and read-only for its members. TEST_PG_DSN on
// a "_test" database (community_it_test), CI only.
package http

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/community-service/internal/service"
	"github.com/atpost/community-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupModerationIT(t *testing.T) (*adminTokenRig, *pgxpool.Pool, *service.Service) {
	t.Helper()
	rg, pool := setupCommunityAdminIT(t)
	st := store.New(pool)
	svc := service.New(st, nil)
	h := New(svc).WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v).WithAdmin(st).WithModeration(svc)
	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)
	rg.r = r
	return rg, pool, svc
}

type suspensionRow struct {
	status string
	at     *time.Time
	by     *uuid.UUID
	code   *string
}

func readSuspension(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) suspensionRow {
	t.Helper()
	var r suspensionRow
	if err := pool.QueryRow(context.Background(),
		`SELECT status, suspended_at, suspended_by, suspension_reason_code FROM communities WHERE id = $1`, id).
		Scan(&r.status, &r.at, &r.by, &r.code); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCommunityModerationIT_SuspendIsAuditedIdempotentAndReadOnly(t *testing.T) {
	rg, pool, svc := setupModerationIT(t)
	ctx := context.Background()
	community := seedCommunity(t, pool)
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_id FROM communities WHERE id = $1`, community).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	member, stranger := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO community_members (community_id, user_id, role) VALUES ($1, $2, 'member')`, community, member); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM community_members WHERE community_id = $1`, community)
	})

	call := func(path, body, perm string) int {
		hdr := forgedEdge(uuid.New())
		hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceChat, []string{perm}, rg.actor.String())
		return adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/communities/"+community.String()+path, body, hdr).Code
	}

	// Refused before anything is written.
	if code := call("/suspend", `{"reason_code":"not-a-code"}`, PermCommunitiesModerate); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad reason code: status=%d, want 422", code)
	}
	if code := call("/suspend", `{"reason_code":"spam"}`, PermReportsAct); code != http.StatusForbidden {
		t.Fatalf("report permission: status=%d, want 403", code)
	}
	if actors, _ := communityAuditRows(t, pool, community); len(actors) != 0 {
		t.Fatalf("refusals wrote %d audit rows", len(actors))
	}

	// Suspend.
	if code := call("/suspend", `{"reason_code":"spam","note":"bot ring"}`, PermCommunitiesModerate); code != http.StatusOK {
		t.Fatalf("suspend: status=%d, want 200", code)
	}
	first := readSuspension(t, pool, community)
	if first.status != "suspended" || first.at == nil || first.by == nil || *first.by != rg.actor || first.code == nil || *first.code != "spam" {
		t.Fatalf("after suspend: %+v, want suspended by %s for spam", first, rg.actor)
	}
	// Repeat: no change, still audited, the first suspension kept.
	if code := call("/suspend", `{"reason_code":"harassment"}`, PermCommunitiesModerate); code != http.StatusOK {
		t.Fatalf("repeat suspend: status=%d, want 200", code)
	}
	again := readSuspension(t, pool, community)
	if again.status != "suspended" || !again.at.Equal(*first.at) || *again.code != "spam" {
		t.Fatalf("repeat suspend changed the row: %+v", again)
	}
	actors, actions := communityAuditRows(t, pool, community)
	if len(actors) != 2 || actors[0] != rg.actor || actions[0] != store.AuditCommunitySuspended || actions[1] != store.AuditCommunitySuspended {
		t.Fatalf("audit actors=%v actions=%v, want two suspensions by %s", actors, actions, rg.actor)
	}

	// Effects while suspended.
	if _, err := svc.GetCommunity(ctx, community, &stranger); !errors.Is(err, service.ErrCommunityNotFound) {
		t.Fatalf("stranger card: %v, want not found", err)
	}
	if _, err := svc.GetCommunity(ctx, community, nil); !errors.Is(err, service.ErrCommunityNotFound) {
		t.Fatalf("signed-out card: %v, want not found", err)
	}
	card, err := svc.GetCommunity(ctx, community, &member)
	if err != nil || card.Status != "suspended" || card.Suspension == nil || card.Suspension.ReasonCode != "spam" || !card.Suspension.ReadOnly {
		t.Fatalf("member card: %+v err=%v, want the suspension shown", card, err)
	}
	if _, err := svc.ListSpaces(ctx, community, &member); err != nil {
		t.Fatalf("member read: %v, want allowed", err)
	}
	if _, err := svc.GetCommunity(ctx, community, &owner); err != nil {
		t.Fatalf("owner card: %v", err)
	}
	if _, err := svc.CreateSpace(ctx, community, owner, service.CreateSpaceParams{Name: "x"}); !errors.Is(err, service.ErrCommunitySuspended) {
		t.Fatalf("owner write: %v, want ErrCommunitySuspended", err)
	}
	if err := svc.DeleteCommunity(ctx, community, owner); !errors.Is(err, service.ErrCommunitySuspended) {
		t.Fatalf("owner delete: %v, want ErrCommunitySuspended", err)
	}
	if _, _, err := svc.JoinCommunity(ctx, community, stranger, nil); !errors.Is(err, service.ErrCommunityNotFound) {
		t.Fatalf("join: %v, want not found", err)
	}
	listed, err := svc.DiscoverCommunities(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range listed {
		if c.ID == community {
			t.Fatal("a suspended community is listed in Discover")
		}
	}

	// Unsuspend restores everything.
	if code := call("/unsuspend", `{}`, PermCommunitiesModerate); code != http.StatusUnprocessableEntity {
		t.Fatalf("unsuspend without a note: status=%d, want 422", code)
	}
	if code := call("/unsuspend", `{"note":"appeal granted"}`, PermCommunitiesModerate); code != http.StatusOK {
		t.Fatalf("unsuspend: status=%d, want 200", code)
	}
	lifted := readSuspension(t, pool, community)
	if lifted.status != "active" || lifted.at != nil || lifted.by != nil || lifted.code != nil {
		t.Fatalf("after unsuspend: %+v, want active with the columns cleared", lifted)
	}
	if _, err := svc.GetCommunity(ctx, community, &stranger); err != nil {
		t.Fatalf("stranger card after unsuspend: %v", err)
	}
	if code := call("/unsuspend", `{"note":"again"}`, PermCommunitiesModerate); code != http.StatusOK {
		t.Fatalf("repeat unsuspend: status=%d, want 200", code)
	}
	if _, actions = communityAuditRows(t, pool, community); len(actions) != 4 || actions[2] != store.AuditCommunityUnsuspended || actions[3] != store.AuditCommunityUnsuspended {
		t.Fatalf("audit actions=%v, want two suspensions then two unsuspensions", actions)
	}

	// An archived community is never touched; missing is 404.
	if _, err := pool.Exec(ctx, `UPDATE communities SET status = 'archived' WHERE id = $1`, community); err != nil {
		t.Fatal(err)
	}
	if code := call("/suspend", `{"reason_code":"spam"}`, PermCommunitiesModerate); code != http.StatusConflict {
		t.Fatalf("suspend archived: status=%d, want 409", code)
	}
	if code := call("/unsuspend", `{"note":"x"}`, PermCommunitiesModerate); code != http.StatusConflict {
		t.Fatalf("unsuspend archived: status=%d, want 409", code)
	}
	if got := readSuspension(t, pool, community); got.status != "archived" {
		t.Fatalf("archived community moved to %s", got.status)
	}
	hdr := bearer(rg.mint(t, rg.admin, AudienceChat, []string{PermCommunitiesModerate}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/communities/"+uuid.NewString()+"/suspend", `{"reason_code":"spam"}`, hdr); w.Code != http.StatusNotFound {
		t.Fatalf("missing community: status=%d, want 404", w.Code)
	}
}
