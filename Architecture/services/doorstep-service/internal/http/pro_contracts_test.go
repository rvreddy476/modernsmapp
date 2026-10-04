package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/bgv"
	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/digilocker"
	"github.com/atpost/doorstep-service/internal/facecompare"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

// Professional onboarding contract fixtures (A2): the journey of one woman
// professional from apply to approval, the admin review, and the refusals.
// Same rules as contracts_test.go: real route table, pinned clock
// (2026-10-04T06:30:00Z = 12:00 IST) and request id, deterministic ids.

var (
	proUser   = uuid.MustParse("66668bc2-a3f6-40a5-9cdd-c998dcf72f29") // call_a on dev
	otherUser = uuid.MustParse("7b0c3e1a-2f4d-4e5a-9b6c-8d7e6f5a4b3c")
)

func mediaOf(user uuid.UUID, what string) uuid.UUID {
	return devseed.ID("media", user.String()+"/"+what)
}

type proRig struct {
	*rig
	pro   *fakeProStore
	media *fakeMedia
}

func newProRig(t *testing.T, opts ...func(*service.ProDeps)) *proRig {
	t.Helper()
	return newProRigExt(t, nil, opts...)
}

// newProRigExt is newProRig with the service extended before mounting (the
// A4 fixtures add bookings and dispatch).
func newProRigExt(t *testing.T, ext func(*service.Service) *service.Service, opts ...func(*service.ProDeps)) *proRig {
	t.Helper()
	rg := newRig(t)
	pr := &proRig{rig: rg, pro: newFakeProStore(), media: &fakeMedia{owner: map[uuid.UUID]uuid.UUID{}}}
	for _, u := range []uuid.UUID{proUser, otherUser} {
		for _, what := range []string{"photo", "selfie", "police", "trade"} {
			pr.media.owner[mediaOf(u, what)] = u
		}
	}
	pii, err := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x42}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	dl, err := digilocker.New("mock", false, digilocker.HTTPConfig{})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := bgv.New("uploaded_document", true)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	tokens := 0
	deps := service.ProDeps{
		Store:               pr.pro,
		DigiLocker:          digilocker.Settings{Mode: digilocker.ModeMock, Client: dl, RedirectURI: "http://localhost:8080/doorstep-pro/digilocker"},
		Faces:               &facecompare.Mock{Similarity: 96},
		SelfieMinSimilarity: 80,
		Media:               pr.media,
		Images:              pr.media,
		PII:                 pii,
		BGV:                 provider,
		NewToken: func() (string, error) {
			tokens++
			sum := sha256.Sum256([]byte(fmt.Sprintf("fixture-token-%d", tokens)))
			return base64.RawURLEncoding.EncodeToString(sum[:]), nil
		},
	}
	for _, o := range opts {
		o(&deps)
	}
	tc, err := tax.NewGST(nil, testGSTIN(t))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(rg.store, tc, 15*time.Minute).
		WithClock(func() time.Time { return fixtureNow }, func() uuid.UUID { n++; return devseed.ID("fixture", fmt.Sprint(n)) }).
		WithPro(deps)
	if ext != nil {
		svc = ext(svc)
	}
	rg.r = mount(svc, testInternalKey, rg.v)
	return pr
}

func as(user uuid.UUID) map[string]string { return map[string]string{"X-User-Id": user.String()} }

func (pr *proRig) call(user uuid.UUID, method, path, body string) *httptest.ResponseRecorder {
	return pr.do(req{method: method, path: "/v1/doorstep/pro" + path, body: body, headers: as(user)})
}

func (pr *proRig) admin(perm, method, path, body string) *httptest.ResponseRecorder {
	return pr.do(req{method: method, path: InternalAdminPrefix + path, body: body, headers: pr.adminHeaders(perm), noKey: true})
}

func mustStatus(t *testing.T, what string, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("%s: %d %s, want %d", what, w.Code, w.Body.String(), status)
	}
}

