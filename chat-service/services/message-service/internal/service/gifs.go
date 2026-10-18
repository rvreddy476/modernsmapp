package service

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Chat parity §2.1 GIF messages (founder decision, 18 Oct 2026): GIF search
// is client-side through the Giphy SDK (per-platform keys). Giphy's terms
// forbid proxying search or caching media server-side, so this service has
// no GIF search route and no Giphy key. A gif send carries the client's
// choice — provider, id, the Giphy media URL and its size — which is
// validated here and stored / returned UNMODIFIED (query parameters
// included: Giphy's URLs carry their own tracking parameters).

var (
	giphyIDRe   = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
	giphyHostRe = regexp.MustCompile(`^(i|media|media[0-9])\.giphy\.com$`)
)

const gifMaxDimension = 4096

// validGiphyURL is an https URL on i.giphy.com, media.giphy.com or
// mediaN.giphy.com (N 0-9), with no credentials and no explicit port.
func validGiphyURL(raw string) bool {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Opaque != "" {
		return false
	}
	return giphyHostRe.MatchString(u.Host)
}

// validateGIFRef checks the client-supplied gif card.
func validateGIFRef(g *GIFRef) error {
	if g == nil {
		return fmt.Errorf("%w: gif is required for gif messages", ErrInvalidMessageContent)
	}
	if g.Provider != "giphy" {
		return fmt.Errorf("%w: gif provider must be giphy", ErrInvalidMessageContent)
	}
	if !giphyIDRe.MatchString(g.ID) {
		return fmt.Errorf("%w: gif id is malformed", ErrInvalidMessageContent)
	}
	if !validGiphyURL(g.URL) {
		return fmt.Errorf("%w: gif url must be an https Giphy media url", ErrInvalidMessageContent)
	}
	if g.StillURL != "" && !validGiphyURL(g.StillURL) {
		return fmt.Errorf("%w: gif still_url must be an https Giphy media url", ErrInvalidMessageContent)
	}
	if g.Width < 1 || g.Width > gifMaxDimension || g.Height < 1 || g.Height > gifMaxDimension {
		return fmt.Errorf("%w: gif width and height must be 1..%d", ErrInvalidMessageContent, gifMaxDimension)
	}
	return nil
}

// gifHashRef is the gif card as the idempotency request hash sees it:
// is_sticker is omitted when false, so the hash of every gif send made
// before stickers existed is unchanged (a retried key still matches).
type gifHashRef struct {
	Provider  string `json:"provider"`
	ID        string `json:"id"`
	URL       string `json:"url"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	StillURL  string `json:"still_url,omitempty"`
	IsSticker bool   `json:"is_sticker,omitempty"`
}

func gifHashOf(g *GIFRef) *gifHashRef {
	if g == nil {
		return nil
	}
	return &gifHashRef{Provider: g.Provider, ID: g.ID, URL: g.URL, Width: g.Width, Height: g.Height, StillURL: g.StillURL, IsSticker: g.IsSticker}
}
