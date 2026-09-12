package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Tube channel subscriptions (2026-09-12).
//
// Founder decisions: Subscribe on a Tube channel is follow + notify behind
// one button, and unsubscribing removes both. Every subscriber is notified
// by default (notify_on 'all'); the per-channel bell opts out ('none').
//
// The order of operations is the contract. A subscription is a follow plus
// a bell, so the follow edge is written FIRST, through graph-service, and
// the subscription row only once that succeeded. A graph outage therefore
// leaves no half-subscription behind: the caller sees GRAPH_UNAVAILABLE and
// taps again. The reverse direction is enforced by the UserUnfollowed
// consumer (internal/events), which drops the subscription whenever the
// follow goes away through any other door.

var (
	ErrCannotSubscribeSelf = errors.New("you cannot subscribe to your own channel")
	ErrGraphUnavailable    = errors.New("the follow graph is unavailable; try again")
	ErrInvalidNotifyOn     = errors.New("notify_on must be 'all' or 'none'")
	// ErrNotSubscribed mirrors the store error at the service boundary.
	ErrNotSubscribed = postgres.ErrNotSubscribed
)

// Follow status values graph-service answers with.
const (
	FollowStatusFollowed  = "followed"
	FollowStatusRequested = "requested"
)

// ValidateNotifyOn resolves the bell value: empty means the default 'all';
// anything but 'all' / 'none' is ErrInvalidNotifyOn. 'highlights' and the
// never-valid 'uploads' are refused on purpose: the fan-out reads exactly
// one value, and a tier it does not read is a silent unsubscribe.
func ValidateNotifyOn(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return postgres.NotifyOnAll, nil
	case postgres.NotifyOnAll:
		return postgres.NotifyOnAll, nil
	case postgres.NotifyOnNone:
		return postgres.NotifyOnNone, nil
	}
	return "", ErrInvalidNotifyOn
}

// graphFollowClient is the slice of graph-service the subscription flows
// need, an interface so the service tests drive it with httptest or a fake.
type graphFollowClient interface {
	// Follow creates follower -> target and reports "followed" or
	// "requested" (private target). Idempotent on graph-service's side.
	Follow(ctx context.Context, followerID, targetID uuid.UUID) (string, error)
	// Unfollow removes follower -> target. A missing edge is not an error.
	Unfollow(ctx context.Context, followerID, targetID uuid.UUID) error
}

// SetGraphFollowClient overrides the graph client (tests). Production
// builds one lazily from GRAPH_SERVICE_URL and the internal key.
func (s *Service) SetGraphFollowClient(c graphFollowClient) {
	s.graphFollows = c
}

func (s *Service) graphFollowClient() graphFollowClient {
	if s.graphFollows != nil {
		return s.graphFollows
	}
	if s.graphServiceURL == "" {
		return nil
	}
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	s.graphFollows = &httpGraphFollowClient{baseURL: strings.TrimRight(s.graphServiceURL, "/"), internalKey: s.internalServiceKey, client: client}
	return s.graphFollows
}

// graphWriteSource is the SR-3 attribution graph-service demands on every
// mutating write. It is what makes post-service a NAMED graph writer rather
// than an anonymous holder of the shared internal key.
const graphWriteSource = "post-service"

// httpGraphFollowClient speaks graph-service's follow contract:
//
//	POST /v1/graph/follow    {"user_id": target}  -> {"data": {"status": "followed"|"requested"}}
//	POST /v1/graph/unfollow  {"user_id": target}  -> {"data": {"status": "unfollowed"}}
//
// with X-User-Id naming the follower.
type httpGraphFollowClient struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

func (g *httpGraphFollowClient) post(ctx context.Context, path string, followerID, targetID uuid.UUID) (string, error) {
	body, err := json.Marshal(map[string]string{"user_id": targetID.String()})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", followerID.String())
	req.Header.Set("X-Graph-Write-Source", graphWriteSource)
	if g.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", g.internalKey)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("graph-service %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var envelope struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
		Status string `json:"status"` // legacy un-wrapped shape tolerated
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("graph-service %s: decode: %w", path, err)
	}
	if envelope.Data.Status != "" {
		return envelope.Data.Status, nil
	}
	return envelope.Status, nil
}

