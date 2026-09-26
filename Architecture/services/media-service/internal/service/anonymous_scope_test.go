package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/media-service/internal/store/postgres"
)

func TestAnonymousScopeDeniesEveryoneButTheUploader(t *testing.T) {
	up := uuid.New()
	anon := &postgres.MediaAsset{UploaderID: up, AccessScope: postgres.AccessScopeAnonymous}
	plain := &postgres.MediaAsset{UploaderID: up}
	if AnonymousScopeDenies(anon, up) {
		t.Fatal("the uploader was denied their own record")
	}
	if !AnonymousScopeDenies(anon, uuid.New()) || !AnonymousScopeDenies(anon, uuid.Nil) {
		t.Fatal("a stranger or an anonymous viewer could read an anonymous asset's record")
	}
	if AnonymousScopeDenies(plain, uuid.New()) || AnonymousScopeDenies(nil, up) {
		t.Fatal("an unscoped asset must not be affected")
	}
}

func TestParseByteRange(t *testing.T) {
	cases := []struct {
		header     string
		start, end int64
		ranged     bool
		err        bool
	}{
		{"", 0, 0, false, false},
		{"bytes=0-99", 0, 99, true, false},
		{"bytes=100-", 100, 999, true, false},
		{"bytes=900-5000", 900, 999, true, false},
		{"bytes=-100", 900, 999, true, false},
		{"bytes=1000-", 0, 0, false, true},   // past the end → 416
		{"bytes=0-9,20-29", 0, 0, false, false}, // multi-range → whole object
		{"items=0-9", 0, 0, false, false},
	}
	for _, c := range cases {
		s, e, r, err := parseByteRange(c.header, 1000)
		if (err != nil) != c.err || r != c.ranged || s != c.start || e != c.end {
			t.Errorf("%q: got (%d,%d,%v,%v) want (%d,%d,%v,err=%v)", c.header, s, e, r, err, c.start, c.end, c.ranged, c.err)
		}
	}
}

// The one wire difference between an anonymous asset and any other: its
// segments never become signed object URLs, which carry the uploader's id.
func TestRewriteHLSSendsAnonymousSegmentsThroughTheService(t *testing.T) {
	id := uuid.New()
	child := []string{"#EXTM3U", "#EXTINF:4.0,", "seg_000.ts", "#EXTINF:4.0,", "seg_001.ts", ""}
	signed := map[string]string{
		"seg_000.ts": "https://cdn/media/user/UPLOADER/" + id.String() + "/hls/seg_000.ts?X-Amz-Signature=abc",
		"seg_001.ts": "https://cdn/media/user/UPLOADER/" + id.String() + "/hls/seg_001.ts?X-Amz-Signature=def",
	}

	anon, err := rewriteHLS(child, "480p.m3u8", id, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(anon), "UPLOADER") || strings.Contains(string(anon), "X-Amz") {
		t.Fatalf("an anonymous playlist named the uploader or carried a signed URL:\n%s", anon)
	}
	if !strings.Contains(string(anon), "/v1/media/"+id.String()+"/hls-seg/seg_000.ts") {
		t.Fatalf("segments do not point back at this service:\n%s", anon)
	}

	plain, err := rewriteHLS(child, "480p.m3u8", id, signed, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), "X-Amz-Signature=abc") {
		t.Fatalf("an ordinary playlist lost its signed segment URLs:\n%s", plain)
	}

	master := []string{"#EXTM3U", "#EXT-X-STREAM-INF:BANDWIDTH=1", "480p.m3u8"}
	out, err := rewriteHLS(master, "master.m3u8", id, nil, true)
	if err != nil || !strings.Contains(string(out), "/v1/media/"+id.String()+"/hls/480p.m3u8") {
		t.Fatalf("master children must point at the playlist route: %v\n%s", err, out)
	}

	if _, err := rewriteHLS([]string{"../secret.ts"}, "480p.m3u8", id, nil, true); err == nil {
		t.Fatal("a segment name with a path was accepted")
	}
}
