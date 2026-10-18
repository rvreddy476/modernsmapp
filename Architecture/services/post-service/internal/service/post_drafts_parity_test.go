package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Drafts carry everything the photo/text details screen sets (2026-10-18)
// and publish all of it: the payload used to reject these keys outright
// (DisallowUnknownFields), publish hard-coded post_type "text" and dropped
// alt_texts.

const draftMediaA = "44444444-4444-4444-8444-444444444444"
const draftMediaB = "45444444-4444-4444-8444-444444444444"

func fullDraftPayload() string {
	return `{
	  "text": "Sunday market #food @call.b",
	  "visibility": "friends",
	  "post_type": "image",
	  "media_ids": ["` + draftMediaA + `", "` + draftMediaB + `"],
	  "alt_texts": {"` + draftMediaA + `": "Stalls under string lights"},
	  "cover_media_id": "` + draftMediaB + `",
	  "cover_index": 1,
	  "hashtags": ["food", "market"],
	  "mentions": ["call.b"],
	  "remix_setting": "disallow",
	  "comment_access": "followers",
	  "link_url": "https://example.com/market",
	  "ai_generated": true,
	  "is_branded": true,
	  "paid_promotion": false,
	  "location_name": "Banjara Hills",
	  "location_place_id": "ChIJLbZ-NFv9DDkRQJY4FbcFcgM",
	  "location_lat": 17.41,
	  "location_lng": 78.44
	}`
}

func TestParseDraftPayload_AcceptsTheDetailsScreenKeys(t *testing.T) {
	p, err := parseDraftPayload(json.RawMessage(fullDraftPayload()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.PostType != "image" || p.RemixSetting != "disallow" || p.CommentAccess != "followers" ||
		p.LinkURL == nil || *p.LinkURL != "https://example.com/market" || !p.AIGenerated || !p.IsBranded ||
		p.CoverIndex == nil || *p.CoverIndex != 1 || p.LocationPlaceID == nil || len(p.Hashtags) != 2 || len(p.Mentions) != 1 {
		t.Fatalf("payload = %+v", p)
	}
	if err := validateDraft("post", p); err != nil {
		t.Fatalf("the full payload must validate: %v", err)
	}
	// Still strict: a key nobody declared is refused.
	if _, err := parseDraftPayload(json.RawMessage(`{"text":"x","nope":1}`)); !errors.Is(err, ErrInvalidDraft) {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestValidateDraft_RefusesBadDetailFields(t *testing.T) {
	cases := map[string]struct {
		patch string // replaces the payload's matching key
		want  error
	}{
		"visibility":       {`"visibility": "staged"`, ErrInvalidVisibility},
		"remix_setting":    {`"remix_setting": "deny"`, ErrInvalidRemixSetting},
		"comment_access":   {`"comment_access": "friends_only"`, ErrInvalidCommentAccess},
		"link_url":         {`"link_url": "javascript:alert(1)"`, ErrInvalidLinkURL},
		"location":         {`"location_place_id": "<x>"`, ErrInvalidLocationPlaceID},
		"cover off-strip":  {`"cover_media_id": "` + uuid.NewString() + `"`, ErrInvalidCover},
		"cover not an id":  {`"cover_media_id": "nope"`, ErrInvalidCover},
		"cover_index":      {`"cover_index": 5`, ErrInvalidCover},
		"hashtag alphabet": {`"hashtags": ["no spaces allowed"]`, ErrInvalidHashtag},
	}
	keyOf := func(kv string) string { return kv[:strings.Index(kv, ":")] }
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var lines []string
			for _, line := range strings.Split(fullDraftPayload(), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, keyOf(tc.patch)+":") {
					comma := strings.HasSuffix(trimmed, ",")
					line = "  " + tc.patch
					if comma {
						line += ","
					}
				}
				lines = append(lines, line)
			}
			p, err := parseDraftPayload(json.RawMessage(strings.Join(lines, "\n")))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if tc.patch == `"cover_index": 5` {
				p.CoverMediaID = nil
			}
			err = validateDraft("post", p)
			if !errors.Is(err, ErrInvalidDraft) || !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want ErrInvalidDraft and %v", err, tc.want)
			}
		})
	}
	// The payload post_type is the post's, not the draft row's kind.
	p, _ := parseDraftPayload(json.RawMessage(`{"text":"x","post_type":"video"}`))
	if err := validateDraft("post", p); !errors.Is(err, ErrInvalidDraft) {
		t.Fatalf("a photo/text draft claiming post_type video: %v", err)
	}
	if err := validateDraft("reel", p); err != nil {
		t.Fatalf("a reel draft may say video: %v", err)
	}
}

