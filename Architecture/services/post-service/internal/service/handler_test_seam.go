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
}

// NewForHandlerTests builds a Service over HandlerTestDeps, with no
// Postgres, Scylla or Redis behind it. For tests only; main never calls it.
func NewForHandlerTests(d HandlerTestDeps) *Service {
	s := &Service{
		postEdits:         d.PostEdits,
		privateShare:      d.PrivateShares,
		birthDates:        d.BirthDates,
		readGate:          d.ReadGate,
		bulkDelete:        d.BulkDelete,
		profileServiceURL: d.ProfileServiceURL,
		now:               d.Now,
		httpClient:        &http.Client{Timeout: 5 * time.Second},
	}
	s.authoringOwners = d.Authoring
	return s
}
