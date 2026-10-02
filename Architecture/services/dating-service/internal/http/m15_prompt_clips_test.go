package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mechanic M15 — voice/video prompt answers (DATING_MEDIA_PROMPTS_ENABLED):
// golden fixtures and the guards behind them, with a media-service stub.

var m15Fixtures = []string{
	"prompt_clip_put_200_approved",
	"prompt_clip_put_200_pending_review",
	"prompt_clip_put_404_media_not_found",
	"prompt_clip_put_409_not_ready",
	"prompt_clip_put_422_too_long",
	"prompt_clip_put_404_not_enabled",
	"pulse_today_get_200_prompt_clip",
}

type fakeClip struct {
	owner      uuid.UUID
	kind       string
	processing string
	moderation string
	durationMs int
	prepared   bool
}

// clipMedia is a media-service stub holding clips by media id.
type clipMedia struct {
	mu      sync.Mutex
	clips   map[uuid.UUID]*fakeClip
	deleted []uuid.UUID
	// lenient answers for any requester, like a media-service that forgot
	// to check ownership: dating must still refuse.
	lenient bool
}

func (m *clipMedia) add(owner uuid.UUID, kind, moderation string, ms int) uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.New()
	m.clips[id] = &fakeClip{owner: owner, kind: kind, processing: "ready", moderation: moderation, durationMs: ms}
	return id
}

func (m *clipMedia) owned(id, requester uuid.UUID) (*fakeClip, error) {
	c, ok := m.clips[id]
	if !ok || (c.owner != requester && !m.lenient) {
		return nil, service.ErrClipMediaNotFound
	}
	return c, nil
}

func (m *clipMedia) ClipOwnerStatus(_ context.Context, id, requester uuid.UUID) (*service.ClipStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.owned(id, requester)
	if err != nil {
		return nil, err
	}
	return &service.ClipStatus{OwnerUserID: c.owner, Kind: c.kind, Processing: c.processing, Moderation: c.moderation,
		DurationMs: c.durationMs, Usable: c.moderation == "passed"}, nil
}

func (m *clipMedia) PrepareClip(_ context.Context, id, requester uuid.UUID) (string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.owned(id, requester)
	if err != nil {
		return "", 0, err
	}
	if c.durationMs > 30000 {
		return "", 0, &service.ClipTooLongError{MaxMs: 30000}
	}
	c.prepared = true
	return c.kind, c.durationMs, nil
}

func (m *clipMedia) ClipDeliveryURL(_ context.Context, id, owner uuid.UUID) (*service.ClipDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.owned(id, owner)
	if err != nil || !c.prepared || c.moderation != "passed" {
		return nil, service.ErrClipMediaNotFound
	}
	return &service.ClipDelivery{Kind: c.kind, URL: "https://cdn.test/clip/" + id.String(), ExpiresAt: time.Now().Add(2 * time.Minute)}, nil
}

func (m *clipMedia) DeleteClip(_ context.Context, id, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, id)
	return nil
}

func newM15Deck(t *testing.T, on bool) (*m1Deck, *clipMedia) {
	t.Helper()
	d := newM1Deck(t, service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, MediaPrompts: on})
	m := &clipMedia{clips: map[uuid.UUID]*fakeClip{}}
	d.env.svc.SetMediaClipClient(m)
	return d, m
}

func (d *m1Deck) putClip(user uuid.UUID, promptID int, media uuid.UUID) *httpRec15 {
	d.t.Helper()
	rec := contractDo(d.env.r, http.MethodPut, "/v1/dating/prompts/"+strconv.Itoa(promptID)+"/clip", `{"media_id":"`+media.String()+`"}`, user)
	return &httpRec15{code: rec.Code, body: rec.Body.Bytes()}
}

type httpRec15 struct {
	code int
	body []byte
}

func (d *m1Deck) clipFetch(viewer, owner uuid.UUID, promptID int) int {
	d.t.Helper()
	return contractDo(d.env.r, http.MethodGet, "/v1/dating/people/"+owner.String()+"/prompts/"+strconv.Itoa(promptID)+"/clip", ``, viewer).Code
}

