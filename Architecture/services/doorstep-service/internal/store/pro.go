package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Professional onboarding (A2). Every statement names the doorstep schema.
// Writes that change a professional's status enqueue the identity role
// intent and the doorstep.events message in the same transaction.

var (
	// ErrStateUsed / ErrStateExpired: a DigiLocker state already consumed or
	// past its expiry. A state that does not exist or belongs to another
	// user is ErrNotFound (no ownership oracle).
	ErrStateUsed    = errors.New("store: digilocker state already used")
	ErrStateExpired = errors.New("store: digilocker state expired")
	// ErrGenderMismatch: a second DigiLocker verification disagrees with the
	// gender already recorded (a different person's account).
	ErrGenderMismatch = errors.New("store: digilocker gender differs from the recorded one")
	// ErrDuplicateIdentity: the DigiLocker account already verified another
	// professional.
	ErrDuplicateIdentity = errors.New("store: digilocker account verified another professional")
)

// TransitionError is a status change the current status does not allow.
type TransitionError struct{ Status string }

func (e *TransitionError) Error() string { return "store: transition not allowed from " + e.Status }

// querier is a pool or a transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

const proCols = `id, user_id, status, display_name, city_code, gender, photo_media_id, rating_sum, rating_count,
	jobs_completed, max_jobs_per_day, created_at, updated_at`

