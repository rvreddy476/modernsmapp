package verification

import (
	"errors"
	"testing"
)

func TestValidateGSTIN_HandComputedGolden(t *testing.T) {
	// Same golden as Architecture/shared/kyc: check character 6.
	got, err := ValidateGSTIN(" 29zzzpz0000z1z6 ")
	if err != nil || got != "29ZZZPZ0000Z1Z6" {
		t.Fatalf("ValidateGSTIN = %q, %v", got, err)
	}
	if GSTINCheckDigit("29ZZZPZ0000Z1Z") != '6' {
		t.Fatal("check digit disagrees with the hand computation")
	}
}

func TestValidateGSTIN_Refusals(t *testing.T) {
	company := "29AAACZ1234Z1Z"
	valid := company + string(GSTINCheckDigit(company))
	if _, err := ValidateGSTIN(valid); err != nil {
		t.Fatalf("valid company GSTIN refused: %v", err)
	}
	wrongCheck := company + "0"
	if valid[14] == '0' {
		wrongCheck = company + "1"
	}
	stateless := "99AAACZ1234Z1Z"
	badHolder := "29ABCDE1234F1Z"
	for name, in := range map[string]string{
		"empty":        "",
		"short":        "29AAACZ1234Z1",
		"checksum":     wrongCheck,
		"state 99":     stateless + string(GSTINCheckDigit(stateless)),
		"holder D":     badHolder + string(GSTINCheckDigit(badHolder)),
		"no Z":         "29AAACZ1234Z1A" + "5",
		"lower symbol": "29AAACZ1234Z1Z#",
	} {
		if _, err := ValidateGSTIN(in); !errors.Is(err, ErrInvalidGSTIN) {
			t.Errorf("%s: err = %v, want ErrInvalidGSTIN", name, err)
		}
	}
}

func TestValidateCIN(t *testing.T) {
	got, err := ValidateCIN("l17110mh1973plc019786", 2026)
	if err != nil || got != "L17110MH1973PLC019786" {
		t.Fatalf("ValidateCIN = %q, %v", got, err)
	}
	for name, in := range map[string]string{
		"empty":        "",
		"listing X":    "X17110MH1973PLC019786",
		"future year":  "L17110MH2099PLC019786",
		"ancient year": "L17110MH1700PLC019786",
		"ownership":    "L17110MH1973ZZZ019786",
		"short":        "L17110MH1973PLC01978",
	} {
		if _, err := ValidateCIN(in, 2026); !errors.Is(err, ErrInvalidCIN) {
			t.Errorf("%s: err = %v, want ErrInvalidCIN", name, err)
		}
	}
}

func TestOrgNamesMatch(t *testing.T) {
	for _, pair := range [][2]string{
		{"Acme Private Limited", "ACME Pvt. Ltd."},
		{"Tata Sons Pvt Ltd", "tata sons"},
		{"Café Coffee Day", "CAFE COFFEE DAY LIMITED"},
		{"Johnson & Johnson", "Johnson and Johnson"},
	} {
		if !OrgNamesMatch(pair[0], pair[1]) {
			t.Errorf("%q and %q should match", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{"Acme Private Limited", "Acme Holdings Limited"},
		{"Tata Sons", "Sons Tata"},
		{"Limited", "Ltd"}, // nothing left: never a match
		{"", ""},
	} {
		if OrgNamesMatch(pair[0], pair[1]) {
			t.Errorf("%q and %q must not match", pair[0], pair[1])
		}
	}
}

func TestNormaliseDomain(t *testing.T) {
	for in, want := range map[string]string{
		"Example.COM":        "example.com",
		" acme.co.in. ":      "acme.co.in",
		"xn--80ak6aa92e.com": "xn--80ak6aa92e.com",
	} {
		if got, err := NormaliseDomain(in); err != nil || got != want {
			t.Errorf("NormaliseDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "localhost", "10.0.0.1", "acme.com:443", "acme.com/x", "-acme.com", "acme..com", "acme.c0m", "a.b"} {
		if _, err := NormaliseDomain(in); err == nil {
			t.Errorf("NormaliseDomain(%q) accepted", in)
		}
	}
}

func TestIsFreeMailDomain(t *testing.T) {
	for _, d := range []string{"gmail.com", "yahoo.co.in", "mail.gmail.com", "proton.me"} {
		if !IsFreeMailDomain(d) {
			t.Errorf("%s should be free mail", d)
		}
	}
	for _, d := range []string{"acme.com", "gmail.com.acme.in", "notgmail.com", "isro.gov.in"} {
		if IsFreeMailDomain(d) {
			t.Errorf("%s is not free mail", d)
		}
	}
}

func TestSplitEmail(t *testing.T) {
	local, domain, err := SplitEmail(" Signatory.Name@Acme.CO.in ")
	if err != nil || local != "Signatory.Name" || domain != "acme.co.in" {
		t.Fatalf("SplitEmail = %q %q %v", local, domain, err)
	}
	for _, in := range []string{"", "a@b@acme.com", "Name <a@acme.com>", "a b@acme.com", "a@acme.com\r\nBcc: x@y.com", ".a@acme.com", "a..b@acme.com", "a@localhost", "@acme.com"} {
		if _, _, err := SplitEmail(in); err == nil {
			t.Errorf("SplitEmail(%q) accepted", in)
		}
	}
}
