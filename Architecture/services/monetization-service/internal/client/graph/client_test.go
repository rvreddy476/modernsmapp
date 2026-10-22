package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestBlockedEitherWayAsksGraphAndReadsTheAnswer(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name    string
		answer  string
		status  int
		want    bool
		wantErr bool
	}{
		{"blocked", `{"blocked_user_ids":["` + b.String() + `"]}`, 200, true, false},
		{"not blocked", `{"blocked_user_ids":[]}`, 200, false, false},
		{"field missing is not a no", `{}`, 200, false, true},
		{"malformed", `nope`, 200, false, true},
		{"graph error", `{"error":{}}`, 500, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != BlockedAnyPath {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("X-Internal-Service-Key") != "k" {
					t.Errorf("internal key not sent")
				}
				var body struct {
					UserID       uuid.UUID   `json:"user_id"`
					CandidateIDs []uuid.UUID `json:"candidate_ids"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.UserID != a || len(body.CandidateIDs) != 1 || body.CandidateIDs[0] != b {
					t.Errorf("body = %+v", body)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.answer))
			}))
			defer srv.Close()
			got, err := New(srv.URL, "k").BlockedEitherWay(context.Background(), a, b)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got (%v, %v), want (%v, err=%v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestUnconfiguredClientIsAnError(t *testing.T) {
	if c := New("  ", "k"); c != nil {
		t.Fatalf("blank URL built a client")
	}
	var c *Client
	if _, err := c.BlockedEitherWay(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("a nil client answered")
	}
}

func TestTransportFailureIsAnError(t *testing.T) {
	if _, err := New("http://127.0.0.1:1", "").BlockedEitherWay(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("unreachable graph answered")
	}
}
