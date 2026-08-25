package http

import (
	"encoding/json"
	"testing"
)

// The canonical create fingerprint — C-P0-5.
//
// The defect being corrected: the old fingerprint hashed six fields, so the
// same actor could reuse one `Idempotency-Key` with a materially different body
// and have the ORIGINAL post replayed while being told it succeeded. Every case
// below is a body difference that MUST change the fingerprint, and each one was
// invisible to the old hash.

func fingerprintOrFail(t *testing.T, req CreatePostRequest) string {
	t.Helper()
	fp, err := createFingerprint(req)
	if err != nil {
		t.Fatalf("canonicalise: %v", err)
	}
	return fp
}

func baseRequest() CreatePostRequest {
	return CreatePostRequest{
		Text:         "hello",
		Visibility:   "public",
		ContentType:  "post",
		PostType:     "text",
		AppOrigin:    "postbook",
		Language:     "en",
		Distribution: json.RawMessage(`{"version":1,"main_feed":true,"notify_subscribers":false}`),
	}
}

// Identical intent must hash identically, or every retry is a spurious 409.
func TestCanonicalFingerprintIsStable(t *testing.T) {
	if fingerprintOrFail(t, baseRequest()) != fingerprintOrFail(t, baseRequest()) {
		t.Fatal("identical requests must fingerprint identically")
	}
}

// Each of these was IGNORED by the previous six-field hash.
func TestCanonicalFingerprintCoversFieldsTheOldHashMissed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CreatePostRequest)
	}{
		{"app_origin", func(r *CreatePostRequest) { r.AppOrigin = "posttube" }},
		{"distribution policy", func(r *CreatePostRequest) {
			r.Distribution = json.RawMessage(`{"version":1,"main_feed":true,"notify_subscribers":true}`)
		}},
		{"comments disabled", func(r *CreatePostRequest) { r.NoComments = true }},
		{"likes disabled", func(r *CreatePostRequest) { r.NoLikes = true }},
		{"location name", func(r *CreatePostRequest) { s := "Bengaluru"; r.LocationName = &s }},
		{"location latitude", func(r *CreatePostRequest) { f := 12.97; r.LocationLat = &f }},
		{"feeling", func(r *CreatePostRequest) { s := "happy"; r.Feeling = &s }},
		{"tags", func(r *CreatePostRequest) { r.Tags = []string{"kannada"} }},
		{"rich text", func(r *CreatePostRequest) { r.RichText = json.RawMessage(`{"blocks":[]}`) }},
		{"title", func(r *CreatePostRequest) { r.Title = "a title" }},
		{"category", func(r *CreatePostRequest) { r.Category = "music" }},
		{"paid promotion", func(r *CreatePostRequest) { r.PaidPromotion = true }},
		{"made for kids", func(r *CreatePostRequest) { r.IsMadeForKids = true }},
		{"altered content", func(r *CreatePostRequest) { r.AlteredContent = true }},
		// The sharpest case: this side effect happens AFTER the idempotent
		// transaction, so a fingerprint that ignores it lets a retry that adds
		// audio be swallowed as a replay of a post that has none.
		{"audio track", func(r *CreatePostRequest) { s := "a-track-id"; r.AudioTrackID = &s }},
		{"legacy publish_to_feed", func(r *CreatePostRequest) { b := false; r.PublishToFeed = &b }},
		{"legacy share_to_postbook", func(r *CreatePostRequest) { b := false; r.ShareToPostbook = &b }},
		{"cover media", func(r *CreatePostRequest) { s := "cover-id"; r.CoverMediaID = &s }},
		{"comment access", func(r *CreatePostRequest) { r.CommentAccess = "followers" }},
	}

	base := fingerprintOrFail(t, baseRequest())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseRequest()
			tc.mutate(&req)
			if fingerprintOrFail(t, req) == base {
				t.Errorf("changing %s did not change the fingerprint: the same "+
					"idempotency key would replay the ORIGINAL post and the "+
					"client would be told it succeeded", tc.name)
			}
		})
	}
}

// The six fields the old hash DID cover must still be covered.
func TestCanonicalFingerprintStillCoversTheOriginalFields(t *testing.T) {
	base := fingerprintOrFail(t, baseRequest())

	for _, tc := range []struct {
		name   string
		mutate func(*CreatePostRequest)
	}{
		{"text", func(r *CreatePostRequest) { r.Text = "hello, edited" }},
		{"visibility", func(r *CreatePostRequest) { r.Visibility = "followers" }},
		{"content type", func(r *CreatePostRequest) { r.ContentType = "flick" }},
		{"post type", func(r *CreatePostRequest) { r.PostType = "image" }},
		{"language", func(r *CreatePostRequest) { r.Language = "hi" }},
		{"media ids", func(r *CreatePostRequest) { r.MediaIDs = []string{"m1"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := baseRequest()
			tc.mutate(&req)
			if fingerprintOrFail(t, req) == base {
				t.Errorf("changing %s did not change the fingerprint", tc.name)
			}
		})
	}
}

// Media ORDER matters: [a,b] and [b,a] are different posts.
func TestCanonicalFingerprintRespectsMediaOrder(t *testing.T) {
	a := baseRequest()
	a.MediaIDs = []string{"m1", "m2"}
	b := baseRequest()
	b.MediaIDs = []string{"m2", "m1"}

	if fingerprintOrFail(t, a) == fingerprintOrFail(t, b) {
		t.Error("media order must be part of the fingerprint")
	}
}

// Whitespace inside a raw-JSON member is not a content difference.
//
// Without compaction, a client that pretty-printed its distribution policy on
// the retry would get a 409 for a byte difference that means nothing.
func TestCanonicalFingerprintIgnoresRawJsonWhitespace(t *testing.T) {
	compact := baseRequest()
	compact.Distribution = json.RawMessage(`{"version":1,"main_feed":true}`)

	spaced := baseRequest()
	spaced.Distribution = json.RawMessage("{ \"version\" : 1,\n  \"main_feed\" : true }")

	if fingerprintOrFail(t, compact) != fingerprintOrFail(t, spaced) {
		t.Error("insignificant whitespace must not change the fingerprint")
	}
}

// An absent raw member and an empty one are the same absence.
func TestCanonicalFingerprintTreatsEmptyRawAsAbsent(t *testing.T) {
	absent := baseRequest()
	absent.RichText = nil

	empty := baseRequest()
	empty.RichText = json.RawMessage("   ")

	if fingerprintOrFail(t, absent) != fingerprintOrFail(t, empty) {
		t.Error("an omitted and a blank raw member must fingerprint identically")
	}
}

// Malformed raw JSON is refused rather than silently hashed as bytes.
//
// Hashing unparseable bytes would bind a key to something the server cannot
// interpret, so a later identical-meaning request could not match it.
func TestCanonicalFingerprintRefusesMalformedRawJson(t *testing.T) {
	req := baseRequest()
	req.RichText = json.RawMessage(`{"blocks":`)

	if _, err := createFingerprint(req); err == nil {
		t.Fatal("malformed rich_text must fail canonicalisation, not be hashed as bytes")
	}
}
