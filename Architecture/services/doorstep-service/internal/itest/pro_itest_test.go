package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/bgv"
	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/digilocker"
	"github.com/atpost/doorstep-service/internal/facecompare"
	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/mediaclient"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/identityroles"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Professional onboarding (A2) on the real store: role intents and events in
// the same transaction, gender only from DigiLocker, sealed PII, the day-off
// calendar block, the background-check guard in the database.

// ownAll is a media verifier that treats every id as the caller's own
// processed image (media-service is not part of this suite) and serves
// image bytes.
type ownAll struct{}

func (ownAll) VerifyOwned(context.Context, uuid.UUID, uuid.UUID, ...mediaclient.Kind) error {
	return nil
}
func (ownAll) FetchImage(context.Context, uuid.UUID) ([]byte, string, error) {
	return []byte("\x89PNG\r\n\x1a\nit"), "image/png", nil
}

func newProITRig(t *testing.T) *itRig {
	t.Helper()
	rg := newRig(t, time.Now())
	p := pool(t)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                     "admin-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    strings.Join(doorstephttp.AdminPermissions, ","),
	}
	v, err := doorstephttp.ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	rg.admin, err = servicetoken.NewSignerFromBase64(doorstephttp.IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	crypto, err := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x5a}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	dl, _ := digilocker.New("mock", false, digilocker.HTTPConfig{})
	provider, _ := bgv.New("uploaded_document", true)
	st := store.New(p).WithRoleIntents(identityroles.NewOutbox("doorstep", "doorstep-service"))
	tc, _ := tax.NewGST(nil, gstin(t))
	svc := service.New(st, tc, 15*time.Minute).WithClock(func() time.Time { return rg.now }, uuid.New).WithPro(service.ProDeps{
		Store:      st,
		DigiLocker: digilocker.Settings{Mode: digilocker.ModeMock, Client: dl, RedirectURI: "http://localhost:8080/doorstep-pro/digilocker"},
		Faces:      &facecompare.Mock{Similarity: 93}, SelfieMinSimilarity: 80,
		Media: ownAll{}, Images: ownAll{}, PII: crypto, BGV: provider,
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	doorstephttp.New(svc, itKey).WithServiceAuth(v).RegisterRoutes(r)
	rg.r = r
	return rg
}

func (rg *itRig) as(user uuid.UUID, method, path, body string) (int, []byte) {
	return rg.call(method, "/v1/doorstep/pro"+path, body, map[string]string{"X-User-Id": user.String()})
}

func want(t *testing.T, what string, status int, body []byte, wantStatus int) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("%s: %d %s, want %d", what, status, body, wantStatus)
	}
}

func dlComplete(t *testing.T, rg *itRig, user uuid.UUID, code string) (int, []byte) {
	t.Helper()
	status, body := rg.as(user, "POST", "/digilocker/start", "")
	want(t, "digilocker start", status, body, 200)
	var s struct {
		State string `json:"state"`
	}
	data(t, body, &s)
	return rg.as(user, "POST", "/digilocker/callback", `{"code":"`+code+`","state":"`+s.State+`"}`)
}

