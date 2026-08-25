package service

import (
	"testing"

	"github.com/google/uuid"
)

func TestKeysetWindowUsesLastChronologicalItemAsWatermark(t *testing.T) {
	items := make([]FeedItem, 4)
	for i := range items {
		token, err := uuid.NewUUID()
		if err != nil {
			t.Fatalf("timeuuid: %v", err)
		}
		items[i] = FeedItem{PostID: uuid.New(), CursorToken: token.String()}
	}

	page, next := keysetWindow(items, 3)
	if len(page) != 3 {
		t.Fatalf("page length=%d want 3", len(page))
	}
	if next != items[2].CursorToken {
		t.Fatalf("next=%q want last chronological token %q", next, items[2].CursorToken)
	}

	// Ranking is allowed to reorder page, but it must not be able to mutate
	// the source slice from which the page boundary was chosen.
	page[0].CursorToken = "changed"
	if items[0].CursorToken == "changed" {
		t.Fatal("keyset window aliases the source slice")
	}
}

func TestKeysetWindowOmitsCursorForTerminalPage(t *testing.T) {
	items := []FeedItem{{PostID: uuid.New(), CursorToken: "terminal"}}
	page, next := keysetWindow(items, 3)
	if len(page) != 1 || next != "" {
		t.Fatalf("page=%d next=%q want terminal page without cursor", len(page), next)
	}
}
