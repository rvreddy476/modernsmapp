package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RealtimeTokenTTL is the lifetime of a food realtime token. notification-
// service checks the token only when an SSE connection opens, so the TTL bounds
// how long a leaked token can open new connections; clients fetch a fresh token
// for every (re)connect.
const RealtimeTokenTTL = 5 * time.Minute

// Realtime token scopes.
const (
	RealtimeScopeOrder      = "order"
	RealtimeScopeRestaurant = "restaurant"
	RealtimeScopeDelivery   = "delivery"
)

var (
	ErrRealtimeNotConfigured = errors.New("realtime is not configured")
	ErrRealtimeScopeInvalid  = errors.New("scope must be order, restaurant or delivery")
	ErrRealtimeIDRequired    = errors.New("this scope needs an id")
	ErrRealtimeIDNotAllowed  = errors.New("the delivery scope takes no id")
)

// RealtimeTokenSigner is the part of *realtime.TokenSigner the service uses.
type RealtimeTokenSigner interface {
	Sign(userID string, topics []string) (string, error)
}

// RealtimeToken is the POST /v1/food/realtime/token response.
type RealtimeToken struct {
	Token      string   `json:"token"`
	Scope      string   `json:"scope"`
	Topics     []string `json:"topics"`
	ExpiresAt  string   `json:"expires_at"`
	TTLSeconds int      `json:"ttl_seconds"`
}

// IssueRealtimeToken signs a token for ONE scope:
//
//	order      food.order.<id>                   only the order's customer
//	restaurant food.restaurant.<id>.orders and   only the restaurant's owner
//	           food.restaurant.<id>
//	delivery   food.delivery_partner.<user_id>.assignments
//	                                             the caller's own, if they are a
//	                                             delivery partner
//
// An order or restaurant the caller does not own is pgx.ErrNoRows (404), the
// same answer as one that does not exist.
func (s *Service) IssueRealtimeToken(ctx context.Context, userID uuid.UUID, scope string, id *uuid.UUID) (*RealtimeToken, error) {
	if s.rtSigner == nil || s.rtPublisher == nil {
		return nil, ErrRealtimeNotConfigured
	}
	var topics []string
	switch scope {
	case RealtimeScopeOrder:
		if id == nil {
			return nil, ErrRealtimeIDRequired
		}
		if _, err := s.store.GetOrder(ctx, userID, *id); err != nil {
			return nil, err
		}
		topics = []string{orderTopic(*id)}
	case RealtimeScopeRestaurant:
		if id == nil {
			return nil, ErrRealtimeIDRequired
		}
		owned, err := s.store.ListPartnerRestaurants(ctx, userID)
		if err != nil {
			return nil, err
		}
		if !ownsRestaurant(owned, *id) {
			return nil, pgx.ErrNoRows
		}
		topics = []string{restaurantOrdersTopic(*id), restaurantTopic(*id)}
	case RealtimeScopeDelivery:
		if id != nil {
			return nil, ErrRealtimeIDNotAllowed
		}
		if _, err := s.store.GetDeliveryPartner(ctx, userID); err != nil {
			return nil, err
		}
		topics = []string{deliveryPartnerTopic(userID)}
	default:
		return nil, ErrRealtimeScopeInvalid
	}
	// Read the clock before signing: the signer stamps its own expiry at Sign
	// time, which is never earlier than the one reported here.
	expires := s.clockNow().Add(RealtimeTokenTTL).UTC().Truncate(time.Second)
	tok, err := s.rtSigner.Sign(userID.String(), topics)
	if err != nil {
		return nil, err
	}
	return &RealtimeToken{
		Token: tok, Scope: scope, Topics: topics,
		ExpiresAt: expires.Format(time.RFC3339), TTLSeconds: int(RealtimeTokenTTL / time.Second),
	}, nil
}

func ownsRestaurant(owned []postgres.PartnerRestaurant, id uuid.UUID) bool {
	for _, r := range owned {
		if r.ID == id {
			return true
		}
	}
	return false
}

// WithRealtimeClock overrides the clock used for token expiry (tests).
func (s *Service) WithRealtimeClock(now func() time.Time) *Service {
	s.realtimeNow = now
	return s
}

func (s *Service) clockNow() time.Time {
	if s.realtimeNow != nil {
		return s.realtimeNow()
	}
	return time.Now()
}
