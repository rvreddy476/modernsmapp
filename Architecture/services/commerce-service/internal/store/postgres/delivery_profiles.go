package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrDeliveryProfileMissing = errors.New("commerce delivery profile not found")
	ErrDeliveryInvalid        = errors.New("invalid delivery application")
	ErrDeliveryReviewConflict = errors.New("delivery application changed; refresh before reviewing")
	ErrDeliveryIncomplete     = errors.New("required delivery documents have not been submitted or approved")
)

type DeliveryProfile struct {
	ID          uuid.UUID          `json:"id"`
	UserID      uuid.UUID          `json:"user_id"`
	FullName    string             `json:"full_name"`
	VehicleType string             `json:"vehicle_type"`
	Status      string             `json:"status"`
	ReviewNote  string             `json:"review_note"`
	Revision    int64              `json:"revision"`
	SubmittedAt *time.Time         `json:"submitted_at,omitempty"`
	ReviewedAt  *time.Time         `json:"reviewed_at,omitempty"`
	Documents   []DeliveryDocument `json:"documents"`
}

type DeliveryDocument struct {
	ID         uuid.UUID `json:"id"`
	Kind       string    `json:"kind"`
	MediaID    uuid.UUID `json:"-"`
	Status     string    `json:"status"`
	ReviewNote string    `json:"review_note"`
}

func ValidDeliveryVehicle(v string) bool { return v == "bicycle" || v == "motorbike" || v == "car" }
func ValidDeliveryDocument(k string) bool {
	switch k {
	case "selfie", "identity", "driving_licence", "vehicle_registration", "bank_proof", "introduction":
		return true
	}
	return false
}
func RequiredDeliveryDocuments(vehicle string) []string {
	kinds := []string{"selfie", "identity", "bank_proof"}
	if vehicle != "bicycle" {
		kinds = append(kinds, "driving_licence", "vehicle_registration")
	}
	return kinds
}

const deliveryProfileCols = `id,user_id,full_name,vehicle_type,status,review_note,revision,submitted_at,reviewed_at`

func scanDeliveryProfile(row pgx.Row) (*DeliveryProfile, error) {
	p := &DeliveryProfile{Documents: []DeliveryDocument{}}
	err := row.Scan(&p.ID, &p.UserID, &p.FullName, &p.VehicleType, &p.Status, &p.ReviewNote, &p.Revision, &p.SubmittedAt, &p.ReviewedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeliveryProfileMissing
	}
	return p, err
}
func (s *Store) DeliveryProfile(ctx context.Context, user uuid.UUID) (*DeliveryProfile, error) {
	// Repeatable read avoids combining approval from one revision with new documents.
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	p, err := scanDeliveryProfile(tx.QueryRow(ctx, `SELECT `+deliveryProfileCols+` FROM commerce_delivery_profiles WHERE user_id=$1`, user))
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,kind,media_id,status,review_note FROM commerce_delivery_documents WHERE profile_id=$1 ORDER BY kind`, p.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d DeliveryDocument
		if err = rows.Scan(&d.ID, &d.Kind, &d.MediaID, &d.Status, &d.ReviewNote); err != nil {
			rows.Close()
			return nil, err
		}
		p.Documents = append(p.Documents, d)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// An approved application's identity is immutable here: a later change-request
// workflow must preserve approved data until its replacement is reviewed.
func (s *Store) SaveDeliveryProfile(ctx context.Context, user uuid.UUID, name, vehicle string) error {
	name = strings.TrimSpace(name)
	if user == uuid.Nil || len([]rune(name)) < 2 || len([]rune(name)) > 120 || !ValidDeliveryVehicle(vehicle) {
		return ErrDeliveryInvalid
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO commerce_delivery_profiles(user_id,full_name,vehicle_type) VALUES($1,$2,$3)
        ON CONFLICT(user_id) DO UPDATE SET full_name=$2,vehicle_type=$3,revision=commerce_delivery_profiles.revision+1,updated_at=now()
        WHERE commerce_delivery_profiles.status IN ('draft','changes_requested')`, user, name, vehicle)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrDeliveryReviewConflict
	}
	return err
}

