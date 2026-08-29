package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/geo"
	"github.com/atpost/rider-service/internal/matcher"
	"github.com/atpost/rider-service/internal/otp"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// Ride state machine. Map[from][to]bool. Anything not in the map is rejected
// with 409. Cancellation paths are listed exhaustively rather than as a
// wildcard so a future state addition forces an explicit decision.
var allowedRideTransitions = map[string]map[string]bool{
	"requested": {
		"searching_partner":     true,
		"cancelled_by_customer": true,
		"cancelled_by_admin":    true,
		"expired":               true,
		"failed":                true,
	},
	"searching_partner": {
		"partner_assigned":      true,
		"cancelled_by_customer": true,
		"cancelled_by_admin":    true,
		"expired":               true,
		"failed":                true,
	},
	"partner_assigned": {
		"partner_arriving":      true,
		"cancelled_by_customer": true,
		"cancelled_by_partner":  true,
		"cancelled_by_admin":    true,
		"failed":                true,
	},
	"partner_arriving": {
		"arrived":               true,
		"cancelled_by_customer": true,
		"cancelled_by_partner":  true,
		"cancelled_by_admin":    true,
		"failed":                true,
	},
	"arrived": {
		"otp_verified":          true,
		"cancelled_by_customer": true,
		"cancelled_by_partner":  true,
		"cancelled_by_admin":    true,
		"failed":                true,
	},
	"otp_verified": {
		"in_progress": true,
		"failed":      true,
	},
	"in_progress": {
		"completed":             true,
		"cancelled_by_customer": true,
		"cancelled_by_partner":  true,
		"cancelled_by_admin":    true,
		"failed":                true,
	},
	// Terminal states: completed, cancelled_*, expired, failed have no allowed exits.
}

// validRideTransition returns nil if (from -> to) is permitted by the state
// machine, otherwise an "invalid_transition" error.
func validRideTransition(from, to string) error {
	if from == to {
		return fmt.Errorf("invalid_transition: already in %s", to)
	}
	allowed, ok := allowedRideTransitions[from]
	if !ok {
		return fmt.Errorf("invalid_transition: %s is terminal", from)
	}
	if !allowed[to] {
		return fmt.Errorf("invalid_transition: %s -> %s not allowed", from, to)
	}
	return nil
}

// ErrInvalidRideTransition wraps a state-machine rejection. Handlers map this
// to HTTP 409.
var ErrInvalidRideTransition = errors.New("ride: invalid state transition")

// transitionRide is the in-tx core: validate the (from -> to) pair, run the
// guarded UPDATE (which fails atomically if another writer transitioned the
// row), and append a status-history row capturing the actor.
//
// The actor fields are required so audit trails can answer "who cancelled?"
// at any point in S3 admin reviews.
func (s *Service) transitionRide(ctx context.Context, ride *store.Ride, to, actorKind string, actorUserID *uuid.UUID, reason *string) error {
	if err := validRideTransition(ride.Status, to); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRideTransition, err)
	}
	if err := s.store.TransitionRide(ctx, ride.ID, ride.Status, to); err != nil {
		if errors.Is(err, store.ErrInvalidTransition) {
			return ErrInvalidRideTransition
		}
		return err
	}
	from := ride.Status
	if err := s.store.AppendStatusHistory(ctx, ride.ID, &from, to, actorKind, actorUserID, reason); err != nil {
		// Status-history failures are non-recoverable from the audit POV but
		// the row is already moved. Log loudly so SRE can backfill.
		slog.Error("rider: append status history failed",
			"ride_id", ride.ID, "from", from, "to", to, "error", err)
	}
	ride.Status = to
	return nil
}

// --- Matching -------------------------------------------------------------

// MatchRideOptions tunes MatchRide. Sane defaults applied by the service.
type MatchRideOptions struct {
	BatchSize         int           // default 5
	OfferTimer        time.Duration // default 15s
	InitialRadiusKM   float64       // default 5
	MaxRadiusKM       float64       // default 20
	GeohashPrecision  int           // default 6
	MaxCandidatesScan int           // default 50
}

func (o *MatchRideOptions) defaults() {
	if o.BatchSize <= 0 {
		o.BatchSize = 5
	}
	if o.OfferTimer <= 0 {
		o.OfferTimer = 15 * time.Second
	}
	if o.InitialRadiusKM <= 0 {
		o.InitialRadiusKM = 5
	}
	if o.MaxRadiusKM <= 0 {
		o.MaxRadiusKM = 20
	}
	if o.GeohashPrecision <= 0 {
		o.GeohashPrecision = 6
	}
	if o.MaxCandidatesScan <= 0 {
		o.MaxCandidatesScan = 50
	}
}

// MatchRideResult is the matcher fan-out outcome.
type MatchRideResult struct {
	OffersCreated int       `json:"offers_created"`
	BatchExpires  time.Time `json:"batch_expires"`
	NoCandidates  bool      `json:"no_candidates"`
}

