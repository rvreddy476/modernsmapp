package service

import (
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// A reaction toggle announces a NEW reaction and a removed one — never a
// change of type, which adds nothing to the post.
func TestReactionEvent(t *testing.T) {
	post, author, reactor := uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		name     string
		toggle   postgres.ReactionToggle
		wantType string
		wantKind string
	}{
		{"new reaction", postgres.ReactionToggle{NewType: "like", IsSet: true}, events.PostReacted, "like"},
		{"removed", postgres.ReactionToggle{PrevType: "love"}, events.PostUnreacted, "love"},
		{"type switch", postgres.ReactionToggle{NewType: "love", PrevType: "like", IsSet: true}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eventType, payload, ok := reactionEvent(tc.toggle, post, author, reactor)
			if tc.wantType == "" {
				if ok {
					t.Fatalf("announced %s for a change of type", eventType)
				}
				return
			}
			if !ok || eventType != tc.wantType {
				t.Fatalf("event = %q, ok=%v; want %q", eventType, ok, tc.wantType)
			}
			p := payload.(events.PostReactedPayload)
			if p.ReactType != tc.wantKind || p.PostID != post.String() || p.ReactorID != reactor.String() || p.PostAuthorID != author.String() {
				t.Fatalf("payload = %+v", p)
			}
		})
	}
}
