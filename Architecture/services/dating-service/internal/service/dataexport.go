// DPDP data export + purge service — Sprint 5.
//
// See PULSE_DATING_SPEC.md §15.8. Two flows here:
//
//   - RequestExport / GetExport / ListExports: user-facing. Creates a row
//     in `dating_data_exports`, emits a Kafka event for the async exporter.
//
//   - BuildExportPayload / FulfillExport: called by cmd/data-exporter. The
//     exporter pulls every row owned by the user, anonymises other-party
//     PII, writes the JSON to blob storage, and stamps the download URL.
//
// CRITICAL RULES (DPDP):
//   - The Aadhaar NUMBER is never read or written by this code. Only
//     verification *status* + timestamps are surfaced.
//   - Other-party data (matches, vouches, sparks) is reduced to user_ids;
//     we never serialise the other user's profile.
//   - Download URLs expire 7 days after issuance.
//
// PurgeProfile (called by cmd/data-purger) is the 30-day grace deletion. It
// uses store.PurgeUserData inside one transaction and emits
// dating.profile.purged on success.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// DataExportPublisher is the Kafka hook used by the request flow. The
// production wiring is dating-service's *events.Producer; tests inject a
// stub.
type DataExportPublisher interface {
	PublishDataExportRequested(ctx context.Context, exportID, userID uuid.UUID) error
	PublishDataExportReady(ctx context.Context, exportID, userID uuid.UUID, downloadURL string, downloadExpires time.Time) error
}

// NotificationClient is the in-app/push notification fanout for the
// data-export ready signal. The production wiring uses notification-service
// over HTTP; tests stub it.
type NotificationClient interface {
	NotifyDataExportReady(ctx context.Context, userID uuid.UUID, downloadURL string, expiresAt time.Time) error
}

// ExportStorageClient is the blob writer. Production wiring uses media-
// service signed URLs; tests stub it.
type ExportStorageClient interface {
	WriteExport(ctx context.Context, exportID uuid.UUID, payload []byte) (downloadURL string, expiresAt time.Time, err error)
}

// SetDataExportPublisher injects the Kafka publisher (typically the same
// *events.Producer that already lives on the service).
func (s *Service) SetDataExportPublisher(p DataExportPublisher) {
	s.dataExportPublisher = p
}

// SetNotificationClient injects the user-notification fan-out.
func (s *Service) SetNotificationClient(c NotificationClient) {
	s.notificationClient = c
}

// SetExportStorageClient injects the blob writer.
func (s *Service) SetExportStorageClient(c ExportStorageClient) {
	s.storageClient = c
}

// dataExportRateWindow is the spec §15.8 rate gate.
const dataExportRateWindow = 7 * 24 * time.Hour

// RequestDataExport creates a pending export row, emits the requested
// event, and returns the row. Rate-limited to one per 7 days.
func (s *Service) RequestDataExport(ctx context.Context, userID uuid.UUID) (*store.DataExport, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}

	latest, err := s.store.LatestExportForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if latest != nil {
		// In-flight or recent export blocks the request. Spec §15.8.
		if latest.Status == "pending" || latest.Status == "processing" {
			return latest, nil
		}
		if time.Since(latest.RequestedAt) < dataExportRateWindow {
			return nil, fmt.Errorf("forbidden: data export rate limited; one per 7 days")
		}
	}

	out, err := s.store.CreateDataExportRequest(ctx, userID)
	if err != nil {
		return nil, err
	}

	publisher := s.dataExportPublisher
	if publisher == nil && s.producer != nil {
		// Default: use the dating-service Kafka producer.
		publisher = s.producer
	}
	if publisher != nil {
		if err := publisher.PublishDataExportRequested(ctx, out.ID, userID); err != nil {
			slog.Warn("publish data.export.requested failed", "export_id", out.ID, "error", err)
		}
	}
	return out, nil
}

// ListMyExports returns the user's export history.
func (s *Service) ListMyExports(ctx context.Context, userID uuid.UUID) ([]*store.DataExport, error) {
	return s.store.ListDataExportsForUser(ctx, userID)
}

