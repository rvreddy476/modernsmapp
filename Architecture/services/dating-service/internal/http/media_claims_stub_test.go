package http

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// stubClaims is media-service's claim API for the HTTP tests: every claim
// and release succeeds, and the claims are kept so a test can look.
type stubClaims struct {
	mu   sync.Mutex
	held map[string]map[uuid.UUID]bool
}

func newStubClaims() *stubClaims { return &stubClaims{held: map[string]map[uuid.UUID]bool{}} }

func (s *stubClaims) Claim(_ context.Context, kind, ref string, ids []uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := kind + "|" + ref
	if s.held[k] == nil {
		s.held[k] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		s.held[k][id] = true
	}
	return nil
}

func (s *stubClaims) Release(_ context.Context, kind, ref string, ids []uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := kind + "|" + ref
	if len(ids) == 0 {
		delete(s.held, k)
		return nil
	}
	for _, id := range ids {
		delete(s.held[k], id)
	}
	return nil
}
