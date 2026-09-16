package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Golden contract fixtures for lane D3 (pass, decline, spark refusals, the
// match list), following food-service's testdata/contracts pattern. The
// dating handler runs on the real store, so these cases need TEST_PG_DSN;
// ids and timestamps are replaced with stable placeholders before the
// compare. Regenerate deliberately with UPDATE_CONTRACTS=1 and review.

const contractsDir = "testdata/contracts"

var d3Fixtures = []string{
	"pulse_pass_post_200",
	"spark_decline_post_200",
	"spark_create_404_candidate_unavailable",
	"spark_create_429_rate_limited",
	"matches_get_200",
}

var (
	contractUUIDRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	contractTimeRe = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T[0-9:.]+(?:Z|[+-]\d{2}:\d{2})"`)
)

// normaliseContract swaps known ids for labels, any other uuid for <uuid>
// and RFC 3339 timestamps for <timestamp>.
func normaliseContract(body []byte, labels map[uuid.UUID]string) []byte {
	s := string(body)
	for id, label := range labels {
		s = strings.ReplaceAll(s, id.String(), label)
	}
	s = contractUUIDRe.ReplaceAllString(s, "<uuid>")
	s = contractTimeRe.ReplaceAllString(s, `"<timestamp>"`)
	return []byte(s)
}

func assertContract(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, fixture string, labels map[uuid.UUID]string) {
	t.Helper()
	path := filepath.Join(contractsDir, fixture+".json")
	if rec.Code != wantStatus {
		t.Fatalf("%s: status = %d, want %d (body %s)", fixture, rec.Code, wantStatus, rec.Body.String())
	}
	body := normaliseContract(bytes.TrimSpace(rec.Body.Bytes()), labels)
	if os.Getenv("UPDATE_CONTRACTS") == "1" {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, body, "", "  "); err != nil {
			t.Fatalf("%s: indent: %v", fixture, err)
		}
		pretty.WriteByte('\n')
		if err := os.MkdirAll(contractsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: missing fixture (run with UPDATE_CONTRACTS=1 and review): %v", fixture, err)
	}
	var got, want any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%s: response is not JSON: %v", fixture, err)
	}
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("%s: fixture is not JSON: %v", fixture, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: response drifted from %s\n got: %s", fixture, path, body)
	}
}

func contractDo(r http.Handler, method, path, body string, user uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", user.String())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func sparkBody(to uuid.UUID, ref string) string {
	return fmt.Sprintf(`{"to_user_id":%q,"target_kind":"prompt","target_ref":%q}`, to.String(), ref)
}

