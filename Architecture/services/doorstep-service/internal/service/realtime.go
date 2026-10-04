package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// Realtime (A4). Live updates go onto Redis Streams through shared/realtime's
// StreamPublisher (notification-service's SSE gateway reads them; rider's
// Pub/Sub publisher never reaches a client and is not used). Topics:
// doorstep.booking.<booking_id> (the customer, the accepted professional),
// doorstep.pro.<user_id> (one professional's offers and jobs) and
// doorstep.admin.live (ops). Tokens are shared/realtime TokenSigner tokens
// (REALTIME_TOKEN_SECRET) naming exactly the topics the caller may read.
//
// Frames carry ids, statuses and times only: never an address, a phone
// number or an OTP. A publish failure is logged and dropped: Kafka (the
// outbox) is the durable copy and clients refetch on reconnect.

// RealtimePublisher is shared/realtime.StreamPublisher.
type RealtimePublisher interface {
	Publish(ctx context.Context, topic, eventType string, data any) error
}

// TokenSigner is shared/realtime.TokenSigner.
type TokenSigner interface {
	Sign(userID string, topics []string) (string, error)
}

// Presence mirrors on-duty professionals' latest fix into Redis GEO per
// city (internal/presence).
type Presence interface {
	Upsert(ctx context.Context, city string, proID uuid.UUID, lat, lng float64) error
	Remove(ctx context.Context, city string, proID uuid.UUID) error
}

// RealtimeTokenTTL is a realtime token's lifetime: notification-service
// checks it when an SSE connection opens, so clients fetch one per connect.
const RealtimeTokenTTL = 5 * time.Minute

// Realtime topics.
const AdminLiveTopic = "doorstep.admin.live"

// BookingTopic is one booking's topic.
func BookingTopic(id uuid.UUID) string { return "doorstep.booking." + id.String() }

// ProTopic is one professional's topic (by user id).
func ProTopic(userID uuid.UUID) string { return "doorstep.pro." + userID.String() }

// Realtime frame types.
const (
	FrameBookingStatus   = "doorstep.booking.status"
	FrameBookingLate     = "doorstep.booking.pro_late"
	FrameProLocation     = "doorstep.booking.pro_location"
	FrameOfferNew        = "doorstep.pro.offer.new"
	FrameOfferClosed     = "doorstep.pro.offer.closed"
	FrameJobAssigned     = "doorstep.pro.job.assigned"
	FrameJobRemoved      = "doorstep.pro.job.removed"
	FrameJobUpdated      = "doorstep.pro.job.updated"
	FrameDuty            = "doorstep.pro.duty"
	FrameAdminUnassigned = "doorstep.admin.unassigned"
)

// BookingStatusFrame is a booking's status change.
type BookingStatusFrame struct {
	BookingID uuid.UUID `json:"booking_id"`
	Status    string    `json:"status"`
	SlotStart time.Time `json:"slot_start"`
	SlotEnd   time.Time `json:"slot_end"`
	At        time.Time `json:"at"`
}

// LateFrame tells the customer the professional is late.
type LateFrame struct {
	BookingID   uuid.UUID `json:"booking_id"`
	MinutesLate int       `json:"minutes_late"`
	FreeCancel  bool      `json:"free_cancel"`
	At          time.Time `json:"at"`
}

