package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
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

// AcceptOfferResult is what AcceptOffer returns to the partner. Includes the
// plain-text OTP — the only call that ever exposes it. Subsequent fetches
// only see the bcrypt hash on the row.
type AcceptOfferResult struct {
	RideID    uuid.UUID `json:"ride_id"`
	PartnerID uuid.UUID `json:"partner_id"`
	OTP       string    `json:"otp"`
	OTPExpiry time.Time `json:"otp_expires_at"`
}

// AcceptOffer is the race-safe accept path:
//  1. AcceptOfferTx (in store) takes a row lock + supersedes siblings.
//  2. Generate 4-digit OTP, bcrypt hash it, expiry +30min.
//  3. AssignRidePartner stamps partner_id, vehicle_id, otp_hash on the ride.
//  4. Transition ride searching_partner -> partner_assigned.
//  5. Increment lead_usage on the partner's active subscription.
//  6. Emit ride.assigned + return plaintext OTP in the response.
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
	updated, err := s.store.AcceptOfferTx(ctx, offerID, partner.ID)
	if err != nil {
		if errors.Is(err, store.ErrOfferAlreadyDecided) {
			return nil, fmt.Errorf("conflict: offer already decided")
		}
		if errors.Is(err, store.ErrOfferNotFound) {
			return nil, fmt.Errorf("not_found: offer")
		}
		return nil, err
	}
	// Pick the partner's first approved vehicle as the assignment carrier.
	vehicles, err := s.store.ListVehiclesByPartner(ctx, partner.ID)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	var vehicleID uuid.UUID
	for _, v := range vehicles {
		if v.Status == "approved" && v.IsActive {
			vehicleID = v.ID
			break
		}
	}
	if vehicleID == uuid.Nil {
		return nil, fmt.Errorf("invalid: partner has no approved vehicle")
	}
	otpPlain, otpHash, err := generateOTPAndHash()
	if err != nil {
		return nil, fmt.Errorf("generate otp: %w", err)
	}
	otpExpiry := time.Now().UTC().Add(30 * time.Minute)
	if err := s.store.AssignRidePartner(ctx, updated.RideID, partner.ID, vehicleID, otpHash, otpExpiry); err != nil {
		return nil, fmt.Errorf("assign partner: %w", err)
	}
	ride, err := s.store.GetRide(ctx, updated.RideID)
	if err != nil {
		return nil, err
	}
	if err := s.transitionRide(ctx, ride, "partner_assigned", "partner", &partner.UserID, nil); err != nil {
		return nil, err
	}
	if _, err := s.store.IncrementSubscriptionLeadsUsed(ctx, partner.ID); err != nil {
		// Log but don't fail — lead accounting is best-effort vs blocking the ride.
		slog.Warn("rider: increment leads_used failed", "partner_id", partner.ID, "error", err)
	}
	if perr := s.producer.PublishRideAssigned(ctx, ride.ID, partner.ID, vehicleID, offerID); perr != nil {
		slog.Warn("rider: publish ride.assigned failed", "ride_id", ride.ID, "error", perr)
	}
	s.emit(ctx, "rider.ride."+ride.ID.String(), "rider.ride.assigned", ride)
	s.publishRealtime(ctx, "rider.admin.live_rides", "rider.ride.assigned", ride)
	return &AcceptOfferResult{
		RideID:    ride.ID,
		PartnerID: partner.ID,
		OTP:       otpPlain,
		OTPExpiry: otpExpiry,
	}, nil
}

// --- Mid-ride status changes ----------------------------------------------

// MarkArriving moves partner_assigned -> partner_arriving.
func (s *Service) MarkArriving(ctx context.Context, partnerUserID, rideID uuid.UUID) error {
	ride, partner, err := s.loadRideForPartner(ctx, partnerUserID, rideID)
	if err != nil {
		return err
	}
	if err := s.store.SetArrivingAt(ctx, rideID); err != nil {
		return fmt.Errorf("set arriving: %w", err)
	}
	if err := s.transitionRide(ctx, ride, "partner_arriving", "partner", &partner.UserID, nil); err != nil {
		return err
	}
	if perr := s.producer.PublishRideArriving(ctx, rideID, partner.ID); perr != nil {
		slog.Warn("rider: publish ride.arriving failed", "ride_id", rideID, "error", perr)
	}
	return nil
}

