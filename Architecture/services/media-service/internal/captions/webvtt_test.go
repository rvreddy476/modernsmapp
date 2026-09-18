package captions

import (
	"strings"
	"testing"
)

func TestFormatVTTTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name string
		ms   int64
		want string
	}{
		{"zero", 0, "00:00:00.000"},
		{"sub second", 7, "00:00:00.007"},
		{"seconds and millis", 1234, "00:00:01.234"},
		{"minutes", 61_000, "00:01:01.000"},
		{"hours", 3_723_004, "01:02:03.004"},
		{"double digit hours", 36_000_000, "10:00:00.000"},
		{"negative clamps to zero", -5, "00:00:00.000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatVTTTimestamp(tc.ms); got != tc.want {
				t.Fatalf("FormatVTTTimestamp(%d) = %q, want %q", tc.ms, got, tc.want)
			}
		})
	}
}

func TestRenderWebVTT(t *testing.T) {
	for _, tc := range []struct {
		name string
		cues []Cue
		want string
	}{
		{
			name: "no cues still produces a parseable file",
			cues: nil,
			want: "WEBVTT\n",
		},
		{
			name: "single cue",
			cues: []Cue{{StartMs: 0, EndMs: 1500, Text: "hello there"}},
			want: "WEBVTT\n\n00:00:00.000 --> 00:00:01.500\nhello there\n",
		},
		{
			name: "cues are separated by a blank line",
			cues: []Cue{
				{StartMs: 0, EndMs: 1000, Text: "one"},
				{StartMs: 1000, EndMs: 2000, Text: "two"},
			},
			want: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\none\n" +
				"\n00:00:01.000 --> 00:00:02.000\ntwo\n",
		},
		{
			// The whole reason escaping exists: an unescaped "-->" in a
			// payload is read as the timing line of a new cue, and the
			// rest of the file is discarded.
			name: "arrow in the payload is escaped",
			cues: []Cue{{StartMs: 0, EndMs: 1000, Text: "profit --> loss"}},
			want: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nprofit --&gt; loss\n",
		},
		{
			name: "markup in the payload is escaped",
			cues: []Cue{{StartMs: 0, EndMs: 1000, Text: `<script>alert("x")</script> & <i>tags</i>`}},
			want: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\n" +
				`&lt;script&gt;alert("x")&lt;/script&gt; &amp; &lt;i&gt;tags&lt;/i&gt;` + "\n",
		},
		{
			name: "blank lines inside a payload are collapsed",
			cues: []Cue{{StartMs: 0, EndMs: 1000, Text: "first\r\n\r\n\r\nsecond\n"}},
			want: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nfirst\nsecond\n",
		},
		{
			name: "empty payloads are skipped, not emitted as broken cues",
			cues: []Cue{
				{StartMs: 0, EndMs: 1000, Text: "   \n\n  "},
				{StartMs: 1000, EndMs: 2000, Text: "kept"},
			},
			want: "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nkept\n",
		},
		{
			name: "zero-length and inverted cues are normalized",
			cues: []Cue{
				{StartMs: 500, EndMs: 500, Text: "instant"},
				{StartMs: -10, EndMs: -20, Text: "before the start"},
			},
			want: "WEBVTT\n\n00:00:00.500 --> 00:00:00.501\ninstant\n" +
				"\n00:00:00.000 --> 00:00:00.001\nbefore the start\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderWebVTT(tc.cues)
			if got != tc.want {
				t.Fatalf("RenderWebVTT()\n got: %q\nwant: %q", got, tc.want)
			}
			if !strings.HasPrefix(got, "WEBVTT\n") {
				t.Fatalf("every rendering must start with the WEBVTT signature: %q", got)
			}
		})
	}
}

func TestBuildCues(t *testing.T) {
	for _, tc := range []struct {
		name       string
		text       string
		words      []Word
		durationMs int64
		want       []Cue
	}{
		{
			name: "no text and no words yields nothing",
		},
		{
			name:       "transcript without timings becomes one spanning cue",
			text:       "a full transcript",
			durationMs: 12_000,
			want:       []Cue{{StartMs: 0, EndMs: 12_000, Text: "a full transcript"}},
		},
		{
			name: "unknown duration still produces a cue with a length",
			text: "two words",
			want: []Cue{{StartMs: 0, EndMs: 800, Text: "two words"}},
		},
		{
			name:       "word timings win over the flat transcript",
			text:       "ignored",
			durationMs: 60_000,
			words: []Word{
				{Text: "hello", StartMs: 100, EndMs: 400},
				{Text: "world", StartMs: 400, EndMs: 900},
			},
			want: []Cue{{StartMs: 100, EndMs: 900, Text: "hello world"}},
		},
		{
			name: "long word runs are split into readable cues",
			words: []Word{
				{Text: "a", StartMs: 0, EndMs: 100},
				{Text: "b", StartMs: 100, EndMs: 200},
				{Text: "c", StartMs: 200, EndMs: 300},
				{Text: "d", StartMs: 300, EndMs: 400},
				{Text: "e", StartMs: 400, EndMs: 500},
				{Text: "f", StartMs: 500, EndMs: 600},
				{Text: "g", StartMs: 600, EndMs: 700},
				{Text: "h", StartMs: 700, EndMs: 800},
				{Text: "i", StartMs: 800, EndMs: 900},
				{Text: "j", StartMs: 900, EndMs: 1000},
				{Text: "k", StartMs: 1000, EndMs: 1100},
			},
			want: []Cue{
				{StartMs: 0, EndMs: 1000, Text: "a b c d e f g h i j"},
				{StartMs: 1000, EndMs: 1100, Text: "k"},
			},
		},
		{
			name: "a long pause closes the cue on the duration bound",
			words: []Word{
				{Text: "start", StartMs: 0, EndMs: 200},
				{Text: "end", StartMs: 5000, EndMs: 5400},
				{Text: "next", StartMs: 5400, EndMs: 5600},
			},
			want: []Cue{
				{StartMs: 0, EndMs: 5400, Text: "start end"},
				{StartMs: 5400, EndMs: 5600, Text: "next"},
			},
		},
		{
			name:  "blank words are dropped",
			words: []Word{{Text: "  ", StartMs: 0, EndMs: 100}, {Text: "real", StartMs: 100, EndMs: 200}},
			want:  []Cue{{StartMs: 100, EndMs: 200, Text: "real"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildCues(tc.text, tc.words, tc.durationMs)
			if len(got) != len(tc.want) {
				t.Fatalf("BuildCues() = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("cue %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
