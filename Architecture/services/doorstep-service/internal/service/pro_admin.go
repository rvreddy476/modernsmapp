package service

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/mediaclient"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// Admin-internal professional review (A2): list, detail (masked KYC),
// approve / reject / suspend / reinstate / block, skill verification,
// document review and the audited document view. The HTTP layer admitted an
// admin-service token carrying the route's permission; the store writes the
// change, its role intent, its event and its audit row in one transaction.

var proStatuses = set("draft", "pending_verification", "approved", "suspended", "rejected", "blocked")

// proAdminErr maps store errors of the professional admin routes.
func proAdminErr(ctx context.Context, op string, err error) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae
	}
	if status, ok := store.IsTransition(err); ok {
		return transitionErr(status)
	}
	if errors.Is(err, store.ErrNotFound) {
		return apperr.New(http.StatusNotFound, apperr.CodeProNotFound, "professional not found")
	}
	return internal(ctx, op, err)
}

func (s *Service) proStore(ctx context.Context) (ProStore, error) {
	if s.pro.Store == nil {
		return nil, internal(ctx, "pro store", errors.New("professional onboarding is not wired"))
	}
	return s.pro.Store, nil
}

func encodeCursor(p model.Professional) string {
	return base64.RawURLEncoding.EncodeToString([]byte(p.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + p.ID.String()))
}

func decodeCursor(raw string) (*store.ProCursor, *apperr.Error) {
	bad := apperr.Invalid("cursor", "cursor is not one this service issued")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, bad
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, bad
	}
	at, err1 := time.Parse(time.RFC3339Nano, ts)
	uid, err2 := uuid.Parse(id)
	if err1 != nil || err2 != nil {
		return nil, bad
	}
	return &store.ProCursor{CreatedAt: at, ID: uid}, nil
}

// AdminListProfessionals pages professionals newest first.
func (s *Service) AdminListProfessionals(ctx context.Context, status, city, cursor string, limit int) (*model.ProfessionalPage, error) {
	st, err := s.proStore(ctx)
	if err != nil {
		return nil, err
	}
	if status != "" && !proStatuses[status] {
		return nil, apperr.Invalid("status", "status has an unsupported value")
	}
	if city != "" {
		c, aerr := normaliseCity(city)
		if aerr != nil {
			return nil, aerr
		}
		city = c
	}
	var after *store.ProCursor
	if cursor != "" {
		c, aerr := decodeCursor(cursor)
		if aerr != nil {
			return nil, aerr
		}
		after = c
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	items, err := st.ListProfessionals(ctx, status, city, after, limit+1)
	if err != nil {
		return nil, internal(ctx, "list professionals", err)
	}
	page := &model.ProfessionalPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		next := encodeCursor(items[limit-1])
		page.NextCursor = &next
	}
	if page.Items == nil {
		page.Items = []model.Professional{}
	}
	return page, nil
}

// AdminProfessional is one professional with masked KYC: check statuses and
// scores, document media ids (viewable only through the audited view
// route), the payout account's last four digits. Never a number or an OTP.
func (s *Service) AdminProfessional(ctx context.Context, id uuid.UUID) (*model.AdminProfessionalDetail, error) {
	st, err := s.proStore(ctx)
	if err != nil {
		return nil, err
	}
	p, err := st.ProfessionalByID(ctx, id)
	if err != nil {
		return nil, proAdminErr(ctx, "professional", err)
	}
	facts, err := st.ProFacts(ctx, id, s.today())
	if err != nil {
		return nil, proAdminErr(ctx, "pro facts", err)
	}
	out := &model.AdminProfessionalDetail{Professional: *p, Readiness: *readinessOf(facts)}
	if out.Skills, err = st.ProSkills(ctx, id); err != nil {
		return nil, internal(ctx, "pro skills", err)
	}
	if out.ZoneIDs, err = st.ProZoneIDs(ctx, id); err != nil {
		return nil, internal(ctx, "pro zones", err)
	}
	if out.Documents, err = st.ProDocuments(ctx, id); err != nil {
		return nil, internal(ctx, "pro documents", err)
	}
	if out.BackgroundChecks, err = st.BackgroundChecks(ctx, id); err != nil {
		return nil, internal(ctx, "background checks", err)
	}
	if out.KycChecks, err = st.KycChecks(ctx, id); err != nil {
		return nil, internal(ctx, "kyc checks", err)
	}
	if out.PayoutAccount, err = st.PayoutAccount(ctx, id); err != nil {
		return nil, internal(ctx, "payout account", err)
	}
	return out, nil
}

func requiredReason(r *string) (*string, *apperr.Error) {
	v, aerr := optionalReason(r)
	if aerr != nil {
		return nil, aerr
	}
	if v == nil {
		return nil, apperr.Invalid("reason", "reason is required")
	}
	return v, nil
}

