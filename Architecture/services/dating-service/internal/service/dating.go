// Package service is the domain orchestration layer for dating-service.
//
// Service holds the kafka producer (write paths emit dating.profile.* events
// per spec §11) and, as of Sprint 2, the matcher.GraphProvider used by Pulse.
package service

import (
	"net/http"
	"sync"
	"time"

	"github.com/atpost/dating-service/internal/digilocker"
	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/matcher"
	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/httpclient"
	"github.com/redis/go-redis/v9"
)

// Service exposes high-level operations to the http layer.
type Service struct {
	store                *store.Store
	rdb                  *redis.Client
	producer             *datingevents.Producer
	graphProvider        matcher.GraphProvider
	msgClient            MessageServiceClient
	digilockerClient     digilocker.Client
	mediaClient          MediaServiceClient
	graphServiceClient   GraphServiceClient
	communityClient      CommunityServiceClient
	flagsClient          FeatureFlagsClient
	moderationLLM        ModerationLLMClient
	// Sprint 5 — premium + DPDP wiring.
	razorpay             payments.Client
	consentPolicyVersion string
	dataExportPublisher  DataExportPublisher
	notificationClient   NotificationClient
	storageClient        ExportStorageClient

	// Sprint 6 — moderation-strict feature-flag cache (60s TTL). The mutex
	// is held for the cache read/write only; flag fetches happen outside.
	strictMu       sync.RWMutex
	strictCacheVal bool
	strictCacheAt  time.Time

	// H1 (arch review): shared HTTP client with timeout + circuit breaker
	// for the best-effort block-propagation call to graph-service. Avoids
	// the http.DefaultClient leak that hangs goroutines on a slow graph.
	graphHTTPClient *http.Client
}

// New builds a Service. The producer + graphProvider are set later in main.go.
func New(s *store.Store, rdb *redis.Client) *Service {
	return &Service{
		store:           s,
		rdb:             rdb,
		graphHTTPClient: httpclient.NewWithBreaker(5*time.Second, "dating->graph"),
	}
}

// SetProducer wires the Kafka producer for emit-on-write paths.
func (s *Service) SetProducer(p *datingevents.Producer) {
	s.producer = p
}

// Store exposes the underlying Store for handlers that need raw queries.
func (s *Service) Store() *store.Store {
	return s.store
}