// MatchRide drives one matching pass for a ride: transitions the ride to
// `searching_partner`, looks up nearby online partners (Redis fast-path,
// Postgres geohash fallback), filters + scores them, and inserts the top-N
// `rider_ride_offers` rows with a 15s expiry. One Kafka offer event per row.
func (s *Service) MatchRide(ctx context.Context, rideID uuid.UUID, opts MatchRideOptions) (*MatchRideResult, error) {
	opts.defaults()
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	// Move requested -> searching_partner. Idempotent: already-searching is OK.
	if ride.Status == "requested" {
		if err := s.transitionRide(ctx, ride, "searching_partner", "system", nil, nil); err != nil {
			return nil, err
		}
	} else if ride.Status != "searching_partner" {
		return nil, fmt.Errorf("invalid: ride is in %s; matcher only runs in requested/searching_partner", ride.Status)
	}

	candidates, err := s.findNearbyCandidates(ctx, ride, opts)
	if err != nil {
		return nil, fmt.Errorf("find candidates: %w", err)
	}
	cityStr := ""
	if ride.CityID != nil {
		cityStr = ride.CityID.String()
	}
	kept, _ := matcher.FilterCandidates(matcher.RideRequest{
		VehicleType: ride.VehicleType,
		CityID:      cityStr,
	}, candidates)
	if len(kept) == 0 {
		return &MatchRideResult{NoCandidates: true}, nil
	}
	ranked := matcher.Rank(kept)
	now := time.Now().UTC()
	batch := matcher.BatchOffer(ranked, opts.BatchSize, opts.OfferTimer, now)
	created := 0
	for _, sc := range batch.Selected {
		pid, err := uuid.Parse(sc.Candidate.PartnerID)
		if err != nil {
			slog.Warn("rider: skip candidate with invalid partner_id", "partner_id", sc.Candidate.PartnerID)
			continue
		}
		dist := sc.Candidate.DistanceKM
		offer, err := s.store.CreateRideOffer(ctx, store.CreateOfferInput{
			RideID:     rideID,
			PartnerID:  pid,
			Score:      sc.Score,
			DistanceKM: &dist,
			ExpiresAt:  batch.ExpiresAt,
		})
		if err != nil {
			slog.Warn("rider: create offer failed", "ride_id", rideID, "partner_id", pid, "error", err)
			continue
		}
		created++
		if perr := s.producer.PublishRideOffered(ctx, rideID, offer.ID, pid, sc.Score, batch.ExpiresAt); perr != nil {
			slog.Warn("rider: publish ride.offered failed", "ride_id", rideID, "offer_id", offer.ID, "error", perr)
		}
		s.emit(ctx, "rider.partner."+pid.String()+".offers", "rider.ride.offered", offer)
	}
	return &MatchRideResult{OffersCreated: created, BatchExpires: batch.ExpiresAt}, nil
}

// findNearbyCandidates resolves the partner-discovery hot path.
//
// Order of attempts:
//  1. Redis GEOSEARCH on rider:online:<city> centered on the pickup point.
//  2. Postgres geohash neighbors fallback when Redis returns 0 (cold start
//     or Redis outage).
//
// Returns matcher-shaped candidates with distance pre-computed so the
// matcher can score without further I/O.
func (s *Service) findNearbyCandidates(ctx context.Context, ride *store.Ride, opts MatchRideOptions) ([]matcher.PartnerCandidate, error) {
	cityStr := ""
	if ride.CityID != nil {
		cityStr = ride.CityID.String()
	}
	partnerIDs, dists := s.discoverFromRedis(ctx, cityStr, ride.PickupLat, ride.PickupLng, opts.InitialRadiusKM, opts.MaxRadiusKM)
	if len(partnerIDs) == 0 {
		gh := geo.Encode(ride.PickupLat, ride.PickupLng, opts.GeohashPrecision)
		neighbors := geo.Neighbors(gh)
		if len(neighbors) == 0 {
			neighbors = []string{gh}
		}
		locs, err := s.store.FindOnlinePartnersByGeohash(ctx, neighbors, opts.MaxCandidatesScan)
		if err != nil {
			return nil, fmt.Errorf("postgres geohash lookup: %w", err)
		}
		partnerIDs = make([]string, 0, len(locs))
		dists = make(map[string]float64, len(locs))
		for _, l := range locs {
			distKM := geo.HaversineKM(ride.PickupLat, ride.PickupLng, l.LastLat, l.LastLng)
			partnerIDs = append(partnerIDs, l.PartnerID.String())
			dists[l.PartnerID.String()] = distKM
		}
	}
	if len(partnerIDs) == 0 {
		return nil, nil
	}
	pids := make([]uuid.UUID, 0, len(partnerIDs))
	for _, idStr := range partnerIDs {
		if u, err := uuid.Parse(idStr); err == nil {
			pids = append(pids, u)
		}
	}
	cands, err := s.store.LoadMatcherCandidates(ctx, pids)
	if err != nil {
		return nil, err
	}
	out := make([]matcher.PartnerCandidate, 0, len(cands))
	for _, c := range cands {
		if d, ok := dists[c.PartnerID]; ok {
			c.DistanceKM = d
		}
		out = append(out, c)
	}
	return out, nil
}

// discoverFromRedis runs GEOSEARCH against rider:online:<city> (or the global
// rider:online key when city is empty). Expanding-radius: 5km -> 10km -> 20km.
func (s *Service) discoverFromRedis(ctx context.Context, cityID string, lat, lng, initialKM, maxKM float64) ([]string, map[string]float64) {
	if s.rdb == nil {
		return nil, nil
	}
	key := redisOnlineKey(cityID)
	radius := initialKM
	if radius <= 0 {
		radius = 5
	}
	for radius <= maxKM {
		q := &redis.GeoSearchLocationQuery{
			GeoSearchQuery: redis.GeoSearchQuery{
				Longitude:  lng,
				Latitude:   lat,
				Radius:     radius,
				RadiusUnit: "km",
				Sort:       "ASC",
				Count:      50,
			},
			WithCoord: true,
			WithDist:  true,
		}
		res, err := s.rdb.GeoSearchLocation(ctx, key, q).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			slog.Warn("rider: redis geosearch failed", "key", key, "radius", radius, "error", err)
			return nil, nil
		}
		if len(res) > 0 {
			ids := make([]string, 0, len(res))
			dists := make(map[string]float64, len(res))
			for _, item := range res {
				ids = append(ids, item.Name)
				dists[item.Name] = item.Dist
			}
			return ids, dists
		}
		radius *= 2
	}
	return nil, nil
}

