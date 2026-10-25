package service

// Instant captain approval where a machine can decide (founder, 25 Oct:
// "nothing manual, instant approval"), on top of the review gate d61bd603c
// added (13 Oct). evaluatePartnerApproval runs after every document,
// vehicle or Aadhaar change and after every admin decision:
//
//   - a document DigiLocker returned (source digilocker) is already verified
//     (the store recorded it approved, verified_by_actor auto, audit row
//     actor system / reason digilocker);
//   - the selfie (the latest profile_photo, an upload with a media_id) is
//     compared server-side through media-service with the latest approved
//     driving licence's DigiLocker photo or, when that licence has none, its
//     hand-uploaded image once a person approved it (pickSelfieReference;
//     the reference used is recorded); a match (selfieMatches: at or above
//     the threshold, one face on each side) verifies it automatically; a
//     borderline or failed match, or no verdict at all, leaves it pending
//     for a person;
//   - a vehicle's RC is pulled from DigiLocker under the partner's
//     assertion; a vehicle whose latest RC came from DigiLocker is approved
//     automatically (autoApprovableVehicles); a hand-uploaded RC waits for
//     a person (VerifyVehicle);
//   - when the rule (evaluateApproval) finds every required document and an
//     RC verified, by the machine or by a person, the partner is approved
//     at once (AutoApprovePartner, behind the same evidence gate as the
//     admin's approval); no second manual step;
//   - otherwise the partner is "under review" with the pending documents
//     named; the under_review event is queued once per change of that set,
//     and an approval the gate refused is named partner_approval for a
//     person.
//
// Uploaded identity documents (DL, PAN, passport, voter ID) are never
// verified automatically: they keep the admin review path.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/rider-service/internal/digilocker"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// Document types the rule reads.
const (
	DocAadhaar        = digilocker.DocAadhaar
	DocDrivingLicence = digilocker.DocDrivingLicence
	DocSelfie         = "profile_photo"
	DocVehicleRC      = digilocker.DocVehicleRC
)

// requiredPartnerDocuments must each be verified before approval, plus the
// RC of at least one vehicle. Whatever the current rule requires beyond
// these is the admin's manual approval, which stays available.
var requiredPartnerDocuments = []string{DocDrivingLicence, DocSelfie}

// CodeSelfieMediaRequired: a selfie (profile_photo) submitted without the
// media-service id the face check needs.
const CodeSelfieMediaRequired = "SELFIE_MEDIA_REQUIRED"

// ErrSelfieMediaRequired answers a profile_photo upload without media_id.
var ErrSelfieMediaRequired = payErr(http.StatusBadRequest, CodeSelfieMediaRequired,
	"profile_photo needs media_id: upload the selfie to media-service first and send its id")

// Onboarding statuses the partner sees.
const (
	OnboardingApproved    = "approved"
	OnboardingUnderReview = "under_review"
	OnboardingIncomplete  = "incomplete"
	OnboardingBlocked     = "blocked"
)

// OnboardingStatus is GET /v1/rider/partners/me/onboarding.
type OnboardingStatus struct {
	// Status: approved | under_review (a human is reviewing the documents in
	// Pending) | incomplete (documents in Missing were never provided, or
	// were rejected) | blocked (suspended / blocked / rejected partner).
	Status        string   `json:"status"`
	PartnerStatus string   `json:"partner_status"`
	Pending       []string `json:"pending"`
	Missing       []string `json:"missing"`
}

// ApprovalInput is what evaluateApproval reads.
type ApprovalInput struct {
	PartnerStatus string
	Documents     map[string]store.DocumentState
	Vehicles      []store.VehicleState
}

// ApprovalVerdict is the pure verdict: Approve when everything required is
// verified; Pending the documents waiting for a human (uploaded, pending);
// Missing the documents never provided or rejected.
type ApprovalVerdict struct {
	Approve bool
	Pending []string
	Missing []string
}

