package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Match list paging (10 Nov 2026): GET /v1/dating/matches?limit=&cursor=.
// The refusals return before the store, so they run without a database;
// the page fixtures need TEST_PG_DSN (CI only).

var matchesPageFixtures = []string{
	"matches_get_200_page_first",
	"matches_get_200_page_last",
	"matches_get_400_invalid_limit",
	"matches_get_400_invalid_cursor",
}

func matchesNoDB(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(nil, nil)).RegisterRoutes(r)
	return contractDo(r, http.MethodGet, "/v1/dating/matches"+query, ``, uuid.New())
}

func TestMatchesPage_InvalidLimitRefused(t *testing.T) {
	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=abc", "?limit=", "?limit=1.5", "?status=quiet&limit=0"} {
		assertContract(t, matchesNoDB(t, q), http.StatusBadRequest, "matches_get_400_invalid_limit", nil)
	}
}

func TestMatchesPage_InvalidCursorRefused(t *testing.T) {
	for _, q := range []string{"?cursor=garbage", "?limit=10&cursor=" + uuid.NewString(), "?status=active&cursor=%25%25"} {
		assertContract(t, matchesNoDB(t, q), http.StatusBadRequest, "matches_get_400_invalid_cursor", nil)
	}
}

func TestParseMatchPageLimit(t *testing.T) {
	cases := []struct {
		raw     string
		present bool
		want    int
		ok      bool
	}{
		{"", false, store.DefaultMatchPageLimit, true},
		{"1", true, 1, true},
		{"100", true, 100, true},
		{"101", true, store.MaxMatchPageLimit, true},
		{"100000", true, store.MaxMatchPageLimit, true},
		{"0", true, 0, false},
		{"-5", true, 0, false},
		{"", true, 0, false},
		{"ten", true, 0, false},
	}
	for _, c := range cases {
		got, err := parseMatchPageLimit(c.raw, c.present)
		if (err == nil) != c.ok || (c.ok && got != c.want) {
			t.Errorf("parseMatchPageLimit(%q, %v) = %d, %v; want %d ok=%v", c.raw, c.present, got, err, c.want, c.ok)
		}
	}
}

func TestMatchesPageFixturesWellFormed(t *testing.T) {
	for _, name := range matchesPageFixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		if contractUUIDRe.Match(raw) || contractTimeRe.Match(raw) {
			t.Fatalf("%s: carries a raw uuid or timestamp", name)
		}
	}
	// The first page names its cursor; the last page has no meta at all,
	// exactly like the unpaged answer.
	first, _ := os.ReadFile(filepath.Join(contractsDir, "matches_get_200_page_first.json"))
	if !strings.Contains(string(first), `"next_cursor": "<cursor>"`) {
		t.Fatalf("first page fixture has no meta.next_cursor")
	}
	last, _ := os.ReadFile(filepath.Join(contractsDir, "matches_get_200_page_last.json"))
	if strings.Contains(string(last), `"meta"`) {
		t.Fatalf("last page fixture carries meta")
	}
}

// replaceBody swaps s for label in a recorded response, so an opaque cursor
// compares as a placeholder.
func replaceBody(rec *httptest.ResponseRecorder, s, label string) *httptest.ResponseRecorder {
	out := httptest.NewRecorder()
	out.Code = rec.Code
	out.Body.WriteString(strings.ReplaceAll(rec.Body.String(), s, label))
	return out
}

func TestMatchesPageContracts(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	// The viewer is user_a of both matches (d10Pair-ordered ids).
	var viewer uuid.UUID
	for {
		viewer = uuid.New()
		if strings.HasPrefix(viewer.String(), "0") {
			break
		}
	}
	mustSeedActiveProfile(t, st, viewer)
	mk := func() (uuid.UUID, uuid.UUID, uuid.UUID) {
		var other uuid.UUID
		for {
			other = uuid.New()
			if other.String() > viewer.String() {
				break
			}
		}
		mustSeedActiveProfile(t, st, other)
		id, _, err := st.CreateOrGetOpenMatch(ctx, viewer, other, map[string]any{"target_kind": "photo", "target_ref": "0"})
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		conv := uuid.New()
		if err := st.MarkMatchActive(ctx, id, conv); err != nil {
			t.Fatalf("activate: %v", err)
		}
		return id, other, conv
	}
	older, olderOther, olderConv := mk()
	newer, newerOther, newerConv := mk()

	rec := contractDo(r, http.MethodGet, "/v1/dating/matches?status=active&limit=1", ``, viewer)
	var env struct {
		Data []struct {
			ID uuid.UUID `json:"id"`
		} `json:"data"`
		Meta struct {
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(env.Data) != 1 || env.Data[0].ID != newer || env.Meta.NextCursor == "" {
		t.Fatalf("first page = %s, want the newer match and a cursor", rec.Body.String())
	}
	assertContract(t, replaceBody(rec, env.Meta.NextCursor, "<cursor>"), http.StatusOK, "matches_get_200_page_first",
		map[uuid.UUID]string{newer: "<match>", newerConv: "<conversation>", viewer: "<user_a>", newerOther: "<user_b>"})

	rec = contractDo(r, http.MethodGet, "/v1/dating/matches?status=active&limit=1&cursor="+env.Meta.NextCursor, ``, viewer)
	assertContract(t, rec, http.StatusOK, "matches_get_200_page_last",
		map[uuid.UUID]string{older: "<match>", olderConv: "<conversation>", viewer: "<user_a>", olderOther: "<user_b>"})
}
