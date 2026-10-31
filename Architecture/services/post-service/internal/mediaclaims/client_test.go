package mediaclaims

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type seen struct {
	path, key string
	body      claimRequest
}

func server(t *testing.T, status int, body string) (*Client, *[]seen) {
	t.Helper()
	var calls []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b claimRequest
		_ = json.NewDecoder(r.Body).Decode(&b)
		calls = append(calls, seen{path: r.URL.Path, key: r.Header.Get(internalKeyHeader), body: b})
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "k-test"), &calls
}

func TestClaimSendsTheContractBody(t *testing.T) {
	c, calls := server(t, http.StatusOK, `{"data":{"claimed":1}}`)
	ref, id := uuid.New(), uuid.New()
	if err := c.Claim(context.Background(), KindPostCover, ref, []uuid.UUID{id}); err != nil {
		t.Fatal(err)
	}
	got := (*calls)[0]
	if got.path != "/v1/media/internal/claims" || got.key != "k-test" {
		t.Fatalf("path=%q keyed=%v", got.path, got.key != "")
	}
	if got.body.Claimant != "post-service" || got.body.Kind != "post_cover" || got.body.Ref != ref.String() ||
		len(got.body.MediaIDs) != 1 || got.body.MediaIDs[0] != id {
		t.Fatalf("body = %+v", got.body)
	}
}

// The exact strings media-service must register (claimKinds).
func TestKindStrings(t *testing.T) {
	want := []string{"post_cover", "reel_draft", "event_cover", "post_draft_cover", "video_series_cover", "playlist_cover"}
	if len(Kinds) != len(want) {
		t.Fatalf("kinds = %v", Kinds)
	}
	for i := range want {
		if Kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", Kinds, want)
		}
	}
}

func TestReleaseAndReleaseExcept(t *testing.T) {
	c, calls := server(t, http.StatusOK, `{"data":{"released":1}}`)
	ref, keep := uuid.New(), uuid.New()
	if err := c.Release(context.Background(), KindReelDraft, ref, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.ReleaseExcept(context.Background(), KindReelDraft, ref, []uuid.UUID{keep}); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].path != "/v1/media/internal/claims/release" || len((*calls)[0].body.MediaIDs) != 0 {
		t.Fatalf("release-all = %+v", (*calls)[0])
	}
	if b := (*calls)[1].body; len(b.ExceptMediaIDs) != 1 || b.ExceptMediaIDs[0] != keep || len(b.MediaIDs) != 0 {
		t.Fatalf("release-except body = %+v", b)
	}
	// An empty keep is "release everything" sent by mistake: refused here.
	if err := c.ReleaseExcept(context.Background(), KindReelDraft, ref, nil); err == nil {
		t.Fatal("ReleaseExcept with an empty keep set must be refused")
	}
	if len(*calls) != 2 {
		t.Fatal("the refused ReleaseExcept reached media-service")
	}
}

// 404 with MEDIA_NOT_FOUND is a verdict; a bare 404 (a media-service without
// the route) and every other status are not.
func TestClaimAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"asset missing", http.StatusNotFound, `{"error":{"code":"MEDIA_NOT_FOUND"}}`, ErrNotFound},
		{"route missing", http.StatusNotFound, `404 page not found`, ErrUnavailable},
		{"unknown claimant", http.StatusBadRequest, `{"error":{"code":"CLAIM_INVALID"}}`, ErrUnavailable},
		{"down", http.StatusServiceUnavailable, ``, ErrUnavailable},
		{"redirect", http.StatusFound, ``, ErrUnavailable},
	}
	for _, tc := range cases {
		c, _ := server(t, tc.status, tc.body)
		err := c.Claim(context.Background(), KindEventCover, uuid.New(), []uuid.UUID{uuid.New()})
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if tc.want == ErrUnavailable && errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: read as a verdict about the asset", tc.name)
		}
	}
}

func TestNilClientIsUnavailable(t *testing.T) {
	if New("", "k") != nil || New("http://media", "") != nil {
		t.Fatal("a client without an address or a key must not be built")
	}
	var c *Client
	if err := c.Claim(context.Background(), KindPostCover, uuid.New(), []uuid.UUID{uuid.New()}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client claim: %v", err)
	}
	if err := c.Release(context.Background(), KindPostCover, uuid.New(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client release: %v", err)
	}
}

func TestClaimOfNothingSendsNothing(t *testing.T) {
	c, calls := server(t, http.StatusOK, `{}`)
	if err := c.Claim(context.Background(), KindPostCover, uuid.New(), nil); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatal("an empty claim reached media-service (which answers 400 CLAIM_INVALID)")
	}
}

// A redirect is never followed: Go's client would resend the internal key
// (a custom header) to wherever the Location points.
func TestClaimNeverFollowsARedirect(t *testing.T) {
	reached := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(elsewhere.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	err := New(redirector.URL, "k-test").Claim(context.Background(), KindPostCover, uuid.New(), []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if reached {
		t.Fatal("the client followed a redirect, carrying the internal key with it")
	}
}
