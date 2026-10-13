package store

import (
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ApplicationSubmission struct {
	UserID          uuid.UUID
	Input           model.PartnerApplicationInput
	StateName       string
	GSTStateCode    string
	PhoneLast4      string
	AlternateSealed []byte
	AlternateLast4  *string
	Skills          []SkillDecl
	Bank            BankSubmission
	PANSealed       []byte
	PANLast4        string
}

func (s *Store) PartnerApplication(ctx context.Context, id uuid.UUID) (*model.PartnerApplication, error) {
	var a model.PartnerApplication
	err := s.db.QueryRow(ctx, `SELECT first_name,last_name,declared_gender,state_code,state_name,town,
	 primary_phone_last4,alternate_phone_last4,alternate_phone_verified_at,submitted_at FROM doorstep.pro_applications WHERE pro_id=$1`, id).Scan(
		&a.FirstName, &a.LastName, &a.DeclaredGender, &a.StateCode, &a.StateName, &a.Town, &a.PrimaryPhoneLast4, &a.AlternatePhoneLast4, &a.AlternatePhoneVerifiedAt, &a.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

func (s *Store) SubmitPartnerApplication(ctx context.Context, in ApplicationSubmission) (*model.Professional, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.UserID.String()); err != nil {
		return nil, err
	}
	var p model.Professional
	p, err = scanPro(tx.QueryRow(ctx, `SELECT `+proCols+` FROM doorstep.professionals WHERE user_id=$1 FOR UPDATE`, in.UserID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err == nil {
		var already bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM doorstep.pro_applications WHERE pro_id=$1)`, p.ID).Scan(&already); err != nil {
			return nil, err
		}
		if already {
			_ = tx.Rollback(ctx)
			return s.ProfessionalByUser(ctx, in.UserID)
		}
		if p.Status != "draft" {
			return nil, &TransitionError{Status: p.Status}
		}
	} else {
		p, err = scanPro(tx.QueryRow(ctx, `INSERT INTO doorstep.professionals(user_id,display_name,city_code,status)
		 VALUES($1,$2,(SELECT code FROM doorstep.cities WHERE lower(name)=lower($3) AND state_code=$4 LIMIT 1),'draft') RETURNING `+proCols, in.UserID, in.Input.FirstName+" "+in.Input.LastName, in.Input.Town, in.GSTStateCode))
		if err != nil {
			return nil, err
		}
	}
	// New skills are declared only; retain legitimate prior admin reviews.
	if _, err = tx.Exec(ctx, `DELETE FROM doorstep.pro_skills WHERE pro_id=$1 AND NOT (skill_code=ANY($2))`, p.ID, in.Input.SkillCodes); err != nil {
		return nil, err
	}
	if err = insertSkills(ctx, tx, p.ID, in.Skills); err != nil {
		return nil, err
	}
	if len(in.PANSealed) == 0 {
		return nil, ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE doorstep.professionals SET pan_sealed=$2,pan_last4=$3 WHERE id=$1`, p.ID, in.PANSealed, in.PANLast4); err != nil {
		return nil, err
	}
	if _, err = insertBank(ctx, tx, p.ID, in.Bank); err != nil {
		return nil, err
	}
	identityID := uuid.New()
	if in.Input.IntroductionMediaID != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO doorstep.pro_documents(pro_id,kind,media_id,status) VALUES($1,'introduction_video',$2,'pending')`, p.ID, in.Input.IntroductionMediaID); err != nil {
			return nil, err
		}
	}
	// Selfie is private review evidence, not a public profile photo. It is
	// committed with the rest of the application and never auto-approved.
	if _, err = tx.Exec(ctx, `UPDATE doorstep.pro_documents SET status='rejected',reason='superseded by application selfie',reviewed_at=NOW() WHERE pro_id=$1 AND kind='selfie' AND status IN ('pending','approved')`, p.ID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE doorstep.pro_kyc_checks SET status='expired' WHERE pro_id=$1 AND kind IN ('selfie_face_match','manual_identity') AND status='passed'`, p.ID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE doorstep.professionals SET gender=NULL,gender_source=NULL WHERE id=$1 AND gender_source='admin_review'`, p.ID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO doorstep.pro_documents(pro_id,kind,media_id,status) VALUES($1,'selfie',$2,'pending')`, p.ID, in.Input.SelfieMediaID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO doorstep.pro_kyc_checks(pro_id,kind,status,provider,details) VALUES($1,'selfie_face_match','pending','manual_review',jsonb_build_object('selfie_media_id',$2::text))`, p.ID, in.Input.SelfieMediaID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO doorstep.pro_documents(id,pro_id,kind,media_id,status) VALUES($1,$2,$3,$4,'pending')`, identityID, p.ID, in.Input.IdentityKind, in.Input.IdentityMediaID); err != nil {
		return nil, err
	}
	if in.Input.ExperienceMediaID != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO doorstep.pro_documents(id,pro_id,kind,media_id,status) VALUES($1,$2,'experience_certificate',$3,'pending')`, uuid.New(), p.ID, in.Input.ExperienceMediaID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO doorstep.pro_applications(pro_id,first_name,last_name,declared_gender,state_code,state_name,town,primary_phone_last4,alternate_phone_sealed,alternate_phone_last4,identity_document_id)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, p.ID, in.Input.FirstName, in.Input.LastName, in.Input.Gender, in.Input.StateCode, in.StateName, in.Input.Town, in.PhoneLast4, in.AlternateSealed, in.AlternateLast4, identityID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE doorstep.professionals SET status='pending_verification',display_name=$2,city_code=(SELECT code FROM doorstep.cities WHERE lower(name)=lower($3) AND state_code=$4 LIMIT 1),updated_at=NOW() WHERE id=$1`, p.ID, in.Input.FirstName+" "+in.Input.LastName, in.Input.Town, in.GSTStateCode); err != nil {
		return nil, err
	}
	if err = s.enqueueRoleForStatusTx(ctx, tx, p.UserID, "pending_verification", "partner application received"); err != nil {
		return nil, err
	}
	if err = s.enqueueProEvent(ctx, tx, events.ProStatusChanged, p.UserID, events.ProStatusChangedData{ProID: p.ID, ProUserID: p.UserID, FromStatus: "draft", ToStatus: "pending_verification"}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ProfessionalByUser(ctx, in.UserID)
}
