package kyc

// gstStateNames is the GST state and union-territory code table: the first
// two characters of every GSTIN.
//
// 01..38 are states and union territories. 97 is "Other Territory". 00, 39..96
// and 98 are not assigned. 99 is the Centre Jurisdiction code; it is refused
// here on purpose, because no restaurant, rider or seller onboarding this
// platform performs registers under it.
//
// 25 (Daman and Diu) and 28 (Andhra Pradesh before reorganisation) are kept:
// GSTINs issued under them may still be on record. Whether a NEW
// registration can still carry them is a question for the tax adviser, not
// something this table decides.
var gstStateNames = map[string]string{
	"01": "Jammu and Kashmir",
	"02": "Himachal Pradesh",
	"03": "Punjab",
	"04": "Chandigarh",
	"05": "Uttarakhand",
	"06": "Haryana",
	"07": "Delhi",
	"08": "Rajasthan",
	"09": "Uttar Pradesh",
	"10": "Bihar",
	"11": "Sikkim",
	"12": "Arunachal Pradesh",
	"13": "Nagaland",
	"14": "Manipur",
	"15": "Mizoram",
	"16": "Tripura",
	"17": "Meghalaya",
	"18": "Assam",
	"19": "West Bengal",
	"20": "Jharkhand",
	"21": "Odisha",
	"22": "Chhattisgarh",
	"23": "Madhya Pradesh",
	"24": "Gujarat",
	"25": "Daman and Diu",
	"26": "Dadra and Nagar Haveli and Daman and Diu",
	"27": "Maharashtra",
	"28": "Andhra Pradesh (before reorganisation)",
	"29": "Karnataka",
	"30": "Goa",
	"31": "Lakshadweep",
	"32": "Kerala",
	"33": "Tamil Nadu",
	"34": "Puducherry",
	"35": "Andaman and Nicobar Islands",
	"36": "Telangana",
	"37": "Andhra Pradesh",
	"38": "Ladakh",
	"97": "Other Territory",
}

// GSTStateName returns the name for a two-digit GST state code. The code
// must be exactly the two characters; it is not trimmed.
func GSTStateName(code string) (string, bool) {
	name, found := gstStateNames[code]
	return name, found
}

// IsValidGSTStateCode reports whether code is an assigned GST state or
// territory code (01..38 or 97).
func IsValidGSTStateCode(code string) bool {
	_, ok := gstStateNames[code]
	return ok
}

// rtoStateCodes are the two-letter state and territory prefixes used on
// driving licences and vehicle registrations. Both spellings are kept where
// a state changed its code (OR/OD, UA/UK, TS/TG) because documents issued
// under the old code remain valid.
var rtoStateCodes = map[string]bool{
	"AN": true, "AP": true, "AR": true, "AS": true, "BR": true, "CG": true,
	"CH": true, "DD": true, "DL": true, "DN": true, "GA": true, "GJ": true,
	"HP": true, "HR": true, "JH": true, "JK": true, "KA": true, "KL": true,
	"LA": true, "LD": true, "MH": true, "ML": true, "MN": true, "MP": true,
	"MZ": true, "NL": true, "OD": true, "OR": true, "PB": true, "PY": true,
	"RJ": true, "SK": true, "TN": true, "TR": true, "TS": true, "TG": true,
	"UK": true, "UA": true, "UP": true, "WB": true,
}

// normalizeASCII trims surrounding whitespace, drops every byte listed in
// drop, and upper-cases a-z. It refuses any non-ASCII byte rather than
// letting strings.ToUpper fold a look-alike rune (U+017F, U+0131) into a
// valid ASCII identifier.
func normalizeASCII(s, drop string) (string, bool) {
	s = trimSpaceASCII(s)
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 {
			return "", false
		}
		if drop != "" && indexByte(drop, c) >= 0 {
			continue
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out), true
}

func trimSpaceASCII(s string) string {
	start, end := 0, len(s)
	for start < end && isSpaceASCII(s[start]) {
		start++
	}
	for end > start && isSpaceASCII(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpaceASCII(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