func TestBuildDraftCreateInput_CarriesEveryDetail(t *testing.T) {
	p, err := parseDraftPayload(json.RawMessage(fullDraftPayload()))
	if err != nil {
		t.Fatal(err)
	}
	d := &postgres.PostDraft{ID: uuid.New(), AuthorID: uuid.New(), PostType: "post"}
	in := buildDraftCreateInput(d, p)
	if in.PostID == nil || *in.PostID != d.ID || in.AuthorID != d.AuthorID {
		t.Fatalf("ids = %v %v", in.PostID, in.AuthorID)
	}
	if in.PostType != "image" {
		t.Errorf("post_type = %q, want image (it used to be hard-coded text)", in.PostType)
	}
	if in.Visibility != "friends" || in.RemixSetting != "disallow" || in.CommentAccess != "followers" ||
		in.LinkURL != "https://example.com/market" || !in.AIGenerated || !in.IsBranded ||
		in.CoverIndex == nil || *in.CoverIndex != 1 || in.CoverMediaID == nil || in.CoverMediaID.String() != draftMediaB ||
		in.LocationPlaceID == nil || *in.LocationPlaceID != "ChIJLbZ-NFv9DDkRQJY4FbcFcgM" ||
		len(in.Hashtags) != 2 || len(in.Mentions) != 1 || len(in.MediaIDs) != 2 {
		t.Fatalf("input = %+v", in)
	}
	// No audience saved = the composer's default, never "" (a CHECK failure).
	bare, _ := parseDraftPayload(json.RawMessage(`{"text":"x"}`))
	if got := buildDraftCreateInput(d, bare); got.Visibility != "public" || got.PostType != "text" || got.LinkURL != "" {
		t.Fatalf("bare draft = vis %q type %q link %q", got.Visibility, got.PostType, got.LinkURL)
	}
	// A reel draft still publishes as video whatever its payload says.
	reel := &postgres.PostDraft{ID: uuid.New(), AuthorID: uuid.New(), PostType: "reel"}
	if got := buildDraftCreateInput(reel, bare); got.PostType != "video" {
		t.Fatalf("reel draft post_type = %q", got.PostType)
	}
}

type recordingAltTexts struct {
	got map[uuid.UUID]string
	who []uuid.UUID
	err error
}

func (r *recordingAltTexts) SetAltText(_ context.Context, mediaID, uploaderID uuid.UUID, alt string) error {
	if r.got == nil {
		r.got = map[uuid.UUID]string{}
	}
	r.got[mediaID] = alt
	r.who = append(r.who, uploaderID)
	return r.err
}

func TestApplyDraftAltTexts_WritesOnlyTheDraftsOwnMedia(t *testing.T) {
	a, b := uuid.MustParse(draftMediaA), uuid.MustParse(draftMediaB)
	off := uuid.New()
	author := uuid.New()
	rec := &recordingAltTexts{}
	s := &Service{draftAltTexts: rec}
	s.applyDraftAltTexts(context.Background(), author, &PostDraftPayload{
		MediaIDs: []uuid.UUID{a, b},
		AltTexts: map[string]string{
			a.String():   "  Stalls under string lights ",
			b.String():   "   ",
			off.String(): "not on this draft",
		},
	})
	if len(rec.got) != 1 || rec.got[a] != "Stalls under string lights" {
		t.Fatalf("written = %v", rec.got)
	}
	for _, w := range rec.who {
		if w != author {
			t.Fatalf("wrote as %s, want the author", w)
		}
	}
	// Long descriptions are cut to the cap, never refused.
	rec = &recordingAltTexts{}
	s.draftAltTexts = rec
	s.applyDraftAltTexts(context.Background(), author, &PostDraftPayload{
		MediaIDs: []uuid.UUID{a}, AltTexts: map[string]string{a.String(): strings.Repeat("é", MaxDraftAltTextRunes+5)},
	})
	if n := len([]rune(rec.got[a])); n != MaxDraftAltTextRunes {
		t.Fatalf("alt text is %d runes, want %d", n, MaxDraftAltTextRunes)
	}
	// A failing write never panics or stops the publish.
	s.draftAltTexts = &recordingAltTexts{err: errors.New("down")}
	s.applyDraftAltTexts(context.Background(), author, &PostDraftPayload{MediaIDs: []uuid.UUID{a}, AltTexts: map[string]string{a.String(): "x"}})
}

func TestHTTPDraftAltTextWriter_CallsTheInternalRoute(t *testing.T) {
	media, author := uuid.New(), uuid.New()
	var gotPath, gotMethod, gotKey string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotKey = r.URL.Path, r.Method, r.Header.Get("X-Internal-Service-Key")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"data":{"status":"updated"}}`))
	}))
	defer srv.Close()
	s := &Service{mediaServiceURL: srv.URL, internalServiceKey: "k", httpClient: srv.Client()}
	if err := s.altTextWriter().SetAltText(context.Background(), media, author, "A red bicycle"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/v1/media/internal/"+media.String()+"/alt-text" || gotKey != "k" {
		t.Fatalf("called %s %s key %q", gotMethod, gotPath, gotKey)
	}
	if gotBody["uploader_id"] != author.String() || gotBody["alt_text"] != "A red bicycle" {
		t.Fatalf("body = %v", gotBody)
	}
	// No key, no call: the route refuses keyless callers anyway.
	if err := (&Service{mediaServiceURL: srv.URL}).altTextWriter().SetAltText(context.Background(), media, author, "x"); err == nil {
		t.Fatal("a keyless writer reported success")
	}
}