func scanPro(r pgx.Row) (model.Professional, error) {
	var p model.Professional
	var sum int64
	err := r.Scan(&p.ID, &p.UserID, &p.Status, &p.DisplayName, &p.CityCode, &p.Gender, &p.PhotoMediaID, &sum, &p.RatingCount,
		&p.JobsCompleted, &p.MaxJobsPerDay, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, mapErr(err)
	}
	if p.RatingCount > 0 {
		avg := math.Round(float64(sum)/float64(p.RatingCount)*100) / 100
		p.RatingAvg = &avg
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, nil
}

func (s *Store) enqueueProEvent(ctx context.Context, tx pgx.Tx, eventType string, proUserID uuid.UUID, data any) error {
	key, payload, err := events.Pro(eventType, proUserID, time.Now(), data)
	if err != nil {
		return err
	}
	return s.events.Enqueue(ctx, tx, eventType, key, payload)
}

// SkillDecl is one declared skill and whether declaring verifies it.
type SkillDecl struct {
	Code     string
	Verified bool
}

// NewProfessional is an application.
type NewProfessional struct {
	UserID      uuid.UUID
	DisplayName string
	CityCode    string
	Skills      []SkillDecl
}

// CreateProfessional inserts a draft professional, its declared skills, the
// service_professional grant and doorstep.pro.applied in one transaction.
// The user already having one is ErrConflict; an unknown or inactive city
// ErrBadReference.
func (s *Store) CreateProfessional(ctx context.Context, in NewProfessional) (*model.Professional, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `SELECT active FROM doorstep.cities WHERE code = $1`, in.CityCode).Scan(&active); err != nil || !active {
		if err == nil || errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBadReference
		}
		return nil, err
	}
	p, err := scanPro(tx.QueryRow(ctx, `
		INSERT INTO doorstep.professionals (user_id, display_name, city_code, status)
		VALUES ($1, $2, $3, 'draft') RETURNING `+proCols, in.UserID, in.DisplayName, in.CityCode))
	if err != nil {
		return nil, err
	}
	if err := insertSkills(ctx, tx, p.ID, in.Skills); err != nil {
		return nil, mapErr(err)
	}
	if err := s.enqueueRoleForStatusTx(ctx, tx, p.UserID, p.Status, "doorstep professional applied"); err != nil {
		return nil, err
	}
	if err := s.enqueueProEvent(ctx, tx, events.ProApplied, p.UserID, events.ProAppliedData{ProID: p.ID, ProUserID: p.UserID, CityCode: p.CityCode}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

// insertSkills declares skills. A certificate-free skill is verified at once;
// a certificate skill is verified when an approved trade certificate for it
// already exists, else pending. Existing rows keep their status.
func insertSkills(ctx context.Context, tx pgx.Tx, proID uuid.UUID, decls []SkillDecl) error {
	for _, d := range decls {
		if _, err := tx.Exec(ctx, `
			INSERT INTO doorstep.pro_skills (pro_id, skill_code, status, verified_at)
			SELECT $1, $2, CASE WHEN v THEN 'verified' ELSE 'pending' END, CASE WHEN v THEN NOW() END
			FROM (SELECT $3::bool OR EXISTS (
			          SELECT 1 FROM doorstep.pro_documents d
			           WHERE d.pro_id = $1 AND d.kind = 'trade_certificate' AND d.skill_code = $2 AND d.status = 'approved') AS v) x
			ON CONFLICT (pro_id, skill_code) DO NOTHING`, proID, d.Code, d.Verified); err != nil {
			return err
		}
	}
	return nil
}

// ProfessionalByUser is the caller's professional, or ErrNotFound.
func (s *Store) ProfessionalByUser(ctx context.Context, userID uuid.UUID) (*model.Professional, error) {
	p, err := scanPro(s.db.QueryRow(ctx, `SELECT `+proCols+` FROM doorstep.professionals WHERE user_id = $1`, userID))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ProfessionalByID is one professional, or ErrNotFound.
func (s *Store) ProfessionalByID(ctx context.Context, id uuid.UUID) (*model.Professional, error) {
	p, err := scanPro(s.db.QueryRow(ctx, `SELECT `+proCols+` FROM doorstep.professionals WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateProfile patches the display name and photo.
func (s *Store) UpdateProfile(ctx context.Context, proID uuid.UUID, name, photo *string) (*model.Professional, error) {
	p, err := scanPro(s.db.QueryRow(ctx, `
		UPDATE doorstep.professionals SET display_name = COALESCE($2::text, display_name),
		    photo_media_id = COALESCE($3::text, photo_media_id), updated_at = NOW()
		WHERE id = $1 RETURNING `+proCols, proID, name, photo))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ProState is a professional's status and onboarding facts.
type ProState struct {
	prokyc.Facts
	ProID             uuid.UUID
	UserID            uuid.UUID
	Status            string
	Gender            string
	IncidentSuspended bool
}

// ProFacts reads the onboarding facts on today's date (Asia/Kolkata).
func (s *Store) ProFacts(ctx context.Context, proID uuid.UUID, today time.Time) (*ProState, error) {
	return proFacts(ctx, s.db, proID, today, false)
}

func proFacts(ctx context.Context, q querier, proID uuid.UUID, today time.Time, lock bool) (*ProState, error) {
	forUpdate := ""
	if lock {
		forUpdate = " FOR UPDATE OF p"
	}
	var st ProState
	var verified, awaiting int64
	err := q.QueryRow(ctx, `
		SELECT p.id, p.user_id, p.status, COALESCE(p.gender, ''), p.incident_suspended, p.display_name, p.photo_media_id IS NOT NULL,
		    (p.gender IS NOT NULL AND p.gender_source = 'digilocker' AND EXISTS (
		        SELECT 1 FROM doorstep.pro_kyc_checks k WHERE k.pro_id = p.id AND k.kind = 'digilocker_aadhaar'
		           AND k.status = 'passed' AND (k.expires_at IS NULL OR k.expires_at > NOW()))),
		    EXISTS (SELECT 1 FROM doorstep.pro_kyc_checks k WHERE k.pro_id = p.id AND k.kind = 'selfie_face_match' AND k.status = 'passed'),
		    (SELECT count(*) FROM doorstep.pro_skills ps WHERE ps.pro_id = p.id AND ps.status = 'verified'),
		    (SELECT count(*) FROM doorstep.pro_skills ps WHERE ps.pro_id = p.id AND ps.status = 'pending' AND EXISTS (
		        SELECT 1 FROM doorstep.pro_documents d WHERE d.pro_id = p.id AND d.kind = 'trade_certificate'
		           AND d.skill_code = ps.skill_code AND d.status = 'pending')),
		    (p.home_point IS NOT NULL AND EXISTS (SELECT 1 FROM doorstep.pro_zones z WHERE z.pro_id = p.id)),
		    EXISTS (SELECT 1 FROM doorstep.pro_weekly_hours h WHERE h.pro_id = p.id),
		    EXISTS (SELECT 1 FROM doorstep.pro_payout_accounts a WHERE a.pro_id = p.id AND a.active AND a.status <> 'failed'),
		    EXISTS (SELECT 1 FROM doorstep.background_checks b WHERE b.pro_id = p.id AND b.status = 'clear'
		               AND b.valid_from <= $2::date AND b.valid_until > $2::date),
		    EXISTS (SELECT 1 FROM doorstep.pro_documents d WHERE d.pro_id = p.id AND d.kind = 'police_certificate' AND d.status = 'pending'),
		    COALESCE(p.agreement_version, ''), p.pan_sealed IS NOT NULL
		FROM doorstep.professionals p WHERE p.id = $1`+forUpdate, proID, today.Format("2006-01-02")).Scan(
		&st.ProID, &st.UserID, &st.Status, &st.Gender, &st.IncidentSuspended, &st.DisplayName, &st.HasPhoto,
		&st.AadhaarVerified, &st.SelfieMatched, &verified, &awaiting, &st.HasServiceArea, &st.HasWeeklyHours,
		&st.HasPayoutAccount, &st.BackgroundClear, &st.PoliceCertificatePending, &st.AgreementVersion, &st.HasPAN)
	if err != nil {
		return nil, mapErr(err)
	}
	st.VerifiedSkills, st.SkillsAwaitingReview = int(verified), int(awaiting)
	return &st, nil
}

// MarkPendingVerification moves a draft professional to pending_verification
// (everything they can do is done). A professional not in draft is left as
// is and reports false.
func (s *Store) MarkPendingVerification(ctx context.Context, proID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE doorstep.professionals SET status = 'pending_verification', updated_at = NOW()
		WHERE id = $1 AND status = 'draft' RETURNING user_id`, proID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := s.enqueueRoleForStatusTx(ctx, tx, userID, "pending_verification", "doorstep professional submitted for verification"); err != nil {
		return false, err
	}
	if err := s.enqueueProEvent(ctx, tx, events.ProStatusChanged, userID, events.ProStatusChangedData{
		ProID: proID, ProUserID: userID, FromStatus: "draft", ToStatus: "pending_verification"}); err != nil {
		return false, err
	}
	return true, mapErr(tx.Commit(ctx))
}

// ---------------------------------------------------------------- DigiLocker

// CreateDigiLockerState stores a state hash and sealed verifier for the
// professional, and drops their states a day past expiry.
func (s *Store) CreateDigiLockerState(ctx context.Context, proID uuid.UUID, stateHash string, verifierSealed []byte, expiresAt time.Time) error {
	if len(stateHash) != 64 || len(verifierSealed) == 0 {
		return errors.New("store: digilocker state must arrive hashed, with a sealed verifier")
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO doorstep.digilocker_auth_states (state_hash, pro_id, code_verifier_sealed, expires_at)
		VALUES ($1, $2, $3, $4)`, stateHash, proID, verifierSealed, expiresAt); err != nil {
		return mapErr(err)
	}
	_, err := s.db.Exec(ctx, `DELETE FROM doorstep.digilocker_auth_states WHERE pro_id = $1 AND expires_at < NOW() - INTERVAL '1 day'`, proID)
	return err
}

// ConsumeDigiLockerState marks the state used exactly once. The single
// conditional UPDATE is the guard: the state must exist, belong to the
// professional owned by userID, be unconsumed and unexpired. A stranger's
// attempt does not consume the owner's state.
func (s *Store) ConsumeDigiLockerState(ctx context.Context, userID uuid.UUID, stateHash string) (uuid.UUID, []byte, error) {
	var proID uuid.UUID
	var sealed []byte
	err := s.db.QueryRow(ctx, `
		UPDATE doorstep.digilocker_auth_states st SET consumed_at = NOW()
		FROM doorstep.professionals p
		WHERE st.state_hash = $1 AND p.id = st.pro_id AND p.user_id = $2
		  AND st.consumed_at IS NULL AND st.expires_at > NOW()
		RETURNING st.pro_id, st.code_verifier_sealed`, stateHash, userID).Scan(&proID, &sealed)
	if err == nil {
		return proID, sealed, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, err
	}
	var owner, used, expired bool
	derr := s.db.QueryRow(ctx, `
		SELECT p.user_id = $2, st.consumed_at IS NOT NULL, st.expires_at <= NOW()
		FROM doorstep.digilocker_auth_states st JOIN doorstep.professionals p ON p.id = st.pro_id
		WHERE st.state_hash = $1`, stateHash, userID).Scan(&owner, &used, &expired)
	switch {
	case errors.Is(derr, pgx.ErrNoRows), derr == nil && !owner:
		return uuid.Nil, nil, ErrNotFound
	case derr != nil:
		return uuid.Nil, nil, derr
	case used:
		return uuid.Nil, nil, ErrStateUsed
	case expired:
		return uuid.Nil, nil, ErrStateExpired
	}
	return uuid.Nil, nil, ErrStateUsed
}

// AadhaarRecord is a verified DigiLocker Aadhaar result. No number.
type AadhaarRecord struct {
	Provider     string
	Reference    string
	DocTypeHash  string
	Gender       string
	PhotoMediaID *uuid.UUID
}

// RecordAadhaar records the passed check and sets the professional's gender
// from it (gender_source = digilocker), in one transaction. It is the ONLY
// writer of professionals.gender. A gender that differs from one already
// recorded is ErrGenderMismatch; a DigiLocker account that verified another
// professional is ErrDuplicateIdentity.
func (s *Store) RecordAadhaar(ctx context.Context, proID uuid.UUID, rec AadhaarRecord) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM doorstep.pro_kyc_checks
		WHERE kind = 'digilocker_aadhaar' AND status = 'passed' AND reference = $1 AND pro_id <> $2)`, rec.Reference, proID).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return ErrDuplicateIdentity
	}
	details := map[string]any{"doc_type_hash": rec.DocTypeHash}
	if rec.PhotoMediaID != nil {
		details["photo_media_id"] = rec.PhotoMediaID.String()
	}
	raw, _ := json.Marshal(details)
	tag, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET gender = $2, gender_source = 'digilocker', updated_at = NOW()
		WHERE id = $1 AND (gender IS NULL OR gender = $2)`, proID, rec.Gender)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrGenderMismatch
	}
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_kyc_checks (pro_id, kind, status, provider, reference, details, verified_at)
		VALUES ($1, 'digilocker_aadhaar', 'passed', $2, $3, $4, NOW())`, proID, rec.Provider, rec.Reference, raw); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

// AadhaarReference reports whether a passed Aadhaar check exists and the
// reference face it carries (nil when the provider supplied none).
func (s *Store) AadhaarReference(ctx context.Context, proID uuid.UUID) (bool, *uuid.UUID, error) {
	var photo *string
	err := s.db.QueryRow(ctx, `SELECT details->>'photo_media_id' FROM doorstep.pro_kyc_checks
		WHERE pro_id = $1 AND kind = 'digilocker_aadhaar' AND status = 'passed' AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC LIMIT 1`, proID).Scan(&photo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if photo == nil {
		return true, nil, nil
	}
	id, err := uuid.Parse(*photo)
	if err != nil {
		return true, nil, nil
	}
	return true, &id, nil
}

// RecordSelfie records one selfie face-match attempt and the selfie as a
// document (so an admin can view it, and decide it when the match left it
// pending): passed → approved, pending → pending, failed → rejected. A
// selfie still pending review is superseded by the new one.
func (s *Store) RecordSelfie(ctx context.Context, proID, mediaID uuid.UUID, status, provider string, score *float64, details map[string]any) (*model.KycCheck, error) {
	raw, _ := json.Marshal(details)
	docStatus := map[string]string{"passed": "approved", "pending": "pending", "failed": "rejected"}[status]
	if docStatus == "" {
		return nil, ErrInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_documents SET status = 'rejected', reason = 'superseded by a newer selfie', reviewed_at = NOW()
		WHERE pro_id = $1 AND kind = 'selfie' AND status = 'pending'`, proID); err != nil {
		return nil, err
	}
	var reason *string
	if r, ok := details["reason"].(string); ok && docStatus != "approved" {
		reason = &r
	}
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_documents (pro_id, kind, media_id, status, reason, reviewed_at)
		VALUES ($1, 'selfie', $2, $3, $4, CASE WHEN $3 <> 'pending' THEN NOW() END)`, proID, mediaID.String(), docStatus, reason); err != nil {
		return nil, mapErr(err)
	}
	var out model.KycCheck
	err = tx.QueryRow(ctx, `INSERT INTO doorstep.pro_kyc_checks (pro_id, kind, status, provider, score, details, verified_at)
		VALUES ($1, 'selfie_face_match', $2, $3, $4, $5, CASE WHEN $2 = 'passed' THEN NOW() END)
		RETURNING kind, status, score::float8, verified_at`, proID, status, provider, score, raw).
		Scan(&out.Kind, &out.Status, &out.Score, &out.VerifiedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	utcPtr(&out.VerifiedAt)
	return &out, mapErr(tx.Commit(ctx))
}

func utcPtr(t **time.Time) {
	if *t != nil {
		u := (*t).UTC()
		*t = &u
	}
}

// ---------------------------------------------------------------- skills

// SkillRule is a skill's certificate rule and every category using it.
type SkillRule struct {
	Code                string
	RequiresCertificate bool
	Categories          []prokyc.SkillCategory
}

// SkillRules returns the rule of each known code (unknown codes absent).
// Categories count whether active or not: the gender rule fails closed.
func (s *Store) SkillRules(ctx context.Context, codes []string) (map[string]*SkillRule, error) {
	return skillRules(ctx, s.db, codes)
}

func skillRules(ctx context.Context, q querier, codes []string) (map[string]*SkillRule, error) {
	rows, err := q.Query(ctx, `
		SELECT k.code, k.requires_certificate, c.family, c.gender_rule
		FROM doorstep.skills k
		LEFT JOIN doorstep.services sv ON sv.required_skill = k.code
		LEFT JOIN doorstep.categories c ON c.id = sv.category_id
		WHERE k.code = ANY($1)`, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*SkillRule{}
	seen := map[string]bool{}
	for rows.Next() {
		var code string
		var req bool
		var family, rule *string
		if err := rows.Scan(&code, &req, &family, &rule); err != nil {
			return nil, err
		}
		r := out[code]
		if r == nil {
			r = &SkillRule{Code: code, RequiresCertificate: req}
			out[code] = r
		}
		if family != nil && rule != nil {
			key := code + "|" + *family + "|" + *rule
			if !seen[key] {
				seen[key] = true
				r.Categories = append(r.Categories, prokyc.SkillCategory{Skill: code, Family: *family, GenderRule: *rule})
			}
		}
	}
	return out, rows.Err()
}

// VerifiedSkillCategories are the categories of the professional's verified
// skills (approval rules).
func verifiedSkillCategories(ctx context.Context, q querier, proID uuid.UUID) ([]prokyc.SkillCategory, error) {
	rows, err := q.Query(ctx, `SELECT skill_code FROM doorstep.pro_skills WHERE pro_id = $1 AND status = 'verified'`, proID)
	if err != nil {
		return nil, err
	}
	codes, err := collect(rows, func(r pgx.Rows) (string, error) {
		var c string
		return c, r.Scan(&c)
	})
	if err != nil {
		return nil, err
	}
	rules, err := skillRules(ctx, q, codes)
	if err != nil {
		return nil, err
	}
	var out []prokyc.SkillCategory
	for _, c := range codes {
		if r := rules[c]; r != nil {
			out = append(out, r.Categories...)
		}
	}
	return out, nil
}

// ReplaceSkills sets the declared skills: codes not listed are removed,
// listed ones are declared (insertSkills), existing rows keep their status.
func (s *Store) ReplaceSkills(ctx context.Context, proID uuid.UUID, decls []SkillDecl) ([]model.ProSkill, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	codes := make([]string, 0, len(decls))
	for _, d := range decls {
		codes = append(codes, d.Code)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM doorstep.pro_skills WHERE pro_id = $1 AND NOT (skill_code = ANY($2))`, proID, codes); err != nil {
		return nil, err
	}
	if err := insertSkills(ctx, tx, proID, decls); err != nil {
		return nil, mapErr(err)
	}
	out, err := proSkills(ctx, tx, proID)
	if err != nil {
		return nil, err
	}
	return out, mapErr(tx.Commit(ctx))
}

// ProSkills lists the professional's skills.
func (s *Store) ProSkills(ctx context.Context, proID uuid.UUID) ([]model.ProSkill, error) {
	return proSkills(ctx, s.db, proID)
}

func proSkills(ctx context.Context, q querier, proID uuid.UUID) ([]model.ProSkill, error) {
	rows, err := q.Query(ctx, `SELECT skill_code, status, verified_at FROM doorstep.pro_skills WHERE pro_id = $1 ORDER BY skill_code`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.ProSkill, error) {
		var k model.ProSkill
		err := r.Scan(&k.SkillCode, &k.Status, &k.VerifiedAt)
		utcPtr(&k.VerifiedAt)
		return k, err
	})
}

// ---------------------------------------------------------------- documents

const docCols = `id, pro_id, kind, skill_code, media_id, status, to_char(issued_on, 'YYYY-MM-DD'), to_char(expires_on, 'YYYY-MM-DD'), reason, created_at`

func scanDoc(r pgx.Row) (model.ProDocument, error) {
	var d model.ProDocument
	err := r.Scan(&d.ID, &d.ProID, &d.Kind, &d.SkillCode, &d.MediaID, &d.Status, &d.IssuedOn, &d.ExpiresOn, &d.Reason, &d.CreatedAt)
	d.CreatedAt = d.CreatedAt.UTC()
	return d, mapErr(err)
}

// NewDocument is a certificate upload. NumberSealed is sealed already.
type NewDocument struct {
	ID           uuid.UUID
	ProID        uuid.UUID
	Kind         string // police_certificate | trade_certificate
	SkillCode    *string
	MediaID      uuid.UUID
	NumberSealed []byte
	NumberLast4  *string
	IssuedOn     time.Time
	ExpiresOn    *time.Time
}

// BackgroundInit is the background check a police certificate starts: the
// provider's first answer.
type BackgroundInit struct {
	Provider   string
	Status     string
	ValidFrom  *time.Time
	ValidUntil *time.Time
}

// UploadDocument stores a certificate pending review. A police certificate
// also starts a background_checks row (source uploaded_document) bound to
// it. When the provider cleared the check at once (mock, development only)
// the certificate is approved in the same transaction, because the database
// refuses a clear check whose certificate is not approved. A trade
// certificate needs the skill declared (else ErrNotFound). One certificate
// under review at a time (else ErrConflict).
func (s *Store) UploadDocument(ctx context.Context, d NewDocument, bg *BackgroundInit) (*model.ProDocument, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if d.Kind == "trade_certificate" {
		if err := requireRow(ctx, tx, `SELECT 1 FROM doorstep.pro_skills WHERE pro_id = $1 AND skill_code = $2`, d.ProID, d.SkillCode); err != nil {
			return nil, err
		}
	}
	status, reason := "pending", (*string)(nil)
	autoClear := bg != nil && bg.Status == "clear"
	if autoClear {
		status = "approved"
		r := "cleared by the " + bg.Provider + " background check"
		reason = &r
	}
	doc, err := scanDoc(tx.QueryRow(ctx, `
		INSERT INTO doorstep.pro_documents (id, pro_id, kind, skill_code, media_id, number_sealed, number_last4, status, issued_on, expires_on, reason, reviewed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, CASE WHEN $8 = 'approved' THEN NOW() END)
		RETURNING `+docCols, d.ID, d.ProID, d.Kind, d.SkillCode, d.MediaID.String(), d.NumberSealed, d.NumberLast4, status,
		d.IssuedOn.Format("2006-01-02"), datePtr(d.ExpiresOn), reason))
	if err != nil {
		return nil, err
	}
	if bg != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO doorstep.background_checks (pro_id, source, provider, document_id, status, valid_from, valid_until)
			VALUES ($1, 'uploaded_document', $2, $3, $4, $5, $6)`,
			d.ProID, bg.Provider, d.ID, bg.Status, datePtr(bg.ValidFrom), datePtr(bg.ValidUntil)); err != nil {
			return nil, mapErr(err)
		}
	}
	return &doc, mapErr(tx.Commit(ctx))
}

func datePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

// ---------------------------------------------------------------- area, hours, days off

// SetArea replaces the service area. Every zone must be an active zone of
// the professional's city (else ErrBadReference).
func (s *Store) SetArea(ctx context.Context, proID uuid.UUID, zoneIDs []uuid.UUID, lat, lng float64, radiusM int) (*model.ProArea, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM doorstep.zones z JOIN doorstep.professionals p ON p.city_code = z.city_code
		WHERE p.id = $1 AND z.id = ANY($2::uuid[]) AND z.active`, proID, uuidStrings(zoneIDs)).Scan(&n); err != nil {
		return nil, err
	}
	if n != len(zoneIDs) {
		return nil, ErrBadReference
	}
	tag, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET home_point = ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography,
		service_radius_m = $4, updated_at = NOW() WHERE id = $1`, proID, lng, lat, radiusM)
	if err != nil {
		return nil, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM doorstep.pro_zones WHERE pro_id = $1`, proID); err != nil {
		return nil, err
	}
	for _, z := range zoneIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_zones (pro_id, zone_id) VALUES ($1, $2)`, proID, z); err != nil {
			return nil, mapErr(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return &model.ProArea{ZoneIDs: zoneIDs, HomeLat: &lat, HomeLng: &lng, RadiusM: radiusM}, nil
}

// ProZoneIDs lists the professional's zones.
func (s *Store) ProZoneIDs(ctx context.Context, proID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT zone_id FROM doorstep.pro_zones WHERE pro_id = $1 ORDER BY zone_id`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (uuid.UUID, error) {
		var id uuid.UUID
		return id, r.Scan(&id)
	})
}

// ReplaceHours replaces the weekly hours (validated by prokyc).
func (s *Store) ReplaceHours(ctx context.Context, proID uuid.UUID, ws []prokyc.Window) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM doorstep.pro_weekly_hours WHERE pro_id = $1`, proID); err != nil {
		return err
	}
	for _, w := range ws {
		if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_weekly_hours (pro_id, weekday, start_time, end_time)
			VALUES ($1, $2, $3::time, $4::time)`, proID, w.Weekday, w.Start, w.End); err != nil {
			return mapErr(err)
		}
	}
	return mapErr(tx.Commit(ctx))
}

// Hours lists the weekly hours by weekday and start.
func (s *Store) Hours(ctx context.Context, proID uuid.UUID) ([]model.HoursWindow, error) {
	rows, err := s.db.Query(ctx, `SELECT weekday, to_char(start_time, 'HH24:MI'), to_char(end_time, 'HH24:MI')
		FROM doorstep.pro_weekly_hours WHERE pro_id = $1 ORDER BY weekday, start_time`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.HoursWindow, error) {
		var w model.HoursWindow
		return w, r.Scan(&w.Weekday, &w.Start, &w.End)
	})
}

// AddDayOff records a day off and its day_off calendar block over
// [from, to). A day already off is ErrConflict; a job or hold on the
// calendar that day is ErrOverlap (the exclusion constraint).
func (s *Store) AddDayOff(ctx context.Context, proID uuid.UUID, day string, reason *string, from, to time.Time) (*model.DayOff, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_days_off (pro_id, day, reason) VALUES ($1, $2::date, $3)`, proID, day, reason); err != nil {
		return nil, mapErr(err)
	}
	var block uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO doorstep.pro_calendar_blocks (pro_id, kind, during)
		VALUES ($1, 'day_off', tstzrange($2, $3, '[)')) RETURNING id`, proID, from, to).Scan(&block); err != nil {
		return nil, mapErr(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_days_off SET calendar_block_id = $3 WHERE pro_id = $1 AND day = $2::date`, proID, day, block); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return &model.DayOff{Date: day, Reason: reason}, nil
}

// DaysOff lists days off from fromDay on.
func (s *Store) DaysOff(ctx context.Context, proID uuid.UUID, fromDay string) ([]model.DayOff, error) {
	rows, err := s.db.Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), reason FROM doorstep.pro_days_off
		WHERE pro_id = $1 AND day >= $2::date ORDER BY day`, proID, fromDay)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.DayOff, error) {
		var d model.DayOff
		return d, r.Scan(&d.Date, &d.Reason)
	})
}

// DeleteDayOff removes a day off and releases its calendar block.
func (s *Store) DeleteDayOff(ctx context.Context, proID uuid.UUID, day string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var block *uuid.UUID
	if err := tx.QueryRow(ctx, `DELETE FROM doorstep.pro_days_off WHERE pro_id = $1 AND day = $2::date RETURNING calendar_block_id`,
		proID, day).Scan(&block); err != nil {
		return mapErr(err)
	}
	if block != nil {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_calendar_blocks SET active = FALSE, released_at = NOW(), release_reason = 'day_off_removed'
			WHERE id = $1 AND pro_id = $2`, *block, proID); err != nil {
			return err
		}
	}
	return mapErr(tx.Commit(ctx))
}

// ---------------------------------------------------------------- bank, PAN, agreement

// SetPayoutAccount replaces the active payout account. Only the sealed
// number and its last four digits are stored.
func (s *Store) SetPayoutAccount(ctx context.Context, proID uuid.UUID, holder string, sealed []byte, keyVersion uint32, last4, ifsc string) (*model.PayoutAccount, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_payout_accounts SET active = FALSE WHERE pro_id = $1 AND active`, proID); err != nil {
		return nil, err
	}
	var out model.PayoutAccount
	if err := tx.QueryRow(ctx, `INSERT INTO doorstep.pro_payout_accounts (pro_id, account_holder, account_number_sealed, account_last4, ifsc, key_version)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING account_holder, account_last4, ifsc, status`,
		proID, holder, sealed, last4, ifsc, "v"+strconv.FormatUint(uint64(keyVersion), 10)).
		Scan(&out.AccountHolder, &out.AccountLast4, &out.IFSC, &out.Status); err != nil {
		return nil, mapErr(err)
	}
	return &out, mapErr(tx.Commit(ctx))
}

// PayoutAccount is the masked active account, or nil.
func (s *Store) PayoutAccount(ctx context.Context, proID uuid.UUID) (*model.PayoutAccount, error) {
	var out model.PayoutAccount
	err := s.db.QueryRow(ctx, `SELECT account_holder, account_last4, ifsc, status FROM doorstep.pro_payout_accounts
		WHERE pro_id = $1 AND active`, proID).Scan(&out.AccountHolder, &out.AccountLast4, &out.IFSC, &out.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SetPAN stores the sealed PAN and its last four characters.
func (s *Store) SetPAN(ctx context.Context, proID uuid.UUID, sealed []byte, last4 string) error {
	_, err := s.db.Exec(ctx, `UPDATE doorstep.professionals SET pan_sealed = $2, pan_last4 = $3, updated_at = NOW() WHERE id = $1`, proID, sealed, last4)
	return mapErr(err)
}

// AcceptAgreement records the accepted version and time.
func (s *Store) AcceptAgreement(ctx context.Context, proID uuid.UUID, version string, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE doorstep.professionals SET agreement_version = $2, agreement_accepted_at = $3, updated_at = NOW()
		WHERE id = $1`, proID, version, at)
	return mapErr(err)
}

