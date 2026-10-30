// Audit 2026-10-30, access & privacy lane — HTTP flows on the test database
// (CI only; skipped without TEST_PG_DSN): H1 (actions keyed by a candidate id
// need a served candidate; Nebula shows only visible people) and L4 (hidden
// spark notes are never sent, the recipient reads one on request).
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

// pairInDeck makes candidate the one person in viewer's deck: a gender only
// candidate holds, which viewer's preference asks for.
func pairInDeck(t *testing.T, st *store.Store, viewer, candidate uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	gender := "h1-" + uuid.NewString()[:8]
	if _, err := st.UpsertProfile(ctx, candidate, store.UpsertProfileParams{Gender: &gender}); err != nil {
		t.Fatalf("pair: candidate gender: %v", err)
	}
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatalf("pair: viewer preference: %v", err)
	}
}

func nebulaIDs(t *testing.T, d *m1Deck) map[uuid.UUID]bool {
	t.Helper()
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/nebula", ``, d.viewer)
	var body m1DeckBody
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("nebula: %d %s", rec.Code, rec.Body.String())
	}
	out := map[uuid.UUID]bool{}
	for _, c := range body.Data {
		out[c.CandidateID] = true
	}
	return out
}

// H1: passing (or stashing) a user id the caller was never served is the
// one 404 CANDIDATE_UNAVAILABLE, nothing is recorded, and the victim never
// reaches Nebula; a served candidate who later pauses leaves Nebula too.
func TestH1HTTP_PassNeedsAServedCandidateAndNebulaHidesPaused(t *testing.T) {
	d := newM1Deck(t, m1Config(50, 50))
	ctx := context.Background()
	victim := uuid.New()
	mustSeedActiveProfile(t, d.env.st, victim) // active, but not in the viewer's deck
	for _, id := range []uuid.UUID{victim, uuid.New()} {
		d.wantRefusal(contractDo(d.env.r, http.MethodPost, "/v1/dating/pulse/"+id.String()+"/pass", ``, d.viewer),
			http.StatusNotFound, "CANDIDATE_UNAVAILABLE")
		d.wantRefusal(contractDo(d.env.r, http.MethodPost, "/v1/dating/stash", `{"candidate_id":"`+id.String()+`"}`, d.viewer),
			http.StatusNotFound, "CANDIDATE_UNAVAILABLE")
	}
	if standing, err := d.env.st.HasStandingPass(ctx, d.viewer, victim); err != nil || standing {
		t.Fatalf("refused pass was recorded (standing=%v err=%v)", standing, err)
	}
	if nebulaIDs(t, d)[victim] {
		t.Fatalf("the never-served victim is in Nebula")
	}

	// A served candidate: the pass lands, a retry answers the same, Nebula
	// shows them — until they pause.
	served := d.candidate()
	d.pass(served)
	d.pass(served)
	if !nebulaIDs(t, d)[served] {
		t.Fatalf("passed candidate missing from Nebula")
	}
	if _, err := d.env.st.TransitionProfileStatus(ctx, served, store.ProfileEventPause, store.ProfileActorUser); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if nebulaIDs(t, d)[served] {
		t.Fatalf("a paused profile is still readable through Nebula")
	}
}

// L4: a note the recipient's filter hides is not in the incoming list; the
// recipient — and only the recipient — reads it with GET /sparks/:id/note.
func TestL4HTTP_HiddenNoteNeverSentRevealOnRequest(t *testing.T) {
	d := newM1Deck(t, m13Config(true))
	sender := d.candidate()
	spark := d.sparkWithNote(sender, "you look stupid")
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/sparks/incoming", ``, d.viewer)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"note_hidden":"unkind"`) {
		t.Fatalf("incoming: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "stupid") {
		t.Fatalf("incoming list carries the hidden note text: %s", rec.Body.String())
	}
	liked := contractDo(d.env.r, http.MethodGet, "/v1/dating/liked-you", ``, d.viewer)
	if strings.Contains(liked.Body.String(), "stupid") {
		t.Fatalf("liked-you carries the hidden note text: %s", liked.Body.String())
	}
	path := "/v1/dating/sparks/" + spark.String() + "/note"
	reveal := contractDo(d.env.r, http.MethodGet, path, ``, d.viewer)
	if reveal.Code != http.StatusOK || !strings.Contains(reveal.Body.String(), `"note":"you look stupid"`) {
		t.Fatalf("reveal: %d %s", reveal.Code, reveal.Body.String())
	}
	for _, who := range []uuid.UUID{sender, uuid.New()} {
		if w := contractDo(d.env.r, http.MethodGet, path, ``, who); w.Code != http.StatusNotFound {
			t.Fatalf("reveal by a non-recipient: %d %s, want 404", w.Code, w.Body.String())
		}
	}
}
