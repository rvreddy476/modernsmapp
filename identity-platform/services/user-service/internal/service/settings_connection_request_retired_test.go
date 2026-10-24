package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atpost/identity-user-service/internal/store"
)

// A settings cache entry written by a build from before 24 Oct 2026 still
// carries who_can_send_connection_request. It must decode, validate and be
// saved without the key — and the key must not come back out.
func TestCachedRowWithRetiredConnectionRequestKey(t *testing.T) {
	raw, err := json.Marshal(validSettings())
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(raw), `{`, `{"who_can_send_connection_request":"friends_of_friends_or_contacts",`, 1)

	var s store.UserSettings
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		t.Fatalf("an old cache entry no longer decodes: %v", err)
	}
	if err := prepareSettings(&s); err != nil {
		t.Fatalf("an old cache entry no longer validates: %v", err)
	}
	out, err := json.Marshal(&s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "who_can_send_connection_request") {
		t.Fatalf("the retired key is still serialised: %s", out)
	}
}
