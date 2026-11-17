package captions

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Uploaded caption FILES (WebVTT and SRT).
//
// A creator's upload arrives as the text of a .vtt (or .srt) file. It used to
// be stored and served as one plain-text transcript, so a viewer saw the
// whole file — timing lines included — as a single block over the entire
// video. This file parses such an upload into timed cues, refuses a file a
// player would mis-read, and neutralises everything that is not caption
// text.
//
// The output is served to every viewer's player as text/vtt, so the rule is
// that nothing from the upload reaches that body except:
//
//   - timings this parser produced itself (re-rendered, never copied);
//   - cue text, with `&`, `<` and `>` escaped, except the three basic
//     formatting tags <b>, <i> and <u> (re-emitted by name, attributes and
//     classes dropped, always balanced).
//
// Identifiers, cue settings, NOTE, STYLE and REGION blocks, voice and class
// spans, inline timestamps, ruby, and any other tag are dropped. Pure: no
// store, no clock.

// utf8BOM is the byte order mark some editors write first; it is skipped.
const utf8BOM = "\xef\xbb\xbf"

// Limits on an uploaded caption file.
const (
	// MaxCaptionFileBytes matches the web client's cap and the service's
	// stored-transcript cap.
	MaxCaptionFileBytes = 200_000
	// MaxCaptionCues bounds the cue count. 200 kB of the tightest legal
	// cues is about 7,700, so this cap is reachable; a 3-hour film is
	// typically 2,000-3,000 cues.
	MaxCaptionCues = 7_000
	// MaxCaptionTimestampMs is the latest time a cue may end: 48 hours.
	MaxCaptionTimestampMs = 48 * 3_600_000
	// maxMarkupDepth bounds nested <b>/<i>/<u>.
	maxMarkupDepth = 3
)

// Error codes a client can show. Stable: they are the API's 400 codes.
const (
	CodeCaptionEmpty    = "CAPTION_EMPTY"
	CodeCaptionTooLarge = "CAPTION_TOO_LARGE"
	CodeCaptionEncoding = "CAPTION_BAD_ENCODING"
	CodeCaptionHeader   = "CAPTION_BAD_HEADER"
	CodeCaptionTiming   = "CAPTION_BAD_TIMING"
	// CodeCaptionOverlap is no longer returned (REG-4): overlapping cues are
	// valid WebVTT. Kept so the code is never reused for something else.
	CodeCaptionOverlap = "CAPTION_OVERLAP"
	// CodeCaptionOrder: a cue starts before the previous cue starts.
	CodeCaptionOrder       = "CAPTION_OUT_OF_ORDER"
	CodeCaptionCue         = "CAPTION_BAD_CUE"
	CodeCaptionTooManyCues = "CAPTION_TOO_MANY_CUES"
	CodeCaptionFormat      = "CAPTION_FORMAT_UNSUPPORTED"
)

// FileError is a refused caption file. Line is 1-based, 0 when the problem
// is not on one line.
type FileError struct {
	Code   string
	Line   int
	Reason string
}

func (e *FileError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Reason)
	}
	return e.Reason
}

func fileErr(code string, line int, format string, args ...any) *FileError {
	return &FileError{Code: code, Line: line, Reason: fmt.Sprintf(format, args...)}
}

// LooksLikeWebVTT reports whether src opens with the WebVTT signature. The
// read path uses it to tell a stored, normalised caption file from a
// legacy plain-text transcript.
func LooksLikeWebVTT(src string) bool {
	src = strings.TrimPrefix(src, utf8BOM)
	first, _, _ := strings.Cut(src, "\n")
	return isWebVTTSignature(strings.TrimRight(first, "\r"))
}

