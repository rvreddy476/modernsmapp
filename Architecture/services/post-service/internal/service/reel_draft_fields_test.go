package service

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A saved draft publishes FROM the draft row (2026-10-22). Anything the
// studio patches onto it that the publish does not copy is lost without an
// error: content_type, hashtags, the SEO title and the recording fields all
// were. These tests make that loss impossible to reintroduce quietly.

// draftReachesPost maps every UpdateDraftInput field that the post carries
// to the CreatePostInput field(s) it must change.
var draftReachesPost = map[string][]string{
	"Title":             {"Title"},
	"Caption":           {"Text"},
	"Hashtags":          {"Hashtags"},
	"Tags":              {"Tags"},
	"Visibility":        {"Visibility"},
	"Category":          {"Category"},
	"Language":          {"Language"},
	"SEOTitle":          {"SEOTitle"},
	"CrossPostPostbook": {"ShareToPostbook"},
	"PublishToFeed":     {"PublishToFeed", "LegacyDistribution"},
	"IsMadeForKids":     {"IsMadeForKids"},
	"PaidPromotion":     {"PaidPromotion"},
	"AlteredContent":    {"AlteredContent"},
	"License":           {"License"},
	"AllowEmbedding":    {"AllowEmbedding"},
	"RemixSetting":      {"RemixSetting"},
	"LikesEnabled":      {"NoLikes"},
	"CommentsEnabled":   {"NoComments"},
	"CommentModeration": {"CommentModeration"},
	"CommentAccess":     {"CommentAccess"},
	"RecordingDate":     {"RecordingDate"},
	"RecordingLocation": {"RecordingLocation"},
	"AIGenerated":       {"AIGenerated"},
	"LocationName":      {"LocationName"},
	"LocationPlaceID":   {"LocationPlaceID"},
	"ContentType":       {"ContentType"},
	"OriginalAudioVol":  {"OriginalAudioVol"},
	"OverlayAudioVol":   {"OverlayAudioVol"},
	"CoverMediaID":      {"CoverMediaID"},
	// Video credits (migration 070): a long video's Credits step.
	"Credits":         {"VideoCredits"},
	"Genres":          {"VideoCredits"},
	"ReleaseYear":     {"VideoCredits"},
	"ProductionHouse": {"VideoCredits"},
	// Members-only from birth (migration 071): the gate the post is
	// created with, checked by CreatePost.
	"TierRequiredID": {"TierRequiredID"},
	// The Action button (migration 072): checked and written by CreatePost.
	"Action": {"Action"},
}

// draftOnly lists the UpdateDraftInput fields that deliberately do NOT
// reach CreatePostInput, with the reason. Moving a field here is a
// decision; forgetting one fails TestEveryDraftFieldIsAccountedFor.
var draftOnly = map[string]string{
	"TopicID":           "no post-side topic: CreatePostInput has no field for it",
	"CrossPostPosttube": "no post-side switch: a long_video is on PostTube by its content_type",
	"AutoChapters":      "studio preference with no post-side field",
	"FeaturedPlaces":    "studio preference with no post-side field",
	"AutoConcepts":      "studio preference with no post-side field",
	"AudioTrackID":      "linked after the create by keepDraftSound, not through CreatePostInput",
	"AudioStartMs":      "linked after the create by keepDraftSound, not through CreatePostInput",
	"ScheduleAt":        "drives the schedule worker that publishes the draft; not a post field",
}

func jsonName(f reflect.StructField) string {
	return strings.Split(f.Tag.Get("json"), ",")[0]
}

func TestEveryDraftFieldIsAccountedFor(t *testing.T) {
	patch := reflect.TypeOf(UpdateDraftInput{})
	post := reflect.TypeOf(CreatePostInput{})
	seen := map[string]bool{}
	for i := 0; i < patch.NumField(); i++ {
		name := patch.Field(i).Name
		seen[name] = true
		targets, reaches := draftReachesPost[name]
		_, only := draftOnly[name]
		switch {
		case reaches && only:
			t.Errorf("%s is listed both as reaching the post and as draft-only", name)
		case !reaches && !only:
			t.Errorf("UpdateDraftInput.%s neither reaches CreatePostInput nor is listed draft-only: copy it in applyDraftScreenFields, or add it to draftOnly with the reason", name)
		}
		for _, target := range targets {
			if _, ok := post.FieldByName(target); !ok {
				t.Errorf("%s maps to CreatePostInput.%s, which does not exist", name, target)
			}
		}
	}
	for name := range draftReachesPost {
		if !seen[name] {
			t.Errorf("draftReachesPost names %s, which UpdateDraftInput no longer has", name)
		}
	}
	for name := range draftOnly {
		if !seen[name] {
			t.Errorf("draftOnly names %s, which UpdateDraftInput no longer has", name)
		}
	}
}

