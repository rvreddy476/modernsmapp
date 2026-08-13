package config

import "testing"

func TestCallsDefaultOff(t *testing.T) {
	t.Setenv("CALLS_ENABLED", "")
	if Load().CallsEnabled {
		t.Fatal("calls must default off until device/network proof is approved")
	}
}

func TestEnabledCallsRequireEverySafetyDependency(t *testing.T) {
	base := Config{
		CallsEnabled: true, GraphServiceURL: "http://graph", InternalServiceKey: "key",
		LiveKitHost: "wss://livekit", LiveKitAPIKey: "api", LiveKitAPISecret: "secret",
		ICEServersJSON: `[{"urls":["turn:turn.example.com:3478"]}]`,
	}
	if err := base.ValidateCallEnablement(); err != nil {
		t.Fatalf("complete configuration rejected: %v", err)
	}
	cases := map[string]func(*Config){
		"graph":            func(c *Config) { c.GraphServiceURL = "" },
		"internal key":     func(c *Config) { c.InternalServiceKey = "" },
		"livekit host":     func(c *Config) { c.LiveKitHost = "" },
		"livekit key":      func(c *Config) { c.LiveKitAPIKey = "" },
		"livekit secret":   func(c *Config) { c.LiveKitAPISecret = "" },
		"turn config":      func(c *Config) { c.ICEServersJSON = "" },
		"stun only":        func(c *Config) { c.ICEServersJSON = `[{"urls":["stun:stun.example.com:3478"]}]` },
		"invalid ice json": func(c *Config) { c.ICEServersJSON = `not-json` },
	}
	for name, remove := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := base
			remove(&candidate)
			if candidate.ValidateCallEnablement() == nil {
				t.Fatal("unsafe enabled-call configuration accepted")
			}
		})
	}
}