// MarkArrived moves partner_arriving -> arrived.
func (s *Service) MarkArrived(ctx context.Context, partnerUserID, rideID uuid.UUID) error {
	ride, partner, err := s.loadRideForPartner(ctx, partnerUserID, rideID)
	if err != nil {
		return err
	}
	if err := s.store.SetArrivedAt(ctx, rideID); err != nil {
		return fmt.Errorf("set arrived: %w", err)
	}
	if err := s.transitionRide(ctx, ride, "arrived", "partner", &partner.UserID, nil); err != nil {
		return err
	}
	if perr := s.producer.PublishRideArrived(ctx, rideID, partner.ID); perr != nil {
		slog.Warn("rider: publish ride.arrived failed", "ride_id", rideID, "error", perr)
	}
	return nil
}

// StartRide verifies the OTP + transitions arrived -> otp_verified -> in_progress.
func (s *Service) StartRide(ctx context.Context, partnerUserID, rideID uuid.UUID, otpPlain string) error {
	if strings.TrimSpace(otpPlain) == "" {
		return fmt.Errorf("invalid: otp required")
	}
	ride, partner, err := s.loadRideForPartner(ctx, partnerUserID, rideID)
	if err != nil {
		return err
	}
	_, otpHash, otpExpiry, err := s.store.GetRideWithOTP(ctx, rideID)
	if err != nil {
		return err
	}
	if otpHash == nil || *otpHash == "" {
		return fmt.Errorf("invalid: ride has no OTP set")
	}
	if otpExpiry != nil && otpExpiry.Before(time.Now().UTC()) {
		return fmt.Errorf("forbidden: otp expired")
	}
	if err := otp.CompareHashAndPassword([]byte(*otpHash), []byte(strings.TrimSpace(otpPlain))); err != nil {
		if errors.Is(err, otp.ErrMismatchedHashAndPassword) {
			return fmt.Errorf("forbidden: otp mismatch")
		}
		return fmt.Errorf("verify otp: %w", err)
	}
	// arrived -> otp_verified -> in_progress (two transitions, one history row each).
	if err := s.transitionRide(ctx, ride, "otp_verified", "partner", &partner.UserID, nil); err != nil {
		return err
	}
	if err := s.store.SetStartedAt(ctx, rideID); err != nil {
		return fmt.Errorf("set started: %w", err)
	}
	if err := s.transitionRide(ctx, ride, "in_progress", "partner", &partner.UserID, nil); err != nil {
		return err
	}
	if perr := s.producer.PublishRideStarted(ctx, rideID, partner.ID); perr != nil {
		slog.Warn("rider: publish ride.started failed", "ride_id", rideID, "error", perr)
	}
	return nil
}

// CompleteRideRequest is the partner-supplied final telemetry.
type CompleteRideRequest struct {
	FinalDistanceKM  float64
	FinalDurationMin int
	IdempotencyKey   string
}

