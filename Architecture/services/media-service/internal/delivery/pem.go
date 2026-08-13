package delivery

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// parseRSAPrivateKey accepts PKCS#1 or PKCS#8 PEM.
//
// Both are accepted because CloudFront key pairs arrive in either encoding
// depending on how they were generated, and refusing to boot over a key that
// is perfectly valid is an outage with a confusing cause.
func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	if len(pemBytes) == 0 {
		return nil, fmt.Errorf("empty private key")
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is %T, want RSA (CloudFront requires RSA)", parsed)
	}
	return key, nil
}
