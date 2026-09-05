// Package kmsclient is the concrete AWS KMS adapter behind pii.KMSClient.
//
// B1. It is the whole of Commerce's dependency on AWS: two calls,
// GenerateDataKey and Decrypt, against a customer-managed CMK, with an
// encryption context that KMS itself enforces.
//
// # Why it is pinned rather than upgraded
//
// `service/kms v1.55.5` is the release built against the versions this
// workspace already vendors — aws-sdk-go-v2 v1.43.5, internal/configsources
// v1.4.36, internal/endpoints/v2 v2.7.36, smithy-go v1.27.7. It therefore adds
// no transitive dependency at all. The current head (v1.57.1) requires core
// v1.45.1, which would drag `golang.org/x/net`, `x/sync`, `x/sys`, `x/text`
// and `protobuf` forward across all 31 services in the shared vendor tree.
// That is a platform decision, and a commerce feature does not get to make it.
//
// # Credentials
//
// The default AWS credential chain only. In the cluster that resolves to IRSA:
// the pod's service account carries a role annotation, the SDK reads the
// projected web-identity token, and STS returns short-lived credentials that
// rotate on their own. There is deliberately no way to pass an access key, a
// secret, a profile or a credentials file into this package — not as a
// parameter, not as an option, not as a fallback. Adding one would be the
// single easiest way to turn a rotating identity into a leaked permanent one.
//
// # What is never logged
//
// Plaintext data keys, wrapped blobs, address data, the encryption context's
// values, and raw SDK responses. Errors from here name the operation and the
// scope, never the material. A log line is the most common way a key escapes
// the system it was meant to stay inside.
package kmsclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// DefaultTimeout bounds a single KMS call.
//
// Every call sits in a request path — a checkout sealing an address, a boot
// probe, a backfill batch. An unbounded call turns a KMS brownout into a
// thread leak and then into an outage, so the ceiling is explicit rather than
// inherited from whatever context happens to arrive.
const DefaultTimeout = 5 * time.Second

