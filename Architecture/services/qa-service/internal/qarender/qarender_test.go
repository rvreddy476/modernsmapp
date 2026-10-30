package qarender

import (
	"regexp"
	"strings"
	"testing"
)

// allowedTag matches every tag the renderer may emit; anything else in the
// output is an injected element.
var anyTag = regexp.MustCompile(`<[^>]*>`)
var hrefValue = regexp.MustCompile(`href="([^"]*)"`)
var allowedTag = regexp.MustCompile(`^(<p>|</p>|<br>|</a>|<a href="[^"<>]*" rel="` + LinkRel + `">)$`)

func assertOnlyAllowedMarkup(t *testing.T, in, out string) {
	t.Helper()
	for _, tag := range anyTag.FindAllString(out, -1) {
		if !allowedTag.MatchString(tag) {
			t.Fatalf("BodyHTML(%q) emitted a tag outside the allow-list: %q in %q", in, tag, out)
		}
	}
	for _, m := range hrefValue.FindAllStringSubmatch(out, -1) {
		if !strings.HasPrefix(m[1], "http://") && !strings.HasPrefix(m[1], "https://") {
			t.Fatalf("BodyHTML(%q) linked a non-http URL: %q", in, m[1])
		}
	}
}

// H3: no script, event handler or foreign element survives a round trip.
func TestBodyHTMLNeverEmitsActiveMarkup(t *testing.T) {
	attacks := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror="fetch('/v1/qa/questions/x/vote',{method:'POST'})">`,
		`<a href="javascript:alert(1)">click</a>`,
		`javascript:alert(1)`,
		`https://evil.example/"onmouseover="alert(1)`,
		`https://evil.example/<script>alert(1)</script>`,
		`<svg onload=alert(1)>`,
		"<p>already html</p>\n\n<iframe src=//evil></iframe>",
		`"><img src=x onerror=alert(1)>`,
		`<style>body{display:none}</style>`,
		"https://user:pass@evil.example/login",
		"data:text/html,<script>alert(1)</script>",
	}
	for _, in := range attacks {
		out := BodyHTML(in)
		assertOnlyAllowedMarkup(t, in, out)
		if strings.Contains(out, "<script") || strings.Contains(out, "<img") || strings.Contains(out, "<svg") || strings.Contains(out, "<iframe") {
			t.Fatalf("BodyHTML(%q) = %q", in, out)
		}
	}
}

func TestBodyHTMLEscapesText(t *testing.T) {
	got := BodyHTML(`a < b && "c" > 'd'`)
	want := `<p>a &lt; b &amp;&amp; &#34;c&#34; &gt; &#39;d&#39;</p>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBodyHTMLParagraphsAndLines(t *testing.T) {
	got := BodyHTML("first line\r\nsecond line\n\n\nnext para\n")
	want := `<p>first line<br>second line</p><p>next para</p>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if BodyHTML("  \n\n ") != "" {
		t.Fatal("a blank body must render empty")
	}
}

func TestBodyHTMLLinksOnlyHTTP(t *testing.T) {
	got := BodyHTML("see https://example.com/a?b=1&c=2. or ftp://x.example")
	want := `<p>see <a href="https://example.com/a?b=1&amp;c=2" rel="` + LinkRel + `">https://example.com/a?b=1&amp;c=2</a>. or ftp://x.example</p>`
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if strings.Contains(BodyHTML("https://user:pw@evil.example/"), "<a ") {
		t.Fatal("a URL with credentials must not become a link")
	}
	long := "https://example.com/" + strings.Repeat("a", MaxLinkLength)
	if strings.Contains(BodyHTML(long), "<a ") {
		t.Fatal("an over-long URL must stay text")
	}
}
