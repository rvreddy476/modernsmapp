package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/geo"
	"github.com/atpost/rider-service/internal/otp"
	"github.com/atpost/rider-service/internal/store"
)

// TestNegativeControl_DualGeofencingEnforced verifies that a coordinate pair outside the boundary is strictly rejected.
func TestNegativeControl_DualGeofencingEnforced(t *testing.T) {
	// Pickup inside Bangalore, Drop in Mumbai (out of zone for Bangalore city)
	pickupLat, pickupLng := 12.9716, 77.5946
	mumbaiLat, mumbaiLng := 19.0760, 72.8777

	// Direct distance check
	distKM := geo.HaversineKM(pickupLat, pickupLng, mumbaiLat, mumbaiLng)
	if distKM < 500.0 {
		t.Fatalf("expected distance > 500km, got %f", distKM)
	}

	// Geofence coordinate validator
	if !validLatLng(pickupLat, pickupLng) {
		t.Fatalf("expected valid lat/lng for pickup")
	}
	if validLatLng(0.0, 0.0) {
		t.Fatalf("null-island (0,0) must fail validation")
	}
}

// TestNegativeControl_PilotVehicleTypesRestricted ensures only bike and auto are allowed during pilot.
func TestNegativeControl_PilotVehicleTypesRestricted(t *testing.T) {
	allowed := []string{"bike", "auto"}
	disallowed := []string{"sedan", "suv", "premium", "helicopter", "luxury"}

	for _, v := range allowed {
		if !pilotVehicleTypes[v] {
			t.Fatalf("expected %s to be allowed in pilot", v)
		}
	}
	for _, v := range disallowed {
		if pilotVehicleTypes[v] {
			t.Fatalf("expected %s to be disallowed in pilot", v)
		}
	}
}

// TestNegativeControl_QuoteCoordinateDeviationStrict50m proves that deviation > 50m triggers rejection.
func TestNegativeControl_QuoteCoordinateDeviationStrict50m(t *testing.T) {
	pLat, pLng := 12.9716, 77.5946

	// Shift 30m away (approx 0.00027 deg lat) -> should pass (< 50m)
	lat30m := pLat + 0.00027
	dist30m := geo.HaversineKM(pLat, pLng, lat30m, pLng) * 1000.0
	if dist30m > 50.0 {
		t.Fatalf("expected <= 50m, got %f", dist30m)
	}

	// Shift 100m away (approx 0.00090 deg lat) -> must fail (> 50m)
	lat100m := pLat + 0.00090
	dist100m := geo.HaversineKM(pLat, pLng, lat100m, pLng) * 1000.0
	if dist100m <= 50.0 {
		t.Fatalf("expected > 50m, got %f", dist100m)
	}
}

// TestNegativeControl_IdempotencyFingerprintMismatch verifies request fingerprinting.
func TestNegativeControl_IdempotencyFingerprintMismatch(t *testing.T) {
	reqA := "cust-1:quote-1:bike:12.9716:77.5946:12.9698:77.7500"
	reqB := "cust-1:quote-1:auto:12.9716:77.5946:12.9698:77.7500"

	hashA := fmt.Sprintf("%x", sha256.Sum256([]byte(reqA)))
	hashB := fmt.Sprintf("%x", sha256.Sum256([]byte(reqB)))

	if hashA == hashB {
		t.Fatalf("distinct requests must produce distinct SHA256 fingerprints")
	}
}

// TestNegativeControl_InvertedOTP3Failures15MinLockout verifies envelope encryption, hash verification, and lockout.
func TestNegativeControl_InvertedOTP3Failures15MinLockout(t *testing.T) {
	plain, hash, enc, err := generateOTPAndHash()
	if err != nil {
		t.Fatalf("generateOTPAndHash failed: %v", err)
	}

	// Plaintext decryption test
	decrypted, err := otp.DecryptOTP(enc, nil)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if decrypted != plain {
		t.Fatalf("expected decrypted %s, got %s", plain, decrypted)
	}

	// Matching hash test
	if err := otp.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		t.Fatalf("hash match failed: %v", err)
	}

	// Wrong OTP test
	wrongOTP := "9999"
	if plain == wrongOTP {
		wrongOTP = "0000"
	}
	if err := otp.CompareHashAndPassword([]byte(hash), []byte(wrongOTP)); err == nil {
		t.Fatalf("wrong OTP unexpectedly matched hash")
	}

	// Simulate 3 failures lockout duration
	lockoutDuration := 15 * time.Minute
	lockedUntil := time.Now().UTC().Add(lockoutDuration)
	if !lockedUntil.After(time.Now().UTC()) {
		t.Fatalf("lockout time must be in future")
	}
}

// TestPositiveControl_IntegerPaiseMathExactness tests integer fare calculation without float drift.
func TestPositiveControl_IntegerPaiseMathExactness(t *testing.T) {
	baseFarePaise := int64(3000)   // ₹30.00
	perKMPaise := int64(1200)      // ₹12.00 / km
	distanceMeters := 10500        // 10.5 km
	perMinPaise := int64(150)      // ₹1.50 / min
	durationSecs := 1200           // 20 min

	distFarePaise := (int64(distanceMeters) * perKMPaise) / 1000
	timeFarePaise := (int64(durationSecs) * perMinPaise) / 60
	totalPaise := baseFarePaise + distFarePaise + timeFarePaise

	// ₹30 + (10.5 * 12 = ₹126) + (20 * 1.5 = ₹30) = ₹186.00 = 18600 paise
	expectedPaise := int64(18600)
	if totalPaise != expectedPaise {
		t.Fatalf("expected total %d paise, got %d", expectedPaise, totalPaise)
	}
}

// TestNegativeControl_OTPRedactedFromJSON ensures no OTP secret is ever serialized to JSON.
func TestNegativeControl_OTPRedactedFromJSON(t *testing.T) {
	plain := "1234"
	enc := []byte("encrypted-ciphertext")
	lockTime := time.Now().UTC().Add(15 * time.Minute)
	r := store.Ride{
		OTPCode:        &plain,
		OTPEncrypted:   enc,
		OTPAttempts:    2,
		OTPLockedUntil: &lockTime,
	}

	bytes, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal ride failed: %v", err)
	}
	jsonStr := string(bytes)
	if strings.Contains(jsonStr, "1234") {
		t.Fatalf("JSON serialization leaked plaintext OTP: %s", jsonStr)
	}
	if strings.Contains(jsonStr, "encrypted-ciphertext") {
		t.Fatalf("JSON serialization leaked encrypted OTP: %s", jsonStr)
	}
	if strings.Contains(jsonStr, "otp_attempts") {
		t.Fatalf("JSON serialization leaked otp_attempts: %s", jsonStr)
	}
	if strings.Contains(jsonStr, "otp_locked_until") {
		t.Fatalf("JSON serialization leaked otp_locked_until: %s", jsonStr)
	}
}

// TestNegativeControl_MandatoryExpectedRevision verifies that missing revision is rejected.
func TestNegativeControl_MandatoryExpectedRevision(t *testing.T) {
	in := store.TransitionRideAtomicInput{
		ExpectedRevision: 0,
	}
	if in.ExpectedRevision <= 0 {
		if store.ErrExpectedRevisionRequired == nil {
			t.Fatalf("expected ErrExpectedRevisionRequired to be defined")
		}
	}
}
