package actionlinks

import (
	"errors"
	"strings"
	"testing"
)

// The label enum is exactly the contract's eleven, in order.
func TestLabelsAreTheContractEnum(t *testing.T) {
	want := []string{"apply_now:Apply now", "learn_more:Learn more", "sign_up:Sign up", "register:Register",
		"book_now:Book now", "shop_now:Shop now", "order_now:Order now", "download:Download",
		"contact_us:Contact us", "visit_website:Visit website", "watch_more:Watch more"}
	if len(Labels) != len(want) {
		t.Fatalf("%d labels, want %d", len(Labels), len(want))
	}
	for i, l := range Labels {
		if l.Value+":"+l.Label != want[i] {
			t.Errorf("label %d = %s:%s, want %s", i, l.Value, l.Label, want[i])
		}
		if text, ok := LabelText(l.Value); !ok || text != l.Label {
			t.Errorf("LabelText(%q) = %q, %v", l.Value, text, ok)
		}
	}
	for _, bad := range []string{"", "Apply_Now", "apply now", "apply_now ", "buy_now", "Apply now"} {
		if _, ok := LabelText(bad); ok {
			t.Errorf("LabelText(%q) accepted", bad)
		}
	}
}

func TestCheckURLAccepts(t *testing.T) {
	cases := []struct{ in, url, domain string }{
		{"https://www.epfindia.gov.in/site_en/index.php", "https://www.epfindia.gov.in/site_en/index.php", "epfindia.gov.in"},
		{"  https://Example.COM/Path?q=1#frag ", "https://example.com/Path?q=1#frag", "example.com"},
		{"HTTPS://example.com", "https://example.com", "example.com"},
		{"https://example.com:443/x", "https://example.com/x", "example.com"},
		{"https://shop.example.co.uk", "https://shop.example.co.uk", "shop.example.co.uk"},
		{"https://example.com./a", "https://example.com/a", "example.com"},
		{"https://xn--80ak6aa92e.com/", "https://xn--80ak6aa92e.com/", "xn--80ak6aa92e.com"},
		{"https://example.xn--p1ai/", "https://example.xn--p1ai/", "example.xn--p1ai"},
		// wa.me/<number> is allowed (a chat with one person, not a group).
		{"https://wa.me/919876543210", "https://wa.me/919876543210", "wa.me"},
		{"https://api.whatsapp.com/send?phone=919876543210", "https://api.whatsapp.com/send?phone=919876543210", "api.whatsapp.com"},
		// Other t.me links (channels, people, bots) are allowed.
		{"https://t.me/vchatofficial", "https://t.me/vchatofficial", "t.me"},
		{"https://t.me/s/vchatofficial", "https://t.me/s/vchatofficial", "t.me"},
		{"https://telegram.me/vchatofficial", "https://telegram.me/vchatofficial", "telegram.me"},
		// A host that merely contains a shortener's name is not one.
		{"https://notbit.ly.example.com", "https://notbit.ly.example.com", "notbit.ly.example.com"},
		{"https://bit.lyx.com", "https://bit.lyx.com", "bit.lyx.com"},
		// Our own domains (in-app links) pass the rules like any site.
		{"https://cleestudio.com/@ravi", "https://cleestudio.com/@ravi", "cleestudio.com"},
		{"https://" + strings.Repeat("a", 480) + ".com", "", ""},
		{"https://" + strings.Repeat("a", 64) + ".com", "", ""},
		{"https://" + strings.Repeat(strings.Repeat("a", 60)+".", 5) + "com", "", ""},
		{"https://" + strings.Repeat("a", 63) + ".com", "https://" + strings.Repeat("a", 63) + ".com", strings.Repeat("a", 63) + ".com"},
	}
	for _, tc := range cases {
		got, err := CheckURL(tc.in)
		if tc.domain == "" {
			// A label over 63 characters (or a host over 253): refused.
			if err == nil {
				t.Errorf("CheckURL(%.40q) accepted an over-long label", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("CheckURL(%q) = %v", tc.in, err)
			continue
		}
		if got.URL != tc.url || got.Domain != tc.domain {
			t.Errorf("CheckURL(%q) = %+v, want url %q domain %q", tc.in, got, tc.url, tc.domain)
		}
	}
}

func TestCheckURLRefusesInvalid(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", MaxURLLength-len("https://example.com/")+1)
	atLimit := "https://example.com/" + strings.Repeat("a", MaxURLLength-len("https://example.com/"))
	if _, err := CheckURL(atLimit); err != nil {
		t.Fatalf("a %d-character URL is refused: %v", MaxURLLength, err)
	}
	for _, in := range []string{
		"",
		long,
		// https only.
		"http://example.com", "ftp://example.com", "javascript:alert(1)", "data:text/html,hi", "//example.com", "example.com",
		"https:example.com", "https:/example.com",
		// IP literals, in every form a browser reads as one.
		"https://127.0.0.1/", "https://8.8.8.8", "https://[::1]/", "https://[2001:db8::1]:443/",
		"https://2130706433/", "https://0x7f000001/", "https://127.1/", "https://0177.0.0.1/", "https://10.0.0.1.",
		// localhost.
		"https://localhost/", "https://localhost:443/", "https://api.localhost/", "https://LOCALHOST/",
		// userinfo.
		"https://user@example.com/", "https://user:pass@example.com/", "https://example.com@evil.com/",
		// ports other than 443.
		"https://example.com:8443/", "https://example.com:80/", "https://example.com:/", "https://example.com:0/",
		// not a real host.
		"https://example/", "https://ex_ample.com/", "https://-example.com/", "https://example-.com/",
		"https://example.c/", "https://example.123/", "https://example.xn--/", "https://exa%6dple.com/", "https://www./",
		// whitespace, control characters, backslashes.
		"https://exa mple.com/", "https://example.com/a b", "https://example.com/\x00", "https://example.com\\@evil.com",
		"https://example.com/a b", "https://example.com/a b",
	} {
		if _, err := CheckURL(in); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("CheckURL(%q) = %v, want ErrInvalidURL", in, err)
		}
	}
}

// Every shortener in the contract, bare, with www., on a subdomain and with
// a path.
func TestCheckURLRefusesEveryShortener(t *testing.T) {
	if len(Shorteners) != 15 {
		t.Fatalf("%d shorteners, want the contract's 15", len(Shorteners))
	}
	for _, s := range []string{"bit.ly", "tinyurl.com", "t.co", "goo.gl", "cutt.ly", "rb.gy", "is.gd", "ow.ly",
		"shorturl.at", "tiny.cc", "rebrand.ly", "s.id", "t.ly", "bl.ink", "buff.ly"} {
		for _, in := range []string{"https://" + s + "/abc", "https://www." + s + "/abc", "https://go." + s + "/abc", "https://" + strings.ToUpper(s)} {
			if _, err := CheckURL(in); !errors.Is(err, ErrShortener) {
				t.Errorf("CheckURL(%q) = %v, want ErrShortener", in, err)
			}
		}
	}
}

// Every group-invite pattern, refused; the neighbours, allowed.
func TestCheckURLRefusesGroupInvites(t *testing.T) {
	for _, in := range []string{
		"https://chat.whatsapp.com/AbCdEf123", "https://chat.whatsapp.com/", "https://chat.whatsapp.com",
		"https://www.chat.whatsapp.com/AbC", "https://CHAT.WHATSAPP.COM/AbC",
		"https://t.me/+AbCdEf123", "https://t.me/%2BAbCdEf123", "https://www.t.me/+AbC",
		"https://t.me/joinchat/AbCdEf", "https://t.me/JoinChat/AbCdEf", "https://t.me/joinchat",
		"https://telegram.me/joinchat/AbCdEf", "https://www.telegram.me/joinchat/AbC",
		// The same invite on Telegram's second domain.
		"https://telegram.me/+AbCdEf",
	} {
		if _, err := CheckURL(in); !errors.Is(err, ErrGroupInvite) {
			t.Errorf("CheckURL(%q) = %v, want ErrGroupInvite", in, err)
		}
	}
	for _, in := range []string{"https://t.me/joinchatbot", "https://t.me/channel/+x", "https://wa.me/+919876543210"} {
		if _, err := CheckURL(in); err != nil {
			t.Errorf("CheckURL(%q) = %v, want allowed", in, err)
		}
	}
}

func TestOwnDomains(t *testing.T) {
	own := ParseOwnDomains(" cleestudio.com, https://www.VChat.app/ , not a domain, localhost")
	if len(own) != 2 || own[0] != "cleestudio.com" || own[1] != "vchat.app" {
		t.Fatalf("ParseOwnDomains = %v", own)
	}
	for d, want := range map[string]bool{
		"cleestudio.com": true, "api-dev.cleestudio.com": true, "vchat.app": true,
		"evilcleestudio.com": false, "cleestudio.com.evil.com": false, "example.com": false,
	} {
		if got := IsOwnDomain(d, own); got != want {
			t.Errorf("IsOwnDomain(%q) = %v, want %v", d, got, want)
		}
	}
	if IsOwnDomain("cleestudio.com", nil) {
		t.Error("no own domains configured, yet one matched")
	}
}