// typicalDraft is a draft as CreateDraft makes it, with a tagged place so
// the place id is copied at all.
func typicalDraft() postgres.ReelDraft {
	name, place := "Charminar", "ChIJm5MaboqXyzsR-xPxquRpWss"
	media := uuid.New()
	return postgres.ReelDraft{
		ID: uuid.New(), AuthorID: uuid.New(), MediaID: &media,
		Caption: "a caption", Visibility: "public", Status: "draft", ModerationStatus: "pending",
		PublishToFeed: true, AutoChapters: true, FeaturedPlaces: true, AutoConcepts: true,
		License: "standard", AllowEmbedding: true, RemixSetting: "allow",
		LikesEnabled: true, CommentsEnabled: true, CommentModeration: "basic", CommentAccess: "everyone",
		Language: "en", OriginalAudioVol: 1, OverlayAudioVol: 1,
		LocationName: &name, LocationPlaceID: &place,
	}
}

// perturb sets v to a value different from the one it holds.
func perturb(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-changed")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 0.25)
	case reflect.Slice:
		if v.Type().Elem() == reflect.TypeOf(postgres.CreditInput{}) {
			v.Set(reflect.Append(v, reflect.ValueOf(postgres.CreditInput{Name: "changed", Role: "Actor"})))
			return
		}
		v.Set(reflect.Append(v, reflect.ValueOf("changed")))
	case reflect.Pointer:
		next := reflect.New(v.Type().Elem())
		if !v.IsNil() {
			next.Elem().Set(v.Elem())
		}
		perturb(t, next.Elem())
		v.Set(next)
	case reflect.Array: // uuid.UUID
		v.Set(reflect.ValueOf(uuid.New()))
	case reflect.Struct:
		if tm, ok := v.Interface().(time.Time); ok {
			v.Set(reflect.ValueOf(tm.Add(36 * time.Hour)))
			return
		}
		if a, ok := v.Interface().(postgres.DraftAction); ok {
			v.Set(reflect.ValueOf(postgres.DraftAction{Label: "learn_more", URL: a.URL + "/changed"}))
			return
		}
		t.Fatalf("perturb: no rule for %s", v.Type())
	default:
		t.Fatalf("perturb: no rule for %s", v.Type())
	}
}

// For each UpdateDraftInput field, change the draft column it writes and
// build the post both publish paths build: the mapped CreatePostInput
// fields change and nothing else does. A draft-only field changes nothing.
func TestEveryDraftFieldReachesThePostItMapsTo(t *testing.T) {
	draftType := reflect.TypeOf(postgres.ReelDraft{})
	columnByJSON := map[string]int{}
	for i := 0; i < draftType.NumField(); i++ {
		columnByJSON[jsonName(draftType.Field(i))] = i
	}
	base := typicalDraft()
	author := uuid.New()
	want := reelDraftPostInput(&base, author, "long_video") // a long video, so the credit fields map too
	postType := reflect.TypeOf(CreatePostInput{})

	patch := reflect.TypeOf(UpdateDraftInput{})
	for i := 0; i < patch.NumField(); i++ {
		field := patch.Field(i)
		col, ok := columnByJSON[jsonName(field)]
		if !ok {
			t.Errorf("UpdateDraftInput.%s (%q) has no ReelDraft column with that json name", field.Name, jsonName(field))
			continue
		}
		changed := typicalDraft()
		changed.ID, changed.AuthorID, changed.MediaID = base.ID, base.AuthorID, base.MediaID
		perturb(t, reflect.ValueOf(&changed).Elem().Field(col))
		got := reelDraftPostInput(&changed, author, "long_video")

		targets := map[string]bool{}
		for _, name := range draftReachesPost[field.Name] {
			targets[name] = true
		}
		gv, wv := reflect.ValueOf(got).Elem(), reflect.ValueOf(want).Elem()
		for j := 0; j < postType.NumField(); j++ {
			name := postType.Field(j).Name
			differs := !reflect.DeepEqual(gv.Field(j).Interface(), wv.Field(j).Interface())
			switch {
			case targets[name] && !differs:
				t.Errorf("draft %s changed but CreatePostInput.%s did not: the publish drops it", field.Name, name)
			case !targets[name] && differs:
				t.Errorf("draft %s changed CreatePostInput.%s, which it is not mapped to", field.Name, name)
			}
		}
	}
}