// evaluateApproval is the pure approval rule.
func evaluateApproval(in ApprovalInput) ApprovalVerdict {
	v := ApprovalVerdict{Pending: []string{}, Missing: []string{}}
	switch in.PartnerStatus {
	case "draft", "pending_verification":
	default:
		// approved already, or off the journey (rejected / suspended /
		// blocked / inactive): never approved from here.
		return v
	}
	for _, t := range requiredPartnerDocuments {
		d, ok := in.Documents[t]
		switch {
		case !ok, d.Status == "rejected", d.Status == "expired":
			v.Missing = append(v.Missing, t)
		case d.Status == "approved":
		default:
			v.Pending = append(v.Pending, t)
		}
	}
	rcVerified, rcPending := false, false
	for _, veh := range in.Vehicles {
		if veh.Status == "approved" && veh.RCStatus == "approved" {
			rcVerified = true
			break
		}
		if veh.RCStatus == "pending" || (veh.Status == "pending" && veh.RCStatus != "rejected") {
			rcPending = true
		}
	}
	switch {
	case rcVerified:
	case rcPending:
		v.Pending = append(v.Pending, DocVehicleRC)
	default:
		v.Missing = append(v.Missing, DocVehicleRC)
	}
	v.Approve = len(v.Pending) == 0 && len(v.Missing) == 0
	return v
}

// evaluatePartnerApproval runs the automatic checks (selfie face match, RC
// pull and vehicle approval) and then the approval rule, approving the
// partner or queueing under_review. A failed automatic check never returns
// an error (it leaves the document pending for a person); only failed
// reads/writes do.
func (s *Service) evaluatePartnerApproval(ctx context.Context, partnerID uuid.UUID) (*OnboardingStatus, error) {
	partner, err := s.store.GetPartner(ctx, partnerID)
	if err != nil {
		return nil, err
	}
	// The automatic checks run for a partner on the journey and for an
	// approved one (a vehicle added later is approved from its DigiLocker
	// RC too); never for a rejected, suspended or blocked one.
	switch partner.Status {
	case "rejected", "suspended", "blocked":
	default:
		s.autoVerifySelfie(ctx, partner)
		s.pullVehicleRCs(ctx, partner)
		s.autoApproveVehicles(ctx, partner)
	}
	status, verdict, err := s.onboardingVerdict(ctx, partner)
	if err != nil {
		return nil, err
	}
	if verdict.Approve {
		approved, err := s.store.AutoApprovePartner(ctx, partner.ID, map[string]any{"reason": "all required documents verified"})
		if err != nil {
			return nil, fmt.Errorf("auto approve partner: %w", err)
		}
		if approved {
			slog.Info("rider: partner approved automatically", "partner_id", partner.ID)
			status.Status, status.PartnerStatus, status.Pending = OnboardingApproved, "approved", []string{}
			return status, nil
		}
		// The evidence gate refused what the rule allowed (an expired RC,
		// a licence without its evidence): status names partner_approval
		// and a person reviews it.
	}
	if status.Status == OnboardingUnderReview {
		s.publishUnderReviewOnce(ctx, partner, status.Pending)
	}
	return status, nil
}

// OnboardingStatusFor is the read-only view for the partner app.
func (s *Service) OnboardingStatusFor(ctx context.Context, userID uuid.UUID) (*OnboardingStatus, error) {
	partner, err := s.GetMyPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	status, _, err := s.onboardingVerdict(ctx, partner)
	return status, err
}

func (s *Service) onboardingVerdict(ctx context.Context, partner *store.Partner) (*OnboardingStatus, ApprovalVerdict, error) {
	docs, err := s.store.LatestPartnerDocuments(ctx, partner.ID)
	if err != nil {
		return nil, ApprovalVerdict{}, err
	}
	vehicles, err := s.store.PartnerVehicleStates(ctx, partner.ID)
	if err != nil {
		return nil, ApprovalVerdict{}, err
	}
	verdict := evaluateApproval(ApprovalInput{PartnerStatus: partner.Status, Documents: docs, Vehicles: vehicles})
	st := &OnboardingStatus{PartnerStatus: partner.Status, Pending: verdict.Pending, Missing: verdict.Missing}
	switch {
	case partner.Status == "approved":
		st.Status = OnboardingApproved
	case partner.Status == "rejected" || partner.Status == "suspended" || partner.Status == "blocked":
		st.Status = OnboardingBlocked
	case verdict.Approve:
		st.Status = OnboardingUnderReview
		st.Pending = []string{"partner_approval"}
	case len(verdict.Missing) > 0:
		st.Status = OnboardingIncomplete
	default:
		st.Status = OnboardingUnderReview
	}
	return st, verdict, nil
}