// approvalCheck refuses approval while any onboarding step is missing
// (422 DOORSTEP_ONBOARDING_INCOMPLETE) and applies the category rules:
// every category of a verified skill must accept the DigiLocker gender
// (403 DOORSTEP_GENDER_RULE) and a salon skill needs a clear background
// check (403 DOORSTEP_BACKGROUND_CHECK_REQUIRED).
func approvalCheck(st *store.ProState, cats []prokyc.SkillCategory) error {
	if missing := prokyc.MissingSteps(st.Facts); len(missing) > 0 {
		return incomplete(missing...)
	}
	for _, b := range prokyc.ApprovalBlocks(cats, st.Gender, st.BackgroundClear) {
		if b.Reason == prokyc.BlockGender {
			return apperr.New(http.StatusForbidden, apperr.CodeGenderRule,
				"the professional's DigiLocker gender does not satisfy this skill's category rule").WithDetails(map[string]any{"skill_code": b.Skill})
		}
		return apperr.New(http.StatusForbidden, apperr.CodeBackgroundCheckRequired,
			"a salon skill needs a clear background check").WithDetails(map[string]any{"skill_code": b.Skill})
	}
	return nil
}

func (s *Service) changeStatus(ctx context.Context, a store.Actor, id uuid.UUID, ch store.StatusChange) (*model.Professional, error) {
	st, err := s.proStore(ctx)
	if err != nil {
		return nil, err
	}
	ch.Today = s.today()
	p, err := st.ChangeProStatus(ctx, a, id, ch)
	return p, proAdminErr(ctx, ch.Action, err)
}

// AdminApproveProfessional approves a draft or pending professional, only
// when MissingSteps is empty and the category rules hold.
func (s *Service) AdminApproveProfessional(ctx context.Context, a store.Actor, id uuid.UUID, in model.ReasonInput) (*model.Professional, error) {
	reason, aerr := optionalReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	return s.changeStatus(ctx, a, id, store.StatusChange{Action: "professional.approve", From: []string{"draft", "pending_verification"},
		To: "approved", Reason: reason, Check: approvalCheck})
}

// AdminRejectProfessional rejects an application (terminal; revokes the role).
func (s *Service) AdminRejectProfessional(ctx context.Context, a store.Actor, id uuid.UUID, in model.ReasonInput) (*model.Professional, error) {
	reason, aerr := requiredReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	return s.changeStatus(ctx, a, id, store.StatusChange{Action: "professional.reject", From: []string{"draft", "pending_verification"},
		To: "rejected", Reason: reason})
}

// AdminSuspendProfessional pauses an approved professional (role kept).
func (s *Service) AdminSuspendProfessional(ctx context.Context, a store.Actor, id uuid.UUID, in model.ReasonInput) (*model.Professional, error) {
	reason, aerr := requiredReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	return s.changeStatus(ctx, a, id, store.StatusChange{Action: "professional.suspend", From: []string{"approved"},
		To: "suspended", Reason: reason})
}

// AdminReinstateProfessional lifts a suspension: the onboarding and category
// rules must still hold (an expired background check blocks it), and an
// open safety incident's suspension cannot be lifted here.
func (s *Service) AdminReinstateProfessional(ctx context.Context, a store.Actor, id uuid.UUID, in model.ReasonInput) (*model.Professional, error) {
	reason, aerr := requiredReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	return s.changeStatus(ctx, a, id, store.StatusChange{Action: "professional.reinstate", From: []string{"suspended"},
		To: "approved", Reason: reason, Check: func(st *store.ProState, cats []prokyc.SkillCategory) error {
			if st.IncidentSuspended {
				return apperr.New(http.StatusConflict, apperr.CodeConflict, "a safety incident suspended this professional; resolve the incident first")
			}
			return approvalCheck(st, cats)
		}})
}

// AdminBlockProfessional blocks permanently (revokes the role).
func (s *Service) AdminBlockProfessional(ctx context.Context, a store.Actor, id uuid.UUID, in model.ReasonInput) (*model.Professional, error) {
	reason, aerr := requiredReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	return s.changeStatus(ctx, a, id, store.StatusChange{Action: "professional.block",
		From: []string{"draft", "pending_verification", "approved", "suspended", "rejected"}, To: "blocked", Reason: reason})
}