func isWebVTTSignature(line string) bool {
	if !strings.HasPrefix(line, "WEBVTT") {
		return false
	}
	rest := line[len("WEBVTT"):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

// ParseWebVTT parses and validates a WebVTT file into cues with sanitised
// text (Cue.Markup is set). Cues must be in start-time order; they may
// overlap (two speakers, rolling captions), as WebVTT allows (REG-4).
func ParseWebVTT(src string) ([]Cue, error) {
	lines, err := splitCaptionLines(src)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 || !isWebVTTSignature(lines[0]) {
		return nil, fileErr(CodeCaptionHeader, 1, "a WebVTT file must start with the line WEBVTT")
	}
	// The header runs to the first blank line and may not hold a cue.
	i := 1
	for ; i < len(lines) && lines[i] != ""; i++ {
		if strings.Contains(lines[i], "-->") {
			return nil, fileErr(CodeCaptionHeader, i+1, "a blank line must separate the WEBVTT header from the first cue")
		}
	}
	return parseCueBlocks(lines, i, true)
}

// ParseSRT parses and validates a SubRip file into cues with sanitised text.
// Timestamps take a comma (the SRT spelling) or a full stop.
func ParseSRT(src string) ([]Cue, error) {
	lines, err := splitCaptionLines(src)
	if err != nil {
		return nil, err
	}
	if len(lines) > 0 && isWebVTTSignature(lines[0]) {
		return nil, fileErr(CodeCaptionHeader, 1, "this is a WebVTT file; upload it with format vtt")
	}
	return parseCueBlocks(lines, 0, false)
}

// splitCaptionLines checks size and encoding and splits on any line ending.
func splitCaptionLines(src string) ([]string, error) {
	if len(src) > MaxCaptionFileBytes {
		return nil, fileErr(CodeCaptionTooLarge, 0, "caption file exceeds %d bytes", MaxCaptionFileBytes)
	}
	if !utf8.ValidString(src) {
		return nil, fileErr(CodeCaptionEncoding, 0, "caption file must be UTF-8 text")
	}
	if strings.IndexByte(src, 0) >= 0 {
		return nil, fileErr(CodeCaptionEncoding, 0, "caption file contains a NUL byte")
	}
	src = strings.TrimPrefix(src, utf8BOM)
	if strings.TrimSpace(src) == "" {
		return nil, fileErr(CodeCaptionEmpty, 0, "caption file is empty")
	}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")
	for k := range lines {
		// A line of only whitespace separates blocks, as a player treats it.
		if strings.TrimSpace(lines[k]) == "" {
			lines[k] = ""
		}
	}
	return lines, nil
}

// isBlockKeyword reports whether line opens a NOTE/STYLE/REGION block.
func isBlockKeyword(line, keyword string) bool {
	if !strings.HasPrefix(line, keyword) {
		return false
	}
	rest := line[len(keyword):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

// parseCueBlocks reads blank-line separated blocks from lines[from:].
func parseCueBlocks(lines []string, from int, vtt bool) ([]Cue, error) {
	var (
		cues      []Cue
		prevStart int64 = -1
		timed     int
	)
	for i := from; i < len(lines); {
		if lines[i] == "" {
			i++
			continue
		}
		start := i
		for i < len(lines) && lines[i] != "" {
			i++
		}
		block := lines[start:i]

		if vtt && (isBlockKeyword(block[0], "NOTE") ||
			((isBlockKeyword(block[0], "STYLE") || isBlockKeyword(block[0], "REGION")) && !blockHasArrow(block))) {
			continue // comments, CSS and regions are never served
		}

		timingAt := 0
		if !strings.Contains(block[0], "-->") {
			// An identifier (VTT) or index (SRT) line; dropped.
			if len(block) < 2 || !strings.Contains(block[1], "-->") {
				return nil, fileErr(CodeCaptionCue, start+1, "expected a timing line like 00:00:01.000 --> 00:00:02.000")
			}
			timingAt = 1
		}
		lineNo := start + timingAt + 1
		startMs, endMs, err := parseTimingLine(block[timingAt], vtt, lineNo)
		if err != nil {
			return nil, err
		}
		// REG-4: overlapping cues are valid WebVTT (two speakers, rolling
		// captions, YouTube exports); only the start-time order is required.
		if startMs < prevStart {
			return nil, fileErr(CodeCaptionOrder, lineNo,
				"cue starts at %s, before the previous cue starts at %s; cues must be in start-time order",
				FormatVTTTimestamp(startMs), FormatVTTTimestamp(prevStart))
		}
		prevStart = startMs

		payload := block[timingAt+1:]
		for k, line := range payload {
			if strings.Contains(line, "-->") {
				return nil, fileErr(CodeCaptionCue, lineNo+1+k, "a blank line must separate one cue from the next")
			}
		}
		timed++
		if timed > MaxCaptionCues {
			return nil, fileErr(CodeCaptionTooManyCues, lineNo, "caption file has more than %d cues", MaxCaptionCues)
		}
		text := SanitizeCueText(strings.Join(payload, "\n"))
		if text == "" {
			continue // a cue with nothing to show is dropped, not served empty
		}
		cues = append(cues, Cue{StartMs: startMs, EndMs: endMs, Text: text, Markup: true})
	}
	if len(cues) == 0 {
		return nil, fileErr(CodeCaptionEmpty, 0, "caption file has no cues with text")
	}
	return cues, nil
}

func blockHasArrow(block []string) bool {
	for _, l := range block {
		if strings.Contains(l, "-->") {
			return true
		}
	}
	return false
}

// parseTimingLine reads "start --> end [settings]"; settings are dropped.
func parseTimingLine(line string, vtt bool, lineNo int) (int64, int64, error) {
	left, right, _ := strings.Cut(line, "-->")
	fields := strings.Fields(right)
	if len(fields) == 0 {
		return 0, 0, fileErr(CodeCaptionTiming, lineNo, "timing line has no end time")
	}
	startMs, ok := parseCueTimestamp(strings.TrimSpace(left), vtt)
	if !ok {
		return 0, 0, fileErr(CodeCaptionTiming, lineNo, "start time %q is not a valid timestamp", strings.TrimSpace(left))
	}
	endMs, ok := parseCueTimestamp(fields[0], vtt)
	if !ok {
		return 0, 0, fileErr(CodeCaptionTiming, lineNo, "end time %q is not a valid timestamp", fields[0])
	}
	if endMs <= startMs {
		return 0, 0, fileErr(CodeCaptionTiming, lineNo, "cue must end after it starts")
	}
	if endMs > MaxCaptionTimestampMs {
		return 0, 0, fileErr(CodeCaptionTiming, lineNo, "cue ends after 48 hours")
	}
	return startMs, endMs, nil
}

// parseCueTimestamp reads [H+:]MM:SS.mmm. No sign, minutes and seconds
// 00-59, exactly three millisecond digits. Tolerated in both formats, as real
// files and converters write them (REG-4, REG-8's server half): one to four
// hour digits ("0:00:01.000"), no hours ("00:01.000"), and a comma or a full
// stop before the milliseconds (an SRT converted to VTT that kept its comma).
// The served file is always re-rendered in the canonical form.
func parseCueTimestamp(s string, _ bool) (int64, bool) {
	sep := strings.LastIndexAny(s, ".,")
	if sep < 0 {
		return 0, false
	}
	frac := s[sep+1:]
	parts := strings.Split(s[:sep], ":")
	if len(frac) != 3 || !allDigits(frac) {
		return 0, false
	}
	var hours int64
	switch len(parts) {
	case 2:
	case 3:
		if !allDigits(parts[0]) || len(parts[0]) > 4 {
			return 0, false
		}
		hours, _ = strconv.ParseInt(parts[0], 10, 64)
		parts = parts[1:]
	default:
		return 0, false
	}
	if len(parts[0]) != 2 || len(parts[1]) != 2 || !allDigits(parts[0]) || !allDigits(parts[1]) {
		return 0, false
	}
	minutes, _ := strconv.ParseInt(parts[0], 10, 64)
	seconds, _ := strconv.ParseInt(parts[1], 10, 64)
	millis, _ := strconv.ParseInt(frac, 10, 64)
	if minutes > 59 || seconds > 59 {
		return 0, false
	}
	return ((hours*60+minutes)*60+seconds)*1000 + millis, true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// allowedCueTags are the only markup a served cue may carry.
var allowedCueTags = map[string]bool{"b": true, "i": true, "u": true}

// allowedCueEntities pass through unchanged; every other `&` is escaped.
var allowedCueEntities = []string{"&amp;", "&lt;", "&gt;", "&nbsp;", "&lrm;", "&rlm;"}

// SanitizeCueText turns cue text from an uploaded file into a safe WebVTT
// payload. It is idempotent, so the read path re-applies it to stored rows.
//
//   - <b>, <i>, <u> (any case, classes and annotations dropped) are kept and
//     balanced; every other tag is removed with its brackets;
//   - a `<` that does not close on the same line, and every stray `>`, are
//     escaped, so `-->` can never appear in a payload;
//   - `&` is escaped unless it starts one of the basic named entities;
//   - control characters are dropped, lines are trimmed, blank lines removed
//     (a blank line would end the cue).
//
// Returns "" when no visible text remains.
func SanitizeCueText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	s := strings.Join(kept, "\n")

	var (
		out     strings.Builder
		stack   []string
		visible bool
	)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '<':
			rest := s[i+1:]
			end := strings.IndexByte(rest, '>')
			nl := strings.IndexByte(rest, '\n')
			if end < 0 || (nl >= 0 && nl < end) {
				out.WriteString("&lt;")
				visible = true
				i++
				continue
			}
			tag := strings.ToLower(strings.TrimSpace(rest[:end]))
			i += end + 2
			closing := strings.HasPrefix(tag, "/")
			name := strings.TrimSpace(strings.TrimPrefix(tag, "/"))
			if cut := strings.IndexAny(name, ". \t"); cut >= 0 {
				name = name[:cut]
			}
			if !allowedCueTags[name] {
				continue // dropped, brackets and all
			}
			if closing {
				if len(stack) > 0 && stack[len(stack)-1] == name {
					stack = stack[:len(stack)-1]
					out.WriteString("</" + name + ">")
				}
				continue
			}
			if len(stack) >= maxMarkupDepth {
				continue
			}
			stack = append(stack, name)
			out.WriteString("<" + name + ">")
		case c == '>':
			out.WriteString("&gt;")
			visible = true
			i++
		case c == '&':
			entity := ""
			for _, e := range allowedCueEntities {
				if strings.HasPrefix(s[i:], e) {
					entity = e
					break
				}
			}
			if entity == "" {
				out.WriteString("&amp;")
				i++
			} else {
				out.WriteString(entity)
				i += len(entity)
			}
			visible = true
		case c < 0x20 && c != '\n' && c != '\t', c == 0x7f:
			i++
		default:
			out.WriteByte(c)
			if c != ' ' && c != '\t' && c != '\n' {
				visible = true
			}
			i++
		}
	}
	for k := len(stack) - 1; k >= 0; k-- {
		out.WriteString("</" + stack[k] + ">")
	}
	if !visible {
		return ""
	}
	return out.String()
}
