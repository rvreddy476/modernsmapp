package sfu

import "context"

// SFUProvider abstracts the media server (LiveKit, Janus, etc.).
type SFUProvider interface {
	// CreateRoom allocates a room on the SFU. Returns provider-specific room name.
	CreateRoom(ctx context.Context, roomKey string, maxParticipants int) (providerRoomName string, err error)

	// GenerateToken creates a short-lived token for a participant to join a room.
	GenerateToken(ctx context.Context, roomName string, userID string, canPublish bool) (token string, err error)

	// CloseRoom terminates a room on the SFU.
	CloseRoom(ctx context.Context, roomName string) error

	// GetICEServers returns STUN/TURN server configurations.
	GetICEServers() []ICEServer

	// ProviderName returns "livekit", "janus", or "stub".
	ProviderName() string
}

// ICEServer represents a STUN or TURN server configuration.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}