// AdminVerifySkill verifies or revokes a declared skill. A skill that needs
// a certificate is verified only with an approved trade certificate for it
// (422 DOORSTEP_CERTIFICATE_REQUIRED); a gender the skill's categories
// refuse is 403 DOORSTEP_GENDER_RULE.
func (s *Service) AdminVerifySkill(ctx context.Context, a store.Actor, proID uuid.UUID, code string, in model.SkillVerifyInput) (*model.ProSkill, error) {
	st, err := s.proStore(ctx)
	if err != nil {
		return nil, err
	}
	if !skillCodeRe.MatchString(code) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "the professional has not declared this skill")
	}
	if in.Verified == nil {
		return nil, apperr.Invalid("verified", "verified is required")
	}
	reason, aerr := optionalReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	verified := *in.Verified
	out, err := st.VerifySkill(ctx, a, proID, code, verified, reason, func(rule *store.SkillRule, hasCert bool, gender string) error {
		if !verified {
			return nil
		}
		if rule == nil {
			return apperr.New(http.StatusNotFound, apperr.CodeNotFound, "unknown skill")
		}
		if rule.RequiresCertificate && !hasCert {
			return apperr.New(http.StatusUnprocessableEntity, apperr.CodeCertificateRequired,
				"this skill is verified only through an approved trade certificate").WithDetails(map[string]any{"skill_code": code})
		}
		if prokyc.GenderBlocked(rule.Categories, gender) {
			return apperr.New(http.StatusForbidden, apperr.CodeGenderRule,
				"the professional's DigiLocker gender does not satisfy this skill's category rule").WithDetails(map[string]any{"skill_code": code})
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "the professional has not declared this skill")
	}
	return out, proAdminErr(ctx, "verify skill", err)
}

// AdminListDocuments is the review queue (police certificates first).
func (s *Service) AdminListDocuments(ctx context.Context, status string) ([]model.ProDocument, error) {
	st, err := s.proStore(ctx)
	if err != nil {
		return nil, err
	}
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "approved" && status != "rejected" {
		return nil, apperr.Invalid("status", "status must be pending, approved or rejected")
	}
	v, err := st.ListDocuments(ctx, status)
	if err != nil {
		return nil, internal(ctx, "list documents", err)
	}
	return v, nil
}

// AdminDecideDocument approves or rejects a pending document. Approving a
// police certificate is the ONLY way a background check becomes clear
// (until issued_on + 12 months; the database refuses a clear check without
// its approved certificate); an expired certificate cannot be approved.
func (s *Service) AdminDecideDocument(ctx context.Context, a store.Actor, id uuid.UUID, in model.DocumentDecisionInput) (*model.ProDocument, error) {
	st, err := s.proStore(ctx)
	if err != nil {
		return nil, err
	}
	if in.Decision == nil || (*in.Decision != "approve" && *in.Decision != "reject") {
		return nil, apperr.Invalid("decision", "decision must be approve or reject")
	}
	approve := *in.Decision == "approve"
	reason, aerr := optionalReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	if !approve && reason == nil {
		return nil, apperr.Invalid("reason", "a rejection needs a reason the professional will see")
	}
	today := s.today()
	doc, err := st.DecideDocument(ctx, a, id, approve, reason, func(d *model.ProDocument) error {
		if !approve || d.Kind != "police_certificate" {
			return nil
		}
		if d.IssuedOn == nil {
			return apperr.Invalid("decision", "this police certificate has no issue date")
		}
		issued, err := time.ParseInLocation("2006-01-02", *d.IssuedOn, IST)
		if err != nil || !issued.AddDate(1, 0, 0).After(today) {
			return apperr.Invalid("decision", "this police certificate is older than 12 months; reject it and ask for a new one")
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "document not found")
	}
	return doc, proAdminErr(ctx, "decide document", err)
}

// DocumentImage is a document's bytes for the console.
type DocumentImage struct {
	Bytes       []byte
	ContentType string
}

// AdminViewDocument returns a document's image bytes (police certificate,
// trade certificate, selfie) fetched from media-service server-side by the
// document's own media id, and writes one audit row per view before the
// bytes leave. Never a URL.
func (s *Service) AdminViewDocument(ctx context.Context, a store.Actor, id uuid.UUID) (*DocumentImage, error) {
	st, err := s.proStore(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := st.DocumentByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "document not found")
	}
	if err != nil {
		return nil, internal(ctx, "document", err)
	}
	mediaID, err := uuid.Parse(doc.MediaID)
	if err != nil || s.pro.Images == nil {
		return nil, apperr.New(http.StatusServiceUnavailable, apperr.CodeMediaUnavailable, "document images are unavailable")
	}
	body, ct, err := s.pro.Images.FetchImage(ctx, mediaID)
	switch {
	case errors.Is(err, mediaclient.ErrNotFound), errors.Is(err, mediaclient.ErrNotAnImage):
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "this document has no viewable image")
	case err != nil:
		return nil, apperr.New(http.StatusServiceUnavailable, apperr.CodeMediaUnavailable, "media-service is unavailable; try again")
	}
	if err := st.AuditDocumentView(ctx, a, doc); err != nil {
		return nil, internal(ctx, "audit document view", err)
	}
	return &DocumentImage{Bytes: body, ContentType: ct}, nil
}
