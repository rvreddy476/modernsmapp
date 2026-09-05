package kmsclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// B1 — what this adapter SENDS and what it refuses to accept back.
//
// The pii package already proves the key model (rotation, historical lookup,
// scope binding) against its own fake. What only this layer can prove is the
// request shape: that the key spec is AES-256, that the encryption context
// reaches KMS unmodified, and that a malformed response is refused rather than
// used to seal customer data.
//
// The stub is strict on purpose. A permissive stub here would be the exact
// mistake review 3 caught in the key ring: a fake that is more forgiving than
// the real dependency lets a defect through and calls it a proof.

type stubKMS struct {
	genIn  *kms.GenerateDataKeyInput
	decIn  *kms.DecryptInput
	genOut *kms.GenerateDataKeyOutput
	decOut *kms.DecryptOutput
	genErr error
	decErr error
	delay  time.Duration
}

func (s *stubKMS) GenerateDataKey(ctx context.Context, in *kms.GenerateDataKeyInput, _ ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error) {
	s.genIn = in
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.genOut, s.genErr
}

func (s *stubKMS) Decrypt(ctx context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	s.decIn = in
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.decOut, s.decErr
}

func key32() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func okStub() *stubKMS {
	return &stubKMS{
		genOut: &kms.GenerateDataKeyOutput{
			Plaintext:      key32(),
			CiphertextBlob: []byte("wrapped-blob"),
			KeyId:          aws.String("arn:aws:kms:ap-south-1:1:key/abc"),
		},
		decOut: &kms.DecryptOutput{Plaintext: key32()},
	}
}

func ctxFor() map[string]string {
	return map[string]string{
		"purpose":     "commerce-pii",
		"scope":       "profile",
		"environment": "staging",
	}
}

// ─── Request shape ───────────────────────────────────────────────────

func TestGenerateDataKeyRequestsAES256WithTheExactContext(t *testing.T) {
	s := okStub()
	c := newWithAPI(s, time.Second)

	if _, _, err := c.GenerateDataKey(context.Background(), "cmk-1", ctxFor()); err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}

	if s.genIn.KeySpec != types.DataKeySpecAes256 {
		t.Fatalf("key spec = %q, want AES_256 — a shorter key silently weakens every sealed row",
			s.genIn.KeySpec)
	}
	if aws.ToString(s.genIn.KeyId) != "cmk-1" {
		t.Fatalf("key id = %q, want cmk-1", aws.ToString(s.genIn.KeyId))
	}
	// The context must arrive EXACTLY. KMS verifies it on Decrypt, so a
	// dropped or renamed field makes every key minted here unopenable.
	want := ctxFor()
	if len(s.genIn.EncryptionContext) != len(want) {
		t.Fatalf("encryption context = %v, want %v", s.genIn.EncryptionContext, want)
	}
	for k, v := range want {
		if s.genIn.EncryptionContext[k] != v {
			t.Fatalf("encryption context[%q] = %q, want %q", k, s.genIn.EncryptionContext[k], v)
		}
	}
	// NumberOfBytes must NOT be set alongside KeySpec: KMS rejects both.
	if s.genIn.NumberOfBytes != nil {
		t.Fatal("NumberOfBytes was set alongside KeySpec; KMS refuses that combination")
	}
}

func TestDecryptSendsTheStoredContextUnmodified(t *testing.T) {
	s := okStub()
	c := newWithAPI(s, time.Second)

	if _, err := c.Decrypt(context.Background(), []byte("blob"), ctxFor()); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(s.decIn.CiphertextBlob) != "blob" {
		t.Fatalf("ciphertext blob = %q", s.decIn.CiphertextBlob)
	}
	for k, v := range ctxFor() {
		if s.decIn.EncryptionContext[k] != v {
			t.Fatalf("encryption context[%q] = %q, want %q", k, s.decIn.EncryptionContext[k], v)
		}
	}
}

// ─── Refusals ────────────────────────────────────────────────────────

func TestAContextlessCallIsRefusedBeforeReachingKMS(t *testing.T) {
	s := okStub()
	c := newWithAPI(s, time.Second)

	if _, _, err := c.GenerateDataKey(context.Background(), "cmk-1", nil); err == nil {
		t.Fatal("an unbound data key was requested; anyone with CMK access could open it, " +
			"in any environment, for any scope")
	}
	if s.genIn != nil {
		t.Fatal("the call reached KMS despite being refused")
	}
	if _, err := c.Decrypt(context.Background(), []byte("blob"), map[string]string{}); err == nil {
		t.Fatal("a contextless Decrypt was attempted")
	}
}

func TestABlankKeyIDIsRefused(t *testing.T) {
	c := newWithAPI(okStub(), time.Second)
	if _, _, err := c.GenerateDataKey(context.Background(), "", ctxFor()); err == nil {
		t.Fatal("a data key was requested with no CMK")
	}
}