func mustCode(t *testing.T, what string, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	got, _ := errorCode(t, w)
	if w.Code != status || got != code {
		t.Fatalf("%s: %d %s, want %d %s", what, w.Code, w.Body.String(), status, code)
	}
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestContract_ProJourney walks one professional (a woman, deep cleaning +
// electrician) from apply to approval, writing a fixture at each step.
func TestContract_ProJourney(t *testing.T) {
	pr := newProRig(t)
	u := proUser

	// Apply (declares skills of the chosen categories; home cleaning has
	// none that needs a certificate).
	w := pr.call(u, "POST", "/apply", jsonStr(map[string]any{"display_name": "Lakshmi Devi", "city_code": "hyd",
		"category_ids": []string{idOf("category", "home-cleaning")}}))
	assertFixture(t, "pro_apply_201.json", w, 201)
	assertFixture(t, "pro_apply_409_exists.json", pr.call(u, "POST", "/apply", `{"display_name":"Again","city_code":"HYD"}`), 409)
	// Gender is never self-declared: the strict decoder refuses the field.
	assertFixture(t, "pro_apply_400_gender_field.json",
		pr.call(otherUser, "POST", "/apply", `{"display_name":"Ravi","city_code":"HYD","gender":"female"}`), 400)
	assertFixture(t, "pro_me_get_404.json", pr.call(otherUser, "GET", "/me", ""), 404)
	assertFixture(t, "pro_readiness_200_draft.json", pr.call(u, "GET", "/readiness", ""), 200)

	// Profile photo (own approved image).
	assertFixture(t, "pro_me_patch_200.json", pr.call(u, "PATCH", "/me", `{"photo_media_id":"`+mediaOf(u, "photo").String()+`"}`), 200)
	mustCode(t, "someone else's photo", pr.call(u, "PATCH", "/me", `{"photo_media_id":"`+mediaOf(otherUser, "photo").String()+`"}`), 400, "DOORSTEP_INVALID_REQUEST")
	mustCode(t, "gender in patch", pr.call(u, "PATCH", "/me", `{"gender":"male"}`), 400, "DOORSTEP_INVALID_REQUEST")
	assertFixture(t, "pro_me_get_200.json", pr.call(u, "GET", "/me", ""), 200)

	// Selfie before DigiLocker: the reference face does not exist yet.
	assertFixture(t, "pro_selfie_422_no_aadhaar.json", pr.call(u, "POST", "/selfie", `{"media_id":"`+mediaOf(u, "selfie").String()+`"}`), 422)

	// DigiLocker (PKCE; the mock's authorize URL is the redirect with a code).
	w = pr.call(u, "POST", "/digilocker/start", "")
	assertFixture(t, "pro_digilocker_start_200.json", w, 200)
	var start struct {
		URL   string `json:"authorization_url"`
		State string `json:"state"`
	}
	decodeData(t, w, &start)
	if !strings.Contains(start.URL, "state="+start.State) || !strings.Contains(start.URL, "code=mock-female") {
		t.Fatalf("authorize url %s", start.URL)
	}
	assertFixture(t, "pro_digilocker_callback_400_state.json",
		pr.call(u, "POST", "/digilocker/callback", `{"code":"mock-female","state":"`+strings.Repeat("x", 43)+`"}`), 400)
	// Another user cannot complete this professional's state.
	pr.call(otherUser, "POST", "/apply", `{"display_name":"Ravi Kumar","city_code":"HYD"}`)
	mustCode(t, "foreign state", pr.call(otherUser, "POST", "/digilocker/callback", `{"code":"mock-male","state":"`+start.State+`"}`), 400, "DOORSTEP_INVALID_REQUEST")
	assertFixture(t, "pro_digilocker_callback_200.json",
		pr.call(u, "POST", "/digilocker/callback", `{"code":"mock-female","state":"`+start.State+`"}`), 200)
	mustCode(t, "state reused", pr.call(u, "POST", "/digilocker/callback", `{"code":"mock-female","state":"`+start.State+`"}`), 400, "DOORSTEP_INVALID_REQUEST")
	mustCode(t, "second digilocker", pr.call(u, "POST", "/digilocker/start", ""), 409, "DOORSTEP_CONFLICT")
	if g := pr.pro.pros[devseed.ID("professional", u.String())].p.Gender; g == nil || *g != "female" {
		t.Fatalf("gender from DigiLocker: %v", g)
	}

	// Selfie face match (mock 96 >= 80): advisory, the selfie waits for an
	// admin (founder rule 4 Oct 2026: nothing approves itself).
	assertFixture(t, "pro_selfie_200.json", pr.call(u, "POST", "/selfie", `{"media_id":"`+mediaOf(u, "selfie").String()+`"}`), 200)

	// Skills: the catalogue, then deep cleaning and electrician (needs a trade
	// certificate); both pending until an admin verifies them.
	assertFixture(t, "pro_skills_list_200.json", pr.call(u, "GET", "/skills", ""), 200)
	assertFixture(t, "pro_skills_put_200.json", pr.call(u, "PUT", "/me/skills", `{"skill_codes":["deep_cleaning","electrician"]}`), 200)
	assertFixture(t, "pro_skills_put_400_unknown.json", pr.call(u, "PUT", "/me/skills", `{"skill_codes":["astrology"]}`), 400)
	assertFixture(t, "pro_trade_certificate_400_not_required.json",
		pr.call(u, "POST", "/me/skills/deep_cleaning/certificate", `{"media_id":"`+mediaOf(u, "trade").String()+`","issued_on":"2019-06-01"}`), 400)
	assertFixture(t, "pro_trade_certificate_201.json",
		pr.call(u, "POST", "/me/skills/electrician/certificate", `{"media_id":"`+mediaOf(u, "trade").String()+`","issued_on":"2019-06-01","certificate_number":"ITI/EL/2019/4471"}`), 201)

	// Service area, hours, days off.
	area := map[string]any{"zone_ids": []string{idOf("zone", "west-hitec-gachibowli")}, "home_lat": 17.44, "home_lng": 78.37, "radius_m": 8000}
	assertFixture(t, "pro_area_put_200.json", pr.call(u, "PUT", "/me/area", jsonStr(area)), 200)
	area["radius_m"] = 20000
	assertFixture(t, "pro_area_put_400_radius.json", pr.call(u, "PUT", "/me/area", jsonStr(area)), 400)
	area["radius_m"], area["zone_ids"] = 8000, []string{uuid.NewSHA1(uuid.NameSpaceURL, []byte("nowhere")).String()}
	assertFixture(t, "pro_area_put_400_zone.json", pr.call(u, "PUT", "/me/area", jsonStr(area)), 400)
	hours := `{"items":[{"weekday":2,"start":"09:00","end":"18:00"},{"weekday":1,"start":"14:00","end":"18:00"},{"weekday":1,"start":"09:00","end":"13:00"}]}`
	assertFixture(t, "pro_hours_put_200.json", pr.call(u, "PUT", "/me/hours", hours), 200)
	assertFixture(t, "pro_hours_put_400_overlap.json",
		pr.call(u, "PUT", "/me/hours", `{"items":[{"weekday":1,"start":"09:00","end":"13:00"},{"weekday":1,"start":"12:00","end":"18:00"}]}`), 400)
	assertFixture(t, "pro_hours_get_200.json", pr.call(u, "GET", "/me/hours", ""), 200)
	assertFixture(t, "pro_days_off_post_201.json", pr.call(u, "POST", "/me/days-off", `{"date":"2026-10-10","reason":"Family function"}`), 201)
	assertFixture(t, "pro_days_off_post_409_job.json", pr.call(u, "POST", "/me/days-off", `{"date":"2026-10-09","reason":null}`), 409)
	assertFixture(t, "pro_days_off_post_400_past.json", pr.call(u, "POST", "/me/days-off", `{"date":"2026-10-01","reason":null}`), 400)
	pr.call(u, "POST", "/me/days-off", `{"date":"2026-10-12","reason":null}`)
	assertFixture(t, "pro_days_off_list_200.json", pr.call(u, "GET", "/me/days-off", ""), 200)
	if w := pr.call(u, "DELETE", "/me/days-off/2026-10-12", ""); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("delete day off: %d %s", w.Code, w.Body.String())
	}
	assertFixture(t, "pro_days_off_delete_404.json", pr.call(u, "DELETE", "/me/days-off/2026-10-12", ""), 404)

	// Bank: sealed, masked view only.
	w = pr.call(u, "PUT", "/me/bank", `{"account_holder":"Lakshmi  Devi","account_number":"000123456789","ifsc":"sbin0001234"}`)
	assertFixture(t, "pro_bank_put_200.json", w, 200)
	if strings.Contains(w.Body.String(), "23456789") {
		t.Fatalf("account number leaked: %s", w.Body.String())
	}
	assertFixture(t, "pro_bank_put_400_ifsc.json", pr.call(u, "PUT", "/me/bank", `{"account_holder":"Lakshmi Devi","account_number":"000123456789","ifsc":"SBIN123"}`), 400)

	// Police certificate (pending admin review; older than 12 months refused).
	assertFixture(t, "pro_police_certificate_400_old.json",
		pr.call(u, "POST", "/me/police-certificate", `{"media_id":"`+mediaOf(u, "police").String()+`","issued_on":"2025-09-01"}`), 400)
	w = pr.call(u, "POST", "/me/police-certificate", `{"media_id":"`+mediaOf(u, "police").String()+`","issued_on":"2026-09-20","certificate_number":"TSPCC/2026/118833"}`)
	assertFixture(t, "pro_police_certificate_201.json", w, 201)
	var police struct {
		ID string `json:"id"`
	}
	decodeData(t, w, &police)
	assertFixture(t, "pro_police_certificate_409_pending.json",
		pr.call(u, "POST", "/me/police-certificate", `{"media_id":"`+mediaOf(u, "police").String()+`","issued_on":"2026-09-21"}`), 409)

	// Agreement, PAN.
	assertFixture(t, "pro_agreement_400_version.json", pr.call(u, "POST", "/me/agreement", `{"version":"2025-01-01"}`), 400)
	assertFixture(t, "pro_agreement_200.json", pr.call(u, "POST", "/me/agreement", `{"version":"`+prokyc.AgreementVersion+`"}`), 200)
	assertFixture(t, "pro_pan_put_400.json", pr.call(u, "PUT", "/me/pan", `{"pan":"ABCCE1234F"}`), 400)
	w = pr.call(u, "PUT", "/me/pan", `{"pan":"abcpe1234f"}`)
	assertFixture(t, "pro_pan_put_200.json", w, 200)
	if strings.Contains(strings.ToUpper(w.Body.String()), "ABCPE1234F") {
		t.Fatal("PAN leaked")
	}

	// Only reviews remain: pending_verification.
	assertFixture(t, "pro_readiness_200_pending_review.json", pr.call(u, "GET", "/readiness", ""), 200)

	// ---- admin review ----
	assertFixture(t, "admin_professionals_list_200.json", pr.admin(PermProsRead, "GET", "/professionals?status=pending_verification&city=HYD", ""), 200)
	assertFixture(t, "admin_professional_get_404.json", pr.admin(PermProsRead, "GET", "/professionals/"+uuid.NewSHA1(uuid.NameSpaceURL, []byte("nopro")).String(), ""), 404)
	assertFixture(t, "admin_documents_list_200.json", pr.admin(PermDocumentsReview, "GET", "/documents", ""), 200)
	proID := devseed.ID("professional", u.String()).String()
	assertFixture(t, "admin_professional_approve_422_incomplete.json", pr.admin(PermProsApprove, "POST", "/professionals/"+proID+"/approve", ""), 422)
	assertFixture(t, "admin_skill_verify_422_certificate.json",
		pr.admin(PermProsApprove, "POST", "/professionals/"+proID+"/skills/electrician/verify", `{"verified":true}`), 422)

	// View the police certificate: bytes, no-store, one audit row per view.
	w = pr.admin(PermDocumentsReview, "GET", "/documents/"+police.ID+"/view", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), fakePNG) || w.Header().Get("Content-Type") != "image/png" ||
		w.Header().Get("Cache-Control") != "no-store, private" || w.Header().Get("Location") != "" {
		t.Fatalf("view: %d %v %q", w.Code, w.Header(), w.Body.String())
	}
	if last := pr.pro.audits[len(pr.pro.audits)-1]; last != "document.view "+police.ID {
		t.Fatalf("view audit %q", last)
	}
	assertFixture(t, "admin_document_view_404.json", pr.admin(PermDocumentsReview, "GET", "/documents/"+uuid.NewSHA1(uuid.NameSpaceURL, []byte("nodoc")).String()+"/view", ""), 404)
	assertFixture(t, "admin_document_view_403_scope.json", pr.admin(PermProsRead, "GET", "/documents/"+police.ID+"/view", ""), 403)
	pr.media.unavailable = true
	assertFixture(t, "admin_document_view_503.json", pr.admin(PermDocumentsReview, "GET", "/documents/"+police.ID+"/view", ""), 503)
	pr.media.unavailable = false

	// Decide: reject needs a reason; approve the police certificate (the
	// background check clears) and the trade certificate (electrician).
	assertFixture(t, "admin_document_decide_400_reason.json", pr.admin(PermDocumentsReview, "POST", "/documents/"+police.ID+"/decide", `{"decision":"reject"}`), 400)
	assertFixture(t, "admin_document_decide_200_police.json", pr.admin(PermDocumentsReview, "POST", "/documents/"+police.ID+"/decide", `{"decision":"approve"}`), 200)
	assertFixture(t, "admin_document_decide_409_decided.json", pr.admin(PermDocumentsReview, "POST", "/documents/"+police.ID+"/decide", `{"decision":"approve"}`), 409)
	var tradeID string
	for _, id := range pr.pro.docOrder {
		if pr.pro.docs[id].Kind == "trade_certificate" {
			tradeID = id.String()
		}
	}
	mustStatus(t, "approve trade certificate", pr.admin(PermDocumentsReview, "POST", "/documents/"+tradeID+"/decide", `{"decision":"approve","reason":"ITI certificate checked"}`), 200)
	assertFixture(t, "admin_skill_verify_200_revoke.json",
		pr.admin(PermProsApprove, "POST", "/professionals/"+proID+"/skills/deep_cleaning/verify", `{"verified":false,"reason":"re-check"}`), 200)
	mustStatus(t, "verify deep cleaning", pr.admin(PermProsApprove, "POST", "/professionals/"+proID+"/skills/deep_cleaning/verify", `{"verified":true}`), 200)
	// The selfie the face match left pending: an admin approves it.
	adminReviewsRest(t, pr, u)

	// Approve: nothing missing now.
	assertFixture(t, "admin_professional_approve_200.json", pr.admin(PermProsApprove, "POST", "/professionals/"+proID+"/approve", `{"reason":"all documents checked"}`), 200)
	w = pr.admin(PermProsRead, "GET", "/professionals/"+proID, "")
	assertFixture(t, "admin_professional_get_200.json", w, 200)
	for _, leak := range []string{"23456789", "ABCPE1234F", "118833", "4471", "otp"} {
		if strings.Contains(strings.ToLower(w.Body.String()), strings.ToLower(leak)) {
			t.Fatalf("admin detail leaks %q: %s", leak, w.Body.String())
		}
	}
	assertFixture(t, "pro_readiness_200_approved.json", pr.call(u, "GET", "/readiness", ""), 200)

	// Suspend (role kept), reinstate, block (role revoked).
	assertFixture(t, "admin_professional_suspend_400_reason.json", pr.admin(PermProsSuspend, "POST", "/professionals/"+proID+"/suspend", `{}`), 400)
	assertFixture(t, "admin_professional_suspend_200.json", pr.admin(PermProsSuspend, "POST", "/professionals/"+proID+"/suspend", `{"reason":"customer complaint under review"}`), 200)
	assertFixture(t, "admin_professional_suspend_409_transition.json", pr.admin(PermProsSuspend, "POST", "/professionals/"+proID+"/suspend", `{"reason":"again"}`), 409)
	assertFixture(t, "admin_professional_reinstate_200.json", pr.admin(PermProsSuspend, "POST", "/professionals/"+proID+"/reinstate", `{"reason":"complaint resolved"}`), 200)
	assertFixture(t, "admin_professional_block_200.json", pr.admin(PermProsSuspend, "POST", "/professionals/"+proID+"/block", `{"reason":"identity fraud"}`), 200)
	assertFixture(t, "pro_me_patch_409_blocked.json", pr.call(u, "PATCH", "/me", `{"display_name":"Lakshmi D"}`), 409)

	// Roles: granted at apply (draft), on pending_verification and approval,
	// none on suspend, granted on reinstate, revoked on block.
	var mine []string
	for _, op := range pr.pro.roleOps {
		if strings.HasSuffix(op, u.String()) {
			mine = append(mine, strings.Fields(op)[0])
		}
	}
	if got := strings.Join(mine, ","); got != "grant,grant,grant,grant,revoke" {
		t.Fatalf("role intents %s", got)
	}
	// Events: applied, then status changes and document reviews.
	if pr.pro.events[0] != "doorstep.pro.applied" {
		t.Fatalf("events %v", pr.pro.events)
	}
	// Every admin write is audited under the token's actor.
	for _, a := range pr.pro.auditActs {
		if a.UserID != pr.actor {
			t.Fatalf("audit actor %v", a)
		}
	}
}

