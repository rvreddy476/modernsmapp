package postgres

import "testing"

// The fingerprint is what proves checkout ships to the row the service
// decrypted. It must change with any content or ciphertext change, and it must
// not be fooled by moving bytes across a field boundary.
func TestTheAddressFingerprintTracksContentAndCiphertext(t *testing.T) {
	base := addressContentFingerprint([]byte("enc-1"), nil, "", "", "Bengaluru", "KA", "560002")
	if again := addressContentFingerprint([]byte("enc-1"), nil, "", "", "Bengaluru", "KA", "560002"); again != base {
		t.Fatal("the same row fingerprinted twice gave two answers")
	}
	for name, other := range map[string]string{
		"a re-sealed street (fresh nonce)": addressContentFingerprint([]byte("enc-2"), nil, "", "", "Bengaluru", "KA", "560002"),
		"a different pincode":              addressContentFingerprint([]byte("enc-1"), nil, "", "", "Bengaluru", "KA", "560068"),
		"a plaintext street appearing":     addressContentFingerprint([]byte("enc-1"), nil, "5 Main St", "", "Bengaluru", "KA", "560002"),
		"bytes moved between fields":       addressContentFingerprint([]byte("enc-"), []byte("1"), "", "", "Bengaluru", "KA", "560002"),
	} {
		if other == base {
			t.Errorf("%s did not change the fingerprint", name)
		}
	}
	row := &AddressRow{AddressLine1Enc: []byte("enc-1"), City: "Bengaluru", State: "KA", PostalCode: "560002"}
	if row.ContentFingerprint() != base {
		t.Fatal("AddressRow.ContentFingerprint disagrees with the function checkout recomputes")
	}
}
