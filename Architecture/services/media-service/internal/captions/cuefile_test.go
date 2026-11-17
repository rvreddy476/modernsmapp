package captions

import (
	"errors"
	"strings"
	"testing"
)

func wantFileErr(t *testing.T, err error, code string) {
	t.Helper()
	var fe *FileError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want a *FileError with code %s", err, code)
	}
	if fe.Code != code {
		t.Fatalf("code = %s (%v), want %s", fe.Code, fe, code)
	}
}

func TestParseWebVTTMultiCueRoundTrip(t *testing.T) {
	src := utf8BOM + "WEBVTT - a title\r\nKind: captions\r\n\r\n" +
		"NOTE written by hand\r\nsecond note line\r\n\r\n" +
		"STYLE\r\n::cue { color: red; background: url(javascript:alert(1)) }\r\n\r\n" +
		"intro\r\n00:01.000 --> 00:02.500 align:start line:0\r\nHello <i>there</i>\r\nsecond line\r\n\r\n" +
		"00:00:02.500 --> 00:00:04.000\r\nGeneral Kenobi\r\n\r\n" +
		"3\r\n01:02:03.004 --> 01:02:05.000\r\nlater\r\n"
	cues, err := ParseWebVTT(src)
	if err != nil {
		t.Fatalf("ParseWebVTT: %v", err)
	}
	want := []Cue{
		{StartMs: 1000, EndMs: 2500, Text: "Hello <i>there</i>\nsecond line", Markup: true},
		{StartMs: 2500, EndMs: 4000, Text: "General Kenobi", Markup: true},
		{StartMs: 3_723_004, EndMs: 3_725_000, Text: "later", Markup: true},
	}
	if len(cues) != len(want) {
		t.Fatalf("cues = %+v, want %+v", cues, want)
	}
	for i := range want {
		if cues[i] != want[i] {
			t.Fatalf("cue %d = %+v, want %+v", i, cues[i], want[i])
		}
	}
	served := RenderWebVTT(cues)
	wantServed := "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello <i>there</i>\nsecond line\n" +
		"\n00:00:02.500 --> 00:00:04.000\nGeneral Kenobi\n" +
		"\n01:02:03.004 --> 01:02:05.000\nlater\n"
	if served != wantServed {
		t.Fatalf("served\n got %q\nwant %q", served, wantServed)
	}
	for _, gone := range []string{"NOTE", "STYLE", "javascript", "align:", "intro", "title"} {
		if strings.Contains(served, gone) {
			t.Fatalf("served file still carries %q: %q", gone, served)
		}
	}
	// The stored (compact) form parses back to the same cues.
	again, err := ParseWebVTT(RenderCompactWebVTT(cues))
	if err != nil {
		t.Fatalf("re-parse stored form: %v", err)
	}
	for i := range want {
		if again[i] != want[i] {
			t.Fatalf("re-parsed cue %d = %+v, want %+v", i, again[i], want[i])
		}
	}
}

func TestParseSRTConverts(t *testing.T) {
	src := "1\r\n00:00:01,000 --> 00:00:02,500\r\nHello <font color=\"red\">world</font>\r\n\r\n" +
		"2\r\n00:00:03,000 --> 00:00:04,000\r\n<b>bold</b> & more\r\n"
	cues, err := ParseSRT(src)
	if err != nil {
		t.Fatalf("ParseSRT: %v", err)
	}
	got := RenderWebVTT(cues)
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello world\n" +
		"\n00:00:03.000 --> 00:00:04.000\n<b>bold</b> &amp; more\n"
	if got != want {
		t.Fatalf("converted\n got %q\nwant %q", got, want)
	}
}

