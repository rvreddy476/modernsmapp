package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Lane D10 — the two live-location list views.
func TestLocationShareLists(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()
	sharer, recipient := uuid.New(), uuid.New()
	mustSeedActiveProfile(t, st, sharer)
	mustSeedActiveProfile(t, st, recipient)
	// A share needs a current match (or a trusted contact).
	matchID, _, err := st.CreateOrGetOpenMatch(ctx, sharer, recipient, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	body := `{"recipient_id":"` + recipient.String() + `","duration_minutes":30,"latitude":17.44,"longitude":78.39}`
	rec := contractDo(r, http.MethodPost, "/v1/dating/safety/share-location", body, sharer)
	if rec.Code != http.StatusOK {
		t.Fatalf("share: status %d body %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ShareID string `json:"share_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	type listResp struct {
		Data struct {
			Items []struct {
				ShareID   string `json:"share_id"`
				UserID    string `json:"user_id"`
				ExpiresAt string `json:"expires_at"`
				Person    *struct {
					UserID string `json:"user_id"`
				} `json:"person"`
				Recipient *struct {
					UserID string `json:"user_id"`
				} `json:"recipient"`
			} `json:"items"`
		} `json:"data"`
	}

	// Outgoing, for the sharer.
	rec = contractDo(r, http.MethodGet, "/v1/dating/safety/share-location", ``, sharer)
	if rec.Code != http.StatusOK {
		t.Fatalf("outgoing list: status %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "latitude") || strings.Contains(rec.Body.String(), "17.44") {
		t.Fatalf("outgoing list carries coordinates: %s", rec.Body.String())
	}
	var out listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode outgoing: %v", err)
	}
	if len(out.Data.Items) != 1 || out.Data.Items[0].ShareID != created.Data.ShareID ||
		out.Data.Items[0].Recipient == nil || out.Data.Items[0].Recipient.UserID != recipient.String() {
		t.Fatalf("outgoing list: %s", rec.Body.String())
	}

	// Incoming, for the recipient: ids, sharer card, expiry, no point.
	rec = contractDo(r, http.MethodGet, "/v1/dating/safety/shared-locations", ``, recipient)
	if rec.Code != http.StatusOK {
		t.Fatalf("incoming list: status %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "latitude") {
		t.Fatalf("incoming list carries coordinates: %s", rec.Body.String())
	}
	var in listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &in); err != nil {
		t.Fatalf("decode incoming: %v", err)
	}
	if len(in.Data.Items) != 1 || in.Data.Items[0].ShareID != created.Data.ShareID ||
		in.Data.Items[0].Person == nil || in.Data.Items[0].Person.UserID != sharer.String() ||
		in.Data.Items[0].ExpiresAt == "" {
		t.Fatalf("incoming list: %s", rec.Body.String())
	}

	// A stranger sees neither.
	stranger := uuid.New()
	mustSeedActiveProfile(t, st, stranger)
	rec = contractDo(r, http.MethodGet, "/v1/dating/safety/shared-locations", ``, stranger)
	var none listResp
	_ = json.Unmarshal(rec.Body.Bytes(), &none)
	if len(none.Data.Items) != 0 {
		t.Fatalf("stranger sees shares: %s", rec.Body.String())
	}

	// Stopping the share empties both lists.
	if rec := contractDo(r, http.MethodDelete, "/v1/dating/safety/share-location/"+created.Data.ShareID, ``, sharer); rec.Code != http.StatusOK {
		t.Fatalf("stop: status %d body %s", rec.Code, rec.Body.String())
	}
	rec = contractDo(r, http.MethodGet, "/v1/dating/safety/share-location", ``, sharer)
	var after listResp
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	if len(after.Data.Items) != 0 {
		t.Fatalf("stopped share still listed: %s", rec.Body.String())
	}
	rec = contractDo(r, http.MethodGet, "/v1/dating/safety/shared-locations", ``, recipient)
	var afterIn listResp
	_ = json.Unmarshal(rec.Body.Bytes(), &afterIn)
	if len(afterIn.Data.Items) != 0 {
		t.Fatalf("stopped share still listed for the recipient: %s", rec.Body.String())
	}
}
