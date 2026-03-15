package sfu

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// StubProvider is a no-op SFU for local development and testing.
// It returns deterministic room names and static STUN servers.
type StubProvider struct{}

func NewStubProvider() *StubProvider {
	return &StubProvider{}
}

func (s *StubProvider) CreateRoom(_ context.Context, roomKey string, _ int) (string, error) {
	return fmt.Sprintf("stub-room-%s", roomKey), nil
}

func (s *StubProvider) GenerateToken(_ context.Context, roomName string, userID string, _ bool) (string, error) {
	return fmt.Sprintf("stub-token-%s-%s-%s", roomName, userID, uuid.New().String()[:8]), nil
}

func (s *StubProvider) CloseRoom(_ context.Context, _ string) error {
	return nil
}

func (s *StubProvider) GetICEServers() []ICEServer {
	return []ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		{URLs: []string{"stun:stun1.l.google.com:19302"}},
	}
}

func (s *StubProvider) ProviderName() string {
	return "stub"
}
