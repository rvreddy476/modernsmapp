// Package kmsclient is the AWS KMS adapter behind fieldcrypt.KeyWrapper for
// chat field encryption at rest.
//
// It is commerce-service/internal/kmsclient re-shaped for the KeyWrapper
// interface (the CMK id is held by the client instead of passed per call):
// two calls, GenerateDataKey (AES-256) and Decrypt, both with an encryption
// context that KMS itself enforces. The versions are the ones commerce pins
// (service/kms v1.55.5 on aws-sdk-go-v2 v1.43.5).
//
// # Credentials
//
// The default AWS credential chain only — IRSA in the cluster (the
// chat-message-service ServiceAccount's role). There is deliberately no way
// to pass an access key, a secret or a profile into this package.
//
// # What is never logged or returned
//
// Plaintext data keys, wrapped blobs, message content and the encryption
// context's values. Errors name the operation and the AWS failure class.
package kmsclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"
)

// DefaultTimeout bounds one KMS call. Calls happen at boot (unwrap every
// version) and on a rotation, never per message.
const DefaultTimeout = 5 * time.Second

type api interface {
	GenerateDataKey(ctx context.Context, in *kms.GenerateDataKeyInput, optFns ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, optFns ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// Client implements fieldcrypt.KeyWrapper against one customer-managed key.
type Client struct {
	api     api
	keyID   string
	timeout time.Duration
}

var _ fieldcrypt.KeyWrapper = (*Client)(nil)

// New builds the adapter from the default AWS credential chain. It makes no
// network call; the boot readiness probe proves KMS answers.
func New(ctx context.Context, keyID string) (*Client, error) {
	if keyID == "" {
		return nil, errors.New("kmsclient: a KMS key id is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("kmsclient: loading AWS configuration: %w", err)
	}
	if cfg.Region == "" {
		return nil, errors.New("kmsclient: no AWS region resolved; set AWS_REGION")
	}
	return &Client{api: kms.NewFromConfig(cfg), keyID: keyID, timeout: DefaultTimeout}, nil
}

// newWithAPI is the test seam (unexported: no caller can inject another
// credential source in production).
func newWithAPI(a api, keyID string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{api: a, keyID: keyID, timeout: timeout}
}

// GenerateDataKey mints a 256-bit key under the CMK and returns it with its
// wrapped form, which the caller must persist before sealing anything.
func (c *Client) GenerateDataKey(ctx context.Context, keyContext map[string]string) (plaintext, wrapped []byte, err error) {
	if len(keyContext) == 0 {
		return nil, nil, errors.New("kmsclient: an encryption context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out, err := c.api.GenerateDataKey(ctx, &kms.GenerateDataKeyInput{
		KeyId:             aws.String(c.keyID),
		KeySpec:           types.DataKeySpecAes256,
		EncryptionContext: keyContext,
	})
	if err != nil {
		return nil, nil, redact("GenerateDataKey", err)
	}
	if len(out.Plaintext) != fieldcrypt.DataKeySize {
		return nil, nil, fmt.Errorf("kmsclient: GenerateDataKey returned a %d-byte key, want %d", len(out.Plaintext), fieldcrypt.DataKeySize)
	}
	if len(out.CiphertextBlob) == 0 {
		return nil, nil, errors.New("kmsclient: GenerateDataKey returned no wrapped key; it could never be recovered")
	}
	return append([]byte(nil), out.Plaintext...), append([]byte(nil), out.CiphertextBlob...), nil
}

// Unwrap decrypts a wrapped key under the SAME encryption context it was
// created with; KMS refuses any difference.
func (c *Client) Unwrap(ctx context.Context, wrapped []byte, keyContext map[string]string) ([]byte, error) {
	if len(wrapped) == 0 {
		return nil, errors.New("kmsclient: no wrapped key to decrypt")
	}
	if len(keyContext) == 0 {
		return nil, errors.New("kmsclient: an encryption context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out, err := c.api.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob:    wrapped,
		EncryptionContext: keyContext,
		KeyId:             aws.String(c.keyID),
	})
	if err != nil {
		return nil, redact("Decrypt", err)
	}
	if len(out.Plaintext) != fieldcrypt.DataKeySize {
		return nil, fmt.Errorf("kmsclient: Decrypt returned a %d-byte key, want %d", len(out.Plaintext), fieldcrypt.DataKeySize)
	}
	return append([]byte(nil), out.Plaintext...), nil
}

// redact reduces an SDK error to its failure class, which is what an
// operator acts on, without whatever request detail the SDK put in it.
func redact(op string, err error) error {
	var (
		notFound  *types.NotFoundException
		disabled  *types.DisabledException
		invalid   *types.KMSInvalidStateException
		unavail   *types.KeyUnavailableException
		throttled *types.LimitExceededException
		wrongKey  *types.IncorrectKeyException
		badCipher *types.InvalidCiphertextException
	)
	switch {
	case errors.As(err, &notFound):
		return fmt.Errorf("kmsclient: %s: the KMS key was not found", op)
	case errors.As(err, &disabled):
		return fmt.Errorf("kmsclient: %s: the KMS key is disabled", op)
	case errors.As(err, &invalid):
		return fmt.Errorf("kmsclient: %s: the KMS key is in an invalid state (pending deletion or import)", op)
	case errors.As(err, &unavail):
		return fmt.Errorf("kmsclient: %s: the KMS key is unavailable", op)
	case errors.As(err, &throttled):
		return fmt.Errorf("kmsclient: %s: KMS rate limit exceeded", op)
	case errors.As(err, &wrongKey):
		return fmt.Errorf("kmsclient: %s: the wrapped key does not belong to this KMS key", op)
	case errors.As(err, &badCipher):
		return fmt.Errorf("kmsclient: %s: the wrapped key or its encryption context does not match", op)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("kmsclient: %s: timed out", op)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("kmsclient: %s: cancelled", op)
	default:
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// AccessDeniedException and friends: the code is what an
			// operator needs to find the call in CloudTrail.
			return fmt.Errorf("kmsclient: %s failed: %s", op, apiErr.ErrorCode())
		}
		return fmt.Errorf("kmsclient: %s failed (%T)", op, err)
	}
}
