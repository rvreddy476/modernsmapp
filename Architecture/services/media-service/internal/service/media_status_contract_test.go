package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// The readiness contract a composer client polls — Slice C, C-LB-2 / C-LB-6.
//
// ## WHY THIS EXISTS
//
// An asset is attachable only at EXACT `processing_status=ready` AND
// `moderation_status=passed`; post-service refuses anything else. The Android
// composer therefore polls this endpoint and waits for both.
//
// `moderation_status` was missing from this response. The C-LB-8 live run is
// what found it: `confirm` returned the verdict, this endpoint did not, so a
// client that polled correctly waited for a field that never arrived — every
// image post would exhaust its poll budget and fail. Every unit test on both
// sides passed, because each one's fake supplied the field the real endpoint
// omitted.
//
// That is the whole reason this test asserts the SERIALISED JSON rather than
// the struct: the defect was in what went on the wire.
func TestMediaStatusResponseCarriesTheModerationVerdict(t *testing.T) {
	body, err := json.Marshal(&MediaStatusResponse{
		MediaID:          uuid.New(),
		ProcessingStatus: "ready",
		ModerationStatus: "passed",
		FileType:         "image",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, field := range []string{"media_id", "processing_status", "moderation_status", "file_type"} {
		if _, ok := wire[field]; !ok {
			t.Fatalf("%q is missing from the status response: %s", field, body)
		}
	}

	if wire["moderation_status"] != "passed" {
		t.Fatalf("moderation_status is %v, want \"passed\": %s", wire["moderation_status"], body)
	}
}

// The verdict must be sent even when it is `pending`.
//
// `omitempty` here would be worse than omitting the field outright: the client
// would see it on approved assets and not on unapproved ones, so "absent" would
// silently mean "not yet approved" — a distinction nothing on the wire states
// and no client should have to infer.
func TestPendingModerationIsStillReported(t *testing.T) {
	body, err := json.Marshal(&MediaStatusResponse{
		MediaID:          uuid.New(),
		ProcessingStatus: "processing",
		ModerationStatus: "pending",
		FileType:         "image",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got, ok := wire["moderation_status"]; !ok || got != "pending" {
		t.Fatalf("a pending verdict must still be reported, got %v: %s", got, body)
	}
}
