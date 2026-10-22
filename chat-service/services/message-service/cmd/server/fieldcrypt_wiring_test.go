package main

import (
	"os"
	"strings"
	"testing"
)

// Field encryption at rest (22 Oct 2026): the stores seal nothing unless main
// hands them the cipher fieldkeys.Load built — and Load is what refuses a
// production boot without a key ring. A refactor that drops either line would
// silently store plaintext, so the wiring is pinned here.
func TestMainWiresTheFieldCipherIntoBothStores(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"fieldkeys.Load(ctx, fieldkeys.Deps{",
		"convStore.SetFieldCipher(fieldKeys.Cipher)",
		"msgStore.SetFieldCipher(fieldKeys.Cipher)",
		"go svc.StartOutboxRetentionWorker(ctx,",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("cmd/server/main.go no longer contains %q", want)
		}
	}
	// The cipher must be installed before anything serves or consumes.
	set := strings.Index(src, "msgStore.SetFieldCipher(fieldKeys.Cipher)")
	for _, later := range []string{"identityConsumer.Start", "svc.StartOutboxRelay", "r.Run("} {
		if i := strings.Index(src, later); i >= 0 && set > i {
			t.Errorf("the field cipher is installed after %s starts", later)
		}
	}
}
