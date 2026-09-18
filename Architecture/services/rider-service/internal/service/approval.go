package service

// Instant captain approval where the data is government-verified.
//
// Founder rule: nothing a captain does waits for a human, except one
// fallback — a document the captain uploaded by hand (source upload), which
// keeps the admin review path. evaluatePartnerApproval runs after every
// document, vehicle or Aadhaar change and after every admin verification:
//
//   - a document DigiLocker returned (source digilocker) is already verified
//     (the store recorded it approved, verified_by_actor auto, audit row
//     actor system / reason digilocker), likewise a vehicle whose RC came
//     from DigiLocker;
//   - the selfie (profile_photo upload with a media_id) is compared
//     server-side with the DL photo through media-service; at or above the
//     threshold it is verified automatically; below it, or with
//     media-service unavailable, it stays pending review — never verified;
//   - when every required document (Aadhaar, DL, selfie) and the RC of at
//     least one vehicle are verified, the partner is approved automatically
//     (audit row, the partner-approved event, the identity role intent);
//   - otherwise the partner is "under review" with the pending documents
//     named; the under_review event is published once per change of that
//     set.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/digilocker"
	"github.com/atpost/rider-service/internal/store"
	sharedevents "github.com/atpost/shared/events"
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
var requiredPartnerDocuments = []string{DocAadhaar, DocDrivingLicence, DocSelfie}

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

// evaluatePartnerApproval runs the automatic checks (selfie compare, RC
// pull for new vehicles) and then the approval rule, approving the partner
// or publishing under_review. It never returns an error for a failed
// automatic check (that leaves the document pending); only reads/writes
// that failed.
func (s *Service) evaluatePartnerApproval(ctx context.Context, partnerID uuid.UUID) (*OnboardingStatus, error) {
	partner, err := s.store.GetPartner(ctx, partnerID)
	if err != nil {
		return nil, err
	}
	s.autoVerifySelfie(ctx, partner)
	s.pullVehicleRCs(ctx, partner)

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
			if perr := s.producer.PublishPartnerStatusChange(ctx, sharedevents.EventRiderPartnerApproved, partner.ID, partner.UserID, "approved", "auto: all required documents verified", store.SystemActorID); perr != nil {
				slog.Warn("rider: publish partner.approved failed", "partner_id", partner.ID, "error", perr)
			}
			slog.Info("rider: partner approved automatically", "partner_id", partner.ID)
		}
		status.Status, status.PartnerStatus = OnboardingApproved, "approved"
		return status, nil
	}
	if status.Status == OnboardingUnderReview {
		s.publishUnderReviewOnce(ctx, partner, verdict.Pending)
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
		st.Status = OnboardingApproved
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
	last, recorded, err := s.store.UnderReviewPending(ctx, partner.ID)
	if err != nil {
		slog.Warn("rider: read last under_review failed", "partner_id", partner.ID, "error", err)
		return
	}
	if recorded && strings.Join(last, ",") == strings.Join(pending, ",") {
		return
	}
	if err := s.store.RecordUnderReview(ctx, partner.ID, pending); err != nil {
		slog.Warn("rider: record under_review failed", "partner_id", partner.ID, "error", err)
		return
	}
	if perr := s.producer.PublishPartnerUnderReview(ctx, sharedevents.RiderPartnerUnderReviewPayload{
		PartnerID: partner.ID.String(), PartnerUserID: partner.UserID.String(), Pending: pending, OccurredAt: time.Now().UTC(),
	}); perr != nil {
		slog.Warn("rider: publish partner.under_review failed", "partner_id", partner.ID, "error", perr)
	}
}

// autoVerifySelfie compares a pending uploaded selfie (media_id) with the
// DigiLocker DL photo (photo_media_id). Verified only at or above the
// threshold; every other outcome leaves it pending with the reason
// recorded.
func (s *Service) autoVerifySelfie(ctx context.Context, partner *store.Partner) {
	selfie, err := s.store.GetPartnerDocumentByType(ctx, partner.ID, DocSelfie)
	if err != nil || selfie.Status != "pending" || selfie.Source != store.DocSourceUpload {
		return
	}
	if selfie.MediaID == nil {
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, "no media_id: manual review")
		return
	}
	dl, err := s.store.GetPartnerDocumentByType(ctx, partner.ID, DocDrivingLicence)
	if err != nil || dl.Status != "approved" || dl.PhotoMediaID == nil {
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, "no verified driving licence photo yet")
		return
	}
	if s.faceCompare == nil {
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, "face compare not configured: manual review")
		return
	}
	res, err := s.faceCompare.CompareFaces(ctx, FaceCompareRequest{SourceMediaID: *selfie.MediaID, TargetMediaID: *dl.PhotoMediaID, RequesterUserID: partner.UserID})
	if err != nil {
		detail := "face compare unavailable: manual review"
		switch {
		case errors.Is(err, ErrFaceMediaNotFound):
			detail = "selfie or licence photo not found in media-service: manual review"
		case errors.Is(err, ErrFaceImageUnsupported):
			detail = "selfie cannot be compared: manual review"
		}
		slog.Warn("rider: selfie face compare gave no verdict", "partner_id", partner.ID, "document_id", selfie.ID, "error", err)
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, detail)
		return
	}
	min := s.selfieMinSimilarity
	if min <= 0 {
		min = DefaultSelfieMinSimilarity
	}
	detail := fmt.Sprintf("face_compare similarity %.1f threshold %.0f provider %s", res.Similarity, min, res.Provider)
	if res.Similarity < min || res.FaceCountSource != 1 || res.FaceCountTarget != 1 {
		_ = s.store.SetDocumentAutoCheckDetail(ctx, selfie.ID, "below threshold: manual review ("+detail+")")
		return
	}
	if _, err := s.store.AutoVerifyPartnerDocument(ctx, selfie.ID, "face_compare", detail); err != nil {
		slog.Warn("rider: auto verify selfie failed", "document_id", selfie.ID, "error", err)
	}
}

// pullVehicleRCs fetches from DigiLocker the RC of every vehicle that has no
// verified RC yet, once the partner's Aadhaar assertion exists. A vehicle
// the issuer does not return stays pending (the upload path).
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
		if v.RCStatus == "approved" {
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