// publishUnderReviewOnce publishes rider.partner.under_review when the
// pending set differs from the one last published for the partner.
func (s *Service) publishUnderReviewOnce(ctx context.Context, partner *store.Partner, pending []string) {
	if err := s.store.RecordUnderReview(ctx, partner.ID, pending); err != nil {
		slog.Warn("rider: record under_review failed", "partner_id", partner.ID, "error", err)
	}
}

// selfieMatches is the face-match decision that verifies a selfie with no
// person: similarity at or above the threshold (DefaultSelfieMinSimilarity
// when unset) and exactly one face in the selfie and in the licence photo.
// Anything else (borderline, failed, no result) goes to a person.
func selfieMatches(res *FaceCompareResult, min float64) bool {
	if res == nil {
		return false
	}
	min = selfieThreshold(min)
	return res.Similarity >= min && res.FaceCountSource == 1 && res.FaceCountTarget == 1
}

// selfieThreshold is the configured similarity threshold, or the default
// when none is set.
func selfieThreshold(min float64) float64 {
	if min <= 0 {
		return DefaultSelfieMinSimilarity
	}
	return min
}

// Where the reference face for the selfie check came from; recorded in the
// document's auto_check_detail and the document.auto_verify audit row.
const (
	// SelfieRefDigiLockerPhoto: the photo DigiLocker returned with the
	// licence (photo_media_id).
	SelfieRefDigiLockerPhoto = "digilocker_licence_photo"
	// SelfieRefUploadedLicence: the hand-uploaded licence image (media_id),
	// once a person approved that licence.
	SelfieRefUploadedLicence = "uploaded_licence_image"
)

// selfieReference is the face the selfie is compared with.
type selfieReference struct {
	MediaID uuid.UUID
	Source  string // SelfieRefDigiLockerPhoto | SelfieRefUploadedLicence
}

// latestDocumentOfType is the partner's latest document of the type (newest
// created_at, then the highest id, the order GetPartnerDocumentByType and
// the review gate use), or nil. Latest wins (d61bd603c): an older licence
// is never evidence once a newer one exists, whatever its status.
func latestDocumentOfType(docs []store.PartnerDocument, docType string) *store.PartnerDocument {
	var latest *store.PartnerDocument
	for i := range docs {
		d := &docs[i]
		if d.DocumentType != docType {
			continue
		}
		if latest == nil || d.CreatedAt.After(latest.CreatedAt) ||
			(d.CreatedAt.Equal(latest.CreatedAt) && bytes.Compare(d.ID[:], latest.ID[:]) > 0) {
			latest = d
		}
	}
	return latest
}

// pickSelfieReference chooses what the selfie (selfieMediaID) is compared
// with, from the partner's documents. Only the latest driving licence
// counts, and only when it is approved:
//
//   - its DigiLocker photo (photo_media_id) when there is one;
//   - otherwise, a hand-uploaded licence a person approved (source upload,
//     verified_by_actor an admin, not auto) is trusted evidence: its own
//     image (media_id) is the reference;
//   - anything else (no licence, pending, rejected, expired status, no
//     image, the selfie's own image) has no reference: the selfie goes to
//     a person. The string says why.
func pickSelfieReference(docs []store.PartnerDocument, selfieMediaID uuid.UUID) (*selfieReference, string) {
	dl := latestDocumentOfType(docs, DocDrivingLicence)
	if dl == nil {
		return nil, "no driving licence yet: manual review"
	}
	if dl.Status != "approved" {
		return nil, "latest driving licence is not approved yet: manual review"
	}
	ref := &selfieReference{}
	switch {
	case dl.PhotoMediaID != nil && *dl.PhotoMediaID != uuid.Nil:
		ref.MediaID, ref.Source = *dl.PhotoMediaID, SelfieRefDigiLockerPhoto
	case dl.Source == store.DocSourceUpload && dl.MediaID != nil && *dl.MediaID != uuid.Nil &&
		dl.VerifiedByActor != nil && *dl.VerifiedByActor != "" && *dl.VerifiedByActor != store.VerifiedByAuto:
		// Fallback, only when the licence has no DigiLocker photo.
		ref.MediaID, ref.Source = *dl.MediaID, SelfieRefUploadedLicence
	default:
		return nil, "no verified driving licence photo yet: manual review"
	}
	if ref.MediaID == selfieMediaID {
		return nil, "selfie is the licence image itself: manual review"
	}
	return ref, ""
}

