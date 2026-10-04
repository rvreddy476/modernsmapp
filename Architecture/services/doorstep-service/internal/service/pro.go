package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/bgv"
	"github.com/atpost/doorstep-service/internal/digilocker"
	"github.com/atpost/doorstep-service/internal/facecompare"
	"github.com/atpost/doorstep-service/internal/mediaclient"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// Professional onboarding (A2): the /pro routes. Every route acts on the
// caller's own professional (X-User-Id from the gateway); nothing here takes
// a professional id from the client.

// ProStore is what onboarding and its admin review need.
type ProStore interface {
	CreateProfessional(ctx context.Context, in store.NewProfessional) (*model.Professional, error)
	ProfessionalByUser(ctx context.Context, userID uuid.UUID) (*model.Professional, error)
	ProfessionalByID(ctx context.Context, id uuid.UUID) (*model.Professional, error)
	UpdateProfile(ctx context.Context, proID uuid.UUID, name, photo *string) (*model.Professional, error)
	ProFacts(ctx context.Context, proID uuid.UUID, today time.Time) (*store.ProState, error)
	MarkPendingVerification(ctx context.Context, proID uuid.UUID) (bool, error)
	CreateDigiLockerState(ctx context.Context, proID uuid.UUID, stateHash string, verifierSealed []byte, expiresAt time.Time) error
	ConsumeDigiLockerState(ctx context.Context, userID uuid.UUID, stateHash string) (uuid.UUID, []byte, error)
	RecordAadhaar(ctx context.Context, proID uuid.UUID, rec store.AadhaarRecord) error
	AadhaarReference(ctx context.Context, proID uuid.UUID) (bool, *uuid.UUID, error)
	RecordSelfie(ctx context.Context, proID, mediaID uuid.UUID, status, provider string, score *float64, details map[string]any) (*model.KycCheck, error)
	ListSkills(ctx context.Context) ([]model.Skill, error)
	SkillRules(ctx context.Context, codes []string) (map[string]*store.SkillRule, error)
	CategorySkills(ctx context.Context, categoryIDs []uuid.UUID) ([]string, int, error)
	ReplaceSkills(ctx context.Context, proID uuid.UUID, decls []store.SkillDecl) ([]model.ProSkill, error)
	ProSkills(ctx context.Context, proID uuid.UUID) ([]model.ProSkill, error)
	UploadDocument(ctx context.Context, d store.NewDocument, bg *store.BackgroundInit) (*model.ProDocument, error)
	SetArea(ctx context.Context, proID uuid.UUID, zoneIDs []uuid.UUID, lat, lng float64, radiusM int) (*model.ProArea, error)
	ProZoneIDs(ctx context.Context, proID uuid.UUID) ([]uuid.UUID, error)
	ReplaceHours(ctx context.Context, proID uuid.UUID, ws []prokyc.Window) error
	Hours(ctx context.Context, proID uuid.UUID) ([]model.HoursWindow, error)
	AddDayOff(ctx context.Context, proID uuid.UUID, day string, reason *string, from, to time.Time) (*model.DayOff, error)
	DaysOff(ctx context.Context, proID uuid.UUID, fromDay string) ([]model.DayOff, error)
	DeleteDayOff(ctx context.Context, proID uuid.UUID, day string) error
	SetPayoutAccount(ctx context.Context, proID uuid.UUID, holder string, sealed []byte, keyVersion uint32, last4, ifsc string) (*model.PayoutAccount, error)
	PayoutAccount(ctx context.Context, proID uuid.UUID) (*model.PayoutAccount, error)
	SetPAN(ctx context.Context, proID uuid.UUID, sealed []byte, last4 string) error
	AcceptAgreement(ctx context.Context, proID uuid.UUID, version string, at time.Time) error
	ApplyProviderCheck(ctx context.Context, provider, externalRef, status string, from, until *time.Time) error

	ListProfessionals(ctx context.Context, status, city string, after *store.ProCursor, limit int) ([]model.Professional, error)
	ProDocuments(ctx context.Context, proID uuid.UUID) ([]model.ProDocument, error)
	ListDocuments(ctx context.Context, status string) ([]model.ProDocument, error)
	BackgroundChecks(ctx context.Context, proID uuid.UUID) ([]model.BackgroundCheckView, error)
	KycChecks(ctx context.Context, proID uuid.UUID) ([]model.KycCheck, error)
	ChangeProStatus(ctx context.Context, a store.Actor, id uuid.UUID, ch store.StatusChange) (*model.Professional, error)
	VerifySkill(ctx context.Context, a store.Actor, proID uuid.UUID, code string, verified bool, reason *string, check store.SkillVerifyCheck) (*model.ProSkill, error)
	DecideDocument(ctx context.Context, a store.Actor, id uuid.UUID, approve bool, reason *string, check store.DocumentCheck) (*model.ProDocument, error)
	DocumentByID(ctx context.Context, id uuid.UUID) (*model.ProDocument, error)
	AuditDocumentView(ctx context.Context, a store.Actor, doc *model.ProDocument) error
}

// ProDeps are onboarding's dependencies. Main builds every one from config
// (each mock refused in production there and again in its own package).
type ProDeps struct {
	Store               ProStore
	DigiLocker          digilocker.Settings
	Faces               facecompare.Comparer
	SelfieMinSimilarity float64
	Media               mediaclient.Verifier
	Images              mediaclient.ImageFetcher
	PII                 *propii.Crypto
	BGV                 bgv.Provider
	// NewToken makes the DigiLocker state and code_verifier (32 random bytes,
	// base64url). nil is digilocker.NewState; contract fixtures pin it.
	NewToken func() (string, error)
}

