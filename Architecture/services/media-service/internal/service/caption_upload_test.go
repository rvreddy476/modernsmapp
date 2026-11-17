package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/captions"
	"github.com/atpost/media-service/internal/store/postgres"
)

// served is what GET /v1/subtitles/:id/track/:lang renders for a stored row.
func served(content string, durationMs int64) string {
	return captions.RenderWebVTT(subtitleCues(&postgres.MediaSubtitle{Content: content}, durationMs))
}

func TestCaptionUploadVTTRoundTripsWithTimings(t *testing.T) {
	upload := "WEBVTT\n\n1\n00:00:00.000 --> 00:00:01.500\nhello there\n\n" +
		"2\n00:00:02.000 --> 00:00:04.250\nsecond <i>cue</i>\nline two\n"
	format, stored, err := NormalizeCaptionUpload("vtt", upload)
	if err != nil {
		t.Fatalf("NormalizeCaptionUpload: %v", err)
	}
	if format != "vtt" {
		t.Fatalf("stored format = %q, want vtt (the column admits vtt|srt)", format)
	}
	got := served(stored, 600_000)
	want := "WEBVTT\n\n00:00:00.000 --> 00:00:01.500\nhello there\n" +
		"\n00:00:02.000 --> 00:00:04.250\nsecond <i>cue</i>\nline two\n"
	if got != want {
		t.Fatalf("served\n got %q\nwant %q", got, want)
	}
	if strings.Contains(stored, "\n1\n") {
		t.Fatalf("identifiers should not be stored: %q", stored)
	}
}

func TestCaptionUploadSRTConverts(t *testing.T) {
	_, stored, err := NormalizeCaptionUpload("SRT", "1\r\n00:00:01,000 --> 00:00:02,000\r\nHi\r\n\r\n2\r\n00:00:02,000 --> 00:00:03,000\r\nBye\r\n")
	if err != nil {
		t.Fatalf("NormalizeCaptionUpload: %v", err)
	}
	if !strings.HasPrefix(stored, "WEBVTT\n") {
		t.Fatalf("SRT must be stored as WebVTT: %q", stored)
	}
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHi\n\n00:00:02.000 --> 00:00:03.000\nBye\n"
	if got := served(stored, 0); got != want {
		t.Fatalf("served\n got %q\nwant %q", got, want)
	}
}

func TestCaptionUploadRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, format, content, code string
	}{
		{"vtt without header", "vtt", "00:00:01.000 --> 00:00:02.000\nhi\n", captions.CodeCaptionHeader},
		{"plain text sent as vtt", "vtt", "a transcript", captions.CodeCaptionHeader},
		{"overlap", "vtt", "WEBVTT\n\n00:01.000 --> 00:03.000\na\n\n00:02.000 --> 00:04.000\nb\n", captions.CodeCaptionOverlap},
		{"negative", "vtt", "WEBVTT\n\n-00:01.000 --> 00:03.000\na\n", captions.CodeCaptionTiming},
		{"oversize", "vtt", "WEBVTT\n\n00:01.000 --> 00:03.000\n" + strings.Repeat("a", 200_001), captions.CodeCaptionTooLarge},
		// Under the input cap, over the stored cap once every & is escaped.
		{"oversize once normalised", "vtt", "WEBVTT\n\n00:01.000 --> 00:03.000\n" + strings.Repeat("&", 199_000), captions.CodeCaptionTooLarge},
		{"unknown format", "ass", "[Script Info]", captions.CodeCaptionFormat},
		{"srt garbage", "srt", "nope", captions.CodeCaptionCue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := NormalizeCaptionUpload(tc.format, tc.content)
			if !errors.Is(err, ErrInvalidCaption) {
				t.Fatalf("err = %v, want ErrInvalidCaption", err)
			}
			if got := CaptionFileErrorCode(err); got != tc.code {
				t.Fatalf("code = %q (%v), want %q", got, err, tc.code)
			}
		})
	}
}

func TestCaptionUploadMarkupInjectionIsNeutralised(t *testing.T) {
	_, stored, err := NormalizeCaptionUpload("vtt",
		"WEBVTT\n\nSTYLE\n::cue { background: url(x) }\n\n00:01.000 --> 00:02.000\n<script>alert(1)</script><b onmouseover=x>hi</b> -- > &x\n")
	if err != nil {
		t.Fatalf("NormalizeCaptionUpload: %v", err)
	}
	body := served(stored, 0)
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nalert(1)<b>hi</b> -- &gt; &amp;x\n"
	if body != want {
		t.Fatalf("served\n got %q\nwant %q", body, want)
	}
}

// Auto-generated captions and rows written before this change: plain text
// still becomes one cue spanning the media, escaped.
func TestCaptionUploadLegacyPlainTextStillOneCue(t *testing.T) {
	for _, format := range []string{"", "text"} {
		f, stored, err := NormalizeCaptionUpload(format, "a whole transcript <b>")
		if err != nil || f != "vtt" || stored != "a whole transcript <b>" {
			t.Fatalf("format %q: (%q, %q, %v), want the content unchanged", format, f, stored, err)
		}
		want := "WEBVTT\n\n00:00:00.000 --> 00:00:30.000\na whole transcript &lt;b&gt;\n"
		if got := served(stored, 30_000); got != want {
			t.Fatalf("served\n got %q\nwant %q", got, want)
		}
	}
	// A row stored unvalidated that only LOOKS like WebVTT degrades to the
	// old single escaped cue rather than failing the track.
	broken := "WEBVTT\n\n00:00:03.000 --> 00:00:01.000\nbackwards\n"
	got := served(broken, 10_000)
	if !strings.HasPrefix(got, "WEBVTT\n\n00:00:00.000 --> 00:00:10.000\n") || !strings.Contains(got, "--&gt;") {
		t.Fatalf("broken row should fall back to one escaped cue: %q", got)
	}
}
