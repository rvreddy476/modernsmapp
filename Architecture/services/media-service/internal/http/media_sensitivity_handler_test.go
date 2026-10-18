package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// Chat parity pass 2: the keyed GET /v1/media/:id carries `sensitive`
// (true/false/null) and `moderation_labels` (names only) for message-service;
// a viewer's GET of the same record carries neither.

func serviceData(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s want 200", rec.Code, rec.Body.String())
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return env.Data
}

func (f *recordFixture) setLabels(id uuid.UUID, scanner, labels string) {
	m := f.store.media[id]
	m.ModerationScanner = scanner
	if labels == "" {
		m.ModerationLabels = nil
	} else {
		m.ModerationLabels = []byte(labels)
	}
}

func TestMediaInfoServiceCallerGetsSensitivity(t *testing.T) {
	f := newRecordFixture(t)
	cases := []struct {
		name, scanner, labels string
		wantSensitive         string // raw JSON
		wantLabels            []string
	}{
		{"sexual", "rekognition", `[{"name":"Revealing Clothes","parent":"Suggestive","confidence":91.2},{"name":"Suggestive","confidence":91.2}]`, "true", []string{"Revealing Clothes", "Suggestive"}},
		{"violent", "rekognition", `[{"name":"Weapons","parent":"Violence","confidence":88}]`, "true", []string{"Weapons"}},
		{"harmless finding", "rekognition", `[{"name":"Alcohol","confidence":95}]`, "false", []string{"Alcohol"}},
		{"clean scan", "rekognition", `[]`, "false", nil},
		{"never scanned", "", "", "null", nil},
		{"stub", "stub", `[]`, "null", nil},
	}
	for _, tc := range cases {
		f.setLabels(f.privateID, tc.scanner, tc.labels)
		data := serviceData(t, f.info(f.privateID, uuid.Nil, internalKeyHeader, recordInternalKey))
		raw, present := data["sensitive"]
		if !present {
			t.Fatalf("%s: keyed record has no `sensitive` field: %v", tc.name, data)
		}
		if string(raw) != tc.wantSensitive {
			t.Errorf("%s: sensitive = %s, want %s", tc.name, raw, tc.wantSensitive)
		}
		var labels []string
		if l, ok := data["moderation_labels"]; ok {
			if err := json.Unmarshal(l, &labels); err != nil {
				t.Fatalf("%s: moderation_labels is not a list of names: %s", tc.name, l)
			}
		}
		if !reflect.DeepEqual(labels, tc.wantLabels) {
			t.Errorf("%s: moderation_labels = %v, want %v", tc.name, labels, tc.wantLabels)
		}
		// The record every caller reads is still there.
		if string(data["moderation_status"]) != `"passed"` || string(data["id"]) != `"`+f.privateID.String()+`"` {
			t.Errorf("%s: base record fields missing: %v", tc.name, data)
		}
	}
}

// Viewers — the owner, a permitted viewer, anonymous on public media — never
// get the moderation summary. Serving serviceMediaRecordOf on the viewer path
// fails this.
func TestMediaInfoViewersGetNoSensitivity(t *testing.T) {
	f := newRecordFixture(t)
	for _, id := range []uuid.UUID{f.privateID, f.publicID} {
		f.setLabels(id, "rekognition", `[{"name":"Suggestive","confidence":91}]`)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"owner":            f.info(f.privateID, f.owner),
		"permitted viewer": f.info(f.privateID, f.viewer),
		"anonymous public": f.info(f.publicID, uuid.Nil),
		"wrong key public": f.info(f.publicID, uuid.Nil, internalKeyHeader, "not-the-key"),
	} {
		data := serviceData(t, rec)
		for _, field := range []string{"sensitive", "moderation_labels", "moderation_scanner"} {
			if _, ok := data[field]; ok {
				t.Errorf("%s: viewer record carries %q: %s", name, field, rec.Body.String())
			}
		}
	}
}
