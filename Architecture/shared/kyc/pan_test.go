package kyc

import (
	"errors"
	"strings"
	"testing"
)

func panPartOf(t *testing.T, err error) PANPart {
	t.Helper()
	if !errors.Is(err, ErrInvalidPAN) {
		t.Fatalf("error %v does not match ErrInvalidPAN", err)
	}
	var pe *PANError
	if !errors.As(err, &pe) {
		t.Fatalf("error %v is not a *PANError", err)
	}
	return pe.Part
}

// Synthetic PANs only: ZZZ + holder + Z0000Z.
func TestValidatePAN_EveryHolderType(t *testing.T) {
	want := map[PANHolderType]string{
		'P': "INDIVIDUAL", 'C': "COMPANY", 'H': "HUF", 'F': "FIRM", 'A': "AOP",
		'T': "TRUST", 'B': "BOI", 'L': "LOCAL_AUTHORITY", 'J': "ARTIFICIAL_JURIDICAL_PERSON", 'G': "GOVERNMENT",
	}
	for h, name := range want {
		in := "ZZZ" + string(byte(h)) + "Z0000Z"
		p, err := ValidatePAN(in)
		if err != nil {
			t.Errorf("holder %c: %v", byte(h), err)
			continue
		}
		if p.Normalized != in || p.HolderType != h || h.String() != name || !h.Valid() {
			t.Errorf("holder %c: %+v %s", byte(h), p, h.String())
		}
	}
	if PANHolderType('D').String() != "UNKNOWN" {
		t.Error("unknown holder type string")
	}
}

func TestValidatePAN_Normalises(t *testing.T) {
	for _, in := range []string{"zzzpz0000z", "  ZZZPZ0000Z ", "\tzzzPZ0000z\n"} {
		p, err := ValidatePAN(in)
		if err != nil || p.Normalized != "ZZZPZ0000Z" {
			t.Errorf("ValidatePAN(%q) = %+v, %v", in, p, err)
		}
	}
}

func TestValidatePAN_RejectsUnknownHolderType(t *testing.T) {
	for _, h := range "DEIKMNOQRSUVWXYZ" {
		in := "ZZZ" + string(h) + "Z0000Z"
		_, err := ValidatePAN(in)
		if err == nil {
			t.Errorf("holder %c accepted", h)
			continue
		}
		if p := panPartOf(t, err); p != PANPartHolderType {
			t.Errorf("holder %c: part %s", h, p)
		}
		assertNoEcho(t, err, in)
	}
}

func TestValidatePAN_FormatFailures(t *testing.T) {
	for _, in := range []string{
		"", "ZZZPZ0000", "ZZZPZ0000ZZ", "ZZZP00000Z", "ZZZPZ000ZZ", "ZZZPZ00001",
		"ZZZPZ 0000Z", "1ZZPZ0000Z", "ZZZPZ0000Z1", "ZZZPſ0000Z",
	} {
		_, err := ValidatePAN(in)
		if err == nil {
			t.Errorf("ValidatePAN(%q) accepted", in)
			continue
		}
		if p := panPartOf(t, err); p != PANPartFormat {
			t.Errorf("ValidatePAN(%q): part %s", in, p)
		}
		assertNoEcho(t, err, in)
		if strings.Contains(err.Error(), "0000") {
			t.Errorf("error text echoes digits: %q", err.Error())
		}
	}
}
