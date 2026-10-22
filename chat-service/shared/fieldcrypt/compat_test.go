package fieldcrypt

import (
	"context"
	"encoding/hex"
	"testing"
)

// piiVector is a blob sealed by Architecture/shared/pii itself (Sealer.Seal,
// scope "chat.vector", version 3, vectorKey), generated on 2026-10-22 with a
// throwaway program that imported that module. fieldcrypt must open it
// byte for byte: the two packages share one wire format.
const piiVector = "000000039c461e0fedba19744dae3678cc3311afc2b66295d0a71439d8571b2eeff0012507913113545782440ca5b837ca3e0c0c9b02b6433d11ed1de337af9aa87a78f74a"

const piiVectorPlaintext = "test vector: నమస్కారం"

func vectorKey() []byte {
	k := make([]byte, DataKeySize)
	for i := range k {
		k[i] = byte(i*13 + 7)
	}
	return k
}

func TestOpensBlobSealedByPII(t *testing.T) {
	blob, err := hex.DecodeString(piiVector)
	if err != nil {
		t.Fatal(err)
	}
	s := mustSealer(t, mustRing(t, StaticKey{"chat.vector", 3, vectorKey()}), "chat.vector")
	got, err := s.Open(context.Background(), blob)
	if err != nil || got != piiVectorPlaintext {
		t.Fatalf("Open(pii blob) = %q, %v", got, err)
	}
	// An unbound Open is exactly the pii additional data; any binding, even
	// one byte, is a different row and must refuse the pii blob.
	if _, err := s.OpenBound(context.Background(), blob, Bind("x")); err == nil {
		t.Fatal("a pii (unbound) blob opened under a row binding")
	}
}

func TestUnboundSealOpensUnderPIILayout(t *testing.T) {
	// The reverse direction, by hand: Seal output with an empty binding is
	// AES-GCM with additional data == scope, the layout pii.Open expects.
	s := mustSealer(t, mustRing(t, StaticKey{"chat.vector", 3, vectorKey()}), "chat.vector")
	for _, binding := range [][]byte{nil, {}} {
		blob, _, err := s.SealBound(context.Background(), piiVectorPlaintext, binding)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Open(context.Background(), blob)
		if err != nil || got != piiVectorPlaintext {
			t.Fatalf("Open(SealBound(empty binding)) = %q, %v", got, err)
		}
	}
}