// deckPrompt reports whether candidate's prompt promptID is on their card in
// the viewer's deck, and its clip URL ("" when none).
func (d *m1Deck) deckPrompt(candidate uuid.UUID, promptID int) (bool, string) {
	d.t.Helper()
	d.env.svc.InvalidatePulseCache(context.Background(), d.viewer)
	rec := contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/today", ``, d.viewer)
	var body struct {
		Data []struct {
			CandidateID uuid.UUID `json:"candidate_id"`
			Profile     struct {
				Detail *struct {
					Prompts []struct {
						PromptID int `json:"prompt_id"`
						Clip     *struct {
							URL string `json:"url"`
						} `json:"clip"`
					} `json:"prompts"`
				} `json:"detail"`
			} `json:"profile"`
		} `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		d.t.Fatalf("deck: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range body.Data {
		if c.CandidateID != candidate || c.Profile.Detail == nil {
			continue
		}
		for _, p := range c.Profile.Detail.Prompts {
			if p.PromptID == promptID {
				if p.Clip != nil {
					return true, p.Clip.URL
				}
				return true, ""
			}
		}
	}
	return false, ""
}

func (d *m1Deck) deckClip(candidate uuid.UUID, promptID int) string {
	_, u := d.deckPrompt(candidate, promptID)
	return u
}

func TestM15PromptClipContracts(t *testing.T) {
	d, m := newM15Deck(t, true)
	video := m.add(d.viewer, "video", "passed", 12000)
	audio := m.add(d.viewer, "audio", "review", 8000)
	labels := map[uuid.UUID]string{d.viewer: "<viewer>"}
	do := func(promptID int, media uuid.UUID) *httptest.ResponseRecorder {
		return contractDo(d.env.r, http.MethodPut, "/v1/dating/prompts/"+strconv.Itoa(promptID)+"/clip", `{"media_id":"`+media.String()+`"}`, d.viewer)
	}
	assertContract(t, do(1, video), http.StatusOK, "prompt_clip_put_200_approved", labels)
	assertContract(t, do(2, audio), http.StatusOK, "prompt_clip_put_200_pending_review", labels)
	assertContract(t, do(3, uuid.New()), http.StatusNotFound, "prompt_clip_put_404_media_not_found", labels)
	busy := m.add(d.viewer, "video", "pending", 9000)
	m.clips[busy].processing = "processing"
	assertContract(t, do(3, busy), http.StatusConflict, "prompt_clip_put_409_not_ready", labels)
	long := m.add(d.viewer, "video", "passed", 45000)
	assertContract(t, do(3, long), http.StatusUnprocessableEntity, "prompt_clip_put_422_too_long", labels)

	// A card in someone's deck carries the approved clip as a route.
	owner := d.candidate()
	ownerVideo := m.add(owner, "video", "passed", 10000)
	if r := d.putClip(owner, 4, ownerVideo); r.code != http.StatusOK {
		t.Fatalf("owner clip: %d %s", r.code, r.body)
	}
	// Narrow the deck to the owner so the fixture holds one card.
	d.exec(`UPDATE dating_preferences SET interested_in_gender = $2 WHERE user_id = $1`, d.viewer, d.gender+"-m15")
	d.exec(`UPDATE dating_profiles SET gender = $2 WHERE user_id = $1`, owner, d.gender+"-m15")
	d.env.svc.InvalidatePulseCache(context.Background(), d.viewer)
	assertContract(t, contractDo(d.env.r, http.MethodGet, "/v1/dating/pulse/today", ``, d.viewer), http.StatusOK, "pulse_today_get_200_prompt_clip",
		map[uuid.UUID]string{d.viewer: "<viewer>", owner: "<owner>"})

	off, _ := newM15Deck(t, false)
	assertContract(t, contractDo(off.env.r, http.MethodPut, "/v1/dating/prompts/1/clip", `{"media_id":"`+uuid.NewString()+`"}`, off.viewer),
		http.StatusNotFound, "prompt_clip_put_404_not_enabled", map[uuid.UUID]string{off.viewer: "<viewer>"})
}

func TestM15ContractFixturesWellFormed(t *testing.T) {
	for _, name := range m15Fixtures {
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
}

// Guard: someone else's upload cannot be attached.
func TestPromptClipMustBeYours(t *testing.T) {
	d, m := newM15Deck(t, true)
	other := d.candidate()
	theirs := m.add(other, "audio", "passed", 5000)
	if r := d.putClip(d.viewer, 1, theirs); r.code != http.StatusNotFound {
		t.Fatalf("attached someone else's clip: %d", r.code)
	}
	// Even if media-service failed to check, dating does.
	m.lenient = true
	if r := d.putClip(d.viewer, 1, theirs); r.code != http.StatusNotFound {
		t.Fatalf("attached someone else's clip through a lenient media-service: %d", r.code)
	}
}

// Guard: only an approved clip reaches a card and can be fetched; a block
// hides it; a video follows "blur until match", a voice clip does not.
func TestPromptClipAudience(t *testing.T) {
	d, m := newM15Deck(t, true)
	owner := d.candidate()
	video := m.add(owner, "video", "review", 10000)
	if r := d.putClip(owner, 1, video); r.code != http.StatusOK {
		t.Fatalf("attach: %d", r.code)
	}
	if present, u := d.deckPrompt(owner, 1); present || u != "" {
		t.Fatalf("a clip-only answer under review is on the card (present %v, clip %q)", present, u)
	}
	if code := d.clipFetch(d.viewer, owner, 1); code != http.StatusNotFound {
		t.Fatalf("a clip under review was served: %d", code)
	}
	// media-service passing it is not enough: a moderator decides.
	m.clips[video].moderation = "passed"
	if code := d.clipFetch(d.viewer, owner, 1); code != http.StatusNotFound {
		t.Fatalf("served before a moderator approved it: %d", code)
	}
	// A moderator approves it.
	if err := d.env.svc.ReviewPromptClip(context.Background(), uuid.New(), owner, 1, "approved", nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if u := d.deckClip(owner, 1); u != "/v1/dating/people/"+owner.String()+"/prompts/1/clip" {
		t.Fatalf("approved clip on the card = %q", u)
	}
	if code := d.clipFetch(d.viewer, owner, 1); code != http.StatusTemporaryRedirect {
		t.Fatalf("approved clip fetch: %d", code)
	}
	// Blur until match: the video is held back, a voice clip is not.
	d.exec(`UPDATE dating_profiles SET blur_photos_until_match = true WHERE user_id = $1`, owner)
	if code := d.clipFetch(d.viewer, owner, 1); code != http.StatusNotFound {
		t.Fatalf("a video served despite blur until match: %d", code)
	}
	voice := m.add(owner, "audio", "passed", 6000)
	if r := d.putClip(owner, 2, voice); r.code != http.StatusOK {
		t.Fatalf("attach voice: %d", r.code)
	}
	if code := d.clipFetch(d.viewer, owner, 2); code != http.StatusTemporaryRedirect {
		t.Fatalf("a voice clip held back by blur until match: %d", code)
	}
	// A block hides both.
	if rec := contractDo(d.env.r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+d.viewer.String()+`"}`, owner); rec.Code != http.StatusOK {
		t.Fatalf("block: %d", rec.Code)
	}
	if code := d.clipFetch(d.viewer, owner, 2); code != http.StatusNotFound {
		t.Fatalf("served across a block: %d", code)
	}
}

