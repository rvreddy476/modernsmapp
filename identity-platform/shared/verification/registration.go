package verification

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Organisation registration checks for the Official mark: GSTIN and CIN
// format, the legal-name comparison against a registry, and the official
// e-mail domain rules.
//
// The GSTIN check is a port of Architecture/shared/kyc/gstin.go (that module
// cannot be imported from the identity platform's GOWORK=off build). Keep the
// two in step. These are FORMAT checks; whether the registration exists,
// is active and carries the name is the RegistryVerifier's job.

// Registration kinds.
const (
	RegistrationGSTIN = "gstin"
	RegistrationCIN   = "cin"
)

var (
	// ErrInvalidGSTIN is any GSTIN format, state-code, PAN or checksum failure.
	ErrInvalidGSTIN = errors.New("INVALID_GSTIN")
	// ErrInvalidCIN is any CIN format failure.
	ErrInvalidCIN = errors.New("INVALID_CIN")
)

var gstinPattern = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)

const gstinAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// gstStateCodes are the assigned GST state and territory codes (01..38 and
// 97). 99 (Centre Jurisdiction) is refused, as in shared/kyc.
var gstStateCodes = func() map[string]bool {
	m := map[string]bool{"97": true}
	for i := 1; i <= 38; i++ {
		m[twoDigits(i)] = true
	}
	return m
}()

func twoDigits(i int) string {
	if i < 10 {
		return "0" + strconv.Itoa(i)
	}
	return strconv.Itoa(i)
}

// panHolderTypes are the ten PAN holder-type characters (fourth PAN letter).
const panHolderTypes = "PCHFATBLJG"

// NormaliseRegistration trims, upper-cases and drops spaces and hyphens.
func NormaliseRegistration(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "", "-", "").Replace(s)
}

// ValidateGSTIN checks layout, state code, embedded PAN holder type and the
// mod-36 check character; it returns the normalised GSTIN.
func ValidateGSTIN(s string) (string, error) {
	n := NormaliseRegistration(s)
	if !gstinPattern.MatchString(n) {
		return "", ErrInvalidGSTIN
	}
	if !gstStateCodes[n[:2]] {
		return "", ErrInvalidGSTIN
	}
	if !strings.ContainsRune(panHolderTypes, rune(n[5])) {
		return "", ErrInvalidGSTIN
	}
	if GSTINCheckDigit(n[:14]) != n[14] {
		return "", ErrInvalidGSTIN
	}
	return n, nil
}

// GSTINCheckDigit computes the fifteenth GSTIN character for the first
// fourteen (upper-case 0-9 / A-Z; anything else yields 0).
func GSTINCheckDigit(first14 string) byte {
	if len(first14) != 14 {
		return 0
	}
	sum := 0
	for i := 0; i < 14; i++ {
		v := strings.IndexByte(gstinAlphabet, first14[i])
		if v < 0 {
			return 0
		}
		p := v * (1 + i%2)
		sum += p/36 + p%36
	}
	return gstinAlphabet[(36-sum%36)%36]
}

// cinPattern is the Ministry of Corporate Affairs Corporate Identification
// Number: listing status (L/U), five-digit industry code, two-letter state,
// four-digit year of incorporation, three-letter ownership class and a
// six-digit registration number.
var cinPattern = regexp.MustCompile(`^[LU][0-9]{5}[A-Z]{2}([0-9]{4})([A-Z]{3})[0-9]{6}$`)

// cinOwnership are the ownership classes MCA issues.
var cinOwnership = map[string]bool{
	"PLC": true, "PTC": true, "GOI": true, "SGC": true, "FLC": true, "FTC": true,
	"GAP": true, "GAT": true, "NPL": true, "ULL": true, "ULT": true, "OPC": true,
}

// ValidateCIN checks a CIN's layout, ownership class and a plausible year
// (1850 to maxYear); it returns the normalised CIN.
func ValidateCIN(s string, maxYear int) (string, error) {
	n := NormaliseRegistration(s)
	m := cinPattern.FindStringSubmatch(n)
	if m == nil {
		return "", ErrInvalidCIN
	}
	year, _ := strconv.Atoi(m[1])
	if year < 1850 || year > maxYear {
		return "", ErrInvalidCIN
	}
	if !cinOwnership[m[2]] {
		return "", ErrInvalidCIN
	}
	return n, nil
}

// corporateSuffixWords are dropped when two legal names are compared:
// "Acme Private Limited" and "ACME Pvt. Ltd." are one registration name.
var corporateSuffixWords = map[string]bool{
	"limited": true, "ltd": true, "private": true, "pvt": true, "pvtltd": true, "llp": true,
	"inc": true, "incorporated": true, "corporation": true, "corp": true, "co": true,
	"company": true, "plc": true, "opc": true, "the": true,
}

