// Package actionlinks holds the rules of the Action button (pinned contract
// 23 Oct 2026): the fixed labels, the URL rules every server write enforces,
// the Google Web Risk adapter, and the identity reads behind the
// eligibility check. It has no dependency on the rest of post-service, so
// the rules are testable on their own and the service only wires them.
//
// The contract, in one place:
//
//   - labels: a closed enum (Labels); anything else is INVALID_ACTION_LABEL;
//   - URL: https only, <= 500 characters, a real host (no IP literal, no
//     localhost, no user@), port 443 or none; link shorteners are refused
//     (ACTION_LINK_SHORTENER) and WhatsApp / Telegram GROUP invites are
//     refused (ACTION_LINK_GROUP_INVITE) — wa.me/<number> and other t.me
//     links are allowed; our own domains are allowed;
//   - safety: Google Web Risk (MALWARE, SOCIAL_ENGINEERING,
//     UNWANTED_SOFTWARE); an unsafe verdict is ACTION_LINK_UNSAFE.
package actionlinks

// Label is one fixed Action-button label: the wire value and the text
// clients show.
type Label struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Labels is the closed enum, in the order the composer offers them.
// GET /v1/posts/action-labels answers exactly this list.
var Labels = []Label{
	{"apply_now", "Apply now"},
	{"learn_more", "Learn more"},
	{"sign_up", "Sign up"},
	{"register", "Register"},
	{"book_now", "Book now"},
	{"shop_now", "Shop now"},
	{"order_now", "Order now"},
	{"download", "Download"},
	{"contact_us", "Contact us"},
	{"visit_website", "Visit website"},
	{"watch_more", "Watch more"},
}

var labelText = func() map[string]string {
	m := make(map[string]string, len(Labels))
	for _, l := range Labels {
		m[l.Value] = l.Label
	}
	return m
}()

// LabelText is the shown text of a wire value, and whether the value is one
// of the enum. Matching is exact: "Apply_Now" is not a label.
func LabelText(value string) (string, bool) {
	t, ok := labelText[value]
	return t, ok
}
