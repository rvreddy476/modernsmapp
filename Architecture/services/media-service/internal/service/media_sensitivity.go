package service

import (
	"encoding/json"
	"strings"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
)

// Chat parity fix pass 2 (2026-10-18): whether an asset is "sensitive" —
// nudity, sexual or violent content that the moderation scan let through
// (a blocked category rejects the asset outright, so a chat attachment, which
// must be moderation_status='passed', can only ever carry the borderline
// ones). message-service reads it off the keyed GET /v1/media/:id and fills
// media.sensitive; clients blur behind a tap.
//
// The answer is tri-state on purpose:
//
//	nil   — no real scan ran (scanner disabled, the stub, labels never
//	        stored, audio). Unknown is never reported as "not sensitive".
//	true  — a stored label, or its parent category, is in sensitiveCategories,
//	        or the asset was rejected.
//	false — a real scanner answered and nothing it found is in those
//	        categories.
//
// No confidence floor is applied here: the stored labels already cleared the
// scanner's own MinConfidence (MEDIA_SCANNER_MIN_CONFIDENCE, default 80 — the
// same bar dating-service reviews at), and lowering that is the operator's
// conservative direction, which this must not undo.

// sensitiveCategories are the Rekognition moderation labels (v6 and v7
// taxonomies) that make an asset sensitive. A label matches on its own name or
// its parent's, case-insensitively — the same rule as the scanner's blocked
// set (worstBlocked) and dating-service's review list. Rekognition returns
// every level of a finding, so naming the top-level categories covers their
// children; the children are listed too so a label stored without its parent
// still matches.
//
// Deliberately NOT here: Alcohol, Drugs / Drugs & Tobacco, Tobacco, Gambling,
// Rude Gestures, Hate Symbols. They are not nudity, sexual or violent content
// (Hate Symbols rejects the asset anyway, which reports true below).
var sensitiveCategories = func() map[string]bool {
	names := []string{
		// Sexual / nudity — v6 "Explicit Nudity" + "Suggestive", v7
		// "Explicit" + "Non-Explicit Nudity of Intimate parts and Kissing"
		// + "Swimwear or Underwear" (v6 files swimwear under Suggestive).
		"Explicit Nudity", "Explicit", "Explicit Sexual Activity", "Sexual Activity",
		"Nudity", "Graphic Male Nudity", "Graphic Female Nudity",
		"Exposed Male Genitalia", "Exposed Female Genitalia", "Exposed Buttocks or Anus",
		"Exposed Female Nipple", "Illustrated Explicit Nudity", "Adult Toys", "Sex Toys",
		"Suggestive", "Partial Nudity", "Sexual Situations", "Revealing Clothes",
		"Female Swimwear Or Underwear", "Male Swimwear Or Underwear",
		"Swimwear or Underwear", "Non-Explicit Nudity",
		"Non-Explicit Nudity of Intimate parts and Kissing", "Obstructed Intimate Parts",
		// Violent / disturbing — v6 and v7.
		"Violence", "Graphic Violence", "Graphic Violence Or Gore", "Physical Violence",
		"Weapon Violence", "Weapons", "Self Injury", "Self-Harm",
		"Visually Disturbing", "Death and Emaciation", "Emaciated Bodies", "Corpses",
		"Hanging", "Crashes", "Air Crash", "Explosions And Blasts",
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[strings.ToLower(n)] = true
	}
	return m
}()

// MediaSensitivity is the moderation summary a keyed service caller reads.
type MediaSensitivity struct {
	// Sensitive is nil when no real scan is on record (see above).
	Sensitive *bool
	// LabelNames are the stored label names, de-duplicated, in scan order:
	// names only, never confidences or parents. Nil when not scanned.
	LabelNames []string
}

// SensitivityOf summarises the stored moderation outcome of m.
func SensitivityOf(m *postgres.MediaAsset) MediaSensitivity {
	if m == nil {
		return MediaSensitivity{}
	}
	labels, scanned := storedScanLabels(m)
	out := MediaSensitivity{}
	if scanned {
		out.LabelNames = labelNames(labels)
	}
	// A rejected asset is the most sensitive answer there is, whatever the
	// record says about labels (video verdicts were not always stored).
	if m.ModerationStatus == "rejected" {
		return out.with(true)
	}
	if !scanned {
		return out
	}
	for _, l := range labels {
		if sensitiveCategories[strings.ToLower(strings.TrimSpace(l.Name))] ||
			(l.Parent != "" && sensitiveCategories[strings.ToLower(strings.TrimSpace(l.Parent))]) {
			return out.with(true)
		}
	}
	return out.with(false)
}

func (s MediaSensitivity) with(v bool) MediaSensitivity {
	s.Sensitive = &v
	return s
}

// storedScanLabels decodes the labels a real scanner stored. scanned is
// false for no scanner, the stub (it "scans" by approving everything), a
// NULL column, or a column that does not decode.
func storedScanLabels(m *postgres.MediaAsset) ([]processing.ModerationLabel, bool) {
	scanner := strings.ToLower(strings.TrimSpace(m.ModerationScanner))
	if scanner == "" || scanner == "stub" || scanner == "unknown" || len(m.ModerationLabels) == 0 {
		return nil, false
	}
	var labels []processing.ModerationLabel
	if err := json.Unmarshal(m.ModerationLabels, &labels); err != nil {
		return nil, false
	}
	if labels == nil {
		// JSON null (a "[]" decodes to an empty, non-nil slice).
		return nil, false
	}
	return labels, true
}

func labelNames(labels []processing.ModerationLabel) []string {
	names := make([]string, 0, len(labels))
	seen := make(map[string]bool, len(labels))
	for _, l := range labels {
		n := strings.TrimSpace(l.Name)
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		names = append(names, n)
	}
	return names
}