func TestProOnboardingOnTheDatabase(t *testing.T) {
	rg := newProITRig(t)
	p := pool(t)
	ctx := context.Background()
	u := uuid.New()
	m := func() string { return uuid.NewString() }

	status, body := rg.as(u, "POST", "/apply", `{"display_name":"Lakshmi Devi","city_code":"HYD","category_ids":["`+id("category", "home-cleaning")+`"]}`)
	want(t, "apply", status, body, 201)
	var pro struct {
		ID string `json:"id"`
	}
	data(t, body, &pro)
	status, body = rg.as(u, "POST", "/apply", `{"display_name":"Lakshmi Devi","city_code":"HYD"}`)
	want(t, "apply twice", status, body, 409)

	// Role intent and applied event committed with the row.
	var grants int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.identity_role_intents WHERE user_id = $1 AND op = 'grant'
		AND role_name = 'service_professional' AND service = 'doorstep-service'`, u).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("grant intents %d %v", grants, err)
	}
	var payload []byte
	if err := p.QueryRow(ctx, `SELECT payload FROM doorstep.outbox_events WHERE event_type = 'doorstep.pro.applied' AND partition_key = $1`, u.String()).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var applied struct {
		ProUserID string `json:"pro_user_id"`
		Data      struct {
			ProID     string `json:"pro_id"`
			ProUserID string `json:"pro_user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &applied); err != nil || applied.ProUserID != u.String() || applied.Data.ProUserID != u.String() || applied.Data.ProID != pro.ID {
		t.Fatalf("applied payload %s", payload)
	}

	chk(t, "photo", 200)(rg.as(u, "PATCH", "/me", `{"photo_media_id":"`+m()+`"}`))
	status, body = dlComplete(t, rg, u, "mock-female")
	want(t, "digilocker", status, body, 200)
	var gender, source string
	if err := p.QueryRow(ctx, `SELECT gender, gender_source FROM doorstep.professionals WHERE user_id = $1`, u).Scan(&gender, &source); err != nil || gender != "female" || source != "digilocker" {
		t.Fatalf("gender %q source %q %v", gender, source, err)
	}
	chk(t, "selfie", 200)(rg.as(u, "POST", "/selfie", `{"media_id":"`+m()+`"}`))
	chk(t, "skills", 200)(rg.as(u, "PUT", "/me/skills", `{"skill_codes":["deep_cleaning","electrician","salon_women"]}`))
	status, body = rg.as(u, "POST", "/me/skills/electrician/certificate", `{"media_id":"`+m()+`","issued_on":"2019-06-01","certificate_number":"ITI/EL/2019/4471"}`)
	want(t, "trade certificate", status, body, 201)
	var trade struct {
		ID string `json:"id"`
	}
	data(t, body, &trade)
	chk(t, "area", 200)(rg.as(u, "PUT", "/me/area", `{"zone_ids":["`+id("zone", "west-hitec-gachibowli")+`","`+id("zone", "central-banjara-jubilee")+`"],"home_lat":17.44,"home_lng":78.37,"radius_m":8000}`))
	chk(t, "hours", 200)(rg.as(u, "PUT", "/me/hours", `{"items":[{"weekday":1,"start":"09:00","end":"13:00"},{"weekday":1,"start":"14:00","end":"18:00"}]}`))
	status, body = rg.as(u, "GET", "/me/hours", "")
	if status != 200 || !bytes.Contains(body, []byte(`{"weekday":1,"start":"14:00","end":"18:00"}`)) {
		t.Fatalf("hours %d %s", status, body)
	}

	// Day off: a day_off block on the calendar; the exclusion constraint
	// refuses one over a job, and removing the day releases the block.
	day := time.Now().In(service.IST).AddDate(0, 0, 3).Format("2006-01-02")
	chk(t, "day off", 201)(rg.as(u, "POST", "/me/days-off", `{"date":"`+day+`","reason":"festival"}`))
	var blocks int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.pro_calendar_blocks b JOIN doorstep.professionals p ON p.id = b.pro_id
		WHERE p.user_id = $1 AND b.kind = 'day_off' AND b.active
		  AND b.during = tstzrange(($2::date)::timestamp AT TIME ZONE 'Asia/Kolkata', ($2::date + 1)::timestamp AT TIME ZONE 'Asia/Kolkata', '[)')`, u, day).Scan(&blocks); err != nil || blocks != 1 {
		t.Fatalf("day_off block %d %v", blocks, err)
	}
	busy := time.Now().In(service.IST).AddDate(0, 0, 4)
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.pro_calendar_blocks (pro_id, kind, during) SELECT id, 'break', tstzrange($2, $3, '[)')
		FROM doorstep.professionals WHERE user_id = $1`, u, time.Date(busy.Year(), busy.Month(), busy.Day(), 10, 0, 0, 0, service.IST),
		time.Date(busy.Year(), busy.Month(), busy.Day(), 12, 0, 0, 0, service.IST)); err != nil {
		t.Fatal(err)
	}
	status, body = rg.as(u, "POST", "/me/days-off", `{"date":"`+busy.Format("2006-01-02")+`"}`)
	if status != 409 || errCode(body) != "DOORSTEP_CONFLICT" {
		t.Fatalf("day off over a job: %d %s", status, body)
	}
	if status, body := rg.as(u, "DELETE", "/me/days-off/"+day, ""); status != 204 {
		t.Fatalf("delete day off %d %s", status, body)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.pro_calendar_blocks b JOIN doorstep.professionals p ON p.id = b.pro_id
		WHERE p.user_id = $1 AND b.kind = 'day_off' AND b.active`, u).Scan(&blocks); err != nil || blocks != 0 {
		t.Fatalf("released day_off blocks %d %v", blocks, err)
	}

	// Bank: sealed in the database, masked on the wire.
	status, body = rg.as(u, "PUT", "/me/bank", `{"account_holder":"Lakshmi Devi","account_number":"000123456789","ifsc":"SBIN0001234"}`)
	if status != 200 || bytes.Contains(body, []byte("123456789")) || !bytes.Contains(body, []byte(`"account_last4":"6789"`)) {
		t.Fatalf("bank %d %s", status, body)
	}
	var sealed []byte
	var last4, kv string
	if err := p.QueryRow(ctx, `SELECT a.account_number_sealed, a.account_last4, a.key_version FROM doorstep.pro_payout_accounts a
		JOIN doorstep.professionals p ON p.id = a.pro_id WHERE p.user_id = $1 AND a.active`, u).Scan(&sealed, &last4, &kv); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("000123456789")) || last4 != "6789" || kv != "v1" {
		t.Fatalf("payout row last4=%s kv=%s plaintext=%v", last4, kv, bytes.Contains(sealed, []byte("000123456789")))
	}

	// Police certificate: pending document + pending background check.
	issued := time.Now().In(service.IST).AddDate(0, -1, 0).Format("2006-01-02")
	status, body = rg.as(u, "POST", "/me/police-certificate", `{"media_id":"`+m()+`","issued_on":"`+issued+`","certificate_number":"TSPCC/2026/118833"}`)
	want(t, "police", status, body, 201)
	var police struct {
		ID string `json:"id"`
	}
	data(t, body, &police)
	var bgStatus string
	if err := p.QueryRow(ctx, `SELECT status FROM doorstep.background_checks WHERE document_id = $1 AND source = 'uploaded_document'`, police.ID).Scan(&bgStatus); err != nil || bgStatus != "pending" {
		t.Fatalf("background check %q %v", bgStatus, err)
	}
	var numSealed []byte
	if err := p.QueryRow(ctx, `SELECT number_sealed FROM doorstep.pro_documents WHERE id = $1`, police.ID).Scan(&numSealed); err != nil || len(numSealed) == 0 || bytes.Contains(numSealed, []byte("118833")) {
		t.Fatalf("certificate number not sealed: %v", err)
	}
	chk(t, "agreement", 200)(rg.as(u, "POST", "/me/agreement", `{"version":"`+prokyc.AgreementVersion+`"}`))
	status, body = rg.as(u, "PUT", "/me/pan", `{"pan":"ABCPE1234F"}`)
	if status != 200 || !bytes.Contains(body, []byte(`"status":"pending_verification"`)) {
		t.Fatalf("pan / pending_verification: %d %s", status, body)
	}
	var panSealed []byte
	var panLast4 string
	if err := p.QueryRow(ctx, `SELECT pan_sealed, pan_last4 FROM doorstep.professionals WHERE user_id = $1`, u).Scan(&panSealed, &panLast4); err != nil ||
		len(panSealed) == 0 || bytes.Contains(panSealed, []byte("ABCPE1234F")) || panLast4 != "234F" {
		t.Fatalf("PAN not sealed (last4 %q, err %v)", panLast4, err)
	}

	// Admin: approval refused while the police certificate is in review.
	proPath := "/professionals/" + pro.ID
	status, body = rg.adminCall("POST", proPath+"/approve", doorstephttp.PermProsApprove, "")
	if status != 422 || errCode(body) != "DOORSTEP_ONBOARDING_INCOMPLETE" || !bytes.Contains(body, []byte(`"police_certificate"`)) {
		t.Fatalf("approve early: %d %s", status, body)
	}
	status, body = rg.adminCall("POST", proPath+"/skills/electrician/verify", doorstephttp.PermProsApprove, `{"verified":true}`)
	if status != 422 || errCode(body) != "DOORSTEP_CERTIFICATE_REQUIRED" {
		t.Fatalf("verify without certificate: %d %s", status, body)
	}
	// View: bytes, audited.
	status, body = rg.adminCall("GET", "/documents/"+police.ID+"/view", doorstephttp.PermDocumentsReview, "")
	if status != 200 || !bytes.HasPrefix(body, []byte("\x89PNG")) {
		t.Fatalf("view %d", status)
	}
	var views int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.admin_audit_log WHERE action = 'document.view' AND entity_id = $1 AND actor_user_id = $2
		AND permission = $3`, police.ID, rg.actor, doorstephttp.PermDocumentsReview).Scan(&views); err != nil || views != 1 {
		t.Fatalf("view audit rows %d %v", views, err)
	}
	chk(t, "approve police", 200)(rg.adminCall("POST", "/documents/"+police.ID+"/decide", doorstephttp.PermDocumentsReview, `{"decision":"approve"}`))
	var from, until time.Time
	if err := p.QueryRow(ctx, `SELECT status, valid_from, valid_until FROM doorstep.background_checks WHERE document_id = $1`, police.ID).Scan(&bgStatus, &from, &until); err != nil {
		t.Fatal(err)
	}
	if bgStatus != "clear" || from.Format("2006-01-02") != issued || !until.Equal(from.AddDate(1, 0, 0)) {
		t.Fatalf("background %s %s..%s", bgStatus, from, until)
	}
	chk(t, "approve trade", 200)(rg.adminCall("POST", "/documents/"+trade.ID+"/decide", doorstephttp.PermDocumentsReview, `{"decision":"approve","reason":"ok"}`))

	// Approve (woman: women's salon is fine; salon needs the clear check: it is).
	status, body = rg.adminCall("POST", proPath+"/approve", doorstephttp.PermProsApprove, `{"reason":"checked"}`)
	if status != 200 || !bytes.Contains(body, []byte(`"status":"approved"`)) {
		t.Fatalf("approve %d %s", status, body)
	}
	status, body = rg.adminCall("GET", proPath, doorstephttp.PermProsRead, "")
	if status != 200 || bytes.Contains(body, []byte("123456789")) || bytes.Contains(body, []byte("ABCPE")) || !bytes.Contains(body, []byte(`"can_go_on_duty":true`)) {
		t.Fatalf("detail %d %s", status, body)
	}
	chk(t, "suspend", 200)(rg.adminCall("POST", proPath+"/suspend", doorstephttp.PermProsSuspend, `{"reason":"complaint"}`))
	chk(t, "reinstate", 200)(rg.adminCall("POST", proPath+"/reinstate", doorstephttp.PermProsSuspend, `{"reason":"resolved"}`))
	chk(t, "block", 200)(rg.adminCall("POST", proPath+"/block", doorstephttp.PermProsSuspend, `{"reason":"fraud"}`))

	// Role intents in order: grant at apply, at pending_verification, at
	// approval, none at suspend, grant at reinstate, revoke at block.
	rows, err := p.Query(ctx, `SELECT op FROM doorstep.identity_role_intents WHERE user_id = $1 ORDER BY id`, u)
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for rows.Next() {
		var op string
		_ = rows.Scan(&op)
		ops = append(ops, op)
	}
	rows.Close()
	if got := strings.Join(ops, ","); got != "grant,grant,grant,grant,revoke" {
		t.Fatalf("role intents %s", got)
	}
	// Events carry pro_user_id; status changes name both statuses.
	rows, err = p.Query(ctx, `SELECT event_type, payload FROM doorstep.outbox_events WHERE partition_key = $1 ORDER BY id`, u.String())
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for rows.Next() {
		var et string
		var pl []byte
		_ = rows.Scan(&et, &pl)
		var env struct {
			ProUserID string          `json:"pro_user_id"`
			Version   int             `json:"version"`
			Data      json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(pl, &env); err != nil || env.ProUserID != u.String() || env.Version != 1 {
			t.Fatalf("envelope %s", pl)
		}
		types = append(types, et)
	}
	rows.Close()
	if got := strings.Join(types, ","); got != "doorstep.pro.applied,doorstep.pro.status_changed,doorstep.pro.document_reviewed,doorstep.pro.document_reviewed,"+
		"doorstep.pro.status_changed,doorstep.pro.status_changed,doorstep.pro.status_changed,doorstep.pro.status_changed" {
		t.Fatalf("events %s", got)
	}
	status, body = rg.as(u, "PUT", "/me/hours", `{"items":[]}`)
	if status != 409 || errCode(body) != "DOORSTEP_INVALID_TRANSITION" {
		t.Fatalf("blocked edits: %d %s", status, body)
	}
}

