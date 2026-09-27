package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// POST /v1/monetization/tips accepts creator_id as an alias for
// recipient_id (MTube web, 2026-09-27). Through the real router with
// writes on; only the ledger call is faked.

type fakeTipService struct {
	calls []service.SendTipInput
}

func (f *fakeTipService) SendTip(_ context.Context, in service.SendTipInput) (*service.TipResult, error) {
	f.calls = append(f.calls, in)
	return &service.TipResult{Tip: &postgres.Tip{ID: uuid.New(), SenderID: in.SenderID, RecipientID: in.RecipientID,
		AmountPaise: in.AmountPaise, Currency: service.TipCurrency, Status: "completed"}}, nil
}

func postTip(t *testing.T, fake *fakeTipService, sender, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := New(nil).WithWritesEnabled(true)
	h.tips = fake
	req := httptest.NewRequest(http.MethodPost, "/v1/monetization/tips", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", sender)
	w := httptest.NewRecorder()
	routerFor(h).ServeHTTP(w, req)
	return w
}

func TestSendTipAcceptsBothRecipientSpellings(t *testing.T) {
	sender, creator, other, post := uuid.NewString(), uuid.New(), uuid.New(), uuid.New()

	cases := []struct {
		name, body string
		want       uuid.UUID
	}{
		{"recipient_id", `{"recipient_id":"` + creator.String() + `","amount_paise":500}`, creator},
		{"creator_id alias (the web's body)", `{"creator_id":"` + creator.String() + `","post_id":"` + post.String() + `","amount_paise":500,"message":"thanks"}`, creator},
		{"both: recipient_id wins", `{"recipient_id":"` + creator.String() + `","creator_id":"` + other.String() + `","amount_paise":500}`, creator},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeTipService{}
			w := postTip(t, fake, sender, tc.body)
			if w.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(fake.calls) != 1 || fake.calls[0].RecipientID != tc.want || fake.calls[0].AmountPaise != 500 {
				t.Fatalf("SendTip got %+v, want recipient %s", fake.calls, tc.want)
			}
		})
	}

	t.Run("the web body carries post_id and message through", func(t *testing.T) {
		fake := &fakeTipService{}
		postTip(t, fake, sender, cases[1].body)
		if len(fake.calls) != 1 || fake.calls[0].PostID == nil || *fake.calls[0].PostID != post || fake.calls[0].Message != "thanks" {
			t.Fatalf("SendTip got %+v", fake.calls)
		}
	})
}

func TestSendTipRefusesAMissingOrBadRecipientBeforeTheLedger(t *testing.T) {
	sender := uuid.NewString()
	cases := map[string]struct{ body, mention string }{
		"neither":          {`{"amount_paise":500}`, "recipient_id (or its alias creator_id) is required"},
		"both empty":       {`{"recipient_id":"","creator_id":"","amount_paise":500}`, "is required"},
		"bad creator_id":   {`{"creator_id":"nope","amount_paise":500}`, "creator_id must be a UUID"},
		"bad recipient_id": {`{"recipient_id":"nope","creator_id":"` + uuid.NewString() + `","amount_paise":500}`, "recipient_id must be a UUID"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeTipService{}
			w := postTip(t, fake, sender, tc.body)
			if w.Code != http.StatusBadRequest || len(fake.calls) != 0 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, len(fake.calls), w.Body.String())
			}
			var env struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &env)
			if env.Error.Code != "INVALID_REQUEST" || !strings.Contains(env.Error.Message, tc.mention) {
				t.Fatalf("error %+v, want INVALID_REQUEST mentioning %q", env.Error, tc.mention)
			}
		})
	}
}
