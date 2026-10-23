package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/shared/events"
)

// Video credits (2026-10-23, credits.go): both writers of a post document
// carry the cast & crew projection for a long video, and only for one.

func creditFields() events.PostCreditSearchFields {
	return events.PostCreditSearchFields{
		CreditNames:     []string{"Asha Rao", "Vikram Iyer"},
		CreditRoles:     []string{"Director", "Actor"},
		Genres:          []string{"drama", "thriller"},
		ReleaseYear:     2024,
		ProductionHouse: "Blue Hour Films",
	}
}

func (f *fakeOS) doc(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.docs[id]
}

func assertCredits(t *testing.T, doc map[string]any, want bool) {
	t.Helper()
	if doc == nil {
		t.Fatal("document not indexed")
	}
	fields := []string{"credit_names", "credit_roles", "genres", "release_year", "production_house"}
	for _, field := range fields {
		_, present := doc[field]
		if present != want {
			t.Fatalf("%s present=%v, want %v (doc %v)", field, present, want, doc)
		}
	}
	if !want {
		return
	}
	names, _ := doc["credit_names"].([]any)
	if len(names) != 2 || names[0] != "Asha Rao" {
		t.Fatalf("credit_names = %v", doc["credit_names"])
	}
	if toInt64(doc["release_year"]) != 2024 || doc["production_house"] != "Blue Hour Films" {
		t.Fatalf("year/house = %v / %v", doc["release_year"], doc["production_house"])
	}
}

func TestPostCreated_IndexesCreditsForLongVideosOnly(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		want        bool
	}{
		{"long_video", true},
		{"video", true},
		{"flick", false},
		{"post", false},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			f := newFakeOS(t)
			c := newTestConsumer(t, f)
			id := "credits-created-" + tc.contentType
			if err := c.processMessage(context.Background(), msg(t, events.PostCreated, events.PostCreatedPayload{
				PostID: id, AuthorID: "author-1", Text: "a film", Visibility: "public",
				ContentType: tc.contentType, ReviewStatus: "approved", SearchRev: 1, CreatedAt: time.Now().UTC(),
				PostCreditSearchFields: creditFields(),
			})); err != nil {
				t.Fatal(err)
			}
			assertCredits(t, f.doc(id), tc.want)
		})
	}
}

func TestEligibility_ReindexKeepsCreditsForLongVideosOnly(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		want        bool
	}{
		{"long_video", true},
		{"flick", false},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			f := newFakeOS(t)
			c := newTestConsumer(t, f)
			id := "credits-eligibility-" + tc.contentType
			if err := c.processMessage(context.Background(), msg(t, events.PostSearchEligibilityChanged, events.PostSearchEligibilityChangedPayload{
				PostID: id, AuthorID: "author-1", Visibility: "public", ReviewStatus: "approved",
				SearchRev: 2, Text: "a film", ContentType: tc.contentType,
				CreatedAt: time.Now().UTC().Add(-time.Hour), ChangedAt: time.Now().UTC(),
				PostCreditSearchFields: creditFields(),
			})); err != nil {
				t.Fatal(err)
			}
			assertCredits(t, f.doc(id), tc.want)
		})
	}
}
