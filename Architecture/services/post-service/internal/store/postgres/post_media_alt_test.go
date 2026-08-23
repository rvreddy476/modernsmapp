package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The accessibility fields a reader receives — Slice C, C-CLB-3.
//
// # WHY THIS IS A CONTRACT TEST AND NOT AN IMPLEMENTATION DETAIL
//
// The composer refuses to create an image post until the author either writes
// a description or explicitly marks the photo decorative. That requirement is
// only honest if the decision reaches a reader. It did not: post reads selected
// `media_id, kind` and stopped there, so a mandatory accessibility field was
// write-only for the entire life of the feature.
//
// These names are consumed by the Android `PostMediaDto`. Renaming one here
// silently breaks the renderer — kotlinx.serialization would simply fall back
// to the default and every image would go back to being unlabelled, with no
// error anywhere.
func TestPostMediaSerializesItsAccessibilityDecision(t *testing.T) {
	id := uuid.New()

	tests := []struct {
		name  string
		media PostMedia
		want  map[string]any
	}{
		{
			name: "a described image carries its description",
			media: PostMedia{
				MediaID: id, Kind: "image",
				AltText: "a cat asleep on a keyboard", AltDecorative: false,
			},
			want: map[string]any{
				"media_id":       id.String(),
				"kind":           "image",
				"alt_text":       "a cat asleep on a keyboard",
				"alt_decorative": false,
			},
		},
		{
			// The decorative flag must be delivered as its own value. Inferring
			// it from an empty description would conflate "marked as carrying
			// no information" with "nobody said", and only the first of those
			// may render an image with no label.
			name:  "a decorative image carries an explicit flag and no description",
			media: PostMedia{MediaID: id, Kind: "image", AltText: "", AltDecorative: true},
			want: map[string]any{
				"media_id":       id.String(),
				"kind":           "image",
				"alt_text":       "",
				"alt_decorative": true,
			},
		},
		{
			// Legacy rows predating the composer requirement. Neither field is
			// set, which the renderer must treat as unknown rather than as a
			// decorative mark.
			name:  "an undecided legacy asset carries neither",
			media: PostMedia{MediaID: id, Kind: "image"},
			want: map[string]any{
				"media_id":       id.String(),
				"kind":           "image",
				"alt_text":       "",
				"alt_decorative": false,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.media)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("field set changed: got %v, want %v", keysOf(got), keysOf(tc.want))
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("%s = %#v, want %#v", key, got[key], want)
				}
			}
		})
	}
}

// Both fields are always present, never omitempty.
//
// `omitempty` on either one would make an empty description and a false
// decorative flag vanish from the wire. The Android DTO would then be reading
// absent fields on exactly the rows where the distinction matters, and a
// strict decoder would fail outright.
func TestAccessibilityFieldsAreNeverOmitted(t *testing.T) {
	raw, err := json.Marshal(PostMedia{MediaID: uuid.New(), Kind: "image"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{`"alt_text"`, `"alt_decorative"`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("%s missing from %s; both fields must always be on the wire", field, raw)
		}
	}
}

// Every post-media read uses the one shared projection.
//
// The defect this closes existed because eight separate queries each selected
// `media_id, kind` independently, so there was no single place where "what a
// reader gets" was written down and no way to change it in one move. If the
// projection stops naming a field, every read loses it at once — which is
// visible here rather than in production.
func TestSharedProjectionNamesBothAccessibilityColumns(t *testing.T) {
	for _, column := range []string{"alt_text", "alt_decorative"} {
		if !strings.Contains(postMediaColumns, column) {
			t.Errorf("postMediaColumns does not select %s: %s", column, postMediaColumns)
		}
	}
	// LEFT JOIN, not INNER: post_media has an ON DELETE RESTRICT foreign key so
	// a missing asset should be impossible, but if one ever is, the image must
	// still be returned without its description rather than the post silently
	// losing its media.
	if !strings.Contains(postMediaSource, "LEFT JOIN media_assets") {
		t.Errorf("postMediaSource must LEFT JOIN media_assets, got: %s", postMediaSource)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
