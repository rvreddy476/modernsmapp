package hiddenchat

import (
	"bytes"
	"strings"
	"testing"
)

func TestHashNeverContainsTheSecretAndIsSalted(t *testing.T) {
	const secret = "Mango42"
	a, err := Hash(secret)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Hash(secret)
	if !strings.HasPrefix(a, HashPrefix) {
		t.Fatalf("not an Argon2id PHC string: %q", a)
	}
	if strings.Contains(a, secret) || strings.Contains(strings.ToLower(a), strings.ToLower(secret)) {
		t.Fatal("hash contains the clear secret")
	}
	if a == b {
		t.Fatal("two hashes of one secret are equal: no per-secret salt")
	}
	if !Verify(secret, a) || !Verify(secret, b) {
		t.Fatal("hash does not verify")
	}
	if Verify("Mango43", a) || Verify("", a) || Verify("mango42", a) {
		t.Fatal("a wrong secret verified")
	}
}

func TestVerifyRefusesMalformedOrTamperedHashes(t *testing.T) {
	good, _ := Hash("Mango42")
	for _, bad := range []string{
		"", "Mango42", "$argon2i$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA",
		strings.Replace(good, "m=19456", "m=999999999", 1), // memory bomb
		strings.Replace(good, "v=19", "v=16", 1),
		good[:len(good)-4],
	} {
		if Verify("Mango42", bad) {
			t.Fatalf("verified against malformed hash %q", bad)
		}
	}
}

func TestVerifyOrDummyNeverMatchesWithoutAHash(t *testing.T) {
	if VerifyOrDummy("hidden-chats-timing-equaliser", "") {
		t.Fatal("an empty stored hash must never match, not even the dummy's own secret")
	}
}

func TestSecretShapes(t *testing.T) {
	for _, ok := range []string{"abcd", "A1b2C3d4E5f6", "ঘর১২", "1234"} {
		if ValidateSecretKey(ok) != nil {
			t.Fatalf("valid key refused: %q", ok)
		}
	}
	for _, bad := range []string{"abc", "abcdefghijklm", "ab cd", "ab-cd", "", "abcd!"} {
		if ValidateSecretKey(bad) == nil {
			t.Fatalf("invalid key accepted: %q", bad)
		}
	}
	for _, ok := range []string{"secret", "a pass phrase with spaces!", strings.Repeat("x", 64)} {
		if ValidateChatPassword(ok) != nil {
			t.Fatalf("valid password refused: %q", ok)
		}
	}
	for _, bad := range []string{"12345", strings.Repeat("x", 65), "abc\x00def"} {
		if ValidateChatPassword(bad) == nil {
			t.Fatalf("invalid password accepted: %q", bad)
		}
	}
}

func TestTokensAreRandomAndStoredHashed(t *testing.T) {
	tok, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tok2, _, _ := NewToken()
	if tok == tok2 {
		t.Fatal("tokens repeat")
	}
	if !WellFormedToken(tok) || WellFormedToken(tok+"x") || WellFormedToken("short") {
		t.Fatal("token shape check")
	}
	if bytes.Contains(hash, []byte(tok)) || !bytes.Equal(hash, TokenHash(tok)) || len(hash) != 32 {
		t.Fatal("stored form must be the 32-byte SHA-256, never the token")
	}
}