// The database refuses a clear background check from an uploaded document
// unless its own police certificate is approved, whatever writes it.
func TestBackgroundCheckClearNeedsApprovedDocument(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	mk := func() (pro, doc, check uuid.UUID) {
		pro, doc = uuid.New(), uuid.New()
		if _, err := p.Exec(ctx, `INSERT INTO doorstep.professionals (id, user_id, display_name, city_code) VALUES ($1, $2, 'BG', 'HYD')`, pro, uuid.New()); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Exec(ctx, `INSERT INTO doorstep.pro_documents (id, pro_id, kind, media_id, issued_on) VALUES ($1, $2, 'police_certificate', $3, CURRENT_DATE - 10)`,
			doc, pro, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if err := p.QueryRow(ctx, `INSERT INTO doorstep.background_checks (pro_id, source, provider, document_id) VALUES ($1, 'uploaded_document', 'uploaded_document', $2) RETURNING id`,
			pro, doc).Scan(&check); err != nil {
			t.Fatal(err)
		}
		return
	}
	clear := func(check uuid.UUID) error {
		_, err := p.Exec(ctx, `UPDATE doorstep.background_checks SET status = 'clear', valid_from = CURRENT_DATE - 10, valid_until = CURRENT_DATE + 355 WHERE id = $1`, check)
		return err
	}
	isGuard := func(err error) bool {
		var pg *pgconn.PgError
		return errors.As(err, &pg) && pg.Code == "23514" && pg.ConstraintName == "ck_doorstep_bg_clear_needs_document"
	}
	pro, doc, check := mk()
	if err := clear(check); !isGuard(err) {
		t.Fatalf("clear with a pending certificate: %v", err)
	}
	if _, err := p.Exec(ctx, `UPDATE doorstep.pro_documents SET status = 'rejected' WHERE id = $1`, doc); err != nil {
		t.Fatal(err)
	}
	if err := clear(check); !isGuard(err) {
		t.Fatalf("clear with a rejected certificate: %v", err)
	}
	// Another professional's approved certificate does not count.
	_, otherDoc, _ := mk()
	if _, err := p.Exec(ctx, `UPDATE doorstep.pro_documents SET status = 'approved' WHERE id = $1`, otherDoc); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE doorstep.background_checks SET document_id = $2 WHERE id = $1`, check, otherDoc); err != nil {
		t.Fatal(err)
	}
	if err := clear(check); !isGuard(err) {
		t.Fatalf("clear with another professional's certificate: %v", err)
	}
	if _, err := p.Exec(ctx, `UPDATE doorstep.background_checks SET document_id = $2 WHERE id = $1`, check, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE doorstep.pro_documents SET status = 'approved' WHERE id = $1`, doc); err != nil {
		t.Fatal(err)
	}
	if err := clear(check); err != nil {
		t.Fatalf("clear with its approved certificate: %v", err)
	}
	// A clear INSERT without an approved certificate is refused too.
	_, pendingDoc, _ := mk()
	_, err := p.Exec(ctx, `INSERT INTO doorstep.background_checks (pro_id, source, document_id, status, valid_from, valid_until)
		SELECT pro_id, 'uploaded_document', id, 'clear', CURRENT_DATE, CURRENT_DATE + 30 FROM doorstep.pro_documents WHERE id = $1`, pendingDoc)
	if !isGuard(err) {
		t.Fatalf("clear insert: %v", err)
	}
	_ = pro
}

