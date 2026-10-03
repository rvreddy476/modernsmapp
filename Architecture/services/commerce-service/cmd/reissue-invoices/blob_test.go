package main

import (
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/store/blob"
)

// The tool builds the invoice store through the same contract as the
// server. Under a development ENV with nothing else set it is the dev
// MinIO; under anything else the static-key MinIO is refused even if the
// target guard were somehow bypassed.
func TestBlobConfig(t *testing.T) {
	getenv := func(map[string]string) func(string) string {
		return func(string) string { return "" }
	}
	cfg, err := blobConfig(getenv(nil), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backend != blob.BackendMinIO || cfg.Bucket != blob.DefaultBucket || cfg.Endpoint != "minio:9000" || cfg.Production {
		t.Fatalf("dev config: %+v", cfg)
	}
	for _, env := range []string{"", "prod", "production", "staging", "Stage", "qa"} {
		if _, err := blobConfig(getenv(nil), env); !errors.Is(err, blob.ErrProductionRefused) {
			t.Fatalf("ENV=%q: err = %v, want ErrProductionRefused", env, err)
		}
	}
}