// redisOnlineKey is the per-city online-partner GEO set key. Falls back to
// "rider:online" when no city is set.
func redisOnlineKey(cityID string) string {
	if cityID == "" {
		return "rider:online"
	}
	return "rider:online:" + cityID
}

// --- Offer accept ---------------------------------------------------------

// AcceptOfferResult is what AcceptOffer returns to the partner.
// Note: Plaintext OTP is NEVER returned to the partner (it is only visible to the rider).
type AcceptOfferResult struct {
	RideID    uuid.UUID `json:"ride_id"`
	PartnerID uuid.UUID `json:"partner_id"`
	Status    string    `json:"status"`
	OTPExpiry time.Time `json:"otp_expires_at"`
}

// AcceptOffer is the race-safe accept path:
//  1. AcceptOfferAndAssignRideTx (in store) takes a row lock on ride, then offer,
//     accepts offer, supersedes siblings, stamps partner_id, vehicle_id, otp_hash, otp_encrypted.
//  2. Transition ride searching_partner -> partner_assigned.
//  3. Emit ride.assigned to Kafka + realtime.
func (s *Service) AcceptOffer(ctx context.Context, partnerUserID, offerID uuid.UUID) (*AcceptOfferResult, error) {
	if partnerUserID == uuid.Nil || offerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: partner user id and offer id required")
	}
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: partner")
		}
		return nil, err
	}
	// Pick the partner's first approved vehicle as the assignment carrier.
	vehicles, err := s.store.ListVehiclesByPartner(ctx, partner.ID)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	var vehicleID *uuid.UUID
	for _, v := range vehicles {
		if v.Status == "approved" && v.IsActive {
			vid := v.ID
			vehicleID = &vid
			break
		}
	}
	if vehicleID == nil {
		return nil, fmt.Errorf("invalid: partner has no approved vehicle")
	}

	_, otpHash, otpEncrypted, err := generateOTPAndHash()
	if err != nil {
		return nil, fmt.Errorf("generate otp: %w", err)
	}

	outboxPayload, _ := json.Marshal(map[string]interface{}{
		"partner_id": partner.ID.String(),
		"vehicle_id": vehicleID.String(),
		"offer_id":   offerID.String(),
	})

	_, ride, err := s.store.AcceptOfferAndAssignRideTx(ctx, store.AcceptOfferAndAssignInput{
		OfferID:         offerID,
		PartnerID:       partner.ID,
		PartnerUserID:   partnerUserID,
		VehicleID:       vehicleID,
		OTPPlain:        otpHash,
		OTPEncrypted:    otpEncrypted,
		OutboxEventType: "rider.ride.assigned",
		OutboxPayload:   outboxPayload,
	})
	if err != nil {
		if errors.Is(err, store.ErrOfferAlreadyDecided) {
			return nil, fmt.Errorf("conflict: offer already decided")
		}
		if errors.Is(err, store.ErrOfferNotFound) {
			return nil, fmt.Errorf("not_found: offer")
		}
		return nil, err
	}

	if _, err := s.store.IncrementSubscriptionLeadsUsed(ctx, partner.ID); err != nil {
		slog.Warn("rider: increment leads_used failed", "partner_id", partner.ID, "error", err)
	}
	if perr := s.producer.PublishRideAssigned(ctx, ride.ID, ride.CustomerUserID, partner.ID, *vehicleID, offerID); perr != nil {
		slog.Warn("rider: publish ride.assigned failed", "ride_id", ride.ID, "error", perr)
	}
	s.emit(ctx, "rider.ride."+ride.ID.String(), "rider.ride.assigned", ride)
	s.publishRealtime(ctx, "rider.admin.live_rides", "rider.ride.assigned", ride)

	otpExp := time.Now().UTC().Add(30 * time.Minute)
	if ride.OTPExpiresAt != nil {
		otpExp = *ride.OTPExpiresAt
	}

	return &AcceptOfferResult{
		RideID:    ride.ID,
		PartnerID: partner.ID,
		Status:    "partner_assigned",
		OTPExpiry: otpExp,
	}, nil
}

// --- Mid-ride status changes ----------------------------------------------

// MarkArriving moves partner_assigned -> partner_arriving.
func (s *Service) MarkArriving(ctx context.Context, partnerUserID, rideID uuid.UUID, expectedRevision int) error {
	if expectedRevision <= 0 {
		return fmt.Errorf("invalid: expected_revision required")
	}
	ride, partner, err := s.loadRideForPartner(ctx, partnerUserID, rideID)
	if err != nil {
		return err
	}
	outboxPayload, _ := json.Marshal(map[string]interface{}{
		"ride_id":    rideID.String(),
		"partner_id": partner.ID.String(),
	})
	updatedRide, err := s.store.TransitionRideAtomic(ctx, store.TransitionRideAtomicInput{
		RideID:           rideID,
		ExpectedRevision: expectedRevision,
		FromStatus:       "partner_assigned",
		ToStatus:         "partner_arriving",
		ActorKind:        "partner",
		ActorUserID:      &partner.UserID,
		OutboxEventType:  "rider.ride.arriving",
		OutboxPayload:    outboxPayload,
		Mutate: func(tx pgx.Tx, r *store.Ride) error {
			const q = `UPDATE rider_rides SET partner_arriving_at = NOW() WHERE id = $1`
			_, err := tx.Exec(ctx, q, rideID)
			return err
		},
	})
	if err != nil {
		if errors.Is(err, store.ErrRevisionConflict) {
			return fmt.Errorf("conflict: revision conflict")
		}
		if errors.Is(err, store.ErrInvalidTransition) {
			return fmt.Errorf("conflict: invalid state transition")
		}
		return err
	}
	if perr := s.producer.PublishRideArriving(ctx, rideID, ride.CustomerUserID, partner.ID); perr != nil {
		slog.Warn("rider: publish ride.arriving failed", "ride_id", rideID, "error", perr)
	}
	s.publishRealtime(ctx, "rider.ride."+rideID.String(), "rider.ride.arriving", updatedRide)
	return nil
}

