package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"sync"
	"time"
)

// PartnerNotice is already-rendered, allowlisted operational copy. It never
// contains raw domain events, document references, addresses or phone numbers.
type PartnerNotice struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	App        string
	Type       string
	EntityID   uuid.UUID
	EntityType string
	Title      string
	Body       string
	DeepLink   string
	Data       map[string]string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	Inbox      bool
}

func (s *Service) queuePartnerNotice(ctx context.Context, n PartnerNotice) error {
	if s.pgStore == nil {
		return errors.New("partner queue unavailable")
	}
	if n.ID == uuid.Nil || n.UserID == uuid.Nil || n.EntityID == uuid.Nil {
		return errors.New("invalid partner notice")
	}
	switch n.App {
	case postgres.AppPartner, postgres.AppFeastRider, postgres.AppDoorstepPro, postgres.AppMopeduCaptain:
	default:
		return errors.New("merchant/customer notice excluded from partner queue")
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return s.pgStore.EnqueuePartnerNotification(ctx, n.ID, raw, n.ExpiresAt)
}

// Retries survive process restarts. Provider errors never contain tokens in the
// persisted error code. A send may repeat after a crash before its receipt;
// deterministic collapse keys bound that unavoidable at-least-once window.
func (s *Service) RunPartnerNotificationWorker(ctx context.Context) {
	if s.pgStore == nil {
		return
	}
	// Independent leases avoid a slow device or inbox call holding every
	// partner's 20-second offer behind a single sender. Concurrency is bounded.
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.runPartnerNotificationWorker(ctx)
		}()
	}
	workers.Wait()
}

func (s *Service) runPartnerNotificationWorker(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for i := 0; i < 20; i++ {
			j, err := s.pgStore.ClaimPartnerNotification(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			if err != nil {
				slog.Error("partner notification queue read failed", "error", err)
				break
			}
			deadline := time.Now().Add(30 * time.Second)
			if j.ExpiresAt != nil && j.ExpiresAt.Before(deadline) {
				deadline = *j.ExpiresAt
			}
			runCtx, cancel := context.WithDeadline(ctx, deadline)
			status, code := s.deliverPartnerNotification(runCtx, j)
			cancel()
			if status == "pending" && code != "device_batch_remaining" && j.Attempts >= 12 {
				status = "failed"
			}
			if err = s.pgStore.FinishPartnerNotification(ctx, j, status, code, partnerJobRetryDelay(j, code)); err != nil {
				slog.Error("partner notification result persistence failed", "job_id", j.ID, "error", err)
			}
			if status == "failed" {
				slog.Error("partner notification needs operator attention", "job_id", j.ID, "reason", code)
			}
		}
	}
}

func partnerRetryDelay(attempt int) time.Duration {
	// Ordinary account/history notices use a conservative durable backoff.
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Minute * time.Duration(1<<(attempt-1))
}

func partnerJobRetryDelay(j *postgres.PartnerNotificationJob, code string) time.Duration {
	delay := partnerRetryDelay(j.Attempts)
	if j.ExpiresAt != nil {
		// A transient failure must have a chance to recover inside the original
		// 20-second window. Never extend that window or shorten provider guidance.
		attempt := min(max(j.Attempts, 1), 3)
		delay = time.Second * time.Duration(1<<(attempt-1))
	}
	if code == "device_batch_remaining" {
		delay = time.Second
	}
	return max(delay, j.RetryAfter)
}