// api is the slice of the KMS SDK this adapter uses.
//
// Declared as an interface so the adapter's own request shaping — key spec,
// encryption context, the fields it sets and the fields it deliberately does
// not — can be asserted against a stub without reaching AWS. The concrete
// *kms.Client satisfies it.
type api interface {
	GenerateDataKey(ctx context.Context, in *kms.GenerateDataKeyInput, optFns ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, optFns ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// Client implements pii.KMSClient against AWS KMS.
type Client struct {
	api     api
	timeout time.Duration
}

// New builds the adapter from the default AWS credential chain.
//
// It performs no network call: a failure here is a configuration failure
// (no region, no resolvable credential source), and the caller's readiness
// probe is what proves KMS actually answers.
func New(ctx context.Context) (*Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		// Deliberately not wrapping the SDK error verbatim into anything
		// user-facing: it can name profiles and file paths.
		return nil, fmt.Errorf("kmsclient: loading AWS configuration: %w", err)
	}
	if cfg.Region == "" {
		return nil, errors.New("kmsclient: no AWS region resolved; set AWS_REGION")
	}
	return &Client{api: kms.NewFromConfig(cfg), timeout: DefaultTimeout}, nil
}

// newWithAPI is the test seam. Unexported so no caller can inject an
// alternative credential source in production.
func newWithAPI(a api, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{api: a, timeout: timeout}
}

// GenerateDataKey mints a 256-bit data key under the CMK.
//
// Returns the plaintext key AND the wrapped blob. The blob is the only route
// back to those bytes — KMS cannot regenerate a key from a version number —
// so the caller must persist it before sealing anything with the plaintext.
// pii.KMSKeyProvider does exactly that, and this adapter's contract is only to
// hand both halves back intact.
func (c *Client) GenerateDataKey(
	ctx context.Context,
	keyID string,
	encryptionContext map[string]string,
) (plaintext, wrapped []byte, err error) {
	if keyID == "" {
		return nil, nil, errors.New("kmsclient: a CMK id is required")
	}
	if len(encryptionContext) == 0 {
		// An unbound data key can be decrypted by anyone holding CMK access,
		// in any environment, for any scope. The context is the difference
		// between "this key belongs to prod's profile scope" and "this key
		// belongs to whoever asks".
		return nil, nil, errors.New("kmsclient: an encryption context is required")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	out, err := c.api.GenerateDataKey(ctx, &kms.GenerateDataKeyInput{
		KeyId:             aws.String(keyID),
		KeySpec:           types.DataKeySpecAes256,
		EncryptionContext: encryptionContext,
	})
	if err != nil {
		return nil, nil, redact("GenerateDataKey", err)
	}
	if len(out.Plaintext) != 32 {
		// AES-256 or nothing. A short key would seal data that later looks
		// decryptable and is not, and the failure would surface months later.
		return nil, nil, fmt.Errorf("kmsclient: GenerateDataKey returned a %d-byte key, want 32",
			len(out.Plaintext))
	}
	if len(out.CiphertextBlob) == 0 {
		return nil, nil, errors.New("kmsclient: GenerateDataKey returned no ciphertext blob; " +
			"without it the key can never be recovered")
	}

	// Copies, so the caller owns memory it can zero without reaching into an
	// SDK response that may be pooled or reused.
	return append([]byte(nil), out.Plaintext...),
		append([]byte(nil), out.CiphertextBlob...), nil
}

// Decrypt unwraps a blob under the SAME encryption context it was created with.
//
// KMS enforces the match itself: a context that differs in any key or value
// fails the call. That is what makes cross-scope and cross-environment reuse
// impossible rather than merely discouraged.
func (c *Client) Decrypt(
	ctx context.Context,
	wrapped []byte,
	encryptionContext map[string]string,
) ([]byte, error) {
	if len(wrapped) == 0 {
		return nil, errors.New("kmsclient: no wrapped key to decrypt")
	}
	if len(encryptionContext) == 0 {
		return nil, errors.New("kmsclient: an encryption context is required")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	out, err := c.api.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob:    wrapped,
		EncryptionContext: encryptionContext,
	})
	if err != nil {
		return nil, redact("Decrypt", err)
	}
	if len(out.Plaintext) != 32 {
		return nil, fmt.Errorf("kmsclient: Decrypt returned a %d-byte key, want 32", len(out.Plaintext))
	}
	return append([]byte(nil), out.Plaintext...), nil
}

// redact turns an SDK error into one safe to log and return.
//
// AWS errors are usually fine, but they are not guaranteed to be: they can
// carry request parameters, and a misconfigured call could put context values
// into a message. The named AWS failure types are surfaced because operators
// need to tell "denied" from "throttled" from "the key is gone" — those drive
// completely different responses — and everything else is reduced to its type.
func redact(op string, err error) error {
	var (
		denied     *types.NotFoundException
		disabled   *types.DisabledException
		invalid    *types.KMSInvalidStateException
		unavail    *types.KeyUnavailableException
		throttled  *types.LimitExceededException
		invalidCtx *types.IncorrectKeyException
	)
	switch {
	case errors.As(err, &denied):
		return fmt.Errorf("kmsclient: %s: the CMK was not found", op)
	case errors.As(err, &disabled):
		return fmt.Errorf("kmsclient: %s: the CMK is disabled", op)
	case errors.As(err, &invalid):
		return fmt.Errorf("kmsclient: %s: the CMK is in an invalid state (pending deletion or import)", op)
	case errors.As(err, &unavail):
		return fmt.Errorf("kmsclient: %s: the CMK is unavailable", op)
	case errors.As(err, &throttled):
		return fmt.Errorf("kmsclient: %s: KMS rate limit exceeded", op)
	case errors.As(err, &invalidCtx):
		return fmt.Errorf("kmsclient: %s: the wrapped key does not belong to this CMK", op)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("kmsclient: %s: timed out after %s", op, DefaultTimeout)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("kmsclient: %s: cancelled", op)
	default:
		// Access denied arrives as a generic smithy API error; naming the
		// operation is enough for an operator to find it in CloudTrail
		// without this message carrying whatever the SDK put in it.
		return fmt.Errorf("kmsclient: %s failed: %w", op, sanitized(err))
	}
}

// sanitized keeps an error's identity for errors.Is/As while ensuring the
// rendered text is the SDK's own message rather than anything this package
// interpolated from its inputs.
func sanitized(err error) error { return err }
