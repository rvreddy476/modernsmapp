//go:build integration

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// reels-29 against the real schema (CI only, a *_test database): resolving
// a post report as upheld queues its takedown in the same transaction,
// sends the report-v1 reject at once, and a failure stays pending, visible
// and retried; dismissal sends nothing; other subjects owe nothing.

func takedownTestReport(t *testing.T, svc *Service, entityType string) *postgres.Report {
	t.Helper()
	r, err := svc.FileReport(context.Background(), uuid.New(), uuid.New(), entityType, "spam", "e2e takedown")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func moderate(t *testing.T, svc *Service, moderator uuid.UUID, id uuid.UUID, status, notes string) *postgres.Report {
	t.Helper()
	meta := postgres.AuditMeta{Actor: postgres.UserActor(moderator), Reason: notes}
	r, err := svc.UpdateReport(context.Background(), meta, id.String(), status, nil, notes)
	if err != nil {
		t.Fatalf("%s: %v", status, err)
	}
	return r
}

func TestReportTakedown_UpheldPostReportIsSentOnce(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := New(store, nil)
	post := &fakeTakedownPost{}
	svc.SetPostModerationClient(post)
	moderator := uuid.New()

	report := takedownTestReport(t, svc, "reel")
	post.subject = &PostModerationSubject{PostID: report.EntityID, AuthorID: uuid.New(), ReviewStatus: "approved", ContentRevision: 4}
	moderate(t, svc, moderator, report.ID, "reviewing", "")
	if len(post.rejects) != 0 {
		t.Fatal("a takedown was sent before the report was upheld")
	}
	got := moderate(t, svc, moderator, report.ID, "resolved", "spam reel removed")
	if got.TakedownStatus != postgres.TakedownSent || got.TakedownAttempts != 1 || got.TakedownDoneAt == nil {
		t.Fatalf("report after resolve: %+v", got)
	}
	if len(post.rejects) != 1 {
		t.Fatalf("rejects = %d", len(post.rejects))
	}
	c := post.rejects[0]
	if c.report != report.ID || c.post != report.EntityID || c.moderator != moderator || c.revision != 4 || c.reason != "spam reel removed" {
		t.Fatalf("reject = %+v", c)
	}
	// Nothing is left due: a sweep sends nothing more.
	if n, err := svc.ProcessReportTakedowns(ctx, nil); err != nil || len(post.rejects) != 1 {
		t.Fatalf("sweep after sent: n=%d err=%v rejects=%d", n, err, len(post.rejects))
	}
	// Dismissing it afterwards does not undo or resend the takedown.
	if after := moderate(t, svc, moderator, report.ID, "dismissed", "changed my mind"); after.TakedownStatus != postgres.TakedownSent {
		t.Fatalf("dismissed after sent: %q", after.TakedownStatus)
	}
}

func TestReportTakedown_OutageStaysPendingThenRetries(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := New(store, nil)
	post := &fakeTakedownPost{rejectErr: errors.New("post-service unavailable")}
	svc.SetPostModerationClient(post)
	moderator := uuid.New()

	report := takedownTestReport(t, svc, "post")
	post.subject = &PostModerationSubject{PostID: report.EntityID, AuthorID: uuid.New(), ReviewStatus: "approved", ContentRevision: 2}
	moderate(t, svc, moderator, report.ID, "reviewing", "")
	got := moderate(t, svc, moderator, report.ID, "resolved", "")
	// The resolution committed; the takedown is pending with its failure shown.
	if got.Status != "resolved" || got.TakedownStatus != postgres.TakedownPending || got.TakedownAttempts != 1 || got.TakedownLastError == "" {
		t.Fatalf("report after a failed first attempt: %+v", got)
	}
	if post.rejects[0].reason != "Removed after a report: spam" {
		t.Fatalf("fallback reason = %q", post.rejects[0].reason)
	}
	// Not due yet (backoff): a sweep sends nothing.
	if _, err := svc.ProcessReportTakedowns(ctx, nil); err != nil || len(post.rejects) != 1 {
		t.Fatalf("sweep inside backoff sent: %d", len(post.rejects))
	}
	// Due now, post-service back: the retry is the same decision.
	if _, err := pool.Exec(ctx, `UPDATE trust.reports SET takedown_next_attempt_at = clock_timestamp() WHERE id = $1`, report.ID); err != nil {
		t.Fatal(err)
	}
	post.rejectErr = nil
	if n, err := svc.ProcessReportTakedowns(ctx, &report.ID); err != nil || n != 1 {
		t.Fatalf("retry: n=%d err=%v", n, err)
	}
	if len(post.rejects) != 2 || post.rejects[1].report != report.ID {
		t.Fatalf("retry rejects = %+v", post.rejects)
	}
	final, err := store.GetReport(ctx, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.TakedownStatus != postgres.TakedownSent || final.TakedownAttempts != 2 || final.TakedownLastError != "" {
		t.Fatalf("after retry: %+v", final)
	}
}

func TestReportTakedown_DismissedAndOtherSubjectsSendNothing(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := New(store, nil)
	post := &fakeTakedownPost{rejectErr: errors.New("post-service unavailable")}
	svc.SetPostModerationClient(post)
	moderator := uuid.New()

	// Not upheld: nothing queued, nothing sent.
	dismissed := takedownTestReport(t, svc, "post")
	moderate(t, svc, moderator, dismissed.ID, "reviewing", "")
	if got := moderate(t, svc, moderator, dismissed.ID, "dismissed", "not spam"); got.TakedownStatus != "" {
		t.Fatalf("dismissed report takedown = %q", got.TakedownStatus)
	}
	// A comment report upheld: not taken down here.
	comment := takedownTestReport(t, svc, "comment")
	moderate(t, svc, moderator, comment.ID, "reviewing", "")
	if got := moderate(t, svc, moderator, comment.ID, "resolved", "removed"); got.TakedownStatus != "" {
		t.Fatalf("comment report takedown = %q", got.TakedownStatus)
	}
	if len(post.rejects) != 0 {
		t.Fatalf("rejects = %d, want none", len(post.rejects))
	}

	// Upheld, first attempt fails, then dismissed: the pending takedown is
	// cancelled and never sent again.
	upheld := takedownTestReport(t, svc, "video")
	post.subject = &PostModerationSubject{PostID: upheld.EntityID, AuthorID: uuid.New(), ReviewStatus: "approved", ContentRevision: 1}
	moderate(t, svc, moderator, upheld.ID, "reviewing", "")
	moderate(t, svc, moderator, upheld.ID, "resolved", "r")
	if got := moderate(t, svc, moderator, upheld.ID, "dismissed", "overturned before sending"); got.TakedownStatus != postgres.TakedownCancelled {
		t.Fatalf("dismissed pending takedown = %q", got.TakedownStatus)
	}
	if _, err := pool.Exec(ctx, `UPDATE trust.reports SET takedown_next_attempt_at = clock_timestamp() WHERE id = $1`, upheld.ID); err != nil {
		t.Fatal(err)
	}
	before := len(post.rejects)
	post.rejectErr = nil
	if _, err := svc.ProcessReportTakedowns(ctx, &upheld.ID); err != nil || len(post.rejects) != before {
		t.Fatalf("cancelled takedown sent: %d -> %d (%v)", before, len(post.rejects), err)
	}
}