func (s *Service) deliverPartnerNotification(ctx context.Context, j *postgres.PartnerNotificationJob) (string, string) {
	var n PartnerNotice
	if json.Unmarshal(j.Payload, &n) != nil || n.ID != j.ID {
		return "failed", "invalid_payload"
	}
	if n.Data == nil {
		n.Data = map[string]string{}
	}
	if j.ExpiresAt != nil && !time.Now().Before(*j.ExpiresAt) {
		return "expired", "offer_expired"
	}
	if s.pgStore == nil {
		return "pending", "suppression_lookup_failed"
	}
	suppressed, err := s.pgStore.IsSuppressed(ctx, n.UserID)
	if err != nil {
		// Fail closed for privacy without permanently losing the notification.
		return "pending", "suppression_lookup_failed"
	}
	if suppressed {
		return "delivered", "account_suppressed"
	}
	decision := DeliveryDecision{CreateInbox: n.Inbox, SendPush: true, SendWebSocket: true}
	if n.App == postgres.AppMomentum {
		if !customerNoticeAllowed(n) {
			return "failed", "invalid_customer_type"
		}
		decision = s.resolveGeneralDelivery(ctx, n.UserID, n.Type)
	}
	if n.Inbox && decision.CreateInbox {
		if s.scyllaStore == nil {
			return "pending", "inbox_unavailable"
		}
		err := s.deliverWithDecision(ctx, DeliveryDecision{CreateInbox: true, SendWebSocket: true}, n.UserID, n.UserID, n.Type, n.EntityType, n.EntityID, n.DeepLink, n.CreatedAt, n.ID.String(), RenderOverride{Title: n.Title, Body: n.Body})
		if err != nil {
			return "pending", "inbox_unavailable"
		}
	}
	if !decision.SendPush {
		return "delivered", "customer_push_muted"
	}
	devices, more, err := s.pgStore.PendingPartnerDevices(ctx, n.UserID, n.App, j.ID, 10)
	if err != nil {
		return "pending", "device_lookup_failed"
	}
	if len(devices) == 0 {
		active, err := s.pgStore.HasActivePartnerDevice(ctx, n.UserID, n.App)
		if err != nil {
			return "pending", "device_lookup_failed"
		}
		if active {
			return "delivered", ""
		} // all active devices have durable receipts
		return "pending", "no_registered_device"
	}
	if s.pusher == nil {
		return "pending", "provider_unconfigured"
	}
	retry := false
	for _, d := range devices {
		if !postgres.DeviceAppReceives(d.App, n.App) {
			continue
		}
		digest := sha256.Sum256([]byte(d.PushToken))
		hash := hex.EncodeToString(digest[:])
		delivered, err := s.pgStore.PartnerDeviceDelivered(ctx, j.ID, hash)
		if err != nil {
			return "pending", "receipt_lookup_failed"
		}
		if delivered {
			continue
		}
		// Recompute provider TTL on EVERY attempt; never restart an offer timer.
		if j.ExpiresAt != nil {
			// A retry or another device must not ring an offer after acceptance.
			if n.App != postgres.AppFeastKitchen {
				busy, err := partnerwork.BusyIfInstalled(ctx, s.pgStore.Pool(), n.UserID)
				if err != nil {
					return "pending", "work_state_unavailable"
				}
				if busy {
					return "expired", "partner_busy"
				}
			}
			left := time.Until(*j.ExpiresAt)
			if left < time.Second {
				return "expired", "offer_expired"
			}
			n.Data[push.AndroidTTLDataKey] = fmt.Sprintf("%ds", int64(left/time.Second))
		}
		err = s.pusher.Send(ctx, d.PushToken, d.Platform, n.Title, n.Body, n.Data)
		if errors.Is(err, push.ErrDeviceRejected) {
			if s.pgStore.RetireDeviceToken(ctx, n.UserID, d.PushToken) != nil {
				retry = true
			}
			continue
		}
		if err != nil {
			j.RetryAfter = max(j.RetryAfter, push.RetryDelay(err))
			retry = true
			continue
		}
		if s.pgStore.MarkPartnerDeviceDelivered(ctx, j, hash) != nil {
			retry = true
		}
	}
	if retry {
		return "pending", "provider_or_receipt_failure"
	}
	if more {
		// The next bounded claim reads only devices without receipts. Do not
		// mark the notice delivered while unsent devices remain in later pages.
		return "pending", "device_batch_remaining"
	}
	return "delivered", ""
}

func expiryAfter(ttl time.Duration) *time.Time {
	if ttl <= 0 {
		return nil
	}
	exp := time.Now().UTC().Add(ttl)
	return &exp
}

func (s *Service) QueueCommerceDeliveryReview(ctx context.Context, typ, key string, user, profile uuid.UUID, at time.Time) error {
	if typ != "commerce.delivery.application.reviewed" && typ != "commerce.delivery.document.reviewed" {
		return errors.New("unknown commerce worker notice")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	title := "Commerce Delivery application updated"
	if typ == "commerce.delivery.document.reviewed" {
		title = "Commerce Delivery document reviewed"
	}
	body := "Open your application for the latest review status and any required corrections."
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("commerce-delivery:"+key+":"+user.String()))
	data := map[string]string{"type": typ, "entity_id": profile.String(), "deep_link": "/partner/commerce-delivery", "title": title, "body": body, "collapse_key": "commerce-delivery:" + profile.String()}
	data[push.AndroidChannelDataKey] = "commerce_delivery_updates"
	return s.queuePartnerNotice(ctx, PartnerNotice{ID: id, UserID: user, App: postgres.AppPartner, Type: typ, EntityID: profile, EntityType: "commerce_delivery_partner", Title: title, Body: body, DeepLink: data["deep_link"], Data: data, CreatedAt: at, Inbox: true})
}
