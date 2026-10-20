// Package places is the location search behind the upload studio's
// Location field (2026-10-21, after a walk of TikTok Studio's upload page).
//
// It wraps Google Places API (New) Autocomplete — the platform's maps
// provider since Feast (13 Sep). The server key never reaches a browser:
// clients call post-service, post-service calls Google. A post stores the
// chosen place as location_name + location_place_id (migration 066).
//
// Without a key the searcher reports ErrDisabled and the client falls back
// to free text; nothing else depends on it.
package places

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	autocompleteURL = "https://places.googleapis.com/v1/places:autocomplete"
	// MinQueryRunes / MaxQueryRunes bound what is worth a billed request.
	MinQueryRunes = 2
	MaxQueryRunes = 100
	// MaxResults is what the field shows; Google returns at most 5 anyway.
	MaxResults = 5
)

var (
	// ErrDisabled: no key configured. The field stays free text.
	ErrDisabled = errors.New("place search is not configured")
	// ErrBadQuery: too short, too long or empty.
	ErrBadQuery = errors.New("search for 2 to 100 characters")
	// ErrUpstream: Google refused or failed; the field falls back to free text.
	ErrUpstream = errors.New("place search is unavailable")
)

// Place is one suggestion: Name is what the post shows, Secondary the line
// under it in the picker ("Hyderabad, Telangana, India").
type Place struct {
	PlaceID   string `json:"place_id"`
	Name      string `json:"name"`
	Secondary string `json:"secondary,omitempty"`
}

// Searcher calls Google Places Autocomplete.
type Searcher struct {
	key      string
	endpoint string
	client   *http.Client
}

// New returns a Searcher; an empty key yields one that reports ErrDisabled.
func New(key string) *Searcher {
	return &Searcher{
		key:      strings.TrimSpace(key),
		endpoint: autocompleteURL,
		client:   &http.Client{Timeout: 3 * time.Second},
	}
}

// Enabled reports whether a key is configured.
func (s *Searcher) Enabled() bool { return s != nil && s.key != "" }

// NormalizeQuery trims the query and checks its length.
func NormalizeQuery(q string) (string, error) {
	q = strings.Join(strings.Fields(q), " ")
	n := utf8.RuneCountInString(q)
	if n < MinQueryRunes || n > MaxQueryRunes {
		return "", ErrBadQuery
	}
	return q, nil
}

type autocompleteRequest struct {
	Input        string `json:"input"`
	SessionToken string `json:"sessionToken,omitempty"`
	LanguageCode string `json:"languageCode,omitempty"`
}

type autocompleteResponse struct {
	Suggestions []struct {
		PlacePrediction *struct {
			PlaceID          string                `json:"placeId"`
			Text             struct{ Text string } `json:"text"`
			StructuredFormat struct {
				MainText      struct{ Text string } `json:"mainText"`
				SecondaryText struct{ Text string } `json:"secondaryText"`
			} `json:"structuredFormat"`
		} `json:"placePrediction"`
	} `json:"suggestions"`
}

// Autocomplete returns up to MaxResults places for q. session groups one
// person's keystrokes into one billed session (a client-made UUID; "" is
// allowed). language is an optional BCP-47 code for the result text.
func (s *Searcher) Autocomplete(ctx context.Context, q, session, language string) ([]Place, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	q, err := NormalizeQuery(q)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(autocompleteRequest{Input: q, SessionToken: sessionToken(session), LanguageCode: languageCode(language)})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", s.key)
	// Ask only for what the picker shows: cheaper, and nothing else leaks.
	req.Header.Set("X-Goog-FieldMask", "suggestions.placePrediction.placeId,suggestions.placePrediction.text.text,suggestions.placePrediction.structuredFormat")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode)
	}
	var out autocompleteResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrUpstream, err)
	}
	places := make([]Place, 0, MaxResults)
	for _, sug := range out.Suggestions {
		p := sug.PlacePrediction
		if p == nil || !validPlaceID(p.PlaceID) {
			continue
		}
		name := strings.TrimSpace(p.StructuredFormat.MainText.Text)
		if name == "" {
			name = strings.TrimSpace(p.Text.Text)
		}
		if name == "" {
			continue
		}
		places = append(places, Place{PlaceID: p.PlaceID, Name: name, Secondary: strings.TrimSpace(p.StructuredFormat.SecondaryText.Text)})
		if len(places) == MaxResults {
			break
		}
	}
	return places, nil
}

// validPlaceID keeps only ids the post row will accept (the character set
// of service.NormalizeLocationPlaceID, at most 255 bytes).
func validPlaceID(id string) bool {
	if id == "" || len(id) > 255 {
		return false
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == ':' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// sessionToken passes a client token through only when it looks like one
// (Google wants URL-safe base64 or a UUID, at most 36 characters).
func sessionToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 36 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return s
}

func languageCode(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || len(s) > 10 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
			return ""
		}
	}
	return s
}