func (g *httpGraphFollowClient) Follow(ctx context.Context, followerID, targetID uuid.UUID) (string, error) {
	status, err := g.post(ctx, "/v1/graph/follow", followerID, targetID)
	if err != nil {
		return "", err
	}
	if status != FollowStatusRequested {
		// graph-service says "followed"; anything else unexpected is still
		// a created edge, so report it as the plain follow.
		status = FollowStatusFollowed
	}
	return status, nil
}

func (g *httpGraphFollowClient) Unfollow(ctx context.Context, followerID, targetID uuid.UUID) error {
	_, err := g.post(ctx, "/v1/graph/unfollow", followerID, targetID)
	return err
}

// SubscribeResult is the POST /v1/channels/:ref/subscribe body.
type SubscribeResult struct {
	Status          string `json:"status"`
	NotifyOn        string `json:"notify_on"`
	Follow          string `json:"follow"`
	SubscriberCount int    `json:"subscriber_count"`
}

// UnsubscribeResult is the DELETE /v1/channels/:ref/subscribe body.
type UnsubscribeResult struct {
	Status          string `json:"status"`
	SubscriberCount int    `json:"subscriber_count"`
}

// SubscriptionView is GET /v1/channels/:ref/subscription: {subscribed:false}
// or {subscribed:true, notify_on, subscribed_at}.
type SubscriptionView struct {
	Subscribed   bool       `json:"subscribed"`
	NotifyOn     string     `json:"notify_on,omitempty"`
	SubscribedAt *time.Time `json:"subscribed_at,omitempty"`
}

// SubscriptionListItem is one row of GET /v1/channels/subscriptions.
type SubscriptionListItem struct {
	Channel      *SubscribedChannelRef `json:"channel"`
	NotifyOn     string                `json:"notify_on"`
	SubscribedAt time.Time             `json:"subscribed_at"`
}

// SubscribedChannelRef is the card the subscriptions page renders.
type SubscribedChannelRef struct {
	UserID          uuid.UUID `json:"user_id"`
	Name            string    `json:"name"`
	Handle          string    `json:"handle"`
	AvatarURL       *string   `json:"avatar_url"`
	SubscriberCount int       `json:"subscriber_count"`
}

// Subscription page sizes.
const (
	DefaultSubscriptionsLimit = 20
	MaxSubscriptionsLimit     = 100
)

// resolveChannelRef finds a channel by handle (with or without '@') or by
// owner user id, or ErrChannelNotFound.
func (s *Service) resolveChannelRef(ctx context.Context, ref string) (*postgres.Channel, error) {
	if s.channels == nil {
		return nil, errors.New("channel store not configured")
	}
	var (
		ch  *postgres.Channel
		err error
	)
	if id, parseErr := uuid.Parse(strings.TrimSpace(ref)); parseErr == nil {
		ch, err = s.channels.GetChannelByUserID(ctx, id)
	} else {
		handle, normErr := NormalizeChannelHandle(ref)
		if normErr != nil {
			return nil, ErrChannelNotFound
		}
		ch, err = s.channels.GetChannelByHandle(ctx, handle)
	}
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, ErrChannelNotFound
	}
	return ch, nil
}

// subscriberCount re-reads the trigger-maintained count after a write so
// the response carries the number the next GET would show.
func (s *Service) subscriberCount(ctx context.Context, ch *postgres.Channel) int {
	fresh, err := s.channels.GetChannelByUserID(ctx, ch.UserID)
	if err != nil || fresh == nil {
		return ch.SubscriberCount
	}
	return fresh.SubscriberCount
}

// Subscribe is the one button: follow the owner through graph-service, then
// record the subscription. A graph failure is ErrGraphUnavailable with no
// row written. Subscribing again is idempotent and never resets the bell.
func (s *Service) Subscribe(ctx context.Context, callerID uuid.UUID, ref, notifyOn string) (*SubscribeResult, error) {
	notifyOn, err := ValidateNotifyOn(notifyOn)
	if err != nil {
		return nil, err
	}
	ch, err := s.resolveChannelRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if ch.UserID == callerID {
		return nil, ErrCannotSubscribeSelf
	}
	graph := s.graphFollowClient()
	if graph == nil {
		return nil, ErrGraphUnavailable
	}
	followStatus, err := graph.Follow(ctx, callerID, ch.UserID)
	if err != nil {
		slog.WarnContext(ctx, "channel subscribe: graph follow failed", "channel", ch.ID, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrGraphUnavailable, err)
	}
	inserted, err := s.channels.Subscribe(ctx, ch.ID, callerID, notifyOn)
	if err != nil {
		return nil, err
	}
	result := &SubscribeResult{Status: "subscribed", NotifyOn: notifyOn, Follow: followStatus}
	if !inserted {
		// Existing row: report the bell as it is, not as the retry asked.
		if sub, err := s.channels.GetSubscription(ctx, ch.ID, callerID); err == nil && sub != nil {
			result.NotifyOn = sub.NotifyOn
		}
	}
	result.SubscriberCount = s.subscriberCount(ctx, ch)
	return result, nil
}

