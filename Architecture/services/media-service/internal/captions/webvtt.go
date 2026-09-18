package captions

import (
	"fmt"
	"sort"
	"strings"
)

// WebVTT rendering for the browser <track> element.
//
// The stored caption row is not a caption FILE: media_subtitles holds the
// transcript inline (`content`) plus optional word timings
// (`word_level_json`), and `content_url` was usually empty. A
// `<track src>` needs bytes with a `text/vtt` body, so the bytes are
// rendered here, from the stored rows, at read time.
//
// Everything in this file is pure: no store, no clock, no network. The
// cue-building and the escaping are the parts that can silently produce a
// file the browser drops on the floor, so they are the parts that are
// table-tested.

// Cue is one timed caption line. Times are milliseconds from the start of
// the media.
type Cue struct {
	StartMs int64
	EndMs   int64
	Text    string
}

// Cue grouping for word-level transcripts. A karaoke word stream is not
// readable as one cue per word, so words are gathered until either bound
// is reached.
const (
	maxCueWords      = 10
	maxCueDurationMs = 5000
)

// RenderWebVTT formats cues as a WebVTT file.
//
// The output is deliberately strict about the three things browsers treat
// as fatal:
//
//   - the `WEBVTT` signature must be the first line of the file;
//   - a blank line separates the header from the first cue and every cue
//     from the next, so a payload that itself contains a blank line would
//     end the cue early — blank lines inside a payload are collapsed;
//   - `-->` inside a payload would be read as a new cue's timing line, and
//     `<` starts a cue-markup tag. Both are escaped, which also makes the
//     payload safe for the parser that renders it.
//
// A cue with an empty payload is skipped rather than emitted: an empty
// payload is not valid, and one bad cue makes a parser discard the file.
func RenderWebVTT(cues []Cue) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n")
	for _, cue := range cues {
		text := escapeCueText(cue.Text)
		if text == "" {
			continue
		}
		start, end := normalizeCueTimes(cue.StartMs, cue.EndMs)
		b.WriteString("\n")
		b.WriteString(FormatVTTTimestamp(start))
		b.WriteString(" --> ")
		b.WriteString(FormatVTTTimestamp(end))
		b.WriteString("\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	return b.String()
}

// normalizeCueTimes clamps a cue onto the timeline WebVTT accepts: no
// negative offsets, and an end strictly after the start (a zero-length cue
// is displayed by nothing).
func normalizeCueTimes(startMs, endMs int64) (int64, int64) {
	if startMs < 0 {
		startMs = 0
	}
	if endMs < startMs {
		endMs = startMs
	}
	if endMs == startMs {
		endMs = startMs + 1
	}
	return startMs, endMs
}

// FormatVTTTimestamp renders milliseconds as HH:MM:SS.mmm.
//
// The hours field is always written. It is optional in the spec, but
// emitting it unconditionally removes the branch where a transcript longer
// than an hour changes shape mid-file.
func FormatVTTTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	hours := ms / 3_600_000
	ms -= hours * 3_600_000
	minutes := ms / 60_000
	ms -= minutes * 60_000
	seconds := ms / 1000
	millis := ms - seconds*1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, millis)
}

// escapeCueText makes an arbitrary transcript safe as a cue payload.
//
// `&`, `<` and `>` are escaped, which covers both hazards at once: `-->`
// becomes `--&gt;` so it can never be mistaken for a timing line, and
// `<script>` (or a stray `<i>` in a machine transcript) becomes literal
// text instead of cue markup. Carriage returns are dropped and runs of
// blank lines collapsed, because a blank line terminates the cue.
func escapeCueText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")

	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// BuildCues turns a stored subtitle row into cues.
//
// Word timings are preferred: they are what the provider actually
// measured. With no timings there is still a transcript worth showing, so
// it becomes a single cue spanning the media — captions that are not
// synchronised are far better than a <track> that loads nothing, and the
// caller knows the duration.
//
// totalDurationMs <= 0 means the duration is unknown; the fallback cue
// then gets a nominal length rather than a zero-length one.
func BuildCues(text string, words []Word, totalDurationMs int64) []Cue {
	if cues := cuesFromWords(words); len(cues) > 0 {
		return cues
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if totalDurationMs <= 0 {
		totalDurationMs = int64(len(strings.Fields(text))) * 400
	}
	if totalDurationMs <= 0 {
		totalDurationMs = 1000
	}
	return []Cue{{StartMs: 0, EndMs: totalDurationMs, Text: text}}
}

// cuesFromWords groups a word stream into readable cues.
func cuesFromWords(words []Word) []Cue {
	timed := make([]Word, 0, len(words))
	for _, w := range words {
		if strings.TrimSpace(w.Text) == "" {
			continue
		}
		timed = append(timed, w)
	}
	if len(timed) == 0 {
		return nil
	}
	sort.SliceStable(timed, func(i, j int) bool { return timed[i].StartMs < timed[j].StartMs })

	var (
		cues    []Cue
		current []string
		start   int64
		end     int64
	)
	flush := func() {
		if len(current) == 0 {
			return
		}
		cues = append(cues, Cue{StartMs: start, EndMs: end, Text: strings.Join(current, " ")})
		current = nil
	}
	for _, w := range timed {
		if len(current) == 0 {
			start, end = w.StartMs, w.EndMs
		}
		if w.EndMs > end {
			end = w.EndMs
		}
		current = append(current, strings.TrimSpace(w.Text))
		if len(current) >= maxCueWords || end-start >= maxCueDurationMs {
			flush()
		}
	}
	flush()
	return cues
}