// Guard: a pending clip is asked about again and approved once media-service
// passes it.
func TestPromptClipRecheck(t *testing.T) {
	d, m := newM15Deck(t, true)
	clip := m.add(d.viewer, "video", "pending", 9000)
	if r := d.putClip(d.viewer, 1, clip); r.code != http.StatusOK {
		t.Fatalf("attach: %d", r.code)
	}
	d.exec(`UPDATE dating_prompts SET clip_checked_at = now() - interval '2 minutes' WHERE user_id = $1`, d.viewer)
	m.clips[clip].moderation = "passed"
	if _, err := d.env.svc.RecheckPromptClips(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	if code := d.clipFetch(d.viewer, d.viewer, 1); code != http.StatusTemporaryRedirect {
		t.Fatalf("after the recheck the owner cannot play it: %d", code)
	}
}

// Guard: replacing and removing a clip delete the old media; a clip-only
// answer goes with its clip.
func TestPromptClipCleanup(t *testing.T) {
	d, m := newM15Deck(t, true)
	first := m.add(d.viewer, "audio", "passed", 5000)
	second := m.add(d.viewer, "audio", "passed", 5000)
	d.putClip(d.viewer, 1, first)
	d.putClip(d.viewer, 1, second)
	if len(m.deleted) != 1 || m.deleted[0] != first {
		t.Fatalf("replacing deleted %v, want the first clip", m.deleted)
	}
	if rec := contractDo(d.env.r, http.MethodDelete, "/v1/dating/prompts/1/clip", ``, d.viewer); rec.Code != http.StatusOK {
		t.Fatalf("remove: %d", rec.Code)
	}
	if len(m.deleted) != 2 || m.deleted[1] != second {
		t.Fatalf("removing deleted %v", m.deleted)
	}
	if _, err := d.env.st.GetPrompt(context.Background(), d.viewer, 1); err == nil {
		t.Fatalf("a clip-only answer outlived its clip")
	}
}

// Guard: with the flag off, an approved clip never reaches a card.
func TestPromptClipFlagOffHidesCards(t *testing.T) {
	on, m := newM15Deck(t, true)
	owner := on.candidate()
	clip := m.add(owner, "audio", "passed", 5000)
	on.putClip(owner, 1, clip)
	on.exec(`UPDATE dating_prompts SET answer = 'hello' WHERE user_id = $1`, owner)
	on.env.svc.SetMechanicsConfig(service.MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50})
	if u := on.deckClip(owner, 1); u != "" {
		t.Fatalf("flag off but the card carries %q", u)
	}
}