// Unsubscribe removes both halves: the follow edge first, then the row. If
// the unfollow fails the row stays, so the two never disagree in the
// direction that would keep pushing uploads to someone who left.
func (s *Service) Unsubscribe(ctx context.Context, callerID uuid.UUID, ref string) (*UnsubscribeResult, error) {
	ch, err := s.resolveChannelRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if ch.UserID == callerID {
		return nil, ErrCannotSubscribeSelf
	}
	graph := s.graphFollowClient()
	if graph == nil {
		return nil, ErrGraphUnavailable
	}
	if err := graph.Unfollow(ctx, callerID, ch.UserID); err != nil {
		slog.WarnContext(ctx, "channel unsubscribe: graph unfollow failed", "channel", ch.ID, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrGraphUnavailable, err)
	}
	if _, err := s.channels.Unsubscribe(ctx, ch.ID, callerID); err != nil {
		return nil, err
	}
	return &UnsubscribeResult{Status: "unsubscribed", SubscriberCount: s.subscriberCount(ctx, ch)}, nil
}

// SetNotifyOn flips the bell on an existing subscription.
func (s *Service) SetNotifyOn(ctx context.Context, callerID uuid.UUID, ref, notifyOn string) (*SubscriptionView, error) {
	notifyOn, err := ValidateNotifyOn(notifyOn)
	if err != nil {
		return nil, err
	}
	ch, err := s.resolveChannelRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.channels.SetNotifyOn(ctx, ch.ID, callerID, notifyOn); err != nil {
		return nil, err
	}
	return s.subscriptionView(ctx, ch.ID, callerID)
}

// GetSubscription answers GET /v1/channels/:ref/subscription for the caller.
func (s *Service) GetSubscription(ctx context.Context, callerID uuid.UUID, ref string) (*SubscriptionView, error) {
	ch, err := s.resolveChannelRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s.subscriptionView(ctx, ch.ID, callerID)
}

func (s *Service) subscriptionView(ctx context.Context, channelID, userID uuid.UUID) (*SubscriptionView, error) {
	sub, err := s.channels.GetSubscription(ctx, channelID, userID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return &SubscriptionView{Subscribed: false}, nil
	}
	at := sub.SubscribedAt
	return &SubscriptionView{Subscribed: true, NotifyOn: sub.NotifyOn, SubscribedAt: &at}, nil
}

// attachViewerSubscription fills is_subscribed / notify_on on a channel view
// for a signed-in viewer. Best-effort: a failed read leaves them absent,
// which the client reads as "unknown", never as "not subscribed".
func (s *Service) attachViewerSubscription(ctx context.Context, viewerID uuid.UUID, ch *postgres.Channel, view *ChannelView) {
	if viewerID == uuid.Nil {
		return
	}
	sub, err := s.channels.GetSubscription(ctx, ch.ID, viewerID)
	if err != nil {
		slog.WarnContext(ctx, "channel view: subscription read skipped", "channel", ch.ID, "err", err)
		return
	}
	subscribed := sub != nil
	view.IsSubscribed = &subscribed
	if sub != nil {
		notify := sub.NotifyOn
		view.NotifyOn = &notify
	}
}

// ClampSubscriptionsLimit resolves the page size: default 20, at most 100.
func ClampSubscriptionsLimit(limit int) int {
	if limit <= 0 {
		return DefaultSubscriptionsLimit
	}
	if limit > MaxSubscriptionsLimit {
		return MaxSubscriptionsLimit
	}
	return limit
}

// ErrInvalidSubscriptionCursor: the cursor is not one this service issued.
var ErrInvalidSubscriptionCursor = errors.New("invalid cursor")

// encodeSubscriptionCursor / decodeSubscriptionCursor carry the keyset
// position opaquely: base64url of "<RFC3339Nano>|<channel uuid>".
func encodeSubscriptionCursor(at time.Time, channelID uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + channelID.String()))
}

