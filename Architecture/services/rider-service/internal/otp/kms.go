package otp

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
)

// KMSClient handles envelope encryption for ride OTPs using AWS KMS.
type KMSClient struct {
	keyARN     string
	region     string
	httpClient *http.Client
	signer     *v4.Signer
	cfg        *aws.Config
	isProd     bool
}

// EnvelopeCiphertext holds the encrypted OTP and the KMS-wrapped data key.
type EnvelopeCiphertext struct {
	Ciphertext    []byte `json:"ciphertext"`
	Nonce         []byte `json:"nonce"`
	KMSWrappedKey []byte `json:"kms_wrapped_key"`
}

// NewKMSClient creates a new KMSClient with fail-closed production checks.
func NewKMSClient(ctx context.Context) (*KMSClient, error) {
	keyARN := strings.TrimSpace(os.Getenv("MOPEDU_OTP_KMS_KEY_ARN"))
	if keyARN == "" {
		keyARN = strings.TrimSpace(os.Getenv("AWS_KMS_KEY_ARN"))
	}
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	if region == "" {
		region = "ap-south-1"
	}

	isProd := isProductionEnv()

	if isProd && keyARN == "" {
		return nil, errors.New("MOPEDU_OTP_KMS_KEY_ARN must be set in production/staging: without it OTP envelope encryption cannot use AWS KMS")
	}

	var awsCfg *aws.Config
	if keyARN != "" {
		c, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil && isProd {
			return nil, fmt.Errorf("failed to load AWS configuration for KMS: %w", err)
		}
		awsCfg = &c
	}

	return &KMSClient{
		keyARN:     keyARN,
		region:     region,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		signer:     v4.NewSigner(),
		cfg:        awsCfg,
		isProd:     isProd,
	}, nil
}

// GenerateEnvelopeEncrypt encrypts the plaintext OTP with a freshly generated data key.
func (k *KMSClient) GenerateEnvelopeEncrypt(ctx context.Context, plaintextOTP string) (*EnvelopeCiphertext, error) {
	if plaintextOTP == "" {
		return nil, errors.New("cannot encrypt empty OTP")
	}

	plainDataKey, wrappedDataKey, err := k.generateDataKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate data key failed: %w", err)
	}
	defer zeroBytes(plainDataKey)

	block, err := aes.NewCipher(plainDataKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintextOTP), nil)

	return &EnvelopeCiphertext{
		Ciphertext:    ciphertext,
		Nonce:         nonce,
		KMSWrappedKey: wrappedDataKey,
	}, nil
}

// DecryptEnvelope decrypts the OTP from the envelope ciphertext using AWS KMS.
func (k *KMSClient) DecryptEnvelope(ctx context.Context, env *EnvelopeCiphertext) (string, error) {
	if env == nil || len(env.Ciphertext) == 0 || len(env.Nonce) == 0 {
		return "", errors.New("invalid envelope ciphertext")
	}

	plainDataKey, err := k.decryptDataKey(ctx, env.KMSWrappedKey)
	if err != nil {
		return "", fmt.Errorf("decrypt data key failed: %w", err)
	}
	defer zeroBytes(plainDataKey)

	block, err := aes.NewCipher(plainDataKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	plaintext, err := gcm.Open(nil, env.Nonce, env.Ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt OTP payload: %w", err)
	}

	return string(plaintext), nil
}

func (k *KMSClient) generateDataKey(ctx context.Context) ([]byte, []byte, error) {
	if k.keyARN != "" && k.cfg != nil {
		creds, err := k.cfg.Credentials.Retrieve(ctx)
		if err != nil {
			if k.isProd {
				return nil, nil, fmt.Errorf("AWS KMS credentials unavailable: %w", err)
			}
		} else {
			endpoint := fmt.Sprintf("https://kms.%s.amazonaws.com/", k.region)
			reqBody, _ := json.Marshal(map[string]any{
				"KeyId":   k.keyARN,
				"KeySpec": "AES_256",
			})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
			if err != nil {
				return nil, nil, err
			}
			req.Header.Set("Content-Type", "application/x-amz-json-1.1")
			req.Header.Set("X-Amz-Target", "TrentService.GenerateDataKey")

			h := sha256.Sum256(reqBody)
			payloadHash := fmt.Sprintf("%x", h)
			if err := k.signer.SignHTTP(ctx, creds, req, payloadHash, "kms", k.region, time.Now()); err == nil {
				resp, err := k.httpClient.Do(req)
				if err == nil && resp.StatusCode == http.StatusOK {
					defer resp.Body.Close()
					var res struct {
						Plaintext      string `json:"Plaintext"`
						CiphertextBlob string `json:"CiphertextBlob"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&res); err == nil {
						plain, err1 := base64.StdEncoding.DecodeString(res.Plaintext)
						wrapped, err2 := base64.StdEncoding.DecodeString(res.CiphertextBlob)
						if err1 == nil && err2 == nil && len(plain) == 32 {
							return plain, wrapped, nil
						}
					}
				}
			}
			if k.isProd {
				return nil, nil, fmt.Errorf("AWS KMS GenerateDataKey failed")
			}
		}
	}

	if k.isProd {
		return nil, nil, errors.New("AWS KMS is required in production but unavailable")
	}

	// Non-production local mock key generation
	rawKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, rawKey); err != nil {
		return nil, nil, err
	}
	mockWrapped := append([]byte("mock_kms:"), rawKey...)
	return rawKey, mockWrapped, nil
}

func (k *KMSClient) decryptDataKey(ctx context.Context, wrappedKey []byte) ([]byte, error) {
	if bytes.HasPrefix(wrappedKey, []byte("mock_kms:")) {
		if k.isProd {
			return nil, errors.New("mock KMS data key is rejected in production")
		}
		raw := make([]byte, len(wrappedKey)-9)
		copy(raw, wrappedKey[9:])
		return raw, nil
	}

	if k.keyARN != "" && k.cfg != nil {
		creds, err := k.cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return nil, fmt.Errorf("AWS credentials retrieve failed: %w", err)
		}
		endpoint := fmt.Sprintf("https://kms.%s.amazonaws.com/", k.region)
		reqBody, _ := json.Marshal(map[string]any{
			"CiphertextBlob": base64.StdEncoding.EncodeToString(wrappedKey),
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-amz-json-1.1")
		req.Header.Set("X-Amz-Target", "TrentService.Decrypt")

		h := sha256.Sum256(reqBody)
		payloadHash := fmt.Sprintf("%x", h)
		if err := k.signer.SignHTTP(ctx, creds, req, payloadHash, "kms", k.region, time.Now()); err != nil {
			return nil, fmt.Errorf("KMS request sign failed: %w", err)
		}
		resp, err := k.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("KMS request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("KMS Decrypt returned status %d", resp.StatusCode)
		}
		var res struct {
			Plaintext string `json:"Plaintext"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return nil, err
		}
		plain, err := base64.StdEncoding.DecodeString(res.Plaintext)
		if err != nil || len(plain) != 32 {
			return nil, errors.New("invalid KMS plaintext data key")
		}
		return plain, nil
	}

	return nil, errors.New("cannot decrypt data key: no KMS configuration")
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