// MarkArrived moves partner_arriving -> arrived.
func (s *Service) MarkArrived(ctx context.Context, partnerUserID, rideID uuid.UUID, expectedRevision int) error {
	if expectedRevision <= 0 {
		return fmt.Errorf("invalid: expected_revision required")
	}
	ride, partner, err := s.loadRideForPartner(ctx, partnerUserID, rideID)
	if err != nil {
		return err
	}
	outboxPayload, _ := json.Marshal(map[string]interface{}{
		"ride_id":    rideID.String(),
		"partner_id": partner.ID.String(),
	})
	updatedRide, err := s.store.TransitionRideAtomic(ctx, store.TransitionRideAtomicInput{
		RideID:           rideID,
		ExpectedRevision: expectedRevision,
		FromStatus:       "partner_arriving",
		ToStatus:         "arrived",
		ActorKind:        "partner",
		ActorUserID:      &partner.UserID,
		OutboxEventType:  "rider.ride.arrived",
		OutboxPayload:    outboxPayload,
		Mutate: func(tx pgx.Tx, r *store.Ride) error {
			const q = `UPDATE rider_rides SET arrived_at = NOW() WHERE id = $1`
			_, err := tx.Exec(ctx, q, rideID)
			return err
		},
	})
	if err != nil {
		if errors.Is(err, store.ErrRevisionConflict) {
			return fmt.Errorf("conflict: revision conflict")
		}
		if errors.Is(err, store.ErrInvalidTransition) {
			return fmt.Errorf("conflict: invalid state transition")
		}
		return err
	}
	if perr := s.producer.PublishRideArrived(ctx, rideID, ride.CustomerUserID, partner.ID); perr != nil {
		slog.Warn("rider: publish ride.arrived failed", "ride_id", rideID, "error", perr)
	}
	s.publishRealtime(ctx, "rider.ride."+rideID.String(), "rider.ride.arrived", updatedRide)
	return nil
}

