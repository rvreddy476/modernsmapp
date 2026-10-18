//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Message reports (chat-parity CONTRACT §2.6 / §8) against the real schema
// through migration 015 on a *_test database:
//
//	M7_TRUST_POSTGRES_DSN=…/trust_safety_it_test go test -tags integration ./internal/service/ -run MessageReports -v
func TestMessageReportsAgainstPostgres(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	svc := New(postgres.New(pool), nil)
	alice, bob, stranger := uuid.New(), uuid.New(), uuid.New()
	conv, msg, media := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM trust.reports WHERE reporter_id = ANY($1)`, []uuid.UUID{alice, bob})
	})
	ref := conv.String() + ":" + msg.String()

	// A message report with a service snapshot.
	r, err := svc.FileReportWith(ctx, FileReportInput{
		ReporterID: alice, EntityType: "message", EntityRef: ref, Reason: "scam", Details: "asked for money",
		Evidence:      &ReportEvidence{Text: "send 500", MediaID: media.String(), SenderID: stranger.String(), ConversationID: conv.String(), Source: EvidenceSourceMessageService},
		SourceEventID: "message-service:it-" + msg.String(),
	})
	if err != nil {
		t.Fatalf("message report: %v", err)
	}
	if r.EntityID != msg || r.EntityKey != ref || r.Reason != "scam_fraud" {
		t.Fatalf("stored (%s, %q, %s)", r.EntityID, r.EntityKey, r.Reason)
	}

	// The admin queue (and a single read) carries the evidence.
	got, err := svc.GetReport(ctx, r.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	var ev ReportEvidence
	if err := json.Unmarshal(got.Evidence, &ev); err != nil {
		t.Fatalf("evidence %s: %v", got.Evidence, err)
	}
	if ev.Text != "send 500" || ev.MediaID != media.String() || ev.Source != EvidenceSourceMessageService {
		t.Fatalf("evidence %+v", ev)
	}
	queue, err := svc.ListReports(ctx, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, q := range queue {
		if q.ID == r.ID {
			found = q.EntityType == "message" && len(q.Evidence) > 0
		}
	}
	if !found {
		t.Fatal("message report missing from the admin queue (or without evidence)")
	}

	// One active report per reporter per message.
	if _, err := svc.FileReportRef(ctx, alice, "message", ref, "spam", ""); !errors.Is(err, ErrActiveReportExists) {
		t.Fatalf("second active report: %v", err)
	}
	// Same idempotency key from another reporter is still a duplicate source.
	if _, err := svc.FileReportWith(ctx, FileReportInput{
		ReporterID: bob, EntityType: "message", EntityRef: ref, Reason: "spam",
		SourceEventID: "message-service:it-" + msg.String(),
	}); !errors.Is(err, postgres.ErrDuplicateSourceEvent) {
		t.Fatalf("reused source key: %v", err)
	}

	// The MessageRequestReported mapping is idempotent even after the first
	// report is closed (the active-report index alone would not stop it).
	reqConv := uuid.New()
	in, ok := MessageRequestReportInput(MessageRequestReported{
		ConversationID: reqConv.String(), SenderID: stranger.String(), ReceiverID: bob.String(), Preview: "hello stranger",
	})
	if !ok {
		t.Fatal("mapping rejected")
	}
	first, err := svc.FileReportWith(ctx, in)
	if err != nil {
		t.Fatalf("request report: %v", err)
	}
	if first.EntityType != "user" || first.EntityID != stranger || len(first.Evidence) == 0 {
		t.Fatalf("request report %+v", first)
	}
	if _, err := pool.Exec(ctx, `UPDATE trust.reports SET status = 'dismissed' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FileReportWith(ctx, in); !errors.Is(err, postgres.ErrDuplicateSourceEvent) {
		t.Fatalf("redelivered request report: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trust.reports WHERE source_event_id = $1`, in.SourceEventID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d rows for the request (%v), want 1", n, err)
	}

	// The schema keeps entity_key consistent with entity_id for messages.
	_, err = pool.Exec(ctx, `INSERT INTO trust.reports (id, reporter_id, entity_type, entity_id, entity_key, reason, details, status)
		VALUES ($1, $2, 'message', $3, $4, 'spam', '', 'open')`, uuid.New(), alice, uuid.New(), ref)
	if err == nil {
		t.Fatal("a message row whose key names another message was accepted")
	}
	_, err = pool.Exec(ctx, `INSERT INTO trust.reports (id, reporter_id, entity_type, entity_id, reason, details, status, evidence)
		VALUES ($1, $2, 'post', $3, 'spam', '', 'open', '[1]'::jsonb)`, uuid.New(), alice, uuid.New())
	if err == nil {
		t.Fatal("non-object evidence was accepted")
	}
}
