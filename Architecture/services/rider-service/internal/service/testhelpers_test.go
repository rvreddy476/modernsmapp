package service

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/atpost/rider-service/internal/riderpii"
)

// testOTPCrypto is a rider.ride_otp sealer on a fixed test key, so unit and
// integration tests can mint and open ride OTPs without RIDER_PII_KEYS.
func testOTPCrypto(t *testing.T) *riderpii.Crypto {
	t.Helper()
	key := base64.StdEncoding.EncodeToString([]byte("rider-otp-test-key-32-bytes-long"))
	keys, err := riderpii.ParseKeys("v1:" + key)
	if err != nil {
		t.Fatalf("parse test keys: %v", err)
	}
	c, err := riderpii.New(context.Background(), keys)
	if err != nil {
		t.Fatalf("test otp crypto: %v", err)
	}
	return c
}
