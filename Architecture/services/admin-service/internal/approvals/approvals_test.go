package approvals

import (
	"encoding/json"
	"testing"
)

func TestCanonicalSurvivesAJSONBRoundTrip(t *testing.T) {
	type payload struct {
		RemittanceID  string `json:"remittance_id"`
		PayoutBatchID string `json:"payout_batch_id,omitempty"`
		Amount        int64  `json:"amount_paise"`
	}
	fromGo, err := Canonical(payload{"r-1", "b-1", 12345678901234})
	if err != nil {
		t.Fatal(err)
	}
	// JSONB hands keys back in its own order with its own spacing.
	fromDB, err := Canonical(json.RawMessage(`{"amount_paise": 12345678901234, "remittance_id": "r-1", "payout_batch_id": "b-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(fromGo) != string(fromDB) {
		t.Fatalf("canonical forms differ:\n%s\n%s", fromGo, fromDB)
	}
	if Hash("commerce", "cod.settle", "cod_remittance", "r-1", fromGo) != Hash("commerce", "cod.settle", "cod_remittance", "r-1", fromDB) {
		t.Fatal("hash differs across the round trip")
	}
}

func TestHashBindsEveryField(t *testing.T) {
	p := []byte(`{"a":1}`)
	base := Hash("commerce", "cod.settle", "cod_remittance", "r-1", p)
	for name, h := range map[string]string{
		"app":         Hash("food", "cod.settle", "cod_remittance", "r-1", p),
		"operation":   Hash("commerce", "cod.reverse", "cod_remittance", "r-1", p),
		"target type": Hash("commerce", "cod.settle", "seller", "r-1", p),
		"target id":   Hash("commerce", "cod.settle", "cod_remittance", "r-2", p),
		"payload":     Hash("commerce", "cod.settle", "cod_remittance", "r-1", []byte(`{"a":2}`)),
		"boundary":    Hash("commerc", "ecod.settle", "cod_remittance", "r-1", p),
	} {
		if h == base {
			t.Fatalf("%s change kept the hash", name)
		}
	}
}
