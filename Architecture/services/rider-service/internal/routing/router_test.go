package routing

import (
	"context"
	"os"
	"testing"
)

func TestDeterministicCalculator(t *testing.T) {
	calc := NewDeterministicCalculator(1.25, 22.0)

	// Bengaluru MG Road to Koramangala (approx 5.5 km straight)
	pLat, pLng := 12.9716, 77.5946
	dLat, dLng := 12.9352, 77.6245

	res, err := calc.CalculateRoute(context.Background(), pLat, pLng, dLat, dLng)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.DistanceMeters <= 0 {
		t.Errorf("expected positive distance, got %d", res.DistanceMeters)
	}
	if res.DurationSeconds <= 0 {
		t.Errorf("expected positive duration, got %d", res.DurationSeconds)
	}
	if res.ProviderVersion != "deterministic-v1" {
		t.Errorf("expected deterministic-v1 version, got %s", res.ProviderVersion)
	}
}

func TestRouting_InvalidCoordinates(t *testing.T) {
	calc := NewDeterministicCalculator(1.25, 22.0)
	_, err := calc.CalculateRoute(context.Background(), 999.0, 77.5946, 12.9352, 77.6245)
	if err != ErrInvalidCoordinates {
		t.Errorf("expected ErrInvalidCoordinates, got %v", err)
	}
}

func TestAWSLocationCalculator_FailClosedInProduction(t *testing.T) {
	orig := os.Getenv("ENV")
	defer os.Setenv("ENV", orig)

	os.Setenv("ENV", "prod")
	awsCalc, err := NewAWSLocationCalculator(context.Background(), "", "ap-south-1")
	if err != ErrRoutingUnconfigured && (awsCalc != nil) {
		_, err = awsCalc.CalculateRoute(context.Background(), 12.9716, 77.5946, 12.9352, 77.6245)
	}
	if err != ErrRoutingUnconfigured {
		t.Errorf("expected ErrRoutingUnconfigured in production when calculator name is empty, got %v", err)
	}
}
