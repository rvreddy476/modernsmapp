package actionlinks

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxURLLength caps an action URL (characters), as migration 072's CHECK.
const MaxURLLength = 500

var (
	// ErrInvalidURL: not an https URL with a real host, port 443 or none,
	// no user info, at most MaxURLLength characters (400 INVALID_ACTION_URL).
	ErrInvalidURL = errors.New("url must be an https link to a real website (no IP address, localhost, user@ or port), at most 500 characters")
	// ErrShortener: the host is a link shortener (400 ACTION_LINK_SHORTENER).
	// A shortened link hides where it goes, so neither the viewer's
	// interstitial nor the safety check can name the real destination.
	ErrShortener = errors.New("link shorteners are not allowed; use the full address")
	// ErrGroupInvite: a WhatsApp or Telegram group invite
	// (400 ACTION_LINK_GROUP_INVITE). wa.me/<number> and other t.me links
	// are fine.
	ErrGroupInvite = errors.New("WhatsApp and Telegram group invite links are not allowed")
)

// Shorteners are the blocked hosts (and their subdomains). Pinned by the
// contract; extending it is a contract change.
var Shorteners = []string{
	"bit.ly", "tinyurl.com", "t.co", "goo.gl", "cutt.ly", "rb.gy", "is.gd", "ow.ly",
	"shorturl.at", "tiny.cc", "rebrand.ly", "s.id", "t.ly", "bl.ink", "buff.ly",
}

// Checked is a URL that passed every rule: the normalised URL (scheme and
// host lower-cased, a default :443 dropped) and its domain — the lower-case
// host without a leading "www.", which is what viewers are shown.
type Checked struct {
	URL    string
	Host   string
	Domain string
}

// CheckURL applies the URL rules. The error is one of ErrInvalidURL,
// ErrShortener, ErrGroupInvite.
func CheckURL(raw string) (Checked, error) {
	v := strings.TrimSpace(raw)
	if v == "" || utf8.RuneCountInString(v) > MaxURLLength || !utf8.ValidString(v) {
		return Checked{}, ErrInvalidURL
	}
	for _, r := range v {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' {
			return Checked{}, ErrInvalidURL
		}
	}
	// "https:host" parses as opaque and "https:/host" with an empty host,
	// both refused below; "//host" has no scheme.
	u, err := url.Parse(v)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil {
		return Checked{}, ErrInvalidURL
	}
	if port := u.Port(); port != "" && port != "443" {
		return Checked{}, ErrInvalidURL
	}
	// A bare "https://host:" (empty port) is refused too: it is not a
	// canonical address and some parsers read it differently.
	if strings.HasSuffix(u.Host, ":") {
		return Checked{}, ErrInvalidURL
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if !realHost(host) {
		return Checked{}, ErrInvalidURL
	}
	domain := strings.TrimPrefix(host, "www.")
	if domain == "" || !realHost(domain) {
		return Checked{}, ErrInvalidURL
	}
	if isShortener(domain) {
		return Checked{}, ErrShortener
	}
	if isGroupInvite(domain, u.Path) {
		return Checked{}, ErrGroupInvite
	}
	u.Scheme = "https"
	u.Host = host
	return Checked{URL: u.String(), Host: host, Domain: domain}, nil
}

// realHost is a public DNS name. IP literals never pass: an IPv6 literal
// keeps its colons ([::1] -> ::1), which no label may contain, and every
// IPv4 form ends in a numeric label, which the letter-TLD rule refuses.
// So: not an IP literal (any form a browser
// would read as one — dotted, integer, hex, octal), not localhost, at least
// two labels, each 1–63 letters, digits or hyphens (not at either end), and
// a top-level label that is letters (or an xn-- IDN label). Numeric-only
// TLDs are what makes "2130706433", "0x7f.1" and "127.1" IP addresses to a
// browser, so requiring a letter TLD refuses all of them.
func realHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || utf8.RuneCountInString(l) > 63 || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return false
		}
		for _, r := range l {
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	if strings.HasPrefix(tld, "xn--") {
		return true // "xn--" alone ends in a hyphen, refused above
	}
	if utf8.RuneCountInString(tld) < 2 {
		return false
	}
	for _, r := range tld {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func isShortener(domain string) bool {
	for _, s := range Shorteners {
		if domain == s || strings.HasSuffix(domain, "."+s) {
			return true
		}
	}
	return false
}

// isGroupInvite: chat.whatsapp.com/* (every path is an invite), t.me/+…,
// t.me/joinchat/…, telegram.me/joinchat/… — and telegram.me/+…, the same
// invite on Telegram's other domain. path is the decoded path, so a
// percent-encoded "+" (%2B) is caught too.
func isGroupInvite(domain, path string) bool {
	if domain == "chat.whatsapp.com" {
		return true
	}
	if domain != "t.me" && domain != "telegram.me" {
		return false
	}
	p := strings.ToLower(path)
	if strings.HasPrefix(p, "/+") {
		return true
	}
	return p == "/joinchat" || strings.HasPrefix(p, "/joinchat/")
}

// IsOwnDomain reports whether domain is one of own or a subdomain of one.
// own entries are lower-case domains without "www." (OwnDomainsFromEnv).
func IsOwnDomain(domain string, own []string) bool {
	for _, d := range own {
		if d != "" && (domain == d || strings.HasSuffix(domain, "."+d)) {
			return true
		}
	}
	return false
}

// ParseOwnDomains reads a comma-separated list ("cleestudio.com, vchat.app"),
// lower-cased, "www." and any scheme dropped, invalid entries skipped.
func ParseOwnDomains(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		d := strings.ToLower(strings.TrimSpace(part))
		d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
		d = strings.TrimSuffix(strings.TrimPrefix(d, "www."), "/")
		if realHost(d) {
			out = append(out, d)
		}
	}
	return out
}
