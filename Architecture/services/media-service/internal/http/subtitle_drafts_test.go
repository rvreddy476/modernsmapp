package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Draft captions on the viewer-facing reads (MTube, 2026-09-27).
//
// A generated caption is a draft (published=false) until its creator
// publishes it from /v1/subtitles/mine. The asset gate lets a viewer read
// the asset's captions; the draft rule then keeps unpublished tracks to the
// owner. For anyone else a draft does not exist: absent from the list, and
// its .vtt is the same 404 as a language that was never made.

func draftFixture(owner uuid.UUID) *fakeClipsService {
	return &fakeClipsService{
		owner: owner,
		subtitles: []postgres.MediaSubtitle{
			{Language: "en", Source: "manual", Content: "published transcript", Published: true},
			{Language: "hi", Source: "auto_generated", Content: "draft transcript", Published: false},
		},
		vtt: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\ntrack body\n",
	}
}

func listedLanguages(t *testing.T, body []byte) []string {
	t.Helper()
	var env struct {
		Data struct {
			Subtitles []postgres.MediaSubtitle `json:"subtitles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("not the envelope: %v\n%s", err, body)
	}
	out := make([]string, len(env.Data.Subtitles))
	for i, s := range env.Data.Subtitles {
		out[i] = s.Language
	}
	return out
}

func TestDraftCaptionTrackIsNotFoundForAStranger(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	media := uuid.New().String()

	for name, viewer := range map[string]string{"stranger": stranger.String(), "anonymous": ""} {
		t.Run(name, func(t *testing.T) {
			fake := draftFixture(owner)
			rec := captionGet(captionRouter(fake), "/v1/subtitles/"+media+"/track/hi.vtt", viewer)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("a draft track was served to a %s: %d %s", name, rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "track body") {
				t.Fatalf("the draft body leaked: %s", rec.Body.String())
			}
			// The published track next to it is still served.
			if ok := captionGet(captionRouter(fake), "/v1/subtitles/"+media+"/track/en.vtt", viewer); ok.Code != http.StatusOK {
				t.Fatalf("the published track was refused: %d", ok.Code)
			}
		})
	}

	t.Run("owner", func(t *testing.T) {
		fake := draftFixture(owner)
		rec := captionGet(captionRouter(fake), "/v1/subtitles/"+media+"/track/hi.vtt", owner.String())
		if rec.Code != http.StatusOK || rec.Body.String() != fake.vtt {
			t.Fatalf("the owner could not read their draft: %d %s", rec.Code, rec.Body.String())
		}
		if len(fake.readAs) != 1 || fake.readAs[0] != owner {
			t.Fatalf("the draft rule was asked about %v, want the request's viewer", fake.readAs)
		}
	})
}

func TestDraftCaptionsAreListedToTheOwnerOnly(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	path := "/v1/subtitles/" + uuid.New().String()

	cases := map[string]struct {
		viewer string
		want   string
	}{
		"owner":     {owner.String(), "en,hi"},
		"stranger":  {stranger.String(), "en"},
		"anonymous": {"", "en"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := captionGet(captionRouter(draftFixture(owner)), path, tc.viewer)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if got := strings.Join(listedLanguages(t, rec.Body.Bytes()), ","); got != tc.want {
				t.Fatalf("listed %q want %q", got, tc.want)
			}
			if tc.want == "en" && strings.Contains(rec.Body.String(), "draft transcript") {
				t.Fatalf("a draft transcript reached a %s: %s", name, rec.Body.String())
			}
		})
	}
}