// CompleteRide finalizes a ride: compute final fare from rule, flag for
// review if >1.5× estimate, insert ride_payments, settle cash immediately,
// debit wallet for wallet method, return UPI intent for upi method.
//
// Idempotent on idempotencyKey via rider_idempotency.
func (s *Service) CompleteRide(ctx context.Context, partnerUserID, rideID uuid.UUID, req CompleteRideRequest) (*store.RidePayment, error) {
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}
	if req.FinalDistanceKM < 0 || req.FinalDurationMin < 0 {
		return nil, fmt.Errorf("invalid: final telemetry must be non-negative")
	}
	if existing, err := s.store.FindIdempotency(ctx, req.IdempotencyKey, partnerUserID, "ride_complete"); err == nil {
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
	rawINR := rule.BaseFare + rule.PerKMFare*req.FinalDistanceKM + rule.PerMinuteFare*float64(req.FinalDurationMin)
	if rawINR < rule.MinimumFare {
		rawINR = rule.MinimumFare
	}
	// Surge handled at the fare-rule level (peak/night multipliers). Apply max
	// of the two so fares are never under-quoted at peak.
	mult := math.Max(rule.NightMultiplier, rule.PeakMultiplier)
	if mult <= 0 {
		mult = 1.0
	}
	rawINR *= mult
	finalPaise := int64(math.Round(rawINR * 100))
	flag := false
	if ride.EstimatedFare != nil && *ride.EstimatedFare > 0 {
		if rawINR > 1.5*(*ride.EstimatedFare) {
			flag = true
		}
	}
	if err := s.store.FinalizeRide(ctx, store.CompleteRideInput{
		RideID:           rideID,
		FinalDistanceKM:  req.FinalDistanceKM,
		FinalDurationMin: req.FinalDurationMin,
		FinalFareINR:     rawINR,
		FinalFarePaise:   finalPaise,
		FlaggedForReview: flag,
	}); err != nil {
		return nil, fmt.Errorf("finalize ride: %w", err)
	}
	if err := s.transitionRide(ctx, ride, "completed", "partner", &partner.UserID, nil); err != nil {
		return nil, err
	}
	method := "cash"
	if ride.PaymentMethod != nil && *ride.PaymentMethod != "" {
		method = *ride.PaymentMethod
	}
	pay, err := s.store.CreateRidePayment(ctx, store.CreateRidePaymentInput{
		RideID:        rideID,
		PartnerID:     partner.ID,
		AmountPaise:   finalPaise,
		PaymentMethod: method,
		Status:        "pending",
	})
	if err != nil {
		return nil, fmt.Errorf("create payment: %w", err)
	}
	switch method {
	case "cash":
		// Cash settles informally — partner collects on the spot.
		if pay, err = s.store.MarkRidePaymentSucceeded(ctx, pay.ID, nil, nil); err != nil {
			return nil, err
		}
	case "wallet":
		if s.wallet == nil {
			slog.Warn("rider: wallet client not configured; payment stays pending", "payment_id", pay.ID)
		} else if finalPaise > 0 {
			debit, derr := s.wallet.DebitForSubscription(ctx, ride.CustomerUserID, finalPaise, pay.ID, "ride-complete-"+pay.ID.String())
			if derr != nil {
				slog.Warn("rider: wallet debit for ride failed", "payment_id", pay.ID, "error", derr)
				_ = s.store.MarkRidePaymentFailed(ctx, pay.ID)
			} else {
				if pay, err = s.store.MarkRidePaymentSucceeded(ctx, pay.ID, &debit.TransactionID, nil); err != nil {
					return nil, err
				}
			}
		}
	case "upi":
		// upi stays pending until customer confirms the txn ref out-of-band.
	default:
		// Unknown method — leave pending; admin reconciles.
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
	_ = s.store.RecordIdempotency(ctx, req.IdempotencyKey, partnerUserID, "ride_complete", &pay.ID, nil)
	return pay, nil
}

// --- Cancellation ---------------------------------------------------------

// CancelRideRequest is the customer- or partner-supplied cancel input.
type CancelRideRequest struct {
	Reason         string
	IdempotencyKey string
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
	if err := s.transitionRide(ctx, ride, to, by, actorRef, r); err != nil {
		return nil, err
	}
	if err := s.store.MarkRideCancelled(ctx, store.CancelRideInput{
		RideID:               rideID,
		CancellationFeePaise: feePaise,
		Reason:               reason,
		CancelledByKind:      by,
	}); err != nil {
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
	return ride, nil
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

// generateOTPAndHash returns a 4-digit OTP + its bcrypt-style hash. The
// plaintext is returned exactly once (to AcceptOffer) and never stored.
func generateOTPAndHash() (plain string, hash string, err error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", err
	}
	n := binary.BigEndian.Uint32(buf[:]) % 10000
	plain = strconv.FormatUint(uint64(n), 10)
	for len(plain) < 4 {
		plain = "0" + plain
	}
	h, err := otp.GenerateFromPassword([]byte(plain), 0)
	if err != nil {
		return "", "", err
	}
	return plain, string(h), nil
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