func TestParseWebVTTRefusesMalformed(t *testing.T) {
	for _, tc := range []struct {
		name, src, code string
	}{
		{"no header", "00:00:01.000 --> 00:00:02.000\nhi\n", CodeCaptionHeader},
		{"header lookalike", "WEBVTTX\n\n00:00:01.000 --> 00:00:02.000\nhi\n", CodeCaptionHeader},
		{"plain transcript", "just some words", CodeCaptionHeader},
		{"cue glued to header", "WEBVTT\n00:00:01.000 --> 00:00:02.000\nhi\n", CodeCaptionHeader},
		{"empty", "  \n", CodeCaptionEmpty},
		{"header only", "WEBVTT\n\n", CodeCaptionEmpty},
		{"only empty cues", "WEBVTT\n\n00:01.000 --> 00:02.000\n<v Bob></v>\n", CodeCaptionEmpty},
		{"end before start", "WEBVTT\n\n00:00:02.000 --> 00:00:01.000\nhi\n", CodeCaptionTiming},
		{"zero length", "WEBVTT\n\n00:00:02.000 --> 00:00:02.000\nhi\n", CodeCaptionTiming},
		{"negative start", "WEBVTT\n\n-00:00:01.000 --> 00:00:02.000\nhi\n", CodeCaptionTiming},
		{"bad seconds", "WEBVTT\n\n00:00:61.000 --> 00:01:02.000\nhi\n", CodeCaptionTiming},
		{"bad minutes", "WEBVTT\n\n00:60:00.000 --> 01:00:02.000\nhi\n", CodeCaptionTiming},
		{"short millis", "WEBVTT\n\n00:00:01.00 --> 00:00:02.000\nhi\n", CodeCaptionTiming},
		{"comma in vtt", "WEBVTT\n\n00:00:01,000 --> 00:00:02.000\nhi\n", CodeCaptionTiming},
		{"no end", "WEBVTT\n\n00:00:01.000 -->\nhi\n", CodeCaptionTiming},
		{"past 48 hours", "WEBVTT\n\n47:59:59.000 --> 48:00:00.001\nhi\n", CodeCaptionTiming},
		{"overlap", "WEBVTT\n\n00:00:01.000 --> 00:00:03.000\na\n\n00:00:02.000 --> 00:00:04.000\nb\n", CodeCaptionOverlap},
		{"out of order", "WEBVTT\n\n00:00:05.000 --> 00:00:06.000\na\n\n00:00:01.000 --> 00:00:02.000\nb\n", CodeCaptionOverlap},
		{"missing timing", "WEBVTT\n\nhello\nthere\n", CodeCaptionCue},
		{"missing blank line between cues", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\na\n00:00:02.000 --> 00:00:03.000\nb\n", CodeCaptionCue},
		{"invalid utf8", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n\xff\xfe\n", CodeCaptionEncoding},
		{"nul byte", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\na\x00b\n", CodeCaptionEncoding},
		{"oversize", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n" + strings.Repeat("x", MaxCaptionFileBytes), CodeCaptionTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cues, err := ParseWebVTT(tc.src)
			if err == nil {
				t.Fatalf("accepted: %+v", cues)
			}
			wantFileErr(t, err, tc.code)
		})
	}
}

func TestParseWebVTTCueCountCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("WEBVTT\n")
	for i := 0; i <= MaxCaptionCues; i++ {
		b.WriteString("\n")
		b.WriteString(formatCompactVTTTimestamp(int64(i) * 10))
		b.WriteString(" --> ")
		b.WriteString(formatCompactVTTTimestamp(int64(i)*10 + 5))
		b.WriteString("\nx\n")
	}
	_, err := ParseWebVTT(b.String())
	wantFileErr(t, err, CodeCaptionTooManyCues)

	// Exactly at the cap is accepted.
	src := b.String()
	last := strings.LastIndex(strings.TrimSuffix(src, "\n"), "\n\n")
	cues, err := ParseWebVTT(src[:last+1])
	if err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	if len(cues) != MaxCaptionCues {
		t.Fatalf("cues = %d, want %d", len(cues), MaxCaptionCues)
	}
}

func TestParseSRTRefusesMalformed(t *testing.T) {
	for _, tc := range []struct {
		name, src, code string
	}{
		{"vtt sent as srt", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhi\n", CodeCaptionHeader},
		{"garbage", "hello world\n", CodeCaptionCue},
		{"inverted", "1\n00:00:02,000 --> 00:00:01,000\nhi\n", CodeCaptionTiming},
		{"overlap", "1\n00:00:01,000 --> 00:00:03,000\na\n\n2\n00:00:02,000 --> 00:00:04,000\nb\n", CodeCaptionOverlap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSRT(tc.src)
			wantFileErr(t, err, tc.code)
		})
	}
}

// Every way a caption file could smuggle markup or a cue boundary into the
// served text/vtt body.
func TestSanitizeCueTextNeutralisesInjection(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`<script>alert(1)</script>`, `alert(1)`},
		{`<img src=x onerror=alert(1)>hi`, `hi`},
		{`<b onclick="x">bold</b>`, `<b>bold</b>`},
		{`<B>loud</B>`, `<b>loud</b>`},
		{`<i.yellow>class</i>`, `<i>class</i>`},
		{`<v Bob>voice</v>`, `voice`},
		{`<c.red>coloured</c>`, `coloured`},
		{`<00:00:01.000>karaoke`, `karaoke`},
		{`<ruby>漢<rt>kan</rt></ruby>`, `漢kan`},
		{`profit --> loss`, `profit --&gt; loss`},
		{`a < b`, `a &lt; b`},
		{`a <b`, `a &lt;b`},
		{"open <i\nnext> line", "open &lt;i\nnext&gt; line"},
		{`fish & chips &amp; &lt;tag&gt; &#60;x&#62; &nbsp;`, `fish &amp; chips &amp; &lt;tag&gt; &amp;#60;x&amp;#62; &nbsp;`},
		{`<b>unclosed`, `<b>unclosed</b>`},
		{`</i>stray close`, `stray close`},
		{`<b><i>crossed</b></i>`, `<b><i>crossed</i></b>`},
		{`<b><b><b><b>deep</b></b></b></b>`, `<b><b><b>deep</b></b></b>`},
		{"one\n\n\ntwo\x07\x1b", "one\ntwo"},
		{`<b></b>`, ``},
		{`   `, ``},
	} {
		got := SanitizeCueText(tc.in)
		if got != tc.want {
			t.Errorf("SanitizeCueText(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if again := SanitizeCueText(got); again != got {
			t.Errorf("not idempotent on %q: %q -> %q", tc.in, got, again)
		}
	}
}

