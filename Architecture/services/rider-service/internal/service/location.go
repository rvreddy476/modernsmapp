package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/rider-service/internal/geo"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// onlineKeyTTL is how long a partner's GEO entry survives without a refresh
// from a location update. The mobile app pings every ~5–15s during active
// duty; 60s here gives plenty of headroom against transient packet loss.
const onlineKeyTTL = 60 * time.Second

// GoOnline flips the partner's online flag after running the eligibility
// gate (KYC + vehicle + active subscription). Idempotent on already-online.
func (s *Service) GoOnline(ctx context.Context, partnerUserID uuid.UUID) error {
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	if partner.Status != "approved" {
		return fmt.Errorf("forbidden: partner not approved")
	}
	if partner.KYCStatus != "approved" {
		return fmt.Errorf("forbidden: kyc not approved")
	}
	// P0.6 — block go-online when any APPROVED KYC document has lapsed.
	// The partner must re-upload (or admin must re-verify) the expired
	// item before they can resume taking rides.
	expiredPartnerDocs, err := s.store.CountExpiredApprovedPartnerDocs(ctx, partner.ID)
	if err != nil {
		return fmt.Errorf("check expired partner docs: %w", err)
	}
	if expiredPartnerDocs > 0 {
		return fmt.Errorf("forbidden: %d kyc document(s) expired — please re-upload", expiredPartnerDocs)
	}
	vehicles, err := s.store.ListVehiclesByPartner(ctx, partner.ID)
	if err != nil {
		return err
	}
	var approvedVehicle *store.Vehicle
	for i, v := range vehicles {
		if v.Status == "approved" && v.IsActive {
			approvedVehicle = &vehicles[i]
			break
		}
	}
	if approvedVehicle == nil {
		return fmt.Errorf("forbidden: no approved vehicle")
	}
	// Same expiry gate applies to vehicle paperwork (RC, insurance,
	// permit, PUC). Without this, an unpermitted vehicle stays
	// matchable until an admin notices.
	expiredVehicleDocs, err := s.store.CountExpiredApprovedVehicleDocs(ctx, approvedVehicle.ID)
	if err != nil {
		return fmt.Errorf("check expired vehicle docs: %w", err)
	}
	if expiredVehicleDocs > 0 {
		return fmt.Errorf("forbidden: %d vehicle document(s) expired — please re-upload", expiredVehicleDocs)
	}
	sub, err := s.store.GetActiveSubscription(ctx, partner.ID)
	if err != nil {
		if errors.Is(err, store.ErrSubscriptionNotFound) {
			return fmt.Errorf("forbidden: no active subscription")
		}
		return err
	}
	if sub.Status != "trial" && sub.Status != "active" && sub.Status != "grace_period" {
		return fmt.Errorf("forbidden: subscription is %s", sub.Status)
	}
	if err := s.store.SetPartnerOnlineFlag(ctx, partner.ID, true); err != nil {
		return fmt.Errorf("set online: %w", err)
	}
	if perr := s.producer.PublishPartnerOnline(ctx, partner.ID); perr != nil {
		slog.Warn("rider: publish partner.online failed", "partner_id", partner.ID, "error", perr)
	}
	return nil
}

// GoOffline flips the partner's online flag off and expires their pending
// offers. The Redis GEO entry naturally times out via TTL but we also remove
// it explicitly to avoid stale matches in the next 60-second window.
func (s *Service) GoOffline(ctx context.Context, partnerUserID uuid.UUID) error {
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	if err := s.store.SetPartnerOnlineFlag(ctx, partner.ID, false); err != nil {
		return fmt.Errorf("set offline: %w", err)
	}
	// Remove the partner from the city GEO set in Redis (best-effort).
	if s.rdb != nil && partner.CityID != nil {
		key := redisOnlineKey(partner.CityID.String())
		if err := s.rdb.ZRem(ctx, key, partner.ID.String()).Err(); err != nil && !errors.Is(err, redis.Nil) {
			slog.Warn("rider: zrem partner from online set failed", "partner_id", partner.ID, "error", err)
		}
		if err := s.rdb.ZRem(ctx, "rider:online", partner.ID.String()).Err(); err != nil && !errors.Is(err, redis.Nil) {
			slog.Warn("rider: zrem partner from global online set failed", "partner_id", partner.ID, "error", err)
		}
	}
	if perr := s.producer.PublishPartnerOffline(ctx, partner.ID); perr != nil {
		slog.Warn("rider: publish partner.offline failed", "partner_id", partner.ID, "error", perr)
	}
	return nil
}

// UpdateLocationRequest is the input for UpdateLocation.
type UpdateLocationRequest struct {
	Lat     float64
	Lng     float64
	Speed   *float64
	Heading *float64
}

// UpdateLocation upserts the partner's location in Postgres and refreshes the
// Redis GEO entry. Geohash precision 6 (~1km cells).
func (s *Service) UpdateLocation(ctx context.Context, partnerUserID uuid.UUID, req UpdateLocationRequest) error {
	if !validLatLng(req.Lat, req.Lng) {
		return fmt.Errorf("invalid: lat/lng out of range")
	}
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	gh := geo.Encode(req.Lat, req.Lng, 6)
	if err := s.store.UpsertPartnerLocation(ctx, store.UpsertPartnerLocationInput{
		PartnerID:    partner.ID,
		LastLat:      req.Lat,
		LastLng:      req.Lng,
		LastGeohash:  gh,
		LastSpeedMPS: req.Speed,
		LastHeading:  req.Heading,
		IsOnline:     partner.IsOnline,
	}); err != nil {
		return fmt.Errorf("upsert location: %w", err)
	}
	// Hot-path mirror in Redis. Only push when the partner is online; offline
	// partners must not appear in matching even if they keep pinging.
	if s.rdb != nil && partner.IsOnline {
		cityKey := redisOnlineKey("")
		if partner.CityID != nil {
			cityKey = redisOnlineKey(partner.CityID.String())
		}
		if err := s.rdb.GeoAdd(ctx, cityKey, &redis.GeoLocation{
			Name:      partner.ID.String(),
			Longitude: req.Lng,
			Latitude:  req.Lat,
		}).Err(); err != nil {
			slog.Warn("rider: geoadd failed", "key", cityKey, "partner_id", partner.ID, "error", err)
		} else {
			// Keep the set bounded — refresh per-key TTL on every update.
			if err := s.rdb.Expire(ctx, cityKey, onlineKeyTTL*4).Err(); err != nil {
				slog.Debug("rider: expire on online set failed", "key", cityKey, "error", err)
			}
		}
	}
	return nil
}
