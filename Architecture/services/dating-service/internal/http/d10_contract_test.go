package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Golden contract fixtures for lane D10: every route the Dating app calls
// that had no fixture, plus the routes this lane added or changed. Same
// harness as d3_contract_test.go — ids and timestamps are replaced with
// stable placeholders before the compare, and the seeded birth date is
// relative, so the files are deterministic. Regenerate deliberately with
// UPDATE_CONTRACTS=1 and review the diff.

var d10Fixtures = []string{
	"profile_get_200",
	"profile_upsert_200",
	"preferences_get_200",
	"preferences_put_200",
	"photos_get_200",
	"photos_post_201",
	"prompts_get_200",
	"prompts_put_200",
	"selfie_challenge_post_200",
	"selfie_post_200_passed",
	"selfie_post_200_review",
	"selfie_post_200_not_enough_blinks",
	"verification_status_get_200",
	"spark_create_post_201_matched",
	"sparks_incoming_get_200",
	"spark_accept_post_201",
	"match_get_200",
	"match_close_post_200",
	"stash_post_201",
	"stash_get_200",
	"block_post_200",
	"blocks_get_200",
	"report_post_201",
	"panic_post_200",
	"trusted_contacts_get_200",
	"trusted_contact_put_200",
	"share_location_post_200",
	"share_location_get_200",
	"share_location_delete_200",
	"shared_location_get_200",
	"shared_locations_get_200",
	"data_export_post_202",
	"data_export_me_get_200",
	"person_get_200",
}

// d10Liveness is a scripted liveness client for the selfie fixtures.
type d10Liveness struct{ result service.LivenessResult }

func (f *d10Liveness) CheckLiveness(context.Context, service.LivenessRequest) (*service.LivenessResult, error) {
	r := f.result
	return &r, nil
}

func d10Blinks(similarity float64) service.LivenessResult {
	return service.LivenessResult{BlinksDetected: 2, FramesAnalysed: 32, DurationMs: 3000,
		SingleFace: true, SameFaceAcrossFrames: true, Similarity: similarity, Provider: "mock"}
}

// d10Media is a media-service stub: every media is the caller's, ready and
// moderation-passed, with no labels and one face.
type d10Media struct{}

func (d10Media) status(mediaID uuid.UUID) *service.MediaPhotoStatus {
	faces := 1
	return &service.MediaPhotoStatus{MediaID: mediaID, OwnerMatches: true, Kind: "image", Status: "ready",
		ModerationStatus: "passed", ContentType: "image/jpeg", ModerationScanned: true, ModerationScanner: "mock",
		ModerationLabels: []service.MediaPhotoLabel{}, Prepared: true, FaceCount: &faces}
}

func (m d10Media) PhotoOwnerStatus(_ context.Context, mediaID, _ uuid.UUID) (*service.MediaPhotoStatus, error) {
	return m.status(mediaID), nil
}

func (m d10Media) PreparePhoto(_ context.Context, mediaID, _ uuid.UUID, _ bool) (*service.MediaPhotoStatus, error) {
	return m.status(mediaID), nil
}

func (d10Media) PhotoDeliveryURL(context.Context, uuid.UUID, uuid.UUID, string) (string, error) {
	return "https://media.example/signed", nil
}

func (d10Media) DeletePhotoMedia(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type d10Env struct {
	r    *gin.Engine
	st   *store.Store
	svc  *service.Service
	live *d10Liveness
}

// setupD10 builds the router with the stubs the fixtures need.
func setupD10(t *testing.T) *d10Env {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D10 contract tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	st := store.New(pool)
	st.SetPII(testPII(t))
	svc := service.New(st, nil)
	svc.SetMessageClient(&stubMessageClient{})
	svc.SetMediaPhotoClient(d10Media{})
	live := &d10Liveness{result: d10Blinks(96)}
	svc.SetLivenessClient(live)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).RegisterRoutes(r)
	return &d10Env{r: r, st: st, svc: svc, live: live}
}

// d10Birth is a birth date 30 years and a day ago, so every fixture says age
// 30 whatever day it is regenerated.
func d10Birth() time.Time {
	return time.Now().UTC().AddDate(-30, 0, -1).Truncate(24 * time.Hour)
}