// autoVerifySelfie compares the latest selfie, when it is a pending upload
// with a media_id, with the reference pickSelfieReference chooses: the
// latest approved driving licence's DigiLocker photo, or, without one, that
// licence's hand-uploaded image once a person approved it. Verified only
// when selfieMatches; every other outcome leaves it pending with the reason
// (and the reference used) recorded for the reviewer.
func (s *Service) autoVerifySelfie(ctx context.Context, partner *store.Partner) {
	selfie, err := s.store.GetPartnerDocumentByType(ctx, partner.ID, DocSelfie)
	if err != nil || selfie.Status != "pending" || selfie.Source != store.DocSourceUpload {
		return
	}
	if selfie.MediaID == nil {
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, "no media_id: manual review")
		return
	}
	docs, err := s.store.ListPartnerDocuments(ctx, partner.ID)
	if err != nil {
		slog.Warn("rider: read documents for the selfie check failed", "partner_id", partner.ID, "error", err)
		return
	}
	ref, why := pickSelfieReference(docs, *selfie.MediaID)
	if ref == nil {
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, why)
		return
	}
	if s.faceCompare == nil {
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, "face compare not configured: manual review")
		return
	}
	res, err := s.faceCompare.CompareFaces(ctx, FaceCompareRequest{SourceMediaID: *selfie.MediaID, TargetMediaID: ref.MediaID, RequesterUserID: partner.UserID})
	if err != nil {
		detail := "face compare unavailable: manual review"
		switch {
		case errors.Is(err, ErrFaceMediaNotFound):
			detail = "selfie or licence photo not found in media-service: manual review"
		case errors.Is(err, ErrFaceImageUnsupported):
			detail = "selfie cannot be compared: manual review"
		}
		slog.Warn("rider: selfie face compare gave no verdict", "partner_id", partner.ID, "document_id", selfie.ID, "reference", ref.Source, "error", err)
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, detail+" (reference "+ref.Source+")")
		return
	}
	min := selfieThreshold(s.selfieMinSimilarity)
	detail := fmt.Sprintf("face_compare reference %s similarity %.1f threshold %.0f faces %d/%d provider %s", ref.Source, res.Similarity, min, res.FaceCountSource, res.FaceCountTarget, res.Provider)
	if !selfieMatches(res, min) {
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, "below threshold: manual review ("+detail+")")
		return
	}
	if _, err := s.store.AutoVerifyPartnerDocument(ctx, selfie.ID, "face_compare", detail); err != nil {
		slog.Warn("rider: auto verify selfie failed", "document_id", selfie.ID, "error", err)
	}
}

