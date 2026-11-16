//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The admin console's queries against live PostgreSQL (CI only), on the same
// schema and DSN as official_verification_integration_test.go:
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration -p 1 ./internal/store/ -run 'MarksAdmin' -v
//
// The CI database is shared by every test in the package, so lists are
// checked for the rows this test made (in order) and stats by deltas.

// inReviewApp stores an application and moves it to the review queue, with
// created_at pinned so the order is the test's, not the clock's.
func inReviewApp(t *testing.T, s *Store, user uuid.UUID, at time.Time) OrgApplication {
	t.Helper()
	ctx := context.Background()
	doc := uuid.New()
	a := OrgApplication{ID: uuid.New(), UserID: user, LegalName: "Acme Pvt Ltd", RegistrationKind: "gstin",
		RegistrationNumber: "GST-" + uuid.NewString(), OfficialDomain: "acme.example", SignatoryEmail: "a@acme.example",
		DocumentMediaID: &doc, EmailCodeHash: "h", EmailCodeExpiresAt: time.Now().Add(time.Hour), CreatedAt: at}
	if err := s.CreateOrgApplication(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveOrgApplicationToReview(ctx, a.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return a
}

// collect pages through a status, keeping only ids in want.
func collectApps(t *testing.T, s *Store, status string, want map[uuid.UUID]bool) []uuid.UUID {
	t.Helper()
	var got []uuid.UUID
	var after *Keyset
	for page := 0; page < 1000; page++ {
		rows, err := s.ListOrgApplicationsAdmin(context.Background(), status, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		n := len(rows)
		if n > 2 {
			rows = rows[:2]
		}
		for _, r := range rows {
			if r.Status != status {
				t.Fatalf("status %s listed a %s row", status, r.Status)
			}
			if want[r.ID] {
				got = append(got, r.ID)
			}
		}
		if n <= 2 {
			return got
		}
		last := rows[len(rows)-1]
		after = &Keyset{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	t.Fatal("paging never ended")
	return nil
}

func TestMarksAdmin_QueueOrderCursorAndDetail(t *testing.T) {
	s, seed := officialStore(t)
	ctx := context.Background()
	// Far in the past, so these sort before anything another test made now.
	base := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(time.Now().UnixNano()%1e6) * time.Second)
	var ids []uuid.UUID
	want := map[uuid.UUID]bool{}
	for i := 0; i < 5; i++ {
		u := seed("Applicant Person", true)
		a := inReviewApp(t, s, u, base.Add(time.Duration(i)*time.Minute))
		ids = append(ids, a.ID)
		want[a.ID] = true
	}
	got := collectApps(t, s, OrgAppInReview, want)
	if len(got) != len(ids) {
		t.Fatalf("paged %d of %d rows: %v", len(got), len(ids), got)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("in_review order = %v, want oldest first %v", got, ids)
		}
	}

	// Reject two: newest first in the rejected list.
	reviewer := uuid.New()
	now := time.Now().UTC()
	for _, id := range ids[:2] {
		if err := s.RejectOrgApplication(ctx, id, reviewer, "does not match", now); err != nil {
			t.Fatal(err)
		}
	}
	rej := collectApps(t, s, OrgAppRejected, map[uuid.UUID]bool{ids[0]: true, ids[1]: true})
	if len(rej) != 2 || rej[0] != ids[1] || rej[1] != ids[0] {
		t.Fatalf("rejected order = %v, want newest first [%s %s]", rej, ids[1], ids[0])
	}

	d, err := s.GetOrgApplicationAdmin(ctx, ids[0])
	if err != nil || d == nil {
		t.Fatalf("detail = %v %v", d, err)
	}
	if d.Status != OrgAppRejected || d.ReviewerID == nil || *d.ReviewerID != reviewer || d.ReviewReason != "does not match" ||
		d.CompletedAt == nil || d.DocumentMediaID == nil || d.Applicant.DisplayName != "Applicant Person" || !d.Applicant.IsPersonVerified() {
		t.Fatalf("detail = %+v", d)
	}
	if missing, err := s.GetOrgApplicationAdmin(ctx, uuid.New()); err != nil || missing != nil {
		t.Fatalf("unknown id = %v %v", missing, err)
	}
}

func TestMarksAdmin_StatsCountDeltas(t *testing.T) {
	s, seed := officialStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	since := now.Add(-30 * 24 * time.Hour)
	before, err := s.MarksStats(ctx, since, now)
	if err != nil {
		t.Fatal(err)
	}
	inReviewApp(t, s, seed("Stats Person", true), now)
	b := inReviewApp(t, s, seed("Stats Person Two", true), now)
	if err := s.RejectOrgApplication(ctx, b.ID, uuid.New(), "no", now); err != nil {
		t.Fatal(err)
	}
	invitee := seed("Stats Invitee", false)
	if err := s.CreateGovInvitation(ctx, GovInvitation{ID: uuid.New(), UserID: invitee, OfficialName: "Ministry", CodeHash: "hash-" + uuid.NewString(),
		ApproverOne: uuid.New(), ApproverTwo: uuid.New(), ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	after, err := s.MarksStats(ctx, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if after.InReview-before.InReview != 1 || after.RejectedSince-before.RejectedSince != 1 ||
		after.InvitationsPending-before.InvitationsPending != 1 {
		t.Fatalf("stats before %+v after %+v", before, after)
	}
}

func TestMarksAdmin_InvitationsListExpiredAndRevoke(t *testing.T) {
	s, seed := officialStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	live := GovInvitation{ID: uuid.New(), UserID: seed("Live Invitee", false), OfficialName: "Ministry of Live", CodeHash: "hash-" + uuid.NewString(),
		ApproverOne: uuid.New(), ApproverTwo: uuid.New(), Reason: "MoU", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	stale := GovInvitation{ID: uuid.New(), UserID: seed("Stale Invitee", false), OfficialName: "Ministry of Stale", CodeHash: "hash-" + uuid.NewString(),
		ApproverOne: uuid.New(), ApproverTwo: uuid.New(), ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-8 * 24 * time.Hour)}
	for _, g := range []GovInvitation{live, stale} {
		if err := s.CreateGovInvitation(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	has := func(status string, id uuid.UUID) *AdminGovInvitation {
		t.Helper()
		rows, err := s.ListGovInvitationsAdmin(ctx, status, 50, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			if rows[i].Status != status {
				t.Fatalf("%s listed a %s row", status, rows[i].Status)
			}
			if rows[i].ID == id {
				return &rows[i]
			}
		}
		return nil
	}
	if g := has(InvitationPending, live.ID); g == nil || g.ApproverOne != live.ApproverOne || g.Reason != "MoU" || g.Invitee.DisplayName != "Live Invitee" {
		t.Fatalf("pending = %+v", g)
	}
	if has(InvitationPending, stale.ID) != nil {
		t.Fatal("an expired invitation is listed as pending")
	}
	if has(InvitationExpired, stale.ID) == nil {
		t.Fatal("the expired invitation is not listed as expired")
	}
	if _, err := s.ListGovInvitationsAdmin(ctx, "approved", 10, nil, now); err == nil {
		t.Fatal("an unknown status was accepted")
	}

	revoker := uuid.New()
	if err := s.RevokeGovInvitation(ctx, stale.ID, revoker, "late", now); !errors.Is(err, ErrInvitationNotPending) {
		t.Fatalf("revoke expired = %v", err)
	}
	if err := s.RevokeGovInvitation(ctx, uuid.New(), revoker, "who", now); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("revoke unknown = %v", err)
	}
	if err := s.RevokeGovInvitation(ctx, live.ID, revoker, "wrong account", now); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeGovInvitation(ctx, live.ID, revoker, "again", now); !errors.Is(err, ErrInvitationNotPending) {
		t.Fatalf("revoke twice = %v", err)
	}
	g, err := s.GetGovInvitationAdmin(ctx, live.ID, now)
	if err != nil || g == nil || g.Status != InvitationRevoked {
		t.Fatalf("after revoke = %+v %v", g, err)
	}
	if has(InvitationRevoked, live.ID) == nil {
		t.Fatal("the revoked invitation is not listed as revoked")
	}
	var by uuid.UUID
	var reason string
	if err := s.db.QueryRow(ctx, `SELECT revoked_by, revoke_reason FROM profile.government_invitations WHERE id = $1`, live.ID).Scan(&by, &reason); err != nil {
		t.Fatal(err)
	}
	if by != revoker || reason != "wrong account" {
		t.Fatalf("revoke recorded %s %q", by, reason)
	}
	// The revoked code no longer accepts.
	if _, err := s.AcceptGovInvitation(ctx, live.UserID, live.CodeHash, now); !errors.Is(err, ErrInvitationGone) {
		t.Fatalf("accept after revoke = %v", err)
	}
}

func TestMarksAdmin_InvitationsCursorNewestFirst(t *testing.T) {
	s, seed := officialStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	base := time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(now.UnixNano()%1e6) * time.Second)
	want := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		g := GovInvitation{ID: uuid.New(), UserID: seed("Cursor Invitee", false), OfficialName: "Ministry", CodeHash: "hash-" + uuid.NewString(),
			ApproverOne: uuid.New(), ApproverTwo: uuid.New(), ExpiresAt: now.Add(time.Hour), CreatedAt: base.Add(time.Duration(i) * time.Minute)}
		if err := s.CreateGovInvitation(ctx, g); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, g.ID)
		want[g.ID] = true
	}
	var got []uuid.UUID
	var after *Keyset
	for page := 0; page < 1000; page++ {
		rows, err := s.ListGovInvitationsAdmin(ctx, InvitationPending, 1, after, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		if want[rows[0].ID] {
			got = append(got, rows[0].ID)
		}
		if len(rows) < 2 {
			break
		}
		after = &Keyset{CreatedAt: rows[0].CreatedAt, ID: rows[0].ID}
	}
	if len(got) != 3 || got[0] != ids[2] || got[1] != ids[1] || got[2] != ids[0] {
		t.Fatalf("order = %v, want newest first %v", got, []uuid.UUID{ids[2], ids[1], ids[0]})
	}
}
