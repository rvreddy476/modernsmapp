package service

import (
	"context"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"time"
)

type automaticBookingStore interface {
	MatchAutomatic(context.Context, uuid.UUID, time.Time) error
	AutomaticQueue(context.Context, int) ([]uuid.UUID, error)
	DeclineAutomatic(context.Context, store.AcceptInput) error
}

func (s *Service) dispatchAutomatic(ctx context.Context, id uuid.UUID) error {
	st, ok := s.ds.Store.(automaticBookingStore)
	if !ok {
		return store.ErrInvalid
	}
	return st.MatchAutomatic(ctx, id, s.nowUTC())
}
