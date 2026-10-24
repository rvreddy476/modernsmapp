package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The two wires the trending ranking reads (trending_sounds_clients.go).
// Everything the contract does not say is an error, never "no uses" or
// "not blocked".

func TestHTTPSoundUsesReadsThePage(t *testing.T) {
	sound, next, after := uuid.New(), uuid.New(), uuid.New()
	var gotQuery, gotKey, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotKey = r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Internal-Service-Key")
		fmt.Fprintf(w, `{"as_of":"2026-10-24T12:00:00Z","days":14,"sounds":[{"audio_track_id":%q,"daily_uses":[3,0,0,0,0,0,0,0,0,0,0,0,0,1]}],"next_after":%q}`, sound, next)
	}))
	defer srv.Close()
	page, err := NewHTTPSoundUses(srv.URL+"/", "k", nil).SoundUses(context.Background(), after)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/internal/sound-uses" || gotKey != "k" || !strings.Contains(gotQuery, "after="+after.String()) || !strings.Contains(gotQuery, "limit=1000") {
		t.Fatalf("asked %s?%s key %q", gotPath, gotQuery, gotKey)
	}
	if len(page.Sounds) != 1 || page.Sounds[0].AudioTrackID != sound || page.Sounds[0].DailyUses[13] != 1 ||
		page.NextAfter == nil || *page.NextAfter != next || page.AsOf.IsZero() {
		t.Fatalf("page %+v", page)
	}

	// The first page names no cursor.
	if _, err := NewHTTPSoundUses(srv.URL, "k", nil).SoundUses(context.Background(), uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotQuery, "after=") {
		t.Fatalf("first page sent %q", gotQuery)
	}
}

func TestHTTPSoundUsesRefusesWhatTheContractDoesNotSay(t *testing.T) {
	sound := uuid.New()
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"503":              {http.StatusServiceUnavailable, `{"error":"unresolved"}`},
		"503 with a page":  {http.StatusServiceUnavailable, `{"as_of":"2026-10-24T12:00:00Z","days":14,"sounds":[]}`},
		"not json":         {200, `<html>`},
		"no sounds":        {200, `{"as_of":"2026-10-24T12:00:00Z","days":14}`},
		"no as_of":         {200, `{"days":14,"sounds":[]}`},
		"seven days":       {200, `{"as_of":"2026-10-24T12:00:00Z","days":7,"sounds":[]}`},
		"short row":        {200, fmt.Sprintf(`{"as_of":"2026-10-24T12:00:00Z","days":14,"sounds":[{"audio_track_id":%q,"daily_uses":[1,2]}]}`, sound)},
		"row without id":   {200, `{"as_of":"2026-10-24T12:00:00Z","days":14,"sounds":[{"daily_uses":[0,0,0,0,0,0,0,0,0,0,0,0,0,0]}]}`},
		"enveloped (data)": {200, `{"data":{"as_of":"2026-10-24T12:00:00Z","days":14,"sounds":[]}}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			if _, err := NewHTTPSoundUses(srv.URL, "k", nil).SoundUses(context.Background(), uuid.Nil); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestHTTPBlockChecker(t *testing.T) {
	user, a, b := uuid.New(), uuid.New(), uuid.New()
	var got struct {
		UserID     uuid.UUID   `json:"user_id"`
		Candidates []uuid.UUID `json:"candidate_ids"`
	}
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/internal/graph/blocked-any" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		key = r.Header.Get("X-Internal-Service-Key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprintf(w, `{"blocked_user_ids":[%q]}`, b)
	}))
	defer srv.Close()
	blocked, err := NewHTTPBlockChecker(srv.URL, "k", nil).BlockedAny(context.Background(), user, []uuid.UUID{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if key != "k" || got.UserID != user || len(got.Candidates) != 2 || !blocked[b] || blocked[a] {
		t.Fatalf("key %q asked %+v answered %v", key, got, blocked)
	}

	for name, body := range map[string]string{"no list": `{}`, "not json": `nope`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		if _, err := NewHTTPBlockChecker(srv.URL, "k", nil).BlockedAny(context.Background(), user, []uuid.UUID{a}); err == nil {
			t.Errorf("%s: read as no blocks", name)
		}
		srv.Close()
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"blocked_user_ids":[]}`))
	}))
	defer down.Close()
	if _, err := NewHTTPBlockChecker(down.URL, "k", nil).BlockedAny(context.Background(), user, []uuid.UUID{a}); err == nil {
		t.Error("500 read as no blocks")
	}
}

// More candidates than graph-service takes in one call are asked in chunks.
func TestHTTPBlockCheckerChunks(t *testing.T) {
	calls, max := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			Candidates []uuid.UUID `json:"candidate_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		calls++
		if len(got.Candidates) > max {
			max = len(got.Candidates)
		}
		_, _ = w.Write([]byte(`{"blocked_user_ids":[]}`))
	}))
	defer srv.Close()
	ids := make([]uuid.UUID, blockedAnyCap+3)
	for i := range ids {
		ids[i] = uuid.New()
	}
	if _, err := NewHTTPBlockChecker(srv.URL, "k", nil).BlockedAny(context.Background(), uuid.New(), ids); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || max != blockedAnyCap {
		t.Fatalf("%d calls, largest %d", calls, max)
	}
}