// Rejection is terminal and revokes the role; a rejected professional can
// no longer edit.
func TestContract_ProReject(t *testing.T) {
	pr := newProRig(t)
	mustStatus(t, "apply", pr.call(otherUser, "POST", "/apply", `{"display_name":"Ravi Kumar","city_code":"HYD"}`), 201)
	id := devseed.ID("professional", otherUser.String()).String()
	assertFixture(t, "admin_professional_reject_400_reason.json", pr.admin(PermProsApprove, "POST", "/professionals/"+id+"/reject", `{"reason":"  "}`), 400)
	assertFixture(t, "admin_professional_reject_200.json", pr.admin(PermProsApprove, "POST", "/professionals/"+id+"/reject", `{"reason":"documents do not match"}`), 200)
	mustCode(t, "edit after reject", pr.call(otherUser, "PUT", "/me/skills", `{"skill_codes":[]}`), 409, "DOORSTEP_INVALID_TRANSITION")
	if got := strings.Join(pr.pro.roleOps, ";"); got != "grant "+otherUser.String()+";revoke "+otherUser.String() {
		t.Fatalf("roles %s", got)
	}
}

// Gender comes from DigiLocker: a man may not declare women's salon, and a
// salon skill declared before DigiLocker blocks approval once DigiLocker
// says the gender does not fit.
func TestContract_ProGenderRules(t *testing.T) {
	pr := newProRig(t)
	u := otherUser
	mustStatus(t, "apply", pr.call(u, "POST", "/apply", `{"display_name":"Ravi Kumar","city_code":"HYD","category_ids":["`+idOf("category", "salon-women")+`"]}`), 201)
	// Declared before DigiLocker: allowed (gender unknown); an admin verified
	// it before DigiLocker said the gender (no certificate needed).
	mustStatus(t, "verify before DigiLocker", pr.admin(PermProsApprove, "POST", "/professionals/"+devseed.ID("professional", u.String()).String()+"/skills/salon_women/verify", `{"verified":true}`), 200)
	completeSteps(t, pr, u, "mock-male")
	id := devseed.ID("professional", u.String()).String()
	assertFixture(t, "admin_professional_approve_403_gender.json", pr.admin(PermProsApprove, "POST", "/professionals/"+id+"/approve", ""), 403)
	assertFixture(t, "pro_skills_put_403_gender.json", pr.call(u, "PUT", "/me/skills", `{"skill_codes":["deep_cleaning","salon_women"]}`), 403)
	mustCode(t, "admin verify women's salon for a man", pr.admin(PermProsApprove, "POST", "/professionals/"+id+"/skills/salon_women/verify", `{"verified":true}`), 403, "DOORSTEP_GENDER_RULE")
	// Dropping the salon skill clears the way.
	mustStatus(t, "skills", pr.call(u, "PUT", "/me/skills", `{"skill_codes":["deep_cleaning","salon_men"]}`), 200)
	adminReviewsRest(t, pr, u)
	mustStatus(t, "approve", pr.admin(PermProsApprove, "POST", "/professionals/"+id+"/approve", ""), 200)
}

