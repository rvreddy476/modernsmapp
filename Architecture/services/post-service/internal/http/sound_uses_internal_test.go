package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /v1/internal/sound-uses (trending sounds, 2026-10-24): the wire
// media-service's ranking worker reads. The counting rules are proved in
// service/sound_uses_test.go.

type fakeSoundUsesReader struct {
	page  *service.SoundUsesPage
	err   error
	after uuid.UUID
	limit int
}

func (f *fakeSoundUsesReader) SoundUses(_ context.Context, after uuid.UUID, limit int) (*service.SoundUsesPage, error) {
	f.after, f.limit = after, limit
	return f.page, f.err
}

func soundUsesRequest(t *testing.T, reader soundUsesReader, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/internal/sound-uses", func(c *gin.Context) { serveSoundUses(c, reader) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/internal/sound-uses"+query, nil))
	return rec
}

func TestSoundUsesRouteAnswersThePage(t *testing.T) {
	sound, next, after := uuid.New(), uuid.New(), uuid.New()
	asOf := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	daily := make([]int, service.SoundUsesDays)
	daily[0] = 4
	reader := &fakeSoundUsesReader{page: &service.SoundUsesPage{
		AsOf: asOf, Days: service.SoundUsesDays, NextAfter: &next,
		Sounds: []service.SoundDailyUses{{AudioTrackID: sound, DailyUses: daily}},
	}}
	rec := soundUsesRequest(t, reader, "?after="+after.String()+"&limit=200")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if reader.after != after || reader.limit != 200 {
		t.Fatalf("reader got after=%v limit=%d", reader.after, reader.limit)
	}
	var body struct {
		AsOf   time.Time `json:"as_of"`
		Days   int       `json:"days"`
		Sounds []struct {
			AudioTrackID uuid.UUID `json:"audio_track_id"`
			DailyUses    []int     `json:"daily_uses"`
		} `json:"sounds"`
		NextAfter *uuid.UUID `json:"next_after"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.AsOf.Equal(asOf) || body.Days != 14 || len(body.Sounds) != 1 ||
		body.Sounds[0].AudioTrackID != sound || body.Sounds[0].DailyUses[0] != 4 ||
		body.NextAfter == nil || *body.NextAfter != next {
		t.Fatalf("body = %s", rec.Body)
	}

	// The first page names no cursor.
	soundUsesRequest(t, reader, "")
	if reader.after != uuid.Nil || reader.limit != 0 {
		t.Fatalf("first page: after=%v limit=%d", reader.after, reader.limit)
	}
}

func TestSoundUsesRouteRefusals(t *testing.T) {
	ok := &fakeSoundUsesReader{page: &service.SoundUsesPage{}}
	for _, tc := range []struct {
		name   string
		reader soundUsesReader
		query  string
		status int
		code   string
	}{
		{"bad cursor", ok, "?after=nope", http.StatusBadRequest, "invalid_after"},
		{"bad limit", ok, "?limit=x", http.StatusBadRequest, "invalid_limit"},
		{"zero limit", ok, "?limit=0", http.StatusBadRequest, "invalid_limit"},
		{"no service", nil, "", http.StatusServiceUnavailable, "unresolved"},
		{"unresolved", &fakeSoundUsesReader{page: &service.SoundUsesPage{}, err: errors.New("graph down")}, "", http.StatusServiceUnavailable, "unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := soundUsesRequest(t, tc.reader, tc.query)
			var body struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != tc.status || body.Error != tc.code {
				t.Fatalf("status %d body %s, want %d %s", rec.Code, rec.Body, tc.status, tc.code)
			}
		})
	}
}