// seedD10Basics seeds the profile basics, consent and an approved primary
// photo. It stops before the selfie, so the selfie fixtures can run it.
func seedD10Basics(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.SetConsent(ctx, id, service.ConsentBiometricSelfie, true, "test"); err != nil {
		t.Fatalf("seed consent: %v", err)
	}
	intent, gender, city, interested := "casual", "woman", "Hyderabad", "everyone"
	if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Intent: &intent, Gender: &gender, City: &city}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := st.SetProfileBirthDate(ctx, id, d10Birth(), store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed birth date: %v", err)
	}
	if _, err := st.SetProfileFirstName(ctx, id, "Asha", store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed first name: %v", err)
	}
	if _, err := st.UpsertPreferences(ctx, id, store.UpsertPreferencesParams{InterestedInGender: &interested}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	photo, err := st.CreatePhoto(ctx, id, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true, Visibility: "public"})
	if err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	if _, err := st.SetPhotoModerationStatus(ctx, photo.ID, "approved", ""); err != nil {
		t.Fatalf("approve photo: %v", err)
	}
	for _, ev := range []store.ProfileEvent{store.ProfileEventBasicsComplete, store.ProfileEventPhotoApproved} {
		if _, err := st.TransitionProfileStatus(ctx, id, ev, store.ProfileActorSystem); err != nil {
			t.Fatalf("seed transition %s: %v", ev, err)
		}
	}
}

// seedD10Profile is seedD10Basics plus a passed selfie: a fully active
// profile.
func seedD10Profile(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	seedD10Basics(t, st, id)
	ctx := context.Background()
	if err := st.RecordSelfieAttempt(ctx, id, 0.99, store.SelfieStatusPassed); err != nil {
		t.Fatalf("seed selfie: %v", err)
	}
	if _, err := st.TransitionProfileStatus(ctx, id, store.ProfileEventSelfiePassed, store.ProfileActorSystem); err != nil {
		t.Fatalf("seed transition selfie_passed: %v", err)
	}
}

