package http

import (
	"bytes"
	"context"
	"image/jpeg"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Lanes D5 + D6 on dev. Dating photo prepare re-encodes the upload (EXIF/GPS
// stripped), which drops the mock face test marker. With the local mock
// provider wired, prepare carries the marker onto the prepared renditions, so
// after prepare:
//   - a seeded photo still matches its seeded selfie video;
//   - a "different face" or "no face" test photo still fails;
//   - media with no marker at all (a real phone photo and recording) is the
//     uploader's one face and passes.
// Without the mock the stored bytes are exactly what PrepareDatingImage
// rendered.

// splitJPEGTrailer splits a JPEG after its end-of-image marker. Entropy-coded
// data stuffs 0xFF as FF 00, so FF D9 only occurs as EOI.
func splitJPEGTrailer(t *testing.T, data []byte) ([]byte, []byte) {
	t.Helper()
	i := bytes.LastIndex(data, []byte{0xFF, 0xD9})
	if i < 0 {
		t.Fatal("no JPEG end-of-image marker")
	}
	return data[:i+2], data[i+2:]
}

// assertServedDatingBytes checks every stored object of a prepared dating
// photo: decodable, no metadata segment, no EXIF/GPS or uploaded comment
// inside the image, and after EOI either nothing or exactly wantTrailer (the
// original must carry wantTrailer; the blurred variant must carry nothing).
func assertServedDatingBytes(t *testing.T, f *datingFixture, media uuid.UUID, wantTrailer string) {
	t.Helper()
	served := map[string]string{"original": f.store.assets[media].StorageKey}
	for _, v := range f.store.variants[media] {
		served[v.Name] = v.ObjectKey
	}
	if served[processing.DatingBlurVariant] == "" {
		t.Fatal("no blurred variant recorded")
	}
	for name, key := range served {
		data := f.blobs.objects[key]
		if len(data) == 0 {
			t.Fatalf("%s (%s) was not stored", name, key)
		}
		img, trailer := splitJPEGTrailer(t, data)
		if processing.JPEGHasMetadata(data) || bytes.Contains(data, []byte(datingGPSSecret)) ||
			bytes.Contains(data, []byte("Exif")) || bytes.Contains(img, []byte("ATPOST-")) {
			t.Fatalf("served %s bytes still carry EXIF/GPS or comments", name)
		}
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			t.Fatalf("served %s is not a decodable JPEG: %v", name, err)
		}
		got := string(trailer)
		switch {
		case name == processing.DatingBlurVariant && got != "":
			t.Fatalf("blurred variant carries %q after EOI", got)
		case name == "original" && got != wantTrailer:
			t.Fatalf("original trailer = %q, want %q", got, wantTrailer)
		case got != "" && got != wantTrailer:
			t.Fatalf("%s trailer = %q, want %q or nothing", name, got, wantTrailer)
		}
	}
}

type carryFixture struct {
	*datingFixture
	faces                           *service.FaceCompareService
	differentFace, noFace, phoneJPG uuid.UUID
	twoBlinks, oneBlink, phoneVideo uuid.UUID
}

// newCarryFixture: the dating fixture (whose photo is marked subject=owner
// and carries EXIF/GPS), plus more owner photos and selfie videos, and the
// face service over the same fake stores with the mock provider.
func newCarryFixture(t *testing.T) *carryFixture {
	t.Helper()
	f := &carryFixture{datingFixture: newDatingFixture(t),
		differentFace: uuid.New(), noFace: uuid.New(), phoneJPG: uuid.New(),
		twoBlinks: uuid.New(), oneBlink: uuid.New(), phoneVideo: uuid.New()}
	add := func(id uuid.UUID, fileType, mime string, content []byte) {
		key := "user/" + f.owner.String() + "/" + id.String() + "/original"
		a := &postgres.MediaAsset{ID: id, UploaderID: f.owner, FileType: fileType, MimeType: mime,
			ProcessingStatus: "ready", ModerationStatus: "passed", StorageKey: key, ModerationScanner: "mock",
			ModerationLabels: []byte(`[]`), FileSizeBytes: int64(len(content))}
		if fileType == "video" {
			d := 3000
			a.DurationMs = &d
		}
		f.store.assets[id] = a
		f.blobs.objects[key] = content
	}
	add(f.differentFace, "image", "image/jpeg", datingJPEGWithComment(t, processing.MockFaceMarker+"faces=1:subject=someone-else\n"))
	add(f.noFace, "image", "image/jpeg", datingJPEGWithComment(t, processing.MockFaceMarker+"faces=0\n"))
	add(f.phoneJPG, "image", "image/jpeg", datingJPEGWithComment(t, ""))
	add(f.twoBlinks, "video", "video/mp4", []byte("\x18ftypmp42\n"+processing.MockLivenessMarker+"blinks=2:faces=1:subject=owner:duration_ms=3000\n"))
	add(f.oneBlink, "video", "video/mp4", []byte("\x18ftypmp42\n"+processing.MockLivenessMarker+"blinks=1:faces=1:subject=owner:duration_ms=3000\n"))
	add(f.phoneVideo, "video", "video/mp4", []byte("\x18ftypmp42 a real four second recording, no marker"))
	f.faces = service.NewFaceCompareService(f.store, f.blobs, processing.NewMockFaceComparer(), 90).
		WithLiveness(processing.NewMockLivenessAnalyzer(), processing.DefaultLivenessConfig())
	return f
}

func (f *carryFixture) mustPrepare(t *testing.T, media uuid.UUID) service.DatingPhotoStatus {
	t.Helper()
	w := f.prepare(t, media, f.owner)
	if w.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	st := datingStatus(t, w)
	if !st.Prepared {
		t.Fatalf("status after prepare = %+v", st)
	}
	return st
}

func (f *carryFixture) liveness(t *testing.T, video, photo uuid.UUID) *service.LivenessOutcome {
	t.Helper()
	out, err := f.faces.CheckLiveness(context.Background(), f.owner, video, photo)
	if err != nil {
		t.Fatalf("liveness: %v", err)
	}
	return out
}

func TestMockCarry_PreparedPhotoStillMatchesItsSelfie(t *testing.T) {
	f := newCarryFixture(t)
	f.mustPrepare(t, f.photo)
	if o := f.liveness(t, f.twoBlinks, f.photo); !o.Match || o.Reason != "" || o.Similarity != 99 || o.BlinksDetected != 2 {
		t.Fatalf("seeded selfie vs prepared seeded photo = %+v; want a match at 99", o)
	}
	selfie := uuid.New()
	f.store.assets[selfie] = &postgres.MediaAsset{ID: selfie, UploaderID: f.owner, FileType: "image", ProcessingStatus: "ready",
		ModerationStatus: "passed", StorageKey: "user/" + f.owner.String() + "/" + selfie.String() + "/original"}
	f.blobs.objects[f.store.assets[selfie].StorageKey] = []byte(processing.MockFaceMarker + "faces=1:subject=owner\n")
	if o, err := f.faces.Compare(context.Background(), f.owner, selfie, f.photo); err != nil || !o.Match || o.Similarity != 99 {
		t.Fatalf("compare vs prepared photo = %+v, %v; want a match", o, err)
	}
}

func TestMockCarry_DifferentFacePhotoStillFailsAfterPrepare(t *testing.T) {
	f := newCarryFixture(t)
	f.mustPrepare(t, f.differentFace)
	for name, video := range map[string]uuid.UUID{"seeded selfie": f.twoBlinks, "phone recording": f.phoneVideo} {
		if o := f.liveness(t, video, f.differentFace); o.Match || o.Similarity != 10 {
			t.Fatalf("%s vs prepared different-face photo = %+v; want no match at 10", name, o)
		}
	}
	st := f.mustPrepare(t, f.noFace)
	if st.FaceCount == nil || *st.FaceCount != 0 {
		t.Fatalf("no-face photo face_count = %v; want 0", st.FaceCount)
	}
	if o := f.liveness(t, f.phoneVideo, f.noFace); o.Match || o.Reason != processing.FaceReasonNoFace {
		t.Fatalf("phone recording vs prepared no-face photo = %+v; want NO_FACE", o)
	}
}

func TestMockCarry_OneBlinkStillNotEnoughBlinks(t *testing.T) {
	f := newCarryFixture(t)
	f.mustPrepare(t, f.photo)
	if o := f.liveness(t, f.oneBlink, f.photo); o.Match || o.Reason != processing.LivenessReasonNotEnoughBlinks || o.BlinksDetected != 1 {
		t.Fatalf("one blink = %+v; want NOT_ENOUGH_BLINKS", o)
	}
}

// The founder's phone: a camera photo and a recording, neither marked.
func TestMockCarry_PhoneMediaWithoutMarkersPasses(t *testing.T) {
	f := newCarryFixture(t)
	st := f.mustPrepare(t, f.phoneJPG)
	if st.FaceCount == nil || *st.FaceCount != 1 {
		t.Fatalf("phone photo face_count = %v; want 1", st.FaceCount)
	}
	assertServedDatingBytes(t, f.datingFixture, f.phoneJPG, "")
	if o := f.liveness(t, f.phoneVideo, f.phoneJPG); !o.Match || o.Reason != "" || o.Similarity != 99 {
		t.Fatalf("phone recording vs phone photo = %+v; want a match", o)
	}
	// A marked seeded selfie is someone else's face to an unmarked photo.
	if o := f.liveness(t, f.twoBlinks, f.phoneJPG); o.Match || o.Similarity != 10 {
		t.Fatalf("seeded selfie vs phone photo = %+v; want no match", o)
	}
}

func TestMockCarry_PreparedBytesStillStripped(t *testing.T) {
	f := newCarryFixture(t)
	f.mustPrepare(t, f.photo)
	f.mustPrepare(t, f.differentFace)
	f.mustPrepare(t, f.noFace)
	assertServedDatingBytes(t, f.datingFixture, f.photo, "\n"+processing.MockFaceMarker+"faces=1:subject=owner\n")
	assertServedDatingBytes(t, f.datingFixture, f.differentFace, "\n"+processing.MockFaceMarker+"faces=1:subject=someone-else\n")
	assertServedDatingBytes(t, f.datingFixture, f.noFace, "\n"+processing.MockFaceMarker+"faces=0\n")
}

type countOnlyFaces struct{}

func (countOnlyFaces) CountFaces(context.Context, []byte) (int, error) { return 1, nil }

// Production shape: Rekognition is a FaceCounter but not a stamper, and with
// no provider there is no counter. Either way the stored objects are exactly
// PrepareDatingImage's output.
func TestDatingPhotoPrepare_WithoutMockStoresRenderedBytesExactly(t *testing.T) {
	var rek processing.FaceCounter = processing.NewRekognitionFaceComparer(nil)
	if _, ok := rek.(processing.DatingImageStamper); ok {
		t.Fatal("the Rekognition comparer must not implement DatingImageStamper")
	}
	for name, counter := range map[string]processing.FaceCounter{"rekognition-shaped": countOnlyFaces{}, "none": nil} {
		f := newDatingFixture(t)
		src := append([]byte(nil), f.blobs.objects[f.store.assets[f.photo].StorageKey]...)
		want, err := processing.PrepareDatingImage(src)
		if err != nil {
			t.Fatal(err)
		}
		svc := service.NewDatingPhotoService(f.store, f.blobs, f.signer, counter, time.Minute, nil)
		if _, err := svc.Prepare(context.Background(), f.owner, f.photo, true); err != nil {
			t.Fatalf("%s: prepare: %v", name, err)
		}
		rendered := map[string][]byte{"original": want.Original.Bytes, processing.DatingBlurVariant: want.Blurred.Bytes}
		for _, v := range want.Variants {
			rendered[v.Name] = v.Bytes
		}
		stored := map[string][]byte{"original": f.blobs.objects[f.store.assets[f.photo].StorageKey]}
		for _, v := range f.store.variants[f.photo] {
			stored[v.Name] = f.blobs.objects[v.ObjectKey]
		}
		if len(stored) != len(rendered) {
			t.Fatalf("%s: stored %d objects, rendered %d", name, len(stored), len(rendered))
		}
		for k, b := range rendered {
			if !bytes.Equal(stored[k], b) {
				t.Fatalf("%s: stored %s differs from PrepareDatingImage's bytes", name, k)
			}
			if strings.Contains(string(stored[k]), "ATPOST-") {
				t.Fatalf("%s: stored %s carries a test marker", name, k)
			}
		}
	}
}