func (s *Store) SaveDeliveryDocument(ctx context.Context, user uuid.UUID, kind string, media uuid.UUID) error {
	if !ValidDeliveryDocument(kind) || media == uuid.Nil {
		return ErrDeliveryInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	p, err := scanDeliveryProfile(tx.QueryRow(ctx, `SELECT `+deliveryProfileCols+` FROM commerce_delivery_profiles WHERE user_id=$1 FOR UPDATE`, user))
	if err != nil {
		return err
	}
	if p.Status != "draft" && p.Status != "changes_requested" {
		return ErrDeliveryReviewConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_documents(profile_id,kind,media_id) VALUES($1,$2,$3)
        ON CONFLICT(profile_id,kind) DO UPDATE SET media_id=$3,status='submitted',review_note='',reviewed_by=NULL,reviewed_at=NULL,updated_at=now()`, p.ID, kind, media)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE commerce_delivery_profiles SET revision=revision+1,updated_at=now() WHERE id=$1`, p.ID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func deliveryDocumentsReady(ctx context.Context, tx pgx.Tx, p *DeliveryProfile, approved bool) error {
	var count int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM commerce_delivery_documents WHERE profile_id=$1 AND kind=ANY($2)
        AND (($3 AND status='approved') OR (NOT $3 AND status IN ('submitted','approved')))`, p.ID, RequiredDeliveryDocuments(p.VehicleType), approved).Scan(&count)
	if err != nil {
		return err
	}
	if count != len(RequiredDeliveryDocuments(p.VehicleType)) {
		return ErrDeliveryIncomplete
	}
	return nil
}

func (s *Store) SubmitDeliveryProfile(ctx context.Context, user uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	p, err := scanDeliveryProfile(tx.QueryRow(ctx, `SELECT `+deliveryProfileCols+` FROM commerce_delivery_profiles WHERE user_id=$1 FOR UPDATE`, user))
	if err != nil {
		return err
	}
	if p.Status == "submitted" {
		return nil
	}
	if p.Status != "draft" && p.Status != "changes_requested" {
		return ErrDeliveryReviewConflict
	}
	if err = deliveryDocumentsReady(ctx, tx, p, false); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE commerce_delivery_profiles SET status='submitted',submitted_at=now(),review_note='',revision=revision+1,updated_at=now() WHERE id=$1`, p.ID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Admin review uses an optimistic revision in addition to a row lock, so a
// stale page cannot approve new/replaced evidence it has never displayed.
func (s *Store) ReviewDelivery(ctx context.Context, actor, profile, document uuid.UUID, revision int64, decision, note string) error {
	note = strings.TrimSpace(note)
	if actor == uuid.Nil || (decision != "approved" && decision != "changes_requested" && decision != "suspended") || len(note) > 1000 || (decision != "approved" && note == "") {
		return ErrDeliveryInvalid
	}
	if document != uuid.Nil && decision == "suspended" {
		return ErrDeliveryInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	p, err := scanDeliveryProfile(tx.QueryRow(ctx, `SELECT `+deliveryProfileCols+` FROM commerce_delivery_profiles WHERE id=$1 FOR UPDATE`, profile))
	if err != nil {
		return err
	}
	if p.Revision != revision {
		return ErrDeliveryReviewConflict
	}
	if document != uuid.Nil {
		if p.Status != "submitted" {
			return ErrDeliveryReviewConflict
		}
		tag, err := tx.Exec(ctx, `UPDATE commerce_delivery_documents SET status=$3,review_note=$4,reviewed_by=$5,reviewed_at=now(),updated_at=now() WHERE id=$1 AND profile_id=$2`, document, profile, decision, note, actor)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrDeliveryInvalid
		}
		// A correction request must reopen the application; otherwise the app
		// correctly locks submitted evidence and the partner cannot fix it.
		if decision == "changes_requested" {
			if _, err = tx.Exec(ctx, `UPDATE commerce_delivery_profiles SET status='changes_requested',review_note=$2,reviewed_by=$3,reviewed_at=now() WHERE id=$1`, profile, note, actor); err != nil {
				return err
			}
		}
	} else {
		if decision == "approved" {
			if p.Status != "submitted" {
				return ErrDeliveryReviewConflict
			}
			if err = deliveryDocumentsReady(ctx, tx, p, true); err != nil {
				return err
			}
		} else if p.Status != "submitted" && p.Status != "approved" {
			return ErrDeliveryReviewConflict
		}
		_, err = tx.Exec(ctx, `UPDATE commerce_delivery_profiles SET status=$2,review_note=$3,reviewed_by=$4,reviewed_at=now() WHERE id=$1`, profile, decision, note, actor)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE commerce_delivery_profiles SET revision=revision+1,updated_at=now() WHERE id=$1`, profile)
	if err != nil {
		return err
	}
	audit := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_review_audit(id,profile_id,document_id,actor_id,decision,note) VALUES($1,$2,$3,$4,$5,$6)`, audit, profile, nullableDeliveryID(document), actor, decision, note)
	if err != nil {
		return err
	}
	eventType := "commerce.delivery.application.reviewed"
	if document != uuid.Nil {
		eventType = "commerce.delivery.document.reviewed"
	}
	// Never include document numbers, media references or private reviewer notes.
	payload, _ := json.Marshal(map[string]any{"partner_user_id": p.UserID, "profile_id": p.ID, "status": decision, "occurred_at": time.Now().UTC()})
	if err = s.EnqueueOutboxEvent(ctx, tx, eventType, p.UserID.String(), audit.String(), payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func nullableDeliveryID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func (s *Store) DeliveryReviewQueue(ctx context.Context) ([]DeliveryProfile, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deliveryProfileCols+` FROM commerce_delivery_profiles WHERE status IN ('submitted','changes_requested') ORDER BY updated_at,id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryProfile{}
	for rows.Next() {
		p, err := scanDeliveryProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}