// completeSteps does every self-service step for user and approves the
// police certificate.
func completeSteps(t *testing.T, pr *proRig, u uuid.UUID, code string) {
	t.Helper()
	steps := []struct{ method, path, body string }{
		{"PATCH", "/me", `{"photo_media_id":"` + mediaOf(u, "photo").String() + `"}`},
	}
	for _, s := range steps {
		mustStatus(t, s.path, pr.call(u, s.method, s.path, s.body), 200)
	}
	w := pr.call(u, "POST", "/digilocker/start", "")
	var start struct {
		State string `json:"state"`
	}
	decodeData(t, w, &start)
	mustStatus(t, "callback", pr.call(u, "POST", "/digilocker/callback", `{"code":"`+code+`","state":"`+start.State+`"}`), 200)
	for _, s := range []struct{ method, path, body string }{
		{"POST", "/selfie", `{"media_id":"` + mediaOf(u, "selfie").String() + `"}`},
		{"PUT", "/me/area", `{"zone_ids":["` + idOf("zone", "west-hitec-gachibowli") + `"],"home_lat":17.44,"home_lng":78.37,"radius_m":5000}`},
		{"PUT", "/me/hours", `{"items":[{"weekday":1,"start":"09:00","end":"18:00"}]}`},
		{"PUT", "/me/bank", `{"account_holder":"Test Pro","account_number":"123456789012","ifsc":"HDFC0000001"}`},
		{"POST", "/me/agreement", `{"version":"` + prokyc.AgreementVersion + `"}`},
	} {
		mustStatus(t, s.path, pr.call(u, s.method, s.path, s.body), 200)
	}
	w = pr.call(u, "POST", "/me/police-certificate", `{"media_id":"`+mediaOf(u, "police").String()+`","issued_on":"2026-09-01"}`)
	mustStatus(t, "police", w, 201)
	var doc struct {
		ID string `json:"id"`
	}
	decodeData(t, w, &doc)
	mustStatus(t, "decide police", pr.admin(PermDocumentsReview, "POST", "/documents/"+doc.ID+"/decide", `{"decision":"approve"}`), 200)
	adminReviewsRest(t, pr, u)
}