// One DigiLocker account verifies one professional; states are single-use
// and bound to their professional.
func TestDigiLockerIdentityAndState(t *testing.T) {
	rg := newProITRig(t)
	a, b := uuid.New(), uuid.New()
	for _, u := range []uuid.UUID{a, b} {
		chk(t, "apply", 201)(rg.as(u, "POST", "/apply", `{"display_name":"Pro `+u.String()[:4]+`","city_code":"HYD"}`))
	}
	code := "mock-male-" + uuid.NewString()
	chk(t, "a verifies", 200)(dlComplete(t, rg, a, code))
	status, body := dlComplete(t, rg, b, code)
	if status != 409 || errCode(body) != "DOORSTEP_CONFLICT" {
		t.Fatalf("same DigiLocker account for b: %d %s", status, body)
	}
	status, body = rg.as(b, "POST", "/digilocker/start", "")
	want(t, "start", status, body, 200)
	var s struct {
		State string `json:"state"`
	}
	data(t, body, &s)
	// a cannot complete b's state; b's state is not consumed by a's attempt.
	if status, body := rg.as(a, "POST", "/digilocker/callback", `{"code":"mock-female","state":"`+s.State+`"}`); status != 400 {
		t.Fatalf("foreign state: %d %s", status, body)
	}
	chk(t, "b completes", 200)(rg.as(b, "POST", "/digilocker/callback", `{"code":"mock-female-`+uuid.NewString()+`","state":"`+s.State+`"}`))
	if status, body := rg.as(b, "POST", "/digilocker/callback", `{"code":"mock-female","state":"`+s.State+`"}`); status != 400 || !bytes.Contains(body, []byte("already used")) {
		t.Fatalf("reuse: %d %s", status, body)
	}
	var g string
	if err := pool(t).QueryRow(context.Background(), `SELECT gender FROM doorstep.professionals WHERE user_id = $1`, b).Scan(&g); err != nil || g != "female" {
		t.Fatalf("b gender %q %v", g, err)
	}
	// Gender is written by RecordAadhaar only: nothing else in the API sets it.
	if status, _ := rg.as(b, "PATCH", "/me", `{"gender":"male"}`); status != 400 {
		t.Fatalf("gender patch: %d", status)
	}
}

