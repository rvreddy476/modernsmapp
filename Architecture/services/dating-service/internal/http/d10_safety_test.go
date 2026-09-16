package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/store"
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

// Lane D10 follow-up — GET /v1/dating/safety/trusted-contacts carries the
// same compact person card a match and an incoming spark carry, so the app
// can name a contact instead of falling back to "Your match". A contact
// whose profile is gone still lists, with person null.
func TestTrustedContactsCarryPersonCards(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()
	user, contact, gone := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{user, contact, gone} {
		mustSeedActiveProfile(t, st, id)
	}
	// The contact has a real location, so there ARE coordinates to leak.
	lat, lng := 17.44, 78.39
	if _, err := st.UpsertProfile(ctx, contact, store.UpsertProfileParams{Latitude: &lat, Longitude: &lng}); err != nil {
		t.Fatalf("seed location: %v", err)
	}
	// Seeded at the store, so eligibility is not what this test is about.
	for _, id := range []uuid.UUID{contact, gone} {
		if _, _, err := st.UpsertTrustedContact(ctx, user, id, true); err != nil {
			t.Fatalf("seed trusted contact: %v", err)
		}
	}
	// The second contact's profile is gone (deleted or purged).
	if err := st.SoftDeleteProfile(ctx, gone); err != nil {
		t.Fatalf("delete profile: %v", err)
	}

	rec := contractDo(r, http.MethodGet, "/v1/dating/safety/trusted-contacts", ``, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Items []struct {
				ContactID            string `json:"contact_id"`
				ShareLocationOnPanic bool   `json:"share_location_on_panic"`
				Person               *struct {
					UserID     string `json:"user_id"`
					FirstName  string `json:"first_name"`
					Age        int    `json:"age"`
					PhotoState string `json:"photo_state"`
					Verified   bool   `json:"verified"`
				} `json:"person"`
			} `json:"items"`
			Max int `json:"max"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Items) != 2 {
		t.Fatalf("items = %d, want 2: %s", len(resp.Data.Items), rec.Body.String())
	}
	seen := 0
	for _, it := range resp.Data.Items {
		// Every existing field survives.
		if !it.ShareLocationOnPanic {
			t.Fatalf("share_location_on_panic lost for %s: %s", it.ContactID, rec.Body.String())
		}
		switch it.ContactID {
		case contact.String():
			seen++
			if it.Person == nil {
				t.Fatalf("live contact has no person card: %s", rec.Body.String())
			}
			if it.Person.UserID != contact.String() || it.Person.FirstName != "Asha" || it.Person.Age <= 0 {
				t.Fatalf("person card = %+v: %s", *it.Person, rec.Body.String())
			}
			if it.Person.PhotoState != "full" && it.Person.PhotoState != "blurred" {
				t.Fatalf("photo_state = %q", it.Person.PhotoState)
			}
		case gone.String():
			seen++
			// The fallback the app applies: listed, but no card.
			if it.Person != nil {
				t.Fatalf("gone profile still has a person card: %s", rec.Body.String())
			}
		default:
			t.Fatalf("unexpected contact %s", it.ContactID)
		}
	}
	if seen != 2 {
		t.Fatalf("saw %d of the 2 contacts: %s", seen, rec.Body.String())
	}
	if resp.Data.Max != 3 {
		t.Fatalf("max = %d, want 3", resp.Data.Max)
	}
	// The card is the compact one: no sealed, sensitive or private field.
	body := rec.Body.String()
	for _, leak := range []string{"religion", "community", "latitude", "longitude", "geohash",
		"birth_date", "last_active_at", "embedding", "bio", "phone", "email"} {
		if strings.Contains(body, `"`+leak+`":`) {
			t.Fatalf("trusted contacts leak %s: %s", leak, body)
		}
	}
}