// ---------------------------------------------------------------- vendor webhook

// ApplyProviderCheck updates a provider-sourced check from a verified
// webhook (no vendor exists yet; the route answers 404 until one does).
func (s *Store) ApplyProviderCheck(ctx context.Context, provider, externalRef, status string, from, until *time.Time) error {
	tag, err := s.db.Exec(ctx, `UPDATE doorstep.background_checks SET status = $3, valid_from = COALESCE($4::date, valid_from),
		valid_until = COALESCE($5::date, valid_until), updated_at = NOW()
		WHERE source = 'provider' AND provider = $1 AND external_ref = $2`, provider, externalRef, status, datePtr(from), datePtr(until))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// CategorySkills returns the required skills of the active services of the
// given active categories and how many of the categories exist and are active.
func (s *Store) CategorySkills(ctx context.Context, categoryIDs []uuid.UUID) ([]string, int, error) {
	ids := uuidStrings(categoryIDs)
	var found int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM doorstep.categories WHERE id = ANY($1::uuid[]) AND active`, ids).Scan(&found); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(ctx, `SELECT DISTINCT sv.required_skill FROM doorstep.services sv
		JOIN doorstep.categories c ON c.id = sv.category_id
		WHERE c.id = ANY($1::uuid[]) AND c.active AND sv.active ORDER BY 1`, ids)
	if err != nil {
		return nil, 0, err
	}
	skills, err := collect(rows, func(r pgx.Rows) (string, error) {
		var c string
		return c, r.Scan(&c)
	})
	return skills, found, err
}
