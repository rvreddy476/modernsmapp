package itest

import (
	"context"
	"encoding/json"
	"github.com/atpost/doorstep-service/database"
	"strings"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

func TestBankHistoryIsMaskedAndOwnerScoped(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	s := store.New(p)
	id, other := uuid.New(), uuid.New()
	for _, pro := range []uuid.UUID{id, other} {
		if _, err := p.Exec(ctx, `INSERT INTO doorstep.professionals(id,user_id,display_name,status) VALUES($1,$2,'History test','draft')`, pro, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	submission := store.BankSubmission{Holder: "Test Partner", Last4: "6789", IFSC: "SBIN0001234", BankName: "Test Bank", ProofKind: "cancelled_cheque", ProofMediaID: uuid.New(), Sealed: []byte("sealed-private-value"), KeyVersion: 1}
	first, err := s.SetPayoutAccount(ctx, id, submission)
	if err != nil {
		t.Fatal(err)
	}
	actor := store.Actor{UserID: uuid.New(), Permission: "doorstep:documents.review"}
	if _, err := s.DecideDocument(ctx, actor, *first.ProofDocumentID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	submission.Last4 = "4321"
	submission.ProofMediaID = uuid.New()
	if _, err := s.SetPayoutAccount(ctx, id, submission); err != nil {
		t.Fatal(err)
	}
	account, err := s.PayoutAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != "verified" || len(account.History) != 2 {
		t.Fatalf("missing bank history: %+v", account)
	}
	latest, previous := account.History[0], account.History[1]
	if latest.Active || latest.Superseded || latest.AccountLast4 != "4321" || latest.Status != "pending" {
		t.Fatal("latest submission not preserved")
	}
	if !previous.Active || previous.Status != "verified" || previous.ReviewedAt == nil || previous.AccountLast4 != "6789" {
		t.Fatal("previous approved record missing or reactivated")
	}
	body, _ := json.Marshal(account)
	if strings.Contains(string(body), "account_number") || strings.Contains(string(body), "sealed-private-value") {
		t.Fatal("bank secret leaked")
	}
	if another, err := s.PayoutAccount(ctx, other); err != nil || another != nil {
		t.Fatal("another partner received bank history", err)
	}
	// Reading history must not change readiness or activate an old account.
	again, err := s.PayoutAccount(ctx, id)
	if err != nil || again.Status != "verified" || !again.History[1].Active {
		t.Fatal("history read changed bank state", err)
	}
	// Rejecting a replacement preserves the approved payout account and readiness.
	if _, err := s.DecideDocument(ctx, actor, *latest.ProofDocumentID, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	facts, err := s.ProFacts(ctx, id, time.Now())
	if err != nil || !facts.HasPayoutAccount {
		t.Fatal("rejection disabled approved bank", err)
	}
	stale, err := s.SetPayoutAccount(ctx, id, submission)
	if err != nil {
		t.Fatal(err)
	}
	submission.Last4 = "9876"
	submission.ProofMediaID = uuid.New()
	replacement, err := s.SetPayoutAccount(ctx, id, submission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideDocument(ctx, actor, *stale.ProofDocumentID, true, nil, nil); err == nil {
		t.Fatal("approved superseded bank proof")
	}
	queue, err := s.ListDocuments(ctx, "pending")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range queue {
		if d.ID == *stale.ProofDocumentID {
			t.Fatal("superseded proof in queue")
		}
		if d.ID == *replacement.ProofDocumentID {
			found = true
		}
	}
	if !found {
		t.Fatal("inactive replacement missing from admin queue")
	}
	if _, err := s.DecideDocument(ctx, actor, *replacement.ProofDocumentID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	account, err = s.PayoutAccount(ctx, id)
	if err != nil || account.Status != "verified" || account.AccountLast4 != "9876" {
		t.Fatal("approval did not switch the account", err)
	}
	var active int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.pro_payout_accounts WHERE pro_id=$1 AND active`, id).Scan(&active); err != nil || active != 1 {
		t.Fatal("non-atomic bank switch", err)
	}
}

func TestBankReplacementRepairsLegacyDeactivation(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	s := store.New(p)
	id := uuid.New()
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.professionals(id,user_id,display_name,status) VALUES($1,$2,'Legacy bank test','draft')`, id, uuid.New()); err != nil {
		t.Fatal(err)
	}
	a := store.Actor{UserID: uuid.New(), Permission: "doorstep:documents.review"}
	in := store.BankSubmission{Holder: "Test", Last4: "1111", IFSC: "SBIN0001234", BankName: "Bank", ProofKind: "passbook", ProofMediaID: uuid.New(), Sealed: []byte("sealed"), KeyVersion: 1}
	old, err := s.SetPayoutAccount(ctx, id, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecideDocument(ctx, a, *old.ProofDocumentID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeProStatus(ctx, a, id, store.StatusChange{Action: "professional.approve", From: []string{"draft"}, To: "approved", Today: time.Now()}); err != nil {
		t.Fatal(err)
	}
	in.ProofMediaID = uuid.New()
	in.Last4 = "2222"
	replacement, err := s.SetPayoutAccount(ctx, id, in)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the old released implementation's specific corrupt state.
	if _, err = p.Exec(ctx, `UPDATE doorstep.pro_payout_accounts SET active=FALSE,created_at=now()-interval '1 day' WHERE proof_document_id=$1`, old.ProofDocumentID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `UPDATE doorstep.pro_payout_accounts SET active=TRUE WHERE proof_document_id=$1`, replacement.ProofDocumentID); err != nil {
		t.Fatal(err)
	}
	migration, err := database.Migrations.ReadFile("migrations/014_bank_replacement_review.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = p.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	account, err := s.PayoutAccount(ctx, id)
	if err != nil || account.Status != "verified" || account.AccountLast4 != "1111" {
		t.Fatal("legacy account not restored", err)
	}
	var pending, repairs int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM doorstep.pro_payout_accounts WHERE pro_id=$1 AND NOT active AND NOT superseded AND status='pending'`, id).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("repair altered replacement approval", err)
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM doorstep.bank_replacement_repairs r JOIN doorstep.pro_payout_accounts a ON a.id=r.replacement_id WHERE a.pro_id=$1`, id).Scan(&repairs); err != nil || repairs != 1 {
		t.Fatal("repair journal missing", err)
	}
}
