package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Golden contract fixtures for lane D7 (location precision and privacy): the
// location change 429, the invalid location 400, a deck card and an explain
// carrying buckets only, the explain 404 and the privacy defaults. Same
// harness as d3_contract_test.go; regenerate with UPDATE_CONTRACTS=1 and
// review.

var d7Fixtures = []string{
	"profile_upsert_429_location_change_rate_limited",
	"profile_upsert_400_invalid_location",
	"pulse_today_get_200",
	"pulse_explain_get_200",
	"pulse_explain_404_candidate_unavailable",
	"privacy_get_200",
}

var (
	d7KmFigureRe   = regexp.MustCompile(`\d\s*km`)
	d7BucketLabels = []string{"Less than 5 km", "< 5 km", "5–10 km", "10–25 km", "25+ km"}
)

// d7PreciseLocationProblems walks a decoded JSON document: every key naming a
// distance must hold a string (a bucket), no coordinate, geohash or
// last_active_at key may appear, and no string may name a km figure other
// than a bucket label.
func d7PreciseLocationProblems(doc any) []string {
	var problems []string
	var walk func(path string, n any)
	walk = func(path string, n any) {
		switch x := n.(type) {
		case map[string]any:
			for k, v := range x {
				lk, p := strings.ToLower(k), path+"."+k
				switch {
				case strings.Contains(lk, "distance"):
					if _, ok := v.(string); !ok {
						problems = append(problems, p+" is not a bucket string")
					}
				case lk == "latitude" || lk == "longitude" || lk == "lat" || lk == "lng" || lk == "lon" ||
					strings.Contains(lk, "geohash") || lk == "last_active_at":
					problems = append(problems, p+" is present")
				}
				walk(p, v)
			}
		case []any:
			for _, e := range x {
				walk(path+"[]", e)
			}
		case string:
			s := x
			for _, label := range d7BucketLabels {
				s = strings.ReplaceAll(s, label, "")
			}
			if d7KmFigureRe.MatchString(s) {
				problems = append(problems, path+" names a km figure: "+x)
			}
		}
	}
	walk("$", doc)
	return problems
}

func d7Rewrite(rec *httptest.ResponseRecorder, old, replacement string) *httptest.ResponseRecorder {
	out := httptest.NewRecorder()
	out.Code = rec.Code
	out.Body.WriteString(strings.ReplaceAll(rec.Body.String(), old, replacement))
	return out
}

func d7AssertBucketsOnly(t *testing.T, what string, body []byte) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%s: not JSON: %v", what, err)
	}
	if problems := d7PreciseLocationProblems(doc); len(problems) > 0 {
		t.Fatalf("%s: %v\nbody: %s", what, problems, body)
	}
}