// StartRide verifies the OTP + transitions arrived -> otp_verified -> in_progress with attempt tracking.
func (s *Service) StartRide(ctx context.Context, partnerUserID, rideID uuid.UUID, otpPlain string, expectedRevision int) error {
	if expectedRevision <= 0 {
		return fmt.Errorf("invalid: expected_revision required")
	}
	if strings.TrimSpace(otpPlain) == "" {
		return fmt.Errorf("invalid: otp required")
	}
	_, partner, err := s.loadRideForPartner(ctx, partnerUserID, rideID)
	if err != nil {
		return err
	}

	tx, err := s.store.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const lockQ = `
        SELECT id, customer_user_id, status, revision, otp_code, otp_expires_at, otp_attempts, otp_locked_until
        FROM rider_rides
        WHERE id = $1 FOR UPDATE`
	var rID, custID uuid.UUID
	var status string
	var rev, attempts int
	var otpCode *string
	var otpExpiresAt, lockedUntil *time.Time
	if err := tx.QueryRow(ctx, lockQ, rideID).Scan(&rID, &custID, &status, &rev, &otpCode, &otpExpiresAt, &attempts, &lockedUntil); err != nil {
		return fmt.Errorf("lock ride for start: %w", err)
	}

	if rev != expectedRevision {
		return fmt.Errorf("conflict: revision conflict")
	}
	if status != "arrived" {
		return fmt.Errorf("conflict: ride must be in arrived status to start (current: %s)", status)
	}
	if lockedUntil != nil && lockedUntil.After(time.Now().UTC()) {
		return fmt.Errorf("forbidden: otp verification temporarily locked for 15 minutes due to excessive failed attempts")
	}
	if attempts >= 3 {
		return fmt.Errorf("forbidden: max otp attempts exceeded")
	}
	if otpCode == nil || *otpCode == "" {
		return fmt.Errorf("invalid: ride has no OTP set")
	}
	if otpExpiresAt != nil && otpExpiresAt.Before(time.Now().UTC()) {
		return fmt.Errorf("forbidden: otp expired")
	}

	if err := otp.CompareHashAndPassword([]byte(*otpCode), []byte(strings.TrimSpace(otpPlain))); err != nil {
		newAttempts := attempts + 1
		var lockTime *time.Time
		if newAttempts >= 3 {
			t := time.Now().UTC().Add(15 * time.Minute)
			lockTime = &t
		}
		const updateLockQ = `UPDATE rider_rides SET otp_attempts = $2, otp_locked_until = $3, updated_at = NOW() WHERE id = $1`
		if _, execErr := tx.Exec(ctx, updateLockQ, rideID, newAttempts, lockTime); execErr != nil {
			return fmt.Errorf("record failed attempt: %w", execErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("commit failed attempt: %w", commitErr)
		}
		if newAttempts >= 3 {
			return fmt.Errorf("forbidden: invalid otp; 3 failed attempts, verification locked for 15 minutes")
		}
		return fmt.Errorf("forbidden: otp mismatch (attempt %d/3)", newAttempts)
	}

	// OTP Verified — transition to in_progress and wipe encrypted OTP in the same transaction
	const updateRideQ = `
        UPDATE rider_rides
        SET status = 'in_progress', revision = revision + 1, started_at = NOW(),
            otp_encrypted = NULL, otp_attempts = 0, otp_locked_until = NULL, updated_at = NOW()
        WHERE id = $1 AND revision = $2`
	if _, err := tx.Exec(ctx, updateRideQ, rideID, rev); err != nil {
		return fmt.Errorf("start ride transition: %w", err)
	}

	const histQ = `
        INSERT INTO rider_ride_status_history (ride_id, from_status, to_status, actor_kind, actor_user_id, reason)
        VALUES ($1, 'arrived', 'in_progress', 'partner', $2, 'OTP verified and ride started')`
	if _, err := tx.Exec(ctx, histQ, rideID, partner.UserID); err != nil {
		return fmt.Errorf("insert start history: %w", err)
	}

	outboxPayload, _ := json.Marshal(map[string]interface{}{
		"ride_id":    rideID.String(),
		"partner_id": partner.ID.String(),
	})
	if err := store.InsertOutboxEventTx(ctx, tx, "rider.ride.started", "ride", rideID.String(), outboxPayload); err != nil {
		return fmt.Errorf("insert start outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ride start: %w", err)
	}

	if perr := s.producer.PublishRideStarted(ctx, rideID, custID, partner.ID); perr != nil {
		slog.Warn("rider: publish ride.started failed", "ride_id", rideID, "error", perr)
	}
	s.publishRealtime(ctx, "rider.ride."+rideID.String(), "rider.ride.started", map[string]interface{}{"id": rideID, "status": "in_progress"})
	return nil
}

// ConfirmCashPayment records that the assigned captain has collected cash.
func (s *Service) ConfirmCashPayment(ctx context.Context, partnerUserID, rideID uuid.UUID, expectedRevision int) error {
	if expectedRevision <= 0 {
		return fmt.Errorf("invalid: expected_revision required")
	}
	ride, partner, err := s.loadRideForPartner(ctx, partnerUserID, rideID)
	if err != nil {
		return err
	}
	if ride.Status != "completed" {
		return fmt.Errorf("conflict: cash payment can only be confirmed after ride completion")
	}

	outboxPayload, _ := json.Marshal(map[string]interface{}{
		"ride_id":    rideID.String(),
		"partner_id": partner.ID.String(),
		"amount":     ride.FinalFarePaise,
	})

	tx, err := s.store.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const lockQ = `SELECT revision FROM rider_rides WHERE id = $1 FOR UPDATE`
	var rev int
	if err := tx.QueryRow(ctx, lockQ, rideID).Scan(&rev); err != nil {
		return fmt.Errorf("lock ride for payment: %w", err)
	}
	if rev != expectedRevision {
		return fmt.Errorf("conflict: revision conflict")
	}

	const updatePayQ = `
        UPDATE rider_ride_payments
        SET status = 'succeeded', settled_at = NOW()
        WHERE ride_id = $1 AND payment_method = 'cash' AND status = 'pending_cash_confirmation'`
	tag, err := tx.Exec(ctx, updatePayQ, rideID)
	if err != nil {
		return fmt.Errorf("update payment status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("conflict: no pending cash payment confirmation row for this ride")
	}

	const updateRideQ = `
        UPDATE rider_rides
        SET cash_confirmed_at = NOW(), cash_confirmed_by = $2, revision = revision + 1, updated_at = NOW()
        WHERE id = $1 AND revision = $3`
	if _, err := tx.Exec(ctx, updateRideQ, rideID, partner.UserID, rev); err != nil {
		return fmt.Errorf("confirm cash in ride: %w", err)
	}

	const histQ = `
        INSERT INTO rider_ride_status_history (ride_id, from_status, to_status, actor_kind, actor_user_id, reason)
        VALUES ($1, 'completed', 'completed', 'partner', $2, 'cash collected and confirmed')`
	if _, err := tx.Exec(ctx, histQ, rideID, partner.UserID); err != nil {
		return fmt.Errorf("insert cash history: %w", err)
	}

	if err := store.InsertOutboxEventTx(ctx, tx, "rider.ride.payment.reconciled", "ride", rideID.String(), outboxPayload); err != nil {
		return fmt.Errorf("insert cash outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cash confirmation: %w", err)
	}
	return nil
}

// CompleteRideRequest is the partner-supplied final telemetry.
type CompleteRideRequest struct {
	FinalDistanceKM  float64
	FinalDurationMin int
	IdempotencyKey   string
	ExpectedRevision int
}

// CompleteRide finalizes a ride: compute final fare from rule, flag for
// review if >1.5× estimate, insert ride_payments atomically, and transition to completed.
func (s *Service) CompleteRide(ctx context.Context, partnerUserID, rideID uuid.UUID, req CompleteRideRequest) (*store.RidePayment, error) {
	if req.ExpectedRevision <= 0 {
		return nil, fmt.Errorf("invalid: expected_revision required")
	}
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}
	if req.FinalDistanceKM < 0 || req.FinalDurationMin < 0 {
		return nil, fmt.Errorf("invalid: final telemetry must be non-negative")
	}
	reqFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%.3f:%d", partnerUserID, rideID, req.FinalDistanceKM, req.FinalDurationMin))))
	if existing, err := s.store.FindIdempotency(ctx, req.IdempotencyKey, partnerUserID, "ride_complete", reqFingerprint); err == nil {
		if existing.ResourceID != nil {
			return s.store.GetRidePayment(ctx, *existing.ResourceID)
		}
	} else if !errors.Is(err, store.ErrIdempotencyKeyNotFound) {
		return nil, err
	}
	ride, partner, err := s.loadRideForPartner(ctx, partnerUserID, rideID)
	if err != nil {
		return nil, err
	}
	if ride.CityID == nil {
		return nil, fmt.Errorf("invalid: ride has no city for fare lookup")
	}
	rule, err := s.store.GetFareRule(ctx, *ride.CityID, ride.VehicleType)
	if err != nil {
		return nil, fmt.Errorf("fare rule lookup: %w", err)
	}

	basePaise := int64(math.Round(rule.BaseFare * 100))
	perKMPaise := int64(math.Round(rule.PerKMFare * 100))
	perMinPaise := int64(math.Round(rule.PerMinuteFare * 100))
	platformPaise := int64(math.Round(rule.PlatformFee * 100))
	minPaise := int64(math.Round(rule.MinimumFare * 100))

	distPaise := (int64(math.Round(req.FinalDistanceKM * 1000)) * perKMPaise) / 1000
	timePaise := (int64(req.FinalDurationMin) * 60 * perMinPaise) / 60
	rawPaise := basePaise + distPaise + timePaise + platformPaise
	if rawPaise < minPaise {
		rawPaise = minPaise
	}

	mult := math.Max(rule.NightMultiplier, rule.PeakMultiplier)
	var surgeBPS int64 = 0
	if mult > 1.0 {
		surgeBPS = int64(math.Round((mult - 1.0) * 10000))
	}
	surgePaise := (rawPaise * surgeBPS) / 10000
	totalPaise := rawPaise + surgePaise
	taxPaise := (totalPaise * 500) / 10000
	finalPaise := totalPaise + taxPaise
	rawINR := float64(finalPaise) / 100.0

	flag := false
	if ride.EstimatedFare != nil && *ride.EstimatedFare > 0 {
		if rawINR > 1.5*(*ride.EstimatedFare) {
			flag = true
		}
	}

	method := "cash"
	if ride.PaymentMethod != nil && *ride.PaymentMethod != "" {
		method = *ride.PaymentMethod
	}
	initialPayStatus := "pending"
	if method == "cash" {
		initialPayStatus = "pending_cash_confirmation"
	}

	payID := uuid.New()
	outboxPayload, _ := json.Marshal(map[string]interface{}{
		"ride_id":    rideID.String(),
		"partner_id": partner.ID.String(),
		"final_fare": finalPaise,
		"payment_id": payID.String(),
	})

	_, err = s.store.TransitionRideAtomic(ctx, store.TransitionRideAtomicInput{
		RideID:           rideID,
		ExpectedRevision: req.ExpectedRevision,
		FromStatus:       "in_progress",
		ToStatus:         "completed",
		ActorKind:        "partner",
		ActorUserID:      &partner.UserID,
		OutboxEventType:  "rider.ride.completed",
		OutboxPayload:    outboxPayload,
		Mutate: func(tx pgx.Tx, r *store.Ride) error {
			const q = `
                UPDATE rider_rides
                SET final_distance_km = $2, final_duration_min = $3, final_fare = $4,
                    final_fare_paise = $5, flagged_for_review = $6, completed_at = NOW()
                WHERE id = $1`
			if _, err := tx.Exec(ctx, q, rideID, req.FinalDistanceKM, req.FinalDurationMin, rawINR, finalPaise, flag); err != nil {
				return err
			}

			const payQ = `
                INSERT INTO rider_ride_payments (id, ride_id, partner_id, amount_paise, payment_method, status)
                VALUES ($1, $2, $3, $4, $5, $6)`
			if _, err := tx.Exec(ctx, payQ, payID, rideID, partner.ID, finalPaise, method, initialPayStatus); err != nil {
				return err
			}

			const idempQ = `
                INSERT INTO rider_idempotency (idempotency_key, user_id, operation, request_hash, resource_id, response_status, expires_at)
                VALUES ($1, $2, 'ride_complete', $3, $4, 200, NOW() + INTERVAL '24 hours')
                ON CONFLICT (idempotency_key, user_id, operation) DO UPDATE SET resource_id = $4`
			if _, err := tx.Exec(ctx, idempQ, req.IdempotencyKey, partnerUserID, reqFingerprint, payID); err != nil {
				return err
			}
			return nil
		},
	})
	if err != nil {
		if errors.Is(err, store.ErrRevisionConflict) {
			return nil, fmt.Errorf("conflict: revision conflict")
		}
		if errors.Is(err, store.ErrInvalidTransition) {
			return nil, fmt.Errorf("conflict: invalid state transition")
		}
		return nil, err
	}

	pay := &store.RidePayment{
		ID:            payID,
		RideID:        rideID,
		PartnerID:     partner.ID,
		AmountPaise:   finalPaise,
		PaymentMethod: method,
		Status:        initialPayStatus,
		CreatedAt:     time.Now().UTC(),
	}

	if method == "wallet" && s.wallet != nil && finalPaise > 0 {
		debit, derr := s.wallet.DebitForSubscription(ctx, ride.CustomerUserID, finalPaise, pay.ID, "ride-complete-"+pay.ID.String())
		if derr != nil {
			slog.Warn("rider: wallet debit for ride failed", "payment_id", pay.ID, "error", derr)
			_ = s.store.MarkRidePaymentFailed(ctx, pay.ID)
		} else {
			if updatedPay, err := s.store.MarkRidePaymentSucceeded(ctx, pay.ID, &debit.TransactionID, nil); err == nil {
				pay = updatedPay
			}
		}
	}

	if err := s.store.IncrementPartnerCompleted(ctx, partner.ID); err != nil {
		slog.Warn("rider: increment partner completed failed", "partner_id", partner.ID, "error", err)
	}

	if perr := s.producer.PublishRideCompleted(ctx, events.RideCompletedPayload{
		RideID:           rideID.String(),
		PartnerID:        partner.ID.String(),
		FinalDistanceKM:  req.FinalDistanceKM,
		FinalDurationMin: req.FinalDurationMin,
		FinalFarePaise:   finalPaise,
		PaymentMethod:    method,
		PaymentStatus:    pay.Status,
		FlaggedForReview: flag,
		CompletedAt:      time.Now().UTC(),
	}); perr != nil {
		slog.Warn("rider: publish ride.completed failed", "ride_id", rideID, "error", perr)
	}

	return pay, nil
}

// --- Cancellation ---------------------------------------------------------

// CancelRideRequest is the customer- or partner-supplied cancel input.
type CancelRideRequest struct {
	Reason           string
	IdempotencyKey   string
	ExpectedRevision int
}

// CancelRide computes the per-state cancellation fee, marks the ride
// cancelled, debits the wallet (when a fee applies and a customer cancels),
// and updates partner cancellation rate when a partner cancels.
//
// Fee schedule (paise):
//   - before partner_assigned   ->  0
//   - before arrived            ->  ₹15 (1500p)
//   - after arrived, before in_progress -> ₹50 (5000p)
//   - during in_progress        ->  prorated (10% of estimated fare)
func (s *Service) CancelRide(ctx context.Context, actorUserID, rideID uuid.UUID, by string, req CancelRideRequest) (*store.Ride, error) {
	if by != "customer" && by != "partner" && by != "admin" && by != "system" {
		return nil, fmt.Errorf("invalid: by must be customer | partner | admin | system")
	}
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}

	expRev := req.ExpectedRevision
	if by != "system" && expRev <= 0 {
		return nil, fmt.Errorf("invalid: expected_revision required")
	}
	if expRev <= 0 {
		expRev = ride.Revision
	}

	// Authorization: customer must own the ride; partner must be assigned.
	switch by {
	case "customer":
		if ride.CustomerUserID != actorUserID {
			return nil, fmt.Errorf("forbidden: ride does not belong to user")
		}
	case "partner":
		if ride.PartnerID == nil {
			return nil, fmt.Errorf("forbidden: ride has no partner assigned")
		}
		partner, err := s.store.GetPartnerByUserID(ctx, actorUserID)
		if err != nil {
			return nil, fmt.Errorf("not_found: partner")
		}
		if *ride.PartnerID != partner.ID {
			return nil, fmt.Errorf("forbidden: ride not assigned to this partner")
		}
	}
	feePaise := computeCancellationFeePaise(ride)
	to := "cancelled_by_" + by
	if by == "system" {
		to = "expired"
	}
	reason := req.Reason
	r := &reason
	if reason == "" {
		r = nil
	}
	var actorRef *uuid.UUID
	if actorUserID != uuid.Nil {
		actorRef = &actorUserID
	}

	outboxPayload, _ := json.Marshal(map[string]interface{}{
		"ride_id":   rideID.String(),
		"reason":    reason,
		"by":        by,
		"fee_paise": feePaise,
	})

	updatedRide, err := s.store.TransitionRideAtomic(ctx, store.TransitionRideAtomicInput{
		RideID:           rideID,
		ExpectedRevision: expRev,
		FromStatus:       ride.Status,
		ToStatus:         to,
		ActorKind:        by,
		ActorUserID:      actorRef,
		Reason:           r,
		OutboxEventType:  "rider.ride.cancelled",
		OutboxPayload:    outboxPayload,
		Mutate: func(tx pgx.Tx, rd *store.Ride) error {
			const cancelQ = `
                UPDATE rider_rides
                SET cancellation_fee_paise = $2, cancelled_by_kind = $3, cancelled_by_user_id = $4,
                    cancellation_reason = $5, cancelled_at = NOW()
                WHERE id = $1`
			_, err := tx.Exec(ctx, cancelQ, rideID, feePaise, by, actorRef, reason)
			return err
		},
	})
	if err != nil {
		if errors.Is(err, store.ErrRevisionConflict) {
			return nil, fmt.Errorf("conflict: revision conflict")
		}
		if errors.Is(err, store.ErrInvalidTransition) {
			return nil, fmt.Errorf("conflict: invalid state transition")
		}
		return nil, err
	}
	if by == "partner" && ride.PartnerID != nil {
		if err := s.store.IncrementPartnerCancelled(ctx, *ride.PartnerID); err != nil {
			slog.Warn("rider: increment partner cancelled failed", "partner_id", *ride.PartnerID, "error", err)
		}
	}
	if by == "customer" && feePaise > 0 && s.wallet != nil && ride.PartnerID != nil {
		key := req.IdempotencyKey
		if key == "" {
			key = "ride-cancel-" + rideID.String()
		}
		if _, derr := s.wallet.DebitForSubscription(ctx, actorUserID, feePaise, rideID, key); derr != nil {
			slog.Warn("rider: cancellation fee wallet debit failed", "ride_id", rideID, "error", derr)
		}
	}
	cancelledBy := ""
	if actorRef != nil {
		cancelledBy = actorRef.String()
	}
	if perr := s.producer.PublishRideCancelled(ctx, events.RideCancelledPayload{
		RideID:               rideID.String(),
		CancelledByKind:      by,
		CancelledByUserID:    cancelledBy,
		Reason:               reason,
		CancellationFeePaise: feePaise,
		CancelledAt:          time.Now().UTC(),
	}); perr != nil {
		slog.Warn("rider: publish ride.cancelled failed", "ride_id", rideID, "error", perr)
	}
	return updatedRide, nil
}

