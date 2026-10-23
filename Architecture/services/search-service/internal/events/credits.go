package events

import (
	"strings"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
)

// Video credits (2026-10-23). post-service puts the cast & crew projection
// (events.PostCreditSearchFields) on PostCreated and on every
// PostSearchEligibilityChanged of a long video, and on nothing else. Both
// handlers copy it through applyCreditFields, and only for a long video:
// a payload of any other kind that carries the fields anyway (a producer
// bug, a replayed event from a post since reclassified) indexes none of
// them, so a reel can never be found by a credited name.

// creditsApply reports whether a content type carries credits: the
// canonical long_video and its legacy spelling.
func creditsApply(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "long_video", "video":
		return true
	}
	return false
}

// applyCreditFields copies the credit projection onto doc when contentType
// is a long video, and clears it otherwise.
func applyCreditFields(doc *search.PostDoc, contentType string, f events.PostCreditSearchFields) {
	if !creditsApply(contentType) {
		doc.CreditNames, doc.CreditRoles, doc.Genres = nil, nil, nil
		doc.ReleaseYear, doc.ProductionHouse = 0, ""
		return
	}
	doc.CreditNames = f.CreditNames
	doc.CreditRoles = f.CreditRoles
	doc.Genres = f.Genres
	doc.ReleaseYear = f.ReleaseYear
	doc.ProductionHouse = f.ProductionHouse
}
