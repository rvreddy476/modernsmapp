// Duplicate open matches (count first) and the decline cooldown, store side.
// The DDL test runs without a database; the rest skip unless TEST_PG_DSN is
// set and refuse a database not named *_test. Tests that need duplicates drop
// uq_dating_matches_open_pair and restore it in cleanup.
package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/google/uuid"
)

func TestOpenPairIndexDDLMatchesSetupSQL(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	setup := norm(database.SetupSQL)
	if !strings.Contains(setup, norm(OpenPairIndexDDL)+";") {
		t.Fatalf("setup.sql does not carry store.OpenPairIndexDDL verbatim; the two index definitions have drifted")
	}
	if !strings.Contains(setup, "IF to_regclass('uq_dating_matches_open_pair') IS NULL AND NOT EXISTS") {
		t.Fatalf("setup.sql creates the open-pair index without the no-duplicates guard")
	}
	if strings.Contains(setup, "SET status = 'closed'") {
		t.Fatalf("setup.sql closes matches; duplicates must be counted and closed only by the boot step")
	}
}

// dropOpenPairIndexForTest drops the unique index so duplicates can be
// seeded, and in cleanup closes any duplicates left behind and recreates it.
func dropOpenPairIndexForTest(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.Exec(ctx, `DROP INDEX IF EXISTS `+OpenPairIndexName); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.Exec(ctx, `
            WITH ranked AS (
                SELECT id, row_number() OVER (PARTITION BY user_a, user_b ORDER BY id) AS rn
                FROM dating_matches WHERE status IN `+openMatchStatuses+`)
            UPDATE dating_matches SET status = 'closed', closed_at = now()
            WHERE id IN (SELECT id FROM ranked WHERE rn > 1)`); err != nil {
			t.Errorf("cleanup duplicates: %v", err)
		}
		if _, err := s.EnsureOpenPairIndex(ctx); err != nil {
			t.Errorf("restore index: %v", err)
		}
	})
	c, err := s.CountDuplicateOpenMatches(ctx)
	if err != nil {
		t.Fatalf("baseline count: %v", err)
	}
	if c.ExtraRows != 0 {
		t.Fatalf("test database already holds %d duplicate open matches; counts would be ambiguous", c.ExtraRows)
	}
}

