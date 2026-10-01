package livekit

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const testKey = "sk_SECRET_test_key"

// TestCreateRTMPIngressRequest: the path, the ingressAdmin grant (and no
// room grant), the request's proto field names, and the answer decoded.
func TestCreateRTMPIngressRequest(t *testing.T) {
	srv, calls := fakeTwirp(t, func(string) (int, string) {
		return 200, `{"ingress_id":"IN_1","name":"live_x","stream_key":"` + testKey + `","url":"rtmps://x.rtmp.livekit.cloud/x","input_type":"RTMP_INPUT","room_name":"stream_r","participant_identity":"encoder_s","reusable":true}`
	})
	defer srv.Close()
	c := New(Config{APIKey: "k", APISecret: "s", URL: srv.URL})
	ing, err := c.CreateRTMPIngress(context.Background(), IngressRequest{Name: "live_x", Room: "stream_r", Identity: "encoder_s", ParticipantName: "Host"})
	if err != nil {
		t.Fatal(err)
	}
	if ing.ID != "IN_1" || ing.URL != "rtmps://x.rtmp.livekit.cloud/x" || ing.StreamKey != testKey {
		t.Fatalf("ingress = %#v / %q", ing, ing.StreamKey)
	}
	call := (*calls)[0]
	if call.path != "/twirp/livekit.Ingress/CreateIngress" {
		t.Fatalf("path = %s", call.path)
	}
	want := map[string]any{
		"input_type": "RTMP_INPUT", "name": "live_x", "room_name": "stream_r",
		"participant_identity": "encoder_s", "participant_name": "Host",
	}
	if len(call.body) != len(want) {
		t.Fatalf("body = %v", call.body)
	}
	for k, v := range want {
		if call.body[k] != v {
			t.Fatalf("body[%s] = %v, want %v (body %v)", k, call.body[k], v, call.body)
		}
	}
	if len(call.grant) != 1 || call.grant["ingressAdmin"] != true {
		t.Fatalf("grant = %v, want exactly ingressAdmin", call.grant)
	}

	// A room and an identity are required before anything is sent.
	if _, err := c.CreateRTMPIngress(context.Background(), IngressRequest{Room: "stream_r"}); err == nil || len(*calls) != 1 {
		t.Fatalf("an ingress without an identity was requested: %v", err)
	}
	// Not configured: an error, no call.
	if _, err := New(Config{URL: srv.URL}).CreateRTMPIngress(context.Background(), IngressRequest{Room: "r", Identity: "i"}); err == nil || len(*calls) != 1 {
		t.Fatalf("unconfigured client: %v", err)
	}
}

// TestCreateRTMPIngressRefusedOrIncomplete: a twirp error is an error that
// carries no key, and an answer without id, url or key is not handed on.
func TestCreateRTMPIngressRefusedOrIncomplete(t *testing.T) {
	answer := `{"code":"resource_exhausted","msg":"ingress limit reached"}`
	code := 429
	srv, _ := fakeTwirp(t, func(string) (int, string) { return code, answer })
	defer srv.Close()
	c := New(Config{APIKey: "k", APISecret: "s", URL: srv.URL})
	req := IngressRequest{Room: "stream_r", Identity: "encoder_s"}
	if _, err := c.CreateRTMPIngress(context.Background(), req); err == nil || !strings.Contains(err.Error(), "resource_exhausted") {
		t.Fatalf("refused: %v", err)
	}
	code = 200
	for _, incomplete := range []string{
		`{"ingress_id":"IN_1","url":"rtmp://x"}`,
		`{"ingress_id":"IN_1","stream_key":"` + testKey + `"}`,
		`{"stream_key":"` + testKey + `","url":"rtmp://x"}`,
		`{}`,
	} {
		answer = incomplete
		ing, err := c.CreateRTMPIngress(context.Background(), req)
		if err == nil || ing != nil {
			t.Fatalf("incomplete answer %s was accepted: %+v", incomplete, ing)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("the error carries the key: %v", err)
		}
	}
}

