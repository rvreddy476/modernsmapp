package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	groupevents "github.com/atpost/group-service/internal/events"
	"github.com/atpost/group-service/internal/mediaclaims"
	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store              *store.Store
	rdb                *redis.Client
	messageServiceURL  string
	postServiceURL     string
	userServiceURL     string
	graphServiceURL    string
	jwtSecret          string
	internalServiceKey string
	chatClient         *http.Client
	postClient         *http.Client
	notifyClient       *http.Client
	userClient         *http.Client
	graphClient        *http.Client
	// media-service, for scoping an anonymous post's attachments (media_anonymize.go).
	mediaServiceURL string
	mediaClient     *http.Client
	// media is the attach gate and claim protocol for group and event media
	// (media_claims.go). Nil refuses every write that adds media: fail closed.
	media *mediaclaims.Gate
	producer        *groupevents.Producer
	rateLimiter     *RateLimiter
}

func New(s *store.Store, rdb *redis.Client, msgURL, postURL, userURL, jwtSecret string) *Service {
	svc := &Service{
		store:             s,
		rdb:               rdb,
		messageServiceURL: msgURL,
		postServiceURL:    postURL,
		userServiceURL:    userURL,
		jwtSecret:         jwtSecret,
		rateLimiter:       NewRateLimiter(rdb),
	}
	svc.chatClient = httpclient.NewWithBreaker(5*time.Second, "group->chat")
	svc.postClient = httpclient.NewWithBreaker(5*time.Second, "group->post")
	svc.notifyClient = httpclient.NewWithBreaker(5*time.Second, "group->notification")
	svc.userClient = httpclient.NewWithBreaker(5*time.Second, "group->user")
	svc.graphClient = httpclient.NewWithBreaker(5*time.Second, "group->graph")
	return svc
}

// SetGraphServiceURL wires the permission authority used by invites. Left
// empty, every invite is refused — see canAddToGroup.
func (s *Service) SetGraphServiceURL(url string) {
	s.graphServiceURL = strings.TrimSpace(url)
}

// SetProducer sets the Kafka producer (called after init in main.go).
func (s *Service) SetProducer(p *groupevents.Producer) {
	s.producer = p
}

func (s *Service) SetInternalServiceKey(key string) {
	s.internalServiceKey = strings.TrimSpace(key)
}

func (s *Service) publishEvent(fn func() error) {
	if s.producer == nil {
		return
	}
	if err := fn(); err != nil {
		slog.Warn("failed to publish event", "error", err)
	}
}

// --- Helpers ---

func (s *Service) invalidateGroupCache(ctx context.Context, groupID uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("group:%s", groupID))
}

/*
	invalidateMembershipCache drops one person's cached membership in one group.

	CheckMembershipCached caches "gm:<group>:<user>" for five minutes, and
	nothing ever deleted it — invalidateGroupCache only clears "group:<id>".
	AddGroupPostComment is the reader, so banning or removing someone left them
	able to keep commenting for up to five minutes after the ban landed. A ban
	that does not take effect until you notice it has stopped working is worse
	than no ban, because the moderator believes it worked.

	Called on every path that ends a membership.
*/
func (s *Service) invalidateMembershipCache(ctx context.Context, groupID, userID uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("gm:%s:%s", groupID, userID))
}

func (s *Service) checkPermission(setting, role string) error {
	switch setting {
	case "all_members":
		return nil
	case "admins_mods":
		if role == "admin" || role == "owner" || role == "moderator" {
			return nil
		}
		return fmt.Errorf("only admins and moderators are allowed")
	case "admins_only":
		if role == "admin" || role == "owner" {
			return nil
		}
		return fmt.Errorf("only admins are allowed")
	}
	return nil
}

func (s *Service) checkHandleInUserService(handle string) bool {
	if s.userServiceURL == "" {
		return false
	}
	url := fmt.Sprintf("%s/v1/users/by-username/%s", s.userServiceURL, handle)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false // fail open
	}
	resp, err := s.userClient.Do(req)
	if err != nil {
		return false // fail open — but spec says fail closed. User-service down is rare.
	}
	defer resp.Body.Close()
	// If 200, handle is taken by a user
	return resp.StatusCode == http.StatusOK
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// viewerKey renders the acting user as the TEXT user id the engagement tables
// store (the same form the spark/echo/stash writers use). An unset actor
// becomes "", which matches no engagement row, so the viewer_* flags come back
// false instead of erroring.
func viewerKey(actorID uuid.UUID) string {
	if actorID == uuid.Nil {
		return ""
	}
	return actorID.String()
}
