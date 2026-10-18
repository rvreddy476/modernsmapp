package config

import "testing"

func TestProductionWebsocketPolicyFailsClosed(t *testing.T) {
	valid := Config{AllowedOrigins: []string{"https://atpost.com"}}
	if err := valid.ValidateProduction(true); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}

	cases := map[string]Config{
		"query token":     {AllowedOrigins: []string{"https://atpost.com"}, WSAllowQueryToken: true},
		"missing origins": {},
		"wildcard origin": {AllowedOrigins: []string{"*"}},
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if candidate.ValidateProduction(true) == nil {
				t.Fatal("unsafe production websocket config accepted")
			}
		})
	}
}

// Chat parity §4: conversation frames and presence fan-out default ON; the
// broader scoped-rooms gate is not something this config can turn on.
func TestConversationFramesAndPresenceDefaultOn(t *testing.T) {
	t.Setenv("WS_CONVERSATION_FRAMES_ENABLED", "")
	t.Setenv("WS_PRESENCE_FANOUT_ENABLED", "")
	cfg := Load()
	if !cfg.ConversationFramesEnabled || !cfg.PresenceFanoutEnabled {
		t.Fatalf("defaults must be ON: frames=%v fanout=%v", cfg.ConversationFramesEnabled, cfg.PresenceFanoutEnabled)
	}
	t.Setenv("WS_CONVERSATION_FRAMES_ENABLED", "false")
	if Load().ConversationFramesEnabled {
		t.Fatal("WS_CONVERSATION_FRAMES_ENABLED=false must turn the frames off")
	}
}
