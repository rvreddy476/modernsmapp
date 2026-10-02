package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Renewing an offline copy (2026-10-02).
//
// A client renews a copy whenever the device is online by repeating the
// grant with the same device_id. A renewal IS the grant: every rule runs
// again, and nothing about already holding a copy makes it easier. Same rig
// as offline_copies_test.go.

func TestOfflineRenewRestartsThirtyDaysFromNow(t *testing.T) {
	r := newOfflineRig(t)
	r.mustGrant(t, r.viewer)
	grantedAt := r.row(r.viewer).GrantedAt
	// 29 days in: one day left on the copy.
	r.now = r.now.Add(29 * 24 * time.Hour)
	card, created, err := r.grant(r.viewer)
	if err != nil || created {
		t.Fatalf("renewal: created=%v err=%v (want the 200 of a refresh)", created, err)
	}
	// Thirty days, written out: the number in the contract, counted from
	// the renewal and not from the first grant.
	want := r.now.Add(720 * time.Hour)
	if !card.ExpiresAt.Equal(want) {
		t.Fatalf("card expires_at = %v, want %v", card.ExpiresAt, want)
	}
	row := r.row(r.viewer)
	if !row.ExpiresAt.Equal(want) || !row.GrantedAt.Equal(grantedAt) || row.RevokedAt != nil ||
		row.LastCheckedAt == nil || !row.LastCheckedAt.Equal(r.now) {
		t.Fatalf("row after a renewal = %+v", row)
	}
	if items := r.check(t, r.viewer, r.post.String()); !items[0].Valid || !items[0].ExpiresAt.Equal(want) {
		t.Fatalf("check after a renewal = %+v", items)
	}
}

func TestOfflineRenewDoesNotCountAgainstTheLimit(t *testing.T) {
	r := newOfflineRig(t)
	r.mustGrant(t, r.viewer)
	// 99 more: the user is at 100 active copies.
	for i := 0; i < 99; i++ {
		other := uuid.New()
		r.store.rows[offlineKey{r.viewer, other, offlineDevice}] = &postgres.OfflineCopy{
			UserID: r.viewer, PostID: other, DeviceID: offlineDevice, GrantedAt: r.now, ExpiresAt: r.now.Add(24 * time.Hour)}
	}
	r.now = r.now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		if _, created, err := r.grant(r.viewer); err != nil || created {
			t.Fatalf("renewal %d at the limit: created=%v err=%v", i, created, err)
		}
	}
	if len(r.store.rows) != 100 {
		t.Fatalf("rows = %d, want 100 (a renewal is not a new copy)", len(r.store.rows))
	}
	// A new copy is still refused.
	_, _, err := r.svc.GrantOfflineCopy(context.Background(), r.viewer, r.post, "device-tablet-0003")
	wantOfflineErr(t, err, ErrOfflineLimit, 409, "OFFLINE_LIMIT")
}

func TestOfflineRenewReappliesEveryRule(t *testing.T) {
	tier := uuid.New()
	cases := []struct {
		name   string
		mutate func(r *offlineRig)
		want   error
		status int
		code   string
	}{
		{"downloads turned off", func(r *offlineRig) { r.p().AllowDownload = false }, ErrOfflineNotAllowed, 403, "OFFLINE_NOT_ALLOWED"},
		{"made members-only", func(r *offlineRig) { r.p().TierRequiredID = &tier }, ErrOfflineNotAllowed, 403, "OFFLINE_NOT_ALLOWED"},
		{"made private", func(r *offlineRig) { r.p().Visibility = "private" }, ErrPostNotFound, 404, "NOT_FOUND"},
		{"author blocked the viewer", func(r *offlineRig) { r.rels[r.author.String()] = ViewerRelationship{BlockedBy: true} }, ErrPostNotFound, 404, "NOT_FOUND"},
		{"deleted", func(r *offlineRig) { r.p().DeletedAt = dayp(2026, 10, 2) }, ErrPostNotFound, 404, "NOT_FOUND"},
		{"taken back for review", func(r *offlineRig) { r.p().ReviewStatus = "pending" }, ErrPostNotFound, 404, "NOT_FOUND"},
		{"the ladder is gone", func(r *offlineRig) { r.media.records[r.video].Variants = nil }, ErrOfflineNotReady, 409, "NOT_READY"},
		{"members-only, monetization down", func(r *offlineRig) { r.p().TierRequiredID = &tier; r.entErr = errors.New("down") },
			ErrOfflineUnavailable, 503, "DEPENDENCY_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newOfflineRig(t)
			r.mustGrant(t, r.viewer)
			before := *r.row(r.viewer)
			r.now = r.now.Add(5 * 24 * time.Hour)
			tc.mutate(r)
			card, _, err := r.grant(r.viewer)
			if card != nil {
				t.Fatalf("a refused renewal handed out a card: %+v", card)
			}
			wantOfflineErr(t, err, tc.want, tc.status, tc.code)
			// A refused renewal extends nothing.
			after := r.row(r.viewer)
			if !after.ExpiresAt.Equal(before.ExpiresAt) || !after.GrantedAt.Equal(before.GrantedAt) {
				t.Fatalf("a refused renewal moved the row: %+v -> %+v", before, after)
			}
		})
	}
}