// pullVehicleRCs fetches from DigiLocker the RC of every vehicle that has no
// verified RC yet, once the partner's DigiLocker assertion exists, and
// records each one the issuer returns. A vehicle the issuer does not return
// keeps its hand-uploaded RC, which waits for a person.
func (s *Service) pullVehicleRCs(ctx context.Context, partner *store.Partner) {
	if s.digilockerClient == nil {
		return
	}
	vehicles, err := s.store.PartnerVehicleStates(ctx, partner.ID)
	if err != nil {
		return
	}
	var regs []string
	byReg := map[string]uuid.UUID{}
	for _, v := range vehicles {
		if v.RCStatus == "approved" || v.Status == "rejected" {
			continue
		}
		regs = append(regs, v.RegistrationNumber)
		byReg[v.RegistrationNumber] = v.ID
	}
	if len(regs) == 0 {
		return
	}
	av, err := s.store.GetAadhaarVerification(ctx, partner.ID)
	if err != nil || av == nil || av.DigiLockerRef == "" {
		return
	}
	docs, err := s.digilockerClient.FetchDocuments(ctx, av.DigiLockerRef, digilocker.DocumentRequest{VehicleRegistrations: regs})
	if err != nil {
		slog.Warn("rider: digilocker rc fetch failed; vehicles stay pending", "partner_id", partner.ID, "error", err)
		return
	}
	for _, d := range docs {
		if d.Type != DocVehicleRC {
			continue
		}
		vid, ok := byReg[strings.ToUpper(strings.TrimSpace(d.RegistrationNumber))]
		if !ok {
			continue
		}
		if _, err := s.store.UpsertDigiLockerVehicleRC(ctx, vid, d.FileURL, d.ExpiresAt, d.Reference); err != nil {
			slog.Warn("rider: record digilocker rc failed", "vehicle_id", vid, "error", err)
		}
	}
}

// autoApprovableVehicles is the RC decision: a pending vehicle is approved
// with no person only when its latest RC came from DigiLocker and is
// approved. A hand-uploaded RC (source upload), pending or not, goes to a
// person; a rejected vehicle stays rejected.
func autoApprovableVehicles(vehicles []store.VehicleState) []uuid.UUID {
	var out []uuid.UUID
	for _, v := range vehicles {
		if v.Status == "pending" && v.RCStatus == "approved" && v.RCSource == store.DocSourceDigiLocker {
			out = append(out, v.ID)
		}
	}
	return out
}

// autoApproveVehicles approves the vehicles autoApprovableVehicles picks;
// the store re-checks the same rule under the row lock.
func (s *Service) autoApproveVehicles(ctx context.Context, partner *store.Partner) {
	vehicles, err := s.store.PartnerVehicleStates(ctx, partner.ID)
	if err != nil {
		slog.Warn("rider: read vehicles for auto approval failed", "partner_id", partner.ID, "error", err)
		return
	}
	for _, id := range autoApprovableVehicles(vehicles) {
		if _, err := s.store.AutoApproveVehicle(ctx, id); err != nil {
			slog.Warn("rider: auto approve vehicle failed", "vehicle_id", id, "error", err)
		}
	}
}

// importDigiLockerDocuments pulls the partner's Aadhaar and DL under the
// fresh assertion and records them verified. Errors are logged: the
// Aadhaar assertion itself is already stored, and the upload path remains.
func (s *Service) importDigiLockerDocuments(ctx context.Context, partner *store.Partner, assertionRef string) {
	if s.digilockerClient == nil || assertionRef == "" {
		return
	}
	docs, err := s.digilockerClient.FetchDocuments(ctx, assertionRef, digilocker.DocumentRequest{Aadhaar: true, DrivingLicence: true})
	if err != nil {
		slog.Warn("rider: digilocker document fetch failed; partner uploads instead", "partner_id", partner.ID, "error", err)
		return
	}
	for _, d := range docs {
		if d.Type != DocAadhaar && d.Type != DocDrivingLicence {
			continue
		}
		in := store.UpsertDigiLockerDocumentInput{PartnerID: partner.ID, DocumentType: d.Type, FileURL: d.FileURL, ExpiresAt: d.ExpiresAt, PhotoMediaID: d.PhotoMediaID, DigiLockerRef: d.Reference}
		if d.Type != DocAadhaar {
			in.DocumentNumber = d.Number // DPDP: an Aadhaar number is never stored
		}
		if _, err := s.store.UpsertDigiLockerDocument(ctx, in); err != nil {
			slog.Warn("rider: record digilocker document failed", "partner_id", partner.ID, "type", d.Type, "error", err)
		}
	}
}

// evaluateApprovalQuietly is the fire-and-forget form used after a change.
func (s *Service) evaluateApprovalQuietly(ctx context.Context, partnerID uuid.UUID) {
	if _, err := s.evaluatePartnerApproval(ctx, partnerID); err != nil {
		slog.Warn("rider: partner approval evaluation failed", "partner_id", partnerID, "error", err)
	}
}
