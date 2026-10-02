package service

import (
	"context"
	"net/http"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

// HandlerTestDeps are the in-memory stores and seams the Creator Hub
// handler tests (internal/http) drive the real router with. Every field is
// optional; an unset store leaves its flows failing closed exactly as an
// unwired production Service would.
type HandlerTestDeps struct {
	PostEdits         postEditStore
	PrivateShares     privateShareStore
	Authoring         videoAuthoringStore
	BirthDates        birthDateSource
	ReadGate          func(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) error
	BulkDelete        func(ctx context.Context, postID, callerID uuid.UUID) error
	ProfileServiceURL string
	Now               func() time.Time

	// End screens and cards (2026-09-29): the store, the channel store the
	// subscribe / channel elements read, the per-day dedupe, and PostCard in
	// place of viewablePostCard (nil = not this viewer's to open). The
	// series stores back the episode-list drops.
	EndScreens  endScreenStore
	Channels    channelStore
	StatDedupe  statDeduper
	PostCard    func(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) *RelatedPostCard
	VideoSeries videoSeriesStore
	FlickSeries flickSeriesStore
	// GraphServiceURL / AnalyticsServiceURL point the privacy gate and the
	// view counts at httptest fakes.
	GraphServiceURL     string
	AnalyticsServiceURL string
	// MediaAccess backs /v1/internal/media-access (media_access.go); nil
	// leaves the gate unresolved (503), never allowed.
	MediaAccess mediaAccessStore
	// Channel feed (channel_feed.go): the store, the media-record source in
	// place of media-service, and the hidden-author list the account gate
	// reads.
	ChannelFeed   channelFeedStore
	FeedMedia     feedMediaSource
	HiddenAuthors hiddenAuthorsStore
	// Original sounds (sounds.go): the reads, the media state of a listed
	// page, its counts in place of Scylla, and media-service itself, which
	// the tests stand up as an httptest server (MediaServiceURL, reached
	// with InternalServiceKey).
	SoundReads         soundStore
	SoundAudience      func(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]bool, error)
	MediaStates        mediaStateStore
	SoundCounts        func(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error)
	MediaServiceURL    string
	InternalServiceKey string
	// Offline copies (offline_copies.go): the store, the relationship graph
	// the playback decision reads, the caption-track list in place of
	// media-service and the members-only answer in place of monetization.
	Offline            offlineStore
	Relationships      GraphRelationships
	OfflineCaptions    offlineCaptionSource
	OfflineEntitlement func(ctx context.Context, viewerID uuid.UUID, p *postgres.Post) (bool, error)
}

// NewForHandlerTests builds a Service over HandlerTestDeps, with no
// Postgres, Scylla or Redis behind it. For tests only; main never calls it.
func NewForHandlerTests(d HandlerTestDeps) *Service {
	s := &Service{
		postEdits:           d.PostEdits,
		privateShare:        d.PrivateShares,
		birthDates:          d.BirthDates,
		readGate:            d.ReadGate,
		bulkDelete:          d.BulkDelete,
		profileServiceURL:   d.ProfileServiceURL,
		now:                 d.Now,
		httpClient:          &http.Client{Timeout: 5 * time.Second},
		endScreens:          d.EndScreens,
		channels:            d.Channels,
		statDedupe:          d.StatDedupe,
		postCardSeam:        d.PostCard,
		videoSeries:         d.VideoSeries,
		flickSeries:         d.FlickSeries,
		graphServiceURL:     d.GraphServiceURL,
		analyticsServiceURL: d.AnalyticsServiceURL,
		mediaAccess:         d.MediaAccess,
		channelFeed:         d.ChannelFeed,
		feedMedia:           d.FeedMedia,
		soundReads:          d.SoundReads,
		soundAudience:       d.SoundAudience,
		mediaStates:         d.MediaStates,
		soundCounts:         d.SoundCounts,
		mediaServiceURL:     d.MediaServiceURL,
		internalServiceKey:  d.InternalServiceKey,

		offline:              d.Offline,
		offlineCaptionTracks: d.OfflineCaptions,
		offlineEntitlement:   d.OfflineEntitlement,
	}
	if d.Relationships != nil {
		s.storyAudience = NewStoryAudience(d.Relationships)
	}
	s.hiddenAuthors = d.HiddenAuthors
	s.authoringOwners = d.Authoring
	return s
}
