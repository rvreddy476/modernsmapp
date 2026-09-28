package service

import (
	"context"
	"net/http"
	"time"

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
	}
	s.authoringOwners = d.Authoring
	return s
}