// chk asserts the (status, body) of a call: chk(t, "what", 200)(rg.as(...)).
func chk(t *testing.T, what string, wantStatus int) func(int, []byte) {
	return func(status int, body []byte) {
		t.Helper()
		want(t, what, status, body, wantStatus)
	}
}

// Rejection revokes service_professional on the database (and suspension
// never does: TestProOnboardingOnTheDatabase).
func TestRejectRevokesRoleOnTheDatabase(t *testing.T) {
	rg := newProITRig(t)
	u := uuid.New()
	status, body := rg.as(u, "POST", "/apply", `{"display_name":"Rejected Pro","city_code":"HYD"}`)
	want(t, "apply", status, body, 201)
	var pro struct {
		ID string `json:"id"`
	}
	data(t, body, &pro)
	chk(t, "reject", 200)(rg.adminCall("POST", "/professionals/"+pro.ID+"/reject", doorstephttp.PermProsApprove, `{"reason":"documents do not match"}`))
	chk(t, "approve after reject", 409)(rg.adminCall("POST", "/professionals/"+pro.ID+"/approve", doorstephttp.PermProsApprove, ""))
	rows, err := pool(t).Query(context.Background(), `SELECT op FROM doorstep.identity_role_intents WHERE user_id = $1 ORDER BY id`, u)
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for rows.Next() {
		var op string
		_ = rows.Scan(&op)
		ops = append(ops, op)
	}
	rows.Close()
	if got := strings.Join(ops, ","); got != "grant,revoke" {
		t.Fatalf("role intents %s", got)
	}
	var audited int
	if err := pool(t).QueryRow(context.Background(), `SELECT count(*) FROM doorstep.admin_audit_log WHERE action = 'professional.reject'
		AND entity_id = $1 AND actor_user_id = $2 AND permission = $3`, pro.ID, rg.actor, doorstephttp.PermProsApprove).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("reject audit %d %v", audited, err)
	}
}