// The served body from a hostile file holds no tag but b/i/u, no raw
// "-->" in a payload, and no blank line inside a cue.
func TestRenderedHostileFileIsInert(t *testing.T) {
	src := "WEBVTT\n\nREGION\nid:x\n\n" +
		"00:01.000 --> 00:02.000\n<script>document.cookie</script><iframe src=//evil>\n-- > --&gt; <a href=javascript:x>link</a>\n"
	cues, err := ParseWebVTT(src)
	if err != nil {
		t.Fatalf("ParseWebVTT: %v", err)
	}
	body := RenderWebVTT(cues)
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\ndocument.cookie\n-- &gt; --&gt; link\n"
	if body != want {
		t.Fatalf("body\n got %q\nwant %q", body, want)
	}
}

func TestLooksLikeWebVTT(t *testing.T) {
	for in, want := range map[string]bool{
		"WEBVTT\n\n00:01.000 --> 00:02.000\nx": true,
		utf8BOM + "WEBVTT\r\n":                 true,
		"WEBVTT - title\n":                     true,
		"WEBVTT":                               true,
		"WEBVTTX\n":                            false,
		"a transcript about WEBVTT":            false,
		"":                                     false,
	} {
		if got := LooksLikeWebVTT(in); got != want {
			t.Errorf("LooksLikeWebVTT(%q) = %v, want %v", in, got, want)
		}
	}
}

// The renderer sanitises Markup cues itself, so a cue that did not come
// through the parser (a row edited in the database) still serves inert.
func TestRenderWebVTTSanitisesMarkupCues(t *testing.T) {
	got := RenderWebVTT([]Cue{
		{StartMs: 0, EndMs: 1000, Text: "<script>x</script> -->\n\nnext", Markup: true},
		{StartMs: 1000, EndMs: 2000, Text: "<b></b>", Markup: true},
	})
	want := "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nx --&gt;\nnext\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if c := RenderCompactWebVTT([]Cue{{StartMs: 61_000, EndMs: 3_600_000, Text: "x"}}); c != "WEBVTT\n\n01:01.000 --> 01:00:00.000\nx\n" {
		t.Fatalf("compact = %q", c)
	}
}
