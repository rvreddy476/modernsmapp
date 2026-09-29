package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The sound on a draft (2026-09-29): PATCH /v1/reels/drafts/:id removes it
// with an empty audio_track_id, leaves it alone when the field is absent,
// and refuses anything that is not a sound id; the composer draft publishes
// no sound for an empty one. The rows are proved in
// sounds_integration_test.go.

func TestDraftSoundUpdates(t *testing.T) {
	sound := uuid.New()
	str := func(v string) *string { return &v }
	num := func(v int) *int { return &v }
	for _, tc := range []struct {
		name  string
		input UpdateDraftInput
		want  map[string]interface{}
	}{
		{"absent leaves the sound as it is", UpdateDraftInput{}, map[string]interface{}{}},
		{"absent, with a start: the start alone moves", UpdateDraftInput{AudioStartMs: num(4000)},
			map[string]interface{}{"audio_start_ms": 4000}},
		{"empty removes the sound", UpdateDraftInput{AudioTrackID: str("")},
			map[string]interface{}{"audio_track_id": nil, "audio_start_ms": 0}},
		{"empty removes the start that came with it too", UpdateDraftInput{AudioTrackID: str(""), AudioStartMs: num(4000)},
			map[string]interface{}{"audio_track_id": nil, "audio_start_ms": 0}},
		{"blank is empty", UpdateDraftInput{AudioTrackID: str("   ")},
			map[string]interface{}{"audio_track_id": nil, "audio_start_ms": 0}},
		{"a sound id", UpdateDraftInput{AudioTrackID: str(sound.String())},
			map[string]interface{}{"audio_track_id": sound.String()}},
		{"a sound id and its start", UpdateDraftInput{AudioTrackID: str(" " + sound.String() + " "), AudioStartMs: num(1500)},
			map[string]interface{}{"audio_track_id": sound.String(), "audio_start_ms": 1500}},
		{"a negative start is 0", UpdateDraftInput{AudioStartMs: num(-20)},
			map[string]interface{}{"audio_start_ms": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]interface{}{}
			if err := draftSoundUpdates(&tc.input, got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("updates = %#v\nwant %#v", got, tc.want)
			}
			// Removing must WRITE the column: a key that is missing would
			// leave the sound in place.
			if tc.input.AudioTrackID != nil {
				if _, written := got["audio_track_id"]; !written {
					t.Fatal("audio_track_id is not among the columns written")
				}
			}
		})
	}
}

func TestDraftSoundUpdatesRefusesWhatIsNotASoundID(t *testing.T) {
	for _, raw := range []string{"not-a-sound", "1234", "null", "cccccccc-cccc-4ccc-8ccc", "' OR 1=1 --"} {
		updates := map[string]interface{}{}
		start := 9
		err := draftSoundUpdates(&UpdateDraftInput{AudioTrackID: &raw, AudioStartMs: &start}, updates)
		if !errors.Is(err, ErrInvalidDraftSound) {
			t.Fatalf("%q: got %v want %v", raw, err, ErrInvalidDraftSound)
		}
		if len(updates) != 0 {
			t.Fatalf("%q: a refused patch resolved columns to write: %v", raw, updates)
		}
	}

	// And the patch is refused before any store is touched: this Service
	// has none, so reaching it would panic.
	junk, caption := "not-a-sound", "a new caption"
	_, err := (&Service{}).UpdateDraft(context.Background(), uuid.New(), uuid.New(),
		&UpdateDraftInput{Caption: &caption, AudioTrackID: &junk})
	if !errors.Is(err, ErrInvalidDraftSound) {
		t.Fatalf("UpdateDraft: got %v want %v", err, ErrInvalidDraftSound)
	}
}

// The JSON the route binds: "" is a value, null and absent are not.
func TestDraftSoundPatchBinding(t *testing.T) {
	for body, wantSet := range map[string]bool{
		`{"audio_track_id":""}`:          true,
		`{"audio_track_id":null}`:        false,
		`{"caption":"no sound in here"}`: false,
	} {
		var input UpdateDraftInput
		if err := json.Unmarshal([]byte(body), &input); err != nil {
			t.Fatal(err)
		}
		if (input.AudioTrackID != nil) != wantSet {
			t.Fatalf("%s: audio_track_id bound = %v, want %v", body, input.AudioTrackID != nil, wantSet)
		}
		updates := map[string]interface{}{}
		if err := draftSoundUpdates(&input, updates); err != nil {
			t.Fatal(err)
		}
		if _, cleared := updates["audio_track_id"]; cleared != wantSet {
			t.Fatalf("%s: clears the sound = %v, want %v", body, cleared, wantSet)
		}
	}
}

// The composer draft: an empty audio_track_id is no sound at publication.
func TestComposerDraftSound(t *testing.T) {
	str := func(v string) *string { return &v }
	start := 1500

	for name, chosen := range map[string]*string{"absent": nil, "empty": str(""), "blank": str("  "), "not a sound id": str("not-a-sound")} {
		f := newAudioFixture()
		f.sound(&f.publicVideo, nil)
		post := &postgres.Post{ID: f.post, AuthorID: f.other}
		f.svc.keepComposerDraftSound(context.Background(), post, &PostDraftPayload{AudioTrackID: chosen, AudioStartMs: &start})
		if post.AudioTrackID != nil || post.AudioStartMs != nil {
			t.Fatalf("%s: the post answers a sound: %v", name, post.AudioTrackID)
		}
		f.wantNothingWritten(t, name)
	}

	f := newAudioFixture()
	sound := f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.DurationMs = 28400 })
	post := &postgres.Post{ID: f.post, AuthorID: f.other}
	f.svc.keepComposerDraftSound(context.Background(), post, &PostDraftPayload{AudioTrackID: str(sound.String()), AudioStartMs: &start})
	if f.store.attached[f.post] != sound || f.store.startMs[f.post] != 1500 || post.AudioTrackID == nil || *post.AudioTrackID != sound {
		t.Fatalf("attached %v start %v, the post answers %v", f.store.attached, f.store.startMs, post.AudioTrackID)
	}

	// No start in the payload is the start of the sound.
	g := newAudioFixture()
	again := g.sound(&g.publicVideo, func(t *postgres.AudioTrack) { t.DurationMs = 28400 })
	g.svc.keepComposerDraftSound(context.Background(), &postgres.Post{ID: g.post, AuthorID: g.other}, &PostDraftPayload{AudioTrackID: str(again.String())})
	if g.store.attached[g.post] != again || g.store.startMs[g.post] != 0 {
		t.Fatalf("attached %v start %v", g.store.attached, g.store.startMs)
	}
}

// The composer saves its whole create body as the draft payload, and the
// payload refuses unknown keys: the new field must be a known one.
func TestComposerDraftPayloadTakesTheSoundFields(t *testing.T) {
	sound := uuid.New()
	p, err := parseDraftPayload(json.RawMessage(`{"text":"x","audio_track_id":"` + sound.String() + `","audio_start_ms":1500}`))
	if err != nil {
		t.Fatalf("a payload with audio_start_ms was refused: %v", err)
	}
	if p.AudioTrackID == nil || *p.AudioTrackID != sound.String() || p.AudioStartMs == nil || *p.AudioStartMs != 1500 {
		t.Fatalf("payload = %+v", p)
	}
	p, err = parseDraftPayload(json.RawMessage(`{"text":"x","audio_track_id":""}`))
	if err != nil || p.AudioTrackID == nil || *p.AudioTrackID != "" {
		t.Fatalf("an empty audio_track_id: %+v %v", p, err)
	}
}
