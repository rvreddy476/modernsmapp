package delivery

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Module 4 M4-P0-5 — bounded, signed delivery for protected media.
//
// WHY THIS EXISTS
//
// Publication and byte delivery are separate gates. Before this, passing a
// content-list query was the ONLY gate: blob.Store.ObjectURL returned
//
//	<MEDIA_CDN_BASE_URL>/<bucket>/<key>
//
// whenever a CDN base was configured. That URL is stable, unauthenticated and
// permanent. Every story/post authorization added in Modules 1-4 governed the
// JSON; the bytes stayed reachable to anyone who had ever seen the URL, and
// stayed reachable after a block, a takedown, or a deletion.
//
// WHY CANNED-POLICY SIGNING, IMPLEMENTED HERE
//
// The AWS SDK CloudFront signer is not vendored, and this module tree vendors
// its dependencies — adding one for ~60 lines of well-specified signing would
// be a large, unrelated diff. Canned policy is a documented, stable format and
// needs only stdlib crypto. The test generates a real RSA key and verifies the
// signature against it, so this is not "probably right".
//
// SHA-1 IS NOT A CHOICE HERE
//
// CloudFront specifies RSA-SHA1 for signed URLs. Using anything else produces
// signatures CloudFront rejects. The security property comes from the RSA key
// and the short expiry, not from the digest.

// ObjectClass says whether bytes may be served without authorization.
type ObjectClass string

const (
	// ClassPublic — bytes anyone may fetch (avatars on public profiles,
	// approved public post media). Served from a stable CDN URL.
	ClassPublic ObjectClass = "public"
	// ClassProtected — bytes that require content authorization on every
	// issuance: story media, followers/close-friends posts, DM attachments.
	ClassProtected ObjectClass = "protected"
)

const (
	// PublicPrefix / ProtectedPrefix partition the bucket.
	//
	// The prefix is the enforceable half of the invariant: the bucket policy
	// and CloudFront behaviours key off it, so a protected object cannot be
	// served by the public behaviour even if application code asks for the
	// wrong class. Metadata alone would not survive an application bug.
	PublicPrefix    = "public/"
	ProtectedPrefix = "protected/"

	// MaxProtectedTTL bounds every protected signature.
	//
	// A signed URL cannot be revoked before it expires, so the TTL IS the
	// revocation window: after a block or takedown, an already-issued URL keeps
	// working for at most this long. Five minutes is long enough to start
	// playback and short enough that a leaked link is not a durable capability.
	MaxProtectedTTL = 5 * time.Minute
)

// ClassForKey derives the class from the object key.
//
// An unrecognised prefix is PROTECTED, not public. New key layouts must opt
// into public delivery explicitly; defaulting the other way means any future
// prefix ships world-readable.
func ClassForKey(key string) ObjectClass {
	if strings.HasPrefix(key, PublicPrefix) {
		return ClassPublic
	}
	return ClassProtected
}

// Signer issues delivery URLs.
type Signer struct {
	cdnBaseURL string
	keyPairID  string
	privateKey *rsa.PrivateKey
}

// Config carries the CloudFront signing identity.
type Config struct {
	CDNBaseURL string
	KeyPairID  string
	// PrivateKeyPEM is the CloudFront key-group private key. It comes from
	// Secrets Manager via the pod's IRSA identity — never from a manifest.
	PrivateKeyPEM []byte
}

// NewSigner validates the configuration up front.
//
// It returns an error rather than degrading to unsigned URLs. A signer that
// silently falls back to public delivery when misconfigured is the same defect
// this whole item exists to remove, just moved one layer down.
func NewSigner(cfg Config) (*Signer, error) {
	if cfg.CDNBaseURL == "" {
		return nil, fmt.Errorf("delivery: CDN base URL is required")
	}
	if cfg.KeyPairID == "" {
		return nil, fmt.Errorf("delivery: CloudFront key pair id is required")
	}
	key, err := parseRSAPrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("delivery: private key: %w", err)
	}
	return &Signer{
		cdnBaseURL: strings.TrimRight(cfg.CDNBaseURL, "/"),
		keyPairID:  cfg.KeyPairID,
		privateKey: key,
	}, nil
}

// PublicURL returns the stable URL for a public object.
func (s *Signer) PublicURL(key string) (string, error) {
	if ClassForKey(key) != ClassPublic {
		// Refusing here is the point: this is the call that would quietly
		// publish protected bytes if a caller passed the wrong key.
		return "", fmt.Errorf("delivery: refusing a stable public URL for protected key %q", key)
	}
	return s.cdnBaseURL + "/" + key, nil
}

// SignProtected returns a bounded signed URL for a protected object.
//
// The caller MUST have completed content authorization first. This function
// signs; it does not decide.
func (s *Signer) SignProtected(key string, ttl time.Duration, now time.Time) (string, error) {
	if ClassForKey(key) != ClassProtected {
		return "", fmt.Errorf("delivery: %q is not a protected key", key)
	}
	if ttl <= 0 || ttl > MaxProtectedTTL {
		// Clamping silently would let a caller ask for a 24-hour URL and
		// believe it got one, or worse, get one.
		return "", fmt.Errorf("delivery: ttl %s outside (0, %s]", ttl, MaxProtectedTTL)
	}

	resource := s.cdnBaseURL + "/" + key
	expires := now.Add(ttl).Unix()

	policy := cannedPolicy{
		Statement: []statement{{
			Resource:  resource,
			Condition: condition{DateLessThan: epoch{EpochTime: expires}},
		}},
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("delivery: encode policy: %w", err)
	}

	sum := sha1.Sum(raw)
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA1, sum[:])
	if err != nil {
		return "", fmt.Errorf("delivery: sign: %w", err)
	}

	q := url.Values{}
	q.Set("Expires", fmt.Sprintf("%d", expires))
	q.Set("Signature", cloudFrontBase64(sig))
	q.Set("Key-Pair-Id", s.keyPairID)

	// NOTE: no bearer token, session id, or viewer id appears in the URL or
	// the query. CloudFront caches on the full URL, so a token in the cache key
	// both fragments the cache and writes the credential into edge logs.
	return resource + "?" + q.Encode(), nil
}

// cloudFrontBase64 is standard base64 with the characters that are unsafe in a
// URL replaced, per the CloudFront signed-URL specification.
func cloudFrontBase64(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	s = strings.ReplaceAll(s, "+", "-")
	s = strings.ReplaceAll(s, "=", "_")
	s = strings.ReplaceAll(s, "/", "~")
	return s
}

type cannedPolicy struct {
	Statement []statement `json:"Statement"`
}

type statement struct {
	Resource  string    `json:"Resource"`
	Condition condition `json:"Condition"`
}

type condition struct {
	DateLessThan epoch `json:"DateLessThan"`
}

type epoch struct {
	EpochTime int64 `json:"AWS:EpochTime"`
}