// LocationFrame is the travelling professional's position (en_route only).
type LocationFrame struct {
	BookingID  uuid.UUID `json:"booking_id"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	EtaMinutes int       `json:"eta_minutes"`
	At         time.Time `json:"at"`
}

// OfferFrame is a new offer.
type OfferFrame struct {
	OfferID   uuid.UUID `json:"offer_id"`
	BookingID uuid.UUID `json:"booking_id"`
	ExpiresAt time.Time `json:"expires_at"`
	SlotStart time.Time `json:"slot_start"`
	SlotEnd   time.Time `json:"slot_end"`
	At        time.Time `json:"at"`
}

// OfferClosedFrame is an offer that is no longer open.
type OfferClosedFrame struct {
	OfferID   uuid.UUID `json:"offer_id"`
	BookingID uuid.UUID `json:"booking_id"`
	Outcome   string    `json:"outcome"`
	At        time.Time `json:"at"`
}

// JobFrame is a change to one of the professional's jobs.
type JobFrame struct {
	BookingID uuid.UUID `json:"booking_id"`
	Status    string    `json:"status,omitempty"`
	Cause     string    `json:"cause,omitempty"`
	At        time.Time `json:"at"`
}

// DutyFrame is the professional's duty change.
type DutyFrame struct {
	OnDuty bool       `json:"on_duty"`
	Since  *time.Time `json:"since"`
	Reason string     `json:"reason"`
	At     time.Time  `json:"at"`
}

// AdminUnassignedFrame is an ops alert.
type AdminUnassignedFrame struct {
	BookingID     uuid.UUID `json:"booking_id"`
	Reason        string    `json:"reason"`
	MinutesToSlot int       `json:"minutes_to_slot"`
	At            time.Time `json:"at"`
}

func (s *Service) publish(ctx context.Context, topic, frame string, data any) {
	if s.ds.Realtime == nil {
		return
	}
	if err := s.ds.Realtime.Publish(ctx, topic, frame, data); err != nil {
		slog.WarnContext(ctx, "doorstep: realtime publish failed", "topic", topic, "frame", frame, "error", err)
	}
}

func (s *Service) publishBooking(ctx context.Context, id uuid.UUID, status string, start, end time.Time) {
	s.publish(ctx, BookingTopic(id), FrameBookingStatus, BookingStatusFrame{BookingID: id, Status: status,
		SlotStart: start.UTC(), SlotEnd: end.UTC(), At: s.nowUTC()})
}

// publishBookingNow publishes a booking's current status (after a change
// made elsewhere: a payment, a cancel, a reschedule).
func (s *Service) publishBookingNow(ctx context.Context, id uuid.UUID) {
	if s.ds.Realtime == nil || s.ds.Store == nil {
		return
	}
	f, err := s.ds.Store.DispatchFacts(ctx, id)
	if err != nil {
		slog.WarnContext(ctx, "doorstep: realtime booking read failed", "booking_id", id, "error", err)
		return
	}
	s.publishBooking(ctx, id, f.Status, f.SlotStart, f.SlotEnd)
}

func realtimeUnavailable() *apperr.Error {
	return apperr.New(http.StatusServiceUnavailable, apperr.CodeRealtimeUnavailable, "live updates are not available on this deployment")
}

func (s *Service) sign(userID uuid.UUID, topics []string) (*model.RealtimeToken, error) {
	expires := s.nowUTC().Add(RealtimeTokenTTL).Truncate(time.Second)
	tok, err := s.ds.Signer.Sign(userID.String(), topics)
	if err != nil {
		return nil, err
	}
	return &model.RealtimeToken{Token: tok, Topics: topics, ExpiresAt: expires}, nil
}

// CustomerRealtimeToken signs a token for one of the customer's bookings
// (doorstep.booking.<id>); anyone else's booking is 404.
func (s *Service) CustomerRealtimeToken(ctx context.Context, user uuid.UUID, in model.RealtimeTokenRequest) (*model.RealtimeToken, error) {
	if in.BookingID == nil || *in.BookingID == uuid.Nil {
		return nil, apperr.Invalid("booking_id", "booking_id is required")
	}
	if s.ds.Signer == nil || s.ds.Realtime == nil {
		return nil, realtimeUnavailable()
	}
	if _, err := s.bk.Store.BookingRecord(ctx, *in.BookingID, &user); errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	} else if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	t, err := s.sign(user, []string{BookingTopic(*in.BookingID)})
	if err != nil {
		return nil, internal(ctx, "realtime sign", err)
	}
	return t, nil
}

// ProRealtimeToken signs a token for the professional's own topic and the
// bookings they have accepted that are still happening.
func (s *Service) ProRealtimeToken(ctx context.Context, user uuid.UUID) (*model.RealtimeToken, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	if s.ds.Signer == nil || s.ds.Realtime == nil {
		return nil, realtimeUnavailable()
	}
	ids, err := s.ds.Store.ProActiveBookingIDs(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "pro active bookings", err)
	}
	topics := []string{ProTopic(user)}
	for _, id := range ids {
		topics = append(topics, BookingTopic(id))
	}
	t, err := s.sign(user, topics)
	if err != nil {
		return nil, internal(ctx, "realtime sign", err)
	}
	return t, nil
}

// etaMinutes estimates travel at 20 km/h in city traffic, at least 1.
func etaMinutes(distanceM float64) int {
	m := int(math.Ceil(distanceM / 1000 / 20 * 60))
	if m < 1 {
		return 1
	}
	return m
}

// liveBefore reads a booking's open offer or accepted professional before
// a change made outside dispatch (cancel, reschedule), so they can be told
// after it commits.
func (s *Service) liveBefore(ctx context.Context, id uuid.UUID) *store.AssignmentRef {
	if s.ds.Store == nil {
		return nil
	}
	f, err := s.ds.Store.DispatchFacts(ctx, id)
	if err != nil {
		return nil
	}
	return f.Live
}

// afterCancelled publishes a cancelled booking and tells its professional.
func (s *Service) afterCancelled(ctx context.Context, id uuid.UUID, live *store.AssignmentRef) {
	s.publishBookingNow(ctx, id)
	s.tellRemoved(ctx, id, live, "booking_cancelled")
}

// tellRemoved tells a professional their offer was withdrawn or their job
// taken away.
func (s *Service) tellRemoved(ctx context.Context, id uuid.UUID, live *store.AssignmentRef, cause string) {
	if live == nil {
		return
	}
	at := s.nowUTC()
	if live.Status == "offered" {
		s.publish(ctx, ProTopic(live.ProUserID), FrameOfferClosed, OfferClosedFrame{OfferID: live.ID, BookingID: id,
			Outcome: "withdrawn", At: at})
		return
	}
	s.publish(ctx, ProTopic(live.ProUserID), FrameJobRemoved, JobFrame{BookingID: id, Cause: cause, At: at})
}
