package itest

import (
	"context"
	"github.com/atpost/doorstep-service/internal/http"
	"github.com/google/uuid"
	"testing"
)

// The live store uses admin audit guards, never a UIDAI assertion. The suite
// itself enforces a *_test database and reruns every migration twice.
func TestManualIdentityReviewOnDatabase(t *testing.T) {
	rg := newProITRig(t)
	p := pool(t)
	ctx := context.Background()
	user := uuid.New()
	status, body := rg.as(user, "POST", "/apply", `{"display_name":"Offline Professional","city_code":"HYD"}`)
	want(t, "apply", status, body, 201)
	var pro struct {
		ID string `json:"id"`
	}
	data(t, body, &pro)
	// PAN alone is neither identity nor a government verification.
	status, body = rg.as(user, "PUT", "/me/pan", `{"pan":"ABCPE1234F"}`)
	want(t, "PAN", status, body, 200)
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID+"/identity-review", http.PermProsApprove, `{"gender":"female","reason":"Review"}`)
	want(t, "selfie required", status, body, 422)
	status, body = rg.as(user, "POST", "/selfie", `{"media_id":"`+uuid.NewString()+`"}`)
	want(t, "offline selfie without DigiLocker", status, body, 200)
	var documentID string
	if err := p.QueryRow(ctx, `SELECT id FROM doorstep.pro_documents WHERE pro_id=$1 AND kind='selfie' AND status='pending'`, pro.ID).Scan(&documentID); err != nil {
		t.Fatal(err)
	}
	status, body = rg.adminCall("POST", "/documents/"+documentID+"/decide", http.PermDocumentsReview, `{"decision":"approve"}`)
	want(t, "review selfie", status, body, 200)
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID+"/identity-review", http.PermProsRead, `{"gender":"female","reason":"Review"}`)
	want(t, "wrong permission", status, body, 403)
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID+"/identity-review", http.PermProsApprove, `{"gender":"female","reason":"Reviewed identity and salon eligibility"}`)
	want(t, "human review", status, body, 200)
	var source string
	var approved bool
	if err := p.QueryRow(ctx, `SELECT gender_source,status='approved' FROM doorstep.professionals WHERE id=$1`, pro.ID).Scan(&source, &approved); err != nil || source != "admin_review" || approved {
		t.Fatalf("identity review approved account: %v", err)
	}
	// A direct passed row without the corresponding audit is refused by SQL.
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.pro_kyc_checks(pro_id,kind,status)VALUES($1,'manual_identity','passed')`, pro.ID); err == nil {
		t.Fatal("unaudited identity accepted")
	}
	status, body = rg.as(user, "PUT", "/me/pan", `{"pan":"ZZZPZ0000Z"}`)
	want(t, "replace PAN", status, body, 200)
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.pro_kyc_checks WHERE pro_id=$1 AND kind='manual_identity' AND status='passed'`, pro.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old identity survives PAN change")
	}
}