func TestD7Contracts(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("profile_upsert_429_location_change_rate_limited", func(t *testing.T) {
		u := uuid.New()
		mustSeedActiveProfile(t, st, u)
		// A client geohash is ignored; the owner gets back the snapped point.
		rec := contractDo(r, http.MethodPost, "/v1/dating/profile",
			`{"latitude":17.385044,"longitude":78.486671,"location_geohash":"zzzzzzz"}`, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("first set: status %d body %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data store.Profile `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		p := body.Data
		if p.Latitude == nil || *p.Latitude != 17.39 || p.Longitude == nil || *p.Longitude != 78.49 ||
			p.LocationGeohash == nil || *p.LocationGeohash != store.EncodeGeohash(17.39, 78.49, 7) {
			t.Fatalf("owner profile = %v,%v %v; want the snapped 17.39,78.49 and its geohash", p.Latitude, p.Longitude, p.LocationGeohash)
		}
		// A move inside the same cell is a 200 no-op.
		rec = contractDo(r, http.MethodPost, "/v1/dating/profile", `{"latitude":17.3851,"longitude":78.4866}`, u)
		if rec.Code != http.StatusOK {
			t.Fatalf("sub-cell move: status %d body %s", rec.Code, rec.Body.String())
		}
		rec = contractDo(r, http.MethodPost, "/v1/dating/profile", `{"latitude":17.5,"longitude":78.4867}`, u)
		assertContract(t, rec, http.StatusTooManyRequests, "profile_upsert_429_location_change_rate_limited", map[uuid.UUID]string{u: "<user>"})
	})

	t.Run("profile_upsert_400_invalid_location", func(t *testing.T) {
		u := uuid.New()
		mustSeedActiveProfile(t, st, u)
		for _, body := range []string{
			`{"latitude":0,"longitude":0}`,
			`{"latitude":91,"longitude":78.4}`,
			`{"latitude":17.3}`,
		} {
			rec := contractDo(r, http.MethodPost, "/v1/dating/profile", body, u)
			assertContract(t, rec, http.StatusBadRequest, "profile_upsert_400_invalid_location", map[uuid.UUID]string{u: "<user>"})
		}
	})

	t.Run("deck_and_explain", func(t *testing.T) {
		viewer, candidate, outsider := uuid.New(), uuid.New(), uuid.New()
		for _, id := range []uuid.UUID{viewer, candidate, outsider} {
			mustSeedActiveProfile(t, st, id)
		}
		gender := "d7c-" + uuid.NewString()[:8]
		if _, err := st.UpsertProfile(ctx, candidate, store.UpsertProfileParams{Gender: &gender}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
			t.Fatal(err)
		}
		for id, point := range map[uuid.UUID][2]float64{viewer: {17.385, 78.4867}, candidate: {17.41, 78.4867}, outsider: {17.41, 78.4867}} {
			lat, lng := point[0], point[1]
			if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Latitude: &lat, Longitude: &lng}); err != nil {
				t.Fatalf("location: %v", err)
			}
		}
		// A stable age for the fixture, and a shown last-active bucket.
		if _, err := st.SetProfileBirthDate(ctx, candidate, time.Now().AddDate(-30, 0, -1), store.BasicsSourceIdentity); err != nil {
			t.Fatal(err)
		}
		hide := false
		if _, err := st.UpdatePrivacy(ctx, candidate, store.PrivacyUpdate{HideLastActive: &hide}); err != nil {
			t.Fatal(err)
		}
		labels := map[uuid.UUID]string{viewer: "<viewer>", candidate: "<candidate>", outsider: "<outsider>"}

		rec := contractDo(r, http.MethodGet, "/v1/dating/pulse/today", ``, viewer)
		d7AssertBucketsOnly(t, "pulse today", rec.Body.Bytes())
		assertContract(t, rec, http.StatusOK, "pulse_today_get_200", labels)

		rec = contractDo(r, http.MethodGet, "/v1/dating/pulse/"+candidate.String()+"/explain", ``, viewer)
		d7AssertBucketsOnly(t, "explain", rec.Body.Bytes())
		assertContract(t, d7Rewrite(rec, gender, "<gender>"), http.StatusOK, "pulse_explain_get_200", labels)

		for _, target := range []uuid.UUID{outsider, uuid.New()} {
			rec = contractDo(r, http.MethodGet, "/v1/dating/pulse/"+target.String()+"/explain", ``, viewer)
			assertContract(t, rec, http.StatusNotFound, "pulse_explain_404_candidate_unavailable", labels)
		}
	})

	t.Run("privacy_get_200", func(t *testing.T) {
		u := uuid.New()
		mustSeedProfile(t, st, u)
		rec := contractDo(r, http.MethodGet, "/v1/dating/profile/privacy", ``, u)
		assertContract(t, rec, http.StatusOK, "privacy_get_200", map[uuid.UUID]string{u: "<user>"})
	})
}

// TestD7ContractFixturesWellFormed runs without a database: every D7 fixture
// exists, is JSON with a data or error member, carries no raw id or timestamp,
// and conveys no distance, coordinate or last-active time finer than a bucket.
func TestD7ContractFixturesWellFormed(t *testing.T) {
	for _, name := range d7Fixtures {
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
		if problems := d7PreciseLocationProblems(map[string]any(doc)); len(problems) > 0 {
			t.Fatalf("%s: %v", name, problems)
		}
	}
	// The walker itself catches what it is meant to.
	for _, leak := range []string{
		`{"data":{"distance_km":3}}`,
		`{"data":[{"profile":{"distance_bucket":"lt_5_km","last_active_at":"x"}}]}`,
		`{"data":{"reasons":[{"summary":"Just 3 km away"}]}}`,
		`{"data":{"latitude":17.39}}`,
	} {
		var doc any
		_ = json.Unmarshal([]byte(leak), &doc)
		if len(d7PreciseLocationProblems(doc)) == 0 {
			t.Fatalf("walker missed a leak in %s", leak)
		}
	}
}
