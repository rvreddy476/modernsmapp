// Tests for the SSE event-id encode/decode round-trip and the Redis
// envelope extractor. These are the only two functions on the path
// the client uses for Last-Event-ID replay — getting them right is
// the difference between a clean reconnect and silently missing
// notifications.

package http

import (
	"fmt"
	"testing"

	"github.com/gocql/gocql"
)

func TestParseEventID(t *testing.T) {
	validTS := gocql.TimeUUID()
	validBucket := 202605

	cases := []struct {
		name       string
		input      string
		wantOK     bool
		wantBucket int
		wantTS     gocql.UUID
	}{
		{
			name:       "valid",
			input:      fmt.Sprintf("%d:%s", validBucket, validTS.String()),
			wantOK:     true,
			wantBucket: validBucket,
			wantTS:     validTS,
		},
		{
			name:   "empty",
			input:  "",
			wantOK: false,
		},
		{
			name:   "no separator",
			input:  "202605abcd",
			wantOK: false,
		},
		{
			name:   "separator at end",
			input:  "202605:",
			wantOK: false,
		},
		{
			name:   "separator at start",
			input:  ":" + validTS.String(),
			wantOK: false,
		},
		{
			name:   "bucket not int",
			input:  "abc:" + validTS.String(),
			wantOK: false,
		},
		{
			name:   "ts not uuid",
			input:  "202605:not-a-uuid",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bucket, ts, ok := parseEventID(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if bucket != tc.wantBucket {
				t.Errorf("bucket = %d, want %d", bucket, tc.wantBucket)
			}
			if ts != tc.wantTS {
				t.Errorf("ts = %s, want %s", ts.String(), tc.wantTS.String())
			}
		})
	}
}

func TestExtractEventID(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "valid envelope",
			payload: `{"type":"notification","payload":{"bucket":202605,"ts":"abc-123"}}`,
			want:    "202605:abc-123",
		},
		{
			name:    "missing payload",
			payload: `{"type":"notification"}`,
			want:    "",
		},
		{
			name:    "missing bucket",
			payload: `{"type":"notification","payload":{"ts":"abc"}}`,
			want:    "",
		},
		{
			name:    "missing ts",
			payload: `{"type":"notification","payload":{"bucket":202605}}`,
			want:    "",
		},
		{
			name:    "bucket zero",
			payload: `{"type":"notification","payload":{"bucket":0,"ts":"abc"}}`,
			want:    "",
		},
		{
			name:    "garbage json",
			payload: `{not valid`,
			want:    "",
		},
		{
			name:    "empty",
			payload: ``,
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractEventID(tc.payload)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Round-trip safety: when extractEventID produces an id, parseEventID
// must accept it. This catches accidental drift in the encoding between
// the publisher (processor.go) and the consumer (StreamNotifications).
func TestEventIDRoundTrip(t *testing.T) {
	ts := gocql.TimeUUID()
	bucket := 202612
	payload := fmt.Sprintf(
		`{"type":"notification","payload":{"bucket":%d,"ts":"%s"}}`,
		bucket, ts.String(),
	)
	id := extractEventID(payload)
	if id == "" {
		t.Fatal("extractEventID returned empty for valid payload")
	}
	gotBucket, gotTS, ok := parseEventID(id)
	if !ok {
		t.Fatalf("parseEventID rejected its own output: %s", id)
	}
	if gotBucket != bucket {
		t.Errorf("bucket round-trip: got %d, want %d", gotBucket, bucket)
	}
	if gotTS != ts {
		t.Errorf("ts round-trip: got %s, want %s", gotTS.String(), ts.String())
	}
}
