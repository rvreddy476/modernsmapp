package itest

import (
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAlternatePhoneLimitsOwnershipAndReplay(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	st := store.New(p)
	uid, pro := uuid.New(), uuid.New()
	_, err := p.Exec(ctx, `INSERT INTO doorstep.professionals(id,user_id,display_name,status) VALUES($1,$2,'Test alternate','pending_verification')`, pro, uid)
	if err != nil {
		t.Fatal(err)
	}
	doc := uuid.New()
	_, err = p.Exec(ctx, `INSERT INTO doorstep.pro_documents(id,pro_id,kind,media_id,status) VALUES($1,$2,'passport',$3,'pending')`, doc, pro, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(ctx, `INSERT INTO doorstep.pro_applications(pro_id,first_name,last_name,declared_gender,state_code,state_name,town,primary_phone_last4,alternate_phone_last4,alternate_phone_sealed,identity_document_id)
 VALUES($1,'Test','Partner','others','36','Telangana','Warangal','3210','3211',$2,$3)`, pro, []byte("sealed-test-only"), doc)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.MinCost)
	request := func() {
		t.Helper()
		if _, e := st.RequestAlternatePhone(ctx, uid, string(hash)); e != nil {
			t.Fatal(e)
		}
	}
	reset := func() {
		t.Helper()
		_, e := p.Exec(ctx, `UPDATE doorstep.pro_applications SET alternate_otp_sent_at=NOW()-INTERVAL '61 seconds' WHERE pro_id=$1`, pro)
		if e != nil {
			t.Fatal(e)
		}
	}
	request()
	if _, e := st.RequestAlternatePhone(ctx, uid, string(hash)); !errors.Is(e, store.ErrConflict) {
		t.Fatal("resend cooldown bypass", e)
	}
	if ok, e := st.VerifyAlternatePhone(ctx, uuid.New(), "123456"); ok || e == nil {
		t.Fatal("another user verified contact")
	}
	for i := 0; i < 5; i++ {
		if ok, e := st.VerifyAlternatePhone(ctx, uid, "000000"); ok || e != nil {
			t.Fatal("wrong code accepted", e)
		}
	}
	if ok, _ := st.VerifyAlternatePhone(ctx, uid, "123456"); ok {
		t.Fatal("attempt cap bypassed")
	}
	var cleared bool
	if e := p.QueryRow(ctx, `SELECT alternate_otp_hash IS NULL FROM doorstep.pro_applications WHERE pro_id=$1`, pro).Scan(&cleared); e != nil || !cleared {
		t.Fatal("exhausted code hash retained", e)
	}
	reset()
	request()
	_, err = p.Exec(ctx, `UPDATE doorstep.pro_applications SET alternate_otp_expires_at=NOW()-INTERVAL '1 second' WHERE pro_id=$1`, pro)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.VerifyAlternatePhone(ctx, uid, "123456"); ok {
		t.Fatal("expired code accepted")
	}
	reset()
	request()
	reset()
	request()
	reset()
	request()
	reset()
	if _, e := st.RequestAlternatePhone(ctx, uid, string(hash)); !errors.Is(e, store.ErrConflict) {
		t.Fatal("hourly send quota bypassed", e)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, e := st.VerifyAlternatePhone(ctx, uid, "123456")
			if e != nil {
				t.Error(e)
			}
			if ok {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("expected single use, got %d", successes.Load())
	}
	if ok, _ := st.VerifyAlternatePhone(ctx, uid, "123456"); ok {
		t.Fatal("replayed code accepted")
	}
	var verified bool
	if e := p.QueryRow(ctx, `SELECT alternate_phone_verified_at IS NOT NULL FROM doorstep.pro_applications WHERE pro_id=$1`, pro).Scan(&verified); e != nil || !verified {
		t.Fatal("verified contact not persisted", e)
	}
}
