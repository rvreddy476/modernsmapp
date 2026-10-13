package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"testing"
)

func TestPartnerApplicationReviewOnDatabase(t *testing.T) {
	rg := newProITRig(t)
	p := pool(t)
	ctx := context.Background()
	user := uuid.New()
	input := `{"review_consent":true,"first_name":"Test","last_name":"Partner","gender":"female","state_code":"36","town":"Warangal","skill_codes":["deep_cleaning"],"identity_kind":"passport","identity_media_id":"` + uuid.NewString() + `","alternate_phone":"9876543211"}`
	var payload map[string]any
	_ = json.Unmarshal([]byte(input), &payload)
	payload["pan"] = "ABCPE1234F"
	payload["selfie_media_id"] = uuid.NewString()
	// Legacy catalogue flags must not require experience evidence. The whole
	// application, including skill and bank proof, is submitted in one request.
	payload["skill_codes"] = []string{"electrician"}
	payload["bank"] = map[string]string{"account_holder": "Test Partner", "account_number": "000123456789", "ifsc": "SBIN0001234", "bank_name": "State Bank", "proof_kind": "passbook", "proof_media_id": uuid.NewString()}
	rawInput, _ := json.Marshal(payload)
	input = string(rawInput)
	status, body := rg.as(user, "POST", "/application", input)
	want(t, "submit outside pilot city", status, body, 201)
	var pro model.Professional
	data(t, body, &pro)
	if pro.Status != "pending_verification" || pro.Application == nil || pro.Application.AlternatePhoneVerifiedAt != nil || pro.Gender != nil {
		t.Fatalf("wrong intake status: %+v", pro)
	}
	firstID := pro.ID
	status, body = rg.as(user, "POST", "/application", input)
	want(t, "idempotent retry", status, body, 201)
	data(t, body, &pro)
	if pro.ID != firstID {
		t.Fatal("duplicate application")
	}
	var docs int
	var docID string
	var raw []byte
	if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.pro_documents WHERE pro_id=$1`, pro.ID).Scan(&docs); err != nil || docs != 3 {
		t.Fatal("duplicate documents", docs, err)
	}
	if err := p.QueryRow(ctx, `SELECT identity_document_id,alternate_phone_sealed FROM doorstep.pro_applications WHERE pro_id=$1`, pro.ID).Scan(&docID, &raw); err != nil || len(raw) == 0 {
		t.Fatal("alternate contact not sealed", err)
	}
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID.String()+"/identity-review", http.PermProsApprove, `{"gender":"female","reason":"review"}`)
	want(t, "unreviewed document", status, body, 422)
	identityReplacement := `{"kind":"voter_id","media_id":"` + uuid.NewString() + `"}`
	status, body = rg.as(user, "POST", "/application/identity", identityReplacement)
	want(t, "cannot overwrite pending proof", status, body, 409)
	status, body = rg.adminCall("POST", "/documents/"+docID+"/decide", http.PermDocumentsReview, `{"decision":"reject","reason":"Please upload a readable identity proof"}`)
	want(t, "return proof for correction", status, body, 200)
	status, body = rg.as(uuid.New(), "POST", "/application/identity", identityReplacement)
	want(t, "another user has no application to correct", status, body, 404)
	status, body = rg.as(user, "POST", "/application/identity", identityReplacement)
	want(t, "correct identity proof", status, body, 200)
	var corrected model.ProDocument
	data(t, body, &corrected)
	if corrected.ID.String() == docID || corrected.Status != "pending" || corrected.Kind != "voter_id" {
		t.Fatal("replacement did not require a new review")
	}
	status, body = rg.as(user, "POST", "/application/identity", identityReplacement)
	want(t, "identity correction retry", status, body, 200)
	var replayed model.ProDocument
	data(t, body, &replayed)
	if replayed.ID != corrected.ID {
		t.Fatal("duplicate identity replacement")
	}
	var retainedStatus string
	if err := p.QueryRow(ctx, `SELECT status FROM doorstep.pro_documents WHERE id=$1`, docID).Scan(&retainedStatus); err != nil || retainedStatus != "rejected" {
		t.Fatal("original review history lost", err)
	}
	docID = corrected.ID.String()
	status, body = rg.adminCall("POST", "/documents/"+docID+"/decide", http.PermDocumentsReview, `{"decision":"approve","reason":"Identity document reviewed"}`)
	want(t, "approve proof", status, body, 200)
	status, body = rg.as(user, "POST", "/application/identity", identityReplacement)
	want(t, "approved proof cannot be overwritten", status, body, 409)
	status, body = rg.adminCall("GET", "/professionals/"+pro.ID.String(), http.PermProsRead, "")
	want(t, "read approval requirements", status, body, 200)
	var detail model.AdminProfessionalDetail
	data(t, body, &detail)
	if !detail.IdentityDocumentApproved {
		t.Fatal("linked identity approval was not exposed to the reviewer")
	}
	// Document review alone must not record the final human identity decision.
	foundIdentityReview := false
	for _, step := range detail.Readiness.MissingSteps {
		foundIdentityReview = foundIdentityReview || step == "identity_review"
	}
	if !foundIdentityReview {
		t.Fatal("document approval silently completed the identity decision")
	}
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID.String()+"/identity-review", http.PermProsApprove, `{"gender":"female","reason":"Identity and eligibility reviewed"}`)
	want(t, "identity review", status, body, 200)
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID.String()+"/skills/electrician/verify", http.PermProsApprove, `{"verified":true,"reason":"Skills reviewed without optional experience certificate"}`)
	want(t, "skill review", status, body, 200)
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID.String()+"/approve", http.PermProsApprove, `{}`)
	want(t, "bank approval required", status, body, 422)
	var bankDoc string
	var accountSealed, panSealed []byte
	if err := p.QueryRow(ctx, `SELECT a.proof_document_id,a.account_number_sealed,p.pan_sealed FROM doorstep.pro_payout_accounts a JOIN doorstep.professionals p ON p.id=a.pro_id WHERE a.pro_id=$1 AND a.active`, pro.ID).Scan(&bankDoc, &accountSealed, &panSealed); err != nil {
		t.Fatal(err)
	}
	if len(accountSealed) == 0 || len(panSealed) == 0 || bytes.Contains(accountSealed, []byte("000123456789")) || bytes.Contains(panSealed, []byte("ABCPE1234F")) {
		t.Fatal("sensitive details not sealed")
	}
	if _, err := p.Exec(ctx, `UPDATE doorstep.pro_payout_accounts SET status='verified' WHERE pro_id=$1 AND active`, pro.ID); err == nil {
		t.Fatal("unreviewed bank proof bypassed database guard")
	}
	status, body = rg.adminCall("POST", "/documents/"+bankDoc+"/decide", http.PermDocumentsReview, `{"decision":"reject","reason":"Upload a clearer bank proof"}`)
	want(t, "reject bank proof", status, body, 200)
	status, body = rg.as(user, "GET", "/me/bank", "")
	want(t, "masked rejected bank", status, body, 200)
	if bytes.Contains(body, []byte("000123456789")) || !bytes.Contains(body, []byte(`"status":"failed"`)) {
		t.Fatal("incorrect bank response")
	}
	bankPayload, _ := json.Marshal(payload["bank"])
	status, body = rg.as(user, "PUT", "/me/bank", string(bankPayload))
	want(t, "resubmit bank", status, body, 200)
	var bank model.PayoutAccount
	data(t, body, &bank)
	if bank.Status != "pending" || bank.ProofDocumentID == nil || bank.ProofDocumentID.String() == bankDoc {
		t.Fatal("replacement did not reset review")
	}
	staleProof := bank.ProofDocumentID.String()
	status, body = rg.as(user, "PUT", "/me/bank", string(bankPayload))
	want(t, "replace pending bank", status, body, 200)
	data(t, body, &bank)
	status, body = rg.adminCall("POST", "/documents/"+staleProof+"/decide", http.PermDocumentsReview, `{"decision":"approve","reason":"Stale proof must not approve replacement"}`)
	want(t, "stale bank proof refused", status, body, 422)
	var staleStatus string
	if err := p.QueryRow(ctx, `SELECT status FROM doorstep.pro_documents WHERE id=$1`, staleProof).Scan(&staleStatus); err != nil || staleStatus != "pending" {
		t.Fatal("stale proof review was not rolled back", err)
	}
	queue, err := store.New(p).ListDocuments(ctx, "pending")
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range queue {
		if doc.ID.String() == staleProof {
			t.Fatal("superseded proof remains in review queue")
		}
	}
	status, body = rg.adminCall("POST", "/documents/"+bank.ProofDocumentID.String()+"/decide", http.PermDocumentsReview, `{"decision":"approve","reason":"Bank name, account and IFSC checked"}`)
	want(t, "approve bank proof", status, body, 200)
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID.String()+"/approve", http.PermProsApprove, `{"reason":"Documents reviewed"}`)
	want(t, "selfie review required", status, body, 422)
	var selfieDoc string
	if err := p.QueryRow(ctx, `SELECT id FROM doorstep.pro_documents WHERE pro_id=$1 AND kind='selfie' AND status='pending'`, pro.ID).Scan(&selfieDoc); err != nil {
		t.Fatal(err)
	}
	status, body = rg.adminCall("POST", "/documents/"+selfieDoc+"/decide", http.PermDocumentsReview, `{"decision":"approve","reason":"Selfie compared with identity evidence"}`)
	want(t, "approve selfie", status, body, 200)
	status, body = rg.adminCall("POST", "/professionals/"+pro.ID.String()+"/approve", http.PermProsApprove, `{"reason":"Documents reviewed"}`)
	want(t, "approve account", status, body, 200)
	status, body = rg.as(user, "GET", "/readiness", "")
	want(t, "readiness", status, body, 200)
	var readiness model.ProReadiness
	data(t, body, &readiness)
	if readiness.Status != "approved" || readiness.CanGoOnDuty {
		t.Fatal("identity approval bypassed job readiness")
	}
	status, body = rg.as(user, "POST", "/duty/on", `{}`)
	if status < 400 {
		t.Fatalf("accepted duty before work setup: %s", body)
	}
}