// TestGetIngress: ListIngress filtered by ingress_id; the item's key comes
// back; nothing listed, a not_found, or another ingress is nil.
func TestGetIngress(t *testing.T) {
	answer := `{"items":[{"ingress_id":"IN_1","stream_key":"` + testKey + `","url":"rtmps://x/x"}],"next_page_token":null}`
	code := 200
	srv, calls := fakeTwirp(t, func(string) (int, string) { return code, answer })
	defer srv.Close()
	c := New(Config{APIKey: "k", APISecret: "s", URL: srv.URL})
	ing, err := c.GetIngress(context.Background(), "IN_1")
	if err != nil || ing == nil || ing.ID != "IN_1" || ing.StreamKey != testKey || ing.URL != "rtmps://x/x" {
		t.Fatalf("get: %v %v", ing, err)
	}
	call := (*calls)[0]
	if call.path != "/twirp/livekit.Ingress/ListIngress" || len(call.body) != 1 || call.body["ingress_id"] != "IN_1" {
		t.Fatalf("call = %+v", call)
	}
	if len(call.grant) != 1 || call.grant["ingressAdmin"] != true {
		t.Fatalf("grant = %v", call.grant)
	}

	// lowerCamelCase (a server built with jsonCamelCase) decodes too.
	answer = `{"items":[{"ingressId":"IN_1","streamKey":"` + testKey + `","url":"rtmps://x/x"}]}`
	if ing, err := c.GetIngress(context.Background(), "IN_1"); err != nil || ing == nil || ing.StreamKey != testKey {
		t.Fatalf("camelCase: %v %v", ing, err)
	}
	// LiveKit lists a different ingress: never handed back.
	answer = `{"items":[{"ingress_id":"IN_OTHER","stream_key":"` + testKey + `","url":"rtmps://x/x"}]}`
	if ing, err := c.GetIngress(context.Background(), "IN_1"); err != nil || ing != nil {
		t.Fatalf("another ingress was returned: %v %v", ing, err)
	}
	answer = `{"items":[]}`
	if ing, err := c.GetIngress(context.Background(), "IN_1"); err != nil || ing != nil {
		t.Fatalf("empty list: %v %v", ing, err)
	}
	code, answer = 404, `{"code":"not_found","msg":"ingress does not exist"}`
	if ing, err := c.GetIngress(context.Background(), "IN_1"); err != nil || ing != nil {
		t.Fatalf("not found: %v %v", ing, err)
	}
	code, answer = 500, `{"code":"internal","msg":"boom"}`
	if _, err := c.GetIngress(context.Background(), "IN_1"); err == nil {
		t.Fatal("a LiveKit error read as 'no ingress'")
	}
	n := len(*calls)
	if ing, err := c.GetIngress(context.Background(), ""); err != nil || ing != nil || len(*calls) != n {
		t.Fatalf("empty id: %v %v", ing, err)
	}
}

// TestDeleteIngress: the path, the body and the grant; an ingress that no
// longer exists is not an error, any other failure is.
func TestDeleteIngress(t *testing.T) {
	code, answer := 200, `{"ingress_id":"IN_1"}`
	srv, calls := fakeTwirp(t, func(string) (int, string) { return code, answer })
	defer srv.Close()
	c := New(Config{APIKey: "k", APISecret: "s", URL: srv.URL})
	if err := c.DeleteIngress(context.Background(), "IN_1"); err != nil {
		t.Fatal(err)
	}
	call := (*calls)[0]
	if call.path != "/twirp/livekit.Ingress/DeleteIngress" || len(call.body) != 1 || call.body["ingress_id"] != "IN_1" {
		t.Fatalf("call = %+v", call)
	}
	if len(call.grant) != 1 || call.grant["ingressAdmin"] != true {
		t.Fatalf("grant = %v", call.grant)
	}
	code, answer = 404, `{"code":"not_found","msg":"ingress does not exist"}`
	if err := c.DeleteIngress(context.Background(), "IN_1"); err != nil {
		t.Fatalf("deleting a missing ingress: %v", err)
	}
	code, answer = 503, `{"code":"unavailable","msg":"no ingress service"}`
	if err := c.DeleteIngress(context.Background(), "IN_1"); err == nil {
		t.Fatal("a failed delete read as deleted")
	}
	n := len(*calls)
	if err := c.DeleteIngress(context.Background(), ""); err != nil || len(*calls) != n {
		t.Fatalf("empty id: %v", err)
	}
}

// TestRoomCallsKeepTheirGrant: the room-admin calls do not gain ingressAdmin.
func TestRoomCallsKeepTheirGrant(t *testing.T) {
	srv, calls := fakeTwirp(t, ok)
	defer srv.Close()
	c := New(Config{APIKey: "k", APISecret: "s", URL: srv.URL})
	if err := c.CreateRoom(context.Background(), "stream_r"); err != nil {
		t.Fatal(err)
	}
	g := (*calls)[0].grant
	if g["roomAdmin"] != true || g["roomCreate"] != true {
		t.Fatalf("room grant = %v", g)
	}
	if _, has := g["ingressAdmin"]; has {
		t.Fatalf("a room call carries ingressAdmin: %v", g)
	}
}

// TestIngressNeverPrintsItsKey: fmt and slog get the id and the URL only.
func TestIngressNeverPrintsItsKey(t *testing.T) {
	ing := Ingress{ID: "IN_1", URL: "rtmps://x/x", StreamKey: testKey}
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	log.Info("ingress", "value", ing, "pointer", &ing)
	out := fmt.Sprintf("%v|%+v|%#v|%s|%v|%+v|%#v|%s", ing, ing, ing, ing, &ing, &ing, &ing, &ing) + logs.String()
	if strings.Contains(out, testKey) {
		t.Fatalf("the key was printed: %s", out)
	}
	if !strings.Contains(out, "IN_1") || !strings.Contains(logs.String(), "IN_1") {
		t.Fatalf("the id is missing: %s", out)
	}
}