// adminReviewsRest is what an admin does after the self-service steps
// (nothing a professional submits approves itself, founder rule 4 Oct
// 2026): approve the pending selfie and verify every declared skill still
// pending.
func adminReviewsRest(t *testing.T, pr *proRig, u uuid.UUID) {
	t.Helper()
	proID := devseed.ID("professional", u.String())
	for _, id := range pr.pro.docOrder {
		if d := pr.pro.docs[id]; d.ProID == proID && d.Kind == "selfie" && d.Status == "pending" {
			mustStatus(t, "approve selfie", pr.admin(PermDocumentsReview, "POST", "/documents/"+id.String()+"/decide", `{"decision":"approve"}`), 200)
		}
	}
	var pending []string
	for code, s := range pr.pro.pros[proID].skills {
		if s.Status == "pending" {
			pending = append(pending, code)
		}
	}
	sort.Strings(pending)
	for _, code := range pending {
		mustStatus(t, "verify "+code, pr.admin(PermProsApprove, "POST", "/professionals/"+proID.String()+"/skills/"+code+"/verify", `{"verified":true}`), 200)
	}
}

// Approval is refused while any step is missing, step by step.
func TestProApprovalBlockedWhileStepsMissing(t *testing.T) {
	pr := newProRig(t)
	u := otherUser
	mustStatus(t, "apply", pr.call(u, "POST", "/apply", `{"display_name":"Ravi Kumar","city_code":"HYD","category_ids":["`+idOf("category", "home-cleaning")+`"]}`), 201)
	id := devseed.ID("professional", u.String()).String()
	w := pr.admin(PermProsApprove, "POST", "/professionals/"+id+"/approve", "")
	mustCode(t, "fresh", w, 422, "DOORSTEP_ONBOARDING_INCOMPLETE")
	_, details := errorCode(t, w)
	if got := fmt.Sprint(details["missing_steps"]); got != "[profile aadhaar_digilocker selfie_face_match skills service_area weekly_hours bank police_certificate agreement]" {
		t.Fatalf("missing %s", got)
	}
	completeSteps(t, pr, u, "mock-male")
	mustStatus(t, "approve", pr.admin(PermProsApprove, "POST", "/professionals/"+id+"/approve", ""), 200)
	if s := pr.pro.pros[devseed.ID("professional", u.String())].p.Status; s != "approved" {
		t.Fatalf("status %s", s)
	}
}

