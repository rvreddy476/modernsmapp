package service

import (
	"context"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPartnerPhoneLocalOnly(t *testing.T) {
	s, _, _ := newTestService(t, "shadow")
	t.Setenv("PARTNER_DEV_PHONE_OTP_ENABLED", "true")
	for _, tc := range []struct {
		env        string
		prod, want bool
	}{{"local", false, true}, {"local", true, false}, {"production", false, false}, {"", false, false}, {"staging", false, false}} {
		s.cfg.RuntimeEnvironment = tc.env
		s.cfg.Production = tc.prod
		if s.PartnerPhoneAvailable() != tc.want {
			t.Fatalf("unsafe transport env=%s production=%v", tc.env, tc.prod)
		}
	}
	s.cfg.RuntimeEnvironment = "local"
	s.cfg.Production = false
	t.Setenv("PARTNER_DEV_PHONE_OTP_ENABLED", "false")
	if s.PartnerPhoneAvailable() {
		t.Fatal("flag ignored")
	}
}
func TestPartnerPhoneValidation(t *testing.T) {
	good := PartnerPhoneInput{Mode: "register", Phone: "9876543210", FirstName: "Test", LastName: "Partner", Gender: "others", AcceptedTerms: true, TermsVersion: CurrentTermsVersion}
	if v, err := normalizePartnerPhone(good); err != nil || v.Phone != "+919876543210" {
		t.Fatal(v, err)
	}
	for _, mutate := range []func(*PartnerPhoneInput){func(v *PartnerPhoneInput) { v.Phone = "123" }, func(v *PartnerPhoneInput) { v.Gender = "" }, func(v *PartnerPhoneInput) { v.AcceptedTerms = false }, func(v *PartnerPhoneInput) { v.LastName = "" }, func(v *PartnerPhoneInput) { v.FirstName = "<script>" }} {
		v := good
		mutate(&v)
		if _, err := normalizePartnerPhone(v); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
func TestPartnerPhoneAttemptLimitReplayAndExpiry(t *testing.T) {
	s, _, mr := newTestService(t, "shadow")
	s.cfg.RuntimeEnvironment = "local"
	t.Setenv("PARTNER_DEV_PHONE_OTP_ENABLED", "true")
	hash, _ := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.MinCost)
	payload, _ := json.Marshal(partnerPhonePending{Input: PartnerPhoneInput{Phone: "+919876543210"}, Hash: string(hash)})
	id := strings.Repeat("A", 43)
	key := "partner_phone:challenge:" + id
	ctx := context.Background()
	seed := func() {
		_ = s.rdb.Del(ctx, key, key+":attempts").Err()
		_ = s.rdb.Set(ctx, key, payload, 5*time.Minute).Err()
	}
	seed()
	for i := 0; i < 5; i++ {
		if _, err := s.consumePartnerPhone(ctx, id, "000000"); err == nil {
			t.Fatal("wrong OTP accepted")
		}
	}
	if _, err := s.consumePartnerPhone(ctx, id, "123456"); err == nil {
		t.Fatal("attempt cap bypass")
	}
	seed()
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.consumePartnerPhone(ctx, id, "123456"); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("single-use successes=%d", successes.Load())
	}
	seed()
	mr.FastForward(6 * time.Minute)
	if _, err := s.consumePartnerPhone(ctx, id, "123456"); err == nil {
		t.Fatal("expired OTP accepted")
	}
}
func TestPartnerPhoneQuota(t *testing.T) {
	s, _, mr := newTestService(t, "shadow")
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.partnerPhoneQuota(ctx, "test", 5, 900); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := AsThrottled(s.partnerPhoneQuota(ctx, "test", 5, 900)); !ok {
		t.Fatal("quota bypass")
	}
	mr.FastForward(901 * time.Second)
	if err := s.partnerPhoneQuota(ctx, "test", 5, 900); err != nil {
		t.Fatal(err)
	}
}
