package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A channel's picture and banner pass the channel-update media gate: the
// actor's own confirmed upload, not refused by moderation, with the same
// errors (and so the same wire codes) as an update's media.
func TestChannelMediaGate(t *testing.T) {
	actor := uuid.New()
	own := asset(actor, "ready", "passed")
	foreign := asset(uuid.New(), "ready", "passed")
	pending := asset(actor, "pending_upload", "pending")
	rejected := asset(actor, "ready", "rejected")
	s, f := svcWith(own, foreign, pending, rejected)
	ctx := context.Background()

	if err := s.verifyChannelMedia(ctx, actor, []uuid.UUID{own.ID}, []uuid.UUID{own.ID}); err != nil {
		t.Fatalf("own ready picture and banner = %v", err)
	}
	for name, tc := range map[string]struct {
		avatar, banner []uuid.UUID
		want           error
	}{
		"someone else's picture": {[]uuid.UUID{foreign.ID}, nil, ErrMediaNotOwned},
		"someone else's banner":  {nil, []uuid.UUID{foreign.ID}, ErrMediaNotOwned},
		"unfinished banner":      {[]uuid.UUID{own.ID}, []uuid.UUID{pending.ID}, ErrMediaNotReady},
		"rejected picture":       {[]uuid.UUID{rejected.ID}, nil, ErrMediaNotReady},
		"missing banner":         {nil, []uuid.UUID{uuid.New()}, ErrMediaNotFound},
		"nil picture":            {[]uuid.UUID{uuid.Nil}, nil, ErrMediaNotFound},
	} {
		if err := s.verifyChannelMedia(ctx, actor, tc.avatar, tc.banner); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}

	// Nothing added: media-service is never asked, even without a client.
	f.calls = nil
	if err := (&Service{}).verifyChannelMedia(ctx, actor, nil, nil); err != nil {
		t.Fatalf("a save adding no media = %v", err)
	}
	// Something added and no client: fail closed.
	if err := (&Service{}).verifyChannelMedia(ctx, actor, []uuid.UUID{own.ID}, nil); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("no client = %v, want ErrMediaUnavailable", err)
	}
}

// CreateChannel and UpdateChannel need a database, so the wiring is pinned on
// the source (media_claims_integration_test.go proves it in CI): the gate runs
// on what the save adds, with the right actor, before anything is claimed or
// written.
func TestChannelWritersRunTheMediaGateBeforeClaiming(t *testing.T) {
	raw, err := os.ReadFile("channel.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, tc := range []struct{ sig, gate string }{
		{"func (s *Service) CreateChannel(", "if err := s.verifyChannelMedia(ctx, ownerID, avatarAdd, bannerAdd); err != nil {\n\t\treturn nil, err\n\t}"},
		{"func (s *Service) UpdateChannel(", "if err := s.verifyChannelMedia(ctx, actorID, avatarAdd, bannerAdd); err != nil {\n\t\treturn nil, err\n\t}"},
	} {
		body, ok := funcBody(src, tc.sig)
		if !ok {
			t.Fatalf("%s not found", tc.sig)
		}
		g, w := strings.Index(body, tc.gate), strings.Index(body, "s.writeWithClaims(")
		if g < 0 {
			t.Errorf("%s does not run the media gate (%q)", tc.sig, tc.gate)
			continue
		}
		if w < 0 || g > w {
			t.Errorf("%s claims or writes before the media gate", tc.sig)
		}
	}
}
