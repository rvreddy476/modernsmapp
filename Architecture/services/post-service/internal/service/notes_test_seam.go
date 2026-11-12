package service

import (
	"context"

	"github.com/google/uuid"
)

// NoteTestDeps are the Echo seams the service and handler tests drive the
// note paths with (internal/notetest has the in-memory stand-ins). For
// tests only; main never calls WithNoteTestDeps.
type NoteTestDeps struct {
	Config    NoteConfig
	Store     noteStore
	Limiter   noteRateLimiter
	Relations noteRelations
	Authors   func(ctx context.Context, viewerID *uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*NoteAuthor, error)
	Standing  StandingChecker
	// EmitEvents writes the outbox events with no Kafka producer.
	EmitEvents bool
}

// WithNoteTestDeps installs d on s and returns s.
func (s *Service) WithNoteTestDeps(d NoteTestDeps) *Service {
	s.SetNoteConfig(d.Config)
	s.noteStoreSeam = d.Store
	s.noteLimiter = d.Limiter
	s.noteRelSeam = d.Relations
	s.noteAuthorsSeam = d.Authors
	s.noteEmitEvents = d.EmitEvents
	if d.Standing != nil {
		s.standing = d.Standing
	}
	return s
}
