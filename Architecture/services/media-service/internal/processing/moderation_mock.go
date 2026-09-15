package processing

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Dating plan lane D6 — the deterministic local/dev moderation scanner.
//
// MockModerationScanner never looks at pixels. It reads a test marker the
// caller embedded in the image bytes (a JPEG comment is enough):
//
//	ATPOST-MODERATION-TEST:v1:labels=<label>[;<label>...]
//	ATPOST-MODERATION-TEST:v1:unavailable
//
// where <label> is [Parent>]Name@confidence with underscores for spaces, e.g.
// labels=Explicit_Nudity>Nudity@97.5;Swimwear_or_Underwear@91. An image with
// no marker is a clean scan (no labels). "unavailable" returns
// ErrScanUnavailable. The blocked categories are the Rekognition scanner's,
// so a marker that would fail a real upload fails this one too.
//
// cmd/server refuses MEDIA_SCANNER_BACKEND=mock unless ENV is local/dev.

// MockModerationMarker prefixes the test marker.
const MockModerationMarker = "ATPOST-MODERATION-TEST:v1:"

// MockModerationScanner is the local/dev Scanner. See MockModerationMarker.
type MockModerationScanner struct {
	blocked map[string]bool
}

// NewMockModerationScanner builds the mock with the default blocked set.
func NewMockModerationScanner() *MockModerationScanner {
	blocked := make(map[string]bool, len(defaultBlockedLabels))
	for _, l := range defaultBlockedLabels {
		blocked[strings.ToLower(l)] = true
	}
	return &MockModerationScanner{blocked: blocked}
}

// Name implements NamedScanner.
func (*MockModerationScanner) Name() string { return "mock" }

// ScanImage implements Scanner.
func (m *MockModerationScanner) ScanImage(ctx context.Context, data []byte) (ScanResult, error) {
	if err := ctx.Err(); err != nil {
		return ScanResult{}, fmt.Errorf("%w: %v", ErrScanUnavailable, err)
	}
	if len(data) == 0 {
		return ScanResult{}, fmt.Errorf("%w: empty image", ErrScanUnavailable)
	}
	i := bytes.Index(data, []byte(MockModerationMarker))
	if i < 0 {
		return ScanResult{IsSafe: true, Labels: []ModerationLabel{}}, nil
	}
	rest := data[i+len(MockModerationMarker):]
	if end := bytes.IndexAny(rest, "\x00\r\n \t\xff"); end >= 0 {
		rest = rest[:end]
	}
	spec := string(rest)
	if spec == "unavailable" {
		return ScanResult{}, fmt.Errorf("%w: mock marker says unavailable", ErrScanUnavailable)
	}
	labels := []ModerationLabel{}
	if raw, ok := strings.CutPrefix(spec, "labels="); ok && raw != "" {
		for _, item := range strings.Split(raw, ";") {
			nameConf, confRaw, ok := strings.Cut(item, "@")
			if !ok {
				continue
			}
			conf, err := strconv.ParseFloat(confRaw, 64)
			if err != nil || conf < 0 || conf > 100 {
				continue
			}
			parent, name, hasParent := strings.Cut(nameConf, ">")
			if !hasParent {
				name, parent = parent, ""
			}
			labels = append(labels, ModerationLabel{
				Name:       strings.ReplaceAll(name, "_", " "),
				Parent:     strings.ReplaceAll(parent, "_", " "),
				Confidence: conf,
			})
		}
	}
	if label, conf := worstBlocked(labels, m.blocked); label != "" {
		return ScanResult{
			IsSafe: false,
			Reason: strings.ToLower(strings.ReplaceAll(label, " ", "_")),
			Score:  conf / 100,
			Labels: labels,
		}, nil
	}
	return ScanResult{IsSafe: true, Labels: labels}, nil
}

// worstBlocked returns the blocked top-level category with the highest
// confidence, or "" when none is blocked. The category is the label's parent
// when it has one, else its own name (the Rekognition scanner's rule).
func worstBlocked(labels []ModerationLabel, blocked map[string]bool) (string, float64) {
	var worst float64
	var worstLabel string
	for _, l := range labels {
		name := l.Parent
		if name == "" {
			name = l.Name
		}
		if !blocked[strings.ToLower(name)] {
			continue
		}
		if l.Confidence > worst {
			worst, worstLabel = l.Confidence, name
		}
	}
	return worstLabel, worst
}
