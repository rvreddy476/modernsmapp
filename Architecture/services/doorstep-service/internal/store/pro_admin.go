package store

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Admin-internal professional review (A2). Every write runs in adminWrite:
// the change, its identity role intent, its doorstep.events message and its
// audit row commit together.

// ProCursor is the keyset position of the admin list (created_at DESC, id DESC).
type ProCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ListProfessionals pages professionals, newest first, optionally by status
// and city.
func (s *Store) ListProfessionals(ctx context.Context, status, city string, after *ProCursor, limit int) ([]model.Professional, error) {
	var at *time.Time
	var id *uuid.UUID
	if after != nil {
		at, id = &after.CreatedAt, &after.ID
	}
	rows, err := s.db.Query(ctx, `SELECT `+proCols+` FROM doorstep.professionals
		WHERE ($1::text = '' OR status = $1) AND ($2::text = '' OR city_code = $2)
		  AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $5`, status, city, at, id, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanPro))
}

// ProDocuments lists one professional's documents, newest first.
func (s *Store) ProDocuments(ctx context.Context, proID uuid.UUID) ([]model.ProDocument, error) {
	rows, err := s.db.Query(ctx, `SELECT `+docCols+` FROM doorstep.pro_documents WHERE pro_id = $1 ORDER BY created_at DESC, id`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanDoc))
}

// ListDocuments is the review queue: police certificates first, then
// oldest first.
func (s *Store) ListDocuments(ctx context.Context, status string) ([]model.ProDocument, error) {
	rows, err := s.db.Query(ctx, `SELECT `+docCols+` FROM doorstep.pro_documents WHERE status = $1
		ORDER BY (kind = 'police_certificate') DESC, created_at, id LIMIT 500`, status)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanDoc))
}

// BackgroundChecks lists one professional's checks, newest first.
func (s *Store) BackgroundChecks(ctx context.Context, proID uuid.UUID) ([]model.BackgroundCheckView, error) {
	rows, err := s.db.Query(ctx, `SELECT id, source, status, to_char(valid_until, 'YYYY-MM-DD') FROM doorstep.background_checks
		WHERE pro_id = $1 ORDER BY created_at DESC, id`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.BackgroundCheckView, error) {
		var b model.BackgroundCheckView
		return b, r.Scan(&b.ID, &b.Source, &b.Status, &b.ValidUntil)
	})
}

// KycChecks lists one professional's KYC checks, newest first. Masked by
// construction: kind, status, score and time only.
func (s *Store) KycChecks(ctx context.Context, proID uuid.UUID) ([]model.KycCheck, error) {
	rows, err := s.db.Query(ctx, `SELECT kind, status, score::float8, verified_at FROM doorstep.pro_kyc_checks
		WHERE pro_id = $1 ORDER BY created_at DESC, id`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.KycCheck, error) {
		var k model.KycCheck
		err := r.Scan(&k.Kind, &k.Status, &k.Score, &k.VerifiedAt)
		utcPtr(&k.VerifiedAt)
		return k, err
	})
}

// StatusCheck runs inside the status-change transaction with the locked
// professional's facts and the categories of their verified skills; an
// error aborts the change (returned as is).
type StatusCheck func(st *ProState, cats []prokyc.SkillCategory) error

// StatusChange is one admin status transition.
type StatusChange struct {
	Action string   // audit action, e.g. professional.approve
	From   []string // statuses the change may start from
	To     string
	Reason *string
	Today  time.Time
	Check  StatusCheck
}

