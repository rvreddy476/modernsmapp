package processing

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFrames(t *testing.T, specs ...string) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i, spec := range specs {
		p := filepath.Join(dir, "frame"+string(rune('a'+i))+".jpg")
		data := []byte("\xff\xd8 frame bytes")
		if spec != "" {
			data = append(data, []byte("\n"+MockModerationMarker+spec+"\n")...)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

// Chat parity pass 2: a passing video keeps every frame's findings, one entry
// per name+parent at its highest confidence. Returning the bare
// {IsSafe: true} again fails this.
func TestScanVideoFramesMergesLabelsOnPass(t *testing.T) {
	frames := writeFrames(t,
		"labels=Suggestive>Revealing_Clothes@82;Alcohol@90",
		"",
		"labels=Suggestive>Revealing_Clothes@93;Suggestive@93",
	)
	res, err := ScanVideoFrames(context.Background(), NewMockModerationScanner(), frames)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsSafe {
		t.Fatalf("borderline-only video was not safe: %+v", res)
	}
	want := []ModerationLabel{
		{Name: "Revealing Clothes", Parent: "Suggestive", Confidence: 93},
		{Name: "Alcohol", Confidence: 90},
		{Name: "Suggestive", Confidence: 93},
	}
	if !reflect.DeepEqual(res.Labels, want) {
		t.Errorf("merged labels %+v, want %+v", res.Labels, want)
	}

	clean, err := ScanVideoFrames(context.Background(), NewMockModerationScanner(), writeFrames(t, "", ""))
	if err != nil || !clean.IsSafe {
		t.Fatalf("clean video: %+v %v", clean, err)
	}
	if clean.Labels == nil || len(clean.Labels) != 0 {
		t.Errorf("clean video labels %#v, want an empty list (scanned, nothing found)", clean.Labels)
	}
}

// One blocked frame still fails the whole video, with that frame's verdict.
func TestScanVideoFramesUnsafeFrameStillRejects(t *testing.T) {
	frames := writeFrames(t, "labels=Suggestive@85", "labels=Explicit_Nudity>Nudity@97")
	res, err := ScanVideoFrames(context.Background(), NewMockModerationScanner(), frames)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsSafe || res.Reason != "explicit_nudity" {
		t.Fatalf("blocked frame passed: %+v", res)
	}
}