// NormaliseOrgName reduces an organisation name to a comparable form:
// compatibility-decomposed, Latin accents dropped, lower-cased, "&" read as
// "and", split on anything that is not a letter or digit, corporate suffix
// words removed, joined by single spaces. Word order is kept: a registry
// name is one exact string.
func NormaliseOrgName(name string) string {
	name = strings.ReplaceAll(name, "&", " and ")
	var b strings.Builder
	lastLatin := false
	for _, r := range norm.NFKD.String(name) {
		if unicode.Is(unicode.Mn, r) && lastLatin {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
		lastLatin = r < 0x250 && unicode.IsLetter(r)
	}
	tokens := strings.FieldsFunc(b.String(), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r))
	})
	kept := tokens[:0]
	for _, t := range tokens {
		if !corporateSuffixWords[t] {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, " ")
}

// OrgNamesMatch reports whether two organisation names normalise to the
// same non-empty form.
func OrgNamesMatch(a, b string) bool {
	na := NormaliseOrgName(a)
	return na != "" && na == NormaliseOrgName(b)
}

// ErrInvalidDomain is a domain that is not a public hostname.
var ErrInvalidDomain = errors.New("INVALID_DOMAIN")

var domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormaliseDomain lower-cases and trims a domain (one trailing dot allowed)
// and checks it is a public hostname: at least two labels, LDH labels of at
// most 63 characters, an alphabetic (or IDN "xn--") top-level label, at
// most 253 characters. No IP addresses, ports, paths or "localhost".
func NormaliseDomain(d string) (string, error) {
	d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
	if d == "" || len(d) > 253 {
		return "", ErrInvalidDomain
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return "", ErrInvalidDomain
	}
	for _, l := range labels {
		if !domainLabel.MatchString(l) {
			return "", ErrInvalidDomain
		}
	}
	tld := labels[len(labels)-1]
	if !strings.HasPrefix(tld, "xn--") {
		for _, r := range tld {
			if r < 'a' || r > 'z' {
				return "", ErrInvalidDomain
			}
		}
		if len(tld) < 2 {
			return "", ErrInvalidDomain
		}
	}
	return d, nil
}

// freeMailDomains are consumer mailbox providers. An address there proves
// control of a mailbox, never of an organisation.
var freeMailDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "yahoo.com": true, "yahoo.co.in": true, "yahoo.in": true,
	"yahoo.co.uk": true, "ymail.com": true, "rocketmail.com": true, "outlook.com": true, "outlook.in": true,
	"hotmail.com": true, "hotmail.co.uk": true, "hotmail.in": true, "live.com": true, "live.in": true,
	"msn.com": true, "icloud.com": true, "me.com": true, "mac.com": true, "aol.com": true,
	"protonmail.com": true, "proton.me": true, "pm.me": true, "tutanota.com": true, "tuta.io": true,
	"zoho.com": true, "zohomail.com": true, "zohomail.in": true, "yandex.com": true, "yandex.ru": true,
	"mail.com": true, "gmx.com": true, "gmx.net": true, "gmx.de": true, "mail.ru": true,
	"rediffmail.com": true, "rediff.com": true, "fastmail.com": true, "hey.com": true,
	"qq.com": true, "163.com": true, "126.com": true, "sify.com": true, "inbox.com": true,
	"duck.com": true, "mailinator.com": true, "guerrillamail.com": true, "10minutemail.com": true,
}

// IsFreeMailDomain reports whether domain (normalised) is, or is under, a
// consumer mailbox provider.
func IsFreeMailDomain(domain string) bool {
	for d := domain; d != ""; {
		if freeMailDomains[d] {
			return true
		}
		i := strings.IndexByte(d, '.')
		if i < 0 {
			break
		}
		d = d[i+1:]
	}
	return false
}

// ErrInvalidEmail is an address that cannot be one plain mailbox.
var ErrInvalidEmail = errors.New("INVALID_EMAIL")

// SplitEmail returns the trimmed local part and the normalised domain of a
// single plain address. Display names, comments, quoted local parts and any
// whitespace or control character are refused.
func SplitEmail(addr string) (local, domain string, err error) {
	addr = strings.TrimSpace(addr)
	if len(addr) > 254 || strings.Count(addr, "@") != 1 {
		return "", "", ErrInvalidEmail
	}
	for _, r := range addr {
		if r <= ' ' || r == 0x7f || r == '"' || r == '<' || r == '>' || r == '(' || r == ')' || r == ',' || r == ';' || r == '\\' {
			return "", "", ErrInvalidEmail
		}
	}
	at := strings.IndexByte(addr, '@')
	local = addr[:at]
	if local == "" || len(local) > 64 || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return "", "", ErrInvalidEmail
	}
	domain, err = NormaliseDomain(addr[at+1:])
	if err != nil {
		return "", "", ErrInvalidEmail
	}
	return local, domain, nil
}
