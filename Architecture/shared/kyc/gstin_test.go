package kyc

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// refGSTINCheck is an independent mod-36 implementation used only to BUILD
// test vectors. It walks right to left starting at weight 2, the way GSTN's
// published description reads, where production walks left to right from
// weight 1. A mutation in production therefore cannot move the expected
// values. Every identifier built from it is synthetic.
func refGSTINCheck(t *testing.T, first14 string) byte {
	t.Helper()
	const alpha = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if len(first14) != 14 {
		t.Fatalf("refGSTINCheck: need 14 characters, got %d", len(first14))
	}
	weight, sum := 2, 0
	for i := 13; i >= 0; i-- {
		cp := strings.IndexByte(alpha, first14[i])
		if cp < 0 {
			t.Fatalf("refGSTINCheck: bad character at %d", i)
		}
		d := weight * cp
		sum += d/36 + d%36
		if weight == 2 {
			weight = 1
		} else {
			weight = 2
		}
	}
	return alpha[(36-sum%36)%36]
}

// synthGSTIN builds a checksum-valid GSTIN from a state code and a synthetic
// PAN (serial 0000, which is not issued in practice).
func synthGSTIN(t *testing.T, state, pan string) string {
	t.Helper()
	base := state + pan + "1Z"
	return base + string(refGSTINCheck(t, base))
}

// assertNoEcho fails when an error's text contains the value that was
// validated (or a meaningful piece of it).
func assertNoEcho(t *testing.T, err error, input string) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	candidates := []string{input, strings.TrimSpace(input), strings.ToUpper(strings.TrimSpace(input))}
	for _, c := range candidates {
		if len(c) >= 6 && strings.Contains(msg, c) {
			t.Errorf("error text echoes the input: %q", msg)
			return
		}
	}
	n := strings.ToUpper(strings.TrimSpace(input))
	if len(n) >= 12 && strings.Contains(msg, n[2:12]) {
		t.Errorf("error text echoes the embedded PAN: %q", msg)
	}
}

func gstinPartOf(t *testing.T, err error) GSTINPart {
	t.Helper()
	if !errors.Is(err, ErrInvalidGSTIN) {
		t.Fatalf("error %v does not match ErrInvalidGSTIN", err)
	}
	var ge *GSTINError
	if !errors.As(err, &ge) {
		t.Fatalf("error %v is not a *GSTINError", err)
	}
	return ge.Part
}

// Hand-computed golden, independent of both implementations:
// 2 9 Z Z Z P Z 0 0 0 0 Z 1 Z -> values 2 9 35 35 35 25 35 0 0 0 0 35 1 35,
// weights 1 2 1 2 ..., digit sums 2 18 35 35 35 15 35 0 0 0 0 35 1 35 = 246,
// 246 mod 36 = 30, check = 36 - 30 = 6.
func TestGSTINChecksum_HandComputedGolden(t *testing.T) {
	const golden = "29ZZZPZ0000Z1Z6"
	if got := refGSTINCheck(t, golden[:14]); got != '6' {
		t.Fatalf("reference implementation disagrees with the hand computation: %c", got)
	}
	got, err := GSTINCheckDigit(golden[:14])
	if err != nil || got != '6' {
		t.Fatalf("GSTINCheckDigit = %c, %v; want 6", got, err)
	}
	g, err := ValidateGSTIN(golden)
	if err != nil {
		t.Fatalf("ValidateGSTIN(golden): %v", err)
	}
	if g.Normalized != golden || g.StateCode != "29" || g.PAN != "ZZZPZ0000Z" ||
		g.PANHolderType != PANHolderIndividual || g.EntityCode != '1' || g.CheckDigit != '6' {
		t.Fatalf("parts = %+v", g)
	}
}

func TestValidateGSTIN_EveryAssignedStateCode(t *testing.T) {
	codes := []string{"97"}
	for i := 1; i <= 38; i++ {
		codes = append(codes, fmt.Sprintf("%02d", i))
	}
	for _, code := range codes {
		in := synthGSTIN(t, code, "ZZZCZ0000Z")
		g, err := ValidateGSTIN(in)
		if err != nil {
			t.Errorf("state %s: %v", code, err)
			continue
		}
		if g.StateCode != code || g.PAN != "ZZZCZ0000Z" || g.PANHolderType != PANHolderCompany {
			t.Errorf("state %s: parts %+v", code, g)
		}
		if _, ok := GSTStateName(code); !ok || !IsValidGSTStateCode(code) {
			t.Errorf("state %s: not in the table", code)
		}
	}
}

func TestValidateGSTIN_Normalises(t *testing.T) {
	want := synthGSTIN(t, "27", "ZZZFZ0000Z")
	for _, in := range []string{strings.ToLower(want), "  " + want + "\t", " " + strings.ToLower(want) + "\n"} {
		g, err := ValidateGSTIN(in)
		if err != nil {
			t.Errorf("ValidateGSTIN(%q): %v", in, err)
			continue
		}
		if g.Normalized != want {
			t.Errorf("Normalized = %q, want %q", g.Normalized, want)
		}
	}
}

