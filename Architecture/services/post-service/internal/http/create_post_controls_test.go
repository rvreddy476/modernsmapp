package http

import (
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/google/uuid"
)

// Per-reel controls on the create request (2026-09-04).
//
// `allow_download` defaults TRUE in the column, so the request field must be
// presence-aware: an omitted key keeps downloads on, and only an explicit
// false turns them off. A plain bool would make every old client silently
// disable downloads on every post.
func TestResolveAllowDownload(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		in   *bool
		want bool
	}{
		{"omitted keeps the column default", nil, true},
		{"explicit true", &yes, true},
		{"explicit false is honored", &no, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAllowDownload(tc.in); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// hide_share is a plain bool because absent and false mean the same thing.
func TestHideShareDefaultsToFalse(t *testing.T) {
	if baseRequest().HideShare {
		t.Fatal("hide_share must default to false")
	}
}

func TestParseTaggedUserIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()

	t.Run("nil is an empty list", func(t *testing.T) {
		got, err := parseTaggedUserIDs(nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v err %v", got, err)
		}
	})

	t.Run("valid ids parse in order, whitespace forgiven", func(t *testing.T) {
		got, err := parseTaggedUserIDs([]string{a.String(), " " + b.String() + " "})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if len(got) != 2 || got[0] != a || got[1] != b {
			t.Fatalf("got %v want [%s %s]", got, a, b)
		}
	})

	t.Run("a non-uuid is rejected and named", func(t *testing.T) {
		_, err := parseTaggedUserIDs([]string{a.String(), "not-a-uuid"})
		if err == nil || !strings.Contains(err.Error(), "not-a-uuid") {
			t.Fatalf("err=%v, want one naming the bad id", err)
		}
	})

	t.Run("duplicates are left for the service to collapse", func(t *testing.T) {
		got, err := parseTaggedUserIDs([]string{a.String(), a.String()})
		if err != nil || len(got) != 2 {
			t.Fatalf("got %v err %v", got, err)
		}
	})

	t.Run("exactly the cap is accepted", func(t *testing.T) {
		raw := make([]string, service.MaxTaggedUsers)
		for i := range raw {
			raw[i] = uuid.New().String()
		}
		if _, err := parseTaggedUserIDs(raw); err != nil {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("over the cap fails before any id is parsed", func(t *testing.T) {
		raw := make([]string, service.MaxTaggedUsers+1)
		for i := range raw {
			raw[i] = "would-not-parse"
		}
		_, err := parseTaggedUserIDs(raw)
		if err == nil || !strings.Contains(err.Error(), "at most") {
			t.Fatalf("err=%v, want the limit error", err)
		}
	})
}

// The idempotency fingerprint marshals the whole request, so a retry that
// flips a switch or changes who is tagged must not replay the original post.
func TestCanonicalFingerprintCoversReelControls(t *testing.T) {
	base := fingerprintOrFail(t, baseRequest())
	no := false
	cases := []struct {
		name   string
		mutate func(*CreatePostRequest)
	}{
		{"hide_share", func(r *CreatePostRequest) { r.HideShare = true }},
		{"allow_download false", func(r *CreatePostRequest) { r.AllowDownload = &no }},
		{"category", func(r *CreatePostRequest) { r.Category = "comedy" }},
		{"tagged_user_ids", func(r *CreatePostRequest) { r.TaggedUserIDs = []string{uuid.New().String()} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := baseRequest()
			tc.mutate(&r)
			if fingerprintOrFail(t, r) == base {
				t.Fatalf("%s did not change the fingerprint", tc.name)
			}
		})
	}
}