// --- Exporter-side flow ----------------------------------------------------

// UserDataExport is the JSON shape written to blob storage. Field naming
// matches what the data-portability portal will surface to the user.
//
// DPDP §15.8 — Aadhaar number is never present. We expose only:
//   - aadhaar_status, aadhaar_at, digilocker_ref, doc_type_hash
type UserDataExport struct {
	UserID           uuid.UUID                   `json:"user_id"`
	GeneratedAt      time.Time                   `json:"generated_at"`
	PolicyVersion    string                      `json:"policy_version"`
	Profile          *store.Profile              `json:"profile,omitempty"`
	Tune             *store.Tune                 `json:"tune,omitempty"`
	Photos           []store.Photo               `json:"photos,omitempty"`
	Prompts          []store.Prompt              `json:"prompts,omitempty"`
	Preferences      *store.Preferences          `json:"preferences,omitempty"`
	SparksSent       []ExportedSpark             `json:"sparks_sent,omitempty"`
	SparksReceived   []ExportedSpark             `json:"sparks_received,omitempty"`
	Stashes          []ExportedStash             `json:"stashes,omitempty"`
	Matches          []ExportedMatch             `json:"matches,omitempty"`
	VouchesSent      []ExportedVouch             `json:"vouches_sent,omitempty"`
	VouchesReceived  []ExportedVouch             `json:"vouches_received,omitempty"`
	Verification     *ExportedVerification       `json:"verification,omitempty"`
	SafetyEvents     []ExportedSafetyEvent       `json:"safety_events,omitempty"`
	Subscription     *store.PremiumSubscription  `json:"subscription,omitempty"`
	PaymentHistory   []ExportedPaymentIntent     `json:"payment_history,omitempty"`
	ConsentLog       []*store.ConsentEntry       `json:"consent_log,omitempty"`
}

