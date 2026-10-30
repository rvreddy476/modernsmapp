// Package qarender turns a question or answer body into the HTML qa-service
// serves as body_html (Q&A audit H3, 30 Oct 2026).
//
// The server never stores or serves HTML a client wrote. Clients used to send
// body_html and qa-service stored it verbatim; the web renders body_html with
// dangerouslySetInnerHTML, so any <img onerror> or <script> in it ran for
// every reader on the postbook origin. No HTML sanitiser is vendored in this
// repository, so instead of sanitising client HTML the server renders
// body_html itself from the plain-text body, on write (the stored column) and
// again on every read (rows stored before this change keep whatever a client
// sent in the column; it is never served).
//
// The output is a strict allow-list by construction: every character of the
// body is HTML-escaped, and the only markup is <p>, <br> and <a> for http(s)
// URLs, with href escaped and rel="nofollow ugc noopener noreferrer".
package qarender

import (
	"html"
	"net/url"
	"regexp"
	"strings"
)

// LinkRel is the rel attribute of every link the renderer emits.
const LinkRel = "nofollow ugc noopener noreferrer"

// MaxLinkLength bounds one autolinked URL; a longer run stays plain text.
const MaxLinkLength = 2048

var (
	paragraphBreak = regexp.MustCompile(`\n[ \t]*\n+`)
	// urlCandidate finds http(s) URLs in raw text. The match is checked again
	// with net/url before it becomes a link.
	urlCandidate = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"'\x60]+`)
)

// BodyHTML renders body (plain text) as safe HTML. An empty or blank body
// renders as "".
func BodyHTML(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	if strings.TrimSpace(body) == "" {
		return ""
	}
	var b strings.Builder
	for _, para := range paragraphBreak.Split(strings.Trim(body, "\n"), -1) {
		para = strings.Trim(para, "\n")
		if strings.TrimSpace(para) == "" {
			continue
		}
		b.WriteString("<p>")
		for i, line := range strings.Split(para, "\n") {
			if i > 0 {
				b.WriteString("<br>")
			}
			writeLine(&b, line)
		}
		b.WriteString("</p>")
	}
	return b.String()
}

// writeLine escapes one line, turning safe http(s) URLs into links.
func writeLine(b *strings.Builder, line string) {
	last := 0
	for _, m := range urlCandidate.FindAllStringIndex(line, -1) {
		start, end := m[0], m[1]
		// Trailing punctuation belongs to the sentence, not the URL.
		for end > start && strings.ContainsRune(".,;:!?)]}", rune(line[end-1])) {
			end--
		}
		raw := line[start:end]
		if !linkable(raw) {
			continue
		}
		b.WriteString(html.EscapeString(line[last:start]))
		esc := html.EscapeString(raw)
		b.WriteString(`<a href="`)
		b.WriteString(esc)
		b.WriteString(`" rel="` + LinkRel + `">`)
		b.WriteString(esc)
		b.WriteString("</a>")
		last = end
	}
	b.WriteString(html.EscapeString(line[last:]))
}

// linkable reports whether raw is an absolute http(s) URL with a host.
func linkable(raw string) bool {
	if len(raw) > MaxLinkLength {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != "" && u.User == nil
}
