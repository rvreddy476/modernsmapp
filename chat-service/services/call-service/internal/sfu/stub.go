package sfu

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// StubProvider is a no-op SFU for local development and testing.
// It returns deterministic room names and configurable ICE servers for the
// current direct-WebRTC mobile client.
type StubProvider struct {
	iceServers []ICEServer
}

func NewStubProvider() *StubProvider {
	return NewStubProviderWithICEServers(nil)
}

func NewStubProviderWithICEServers(iceServers []ICEServer) *StubProvider {
	if len(iceServers) == 0 {
		iceServers = []ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
			{URLs: []string{"stun:stun1.l.google.com:19302"}},
		}
	}

	cloned := make([]ICEServer, 0, len(iceServers))
	for _, server := range iceServers {
		urls := append([]string(nil), server.URLs...)
		cloned = append(cloned, ICEServer{
			URLs:       urls,
			Username:   server.Username,
			Credential: server.Credential,
		})
	}

	return &StubProvider{iceServers: cloned}
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
	cloned := make([]ICEServer, 0, len(s.iceServers))
	for _, server := range s.iceServers {
		urls := append([]string(nil), server.URLs...)
		cloned = append(cloned, ICEServer{
			URLs:       urls,
			Username:   server.Username,
			Credential: server.Credential,
		})
	}
	return cloned
}

func (s *StubProvider) ClientURL() string {
	return ""
}

func (s *StubProvider) ProviderName() string {
	return "stub"
}
