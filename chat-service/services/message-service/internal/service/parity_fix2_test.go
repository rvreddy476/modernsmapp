package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// Chat parity fix pass 2 (lane M) guard tests.

// A Giphy sticker previews as "Sticker"; a plain gif still as "GIF".
func TestGiphyStickerPreview(t *testing.T) {
	sticker, _ := encodeExtra(&MessageExtra{GIF: &GIFInfo{Provider: "giphy", ID: "a", IsSticker: true}})
	plain, _ := encodeExtra(&MessageExtra{GIF: &GIFInfo{Provider: "giphy", ID: "a"}})
	if got := messagePreviewWithExtra(MessageTypeGIF, "", string(sticker)); got != StickerPreview {
		t.Fatalf("giphy sticker preview = %q", got)
	}
	if got := messagePreviewWithExtra(MessageTypeGIF, "", string(plain)); got != GIFPreview {
		t.Fatalf("gif preview = %q", got)
	}
	if got := messagePreviewWithExtra(MessageTypeText, "hi", string(sticker)); got != "hi" {
		t.Fatalf("text preview = %q", got)
	}
}

// is_sticker round-trips through the stored payload and is always on the
// wire; an old stored gif reads as is_sticker false.
func TestGIFIsStickerStoredAndReturned(t *testing.T) {
	raw, _ := encodeExtra(&MessageExtra{GIF: &GIFInfo{Provider: "giphy", ID: "a", URL: "https://i.giphy.com/a.gif", Width: 1, Height: 1, IsSticker: true}})
	var resp MessageResponse
	applyExtra(&resp, decodeExtra(string(raw)))
	if resp.GIF == nil || !resp.GIF.IsSticker {
		t.Fatalf("is_sticker lost in storage: %+v", resp.GIF)
	}
	old := decodeExtra(`{"gif":{"provider":"giphy","id":"a","url":"https://i.giphy.com/a.gif","width":1,"height":1}}`)
	out, _ := json.Marshal(old.GIF)
	if !strings.Contains(string(out), `"is_sticker":false`) {
		t.Fatalf("is_sticker must always be on the wire: %s", out)
	}
}

// The idempotency hash of a non-sticker gif send is the same as before
// is_sticker existed, so a key retried across the deploy still matches.
func TestGIFHashUnchangedForPlainGIFs(t *testing.T) {
	g := &GIFRef{Provider: "giphy", ID: "a", URL: "https://i.giphy.com/a.gif", Width: 2, Height: 3}
	before := struct {
		GIF *struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
			URL      string `json:"url"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			StillURL string `json:"still_url,omitempty"`
		} `json:"gif,omitempty"`
	}{GIF: &struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
		URL      string `json:"url"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		StillURL string `json:"still_url,omitempty"`
	}{g.Provider, g.ID, g.URL, g.Width, g.Height, g.StillURL}}
	after := struct {
		GIF *gifHashRef `json:"gif,omitempty"`
	}{GIF: gifHashOf(g)}
	h1, _ := hashRequestPayload(before)
	h2, _ := hashRequestPayload(after)
	if h1 != h2 {
		t.Fatal("a plain gif's request hash changed")
	}
	g.IsSticker = true
	h3, _ := hashRequestPayload(struct {
		GIF *gifHashRef `json:"gif,omitempty"`
	}{GIF: gifHashOf(g)})
	if h3 == h2 {
		t.Fatal("a sticker and a plain gif must hash differently")
	}
}

// media-service's data.sensitive is read defensively: absent or null stays
// null (never false), true and false map straight through.
func TestFetchMediaInfoReadsSensitive(t *testing.T) {
	bodies := map[string]string{
		"absent": `{"data":{"file_type":"image","width":10,"height":10}}`,
		"null":   `{"data":{"file_type":"image","sensitive":null}}`,
		"true":   `{"data":{"file_type":"video","sensitive":true}}`,
		"false":  `{"data":{"file_type":"image","sensitive":false}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(bodies[r.Header.Get("X-Case")]))
	}))
	defer srv.Close()
	for name, want := range map[string]*bool{"absent": nil, "null": nil, "true": ptrBool(true), "false": ptrBool(false)} {
		s := &Service{mediaServiceURL: srv.URL, internalServiceKey: "k", httpClient: caseClient(srv.Client(), name), log: slog.Default()}
		info := s.fetchMediaInfo(context.Background(), uuid.New())
		if info == nil {
			t.Fatalf("%s: no info", name)
		}
		if (want == nil) != (info.Sensitive == nil) || (want != nil && *want != *info.Sensitive) {
			t.Fatalf("%s: sensitive = %v, want %v", name, info.Sensitive, want)
		}
	}
}

// A media message stored with sensitive null is completed from
// media-service at read time; audio and settled verdicts are not asked
// again, and the lookups per page are bounded.
func TestFillMediaSensitivityBackfills(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"data":{"file_type":"image","width":4,"height":5,"sensitive":true}}`))
	}))
	defer srv.Close()
	s := &Service{mediaServiceURL: srv.URL, internalServiceKey: "k", httpClient: srv.Client(), log: slog.Default()}
	unknown, settled, bare, audio := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	no := false
	msgs := []MessageResponse{
		{Type: MessageTypeMedia, MediaID: &unknown, Media: &MediaInfo{Kind: "image"}},
		{Type: MessageTypeMedia, MediaID: &settled, Media: &MediaInfo{Kind: "image", Sensitive: &no}},
		{Type: MessageTypeMedia, MediaID: &bare},
		{Type: MessageTypeMedia, MediaID: &audio, Media: &MediaInfo{Kind: "audio"}},
		{Type: MessageTypeText},
	}
	shared := msgs[0].Media
	s.fillMediaSensitivity(context.Background(), msgs)
	if msgs[0].Media.Sensitive == nil || !*msgs[0].Media.Sensitive {
		t.Fatalf("unknown verdict not backfilled: %+v", msgs[0].Media)
	}
	if shared.Sensitive != nil {
		t.Fatal("backfill must copy, not mutate the decoded payload")
	}
	if *msgs[1].Media.Sensitive {
		t.Fatal("a settled verdict was overwritten")
	}
	if msgs[2].Media == nil || msgs[2].Media.Kind != "image" || msgs[2].Media.Sensitive == nil {
		t.Fatalf("missing metadata not backfilled: %+v", msgs[2].Media)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("media-service calls = %d, want 2 (unknown + bare only)", got)
	}

	calls.Store(0)
	many := make([]MessageResponse, mediaInfoBackfillCap+7)
	for i := range many {
		id := uuid.New()
		many[i] = MessageResponse{Type: MessageTypeMedia, MediaID: &id}
	}
	s.fillMediaSensitivity(context.Background(), many)
	if got := calls.Load(); got != mediaInfoBackfillCap {
		t.Fatalf("lookups per page = %d, want the cap %d", got, mediaInfoBackfillCap)
	}
}

func ptrBool(b bool) *bool { return &b }

type caseTransport struct {
	base http.RoundTripper
	name string
}

func (c caseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Case", c.name)
	return c.base.RoundTrip(r)
}

func caseClient(base *http.Client, name string) *http.Client {
	return &http.Client{Transport: caseTransport{base: base.Transport, name: name}}
}