// computeCancellationFeePaise applies the fee schedule per the spec.
//
// Exposed via test hook below. The float64 return path is internal —
// callers persist the int64 paise.
func computeCancellationFeePaise(r *store.Ride) int64 {
	switch r.Status {
	case "requested", "searching_partner", "partner_assigned":
		return 0
	case "partner_arriving":
		return 1500
	case "arrived":
		return 5000
	case "otp_verified", "in_progress":
		// Prorate at 10% of the estimated fare, capped at ₹100.
		if r.EstimatedFare == nil {
			return 5000
		}
		fee := *r.EstimatedFare * 0.10
		paise := int64(math.Round(fee * 100))
		if paise > 10000 {
			paise = 10000
		}
		return paise
	default:
		return 0
	}
}

// --- Rating ---------------------------------------------------------------

// RateRideRequest is the customer-side rating input.
type RateRideRequest struct {
	Rating  int16
	Comment string
}

// RateRide stores the rating + comment and triggers a partner-rating recompute.
func (s *Service) RateRide(ctx context.Context, customerID, rideID uuid.UUID, req RateRideRequest) error {
	if req.Rating < 1 || req.Rating > 5 {
		return fmt.Errorf("invalid: rating must be 1..5")
	}
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return fmt.Errorf("not_found: ride")
		}
		return err
	}
	if ride.CustomerUserID != customerID {
		return fmt.Errorf("forbidden: ride does not belong to user")
	}
	if ride.Status != "completed" {
		return fmt.Errorf("invalid: only completed rides can be rated")
	}
	var comment *string
	if c := strings.TrimSpace(req.Comment); c != "" {
		comment = &c
	}
	if err := s.store.SetRating(ctx, rideID, req.Rating, comment); err != nil {
		return fmt.Errorf("set rating: %w", err)
	}
	if ride.PartnerID != nil {
		if err := s.store.UpdatePartnerRating(ctx, *ride.PartnerID); err != nil {
			slog.Warn("rider: update partner rating failed", "partner_id", *ride.PartnerID, "error", err)
		}
		if perr := s.producer.PublishRideRated(ctx, rideID, *ride.PartnerID, req.Rating); perr != nil {
			slog.Warn("rider: publish ride.rated failed", "ride_id", rideID, "error", perr)
		}
	}
	return nil
}

