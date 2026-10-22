package kmsclient

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// What this adapter sends to KMS and what it refuses to accept back. The
// key model itself is proven in shared/fieldcrypt against a fake wrapper.

type stubKMS struct {
	genIn  *kms.GenerateDataKeyInput
	decIn  *kms.DecryptInput
	genOut *kms.GenerateDataKeyOutput
	decOut *kms.DecryptOutput
	err    error
}

func (s *stubKMS) GenerateDataKey(_ context.Context, in *kms.GenerateDataKeyInput, _ ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error) {
	s.genIn = in
	return s.genOut, s.err
}

func (s *stubKMS) Decrypt(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	s.decIn = in
	return s.decOut, s.err
}

func key32() []byte { return bytes.Repeat([]byte{0x42}, 32) }

func keyCtx() map[string]string {
	return map[string]string{"purpose": "chat-field", "scope": "chat.field", "environment": "production"}
}

func TestGenerateDataKeyRequestsAES256UnderTheContext(t *testing.T) {
	stub := &stubKMS{genOut: &kms.GenerateDataKeyOutput{Plaintext: key32(), CiphertextBlob: []byte("wrapped")}}
	c := newWithAPI(stub, "alias/atpost-prod-chat-field", 0)
	plain, wrapped, err := c.GenerateDataKey(context.Background(), keyCtx())
	if err != nil {
		t.Fatal(err)
	}
	if stub.genIn.KeySpec != types.DataKeySpecAes256 || aws.ToString(stub.genIn.KeyId) != "alias/atpost-prod-chat-field" {
		t.Fatalf("request = %+v", stub.genIn)
	}
	if stub.genIn.EncryptionContext["environment"] != "production" || len(stub.genIn.EncryptionContext) != 3 {
		t.Fatal("the encryption context did not reach KMS unmodified")
	}
	if !bytes.Equal(plain, key32()) || string(wrapped) != "wrapped" {
		t.Fatal("the key halves were not returned intact")
	}
	plain[0] = 0
	if stub.genOut.Plaintext[0] == 0 {
		t.Fatal("the returned key aliases the SDK response")
	}
}

func TestRefusesUnusableResponsesAndRequests(t *testing.T) {
	ctx := context.Background()
	short := &stubKMS{genOut: &kms.GenerateDataKeyOutput{Plaintext: key32()[:16], CiphertextBlob: []byte("w")},
		decOut: &kms.DecryptOutput{Plaintext: key32()[:24]}}
	c := newWithAPI(short, "k", 0)
	if _, _, err := c.GenerateDataKey(ctx, keyCtx()); err == nil {
		t.Fatal("a 16-byte data key was accepted")
	}
	if _, err := c.Unwrap(ctx, []byte("w"), keyCtx()); err == nil {
		t.Fatal("a 24-byte unwrapped key was accepted")
	}
	noBlob := newWithAPI(&stubKMS{genOut: &kms.GenerateDataKeyOutput{Plaintext: key32()}}, "k", 0)
	if _, _, err := noBlob.GenerateDataKey(ctx, keyCtx()); err == nil {
		t.Fatal("a key without its wrapped form was accepted")
	}
	if _, _, err := c.GenerateDataKey(ctx, nil); err == nil {
		t.Fatal("an unbound data key was requested")
	}
	if _, err := c.Unwrap(ctx, []byte("w"), nil); err == nil {
		t.Fatal("an unwrap without a context was sent")
	}
	if _, err := c.Unwrap(ctx, nil, keyCtx()); err == nil {
		t.Fatal("an empty wrapped key was sent")
	}
}

func TestUnwrapPassesTheStoredContextBack(t *testing.T) {
	stub := &stubKMS{decOut: &kms.DecryptOutput{Plaintext: key32()}}
	c := newWithAPI(stub, "k", 0)
	if _, err := c.Unwrap(context.Background(), []byte("wrapped"), keyCtx()); err != nil {
		t.Fatal(err)
	}
	if string(stub.decIn.CiphertextBlob) != "wrapped" || stub.decIn.EncryptionContext["scope"] != "chat.field" {
		t.Fatalf("decrypt request = %+v", stub.decIn)
	}
}

func TestErrorsNameTheFailureNotTheMaterial(t *testing.T) {
	stub := &stubKMS{err: &types.DisabledException{Message: aws.String("key arn:aws:kms:secret-detail is disabled")}}
	c := newWithAPI(stub, "k", 0)
	_, err := c.Unwrap(context.Background(), []byte("wrapped-secret"), keyCtx())
	if err == nil || !strings.Contains(err.Error(), "disabled") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v", err)
	}
	stub.err = errors.New("opaque failure with wrapped-secret inside")
	_, err = c.Unwrap(context.Background(), []byte("wrapped-secret"), keyCtx())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v", err)
	}
}
