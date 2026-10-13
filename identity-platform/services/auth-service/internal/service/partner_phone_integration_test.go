//go:build integration

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIntegrationPartnerPhoneRegistration(t *testing.T) {
	pool := rolesPool(t)
	ctx := context.Background()
	// Use the real cross-schema DDL used by registration, never a test subset.
	for _, name := range []string{"../../../user-service/database/setup.sql", "../../../profile-service/database/setup.sql"} {
		ddl, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.BootstrapSchema(ctx, pool, string(ddl)); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := itService(t, pool, &config.Config{RuntimeEnvironment: "local", JWTSecret: "partner-integration-only", RefreshTokenTTL: time.Hour, LoginAnomalyEnforce: "shadow"})
	s.producer = &fakeProducer{}
	t.Setenv("PARTNER_DEV_PHONE_OTP_ENABLED", "true")
	var log bytes.Buffer
	s.log = slog.New(slog.NewJSONHandler(&log, nil))
	// Unique, synthetic test phone within the accepted format.
	phone := fmt.Sprintf("+919%09d", time.Now().UnixNano()%1000000000)
	in := PartnerPhoneInput{Mode: "register", Phone: phone, FirstName: "Test", LastName: "Partner", Gender: "others", AcceptedTerms: true, TermsVersion: CurrentTermsVersion}
	challenge, err := s.RequestPartnerPhone(ctx, in, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var frame map[string]any
	if err = json.Unmarshal(bytes.TrimSpace(log.Bytes()), &frame); err != nil {
		t.Fatal(err)
	}
	code, _ := frame["code"].(string)
	if code == "" || !strings.Contains(frame["msg"].(string), "DEVELOPMENT ONLY") {
		t.Fatal("development transport not labelled")
	}
	out, err := s.VerifyPartnerPhone(ctx, challenge.ChallengeID, code, uuid.NewString(), "web", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if out.User == nil || !out.User.PhoneVerified || out.Tokens.AccessToken == "" {
		t.Fatal("verified registration did not issue a session")
	}
	var ageConsent bool
	if err = pool.QueryRow(ctx, `SELECT consent_age FROM auth.users WHERE user_id=$1`, out.User.ID).Scan(&ageConsent); err != nil || ageConsent {
		t.Fatal("manufactured age declaration", err)
	}
	var first, last, gender string
	if err = pool.QueryRow(ctx, `SELECT first_name,last_name,gender FROM profile.profiles WHERE user_id=$1`, out.User.ID).Scan(&first, &last, &gender); err != nil || first != "Test" || last != "Partner" || gender != "others" {
		t.Fatalf("profile not saved: %s %s %s %v", first, last, gender, err)
	}
	if _, err = s.VerifyPartnerPhone(ctx, challenge.ChallengeID, code, "same", "web", "127.0.0.1", "test"); err == nil {
		t.Fatal("replay accepted")
	}
	// A valid mobile OTP must not bypass an existing account's MFA or suspension.
	for _, status := range []string{"active", "suspended"} {
		if _, err = pool.Exec(ctx, `UPDATE auth.users SET two_factor_enabled=true,account_status=$2 WHERE user_id=$1`, out.User.ID, status); err != nil {
			t.Fatal(err)
		}
		id := strings.ReplaceAll(uuid.NewString(), "-", "") + "abcdefghijk"
		hash, _ := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.MinCost)
		payload, _ := json.Marshal(partnerPhonePending{Input: PartnerPhoneInput{Mode: "login", Phone: phone}, Hash: string(hash)})
		_ = s.rdb.Set(ctx, "partner_phone:challenge:"+id, payload, 5*time.Minute).Err()
		result, e := s.VerifyPartnerPhone(ctx, id, "123456", "same", "web", "127.0.0.1", "test")
		if status == "active" && (e != nil || result == nil || !result.Requires2FA || result.Tokens.AccessToken != "") {
			t.Fatal("mobile OTP bypassed MFA", e)
		}
		if status == "suspended" && e == nil {
			t.Fatal("suspended account signed in")
		}
	}
}
