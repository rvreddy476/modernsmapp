package service

import (
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// P0-8: request-shape validation (the atomic/idempotent store behavior is
// covered by the integration suite — it needs a real Postgres).

func TestCreateThread_Validation(t *testing.T) {
	svc := &Service{} // validation happens before any dependency is touched

	oneEntry := &CreateThreadInput{
		AuthorID: uuid.New(),
		Entries:  []ThreadEntryInput{{Text: "only one"}},
	}
	if _, err := svc.CreateThread(t.Context(), oneEntry); err == nil {
		t.Fatal("single-entry thread must be rejected")
	}

	tooMany := &CreateThreadInput{AuthorID: uuid.New()}
	for i := 0; i < maxThreadEntries+1; i++ {
		tooMany.Entries = append(tooMany.Entries, ThreadEntryInput{Text: "x"})
	}
	if _, err := svc.CreateThread(t.Context(), tooMany); err == nil {
		t.Fatal("oversized thread must be rejected")
	}

	emptyEntry := &CreateThreadInput{
		AuthorID: uuid.New(),
		Entries:  []ThreadEntryInput{{Text: "first"}, {Text: "   "}},
	}
	if _, err := svc.CreateThread(t.Context(), emptyEntry); err == nil {
		t.Fatal("blank entry must be rejected")
	}

	longEntry := &CreateThreadInput{
		AuthorID: uuid.New(),
		Entries: []ThreadEntryInput{
			{Text: "first"},
			{Text: strings.Repeat("a", threadEntryMaxLen+1)},
		},
	}
	if _, err := svc.CreateThread(t.Context(), longEntry); err == nil {
		t.Fatal("over-length entry must be rejected")
	}

	// Codex P1-6: an unsupported visibility must be a 400 here, not a
	// 500 from the database CHECK constraint.
	badVis := &CreateThreadInput{
		AuthorID:   uuid.New(),
		Visibility: "everyone",
		Entries:    []ThreadEntryInput{{Text: "a"}, {Text: "b"}},
	}
	if _, err := svc.CreateThread(t.Context(), badVis); err == nil {
		t.Fatal("unsupported visibility must be rejected at the service boundary")
	}
}

// The accepted visibility set must stay in step with migration 019's
// posts_visibility_check constraint.
func TestValidPostVisibility_MatchesSchemaConstraint(t *testing.T) {
	for _, v := range []string{"public", "followers", "private", "unlisted", "trusted", "close_friends"} {
		if !validPostVisibility[v] {
			t.Errorf("%q is allowed by the DB constraint and must be accepted", v)
		}
	}
	for _, v := range []string{"everyone", "friends", "PUBLIC", ""} {
		if validPostVisibility[v] {
			t.Errorf("%q is not in the DB constraint and must be rejected", v)
		}
	}
}

// canViewThread implements the read-authorization rules. Public and
// author paths need no graph client, so they are unit-testable directly.
func TestCanViewThread_PublicAndAuthorRules(t *testing.T) {
	svc := &Service{} // no graph client ⇒ restricted lookups fail closed
	author := uuid.New()
	other := uuid.New()

	pub := &postgres.Post{AuthorID: author, Visibility: "public"}
	if !svc.canViewThread(t.Context(), pub, nil) {
		t.Error("a public thread must be readable anonymously")
	}
	if !svc.canViewThread(t.Context(), pub, &other) {
		t.Error("a public thread must be readable by anyone")
	}

	priv := &postgres.Post{AuthorID: author, Visibility: "private"}
	if !svc.canViewThread(t.Context(), priv, &author) {
		t.Error("the author must always see their own private thread")
	}
	if svc.canViewThread(t.Context(), priv, &other) {
		t.Error("a private thread must not be readable by another user")
	}
	if svc.canViewThread(t.Context(), priv, nil) {
		t.Error("a private thread must not be readable anonymously")
	}

	// followers-scoped: the author still sees it; a stranger is denied
	// when the graph cannot be consulted (fail-closed).
	fol := &postgres.Post{AuthorID: author, Visibility: "followers"}
	if !svc.canViewThread(t.Context(), fol, &author) {
		t.Error("the author must see their own followers-scoped thread")
	}
	if svc.canViewThread(t.Context(), fol, &other) {
		t.Error("with no graph client, a followers-scoped thread must fail closed")
	}
}