// The exact case a client will meet: the creator turned downloads off, the
// copy was revoked, and the app tries to renew it anyway.
func TestOfflineRenewOfACopyThePostNoLongerAllowsIsRefusedAndStaysRevoked(t *testing.T) {
	r := newOfflineRig(t)
	r.mustGrant(t, r.viewer)
	r.p().AllowDownload = false
	if items := r.check(t, r.viewer, r.post.String()); items[0].Valid || items[0].Reason != "not_allowed" || items[0].Renewable != nil {
		t.Fatalf("check = %+v", items)
	}
	revoked := *r.row(r.viewer)
	if revoked.RevokedAt == nil || revoked.RevokeReason != postgres.OfflineRevokeNotAllowed {
		t.Fatalf("the check did not revoke the copy: %+v", revoked)
	}
	r.now = r.now.Add(time.Hour)
	_, _, err := r.grant(r.viewer)
	wantOfflineErr(t, err, ErrOfflineNotAllowed, 403, "OFFLINE_NOT_ALLOWED")
	after := r.row(r.viewer)
	if after.RevokedAt == nil || !after.RevokedAt.Equal(*revoked.RevokedAt) || after.RevokeReason != postgres.OfflineRevokeNotAllowed ||
		!after.ExpiresAt.Equal(revoked.ExpiresAt) {
		t.Fatalf("a refused renewal changed a revoked row: %+v -> %+v", revoked, after)
	}
	if items := r.check(t, r.viewer, r.post.String()); items[0].Valid || items[0].Reason != "not_allowed" {
		t.Fatalf("check after the refused renewal = %+v", items)
	}
}

func TestOfflineRevokedOrExpiredCopyCanBeGrantedAgain(t *testing.T) {
	t.Run("revoked, and the rules allow it again", func(t *testing.T) {
		r := newOfflineRig(t)
		r.mustGrant(t, r.viewer)
		r.p().AllowDownload = false
		r.check(t, r.viewer, r.post.String()) // revokes: not_allowed
		r.p().AllowDownload = true
		r.now = r.now.Add(2 * 24 * time.Hour)
		card, created, err := r.grant(r.viewer)
		if err != nil || !created {
			t.Fatalf("grant of a revoked copy: created=%v err=%v (want 201: it was not active)", created, err)
		}
		row := r.row(r.viewer)
		if row.RevokedAt != nil || row.RevokeReason != "" || !row.GrantedAt.Equal(r.now) || !row.ExpiresAt.Equal(r.now.Add(720*time.Hour)) ||
			!card.ExpiresAt.Equal(row.ExpiresAt) {
			t.Fatalf("row = %+v card = %+v", row, card)
		}
		if items := r.check(t, r.viewer, r.post.String()); !items[0].Valid {
			t.Fatalf("check = %+v", items)
		}
	})
	t.Run("removed by the viewer", func(t *testing.T) {
		r := newOfflineRig(t)
		r.mustGrant(t, r.viewer)
		if _, err := r.svc.RemoveOfflineCopy(context.Background(), r.viewer, r.post, offlineDevice); err != nil {
			t.Fatal(err)
		}
		if _, created, err := r.grant(r.viewer); err != nil || !created || r.row(r.viewer).RevokedAt != nil {
			t.Fatalf("grant of a removed copy: created=%v err=%v row=%+v", created, err, r.row(r.viewer))
		}
	})
	t.Run("expired", func(t *testing.T) {
		r := newOfflineRig(t)
		r.mustGrant(t, r.viewer)
		r.now = r.now.Add(31 * 24 * time.Hour)
		if items := r.check(t, r.viewer, r.post.String()); items[0].Valid || items[0].Reason != "expired" {
			t.Fatalf("check = %+v", items)
		}
		card, created, err := r.grant(r.viewer)
		if err != nil || !created {
			t.Fatalf("grant of an expired copy: created=%v err=%v", created, err)
		}
		if row := r.row(r.viewer); !row.GrantedAt.Equal(r.now) || !row.ExpiresAt.Equal(r.now.Add(720*time.Hour)) || !card.ExpiresAt.Equal(row.ExpiresAt) {
			t.Fatalf("row = %+v", row)
		}
	})
	t.Run("revoked, and the rules still refuse", func(t *testing.T) {
		r := newOfflineRig(t)
		r.mustGrant(t, r.viewer)
		r.rels[r.author.String()] = ViewerRelationship{Blocked: true}
		r.check(t, r.viewer, r.post.String()) // revokes: blocked
		_, _, err := r.grant(r.viewer)
		wantOfflineErr(t, err, ErrPostNotFound, 404, "NOT_FOUND")
		if row := r.row(r.viewer); row.RevokedAt == nil {
			t.Fatalf("a refused grant un-revoked the copy: %+v", row)
		}
	})
}

