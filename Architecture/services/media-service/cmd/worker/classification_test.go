package main

import (
	"context"
	"errors"
	"testing"
)

func TestPermanentUnlessCancelled(t *testing.T) {
	if err := permanentUnlessCancelled(context.Background(), errors.New("invalid media")); !isPermanentTranscodeFailure(err) {
		t.Fatal("deterministic invalid media was not terminal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := permanentUnlessCancelled(ctx, context.Canceled); isPermanentTranscodeFailure(err) {
		t.Fatal("worker cancellation was incorrectly terminal")
	}
}