// A rejected police certificate never clears the check; nothing else does.
func TestPoliceCertificateRejectNeverClears(t *testing.T) {
	pr := newProRig(t)
	u := otherUser
	mustStatus(t, "apply", pr.call(u, "POST", "/apply", `{"display_name":"Ravi Kumar","city_code":"HYD"}`), 201)
	w := pr.call(u, "POST", "/me/police-certificate", `{"media_id":"`+mediaOf(u, "police").String()+`","issued_on":"2026-09-01"}`)
	var doc struct {
		ID string `json:"id"`
	}
	decodeData(t, w, &doc)
	mustStatus(t, "reject", pr.admin(PermDocumentsReview, "POST", "/documents/"+doc.ID+"/decide", `{"decision":"reject","reason":"blurred"}`), 200)
	p := pr.pro.pros[devseed.ID("professional", u.String())]
	if len(p.bg) != 1 || p.bg[0].status != "failed" {
		t.Fatalf("background %+v", p.bg)
	}
	// Someone else's certificate image is refused.
	mustCode(t, "foreign media", pr.call(u, "POST", "/me/police-certificate", `{"media_id":"`+mediaOf(proUser, "police").String()+`","issued_on":"2026-09-01"}`), 400, "DOORSTEP_INVALID_REQUEST")
}