// WithPro wires professional onboarding.
func (s *Service) WithPro(d ProDeps) *Service {
	if d.SelfieMinSimilarity <= 0 || d.SelfieMinSimilarity > 100 {
		d.SelfieMinSimilarity = 80
	}
	s.pro = d
	return s
}

// IST is the professional calendar's zone (Asia/Kolkata has no DST).
var IST = time.FixedZone("IST", 5*3600+30*60)

const (
	digiLockerStateTTL = 10 * time.Minute
	maxReason          = 1000
)

func (s *Service) today() time.Time {
	n := s.now().In(IST)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, IST)
}

func proNotFound() *apperr.Error {
	return apperr.New(http.StatusNotFound, apperr.CodeProNotFound, "you have not applied as a Doorstep professional")
}

func transitionErr(status string) *apperr.Error {
	return apperr.New(http.StatusConflict, apperr.CodeInvalidTransition, "not allowed while the professional is "+status).
		WithDetails(map[string]any{"status": status})
}

func piiUnavailable() *apperr.Error {
	return apperr.New(http.StatusServiceUnavailable, apperr.CodePIIUnavailable, "sealing keys are not configured on this deployment")
}

func incomplete(steps ...prokyc.Step) *apperr.Error {
	return apperr.New(http.StatusUnprocessableEntity, apperr.CodeOnboardingIncomplete, "onboarding steps are missing").
		WithDetails(map[string]any{"missing_steps": prokyc.Strings(steps)})
}

// mine is the caller's professional.
func (s *Service) mine(ctx context.Context, uid uuid.UUID) (*model.Professional, error) {
	if s.pro.Store == nil {
		return nil, internal(ctx, "pro store", errors.New("professional onboarding is not wired"))
	}
	p, err := s.pro.Store.ProfessionalByUser(ctx, uid)
	if errors.Is(err, store.ErrNotFound) {
		return nil, proNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "professional by user", err)
	}
	return p, nil
}

// editable is the caller's professional, refused once rejected or blocked
// (terminal: they are off the journey).
func (s *Service) editable(ctx context.Context, uid uuid.UUID) (*model.Professional, error) {
	p, err := s.mine(ctx, uid)
	if err != nil {
		return nil, err
	}
	if p.Status == "rejected" || p.Status == "blocked" {
		return nil, transitionErr(p.Status)
	}
	return p, nil
}

func readinessOf(st *store.ProState) *model.ProReadiness {
	missing := prokyc.MissingSteps(st.Facts)
	return &model.ProReadiness{
		Status:           st.Status,
		MissingSteps:     prokyc.Strings(missing),
		CompletedSteps:   prokyc.Strings(prokyc.CompletedSteps(st.Facts)),
		RecommendedSteps: prokyc.Strings(prokyc.RecommendedSteps(st.Facts)),
		CanGoOnDuty:      st.Status == "approved" && len(missing) == 0 && !st.IncidentSuspended,
	}
}

// afterStep re-reads the facts and moves a draft professional whose only
// remaining steps are admin reviews to pending_verification.
func (s *Service) afterStep(ctx context.Context, proID uuid.UUID) (*model.ProReadiness, error) {
	st, err := s.pro.Store.ProFacts(ctx, proID, s.today())
	if err != nil {
		return nil, internal(ctx, "pro facts", err)
	}
	if st.Status == "draft" && prokyc.AwaitingReviewOnly(st.Facts) {
		moved, err := s.pro.Store.MarkPendingVerification(ctx, proID)
		if err != nil {
			return nil, internal(ctx, "mark pending verification", err)
		}
		if moved {
			st.Status = "pending_verification"
		}
	}
	return readinessOf(st), nil
}

var controlRe = regexp.MustCompile(`[\x00-\x1f\x7f]`)

func cleanText(field, v string, min, max int) (string, *apperr.Error) {
	v = strings.TrimSpace(v)
	n := utf8.RuneCountInString(v)
	if n < min || n > max || controlRe.MatchString(v) {
		return "", apperr.Invalid(field, field+" must be "+itoa(min)+"-"+itoa(max)+" characters without control characters")
	}
	return v, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func optionalReason(r *string) (*string, *apperr.Error) {
	if r == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*r)
	if v == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(v) > maxReason {
		return nil, apperr.Invalid("reason", "reason is at most 1000 characters")
	}
	return &v, nil
}

func parseMediaID(field string, raw *string) (uuid.UUID, *apperr.Error) {
	if raw == nil {
		return uuid.Nil, apperr.Invalid(field, field+" is required")
	}
	id, err := uuid.Parse(strings.TrimSpace(*raw))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, apperr.Invalid(field, field+" must be a UUID")
	}
	return id, nil
}

// verifyMedia checks the upload is the caller's own processed, approved
// media of an allowed kind (fail closed).
func (s *Service) verifyMedia(ctx context.Context, field string, id, owner uuid.UUID, kinds ...mediaclient.Kind) error {
	if s.pro.Media == nil {
		return apperr.New(http.StatusServiceUnavailable, apperr.CodeMediaUnavailable, "media verification is not configured")
	}
	err := s.pro.Media.VerifyOwned(ctx, id, owner, kinds...)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mediaclient.ErrUnavailable):
		slog.WarnContext(ctx, "doorstep: media verification unavailable", "error", err)
		return apperr.New(http.StatusServiceUnavailable, apperr.CodeMediaUnavailable, "media-service is unavailable; try again")
	case errors.Is(err, mediaclient.ErrNotReady):
		return apperr.Invalid(field, field+" is still processing; try again in a moment")
	}
	return apperr.Invalid(field, field+" must be your own uploaded, approved file of the right kind")
}