// ExportedSpark is the redacted Spark shape — counterparty is just an id.
type ExportedSpark struct {
	ID         uuid.UUID `json:"id"`
	OtherParty uuid.UUID `json:"other_party_user_id"`
	TargetKind string    `json:"target_kind"`
	TargetRef  string    `json:"target_ref"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ExportedStash is the redacted stash entry.
type ExportedStash struct {
	CandidateID uuid.UUID `json:"candidate_user_id"`
	StashedAt   time.Time `json:"stashed_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// ExportedMatch reduces match counterpart to user id only.
type ExportedMatch struct {
	MatchID    uuid.UUID  `json:"match_id"`
	OtherParty uuid.UUID  `json:"other_party_user_id"`
	Status     string     `json:"status"`
	MatchedAt  time.Time  `json:"matched_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// ExportedVouch reduces counterpart to user id only.
type ExportedVouch struct {
	VouchID    uuid.UUID `json:"vouch_id"`
	OtherParty uuid.UUID `json:"other_party_user_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// ExportedVerification surfaces only DPDP-safe fields.
//
// CRITICAL: we never include the Aadhaar number — by construction, it is
// never stored.
type ExportedVerification struct {
	SelfieStatus  *string    `json:"selfie_status,omitempty"`
	SelfieScore   *float64   `json:"selfie_score,omitempty"`
	SelfieAt      *time.Time `json:"selfie_at,omitempty"`
	AadhaarStatus *string    `json:"aadhaar_status,omitempty"`
	AadhaarAt     *time.Time `json:"aadhaar_at,omitempty"`
	DigilockerRef *string    `json:"digilocker_ref,omitempty"`
	DocTypeHash   *string    `json:"doc_type_hash,omitempty"`
}

// ExportedSafetyEvent is the user-visible subset of dating_safety_events.
type ExportedSafetyEvent struct {
	ID        uuid.UUID      `json:"id"`
	Kind      string         `json:"kind"`
	Details   map[string]any `json:"details,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// ExportedPaymentIntent is what the export portal surfaces under "Payments".
type ExportedPaymentIntent struct {
	IntentID       uuid.UUID  `json:"intent_id"`
	PlanID         string     `json:"plan_id"`
	AmountINRPaise int64      `json:"amount_inr_paise"`
	Status         string     `json:"status"`
	Source         string     `json:"source"`
	CreatedAt      time.Time  `json:"created_at"`
	PaidAt         *time.Time `json:"paid_at,omitempty"`
}

// BuildExportPayload assembles the UserDataExport shape from every dating
// store. Returned bytes are JSON-marshalled and ready for blob storage.
func (s *Service) BuildExportPayload(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}

	out := &UserDataExport{
		UserID:        userID,
		GeneratedAt:   time.Now().UTC(),
		PolicyVersion: s.consentPolicy(),
	}

	// Profile (may be soft-deleted; we still include it).
	if p, err := s.profileForExport(ctx, userID); err != nil {
		return nil, err
	} else {
		out.Profile = p
	}

	if t, err := s.store.GetTune(ctx, userID); err == nil {
		out.Tune = t
	}
	if photos, err := s.store.ListPhotos(ctx, userID); err == nil {
		out.Photos = photos
	}
	if prompts, err := s.store.ListPrompts(ctx, userID); err == nil {
		out.Prompts = prompts
	}
	if prefs, err := s.store.GetPreferences(ctx, userID); err == nil {
		out.Preferences = prefs
	}

	if sent, err := s.store.ListSparksSent(ctx, userID); err == nil {
		for _, sp := range sent {
			out.SparksSent = append(out.SparksSent, ExportedSpark{
				ID: sp.ID, OtherParty: sp.ToUserID,
				TargetKind: sp.TargetKind, TargetRef: sp.TargetRef,
				Note: derefString(sp.Note), CreatedAt: sp.CreatedAt,
			})
		}
	}
	if recv, err := s.store.ListSparksReceived(ctx, userID); err == nil {
		for _, sp := range recv {
			out.SparksReceived = append(out.SparksReceived, ExportedSpark{
				ID: sp.ID, OtherParty: sp.FromUserID,
				TargetKind: sp.TargetKind, TargetRef: sp.TargetRef,
				Note: derefString(sp.Note), CreatedAt: sp.CreatedAt,
			})
		}
	}
	if stashes, err := s.store.ListStash(ctx, userID); err == nil {
		for _, st := range stashes {
			out.Stashes = append(out.Stashes, ExportedStash{
				CandidateID: st.CandidateID, StashedAt: st.StashedAt, ExpiresAt: st.ExpiresAt,
			})
		}
	}
	if matches, err := s.store.ListMatchesForUser(ctx, userID, "all"); err == nil {
		for _, m := range matches {
			other := m.UserB
			if m.UserB == userID {
				other = m.UserA
			}
			out.Matches = append(out.Matches, ExportedMatch{
				MatchID: m.ID, OtherParty: other, Status: m.Status,
				MatchedAt: m.MatchedAt, ExpiresAt: m.ExpiresAt,
			})
		}
	}
	if sent, err := s.store.ListVouchesSent(ctx, userID); err == nil {
		for _, v := range sent {
			out.VouchesSent = append(out.VouchesSent, ExportedVouch{
				VouchID: v.ID, OtherParty: v.VoucheeID, Status: v.Status, CreatedAt: v.CreatedAt,
			})
		}
	}
	if recv, err := s.store.ListVouchesFor(ctx, userID, ""); err == nil {
		for _, v := range recv {
			out.VouchesReceived = append(out.VouchesReceived, ExportedVouch{
				VouchID: v.ID, OtherParty: v.VoucherID, Status: v.Status, CreatedAt: v.CreatedAt,
			})
		}
	}
	if v, err := s.store.GetVerification(ctx, userID); err == nil {
		out.Verification = &ExportedVerification{
			SelfieStatus:  v.SelfieStatus,
			SelfieScore:   v.SelfieScore,
			SelfieAt:      v.SelfieAt,
			AadhaarStatus: v.AadhaarStatus,
			AadhaarAt:     v.AadhaarAt,
			DigilockerRef: v.DigilockerRef,
			DocTypeHash:   v.DocTypeHash,
		}
	}
	if events, err := s.store.ListSafetyEventsForUser(ctx, userID); err == nil {
		for _, ev := range events {
			out.SafetyEvents = append(out.SafetyEvents, ExportedSafetyEvent{
				ID: ev.ID, Kind: ev.Kind, Details: ev.Details, CreatedAt: ev.CreatedAt,
			})
		}
	}
	if sub, err := s.store.GetSubscription(ctx, userID); err == nil {
		out.Subscription = sub
	}
	if intents, err := s.store.ListPaymentIntentsForUser(ctx, userID); err == nil {
		for _, p := range intents {
			out.PaymentHistory = append(out.PaymentHistory, ExportedPaymentIntent{
				IntentID: p.ID, PlanID: p.PlanID, AmountINRPaise: p.AmountINRPaise,
				Status: p.Status, Source: p.Source, CreatedAt: p.CreatedAt, PaidAt: p.PaidAt,
			})
		}
	}
	if consent, err := s.store.ListConsentForUser(ctx, userID); err == nil {
		out.ConsentLog = consent
	}

	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal export: %w", err)
	}
	return buf, nil
}

// profileForExport reads the (possibly soft-deleted) profile row.
func (s *Service) profileForExport(ctx context.Context, userID uuid.UUID) (*store.Profile, error) {
	p, err := s.store.GetProfile(ctx, userID)
	if err == nil {
		return p, nil
	}
	if errors.Is(err, store.ErrProfileNotFound) {
		return nil, nil
	}
	return nil, err
}

// FulfillExport runs the exporter side of the flow. It is called by
// cmd/data-exporter for one export id at a time.
func (s *Service) FulfillExport(ctx context.Context, exportID uuid.UUID) error {
	if exportID == uuid.Nil {
		return fmt.Errorf("invalid: export_id required")
	}
	if s.storageClient == nil {
		return fmt.Errorf("storage client not configured")
	}
	export, err := s.store.GetDataExport(ctx, exportID)
	if err != nil {
		return err
	}
	if export.Status == "ready" || export.Status == "expired" {
		return nil
	}
	if err := s.store.MarkDataExportProcessing(ctx, exportID); err != nil {
		return err
	}
	payload, err := s.BuildExportPayload(ctx, export.UserID)
	if err != nil {
		_ = s.store.FailDataExport(ctx, exportID)
		return err
	}
	url, expires, err := s.storageClient.WriteExport(ctx, exportID, payload)
	if err != nil {
		_ = s.store.FailDataExport(ctx, exportID)
		return fmt.Errorf("write export blob: %w", err)
	}
	if err := s.store.CompleteDataExport(ctx, exportID, url, expires); err != nil {
		return err
	}

	publisher := s.dataExportPublisher
	if publisher == nil && s.producer != nil {
		publisher = s.producer
	}
	if publisher != nil {
		if err := publisher.PublishDataExportReady(ctx, exportID, export.UserID, url, expires); err != nil {
			slog.Warn("publish data.export.ready failed", "export_id", exportID, "error", err)
		}
	}
	if s.notificationClient != nil {
		if err := s.notificationClient.NotifyDataExportReady(ctx, export.UserID, url, expires); err != nil {
			slog.Warn("notify data export ready failed", "user_id", export.UserID, "error", err)
		}
	}
	return nil
}

// PurgeProfile is the 30-day grace cleanup. Called by cmd/data-purger.
func (s *Service) PurgeProfile(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	rows, err := s.store.PurgeUserData(ctx, userID)
	if err != nil {
		return err
	}
	if s.producer != nil {
		_ = s.producer.PublishProfilePurged(ctx, userID, "dpdp_grace_expired")
	}
	slog.Info("dpdp purge complete", "user_id", userID, "rows_affected", rows)
	return nil
}

// RecordConsent is the helper called from any flow that toggles consent
// (Echoes, Aadhaar, AI moderation, location share). Wraps the store with
// the configured policy version.
func (s *Service) RecordConsent(ctx context.Context, userID uuid.UUID, consentType string, granted bool) error {
	return s.store.RecordConsent(ctx, userID, consentType, granted, s.consentPolicy())
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