// d10Challenge asks for a selfie challenge and returns its id.
func d10Challenge(t *testing.T, r http.Handler, user uuid.UUID) string {
	t.Helper()
	rec := contractDo(r, http.MethodPost, "/v1/dating/verification/selfie/challenge", ``, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge: status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			ChallengeID string `json:"challenge_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	return env.Data.ChallengeID
}

func TestD10Contracts(t *testing.T) {
	env := setupD10(t)
	r, st := env.r, env.st
	ctx := context.Background()

	t.Run("profile", func(t *testing.T) {
		user := uuid.New()
		seedD10Profile(t, st, user)
		labels := map[uuid.UUID]string{user: "<user>"}
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/profile", ``, user), http.StatusOK, "profile_get_200", labels)
		body := `{"intent":"casual","bio":"Filter coffee and long walks.","city":"Hyderabad","country":"India"}`
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/profile", body, user), http.StatusOK, "profile_upsert_200", labels)
	})

	t.Run("preferences", func(t *testing.T) {
		user := uuid.New()
		seedD10Profile(t, st, user)
		labels := map[uuid.UUID]string{user: "<user>"}
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/preferences", ``, user), http.StatusOK, "preferences_get_200", labels)
		body := `{"min_age":25,"max_age":35,"distance_km":25,"interested_in_gender":"everyone","intent_filter":["casual"]}`
		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/preferences", body, user), http.StatusOK, "preferences_put_200", labels)
	})

	t.Run("photos_and_prompts", func(t *testing.T) {
		user := uuid.New()
		mustSeedProfile(t, st, user)
		labels := map[uuid.UUID]string{user: "<user>"}
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/photos", ``, user), http.StatusOK, "photos_get_200", labels)
		attach := `{"media_id":"` + uuid.New().String() + `","is_primary":true,"visibility":"public"}`
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/photos", attach, user), http.StatusCreated, "photos_post_201", labels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/prompts", ``, user), http.StatusOK, "prompts_get_200", labels)
		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/prompts/1", `{"answer":"Ask me about filter coffee."}`, user),
			http.StatusOK, "prompts_put_200", labels)
	})

	t.Run("selfie", func(t *testing.T) {
		passUser := uuid.New()
		seedD10Basics(t, st, passUser)
		labels := map[uuid.UUID]string{passUser: "<user>"}
		env.live.result = d10Blinks(96)
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/verification/selfie/challenge", ``, passUser),
			http.StatusOK, "selfie_challenge_post_200", labels)
		ch := d10Challenge(t, r, passUser)
		submit := `{"challenge_id":"` + ch + `","video_media_id":"` + uuid.New().String() + `"}`
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/verification/selfie", submit, passUser),
			http.StatusOK, "selfie_post_200_passed", labels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/verification/status", ``, passUser),
			http.StatusOK, "verification_status_get_200", labels)

		// Borderline similarity: a moderator decides.
		reviewUser := uuid.New()
		seedD10Basics(t, st, reviewUser)
		env.live.result = d10Blinks(85)
		ch = d10Challenge(t, r, reviewUser)
		submit = `{"challenge_id":"` + ch + `","video_media_id":"` + uuid.New().String() + `"}`
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/verification/selfie", submit, reviewUser),
			http.StatusOK, "selfie_post_200_review", map[uuid.UUID]string{reviewUser: "<user>"})

		// One blink: NOT_ENOUGH_BLINKS.
		blinkUser := uuid.New()
		seedD10Basics(t, st, blinkUser)
		oneBlink := d10Blinks(96)
		oneBlink.BlinksDetected = 1
		env.live.result = oneBlink
		ch = d10Challenge(t, r, blinkUser)
		submit = `{"challenge_id":"` + ch + `","video_media_id":"` + uuid.New().String() + `"}`
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/verification/selfie", submit, blinkUser),
			http.StatusOK, "selfie_post_200_not_enough_blinks", map[uuid.UUID]string{blinkUser: "<user>"})
		env.live.result = d10Blinks(96)
	})

	t.Run("sparks_and_matches", func(t *testing.T) {
		a, b := d10Pair()
		seedD10Profile(t, st, a)
		seedD10Profile(t, st, b)
		labels := map[uuid.UUID]string{a: "<sender>", b: "<recipient>"}

		// b sparks a first, so a's spark forms the match.
		if _, err := st.CreateSpark(ctx, b, a, "prompt", "p1", "Loved your answer"); err != nil {
			t.Fatalf("seed spark: %v", err)
		}
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/sparks/incoming", ``, a), http.StatusOK, "sparks_incoming_get_200", labels)
		rec := contractDo(r, http.MethodPost, "/v1/dating/sparks", sparkBody(b, "p2"), a)
		assertContract(t, rec, http.StatusCreated, "spark_create_post_201_matched", labels)

		matches, err := st.ListMatchesForUser(ctx, a, "all")
		if err != nil || len(matches) == 0 {
			t.Fatalf("matches: %v (%d)", err, len(matches))
		}
		m := matches[0]
		matchLabels := map[uuid.UUID]string{m.ID: "<match>", a: "<user_a>", b: "<user_b>"}
		if m.ConversationID != nil {
			matchLabels[*m.ConversationID] = "<conversation>"
		}
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/matches/"+m.ID.String(), ``, a), http.StatusOK, "match_get_200", matchLabels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/people/"+b.String(), ``, a), http.StatusOK, "person_get_200",
			map[uuid.UUID]string{b: "<user>"})
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/matches/"+m.ID.String()+"/close", ``, a),
			http.StatusOK, "match_close_post_200", matchLabels)

		// Accepting an incoming spark forms the match the same way.
		x, y := uuid.New(), uuid.New()
		seedD10Profile(t, st, x)
		seedD10Profile(t, st, y)
		sp, err := st.CreateSpark(ctx, x, y, "photo", "0", "")
		if err != nil {
			t.Fatalf("seed spark: %v", err)
		}
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/sparks/"+sp.ID.String()+"/accept", ``, y),
			http.StatusCreated, "spark_accept_post_201", map[uuid.UUID]string{x: "<sender>", y: "<recipient>", sp.ID: "<spark>"})
	})

	t.Run("stash", func(t *testing.T) {
		user, candidate := uuid.New(), uuid.New()
		seedD10Profile(t, st, user)
		seedD10Profile(t, st, candidate)
		labels := map[uuid.UUID]string{user: "<user>", candidate: "<candidate>"}
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/stash", `{"candidate_id":"`+candidate.String()+`"}`, user),
			http.StatusCreated, "stash_post_201", labels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/stash", ``, user), http.StatusOK, "stash_get_200", labels)
	})

	t.Run("blocks_and_reports", func(t *testing.T) {
		user, target := uuid.New(), uuid.New()
		seedD10Profile(t, st, user)
		seedD10Profile(t, st, target)
		labels := map[uuid.UUID]string{user: "<user>", target: "<target>"}
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+target.String()+`"}`, user),
			http.StatusOK, "block_post_200", labels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/blocks", ``, user), http.StatusOK, "blocks_get_200", labels)

		reporter, reported := uuid.New(), uuid.New()
		seedD10Profile(t, st, reporter)
		seedD10Profile(t, st, reported)
		body := `{"target_id":"` + reported.String() + `","reason":"harassment","details":"Unpleasant messages after the match."}`
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/safety/report", body, reporter),
			http.StatusCreated, "report_post_201", map[uuid.UUID]string{reporter: "<reporter>", reported: "<target>"})
	})

	t.Run("safety", func(t *testing.T) {
		user, contact := uuid.New(), uuid.New()
		seedD10Profile(t, st, user)
		seedD10Profile(t, st, contact)
		labels := map[uuid.UUID]string{user: "<user>", contact: "<contact>"}
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/safety/panic", `{"context":{"source":"match_screen"}}`, user),
			http.StatusOK, "panic_post_200", labels)

		// A trusted contact needs a current match.
		matchID, _, err := st.CreateOrGetOpenMatch(ctx, user, contact, nil)
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
			t.Fatalf("activate: %v", err)
		}
		assertContract(t, contractDo(r, http.MethodPut, "/v1/dating/safety/trusted-contacts/"+contact.String(),
			`{"share_location_on_panic":true}`, user), http.StatusOK, "trusted_contact_put_200", labels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/safety/trusted-contacts", ``, user),
			http.StatusOK, "trusted_contacts_get_200", labels)

		share := `{"recipient_id":"` + contact.String() + `","duration_minutes":60,"latitude":17.44,"longitude":78.39}`
		rec := contractDo(r, http.MethodPost, "/v1/dating/safety/share-location", share, user)
		assertContract(t, rec, http.StatusOK, "share_location_post_200", labels)
		var created struct {
			Data struct {
				ShareID string `json:"share_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode share: %v", err)
		}
		shareID := uuid.MustParse(created.Data.ShareID)
		shareLabels := map[uuid.UUID]string{user: "<user>", contact: "<contact>", shareID: "<share>"}
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/safety/share-location", ``, user),
			http.StatusOK, "share_location_get_200", shareLabels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/safety/shared-locations", ``, contact),
			http.StatusOK, "shared_locations_get_200", shareLabels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/safety/shared-locations/"+shareID.String(), ``, contact),
			http.StatusOK, "shared_location_get_200", shareLabels)
		assertContract(t, contractDo(r, http.MethodDelete, "/v1/dating/safety/share-location/"+shareID.String(), ``, user),
			http.StatusOK, "share_location_delete_200", shareLabels)
	})

	t.Run("data_export", func(t *testing.T) {
		user := uuid.New()
		seedD10Profile(t, st, user)
		labels := map[uuid.UUID]string{user: "<user>"}
		assertContract(t, contractDo(r, http.MethodPost, "/v1/dating/data-export", ``, user), http.StatusAccepted, "data_export_post_202", labels)
		assertContract(t, contractDo(r, http.MethodGet, "/v1/dating/data-export/me", ``, user), http.StatusOK, "data_export_me_get_200", labels)
	})
}

// TestD10ContractFixturesWellFormed runs without a database: every fixture
// exists, is JSON with a data or error member, carries no raw id or
// timestamp, and leaks none of the sealed or sensitive fields.
func TestD10ContractFixturesWellFormed(t *testing.T) {
	for _, name := range d10Fixtures {
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
		for _, leak := range []string{"religion", "geohash", "declined_at", "embedding"} {
			if strings.Contains(string(raw), `"`+leak+`"`) {
				t.Fatalf("%s: exposes %s", name, leak)
			}
		}
	}
}

// d10Pair returns two ids in canonical order (lower first), so a match's
// user_a / user_b — and therefore the fixture's labels — do not depend on
// which random uuid sorted first.
func d10Pair() (uuid.UUID, uuid.UUID) {
	for {
		x, y := uuid.New(), uuid.New()
		if x.String() < y.String() {
			return x, y
		}
	}
}