func parseDate(field string, raw *string) (time.Time, *apperr.Error) {
	if raw == nil {
		return time.Time{}, apperr.Invalid(field, field+" is required")
	}
	d, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(*raw), IST)
	if err != nil {
		return time.Time{}, apperr.Invalid(field, field+" must be a date (YYYY-MM-DD)")
	}
	return d, nil
}

var certNumberRe = regexp.MustCompile(`^[A-Za-z0-9/\-. ]{3,40}$`)

// sealCertificateNumber validates and seals an optional certificate number.
func (s *Service) sealCertificateNumber(ctx context.Context, raw *string) ([]byte, *string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil, nil
	}
	n := strings.TrimSpace(*raw)
	if !certNumberRe.MatchString(n) {
		return nil, nil, apperr.Invalid("certificate_number", "certificate_number must be 3-40 letters, digits, '/', '-', '.' or spaces")
	}
	if kyc.LooksLikeAadhaar(n) {
		return nil, nil, apperr.Invalid("certificate_number", "that looks like an Aadhaar number; enter the certificate's own number")
	}
	sealed, err := s.pro.PII.SealDocumentNumber(ctx, n)
	if errors.Is(err, propii.ErrNotConfigured) {
		return nil, nil, piiUnavailable()
	}
	if err != nil {
		return nil, nil, internal(ctx, "seal certificate number", err)
	}
	compact := strings.ReplaceAll(n, " ", "")
	last4 := compact
	if len(compact) > 4 {
		last4 = compact[len(compact)-4:]
	}
	return sealed.Blob, &last4, nil
}

// ---- apply, me, readiness ----

// ProApply creates the caller's draft professional; service_professional is
// granted in the same transaction.
func (s *Service) ProApply(ctx context.Context, uid uuid.UUID, in model.ProApplyInput) (*model.Professional, error) {
	if s.pro.Store == nil {
		return nil, internal(ctx, "pro store", errors.New("professional onboarding is not wired"))
	}
	if in.DisplayName == nil {
		return nil, apperr.Invalid("display_name", "display_name is required")
	}
	name, aerr := cleanText("display_name", *in.DisplayName, 2, 60)
	if aerr != nil {
		return nil, aerr
	}
	if in.CityCode == nil {
		return nil, apperr.Invalid("city_code", "city_code is required")
	}
	city, aerr := normaliseCity(*in.CityCode)
	if aerr != nil {
		return nil, aerr
	}
	var decls []store.SkillDecl
	if len(in.CategoryIDs) > 0 {
		if len(in.CategoryIDs) > 20 {
			return nil, apperr.Invalid("category_ids", "at most 20 categories")
		}
		ids := dedupeIDs(in.CategoryIDs)
		codes, found, err := s.pro.Store.CategorySkills(ctx, ids)
		if err != nil {
			return nil, internal(ctx, "category skills", err)
		}
		if found != len(ids) {
			return nil, apperr.Invalid("category_ids", "every category_id must be an active Doorstep category")
		}
		if decls, err = s.declarations(ctx, codes, ""); err != nil {
			return nil, err
		}
	}
	p, err := s.pro.Store.CreateProfessional(ctx, store.NewProfessional{UserID: uid, DisplayName: name, CityCode: city, Skills: decls})
	switch {
	case errors.Is(err, store.ErrConflict):
		return nil, apperr.New(http.StatusConflict, apperr.CodeProExists, "you have already applied as a Doorstep professional")
	case errors.Is(err, store.ErrBadReference):
		return nil, apperr.New(http.StatusNotFound, apperr.CodeCityNotFound, "Doorstep does not serve this city")
	case err != nil:
		return nil, internal(ctx, "create professional", err)
	}
	return p, nil
}