// --- Share ----------------------------------------------------------------

// ShareRideResult is the response from ShareRide.
type ShareRideResult struct {
	Token string `json:"token"`
	URL   string `json:"url"`
}

// ShareRide generates a one-time share token for the ride. Idempotent: a
// second call returns the same token.
func (s *Service) ShareRide(ctx context.Context, customerID, rideID uuid.UUID, baseURL string) (*ShareRideResult, error) {
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	if ride.CustomerUserID != customerID {
		return nil, fmt.Errorf("forbidden: ride does not belong to user")
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	tok := hexEncode(tokenBytes)
	stored, err := s.store.SetShareToken(ctx, rideID, tok)
	if err != nil {
		return nil, fmt.Errorf("set share token: %w", err)
	}
	url := stored
	if baseURL != "" {
		url = strings.TrimRight(baseURL, "/") + "/share/" + stored
	}
	return &ShareRideResult{Token: stored, URL: url}, nil
}

// --- helpers --------------------------------------------------------------

// loadRideForPartner fetches the ride + verifies the partner owns it.
func (s *Service) loadRideForPartner(ctx context.Context, partnerUserID, rideID uuid.UUID) (*store.Ride, *store.Partner, error) {
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, nil, fmt.Errorf("not_found: ride")
		}
		return nil, nil, err
	}
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, nil, fmt.Errorf("not_found: partner")
		}
		return nil, nil, err
	}
	if ride.PartnerID == nil || *ride.PartnerID != partner.ID {
		return nil, nil, fmt.Errorf("forbidden: ride not assigned to this partner")
	}
	return ride, partner, nil
}

// generateOTPAndHash returns a 4-digit OTP + its hash + its encrypted envelope.
func generateOTPAndHash() (plain string, hash string, encrypted []byte, err error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", nil, err
	}
	n := binary.BigEndian.Uint32(buf[:]) % 10000
	plain = strconv.FormatUint(uint64(n), 10)
	for len(plain) < 4 {
		plain = "0" + plain
	}
	h, err := otp.GenerateFromPassword([]byte(plain), 0)
	if err != nil {
		return "", "", nil, err
	}
	enc, err := otp.EncryptOTP(plain, nil)
	if err != nil {
		return "", "", nil, err
	}
	return plain, string(h), enc, nil
}

// hexEncode is a tiny hex encoder so share tokens are URL-safe and the
// service avoids importing "encoding/hex" only to reach a 32-char string.
func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[i*2] = digits[x>>4]
		out[i*2+1] = digits[x&0x0F]
	}
	return string(out)
}