func TestValidateGSTIN_RejectsUnassignedStateCodes(t *testing.T) {
	for _, code := range []string{"00", "39", "40", "96", "98", "99"} {
		in := synthGSTIN(t, code, "ZZZPZ0000Z")
		_, err := ValidateGSTIN(in)
		if err == nil {
			t.Errorf("state %s accepted", code)
			continue
		}
		if p := gstinPartOf(t, err); p != GSTINPartStateCode {
			t.Errorf("state %s: part %s, want STATE_CODE", code, p)
		}
		assertNoEcho(t, err, in)
		if IsValidGSTStateCode(code) {
			t.Errorf("IsValidGSTStateCode(%s) = true", code)
		}
	}
}

func TestValidateGSTIN_RejectsEveryWrongCheckCharacter(t *testing.T) {
	valid := synthGSTIN(t, "29", "ZZZHZ0000Z")
	const alpha = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	wrong := 0
	for i := 0; i < len(alpha); i++ {
		in := valid[:14] + string(alpha[i])
		_, err := ValidateGSTIN(in)
		if alpha[i] == valid[14] {
			if err != nil {
				t.Fatalf("the valid vector was refused: %v", err)
			}
			continue
		}
		wrong++
		if err == nil {
			t.Errorf("wrong check character %c accepted", alpha[i])
			continue
		}
		if p := gstinPartOf(t, err); p != GSTINPartChecksum {
			t.Errorf("check %c: part %s, want CHECKSUM", alpha[i], p)
		}
		assertNoEcho(t, err, in)
	}
	if wrong != 35 {
		t.Fatalf("tried %d wrong characters, want 35", wrong)
	}
}

func TestValidateGSTIN_FormatFailures(t *testing.T) {
	valid := synthGSTIN(t, "29", "ZZZPZ0000Z")
	cases := map[string]string{
		"fourteenth not Z":  valid[:13] + "Y" + valid[14:],
		"entity code zero":  valid[:12] + "0" + valid[13:],
		"inner space":       valid[:7] + " " + valid[8:],
		"short":             valid[:14],
		"long":              valid + "1",
		"empty":             "",
		"letter in state":   "2A" + valid[2:],
		"digit in PAN name": valid[:2] + "1" + valid[3:],
		"non-ASCII s":       valid[:2] + "ſ" + valid[3:],
		"symbol":            valid[:14] + "#",
	}
	for name, in := range cases {
		_, err := ValidateGSTIN(in)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if p := gstinPartOf(t, err); p != GSTINPartFormat {
			t.Errorf("%s: part %s, want FORMAT", name, p)
		}
		assertNoEcho(t, err, in)
	}
}

// 29ABCDE1234F1Z? carries a checksum-valid check character (computed by the
// reference implementation), but the embedded PAN's fourth character is D,
// which is not a holder type.
func TestValidateGSTIN_EmbeddedPANHolderType(t *testing.T) {
	base := "29ABCDE1234F1Z"
	in := base + string(refGSTINCheck(t, base))
	if in != "29ABCDE1234F1ZW" {
		t.Fatalf("reference checksum gives %s; the design recorded 29ABCDE1234F1ZW", in)
	}
	_, err := ValidateGSTIN(in)
	if err == nil {
		t.Fatal("bad holder type accepted")
	}
	if p := gstinPartOf(t, err); p != GSTINPartPAN {
		t.Fatalf("part %s, want PAN", p)
	}
	assertNoEcho(t, err, in)
	for _, bad := range []byte{'D', 'E', 'K', 'Z', 'X'} {
		b := "29ZZZ" + string(bad) + "Z0000Z1Z"
		in := b + string(refGSTINCheck(t, b))
		if _, err := ValidateGSTIN(in); err == nil || gstinPartOf(t, err) != GSTINPartPAN {
			t.Errorf("holder %c: err %v, want PAN", bad, err)
		}
	}
}

func TestGSTINCheckDigit_RejectsBadInput(t *testing.T) {
	for _, in := range []string{"", "29ZZZPZ0000Z1", "29ZZZPZ0000Z1Z6", "29zzzpz0000z1z", "29ZZZPZ0000Z1#"} {
		if _, err := GSTINCheckDigit(in); !errors.Is(err, ErrInvalidGSTIN) {
			t.Errorf("GSTINCheckDigit(%q) err = %v", in, err)
		}
	}
}

func TestGSTStateName(t *testing.T) {
	if n, ok := GSTStateName("29"); !ok || n != "Karnataka" {
		t.Errorf("29 = %q, %v", n, ok)
	}
	if n, ok := GSTStateName("97"); !ok || n != "Other Territory" {
		t.Errorf("97 = %q, %v", n, ok)
	}
	for _, c := range []string{"", "0", "00", "39", "99", " 29", "29 "} {
		if _, ok := GSTStateName(c); ok {
			t.Errorf("GSTStateName(%q) found", c)
		}
	}
}
