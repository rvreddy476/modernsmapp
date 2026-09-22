package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/atpost/chat-call-service/internal/sfu"
	"github.com/google/uuid"
)

// nilICEProvider mirrors the LiveKit provider's behaviour: no ICE servers.
type nilICEProvider struct{ sfu.SFUProvider }

func (nilICEProvider) GetICEServers() []sfu.ICEServer { return nil }

type fakeTURN struct {
	servers []sfu.ICEServer
	err     error
}

func (f fakeTURN) ICEServers(context.Context) ([]sfu.ICEServer, error) { return f.servers, f.err }

func relay(url string) []sfu.ICEServer {
	return []sfu.ICEServer{{URLs: []string{url}, Username: "u", Credential: "c"}}
}

func urlOf(t *testing.T, servers []sfu.ICEServer) string {
	t.Helper()
	if len(servers) == 0 || len(servers[0].URLs) == 0 {
		t.Fatalf("no servers returned")
	}
	return servers[0].URLs[0]
}

// The LiveKit provider answers nil. Before this chain existed, a join under
// that provider handed out nothing — ICE_SERVERS_JSON was only ever read by
// the stub provider — so no call had a relay.
func TestICEFallsBackToStaticWhenProviderHasNone(t *testing.T) {
	s := &Service{sfuProvider: nilICEProvider{}, log: slog.Default()}
	s.WithStaticICEServers(relay("turn:lan:3478"))
	if got := urlOf(t, s.iceServersForJoin(context.Background(), uuid.New())); got != "turn:lan:3478" {
		t.Fatalf("want static relay, got %q", got)
	}
}

func TestICEPrefersManagedTURN(t *testing.T) {
	s := &Service{sfuProvider: nilICEProvider{}, log: slog.Default()}
	s.WithStaticICEServers(relay("turn:lan:3478"))
	s.WithTURNCredentials(fakeTURN{servers: relay("turn:turn.cloudflare.com:3478")})
	if got := urlOf(t, s.iceServersForJoin(context.Background(), uuid.New())); got != "turn:turn.cloudflare.com:3478" {
		t.Fatalf("want managed relay first, got %q", got)
	}
}

func TestICEFallsBackWhenMintFails(t *testing.T) {
	s := &Service{sfuProvider: nilICEProvider{}, log: slog.Default()}
	s.WithStaticICEServers(relay("turn:lan:3478"))
	s.WithTURNCredentials(fakeTURN{err: errors.New("cloudflare down")})
	if got := urlOf(t, s.iceServersForJoin(context.Background(), uuid.New())); got != "turn:lan:3478" {
		t.Fatalf("a mint failure must fall through to the static relay, got %q", got)
	}
}

func TestICEProviderListBeatsStatic(t *testing.T) {
	provider := sfu.NewStubProviderWithICEServers(relay("turn:provider:3478"))
	s := &Service{sfuProvider: provider, log: slog.Default()}
	s.WithStaticICEServers(relay("turn:lan:3478"))
	if got := urlOf(t, s.iceServersForJoin(context.Background(), uuid.New())); got != "turn:provider:3478" {
		t.Fatalf("a provider that has servers wins over static, got %q", got)
	}
}