// `renewable` on the check: on valid items only, true exactly when repeating
// the grant would succeed.
func TestOfflineCheckSaysWhetherACopyIsRenewable(t *testing.T) {
	r := newOfflineRig(t)
	r.mustGrant(t, r.viewer)
	r.mustGrant(t, r.author)
	renewable := func(viewer uuid.UUID) bool {
		t.Helper()
		items := r.check(t, viewer, r.post.String())
		if !items[0].Valid {
			t.Fatalf("items = %+v, want a valid copy", items)
		}
		if items[0].Renewable == nil {
			t.Fatalf("a valid item carries no renewable: %+v", items[0])
		}
		return *items[0].Renewable
	}
	agrees := func(viewer uuid.UUID, want bool) {
		t.Helper()
		got := renewable(viewer)
		_, _, err := r.grant(viewer)
		if got != want || (err == nil) != want {
			t.Fatalf("renewable=%v, grant err=%v, want renewable=%v and the grant to agree", got, err, want)
		}
	}
	agrees(r.viewer, true)
	agrees(r.author, true)

	// The owner's own post, scheduled again: their copy is still valid (the
	// owner's always is) but a renewal would answer 409.
	r.p().PublishAt = dayp(2030, 1, 1)
	agrees(r.author, false)
	r.p().PublishAt = nil

	// The owner's own post, back in the pipeline.
	m := r.states[r.video]
	m.ProcessingStatus = "processing"
	r.states[r.video] = m
	agrees(r.author, false)
	m.ProcessingStatus = "ready"
	r.states[r.video] = m
	agrees(r.author, true)

	// No longer a kind that can be saved offline.
	r.p().ContentType = "post"
	agrees(r.viewer, false)
	r.p().ContentType = "long_video"

	// The media state cannot be read: the copy is still valid, and the
	// client is told not to bother renewing right now.
	r.svc.mediaStates = failingMediaStates{}
	if renewable(r.viewer) {
		t.Fatal("renewable=true while the media state could not be read")
	}
	r.svc.mediaStates = r.states

	// An item that is not valid says nothing about renewing.
	r.p().AllowDownload = false
	items := r.check(t, r.viewer, r.post.String(), uuid.NewString())
	for _, item := range items {
		if item.Valid || item.Renewable != nil {
			t.Fatalf("an invalid item carries renewable: %+v", item)
		}
		raw, _ := json.Marshal(item)
		if strings.Contains(string(raw), "renewable") {
			t.Fatalf("an invalid item serialises renewable: %s", raw)
		}
	}
	// And a valid one always serialises it, false included.
	no := false
	raw, _ := json.Marshal(OfflineCheckItem{PostID: "x", Valid: true, Renewable: &no})
	if !strings.Contains(string(raw), `"renewable":false`) {
		t.Fatalf("renewable=false is dropped from the body: %s", raw)
	}
}

type failingMediaStates struct{}

func (failingMediaStates) BatchGetMediaOwnership(context.Context, []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	return nil, errors.New("media_assets unreadable")
}

// A live recording (founder decision, 2 Oct 2026) starts as "viewers can't
// save this offline": a viewer with the link is refused, the creator saves
// their own, and switching it on opens it to viewers.
func TestOfflineLiveRecordingIsTheCreatorsUntilSwitchedOn(t *testing.T) {
	r := newOfflineRig(t)
	stream := uuid.New()
	p := r.p()
	p.Source, p.LiveStreamID, p.Visibility, p.AllowDownload = "live", &stream, "unlisted", false

	_, _, err := r.grant(r.viewer)
	wantOfflineErr(t, err, ErrOfflineNotAllowed, 403, "OFFLINE_NOT_ALLOWED")
	if _, created, err := r.grant(r.author); err != nil || !created {
		t.Fatalf("the creator could not save their own recording: created=%v err=%v", created, err)
	}
	if items := r.check(t, r.author, r.post.String()); !items[0].Valid || !*items[0].Renewable {
		t.Fatalf("the creator's copy: %+v", items)
	}
	p.AllowDownload = true
	if _, created, err := r.grant(r.viewer); err != nil || !created {
		t.Fatalf("after the creator switched it on: created=%v err=%v", created, err)
	}
}