func dedupeIDs(in []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, id := range in {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ProMe is the caller's profile.
func (s *Service) ProMe(ctx context.Context, uid uuid.UUID) (*model.Professional, error) {
	return s.mine(ctx, uid)
}

// ProPatchMe edits the display name and profile photo (the caller's own
// approved image). Gender is not editable: the strict decoder refuses it.
func (s *Service) ProPatchMe(ctx context.Context, uid uuid.UUID, in model.ProPatchInput) (*model.Professional, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	var name, photo *string
	if in.DisplayName != nil {
		v, aerr := cleanText("display_name", *in.DisplayName, 2, 60)
		if aerr != nil {
			return nil, aerr
		}
		name = &v
	}
	if in.PhotoMediaID != nil {
		id, aerr := parseMediaID("photo_media_id", in.PhotoMediaID)
		if aerr != nil {
			return nil, aerr
		}
		if err := s.verifyMedia(ctx, "photo_media_id", id, uid, mediaclient.KindImage); err != nil {
			return nil, err
		}
		v := id.String()
		photo = &v
	}
	out, err := s.pro.Store.UpdateProfile(ctx, p.ID, name, photo)
	if err != nil {
		return nil, internal(ctx, "update profile", err)
	}
	if _, err := s.afterStep(ctx, p.ID); err != nil {
		return nil, err
	}
	return out, nil
}

// ProReadiness is the caller's checklist.
func (s *Service) ProReadiness(ctx context.Context, uid uuid.UUID) (*model.ProReadiness, error) {
	p, err := s.mine(ctx, uid)
	if err != nil {
		return nil, err
	}
	st, err := s.pro.Store.ProFacts(ctx, p.ID, s.today())
	if err != nil {
		return nil, internal(ctx, "pro facts", err)
	}
	return readinessOf(st), nil
}

// ---- DigiLocker (Aadhaar; the only source of gender) ----

func digiLockerUnavailable() *apperr.Error {
	return apperr.New(http.StatusServiceUnavailable, apperr.CodeDigiLockerUnavailable, "DigiLocker verification is unavailable")
}

// ProDigiLockerStart begins PKCE: a single-use state bound to the caller's
// professional (hash stored) and a sealed code_verifier.
func (s *Service) ProDigiLockerStart(ctx context.Context, uid uuid.UUID) (*model.DigiLockerStart, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	dl := s.pro.DigiLocker
	if dl.Client == nil {
		return nil, digiLockerUnavailable()
	}
	if !s.pro.PII.Configured() {
		return nil, piiUnavailable()
	}
	if passed, _, err := s.pro.Store.AadhaarReference(ctx, p.ID); err != nil {
		return nil, internal(ctx, "aadhaar reference", err)
	} else if passed {
		return nil, apperr.New(http.StatusConflict, apperr.CodeConflict, "your Aadhaar is already verified through DigiLocker")
	}
	newToken := s.pro.NewToken
	if newToken == nil {
		newToken = digilocker.NewState
	}
	state, err := newToken()
	if err != nil {
		return nil, internal(ctx, "digilocker state", err)
	}
	verifier, err := newToken()
	if err != nil {
		return nil, internal(ctx, "digilocker verifier", err)
	}
	sealed, err := s.pro.PII.SealVerifier(ctx, verifier)
	if err != nil {
		return nil, internal(ctx, "seal verifier", err)
	}
	if err := s.pro.Store.CreateDigiLockerState(ctx, p.ID, digilocker.HashState(state), sealed.Blob, s.now().Add(digiLockerStateTTL)); err != nil {
		return nil, internal(ctx, "store digilocker state", err)
	}
	authorize, err := digilocker.AuthorizeURL(dl, state, digilocker.CodeChallengeS256(verifier))
	if err != nil {
		return nil, digiLockerUnavailable()
	}
	return &model.DigiLockerStart{AuthorizationURL: authorize, State: state}, nil
}

// ProDigiLockerCallback completes DigiLocker: consumes the state (once, the
// caller's own), exchanges the code with the sealed verifier, and records
// the Aadhaar check and the gender it carries.
func (s *Service) ProDigiLockerCallback(ctx context.Context, uid uuid.UUID, in model.DigiLockerCallbackInput) (*model.ProReadiness, error) {
	if _, err := s.editable(ctx, uid); err != nil {
		return nil, err
	}
	dl := s.pro.DigiLocker
	if dl.Client == nil {
		return nil, digiLockerUnavailable()
	}
	if !s.pro.PII.Configured() {
		return nil, piiUnavailable()
	}
	if in.Code == nil || strings.TrimSpace(*in.Code) == "" || len(*in.Code) > 2048 {
		return nil, apperr.Invalid("code", "code is required")
	}
	if in.State == nil || !digilocker.ValidState(*in.State) {
		return nil, apperr.Invalid("state", "state is not a DigiLocker state issued to you")
	}
	proID, sealed, err := s.pro.Store.ConsumeDigiLockerState(ctx, uid, digilocker.HashState(*in.State))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, apperr.Invalid("state", "state is not a DigiLocker state issued to you")
	case errors.Is(err, store.ErrStateUsed):
		return nil, apperr.Invalid("state", "this DigiLocker state was already used; start again")
	case errors.Is(err, store.ErrStateExpired):
		return nil, apperr.Invalid("state", "this DigiLocker state expired; start again")
	case err != nil:
		return nil, internal(ctx, "consume digilocker state", err)
	}
	verifier, err := s.pro.PII.OpenVerifier(ctx, sealed)
	if err != nil {
		return nil, internal(ctx, "open verifier", err)
	}
	session, err := dl.Client.ExchangeCode(ctx, strings.TrimSpace(*in.Code), verifier)
	if err != nil {
		slog.WarnContext(ctx, "doorstep: digilocker code exchange failed", "pro_id", proID, "provider_error", errors.Is(err, digilocker.ErrProvider))
		return nil, digiLockerUnavailable()
	}
	aadhaar, err := dl.Client.Aadhaar(ctx, session)
	if errors.Is(err, digilocker.ErrNoAadhaar) {
		return nil, incomplete(prokyc.StepAadhaar).WithDetails(map[string]any{
			"missing_steps": []string{string(prokyc.StepAadhaar)}, "reason": "NO_AADHAAR_IN_DIGILOCKER"})
	}
	if err != nil {
		slog.WarnContext(ctx, "doorstep: digilocker aadhaar failed", "pro_id", proID)
		return nil, digiLockerUnavailable()
	}
	err = s.pro.Store.RecordAadhaar(ctx, proID, store.AadhaarRecord{Provider: dl.Mode, Reference: aadhaar.Reference,
		DocTypeHash: aadhaar.DocTypeHash, Gender: aadhaar.Gender, PhotoMediaID: aadhaar.PhotoMediaID})
	switch {
	case errors.Is(err, store.ErrGenderMismatch), errors.Is(err, store.ErrDuplicateIdentity):
		slog.WarnContext(ctx, "doorstep: digilocker identity refused", "pro_id", proID, "reason", err.Error())
		return nil, apperr.New(http.StatusConflict, apperr.CodeConflict, "this DigiLocker account cannot verify this professional")
	case err != nil:
		return nil, internal(ctx, "record aadhaar", err)
	}
	return s.afterStep(ctx, proID)
}

// ---- selfie face match ----

// ProSelfie compares the caller's selfie with the DigiLocker reference face.
// At or above the threshold it passes; below it, or when media-service
// cannot answer, it stays pending (never passed on an error); an image that
// cannot be compared or has no face fails with 422.
func (s *Service) ProSelfie(ctx context.Context, uid uuid.UUID, in model.MediaInput) (*model.KycCheck, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	media, aerr := parseMediaID("media_id", in.MediaID)
	if aerr != nil {
		return nil, aerr
	}
	passed, reference, err := s.pro.Store.AadhaarReference(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "aadhaar reference", err)
	}
	if !passed {
		return nil, incomplete(prokyc.StepAadhaar)
	}
	if err := s.verifyMedia(ctx, "media_id", media, uid, mediaclient.KindImage); err != nil {
		return nil, err
	}
	if s.pro.Faces == nil {
		return nil, apperr.New(http.StatusServiceUnavailable, apperr.CodeMediaUnavailable, "face comparison is not configured")
	}
	details := map[string]any{"selfie_media_id": media.String()}
	record := func(status string, score *float64) (*model.KycCheck, error) {
		c, err := s.pro.Store.RecordSelfie(ctx, p.ID, media, status, "media-service", score, details)
		if err != nil {
			return nil, internal(ctx, "record selfie", err)
		}
		if _, err := s.afterStep(ctx, p.ID); err != nil {
			return nil, err
		}
		return c, nil
	}
	target := uuid.Nil
	if reference != nil {
		target = *reference
	}
	if s.pro.Faces.NeedsReference() && reference == nil {
		details["reason"] = "no_reference_photo"
		return record("pending", nil)
	}
	res, err := s.pro.Faces.CompareFaces(ctx, facecompare.Request{SourceMediaID: media, TargetMediaID: target, RequesterUserID: uid})
	switch {
	case errors.Is(err, facecompare.ErrMediaNotFound), errors.Is(err, facecompare.ErrImageUnsupported):
		details["reason"] = "image_unsupported"
		if _, rerr := record("failed", nil); rerr != nil {
			return nil, rerr
		}
		return nil, apperr.New(http.StatusUnprocessableEntity, apperr.CodeFaceMatchFailed, "this selfie cannot be compared; take a clear photo of your face")
	case err != nil:
		slog.WarnContext(ctx, "doorstep: face compare unavailable", "pro_id", p.ID, "error", err)
		details["reason"] = "unavailable"
		return record("pending", nil)
	}
	score := res.Similarity
	if res.FaceCountSource == 0 {
		details["reason"] = "no_face"
		if _, rerr := record("failed", &score); rerr != nil {
			return nil, rerr
		}
		return nil, apperr.New(http.StatusUnprocessableEntity, apperr.CodeFaceMatchFailed, "no face found in this selfie")
	}
	if res.Similarity >= s.pro.SelfieMinSimilarity {
		return record("passed", &score)
	}
	details["reason"] = "below_threshold"
	return record("pending", &score)
}