// Selfie outcomes: below the threshold stays pending (reviewable document),
// media-service down stays pending, an uncomparable image is 422.
func TestProSelfieOutcomes(t *testing.T) {
	low := &facecompare.Mock{Similarity: 41}
	pr := newProRig(t, func(d *service.ProDeps) { d.Faces = low })
	u := otherUser
	mustStatus(t, "apply", pr.call(u, "POST", "/apply", `{"display_name":"Ravi Kumar","city_code":"HYD"}`), 201)
	w := pr.call(u, "POST", "/digilocker/start", "")
	var start struct {
		State string `json:"state"`
	}
	decodeData(t, w, &start)
	mustStatus(t, "callback", pr.call(u, "POST", "/digilocker/callback", `{"code":"mock-male","state":"`+start.State+`"}`), 200)
	selfie := `{"media_id":"` + mediaOf(u, "selfie").String() + `"}`
	assertFixture(t, "pro_selfie_200_pending.json", pr.call(u, "POST", "/selfie", selfie), 200)
	low.Err = facecompare.ErrImageUnsupported
	assertFixture(t, "pro_selfie_422_failed.json", pr.call(u, "POST", "/selfie", selfie), 422)
	low.Err = facecompare.ErrUnavailable
	mustStatus(t, "unavailable stays pending", pr.call(u, "POST", "/selfie", selfie), 200)
	if pr.pro.pros[devseed.ID("professional", u.String())].selfie {
		t.Fatal("selfie passed on an error")
	}
	// The pending selfie is reviewable: an admin approves it.
	var pending string
	for _, id := range pr.pro.docOrder {
		if d := pr.pro.docs[id]; d.Kind == "selfie" && d.Status == "pending" {
			pending = id.String()
		}
	}
	mustStatus(t, "approve selfie", pr.admin(PermDocumentsReview, "POST", "/documents/"+pending+"/decide", `{"decision":"approve"}`), 200)
	if !pr.pro.pros[devseed.ID("professional", u.String())].selfie {
		t.Fatal("approved selfie did not pass the step")
	}
}