func seedMatchRow(t *testing.T, s *Store, x, y uuid.UUID, status string, withConversation bool, matchedAt time.Time, lastMessageAt *time.Time) uuid.UUID {
	t.Helper()
	a, b := canonicalPair(x, y)
	var conv *uuid.UUID
	if withConversation {
		c := uuid.New()
		conv = &c
	}
	var id uuid.UUID
	if err := s.db.QueryRow(context.Background(), `
        INSERT INTO dating_matches (user_a, user_b, status, conversation_id, matched_at, last_message_at)
        VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, a, b, status, conv, matchedAt, lastMessageAt).Scan(&id); err != nil {
		t.Fatalf("seed match: %v", err)
	}
	return id
}

func TestDuplicateOpenMatches_CountScriptAgreesWithGoAndKeepRule(t *testing.T) {
	s, cleanup := statusITStore(t)
	// t.Cleanup, not defer: cleanups run last-in-first-out, so the pool
	// closes only after dropOpenPairIndexForTest has restored the index.
	t.Cleanup(cleanup)
	ctx := context.Background()
	dropOpenPairIndexForTest(t, s)

	now := time.Now()
	hourAgo := now.Add(-time.Hour)
	p1a, p1b := uuid.New(), uuid.New()
	keep1 := seedMatchRow(t, s, p1a, p1b, "conversing", true, now.Add(-10*24*time.Hour), nil) // conversation wins
	extra1 := seedMatchRow(t, s, p1a, p1b, "matched", false, now.Add(-time.Hour), nil)
	extra2 := seedMatchRow(t, s, p1a, p1b, "quiet", false, now.Add(-5*24*time.Hour), nil)
	seedMatchRow(t, s, p1a, p1b, "closed", true, now, nil) // closed rows never count
	p2a, p2b := uuid.New(), uuid.New()
	keep2 := seedMatchRow(t, s, p2a, p2b, "matched", false, now.Add(-9*24*time.Hour), &hourAgo) // most recent activity
	extra3 := seedMatchRow(t, s, p2a, p2b, "matched", false, now.Add(-2*time.Hour), nil)
	seedMatchRow(t, s, uuid.New(), uuid.New(), "matched", false, now, nil) // a single open match

	goCount, err := s.CountDuplicateOpenMatches(ctx)
	if err != nil {
		t.Fatalf("go count: %v", err)
	}
	if goCount != (DuplicateOpenMatchCounts{Groups: 2, ExtraRows: 3}) {
		t.Fatalf("go count = %+v, want 2 groups / 3 extra rows", goCount)
	}

	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "count-duplicate-matches.sql"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	results, err := conn.Conn().PgConn().Exec(ctx, string(script)).ReadAll()
	conn.Release()
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	var scriptGroups, scriptExtra = -1, -1
	var sample [][][]byte
	for _, res := range results {
		names := make([]string, len(res.FieldDescriptions))
		for i, fd := range res.FieldDescriptions {
			names[i] = fd.Name
		}
		switch strings.Join(names, ",") {
		case "duplicate_groups,extra_rows":
			scriptGroups, _ = strconv.Atoi(string(res.Rows[0][0]))
			scriptExtra, _ = strconv.Atoi(string(res.Rows[0][1]))
		case "user_a,user_b,open_count,kept_match_id,would_close_match_ids":
			sample = res.Rows
		}
	}
	if scriptGroups != goCount.Groups || scriptExtra != goCount.ExtraRows {
		t.Fatalf("script count = %d groups / %d extra, go count = %+v", scriptGroups, scriptExtra, goCount)
	}
	if len(sample) != 2 || string(sample[0][3]) != keep1.String() || string(sample[1][3]) != keep2.String() {
		t.Fatalf("script sample kept ids = %q; want %s then %s", sample, keep1, keep2)
	}
	if after, _ := s.CountDuplicateOpenMatches(ctx); after != goCount {
		t.Fatalf("running the script changed the counts: %+v -> %+v", goCount, after)
	}

	extras, err := s.ListDuplicateOpenMatchExtras(ctx, 100)
	if err != nil {
		t.Fatalf("list extras: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, m := range extras {
		got[m.ID] = true
	}
	if len(got) != 3 || !got[extra1] || !got[extra2] || !got[extra3] {
		t.Fatalf("extras = %v; want %s, %s, %s (keep rule: conversation, then latest activity)", got, extra1, extra2, extra3)
	}

	if _, err := s.EnsureOpenPairIndex(ctx); err == nil {
		t.Fatalf("the unique index was created while duplicates exist")
	}
	// The last open match of a pair is never closed as a duplicate.
	if _, err := s.CloseMatchWithReason(ctx, extra3, SystemActorID, CloseReasonDuplicate, nil); err != nil {
		t.Fatalf("close duplicate: %v", err)
	}
	if _, err := s.CloseMatchWithReason(ctx, keep2, SystemActorID, CloseReasonDuplicate, nil); !errors.Is(err, ErrMatchNotFound) {
		t.Fatalf("closing the last open match as a duplicate: err=%v, want ErrMatchNotFound", err)
	}
}

func TestDeclineCooldown_DeckExcludesDeclinerOneWay(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	senderGender, declinerGender := "dc-"+uuid.NewString()[:8], "dc-"+uuid.NewString()[:8]
	sender, decliner := uuid.New(), uuid.New()
	seedDiscoverableProfile(t, s, sender, senderGender)
	seedDiscoverableProfile(t, s, decliner, declinerGender)

	deckOf := func(viewer uuid.UUID, gender string) []CandidateProfile {
		out, err := s.FetchCandidates(ctx, CandidateQuery{ViewerID: viewer, GenderFilter: gender, Limit: 50})
		if err != nil {
			t.Fatalf("fetch candidates: %v", err)
		}
		return out
	}
	sp, err := s.CreateSpark(ctx, sender, decliner, "photo", "0", "")
	if err != nil {
		t.Fatalf("spark: %v", err)
	}
	if !containsCandidate(deckOf(sender, declinerGender), decliner) {
		t.Fatalf("decliner missing from the sender's deck before the decline")
	}
	if _, err := s.DeclineSpark(ctx, sp.ID, decliner); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if containsCandidate(deckOf(sender, declinerGender), decliner) {
		t.Fatalf("decliner still in the sender's deck within the cooldown")
	}
	if !containsCandidate(deckOf(decliner, senderGender), sender) {
		t.Fatalf("the decline removed the sender from the decliner's deck; it must be one-directional")
	}
	if ok, err := s.HasRecentDecline(ctx, sender, decliner); err != nil || !ok {
		t.Fatalf("HasRecentDecline(sender, decliner) = %v, %v; want true", ok, err)
	}
	if ok, err := s.HasRecentDecline(ctx, decliner, sender); err != nil || ok {
		t.Fatalf("HasRecentDecline(decliner, sender) = %v, %v; want false", ok, err)
	}

	if _, err := s.db.Exec(ctx, `UPDATE dating_sparks SET declined_at = now() - INTERVAL '31 days' WHERE id = $1`, sp.ID); err != nil {
		t.Fatalf("age decline: %v", err)
	}
	if !containsCandidate(deckOf(sender, declinerGender), decliner) {
		t.Fatalf("decliner still excluded after the %s cooldown", DeclineCooldown)
	}
	s.SetDeclineCooldown(40 * 24 * time.Hour)
	if containsCandidate(deckOf(sender, declinerGender), decliner) {
		t.Fatalf("a 40-day cooldown override does not exclude a 31-day-old decline")
	}
}

// The decliner sparking the declined sender AFTER the decline, on any item,
// lifts the cooldown for spark create, the deck and the mutual count. A spark
// made before the decline, or a repeat of it, does not.
func TestDeclineCooldown_LiftedOnlyByDeclinerSparkAfterDecline(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	declinerGender := "dl-" + uuid.NewString()[:8]
	sender, decliner := uuid.New(), uuid.New()
	seedDiscoverableProfile(t, s, sender, "dl-"+uuid.NewString()[:8])
	seedDiscoverableProfile(t, s, decliner, declinerGender)

	cooling := func() bool {
		t.Helper()
		ok, err := s.HasRecentDecline(ctx, sender, decliner)
		if err != nil {
			t.Fatalf("has recent decline: %v", err)
		}
		return ok
	}
	inDeck := func() bool {
		t.Helper()
		out, err := s.FetchCandidates(ctx, CandidateQuery{ViewerID: sender, GenderFilter: declinerGender, Limit: 50})
		if err != nil {
			t.Fatalf("fetch candidates: %v", err)
		}
		return containsCandidate(out, decliner)
	}
	senderCounts := func() bool {
		t.Helper()
		ok, err := s.HasReverseSparks(ctx, decliner, sender)
		if err != nil {
			t.Fatalf("has reverse sparks: %v", err)
		}
		return ok
	}

	// The decliner sparked the sender's photo an hour before the decline.
	early, err := s.CreateSpark(ctx, decliner, sender, "photo", "0", "")
	if err != nil {
		t.Fatalf("early spark: %v", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE dating_sparks SET created_at = now() - INTERVAL '1 hour' WHERE id = $1`, early.ID); err != nil {
		t.Fatalf("age early spark: %v", err)
	}
	sp, err := s.CreateSpark(ctx, sender, decliner, "photo", "0", "")
	if err != nil {
		t.Fatalf("spark: %v", err)
	}
	if _, err := s.DeclineSpark(ctx, sp.ID, decliner); err != nil {
		t.Fatalf("decline: %v", err)
	}
	// An undeclined spark from the sender, written straight to the store.
	if _, err := s.CreateSpark(ctx, sender, decliner, "prompt", "other", ""); err != nil {
		t.Fatalf("other sender spark: %v", err)
	}
	if !cooling() || inDeck() || senderCounts() {
		t.Fatalf("a spark made before the decline lifted it: cooling=%v inDeck=%v senderCounts=%v", cooling(), inDeck(), senderCounts())
	}
	// Repeating the pre-decline spark is not a new spark.
	if _, err := s.CreateSpark(ctx, decliner, sender, "photo", "0", ""); err != nil {
		t.Fatalf("repeat early spark: %v", err)
	}
	if !cooling() || inDeck() || senderCounts() {
		t.Fatalf("repeating a pre-decline spark lifted the decline")
	}

	// A spark on a different item after the decline lifts it.
	if _, err := s.CreateSpark(ctx, decliner, sender, "prompt", "p1", ""); err != nil {
		t.Fatalf("lifting spark: %v", err)
	}
	if cooling() {
		t.Fatalf("HasRecentDecline still true after the decliner sparked the sender")
	}
	if !inDeck() {
		t.Fatalf("decliner still off the sender's deck after the lift")
	}
	if !senderCounts() {
		t.Fatalf("the sender's undeclined spark does not count toward a match after the lift")
	}
	// The lift is one pair, one direction: nothing else changed.
	if ok, err := s.HasRecentDecline(ctx, decliner, sender); err != nil || ok {
		t.Fatalf("HasRecentDecline(decliner, sender) = %v, %v; want false", ok, err)
	}
}
