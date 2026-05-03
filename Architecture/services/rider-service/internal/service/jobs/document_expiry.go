// Document expiry reminder job. Walks rider_partner_documents +
// rider_vehicle_documents and emits one EventRiderDocumentExpiring per
// (document, threshold) once. The dedupe is hard: the unique constraint
// on rider_doc_reminders_sent (document_id, bucket) makes the second
// emission a no-op even if the job runs twice within the same minute.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
)

// DocExpiryPublisher is the contract for the document-expiring publisher.
type DocExpiryPublisher interface {
	PublishDocumentExpiring(ctx context.Context, payload events.DocumentExpiringPayload) error
}

// docExpiryBuckets is the closed set of thresholds we send reminders at.
// Order matters: we pick the first bucket the row falls into, smallest
// first, so a document 2 days from expiry gets the "1d" bucket — not all
// of them at once.
var docExpiryBuckets = []struct {
	Name    string
	UntilLE time.Duration // bucket fires when expires_at - now() <= UntilLE
}{
	{"expired", 0},
	{"1d", 24 * time.Hour},
	{"3d", 3 * 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"14d", 14 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
}

// pickDocBucket returns the smallest bucket the row falls into.
// Returns "" when the document is more than 30 days from expiring.
// "expired" is the bucket for already-passed expiry dates (until <= 0).
func pickDocBucket(until time.Duration) string {
	if until <= 0 {
		return "expired"
	}
	for _, b := range docExpiryBuckets {
		if b.Name == "expired" {
			continue
		}
		if until <= b.UntilLE {
			return b.Name
		}
	}
	return ""
}

// RunDocumentExpiryReminder walks expiring documents and emits a reminder
// per (document_id, bucket) the first time it's seen. Returns the count
// of reminders newly sent.
func RunDocumentExpiryReminder(ctx context.Context, st *store.Store, pub DocExpiryPublisher) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("doc expiry: store required")
	}
	docs, err := st.ListExpiringDocuments(ctx, 30*24*time.Hour)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	sent := 0
	for i := range docs {
		d := &docs[i]
		until := d.ExpiresAt.Sub(now)
		bucket := pickDocBucket(until)
		if bucket == "" {
			continue
		}
		newlyInserted, err := st.MarkReminderSent(ctx, d.PartnerID, d.DocumentID, d.ExpiresAt, bucket)
		if err != nil {
			slog.Warn("rider job: doc reminder mark sent failed",
				"document_id", d.DocumentID, "bucket", bucket, "error", err)
			continue
		}
		if !newlyInserted {
			continue
		}
		payload := events.DocumentExpiringPayload{
			PartnerID:    d.PartnerID.String(),
			DocumentID:   d.DocumentID.String(),
			DocumentKind: d.DocumentKind,
			OwnerKind:    d.OwnerKind,
			ExpiresAt:    d.ExpiresAt,
			Bucket:       bucket,
			OccurredAt:   time.Now().UTC(),
		}
		if pub != nil {
			if perr := pub.PublishDocumentExpiring(ctx, payload); perr != nil {
				slog.Warn("rider job: doc expiring publish failed",
					"document_id", d.DocumentID, "bucket", bucket, "error", perr)
			}
		}
		sent++
	}
	return sent, nil
}
