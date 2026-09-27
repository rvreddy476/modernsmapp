package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /v1/monetization/creators/:creatorId/support (MTube watch page,
// 2026-09-27) through the REAL router and launch boundary; only the tier
// read is faked.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestCreatorSupport
//
// regenerates the fixture; review the diff.

type fakeTiers struct {
	tiers []postgres.CreatorTier
	err   error
	asked []uuid.UUID
}

func (f *fakeTiers) GetCreatorTiers(_ context.Context, creatorID uuid.UUID) ([]postgres.CreatorTier, error) {
	f.asked = append(f.asked, creatorID)
	return f.tiers, f.err
}

var fxSupportCreator = uuid.MustParse("22222222-2222-4222-8222-222222222222")

func supportTiers() *fakeTiers {
	return &fakeTiers{tiers: []postgres.CreatorTier{
		{ID: uuid.New(), CreatorID: fxSupportCreator, Name: "Fan", PricePaise: 4900, IsActive: true},
		{ID: uuid.New(), CreatorID: fxSupportCreator, Name: "Super fan", PricePaise: 19900, IsActive: true},
		{ID: uuid.New(), CreatorID: fxSupportCreator, Name: "Retired", PricePaise: 9900, IsActive: false},
	}}
}

func getSupport(t *testing.T, h *Handler, creator, viewer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/monetization/creators/"+creator+"/support", nil)
	if viewer != "" {
		req.Header.Set("X-User-Id", viewer)
	}
	w := httptest.NewRecorder()
	routerFor(h).ServeHTTP(w, req)
	return w
}

func decodeSupport(t *testing.T, w *httptest.ResponseRecorder) CreatorSupport {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data CreatorSupport `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("not the envelope: %v %s", err, w.Body.String())
	}
	return env.Data
}

func TestCreatorSupportMatchesTheFixture(t *testing.T) {
	h := New(nil).WithWritesEnabled(true)
	h.tiers = supportTiers()
	w := getSupport(t, h, fxSupportCreator.String(), uuid.NewString())
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, w.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join("testdata", "contracts", "mtube", "creator_support.json")
	if os.Getenv("UPDATE_CONTRACTS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture: %v (UPDATE_CONTRACTS=1 to create)", err)
	}
	var got, exp any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if err := json.Unmarshal(want, &exp); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	gotB, _ := json.Marshal(got)
	expB, _ := json.Marshal(exp)
	if !bytes.Equal(gotB, expB) {
		t.Fatalf("response differs from %s\n got: %s", path, pretty.String())
	}
}

// In beta (writes off, the default everywhere) the read must ANSWER — its
// job is to say tips are off — never meet the boundary's 503.
func TestCreatorSupportAnswersInBetaWithTipsOff(t *testing.T) {
	for name, h := range map[string]*Handler{
		"beta":        New(nil).WithWritesEnabled(false),
		"maintenance": New(nil).WithWritesEnabled(false).WithMaintenance(true),
	} {
		t.Run(name, func(t *testing.T) {
			h.tiers = supportTiers()
			got := decodeSupport(t, getSupport(t, h, fxSupportCreator.String(), uuid.NewString()))
			if got.TipsEnabled {
				t.Fatalf("tips_enabled=true while POST /tips is closed: %+v", got)
			}
			if got.MinTipPaise != 100 || got.Currency != "INR" || got.MembershipTiers != 2 {
				t.Fatalf("support: %+v", got)
			}
		})
	}
}

func TestCreatorSupportTipsFollowSendTipsRecipientRule(t *testing.T) {
	h := New(nil).WithWritesEnabled(true)
	h.tiers = supportTiers()
	creator := fxSupportCreator.String()

	if got := decodeSupport(t, getSupport(t, h, creator, uuid.NewString())); !got.TipsEnabled {
		t.Fatalf("a fan cannot tip with writes on: %+v", got)
	}
	if got := decodeSupport(t, getSupport(t, h, creator, "")); !got.TipsEnabled {
		t.Fatalf("an anonymous viewer must still see the button (tipping prompts sign-in): %+v", got)
	}
	// validateTipInput: CANNOT_TIP_SELF.
	if got := decodeSupport(t, getSupport(t, h, creator, creator)); got.TipsEnabled {
		t.Fatalf("the creator is offered a tip to themselves: %+v", got)
	}
}

func TestCreatorSupportRefusesABadIDBeforeTheStore(t *testing.T) {
	h := New(nil).WithWritesEnabled(true)
	tiers := supportTiers()
	h.tiers = tiers
	for _, id := range []string{"not-a-uuid", uuid.Nil.String()} {
		w := getSupport(t, h, id, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", id, w.Code, w.Body.String())
		}
	}
	if len(tiers.asked) != 0 {
		t.Fatalf("a bad id reached the tier read: %v", tiers.asked)
	}
	tiers.err = errors.New("db down")
	if w := getSupport(t, h, fxSupportCreator.String(), ""); w.Code != http.StatusInternalServerError {
		t.Fatalf("tier read failure: status=%d", w.Code)
	}
}

// tips_enabled is not a second copy of the boundary: under every flag
// combination the runmode allows, it equals "POST /v1/monetization/tips is
// let through the real launchBoundary middleware".
func TestTipsEnabledMatchesTheLaunchBoundary(t *testing.T) {
	combos := []struct {
		name                string
		writes, maintenance bool
	}{
		{"beta", false, false},
		{"writes on", true, false},
		{"maintenance", false, true},
		{"maintenance over writes", true, true},
	}
	for _, c := range combos {
		t.Run(c.name, func(t *testing.T) {
			h := New(nil).WithWritesEnabled(c.writes).WithMaintenance(c.maintenance)
			w := serveWithBoundary(t, h, http.MethodPost, sendTipPattern, sendTipPattern, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			admitted := w.Code == http.StatusNoContent
			if got := h.tipsEnabledFor(fxSupportCreator, uuid.New()); got != admitted {
				t.Fatalf("tips_enabled=%v but the boundary admitted=%v (status %d)", got, admitted, w.Code)
			}
		})
	}
}
