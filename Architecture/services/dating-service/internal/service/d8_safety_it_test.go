// Lane D8 service tests: panic incidents (dedupe, check-in help, no
// coordinates in the event, the daily abuse limit), trusted contacts, live
// location shares, meets with matches only, reports (reason codes, auto-block,
// underage hold, admin target validation, rate limit, grievance link and
// retry) and purge evidence retention. Skipped without TEST_PG_DSN; refuses a
// database not named *_test.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func floatPtr(v float64) *float64 { return &v }

// d8Exec runs a raw statement (backdating clocks in tests).
func d8Exec(t *testing.T, st *store.Store, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("exec %q: %v", sql, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func d8QueryInt(t *testing.T, st *store.Store, sql string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	tx, err := st.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var n int
	if err := tx.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

func eventsOfType(t *testing.T, rec *recordingWriter, typ string) []chatEnvelope {
	t.Helper()
	var out []chatEnvelope
	for _, e := range rec.events(t) {
		if e.EventType == typ {
			out = append(out, e)
		}
	}
	return out
}

// ── Panic ───────────────────────────────────────────────────────────────────

func TestD8_PanicWritesOneIncidentAndDedupes(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	user := uuid.New()
	first, err := svc.RecordPanic(ctx, user, PanicRequest{Latitude: floatPtr(12.9716), Longitude: floatPtr(77.5946)})
	if err != nil || first.Deduplicated {
		t.Fatalf("first panic: %+v err=%v", first, err)
	}
	second, err := svc.RecordPanic(ctx, user, PanicRequest{})
	if err != nil {
		t.Fatalf("second panic: %v", err)
	}
	if second.IncidentID != first.IncidentID || !second.Deduplicated {
		t.Fatalf("second panic within 2 minutes = %+v, want the same incident deduplicated", second)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_panic_incidents WHERE user_id = $1`, user); n != 1 {
		t.Fatalf("incidents = %d, want 1", n)
	}
	inc, err := st.GetPanicIncident(ctx, first.IncidentID)
	if err != nil || inc.TriggerCount != 2 || !inc.HasLocation() {
		t.Fatalf("incident = %+v err=%v, want trigger_count 2 keeping the point", inc, err)
	}
	if got := len(eventsOfType(t, rec, "dating.safety.panic")); got != 1 {
		t.Fatalf("panic events = %d, want 1 (a deduplicated trigger never pages again)", got)
	}
	// Past the window it is a new incident.
	d8Exec(t, st, `UPDATE dating_panic_incidents SET last_triggered_at = now() - interval '10 minutes' WHERE id = $1`, first.IncidentID)
	third, err := svc.RecordPanic(ctx, user, PanicRequest{})
	if err != nil || third.IncidentID == first.IncidentID {
		t.Fatalf("panic after the window = %+v err=%v, want a new incident", third, err)
	}
}

func TestD8_PanicEventHasNoCoordinates(t *testing.T) {
	svc, _, rec := newD3Svc(t)
	user := uuid.New()
	out, err := svc.RecordPanic(context.Background(), user, PanicRequest{
		Latitude: floatPtr(12.971599), Longitude: floatPtr(77.594566),
		Context: map[string]any{"screen": "chat", "lat": 12.971599},
	})
	if err != nil {
		t.Fatalf("panic: %v", err)
	}
	evs := eventsOfType(t, rec, "dating.safety.panic")
	if len(evs) != 1 {
		t.Fatalf("panic events = %d, want 1", len(evs))
	}
	var payload map[string]any
	if err := json.Unmarshal(evs[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	for k := range payload {
		switch k {
		case "incident_id", "user_id", "created_at", "has_location":
		default:
			t.Fatalf("panic event carries %q: %s", k, evs[0].Payload)
		}
	}
	if strings.Contains(string(evs[0].Payload), "12.97") || strings.Contains(string(evs[0].Payload), "77.59") {
		t.Fatalf("panic event carries coordinates: %s", evs[0].Payload)
	}
	if payload["incident_id"] != out.IncidentID.String() || payload["has_location"] != true {
		t.Fatalf("panic event = %s", evs[0].Payload)
	}
}

func TestD8_CheckInHelpCreatesIncident(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	d3Match(t, svc, a, b)
	meet, err := svc.ScheduleMeet(ctx, a, MeetRequest{WithUserID: b, When: time.Now().Add(time.Hour), Venue: "Cafe"})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if err := svc.MeetCheckIn(ctx, meet.MeetID, a, "help"); err != nil {
		t.Fatalf("check-in help: %v", err)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_panic_incidents
        WHERE user_id = $1 AND source = 'meet_checkin' AND meet_id = $2`, a, meet.MeetID); n != 1 {
		t.Fatalf("check-in help incidents = %d, want 1", n)
	}
	if got := len(eventsOfType(t, rec, "dating.safety.panic")); got != 1 {
		t.Fatalf("panic events after check-in help = %d, want 1", got)
	}
}

func TestD8_SixthPanicIn24hFlaggedAndNotPaged(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	user := uuid.New()
	for i := 0; i < 5; i++ {
		out, err := svc.RecordPanic(ctx, user, PanicRequest{})
		if err != nil || out.Deduplicated {
			t.Fatalf("panic %d: %+v err=%v", i+1, out, err)
		}
		// Step outside the dedupe window so the next trigger is a new incident.
		d8Exec(t, st, `UPDATE dating_panic_incidents SET last_triggered_at = now() - interval '10 minutes' WHERE id = $1`, out.IncidentID)
	}
	sixth, err := svc.RecordPanic(ctx, user, PanicRequest{})
	if err != nil || sixth.Deduplicated {
		t.Fatalf("sixth panic: %+v err=%v", sixth, err)
	}
	inc, err := st.GetPanicIncident(ctx, sixth.IncidentID)
	if err != nil || !inc.SuspectedAbuse {
		t.Fatalf("sixth incident = %+v err=%v, want suspected_abuse", inc, err)
	}
	if got := len(eventsOfType(t, rec, "dating.safety.panic")); got != 5 {
		t.Fatalf("panic events = %d, want 5 (the sixth is not paged)", got)
	}
}

// ── Trusted contacts, live location, meets ──────────────────────────────────

type stubConnections struct {
	mu        sync.Mutex
	connected map[[2]uuid.UUID]bool
	err       error
}

func (s *stubConnections) IsAcceptedConnection(_ context.Context, a, b uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	return s.connected[[2]uuid.UUID{a, b}] || s.connected[[2]uuid.UUID{b, a}], nil
}

func TestD8_TrustedContactMustBeConnectionOrMatch(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	user, matched, stranger := uuid.New(), uuid.New(), uuid.New()
	friends := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	seedActiveProfile(t, st, user)
	seedActiveProfile(t, st, matched)
	d3Match(t, svc, user, matched)
	conns := &stubConnections{connected: map[[2]uuid.UUID]bool{}}
	for _, f := range friends {
		conns.connected[[2]uuid.UUID{user, f}] = true
	}
	svc.SetConnectionChecker(conns)

	if _, err := svc.SetTrustedContact(ctx, user, stranger, true); !errors.Is(err, ErrTrustedContactNotEligible) {
		t.Fatalf("stranger as trusted contact: err=%v, want ErrTrustedContactNotEligible", err)
	}
	if _, err := svc.SetTrustedContact(ctx, user, matched, true); err != nil {
		t.Fatalf("current match as trusted contact: %v", err)
	}
	for _, f := range friends[:2] {
		if _, err := svc.SetTrustedContact(ctx, user, f, false); err != nil {
			t.Fatalf("accepted connection as trusted contact: %v", err)
		}
	}
	if _, err := svc.SetTrustedContact(ctx, user, friends[2], false); !errors.Is(err, store.ErrTrustedContactLimit) {
		t.Fatalf("fourth trusted contact: err=%v, want ErrTrustedContactLimit", err)
	}
	// Updating an existing contact is not a new one.
	if tc, err := svc.SetTrustedContact(ctx, user, friends[0], true); err != nil || !tc.ShareLocationOnPanic {
		t.Fatalf("update existing contact: %+v err=%v", tc, err)
	}
	conns.err = errors.New("graph down")
	if err := svc.RemoveTrustedContact(ctx, user, friends[1]); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := svc.SetTrustedContact(ctx, user, friends[2], false); !errors.Is(err, ErrConnectionCheckUnavailable) {
		t.Fatalf("add while graph is down: err=%v, want ErrConnectionCheckUnavailable", err)
	}
}

func TestD8_ShareLocationToNonContactRefused(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()
	seedActiveProfile(t, st, user)
	seedActiveProfile(t, st, stranger)
	_, err := svc.ShareLocation(ctx, user, LocationShareRequest{RecipientID: stranger, Latitude: floatPtr(12.97), Longitude: floatPtr(77.59)})
	if !errors.Is(err, ErrShareRecipientNotAllowed) {
		t.Fatalf("share with a non-contact: err=%v, want ErrShareRecipientNotAllowed", err)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_location_shares WHERE user_id = $1`, user); n != 0 {
		t.Fatalf("share rows = %d after a refusal", n)
	}
	if got := len(eventsOfType(t, rec, "dating.safety.location_shared")); got != 0 {
		t.Fatalf("location_shared events = %d after a refusal", got)
	}
}

func TestD8_RecipientReadsShareUntilTTLAndSharerStops(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	sharer, recipient, other := uuid.New(), uuid.New(), uuid.New()
	seedActiveProfile(t, st, sharer)
	seedActiveProfile(t, st, recipient)
	d3Match(t, svc, sharer, recipient)

	share, err := svc.ShareLocation(ctx, sharer, LocationShareRequest{RecipientID: recipient, DurationMinutes: 999,
		Latitude: floatPtr(12.971599), Longitude: floatPtr(77.594566)})
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if share.RecipientKind != store.ShareRecipientMatch || time.Until(share.ExpiresAt) > 2*time.Hour+time.Minute {
		t.Fatalf("share = %+v, want a match share capped at 2h", share)
	}
	evs := eventsOfType(t, rec, "dating.safety.location_shared")
	if len(evs) != 1 || strings.Contains(string(evs[0].Payload), "12.97") {
		t.Fatalf("location_shared events = %v, want one without coordinates", evs)
	}
	got, err := svc.GetSharedLocation(ctx, recipient, share.ShareID)
	if err != nil || got.Latitude == nil || *got.Latitude != 12.971599 {
		t.Fatalf("recipient read = %+v err=%v, want the exact point", got, err)
	}
	if _, err := svc.GetSharedLocation(ctx, other, share.ShareID); !errors.Is(err, store.ErrLocationShareNotFound) {
		t.Fatalf("non-recipient read: err=%v, want not found", err)
	}
	if _, err := svc.GetSharedLocation(ctx, sharer, share.ShareID); !errors.Is(err, store.ErrLocationShareNotFound) {
		t.Fatalf("sharer reading via the recipient route: err=%v, want not found", err)
	}
	if _, err := svc.StopLocationShare(ctx, recipient, share.ShareID); !errors.Is(err, store.ErrLocationShareNotFound) {
		t.Fatalf("recipient stopping the share: err=%v, want not found", err)
	}
	if _, err := svc.StopLocationShare(ctx, sharer, share.ShareID); err != nil {
		t.Fatalf("sharer stop: %v", err)
	}
	if _, err := svc.GetSharedLocation(ctx, recipient, share.ShareID); !errors.Is(err, store.ErrLocationShareNotFound) {
		t.Fatalf("read after stop: err=%v, want not found", err)
	}

	second, err := svc.ShareLocation(ctx, sharer, LocationShareRequest{RecipientID: recipient,
		Latitude: floatPtr(12.97), Longitude: floatPtr(77.59)})
	if err != nil {
		t.Fatalf("second share: %v", err)
	}
	if _, err := svc.GetSharedLocation(ctx, recipient, second.ShareID); err != nil {
		t.Fatalf("read before expiry: %v", err)
	}
	d8Exec(t, st, `UPDATE dating_location_shares SET expires_at = now() - interval '1 second' WHERE id = $1`, second.ShareID)
	if _, err := svc.GetSharedLocation(ctx, recipient, second.ShareID); !errors.Is(err, store.ErrLocationShareNotFound) {
		t.Fatalf("read after expiry: err=%v, want not found", err)
	}
}

func TestD8_MeetWithNonMatchRefused(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	_, err := svc.ScheduleMeet(context.Background(), a, MeetRequest{WithUserID: b, When: time.Now().Add(time.Hour)})
	if !errors.Is(err, ErrMeetRequiresMatch) {
		t.Fatalf("meet with a non-match: err=%v, want ErrMeetRequiresMatch", err)
	}
}

// ── Reports ─────────────────────────────────────────────────────────────────

type stubTrustSafety struct {
	mu    sync.Mutex
	down  bool
	calls []GrievanceLinkRequest
	ids   map[uuid.UUID]uuid.UUID
}

func (s *stubTrustSafety) LinkDatingReportGrievance(_ context.Context, req GrievanceLinkRequest) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	if s.down {
		return uuid.Nil, errors.New("trust-safety unavailable")
	}
	if s.ids == nil {
		s.ids = map[uuid.UUID]uuid.UUID{}
	}
	if id, ok := s.ids[req.ReportID]; ok {
		return id, nil
	}
	id := uuid.New()
	s.ids[req.ReportID] = id
	return id, nil
}

func TestD8_ReportInvalidReasonRefused(t *testing.T) {
	svc, _, _ := newD3Svc(t)
	_, err := svc.Report(context.Background(), uuid.New(), ReportRequest{TargetID: uuid.New(), Reason: "creepy"})
	if !errors.Is(err, ErrInvalidReportReason) {
		t.Fatalf("invalid reason: err=%v", err)
	}
	_, err = svc.Report(context.Background(), uuid.New(), ReportRequest{TargetID: uuid.New(), Reason: "other"})
	if err == nil || !strings.HasPrefix(err.Error(), "invalid: ") {
		t.Fatalf("other without details: err=%v, want invalid", err)
	}
}

func TestD8_ReportAutoBlocksAndClosesMatch(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	matchID := d3Match(t, svc, a, b)
	out, err := svc.Report(ctx, a, ReportRequest{TargetID: b, Reason: "harassment", Details: "abusive messages",
		Evidence: store.ReportEvidence{MessageIDs: []string{"msg-1"}}})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !out.Blocked || !out.AutoBlocked {
		t.Fatalf("report result blocked=%v auto_blocked=%v, want both true", out.Blocked, out.AutoBlocked)
	}
	if blocked, _ := st.IsBlockedEitherWay(ctx, a, b); !blocked {
		t.Fatalf("reporting did not block the target")
	}
	m, err := st.GetMatch(ctx, matchID)
	if err != nil || m.Status != "closed" {
		t.Fatalf("match after report = %+v err=%v, want closed", m, err)
	}
	closed := false
	for _, e := range eventsOfType(t, rec, "dating.match.closed") {
		if strings.Contains(string(e.Payload), matchID.String()) {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("no dating.match.closed for the reported match")
	}
}

func TestD8_UnderageReportHoldsTargetInPendingReview(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	reporter, target := uuid.New(), uuid.New()
	seedActiveProfile(t, st, reporter)
	seedActiveProfile(t, st, target)
	if _, err := svc.Report(ctx, reporter, ReportRequest{TargetID: target, Reason: "underage"}); err != nil {
		t.Fatalf("report: %v", err)
	}
	p, err := st.GetProfile(ctx, target)
	if err != nil || p.ProfileStatus != store.ProfileStatusPendingReview {
		t.Fatalf("target after underage report = %v err=%v, want pending_review", p.ProfileStatus, err)
	}
}

func TestD8_AdminActionWithMismatchedTargetRefused(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	reporter, target, bystander := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{reporter, target, bystander} {
		seedActiveProfile(t, st, id)
	}
	r, err := svc.Report(ctx, reporter, ReportRequest{TargetID: target, Reason: "spam"})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	admin := uuid.New()
	if _, err := svc.ActOnReport(ctx, admin, r.ID, bystander, "suspend"); !errors.Is(err, ErrReportTargetMismatch) {
		t.Fatalf("suspend a bystander through a report: err=%v, want ErrReportTargetMismatch", err)
	}
	if p, _ := st.GetProfile(ctx, bystander); p.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("bystander status = %s, want active", p.ProfileStatus)
	}
	if got, _ := st.GetReportByID(ctx, r.ID); got.Status != "submitted" {
		t.Fatalf("report status after refused action = %s", got.Status)
	}
	if _, err := svc.ActOnReport(ctx, admin, r.ID, uuid.Nil, "suspend"); err != nil {
		t.Fatalf("suspend the report's own target: %v", err)
	}
	if p, _ := st.GetProfile(ctx, target); p.ProfileStatus != store.ProfileStatusSuspended {
		t.Fatalf("target status = %s, want suspended", p.ProfileStatus)
	}
}

func TestD8_ReportRateLimit(t *testing.T) {
	svc, _, _ := newD3Svc(t)
	cfg := DefaultSafetyConfig()
	cfg.ReportDailyLimit = 2
	svc.SetSafetyConfig(cfg)
	ctx := context.Background()
	reporter := uuid.New()
	for i := 0; i < 2; i++ {
		if _, err := svc.Report(ctx, reporter, ReportRequest{TargetID: uuid.New(), Reason: "spam"}); err != nil {
			t.Fatalf("report %d: %v", i+1, err)
		}
	}
	if _, err := svc.Report(ctx, reporter, ReportRequest{TargetID: uuid.New(), Reason: "spam"}); !errors.Is(err, store.ErrReportRateLimited) {
		t.Fatalf("third report: err=%v, want ErrReportRateLimited", err)
	}
}

func TestD8_ReportLinksGrievanceAndRetriesWhenTrustSafetyIsDown(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	ts := &stubTrustSafety{}
	svc.SetTrustSafetyClient(ts)

	ok, err := svc.Report(ctx, uuid.New(), ReportRequest{TargetID: uuid.New(), Reason: "scam", Details: "asked for money"})
	if err != nil || ok.GrievanceID == nil {
		t.Fatalf("report with trust-safety up: grievance=%v err=%v", ok.GrievanceID, err)
	}
	stored, _ := st.GetReportByID(ctx, ok.ID)
	if stored.GrievanceID == nil || *stored.GrievanceID != *ok.GrievanceID {
		t.Fatalf("stored grievance = %v, want %v", stored.GrievanceID, *ok.GrievanceID)
	}

	ts.down = true
	pending, err := svc.Report(ctx, uuid.New(), ReportRequest{TargetID: uuid.New(), Reason: "hate"})
	if err != nil {
		t.Fatalf("report while trust-safety is down must still save: %v", err)
	}
	if pending.GrievanceID != nil {
		t.Fatalf("grievance linked while trust-safety was down")
	}
	if n := d8QueryInt(t, st, `SELECT grievance_attempts FROM dating_reports WHERE id = $1`, pending.ID); n != 1 {
		t.Fatalf("grievance attempts = %d, want 1", n)
	}
	ts.down = false
	d8Exec(t, st, `UPDATE dating_reports SET grievance_next_attempt_at = now() - interval '1 second' WHERE id = $1`, pending.ID)
	if _, err := svc.LinkPendingReportGrievances(ctx, 500); err != nil {
		t.Fatalf("retry worker: %v", err)
	}
	stored, _ = st.GetReportByID(ctx, pending.ID)
	if stored.GrievanceID == nil {
		t.Fatalf("retry worker did not link the grievance")
	}
	// The retry worker links every pending report in the shared test database,
	// so find this report's request rather than assuming it was the last call.
	var found bool
	for _, c := range ts.calls {
		if c.ReportID == pending.ID {
			found = true
			if c.Reason != "hate" {
				t.Fatalf("grievance request = %+v", c)
			}
		}
	}
	if !found {
		t.Fatalf("retry worker sent no grievance request for report %s", pending.ID)
	}
}

// ── Purge retention ─────────────────────────────────────────────────────────

func TestD8_PurgeKeepsTargetReportsAndAnonymisesReporter(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	purged, other, reporter := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{purged, other, reporter} {
		seedActiveProfile(t, st, id)
	}
	byPurged, err := svc.Report(ctx, purged, ReportRequest{TargetID: other, Reason: "spam"})
	if err != nil {
		t.Fatal(err)
	}
	againstPurged, err := svc.Report(ctx, reporter, ReportRequest{TargetID: purged, Reason: "harassment", Details: "threats"})
	if err != nil {
		t.Fatal(err)
	}
	incident, err := svc.RecordPanic(ctx, purged, PanicRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.PurgeProfile(ctx, purged); err != nil {
		t.Fatalf("purge: %v", err)
	}
	token := st.SubjectToken(purged)

	kept, err := st.GetReportByID(ctx, againstPurged.ID)
	if err != nil || kept.TargetID != purged || kept.RetainUntil == nil {
		t.Fatalf("report against the purged user = %+v err=%v, want kept with retain_until", kept, err)
	}
	anon, err := st.GetReportByID(ctx, byPurged.ID)
	if err != nil || anon.ReporterID != token || !anon.ReporterAnonymised || anon.RetainUntil == nil {
		t.Fatalf("report by the purged user = %+v err=%v, want kept with the reporter anonymised", anon, err)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_reports WHERE reporter_id = $1`, purged); n != 0 {
		t.Fatalf("reports still naming the purged reporter = %d", n)
	}
	inc, err := st.GetPanicIncident(ctx, incident.IncidentID)
	if err != nil || inc.UserID != token || !inc.Anonymised {
		t.Fatalf("panic incident after purge = %+v err=%v, want kept under the token", inc, err)
	}
}

func TestD8_PurgeKeepsHashedRiskSignals(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	purged := uuid.New()
	seedActiveProfile(t, st, purged)
	rawFP := "fp-" + uuid.NewString()
	if err := st.UpsertDeviceFingerprint(ctx, purged, rawFP, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAccountRisk(ctx, &store.AccountRisk{UserID: purged, RiskScore: 91, RiskLevel: store.RiskLevelSuspend, Signals: map[string]any{}}); err != nil {
		t.Fatal(err)
	}

	if err := svc.PurgeProfile(ctx, purged); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if err := st.PurgeUserAuxiliary(ctx, purged); err != nil {
		t.Fatalf("purge aux: %v", err)
	}
	token := st.SubjectToken(purged)
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_device_fingerprints WHERE user_id = $1`, purged); n != 0 {
		t.Fatalf("raw fingerprints left after purge = %d", n)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_retained_risk_signals
        WHERE subject_token = $1 AND kind = 'account' AND risk_level = 'suspend' AND risk_score = 91`, token); n != 1 {
		t.Fatalf("retained account risk rows = %d, want 1", n)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_retained_risk_signals
        WHERE subject_token = $1 AND kind = 'device_fingerprint' AND value_hash = $2`, token, st.HashSignal(store.RetainedSignalDeviceFingerprint, rawFP)); n != 1 {
		t.Fatalf("retained fingerprint rows = %d, want 1", n)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_retained_risk_signals
        WHERE value_hash = $1 OR value_hash = '203.0.113.7'`, rawFP); n != 0 {
		t.Fatalf("a raw signal was retained unhashed")
	}
	// Ban evasion: a new account on the same device still sees prior use.
	if n, err := st.CountUsersByFingerprint(ctx, rawFP); err != nil || n != 1 {
		t.Fatalf("CountUsersByFingerprint after purge = %d err=%v, want 1", n, err)
	}
}

func TestD8_PurgeAnonymisesMatchesAndEmitsClosed(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	purged, partner := uuid.New(), uuid.New()
	seedActiveProfile(t, st, purged)
	seedActiveProfile(t, st, partner)
	matchID := d3Match(t, svc, purged, partner)

	if err := svc.PurgeProfile(ctx, purged); err != nil {
		t.Fatalf("purge: %v", err)
	}
	m, err := st.GetMatch(ctx, matchID)
	if err != nil {
		t.Fatalf("match row after purge: %v", err)
	}
	token := st.SubjectToken(purged)
	if m.Status != "closed" || m.UserA == purged || m.UserB == purged ||
		!((m.UserA == token && m.UserB == partner) || (m.UserB == token && m.UserA == partner)) {
		t.Fatalf("match after purge = %+v, want closed with the purged user replaced by %s", m, token)
	}
	emitted := false
	for _, e := range eventsOfType(t, rec, "dating.match.closed") {
		if strings.Contains(string(e.Payload), matchID.String()) {
			emitted = true
		}
	}
	if !emitted {
		t.Fatalf("purge closed the match without dating.match.closed")
	}
}

func TestD8_RetentionSweeperDeletesAfterWindow(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	purged, reporter := uuid.New(), uuid.New()
	seedActiveProfile(t, st, purged)
	seedActiveProfile(t, st, reporter)
	expired, err := svc.Report(ctx, reporter, ReportRequest{TargetID: purged, Reason: "scam"})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := svc.Report(ctx, purged, ReportRequest{TargetID: uuid.New(), Reason: "spam"})
	if err != nil {
		t.Fatal(err)
	}
	incident, err := svc.RecordPanic(ctx, purged, PanicRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.PurgeProfile(ctx, purged); err != nil {
		t.Fatal(err)
	}
	token := st.SubjectToken(purged)
	d8Exec(t, st, `UPDATE dating_reports SET retain_until = now() - interval '1 second' WHERE id = $1`, expired.ID)
	d8Exec(t, st, `UPDATE dating_panic_incidents SET retain_until = now() - interval '1 second' WHERE id = $1`, incident.IncidentID)
	d8Exec(t, st, `UPDATE dating_retained_risk_signals SET retain_until = now() - interval '1 second' WHERE subject_token = $1`, token)

	if _, err := st.DeleteExpiredEvidence(ctx, 1000); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := st.GetReportByID(ctx, expired.ID); !errors.Is(err, store.ErrReportNotFound) {
		t.Fatalf("report past retention still present: %v", err)
	}
	if _, err := st.GetPanicIncident(ctx, incident.IncidentID); !errors.Is(err, store.ErrPanicNotFound) {
		t.Fatalf("panic incident past retention still present: %v", err)
	}
	if n := d8QueryInt(t, st, `SELECT COUNT(*)::int FROM dating_retained_risk_signals WHERE subject_token = $1`, token); n != 0 {
		t.Fatalf("retained signals past retention = %d", n)
	}
	if _, err := st.GetReportByID(ctx, fresh.ID); err != nil {
		t.Fatalf("report inside retention was deleted: %v", err)
	}
}