// RecordAadhaar is the only writer of gender and never changes a recorded one.
func TestRecordAadhaarNeverChangesGender(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	st := store.New(p)
	pro := uuid.New()
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.professionals (id, user_id, display_name, city_code) VALUES ($1, $2, 'G', 'HYD')`, pro, uuid.New()); err != nil {
		t.Fatal(err)
	}
	ref := "digilocker:it-" + uuid.NewString() + ":ADHAR"
	if err := st.RecordAadhaar(ctx, pro, store.AadhaarRecord{Provider: "mock", Reference: ref, DocTypeHash: "h", Gender: "female"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordAadhaar(ctx, pro, store.AadhaarRecord{Provider: "mock", Reference: ref, DocTypeHash: "h", Gender: "male"}); !errors.Is(err, store.ErrGenderMismatch) {
		t.Fatalf("gender change: %v", err)
	}
	var g, src string
	if err := p.QueryRow(ctx, `SELECT gender, gender_source FROM doorstep.professionals WHERE id = $1`, pro).Scan(&g, &src); err != nil || g != "female" || src != "digilocker" {
		t.Fatalf("gender %s %s %v", g, src, err)
	}
	// The schema refuses a gender without its DigiLocker source.
	if _, err := p.Exec(ctx, `UPDATE doorstep.professionals SET gender_source = NULL WHERE id = $1`, pro); err == nil {
		t.Fatal("gender without source accepted")
	}
}

// A rejected police certificate fails its background check on the database.
func TestPoliceCertificateRejectedOnTheDatabase(t *testing.T) {
	rg := newProITRig(t)
	u := uuid.New()
	chk(t, "apply", 201)(rg.as(u, "POST", "/apply", `{"display_name":"Police Reject","city_code":"HYD"}`))
	issued := time.Now().In(service.IST).AddDate(0, 0, -5).Format("2006-01-02")
	status, body := rg.as(u, "POST", "/me/police-certificate", `{"media_id":"`+uuid.NewString()+`","issued_on":"`+issued+`"}`)
	want(t, "police", status, body, 201)
	var doc struct {
		ID string `json:"id"`
	}
	data(t, body, &doc)
	chk(t, "reject", 200)(rg.adminCall("POST", "/documents/"+doc.ID+"/decide", doorstephttp.PermDocumentsReview, `{"decision":"reject","reason":"blurred"}`))
	var st string
	var until *time.Time
	if err := pool(t).QueryRow(context.Background(), `SELECT status, valid_until FROM doorstep.background_checks WHERE document_id = $1`, doc.ID).Scan(&st, &until); err != nil || st != "failed" || until != nil {
		t.Fatalf("background %s %v %v", st, until, err)
	}
	// A new certificate may be uploaded after a rejection.
	chk(t, "re-upload", 201)(rg.as(u, "POST", "/me/police-certificate", `{"media_id":"`+uuid.NewString()+`","issued_on":"`+issued+`"}`))
}