// ChangeProStatus applies an admin status change: lock the row, refuse a
// status outside From (*TransitionError), run Check, write the status, the
// role intent the new status implies (grant, revoke, or none for
// suspended), doorstep.pro.status_changed and the audit row.
func (s *Store) ChangeProStatus(ctx context.Context, a Actor, id uuid.UUID, ch StatusChange) (*model.Professional, error) {
	var out model.Professional
	details := map[string]any{"to_status": ch.To, "reason": ch.Reason}
	err := s.adminWrite(ctx, a, ch.Action, "professional", details, func(tx pgx.Tx) (string, error) {
		st, err := proFacts(ctx, tx, id, ch.Today, true)
		if err != nil {
			return "", err
		}
		allowed := false
		for _, f := range ch.From {
			allowed = allowed || f == st.Status
		}
		if !allowed {
			return "", &TransitionError{Status: st.Status}
		}
		if ch.Check != nil {
			cats, err := verifiedSkillCategories(ctx, tx, id)
			if err != nil {
				return "", err
			}
			if err := ch.Check(st, cats); err != nil {
				return "", err
			}
		}
		details["from_status"] = st.Status
		out, err = scanPro(tx.QueryRow(ctx, `
			UPDATE doorstep.professionals SET status = $2, status_reason = $3, updated_at = NOW(),
			    approved_at = CASE WHEN $2 = 'approved' AND approved_at IS NULL THEN NOW() ELSE approved_at END,
			    approved_by = CASE WHEN $2 = 'approved' AND approved_by IS NULL THEN $4 ELSE approved_by END,
			    on_duty = CASE WHEN $2 = 'approved' THEN on_duty ELSE FALSE END
			WHERE id = $1 RETURNING `+proCols, id, ch.To, ch.Reason, a.UserID))
		if err != nil {
			return "", err
		}
		if err := s.enqueueRoleForStatusTx(ctx, tx, out.UserID, ch.To, "doorstep "+ch.Action); err != nil {
			return "", err
		}
		if err := s.enqueueProEvent(ctx, tx, events.ProStatusChanged, out.UserID, events.ProStatusChangedData{
			ProID: id, ProUserID: out.UserID, FromStatus: st.Status, ToStatus: ch.To, Reason: ch.Reason}); err != nil {
			return "", err
		}
		return id.String(), nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SkillVerifyCheck decides whether an admin may verify a skill: the skill's
// rule, whether an approved trade certificate for it exists, and the
// professional's DigiLocker gender.
type SkillVerifyCheck func(rule *SkillRule, hasApprovedCertificate bool, gender string) error

// VerifySkill sets a declared skill verified or revoked (ErrNotFound if not
// declared), after check.
func (s *Store) VerifySkill(ctx context.Context, a Actor, proID uuid.UUID, code string, verified bool, reason *string, check SkillVerifyCheck) (*model.ProSkill, error) {
	var out model.ProSkill
	details := map[string]any{"pro_id": proID, "skill_code": code, "verified": verified, "reason": reason}
	err := s.adminWrite(ctx, a, "professional.skill_verify", "pro_skill", details, func(tx pgx.Tx) (string, error) {
		if err := requireRow(ctx, tx, `SELECT 1 FROM doorstep.pro_skills WHERE pro_id = $1 AND skill_code = $2 FOR UPDATE`, proID, code); err != nil {
			return "", err
		}
		rules, err := skillRules(ctx, tx, []string{code})
		if err != nil {
			return "", err
		}
		var hasCert bool
		var gender string
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM doorstep.pro_documents WHERE pro_id = $1 AND kind = 'trade_certificate'
			AND skill_code = $2 AND status = 'approved'), COALESCE((SELECT gender FROM doorstep.professionals WHERE id = $1), '')`,
			proID, code).Scan(&hasCert, &gender); err != nil {
			return "", err
		}
		if err := check(rules[code], hasCert, gender); err != nil {
			return "", err
		}
		err = tx.QueryRow(ctx, `UPDATE doorstep.pro_skills SET status = CASE WHEN $3 THEN 'verified' ELSE 'revoked' END,
			    verified_at = CASE WHEN $3 THEN NOW() END, verified_by = CASE WHEN $3 THEN $4::uuid END
			WHERE pro_id = $1 AND skill_code = $2 RETURNING skill_code, status, verified_at`, proID, code, verified, a.UserID).
			Scan(&out.SkillCode, &out.Status, &out.VerifiedAt)
		utcPtr(&out.VerifiedAt)
		return proID.String() + ":" + code, err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DocumentCheck runs on the locked pending document before a decision.
type DocumentCheck func(doc *model.ProDocument) error

// DecideDocument approves or rejects a pending document (else
// *TransitionError). A police certificate's decision is its background
// check's: approve → clear from issued_on until issued_on + 12 months;
// reject → failed. An approved trade certificate verifies its skill. Emits
// doorstep.pro.document_reviewed.
func (s *Store) DecideDocument(ctx context.Context, a Actor, id uuid.UUID, approve bool, reason *string, check DocumentCheck) (*model.ProDocument, error) {
	decision := "rejected"
	if approve {
		decision = "approved"
	}
	var out model.ProDocument
	details := map[string]any{"decision": decision, "reason": reason}
	err := s.adminWrite(ctx, a, "document.decide", "pro_document", details, func(tx pgx.Tx) (string, error) {
		doc, err := scanDoc(tx.QueryRow(ctx, `SELECT `+docCols+` FROM doorstep.pro_documents WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return "", err
		}
		if doc.Status != "pending" {
			return "", &TransitionError{Status: doc.Status}
		}
		if check != nil {
			if err := check(&doc); err != nil {
				return "", err
			}
		}
		out, err = scanDoc(tx.QueryRow(ctx, `UPDATE doorstep.pro_documents SET status = $2, reason = $3, reviewed_by = $4, reviewed_at = NOW()
			WHERE id = $1 RETURNING `+docCols, id, decision, reason, a.UserID))
		if err != nil {
			return "", err
		}
		switch doc.Kind {
		case "police_certificate":
			// The order matters: the database refuses a clear check whose
			// certificate is not approved, and it was approved just above.
			if _, err := tx.Exec(ctx, `UPDATE doorstep.background_checks SET
				    status = CASE WHEN $2 THEN 'clear' ELSE 'failed' END,
				    valid_from = CASE WHEN $2 THEN d.issued_on END,
				    valid_until = CASE WHEN $2 THEN (d.issued_on + INTERVAL '12 months')::date END,
				    reviewed_by = $3, updated_at = NOW()
				FROM doorstep.pro_documents d
				WHERE background_checks.document_id = $1 AND d.id = $1 AND background_checks.status = 'pending'`,
				id, approve, a.UserID); err != nil {
				return "", err
			}
		case "selfie":
			// A selfie the face match left pending, approved by a human.
			if approve {
				if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_kyc_checks (pro_id, kind, status, provider, details, verified_at)
					VALUES ($1, 'selfie_face_match', 'passed', 'admin_review', jsonb_build_object('selfie_media_id', $2::text, 'document_id', $3::text), NOW())`,
					doc.ProID, doc.MediaID, id.String()); err != nil {
					return "", err
				}
			}
		case "trade_certificate":
			if approve && doc.SkillCode != nil {
				if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_skills SET status = 'verified', verified_at = NOW(), verified_by = $3
					WHERE pro_id = $1 AND skill_code = $2 AND status <> 'verified'`, doc.ProID, *doc.SkillCode, a.UserID); err != nil {
					return "", err
				}
			}
		}
		var userID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT user_id FROM doorstep.professionals WHERE id = $1`, doc.ProID).Scan(&userID); err != nil {
			return "", err
		}
		if err := s.enqueueProEvent(ctx, tx, events.ProDocumentReviewed, userID, events.ProDocumentReviewedData{
			ProID: doc.ProID, ProUserID: userID, DocumentID: id, Kind: doc.Kind, Decision: decision}); err != nil {
			return "", err
		}
		details["kind"] = doc.Kind
		details["pro_id"] = doc.ProID
		return id.String(), nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DocumentByID is one document, or ErrNotFound.
func (s *Store) DocumentByID(ctx context.Context, id uuid.UUID) (*model.ProDocument, error) {
	d, err := scanDoc(s.db.QueryRow(ctx, `SELECT `+docCols+` FROM doorstep.pro_documents WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// AuditDocumentView writes the audit row of one admin view of a document's
// image (one row per view, before the bytes are sent).
func (s *Store) AuditDocumentView(ctx context.Context, a Actor, doc *model.ProDocument) error {
	details := map[string]any{"pro_id": doc.ProID, "kind": doc.Kind, "media_id": doc.MediaID}
	return s.adminWrite(ctx, a, "document.view", "pro_document", details, func(pgx.Tx) (string, error) {
		return doc.ID.String(), nil
	})
}

// IsTransition reports a *TransitionError and its status.
func IsTransition(err error) (string, bool) {
	var te *TransitionError
	if errors.As(err, &te) {
		return te.Status, true
	}
	return "", false
}
