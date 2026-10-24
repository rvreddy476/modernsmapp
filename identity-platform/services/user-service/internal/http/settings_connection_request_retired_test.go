package http

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// Connections are retired (24 Oct 2026): who_can_send_connection_request is
// no longer part of the settings contract. A read never returns it, and a PUT
// from an older app that still sends it — with any value — succeeds and
// applies the rest of the body as if the key were absent.

const retiredSettingKey = "who_can_send_connection_request"

func settingsData(t *testing.T, env map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var data map[string]json.RawMessage
	if err := json.Unmarshal(env["data"], &data); err != nil {
		t.Fatalf("decode data: %v (%s)", err, env["data"])
	}
	return data
}

func TestRetiredConnectionRequestSettingIsAbsentOnRead(t *testing.T) {
	id := uuid.New()
	r := memSettingsRouter(t, defaultRow(id), nil)
	code, env := do(t, r, http.MethodGet, "", id)
	if code != http.StatusOK {
		t.Fatalf("GET status %d", code)
	}
	data := settingsData(t, env)
	if _, ok := data[retiredSettingKey]; ok {
		t.Fatalf("GET still returns %s: %s", retiredSettingKey, env["data"])
	}
	// The live keys are still there (the read shape did not collapse).
	for _, k := range []string{"who_can_message", "who_can_call", "who_can_add_to_groups"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("GET lost %s: %s", k, env["data"])
		}
	}
}

func TestRetiredConnectionRequestSettingIsIgnoredOnPut(t *testing.T) {
	for _, value := range []string{`"everyone"`, `"no_one"`, `"not-a-real-value"`, `null`, `42`} {
		t.Run(value, func(t *testing.T) {
			id := uuid.New()
			row := defaultRow(id)
			before := *row
			r := memSettingsRouter(t, row, nil)

			code, env := do(t, r, http.MethodPut, `{"`+retiredSettingKey+`":`+value+`,"chat_bubble_style":"ocean"}`, id)
			if code != http.StatusOK {
				t.Fatalf("PUT with the retired key = %d (%s), want 200", code, env["error"])
			}
			if _, ok := settingsData(t, env)[retiredSettingKey]; ok {
				t.Fatalf("PUT response returns %s: %s", retiredSettingKey, env["data"])
			}
			// The rest of the body applied; nothing else moved.
			if row.ChatBubbleStyle != "ocean" {
				t.Fatalf("chat_bubble_style = %q, want ocean", row.ChatBubbleStyle)
			}
			row.ChatBubbleStyle = before.ChatBubbleStyle
			if !reflect.DeepEqual(*row, before) {
				t.Fatalf("the retired key changed the stored row:\n got %+v\nwant %+v", *row, before)
			}
		})
	}
}