// The studio's draft patch, decoded as the PATCH handler decodes it, must
// keep the kind it measured: a field UpdateDraftInput does not declare is
// dropped by the decoder without an error.
func TestUpdateDraftInputKeepsTheStudioContentType(t *testing.T) {
	var patch UpdateDraftInput
	if err := json.Unmarshal([]byte(`{"content_type":"long_video"}`), &patch); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back["content_type"] != "long_video" {
		t.Fatalf("content_type dropped by UpdateDraftInput: %s", out)
	}
}

func TestDraftPublishUpdatesContentType(t *testing.T) {
	for _, kind := range []string{"flick", "long_video"} {
		updates := map[string]interface{}{}
		if err := draftPublishUpdates(&UpdateDraftInput{ContentType: &kind}, updates); err != nil || updates["content_type"] != kind {
			t.Errorf("%q: err=%v updates=%v", kind, err, updates)
		}
	}
	// Legacy spellings, other post kinds and the empty string are refused
	// before any column is written: a draft is a reel or a long video.
	for _, kind := range []string{"", "reel", "video", "post", "poll", "voice", "Long_Video", "long_video "} {
		updates := map[string]interface{}{}
		err := draftPublishUpdates(&UpdateDraftInput{ContentType: &kind}, updates)
		if !errors.Is(err, ErrInvalidDraftContentType) || len(updates) != 0 {
			t.Errorf("%q: err=%v updates=%v, want ErrInvalidDraftContentType and no writes", kind, err, updates)
		}
	}
	updates := map[string]interface{}{}
	if err := draftPublishUpdates(&UpdateDraftInput{}, updates); err != nil || len(updates) != 0 {
		t.Errorf("absent: err=%v updates=%v", err, updates)
	}
}

func TestDraftPublishUpdatesHashtags(t *testing.T) {
	updates := map[string]interface{}{}
	if err := draftPublishUpdates(&UpdateDraftInput{Hashtags: []string{"#GoLang", "golang", "talks"}}, updates); err != nil {
		t.Fatal(err)
	}
	if got := updates["hashtags"]; !reflect.DeepEqual(got, []string{"golang", "talks"}) {
		t.Errorf("hashtags stored as %v, want the create route's canonical [golang talks]", got)
	}
	updates = map[string]interface{}{}
	if err := draftPublishUpdates(&UpdateDraftInput{Hashtags: []string{}}, updates); err != nil {
		t.Fatal(err)
	}
	if got, ok := updates["hashtags"].([]string); !ok || got == nil || len(got) != 0 {
		t.Errorf("[] must clear to an empty list, got %#v", updates["hashtags"])
	}
	for _, bad := range [][]string{{"c++"}, {""}, make([]string, MaxExplicitHashtags+1)} {
		updates = map[string]interface{}{}
		err := draftPublishUpdates(&UpdateDraftInput{Hashtags: bad}, updates)
		if !errors.Is(err, ErrInvalidHashtag) && !errors.Is(err, ErrTooManyHashtags) {
			t.Errorf("%q: err=%v, want the create route's refusal", bad, err)
		}
		if len(updates) != 0 {
			t.Errorf("%q: wrote %v", bad, updates)
		}
	}
}

// A draft that never said its kind is classified from the measurement the
// way the direct create path classifies one, and stays a flick while the
// video is not measured.
func TestClassifyUnsaidDraft(t *testing.T) {
	for _, c := range []struct {
		dur, w, h int
		want      string
	}{
		{0, 0, 0, "flick"},
		{0, 1920, 1080, "flick"},
		{30, 1080, 1920, "flick"},
		{flickMaxDurationSeconds, 1080, 1920, "flick"},
		{flickMaxDurationSeconds + 1, 1080, 1920, "long_video"},
		{30, 1920, 1080, "long_video"},
		{600, 1920, 1080, "long_video"},
	} {
		if got := classifyUnsaidDraft(c.dur, c.w, c.h); got != c.want {
			t.Errorf("classifyUnsaidDraft(%d, %d, %d) = %q, want %q", c.dur, c.w, c.h, got, c.want)
		}
	}
}

// The kind the draft said wins over the unsaid fallback.
func TestReelDraftPostInputUsesTheSavedKind(t *testing.T) {
	d := typicalDraft()
	if got := reelDraftPostInput(&d, d.AuthorID, "flick").ContentType; got != "flick" {
		t.Errorf("unsaid: %q", got)
	}
	long := "long_video"
	d.ContentType = &long
	if got := reelDraftPostInput(&d, d.AuthorID, "flick").ContentType; got != "long_video" {
		t.Errorf("saved long_video published as %q", got)
	}
}