func TestAShortKeyIsRefused(t *testing.T) {
	s := okStub()
	s.genOut.Plaintext = make([]byte, 16) // AES-128
	c := newWithAPI(s, time.Second)

	if _, _, err := c.GenerateDataKey(context.Background(), "cmk-1", ctxFor()); err == nil {
		t.Fatal("a 16-byte key was accepted for AES-256; every row sealed with it would " +
			"look decryptable and not be")
	}

	s2 := okStub()
	s2.decOut.Plaintext = make([]byte, 31)
	c2 := newWithAPI(s2, time.Second)
	if _, err := c2.Decrypt(context.Background(), []byte("blob"), ctxFor()); err == nil {
		t.Fatal("a 31-byte unwrapped key was accepted")
	}
}

// The blob is the ONLY route back to the plaintext. A response without one is
// a key that can never be recovered, so it must never be used to seal anything.
func TestAMissingCiphertextBlobIsRefused(t *testing.T) {
	s := okStub()
	s.genOut.CiphertextBlob = nil
	c := newWithAPI(s, time.Second)

	if _, _, err := c.GenerateDataKey(context.Background(), "cmk-1", ctxFor()); err == nil {
		t.Fatal("a key with no wrapped blob was accepted; nothing sealed under it could " +
			"ever be opened again")
	}
}

func TestAnEmptyBlobIsRefusedBeforeReachingKMS(t *testing.T) {
	s := okStub()
	c := newWithAPI(s, time.Second)
	if _, err := c.Decrypt(context.Background(), nil, ctxFor()); err == nil {
		t.Fatal("Decrypt was attempted with no blob")
	}
	if s.decIn != nil {
		t.Fatal("the call reached KMS despite being refused")
	}
}

// ─── The caller owns its memory ──────────────────────────────────────

// pii zeroes plaintext keys after use. That is only meaningful if this adapter
// hands back a copy rather than a slice into an SDK response that may be
// pooled — zeroing a shared buffer would either corrupt the SDK's state or
// leave the real bytes elsewhere in memory.
func TestReturnedKeysAreCopies(t *testing.T) {
	s := okStub()
	c := newWithAPI(s, time.Second)

	plaintext, wrapped, err := c.GenerateDataKey(context.Background(), "cmk-1", ctxFor())
	if err != nil {
		t.Fatal(err)
	}
	for i := range plaintext {
		plaintext[i] = 0
	}
	for i := range wrapped {
		wrapped[i] = 0
	}
	if s.genOut.Plaintext[1] == 0 || s.genOut.CiphertextBlob[0] == 0 {
		t.Fatal("zeroing the returned key mutated the SDK response; the caller does not own " +
			"its memory and pii's zeroization reaches into a shared buffer")
	}
}

// ─── Failure mapping ─────────────────────────────────────────────────

func TestATimeoutIsBoundedAndNamed(t *testing.T) {
	s := okStub()
	s.delay = 200 * time.Millisecond
	c := newWithAPI(s, 20*time.Millisecond)

	_, _, err := c.GenerateDataKey(context.Background(), "cmk-1", ctxFor())
	if err == nil {
		t.Fatal("a slow KMS call was not bounded; a brownout becomes a thread leak")
	}
	if !contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want a named timeout", err)
	}
}

// Operators need to tell these apart: denied is an IAM problem, throttled is a
// capacity problem, pending-deletion is an emergency. Collapsing them into one
// message costs real time during an incident.
func TestNamedKMSFailuresAreDistinguishable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not found", &types.NotFoundException{}, "not found"},
		{"disabled", &types.DisabledException{}, "disabled"},
		{"invalid state", &types.KMSInvalidStateException{}, "invalid state"},
		{"unavailable", &types.KeyUnavailableException{}, "unavailable"},
		{"throttled", &types.LimitExceededException{}, "rate limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := okStub()
			s.genErr = tc.err
			c := newWithAPI(s, time.Second)
			_, _, err := c.GenerateDataKey(context.Background(), "cmk-1", ctxFor())
			if err == nil {
				t.Fatal("expected a failure")
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// An error must never carry the material. This asserts the two things that
// would be worst to leak: the wrapped blob and a data key.
func TestErrorsDoNotCarryKeyMaterial(t *testing.T) {
	s := okStub()
	s.decErr = errors.New("AccessDeniedException: not authorized")
	c := newWithAPI(s, time.Second)

	_, err := c.Decrypt(context.Background(), []byte("SECRET-WRAPPED-BLOB"), ctxFor())
	if err == nil {
		t.Fatal("expected a failure")
	}
	if contains(err.Error(), "SECRET-WRAPPED-BLOB") {
		t.Fatalf("the error carries the wrapped blob: %v", err)
	}
	if contains(err.Error(), "commerce-pii") {
		t.Fatalf("the error carries the encryption context: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
