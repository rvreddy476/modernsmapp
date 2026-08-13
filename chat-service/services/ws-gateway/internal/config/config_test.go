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