func decodeSubscriptionCursor(raw string) (*postgres.SubscriptionCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrInvalidSubscriptionCursor
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidSubscriptionCursor
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, ErrInvalidSubscriptionCursor
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return nil, ErrInvalidSubscriptionCursor
	}
	return &postgres.SubscriptionCursor{SubscribedAt: at, ChannelID: id}, nil
}

// ListMySubscriptions answers GET /v1/channels/subscriptions: the caller's
// subscriptions newest first, each with its channel card, plus the cursor
// for the next page ("" when this was the last).
func (s *Service) ListMySubscriptions(ctx context.Context, callerID uuid.UUID, cursor string, limit int) ([]SubscriptionListItem, string, error) {
	if s.channels == nil {
		return nil, "", errors.New("channel store not configured")
	}
	pos, err := decodeSubscriptionCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	limit = ClampSubscriptionsLimit(limit)
	// One extra row tells us whether a next page exists without a count.
	rows, err := s.channels.ListSubscriptionsForUser(ctx, callerID, pos, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = encodeSubscriptionCursor(last.SubscribedAt, last.Channel.ID)
	}
	var avatarIDs []uuid.UUID
	for _, r := range rows {
		if r.Channel.AvatarMediaID != nil {
			avatarIDs = append(avatarIDs, *r.Channel.AvatarMediaID)
		}
	}
	avatars := s.resolveAvatarURLs(ctx, callerID, avatarIDs)
	items := make([]SubscriptionListItem, 0, len(rows))
	for i := range rows {
		ch := rows[i].Channel
		ref := &SubscribedChannelRef{UserID: ch.UserID, Name: ch.Name, Handle: ch.Handle, SubscriberCount: ch.SubscriberCount}
		if ch.AvatarMediaID != nil {
			if u, ok := avatars[*ch.AvatarMediaID]; ok && u != "" {
				url := u
				ref.AvatarURL = &url
			}
		}
		items = append(items, SubscriptionListItem{Channel: ref, NotifyOn: rows[i].NotifyOn, SubscribedAt: rows[i].SubscribedAt})
	}
	return items, next, nil
}

// Internal fan-out contract (same JSON user-service served; the callers are
// notification-service and feed-service, moving their base URL here).

// ListSubscriberIDsAfter pages a channel's push-eligible subscribers.
func (s *Service) ListSubscriberIDsAfter(ctx context.Context, channelID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	if s.channels == nil {
		return nil, errors.New("channel store not configured")
	}
	return s.channels.ListSubscriberIDsAfter(ctx, channelID, after, limit)
}

// ListSubscribedOwnersAfter pages the owners whose channels a viewer subscribes to.
func (s *Service) ListSubscribedOwnersAfter(ctx context.Context, userID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	if s.channels == nil {
		return nil, errors.New("channel store not configured")
	}
	return s.channels.ListSubscribedOwnersAfter(ctx, userID, after, limit)
}

// ChannelIDByOwner resolves an owner to their channel row id, or uuid.Nil.
func (s *Service) ChannelIDByOwner(ctx context.Context, ownerID uuid.UUID) (uuid.UUID, error) {
	if s.channels == nil {
		return uuid.Nil, errors.New("channel store not configured")
	}
	ch, err := s.channels.GetChannelByUserID(ctx, ownerID)
	if err != nil || ch == nil {
		return uuid.Nil, err
	}
	return ch.ID, nil
}

// stampChannel fills the subscriber fan-out fields on PostCreated from the
// author's channel: id (the fan-out key), name and handle (so the push
// renders "{channel} uploaded: {title}" without a lookup per recipient).
// Read locally now that post-service owns the table; the former HTTP hop to
// user-service was best-effort and silently blank whenever it failed.
func (s *Service) stampChannel(ctx context.Context, pc *events.PostCreatedPayload, authorID uuid.UUID) {
	if s.channels == nil {
		return
	}
	ch, err := s.channels.GetChannelByUserID(ctx, authorID)
	if err != nil {
		slog.WarnContext(ctx, "post created: channel stamp skipped", "author", authorID, "err", err)
		return
	}
	if ch == nil {
		return
	}
	pc.ChannelID = ch.ID.String()
	pc.ChannelName = ch.Name
	pc.ChannelHandle = ch.Handle
}