func TestD3Contracts(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("pulse_pass_post_200", func(t *testing.T) {
		viewer, candidate := uuid.New(), uuid.New()
		mustSeedActiveProfile(t, st, viewer)
		mustSeedActiveProfile(t, st, candidate)
		rec := contractDo(r, http.MethodPost, "/v1/dating/pulse/"+candidate.String()+"/pass", `{"reason":"not_my_type"}`, viewer)
		assertContract(t, rec, http.StatusOK, "pulse_pass_post_200", map[uuid.UUID]string{viewer: "<viewer>", candidate: "<candidate>"})
		// Idempotent: a repeat answers the same contract.
		rec = contractDo(r, http.MethodPost, "/v1/dating/pulse/"+candidate.String()+"/pass", ``, viewer)
		assertContract(t, rec, http.StatusOK, "pulse_pass_post_200", map[uuid.UUID]string{viewer: "<viewer>", candidate: "<candidate>"})
	})

	t.Run("spark_decline_post_200", func(t *testing.T) {
		sender, recipient := uuid.New(), uuid.New()
		mustSeedActiveProfile(t, st, sender)
		mustSeedActiveProfile(t, st, recipient)
		sp, err := st.CreateSpark(ctx, sender, recipient, "photo", "0", "hello")
		if err != nil {
			t.Fatalf("seed spark: %v", err)
		}
		labels := map[uuid.UUID]string{sp.ID: "<spark>", sender: "<sender>", recipient: "<recipient>"}
		rec := contractDo(r, http.MethodPost, "/v1/dating/sparks/"+sp.ID.String()+"/decline", ``, sender)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("sender decline: status %d, want 404", rec.Code)
		}
		rec = contractDo(r, http.MethodPost, "/v1/dating/sparks/"+sp.ID.String()+"/decline", ``, recipient)
		assertContract(t, rec, http.StatusOK, "spark_decline_post_200", labels)
		rec = contractDo(r, http.MethodPost, "/v1/dating/sparks/"+sp.ID.String()+"/decline", ``, recipient)
		assertContract(t, rec, http.StatusOK, "spark_decline_post_200", labels)
	})

	t.Run("spark_create_404_candidate_unavailable", func(t *testing.T) {
		from, to := uuid.New(), uuid.New()
		mustSeedActiveProfile(t, st, from)
		mustSeedActiveProfile(t, st, to)
		if _, err := st.TransitionProfileStatus(ctx, to, store.ProfileEventSuspend, store.ProfileActorAdmin); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		rec := contractDo(r, http.MethodPost, "/v1/dating/sparks", sparkBody(to, "p1"), from)
		assertContract(t, rec, http.StatusNotFound, "spark_create_404_candidate_unavailable", map[uuid.UUID]string{from: "<sender>", to: "<recipient>"})

		// A block answers exactly the same, so it is not revealed.
		blocker, blocked := uuid.New(), uuid.New()
		mustSeedActiveProfile(t, st, blocker)
		mustSeedActiveProfile(t, st, blocked)
		if err := st.BlockUser(ctx, blocker, blocked); err != nil {
			t.Fatalf("block: %v", err)
		}
		rec = contractDo(r, http.MethodPost, "/v1/dating/sparks", sparkBody(blocker, "p1"), blocked)
		assertContract(t, rec, http.StatusNotFound, "spark_create_404_candidate_unavailable", map[uuid.UUID]string{blocked: "<sender>", blocker: "<recipient>"})

		// A decline within the cooldown answers exactly the same too.
		sender, decliner := uuid.New(), uuid.New()
		mustSeedActiveProfile(t, st, sender)
		mustSeedActiveProfile(t, st, decliner)
		sp, err := st.CreateSpark(ctx, sender, decliner, "photo", "0", "")
		if err != nil {
			t.Fatalf("seed spark: %v", err)
		}
		if _, err := st.DeclineSpark(ctx, sp.ID, decliner); err != nil {
			t.Fatalf("decline: %v", err)
		}
		rec = contractDo(r, http.MethodPost, "/v1/dating/sparks", sparkBody(decliner, "p1"), sender)
		assertContract(t, rec, http.StatusNotFound, "spark_create_404_candidate_unavailable", map[uuid.UUID]string{sender: "<sender>", decliner: "<recipient>"})
		// One-directional: the decliner can still spark the sender.
		rec = contractDo(r, http.MethodPost, "/v1/dating/sparks", sparkBody(sender, "p1"), decliner)
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("decliner spark to the sender: status %d body %s", rec.Code, rec.Body.String())
		}
		// That spark lifts the cooldown: the sender's next spark succeeds and
		// meets the decliner's to form a match.
		rec = contractDo(r, http.MethodPost, "/v1/dating/sparks", sparkBody(decliner, "p2"), sender)
		if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"matched":true`) {
			t.Fatalf("sender spark after the lift: status %d body %s; want 201 with a match", rec.Code, rec.Body.String())
		}
	})

	t.Run("spark_create_429_rate_limited", func(t *testing.T) {
		from, to := uuid.New(), uuid.New()
		mustSeedActiveProfile(t, st, from)
		mustSeedActiveProfile(t, st, to)
		for i := 0; i < service.DefaultSparkDailyLimit; i++ {
			if _, err := st.CreateSparkWithQuota(ctx, from, to, "prompt", fmt.Sprintf("p%d", i), "", service.DefaultSparkDailyLimit); err != nil {
				t.Fatalf("seed spark %d: %v", i, err)
			}
		}
		rec := contractDo(r, http.MethodPost, "/v1/dating/sparks", sparkBody(to, "overflow"), from)
		assertContract(t, rec, http.StatusTooManyRequests, "spark_create_429_rate_limited", map[uuid.UUID]string{from: "<sender>", to: "<recipient>"})
	})

	t.Run("matches_get_200", func(t *testing.T) {
		// The viewer is always user_a (d10Pair orders the ids), so the
		// person card in the fixture is always user_b.
		x, y := d10Pair()
		mustSeedActiveProfile(t, st, x)
		mustSeedActiveProfile(t, st, y)
		id, _, err := st.CreateOrGetOpenMatch(ctx, x, y, map[string]any{"target_kind": "photo", "target_ref": "0"})
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		conv := uuid.New()
		if err := st.MarkMatchActive(ctx, id, conv); err != nil {
			t.Fatalf("activate: %v", err)
		}
		// user_a/user_b are canonical, so label by position, not by caller.
		userA, userB := x, y
		if y.String() < x.String() {
			userA, userB = y, x
		}
		labels := map[uuid.UUID]string{id: "<match>", conv: "<conversation>", userA: "<user_a>", userB: "<user_b>"}
		rec := contractDo(r, http.MethodGet, "/v1/dating/matches", ``, x)
		assertContract(t, rec, http.StatusOK, "matches_get_200", labels)
	})
}

// TestD3ContractFixturesWellFormed runs without a database: every fixture
// exists, is JSON with a data or error member, and carries no raw id or
// timestamp.
func TestD3ContractFixturesWellFormed(t *testing.T) {
	for _, name := range d3Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if _, ok := doc["data"]; !ok {
			if _, ok := doc["error"]; !ok {
				t.Fatalf("%s: neither data nor error", name)
			}
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
		if strings.Contains(string(raw), "declined_at") {
			t.Fatalf("%s: exposes declined_at", name)
		}
	}
}
