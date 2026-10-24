package http

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// who_can_mention / who_can_tag ride the same settings read and PUT shape as
// every other who_can_* field (2026-10-24).
func TestSettingsMentionTagRoundTrip(t *testing.T) {
	id := uuid.New()
	row := defaultRow(id)
	r := memSettingsRouter(t, row, nil)

	code, env := do(t, r, http.MethodGet, "", id)
	var got map[string]interface{}
	_ = json.Unmarshal(env["data"], &got)
	if code != http.StatusOK || got["who_can_mention"] != "everyone" || got["who_can_tag"] != "everyone" {
		t.Fatalf("GET defaults: %d %v", code, got)
	}

	code, env = do(t, r, http.MethodPut, `{"who_can_mention":"following","who_can_tag":"no_one"}`, id)
	if code != http.StatusOK {
		t.Fatalf("PUT status %d: %s", code, env["error"])
	}
	if row.WhoCanMention != "following" || row.WhoCanTag != "no_one" {
		t.Fatalf("PUT did not store: mention=%q tag=%q", row.WhoCanMention, row.WhoCanTag)
	}

	// A partial PUT leaves both untouched.
	do(t, r, http.MethodPut, `{"chat_bubble_style":"mono"}`, id)
	if row.WhoCanMention != "following" || row.WhoCanTag != "no_one" {
		t.Fatalf("partial PUT clobbered mention/tag: %q %q", row.WhoCanMention, row.WhoCanTag)
	}
	// Only who_can_tag changes when only it is sent.
	do(t, r, http.MethodPut, `{"who_can_tag":"everyone"}`, id)
	if row.WhoCanMention != "following" || row.WhoCanTag != "everyone" {
		t.Fatalf("single-field PUT: mention=%q tag=%q", row.WhoCanMention, row.WhoCanTag)
	}

	code, env = do(t, r, http.MethodGet, "", id)
	got = nil
	_ = json.Unmarshal(env["data"], &got)
	if code != http.StatusOK || got["who_can_mention"] != "following" || got["who_can_tag"] != "everyone" {
		t.Fatalf("GET after PUT: %d %v", code, got)
	}
}
