package service

import "testing"

// Deep links are the canonical web/app routes: a long video opens the Tube
// watch page, a flick opens the reels player. The old "/posttube/watch/{id}"
// path is gone and must never come back.
func TestUploadDeepLink(t *testing.T) {
	const id = "7a1c1c8e-2b0e-4b7f-9a2d-000000000001"
	cases := []struct {
		contentType string
		wantLink    string
		wantType    string
	}{
		{"long_video", "/tube/watch/" + id, "creator_uploaded_video"},
		{"video", "/tube/watch/" + id, "creator_uploaded_video"},
		{"flick", "/reels/" + id, "creator_uploaded_flick"},
		{"reel", "/reels/" + id, "creator_uploaded_flick"},
		{"post", "", ""},
		{"photo", "", ""},
		{"poll", "", ""},
		{"text", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		t.Run(c.contentType, func(t *testing.T) {
			if got := UploadDeepLink(c.contentType, id); got != c.wantLink {
				t.Fatalf("UploadDeepLink(%q) = %q, want %q", c.contentType, got, c.wantLink)
			}
			if got := UploadNotifType(c.contentType); got != c.wantType {
				t.Fatalf("UploadNotifType(%q) = %q, want %q", c.contentType, got, c.wantType)
			}
		})
	}
}
