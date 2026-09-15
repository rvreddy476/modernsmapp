package http

import (
	"bytes"
	"context"
	"testing"

	"github.com/atpost/dating-service/internal/datingpii"
)

// testPII is the fixed lane D9 test key ring (the same bytes as the store and
// service packages').
func testPII(t *testing.T) *datingpii.Crypto {
	t.Helper()
	c, err := datingpii.New(context.Background(),
		[]datingpii.VersionedKey{{Version: 1, Key: bytes.Repeat([]byte{9}, 32)}}, []byte("dating-it-test-lookup-salt-0001"))
	if err != nil {
		t.Fatalf("test pii: %v", err)
	}
	return c
}
