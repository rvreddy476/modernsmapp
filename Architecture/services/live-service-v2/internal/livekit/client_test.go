package livekit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type twirpCall struct {
	path  string
	body  map[string]any
	grant map[string]any
}

// fakeTwirp records each call and answers from a per-path handler.
func fakeTwirp(t *testing.T, answer func(path string) (int, string)) (*httptest.Server, *[]twirpCall) {
	t.Helper()
	var calls []twirpCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(tok, ".")
		var claims struct {
			Video map[string]any `json:"video"`
		}
		if len(parts) == 3 {
			cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
			_ = json.Unmarshal(cb, &claims)
		}
		calls = append(calls, twirpCall{path: r.URL.Path, body: body, grant: claims.Video})
		code, out := answer(r.URL.Path)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, out)
	}))
	return srv, &calls
}

func ok(string) (int, string) { return 200, `{"egress_id":"EG_1","participants":[]}` }

// TestEgressRequestS3Target: the egress endpoint override is what goes in
// the request; with no static keys none are sent (ambient/IAM credentials);
// with both keys both are sent.
func TestEgressRequestS3Target(t *testing.T) {
	srv, calls := fakeTwirp(t, ok)
	defer srv.Close()
	base := Config{APIKey: "k", APISecret: "s", URL: srv.URL, S3Endpoint: "http://minio:9000", S3Bucket: "live-recordings", S3Region: "us-east-1"}

	cfg := base
	cfg.EgressS3Endpoint = "https://media-dev.example.com"
	if _, err := New(cfg).StartEgressToS3(context.Background(), "stream_x", "recordings/x.mp4"); err != nil {
		t.Fatal(err)
	}
	s3 := (*calls)[0].body["file"].(map[string]any)["s3"].(map[string]any)
	if s3["endpoint"] != "https://media-dev.example.com" {
		t.Fatalf("endpoint = %v", s3["endpoint"])
	}
	if _, has := s3["access_key"]; has {
		t.Fatalf("static access_key sent without configured keys: %v", s3)
	}
	if _, has := s3["secret"]; has {
		t.Fatalf("static secret sent without configured keys: %v", s3)
	}

	cfg = base // no override: the service's own endpoint
	cfg.S3AccessKey, cfg.S3SecretKey = "AK", "SK"
	if _, err := New(cfg).StartEgressToS3(context.Background(), "stream_x", "recordings/x.mp4"); err != nil {
		t.Fatal(err)
	}
	s3 = (*calls)[1].body["file"].(map[string]any)["s3"].(map[string]any)
	if s3["endpoint"] != "http://minio:9000" || s3["access_key"] != "AK" || s3["secret"] != "SK" {
		t.Fatalf("s3 = %v", s3)
	}

	cfg = base // only one of the pair: still none
	cfg.S3AccessKey = "AK"
	_, _ = New(cfg).StartEgressToS3(context.Background(), "stream_x", "recordings/x.mp4")
	s3 = (*calls)[2].body["file"].(map[string]any)["s3"].(map[string]any)
	if _, has := s3["access_key"]; has {
		t.Fatalf("half a key pair was sent: %v", s3)
	}
}

// TestRoomAdminCalls: ListParticipants / RemoveParticipant carry a grant
// scoped to the room (LiveKit checks grant.room == request room); a twirp
// not_found is ErrRoomNotFound for List and success for Delete/Remove.
func TestRoomAdminCalls(t *testing.T) {
	notFound := false
	srv, calls := fakeTwirp(t, func(string) (int, string) {
		if notFound {
			return 404, `{"code":"not_found","msg":"requested room does not exist"}`
		}
		return 200, `{"participants":[{"identity":"host","tracks":[{"sid":"TR_1"}]}]}`
	})
	defer srv.Close()
	c := New(Config{APIKey: "k", APISecret: "s", URL: srv.URL})
	ps, err := c.ListParticipants(context.Background(), "stream_r")
	if err != nil || len(ps) != 1 || ps[0].Identity != "host" || len(ps[0].Tracks) != 1 {
		t.Fatalf("list: %+v %v", ps, err)
	}
	if g := (*calls)[0].grant; g["room"] != "stream_r" || g["roomAdmin"] != true {
		t.Fatalf("list grant = %v", g)
	}
	if err := c.RemoveParticipant(context.Background(), "stream_r", "u1"); err != nil {
		t.Fatal(err)
	}
	if g := (*calls)[1].grant; g["room"] != "stream_r" {
		t.Fatalf("remove grant = %v", g)
	}
	notFound = true
	if _, err := c.ListParticipants(context.Background(), "stream_r"); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("list on a missing room: %v", err)
	}
	if err := c.DeleteRoom(context.Background(), "stream_r"); err != nil {
		t.Fatalf("delete on a missing room: %v", err)
	}
	if err := c.RemoveParticipant(context.Background(), "stream_r", "u1"); err != nil {
		t.Fatalf("remove from a missing room: %v", err)
	}
	if (*calls)[3].path != "/twirp/livekit.RoomService/DeleteRoom" || (*calls)[3].body["room"] != "stream_r" {
		t.Fatalf("delete call = %+v", (*calls)[3])
	}
}
