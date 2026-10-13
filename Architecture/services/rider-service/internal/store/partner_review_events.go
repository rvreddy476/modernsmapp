package store

import (
	"context"
	"encoding/json"

	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PartnerReview records the actor and correction reason with the decision.
// They stay in the private event/audit; lock-screen copy is generic.
type PartnerReview struct {
	ActorID uuid.UUID
	Reason  string
}

func partnerStatusEvent(status string) string {
	switch status {
	case "approved":
		return "rider.partner.approved"
	case "rejected":
		return "rider.partner.kyc_rejected"
	case "suspended":
		return "rider.partner.suspended"
	case "blocked":
		return "rider.partner.blocked"
	default:
		return ""
	}
}

// enqueuePartnerReviewTx resolves the recipient from the owning row. A
// notification failure aborts the mutation, leaving a retryable decision.
func enqueuePartnerReviewTx(ctx context.Context, tx pgx.Tx, typ string, partnerID uuid.UUID, status string, review PartnerReview) error {
	if typ == "" {
		return nil
	}
	if review.ActorID == uuid.Nil {
		review.ActorID = SystemActorID
	}
	var body []byte
	if err := tx.QueryRow(ctx, `SELECT jsonb_build_object('partner_id',id,
	 'partner_user_id',user_id,'status',$2::text,'reason',$3::text,
	 'actor_id',$4::text,'occurred_at',clock_timestamp())
	 FROM rider_partners WHERE id=$1`, partnerID, status, review.Reason, review.ActorID.String()).Scan(&body); err != nil {
		return err
	}
	return outbox.NewQueuer("rider").Enqueue(ctx, tx, typ, "rider.partner."+partnerID.String(), json.RawMessage(body))
}