// ---- skills ----

var skillCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,47}$`)

// ProSkillCatalogue lists every skill with its certificate rule.
func (s *Service) ProSkillCatalogue(ctx context.Context) ([]model.Skill, error) {
	if s.pro.Store == nil {
		return nil, internal(ctx, "pro store", errors.New("professional onboarding is not wired"))
	}
	v, err := s.pro.Store.ListSkills(ctx)
	if err != nil {
		return nil, internal(ctx, "skills", err)
	}
	return v, nil
}

// declarations turns codes into declarations: unknown codes are refused, a
// known gender the skill's categories refuse is 403 DOORSTEP_GENDER_RULE,
// certificate-free skills are verified on declaration.
func (s *Service) declarations(ctx context.Context, codes []string, gender string) ([]store.SkillDecl, error) {
	rules, err := s.pro.Store.SkillRules(ctx, codes)
	if err != nil {
		return nil, internal(ctx, "skill rules", err)
	}
	decls := make([]store.SkillDecl, 0, len(codes))
	for _, c := range codes {
		r := rules[c]
		if r == nil {
			return nil, apperr.Invalid("skill_codes", "unknown skill "+c)
		}
		if prokyc.GenderBlocked(r.Categories, gender) {
			return nil, apperr.New(http.StatusForbidden, apperr.CodeGenderRule,
				"your DigiLocker gender does not match this skill's category rule").WithDetails(map[string]any{"skill_code": c})
		}
		decls = append(decls, store.SkillDecl{Code: c, Verified: !r.RequiresCertificate})
	}
	return decls, nil
}

// ProPutSkills replaces the declared skills.
func (s *Service) ProPutSkills(ctx context.Context, uid uuid.UUID, in model.ProSkillsInput) ([]model.ProSkill, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.SkillCodes == nil {
		return nil, apperr.Invalid("skill_codes", "skill_codes is required")
	}
	if len(in.SkillCodes) > 20 {
		return nil, apperr.Invalid("skill_codes", "at most 20 skills")
	}
	seen := map[string]bool{}
	codes := []string{}
	for _, c := range in.SkillCodes {
		c = strings.TrimSpace(c)
		if !skillCodeRe.MatchString(c) {
			return nil, apperr.Invalid("skill_codes", "skill codes are lowercase letters, digits and underscores")
		}
		if !seen[c] {
			seen[c] = true
			codes = append(codes, c)
		}
	}
	gender := ""
	if p.Gender != nil {
		gender = *p.Gender
	}
	decls, err := s.declarations(ctx, codes, gender)
	if err != nil {
		return nil, err
	}
	out, err := s.pro.Store.ReplaceSkills(ctx, p.ID, decls)
	if err != nil {
		return nil, internal(ctx, "replace skills", err)
	}
	if _, err := s.afterStep(ctx, p.ID); err != nil {
		return nil, err
	}
	return out, nil
}

// ProTradeCertificate uploads a trade certificate for a declared skill that
// requires one; an admin's approval verifies the skill.
func (s *Service) ProTradeCertificate(ctx context.Context, uid uuid.UUID, code string, in model.CertificateInput) (*model.ProDocument, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !skillCodeRe.MatchString(code) {
		return nil, apperr.Invalid("code", "unknown skill")
	}
	rules, err := s.pro.Store.SkillRules(ctx, []string{code})
	if err != nil {
		return nil, internal(ctx, "skill rules", err)
	}
	r := rules[code]
	if r == nil {
		return nil, apperr.Invalid("code", "unknown skill")
	}
	if !r.RequiresCertificate {
		return nil, apperr.Invalid("code", "this skill needs no certificate; declaring it verifies it")
	}
	media, aerr := parseMediaID("media_id", in.MediaID)
	if aerr != nil {
		return nil, aerr
	}
	issued, aerr := parseDate("issued_on", in.IssuedOn)
	if aerr != nil {
		return nil, aerr
	}
	today := s.today()
	if issued.After(today) || issued.Before(today.AddDate(-50, 0, 0)) {
		return nil, apperr.Invalid("issued_on", "issued_on must be a past date")
	}
	if err := s.verifyMedia(ctx, "media_id", media, uid, mediaclient.KindImage); err != nil {
		return nil, err
	}
	sealed, last4, err := s.sealCertificateNumber(ctx, in.CertificateNumber)
	if err != nil {
		return nil, err
	}
	skill := code
	doc, err := s.pro.Store.UploadDocument(ctx, store.NewDocument{ID: s.newID(), ProID: p.ID, Kind: "trade_certificate", SkillCode: &skill,
		MediaID: media, NumberSealed: sealed, NumberLast4: last4, IssuedOn: issued}, nil)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, apperr.Invalid("code", "declare the skill before uploading its certificate")
	case errors.Is(err, store.ErrConflict):
		return nil, apperr.New(http.StatusConflict, apperr.CodeConflict, "a certificate for this skill is already under review")
	case err != nil:
		return nil, internal(ctx, "upload trade certificate", err)
	}
	if _, err := s.afterStep(ctx, p.ID); err != nil {
		return nil, err
	}
	return doc, nil
}

// ---- service area, hours, days off ----

// MaxServiceRadiusM is how far a professional may set their reach.
const MaxServiceRadiusM = 15000

// ProPutArea saves zones (active zones of the professional's city), the
// home point and the radius (1-15 km).
func (s *Service) ProPutArea(ctx context.Context, uid uuid.UUID, in model.ProAreaInput) (*model.ProArea, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	zones := dedupeIDs(in.ZoneIDs)
	if len(zones) == 0 || len(zones) > 20 {
		return nil, apperr.Invalid("zone_ids", "choose 1 to 20 zones")
	}
	lat, lng, aerr := checkPoint(in.HomeLat, in.HomeLng)
	if aerr != nil {
		aerr.Details = map[string]any{"field": "home_lat"}
		return nil, aerr
	}
	if in.RadiusM == nil || *in.RadiusM < 1000 || *in.RadiusM > MaxServiceRadiusM {
		return nil, apperr.Invalid("radius_m", "radius_m must be between 1000 and 15000")
	}
	out, err := s.pro.Store.SetArea(ctx, p.ID, zones, lat, lng, *in.RadiusM)
	if errors.Is(err, store.ErrBadReference) {
		return nil, apperr.Invalid("zone_ids", "every zone must be an active zone of your city")
	}
	if err != nil {
		return nil, internal(ctx, "set area", err)
	}
	if _, err := s.afterStep(ctx, p.ID); err != nil {
		return nil, err
	}
	return out, nil
}

// ProGetHours lists the weekly hours.
func (s *Service) ProGetHours(ctx context.Context, uid uuid.UUID) (*model.WeeklyHours, error) {
	p, err := s.mine(ctx, uid)
	if err != nil {
		return nil, err
	}
	items, err := s.pro.Store.Hours(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "hours", err)
	}
	return &model.WeeklyHours{Items: items}, nil
}

// ProPutHours replaces the weekly hours (several windows a day, IST).
func (s *Service) ProPutHours(ctx context.Context, uid uuid.UUID, in model.WeeklyHours) (*model.WeeklyHours, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.Items == nil {
		return nil, apperr.Invalid("items", "items is required")
	}
	ws := make([]prokyc.Window, 0, len(in.Items))
	for _, w := range in.Items {
		ws = append(ws, prokyc.Window{Weekday: w.Weekday, Start: w.Start, End: w.End})
	}
	valid, err := prokyc.ValidateHours(ws)
	if err != nil {
		return nil, apperr.Invalid("items", err.Error())
	}
	if err := s.pro.Store.ReplaceHours(ctx, p.ID, valid); err != nil {
		return nil, internal(ctx, "replace hours", err)
	}
	if _, err := s.afterStep(ctx, p.ID); err != nil {
		return nil, err
	}
	return s.ProGetHours(ctx, uid)
}

// ProDaysOff lists days off from today.
func (s *Service) ProDaysOff(ctx context.Context, uid uuid.UUID) ([]model.DayOff, error) {
	p, err := s.mine(ctx, uid)
	if err != nil {
		return nil, err
	}
	v, err := s.pro.Store.DaysOff(ctx, p.ID, s.today().Format("2006-01-02"))
	if err != nil {
		return nil, internal(ctx, "days off", err)
	}
	return v, nil
}

// ProAddDayOff adds a day off (today to a year ahead): a day_off calendar
// block over the whole IST day, refused over a job or hold.
func (s *Service) ProAddDayOff(ctx context.Context, uid uuid.UUID, in model.DayOff) (*model.DayOff, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	raw := in.Date
	day, aerr := parseDate("date", &raw)
	if aerr != nil {
		return nil, aerr
	}
	today := s.today()
	if day.Before(today) || day.After(today.AddDate(1, 0, 0)) {
		return nil, apperr.Invalid("date", "a day off must be between today and a year ahead")
	}
	reason, aerr := optionalReason(in.Reason)
	if aerr != nil {
		return nil, aerr
	}
	if reason != nil && utf8.RuneCountInString(*reason) > 200 {
		return nil, apperr.Invalid("reason", "reason is at most 200 characters")
	}
	out, err := s.pro.Store.AddDayOff(ctx, p.ID, day.Format("2006-01-02"), reason, day, day.AddDate(0, 0, 1))
	switch {
	case errors.Is(err, store.ErrConflict):
		return nil, apperr.New(http.StatusConflict, apperr.CodeConflict, "that day is already a day off")
	case errors.Is(err, store.ErrOverlap):
		return nil, apperr.New(http.StatusConflict, apperr.CodeConflict, "you have a job on that day; it cannot be a day off")
	case err != nil:
		return nil, internal(ctx, "add day off", err)
	}
	return out, nil
}

// ProDeleteDayOff removes a day off and frees the calendar.
func (s *Service) ProDeleteDayOff(ctx context.Context, uid uuid.UUID, date string) error {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return err
	}
	day, aerr := parseDate("date", &date)
	if aerr != nil {
		return aerr
	}
	err = s.pro.Store.DeleteDayOff(ctx, p.ID, day.Format("2006-01-02"))
	if errors.Is(err, store.ErrNotFound) {
		return apperr.New(http.StatusNotFound, apperr.CodeNotFound, "no day off on that date")
	}
	if err != nil {
		return internal(ctx, "delete day off", err)
	}
	return nil
}

// ---- bank, police certificate, agreement, PAN ----

var holderRe = regexp.MustCompile(`^[\p{L} .'-]{2,100}$`)

// ProPutBank saves the payout account: the number sealed (shared/pii), only
// its last four digits shown, IFSC checked by shared/kyc. Payouts are OFF;
// settlements are computed only.
func (s *Service) ProPutBank(ctx context.Context, uid uuid.UUID, in model.BankInput) (*model.PayoutAccount, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.AccountHolder == nil || !holderRe.MatchString(strings.TrimSpace(*in.AccountHolder)) {
		return nil, apperr.Invalid("account_holder", "account_holder must be the name on the account (letters, spaces, '.', ''', '-')")
	}
	holder := strings.Join(strings.FieldsFunc(*in.AccountHolder, unicode.IsSpace), " ")
	if in.AccountNumber == nil {
		return nil, apperr.Invalid("account_number", "account_number is required")
	}
	number, err := kyc.NormalizeBankAccountNumber(*in.AccountNumber)
	if err != nil {
		return nil, apperr.Invalid("account_number", "account_number must be 9 to 18 digits")
	}
	if in.IFSC == nil {
		return nil, apperr.Invalid("ifsc", "ifsc is required")
	}
	ifsc, err := kyc.NormalizeIFSC(*in.IFSC)
	if err != nil {
		return nil, apperr.Invalid("ifsc", "ifsc must look like ABCD0123456")
	}
	sealed, err := s.pro.PII.SealAccountNumber(ctx, number)
	if errors.Is(err, propii.ErrNotConfigured) {
		return nil, piiUnavailable()
	}
	if err != nil {
		return nil, internal(ctx, "seal account", err)
	}
	out, err := s.pro.Store.SetPayoutAccount(ctx, p.ID, holder, sealed.Blob, sealed.KeyVersion, kyc.BankAccountLast4(number), ifsc)
	if err != nil {
		return nil, internal(ctx, "set payout account", err)
	}
	if _, err := s.afterStep(ctx, p.ID); err != nil {
		return nil, err
	}
	return out, nil
}

// ProPoliceCertificate uploads a Police Clearance Certificate: a document
// pending review and a background check (source uploaded_document) bound to
// it. In uploaded_document mode an admin decides; the check is clear only
// once the certificate is approved, valid 12 months from issue.
func (s *Service) ProPoliceCertificate(ctx context.Context, uid uuid.UUID, in model.CertificateInput) (*model.ProDocument, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	media, aerr := parseMediaID("media_id", in.MediaID)
	if aerr != nil {
		return nil, aerr
	}
	issued, aerr := parseDate("issued_on", in.IssuedOn)
	if aerr != nil {
		return nil, aerr
	}
	today := s.today()
	if issued.After(today) {
		return nil, apperr.Invalid("issued_on", "issued_on cannot be in the future")
	}
	until := bgv.ValidUntil(issued)
	if !until.After(today) {
		return nil, apperr.Invalid("issued_on", "this certificate is older than 12 months; get a new one")
	}
	if err := s.verifyMedia(ctx, "media_id", media, uid, mediaclient.KindImage); err != nil {
		return nil, err
	}
	sealed, last4, err := s.sealCertificateNumber(ctx, in.CertificateNumber)
	if err != nil {
		return nil, err
	}
	provider := s.pro.BGV
	if provider == nil {
		return nil, internal(ctx, "background check", errors.New("no background-check provider configured"))
	}
	docID := s.newID()
	res, err := provider.Initiate(ctx, bgv.Request{ProID: p.ID, DocumentID: docID, IssuedOn: issued})
	if err != nil {
		return nil, internal(ctx, "background check initiate", err)
	}
	init := &store.BackgroundInit{Provider: provider.Name(), Status: res.Status, ValidFrom: res.ValidFrom, ValidUntil: res.ValidUntil}
	if init.Status != bgv.StatusClear {
		init.Status, init.ValidFrom, init.ValidUntil = bgv.StatusPending, nil, nil
	}
	doc, err := s.pro.Store.UploadDocument(ctx, store.NewDocument{ID: docID, ProID: p.ID, Kind: "police_certificate", MediaID: media,
		NumberSealed: sealed, NumberLast4: last4, IssuedOn: issued, ExpiresOn: &until}, init)
	if errors.Is(err, store.ErrConflict) {
		return nil, apperr.New(http.StatusConflict, apperr.CodeConflict, "a police certificate is already under review")
	}
	if err != nil {
		return nil, internal(ctx, "upload police certificate", err)
	}
	if _, err := s.afterStep(ctx, p.ID); err != nil {
		return nil, err
	}
	return doc, nil
}

// ProAcceptAgreement records the current agreement version and the time.
func (s *Service) ProAcceptAgreement(ctx context.Context, uid uuid.UUID, in model.AgreementInput) (*model.ProReadiness, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.Version == nil || strings.TrimSpace(*in.Version) != prokyc.AgreementVersion {
		return nil, apperr.Invalid("version", "accept the current agreement version").
			WithDetails(map[string]any{"field": "version", "current_version": prokyc.AgreementVersion})
	}
	if err := s.pro.Store.AcceptAgreement(ctx, p.ID, prokyc.AgreementVersion, s.now().UTC()); err != nil {
		return nil, internal(ctx, "accept agreement", err)
	}
	return s.afterStep(ctx, p.ID)
}

// ProPutPAN stores an individual's PAN, sealed (recommended, not required).
func (s *Service) ProPutPAN(ctx context.Context, uid uuid.UUID, in model.PANInput) (*model.ProReadiness, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.PAN == nil {
		return nil, apperr.Invalid("pan", "pan is required")
	}
	pan, err := kyc.ValidatePAN(*in.PAN)
	if err != nil {
		return nil, apperr.Invalid("pan", "pan must be ten characters like ABCPE1234F")
	}
	if pan.HolderType != kyc.PANHolderType('P') {
		return nil, apperr.Invalid("pan", "pan must be an individual's PAN (fourth letter P)")
	}
	sealed, err := s.pro.PII.SealPAN(ctx, pan.Normalized)
	if errors.Is(err, propii.ErrNotConfigured) {
		return nil, piiUnavailable()
	}
	if err != nil {
		return nil, internal(ctx, "seal pan", err)
	}
	if err := s.pro.Store.SetPAN(ctx, p.ID, sealed.Blob, pan.Normalized[len(pan.Normalized)-4:]); err != nil {
		return nil, internal(ctx, "set pan", err)
	}
	return s.afterStep(ctx, p.ID)
}

// ---- background-check vendor webhook ----

// BackgroundCheckWebhook applies a vendor callback. Only the configured
// provider, and only one that verifies webhooks, is known; no vendor exists
// at launch, so every provider answers 404.
func (s *Service) BackgroundCheckWebhook(ctx context.Context, provider string, header http.Header, body []byte) error {
	notFound := apperr.New(http.StatusNotFound, apperr.CodeNotFound, "unknown background-check provider")
	p := s.pro.BGV
	if p == nil || p.Name() != provider {
		return notFound
	}
	ev, err := p.VerifyWebhook(header, body)
	switch {
	case errors.Is(err, bgv.ErrNotSupported):
		return notFound
	case errors.Is(err, bgv.ErrSignature):
		return apperr.New(http.StatusUnauthorized, apperr.CodeWebhookSignatureInvalid, "webhook signature invalid")
	case err != nil:
		return apperr.Invalid("", "webhook body is not valid for this provider")
	}
	err = s.pro.Store.ApplyProviderCheck(ctx, provider, ev.ExternalRef, ev.Result.Status, ev.Result.ValidFrom, ev.Result.ValidUntil)
	if errors.Is(err, store.ErrNotFound) {
		return notFound
	}
	if err != nil {
		return internal(ctx, "apply provider check", err)
	}
	return nil
}