func TestProDigiLockerAndPIIUnavailable(t *testing.T) {
	pr := newProRig(t, func(d *service.ProDeps) {
		d.DigiLocker = digilocker.Settings{Mode: digilocker.ModeDisabled}
		d.PII = nil
	})
	mustStatus(t, "apply", pr.call(proUser, "POST", "/apply", `{"display_name":"Lakshmi Devi","city_code":"HYD"}`), 201)
	assertFixture(t, "pro_digilocker_start_503.json", pr.call(proUser, "POST", "/digilocker/start", ""), 503)
	assertFixture(t, "pro_bank_put_503_pii.json",
		pr.call(proUser, "PUT", "/me/bank", `{"account_holder":"Lakshmi Devi","account_number":"000123456789","ifsc":"SBIN0001234"}`), 503)
}

func TestBackgroundCheckWebhookUnknownProvider(t *testing.T) {
	pr := newProRig(t)
	for _, p := range []string{"authbridge", "uploaded_document", "mock"} {
		w := pr.do(req{method: "POST", path: "/v1/doorstep/webhooks/background-check/" + p, body: `{"event":"x"}`})
		if p == "authbridge" {
			assertFixture(t, "webhook_background_check_404.json", w, 404)
		}
		mustCode(t, "webhook "+p, w, 404, "DOORSTEP_NOT_FOUND")
	}
	// Behind the internal key like every /v1/doorstep route.
	if w := pr.do(req{method: "POST", path: "/v1/doorstep/webhooks/background-check/authbridge", body: `{}`, noKey: true}); w.Code != 401 {
		t.Fatalf("webhook without key: %d", w.Code)
	}
}

// Every /pro route needs the gateway identity.
func TestProRoutesRequireGatewayIdentity(t *testing.T) {
	pr := newProRig(t)
	for _, rt := range wantRoutes {
		method, path, _ := strings.Cut(rt, " ")
		if !strings.HasPrefix(path, "/v1/doorstep/pro/") {
			continue
		}
		path = strings.NewReplacer(":code", "electrician", ":date", "2026-10-10").Replace(path)
		w := pr.do(req{method: method, path: path, body: "{}"})
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "AUTH_REQUIRED") {
			t.Errorf("%s %s without identity: %d %s", method, path, w.Code, w.Body.String())
		}
	}
}
