package sfu

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/livekit/protocol/auth"
	livekit "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// LiveKitProvider provisions rooms and access tokens against a LiveKit deployment.
type LiveKitProvider struct {
	roomClient *lksdk.RoomServiceClient
	apiKey     string
	apiSecret  string
	apiURL     string
	clientURL  string
}

func NewLiveKitProvider(hostURL, apiKey, apiSecret string) (*LiveKitProvider, error) {
	if strings.TrimSpace(hostURL) == "" {
		return nil, fmt.Errorf("livekit host is required")
	}
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(apiSecret) == "" {
		return nil, fmt.Errorf("livekit api credentials are required")
	}

	apiURL, clientURL, err := normalizeLiveKitURLs(hostURL)
	if err != nil {
		return nil, err
	}

	return &LiveKitProvider{
		roomClient: lksdk.NewRoomServiceClient(apiURL, apiKey, apiSecret),
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		apiURL:     apiURL,
		clientURL:  clientURL,
	}, nil
}

func (p *LiveKitProvider) CreateRoom(ctx context.Context, roomKey string, maxParticipants int) (string, error) {
	req := &livekit.CreateRoomRequest{
		Name:            roomKey,
		MaxParticipants: uint32(maxParticipants),
		EmptyTimeout:    60,
	}

	room, err := p.roomClient.CreateRoom(ctx, req)
	if err != nil {
		return "", fmt.Errorf("create livekit room: %w", err)
	}

	if room.GetName() == "" {
		return roomKey, nil
	}
	return room.GetName(), nil
}

func (p *LiveKitProvider) GenerateToken(_ context.Context, roomName string, userID string, canPublish bool) (string, error) {
	token := auth.NewAccessToken(p.apiKey, p.apiSecret)
	token.SetIdentity(userID).SetValidFor(time.Hour)
	token.SetVideoGrant(&auth.VideoGrant{
		RoomJoin:     true,
		Room:         roomName,
		CanPublish:   boolPtr(canPublish),
		CanSubscribe: boolPtr(true),
	})

	jwt, err := token.ToJWT()
	if err != nil {
		return "", fmt.Errorf("generate livekit access token: %w", err)
	}
	return jwt, nil
}

func (p *LiveKitProvider) CloseRoom(ctx context.Context, roomName string) error {
	_, err := p.roomClient.DeleteRoom(ctx, &livekit.DeleteRoomRequest{
		Room: roomName,
	})
	if err != nil {
		return fmt.Errorf("delete livekit room: %w", err)
	}
	return nil
}

func (p *LiveKitProvider) GetICEServers() []ICEServer {
	return nil
}

func (p *LiveKitProvider) ClientURL() string {
	return p.clientURL
}

func (p *LiveKitProvider) ProviderName() string {
	return "livekit"
}

func normalizeLiveKitURLs(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf("livekit host is required")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("parse livekit host: %w", err)
	}
	if parsed.Host == "" {
		return "", "", fmt.Errorf("invalid livekit host")
	}

	apiURL := *parsed
	clientURL := *parsed

	switch parsed.Scheme {
	case "https":
		clientURL.Scheme = "wss"
	case "http":
		clientURL.Scheme = "ws"
	case "wss":
		apiURL.Scheme = "https"
	case "ws":
		apiURL.Scheme = "http"
	default:
		return "", "", fmt.Errorf("unsupported livekit scheme %q", parsed.Scheme)
	}

	return strings.TrimRight(apiURL.String(), "/"), strings.TrimRight(clientURL.String(), "/"), nil
}

func boolPtr(value bool) *bool {
	v := value
	return &v
}
