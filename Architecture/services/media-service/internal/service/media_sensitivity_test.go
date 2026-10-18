package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
)

func scannedAsset(t *testing.T, scanner string, labels ...processing.ModerationLabel) *postgres.MediaAsset {
	t.Helper()
	if labels == nil {
		labels = []processing.ModerationLabel{}
	}
	raw, err := json.Marshal(labels)
	if err != nil {
		t.Fatal(err)
	}
	return &postgres.MediaAsset{ModerationStatus: "passed", ModerationScanner: scanner, ModerationLabels: raw}
}

func wantSensitive(t *testing.T, what string, got MediaSensitivity, want *bool) {
	t.Helper()
	switch {
	case want == nil && got.Sensitive != nil:
		t.Errorf("%s: sensitive = %v, want null (unknown)", what, *got.Sensitive)
	case want != nil && got.Sensitive == nil:
		t.Errorf("%s: sensitive = null, want %v", what, *want)
	case want != nil && *got.Sensitive != *want:
		t.Errorf("%s: sensitive = %v, want %v", what, *got.Sensitive, *want)
	}
}

var (
	yes = func() *bool { v := true; return &v }()
	no  = func() *bool { v := false; return &v }()
)

// Every sensitive category, matched on the label's own name and, separately,
// as the parent of an unlisted child. Emptying sensitiveCategories, or
// dropping either side of the name/parent match, fails this.
func TestSensitivityEachSensitiveCategory(t *testing.T) {
	for category := range sensitiveCategories {
		got := SensitivityOf(scannedAsset(t, "rekognition", processing.ModerationLabel{Name: category, Confidence: 91}))
		wantSensitive(t, "name "+category, got, yes)

		got = SensitivityOf(scannedAsset(t, "rekognition", processing.ModerationLabel{Name: "Some Child Label", Parent: category, Confidence: 91}))
		wantSensitive(t, "parent "+category, got, yes)
	}
	// The real taxonomy spellings, case-insensitive (the scanner's
	// worstBlocked and dating-service match the same way).
	for _, l := range []processing.ModerationLabel{
		{Name: "EXPLICIT NUDITY"},
		{Name: "Nudity", Parent: "Explicit Nudity"},
		{Name: "Exposed Female Nipple", Parent: "Explicit Nudity"},
		{Name: "Female Swimwear Or Underwear", Parent: "Suggestive"},
		{Name: "Kissing on the Lips", Parent: "Non-Explicit Nudity of Intimate parts and Kissing"},
		{Name: "Weapons", Parent: "Violence"},
		{Name: "Blood & Gore", Parent: "Graphic Violence"},
		{Name: "Emaciated Bodies", Parent: "Visually Disturbing"},
	} {
		wantSensitive(t, l.Parent+">"+l.Name, SensitivityOf(scannedAsset(t, "rekognition", l)), yes)
	}
}

// Categories that are moderation findings but not nudity, sexual or violent
// content. Adding them to sensitiveCategories fails this.
func TestSensitivityNonSensitiveCategoriesAreFalse(t *testing.T) {
	for _, l := range []processing.ModerationLabel{
		{Name: "Alcohol"},
		{Name: "Drinking", Parent: "Alcohol"},
		{Name: "Drugs"},
		{Name: "Drugs & Tobacco"},
		{Name: "Smoking", Parent: "Tobacco"},
		{Name: "Gambling"},
		{Name: "Middle Finger", Parent: "Rude Gestures"},
		{Name: "Hate Symbols"},
	} {
		wantSensitive(t, l.Parent+">"+l.Name, SensitivityOf(scannedAsset(t, "rekognition", l)), no)
	}
	// A real scan with no findings is a confident "not sensitive".
	wantSensitive(t, "clean rekognition scan", SensitivityOf(scannedAsset(t, "rekognition")), no)
	wantSensitive(t, "clean mock scan", SensitivityOf(scannedAsset(t, "mock")), no)
	// One sensitive label among harmless ones still makes it sensitive.
	wantSensitive(t, "mixed", SensitivityOf(scannedAsset(t, "rekognition",
		processing.ModerationLabel{Name: "Alcohol"}, processing.ModerationLabel{Name: "Suggestive"})), yes)
}

// Unknown is never "not sensitive". Dropping the scanner guard in
// storedScanLabels turns the stub rows false; dropping the NULL guard turns
// the null-column row false.
func TestSensitivityUnknownWithoutARealScan(t *testing.T) {
	for _, scanner := range []string{"", "stub", "STUB", "unknown"} {
		wantSensitive(t, "scanner "+scanner, SensitivityOf(scannedAsset(t, scanner)), nil)
	}
	for name, raw := range map[string][]byte{"no column": nil, "json null": []byte("null"), "garbage": []byte("{not json")} {
		m := &postgres.MediaAsset{ModerationStatus: "passed", ModerationScanner: "rekognition", ModerationLabels: raw}
		got := SensitivityOf(m)
		wantSensitive(t, name, got, nil)
		if got.LabelNames != nil {
			t.Errorf("%s: label names %v, want none", name, got.LabelNames)
		}
	}
	wantSensitive(t, "nil asset", SensitivityOf(nil), nil)
	// A stub that recorded a sensitive-looking label is still not a scan.
	wantSensitive(t, "stub with labels", SensitivityOf(scannedAsset(t, "stub", processing.ModerationLabel{Name: "Explicit Nudity"})), nil)
}

// A rejected asset is sensitive whatever its labels say. Dropping the
// rejected branch fails the unscanned row (null) and the clean row (false).
func TestSensitivityRejectedIsTrue(t *testing.T) {
	unscanned := &postgres.MediaAsset{ModerationStatus: "rejected"}
	wantSensitive(t, "rejected, unscanned", SensitivityOf(unscanned), yes)
	clean := scannedAsset(t, "rekognition")
	clean.ModerationStatus = "rejected"
	wantSensitive(t, "rejected, clean labels", SensitivityOf(clean), yes)
	pending := &postgres.MediaAsset{ModerationStatus: "pending"}
	wantSensitive(t, "pending, unscanned", SensitivityOf(pending), nil)
}

// Names only, de-duplicated case-insensitively, in scan order.
func TestSensitivityLabelNamesOnly(t *testing.T) {
	got := SensitivityOf(scannedAsset(t, "rekognition",
		processing.ModerationLabel{Name: "Suggestive", Confidence: 88},
		processing.ModerationLabel{Name: "Revealing Clothes", Parent: "Suggestive", Confidence: 85},
		processing.ModerationLabel{Name: "suggestive", Confidence: 81},
		processing.ModerationLabel{Name: " ", Confidence: 99},
	))
	if want := []string{"Suggestive", "Revealing Clothes"}; !reflect.DeepEqual(got.LabelNames, want) {
		t.Errorf("label names %v, want %v", got.LabelNames, want)
	}
	if clean := SensitivityOf(scannedAsset(t, "rekognition")); clean.LabelNames == nil || len(clean.LabelNames) != 0 {
		t.Errorf("clean scan label names %#v, want empty (scanned, nothing found)", clean.LabelNames)
	}
}
