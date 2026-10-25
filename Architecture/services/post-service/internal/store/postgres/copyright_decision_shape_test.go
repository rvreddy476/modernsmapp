package postgres

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/post-service/database"
)

// The copyright decision-shape CHECK is evaluated by Postgres, so its
// integration proof (TestOwnerBlockIT_AutoBlockAtPublication) runs only in
// CI. This guard reads the constraint as the embedded chain leaves it (the
// LAST migration that adds it wins) and refuses the defect 075 shipped: a
// three-valued `reason = '...'` comparison, which is NULL for a NULL reason,
// and a CHECK passes on NULL — so a block with no reviewer and no reason was
// accepted. The comparison must be two-valued (IS NOT DISTINCT FROM).

var (
	reDecisionShapeAdd = regexp.MustCompile(`(?is)ADD\s+CONSTRAINT\s+copyright_title_reviews_decision_shape\s+CHECK\s*\(`)
	reNullableReasonEq = regexp.MustCompile(`(?i)\breason\s*(=|<>|!=|\bIN\b)`)
)

// lastDecisionShape returns the CHECK body of the last migration that adds
// copyright_title_reviews_decision_shape, and that migration's name.
func lastDecisionShape(t *testing.T) (string, string) {
	t.Helper()
	entries, err := fs.ReadDir(database.Migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var body, from string
	for _, n := range names {
		raw, err := fs.ReadFile(database.Migrations, "migrations/"+n)
		if err != nil {
			t.Fatal(err)
		}
		sql := reSQLComment.ReplaceAllString(string(raw), "")
		locs := reDecisionShapeAdd.FindAllStringIndex(sql, -1)
		if len(locs) == 0 {
			continue
		}
		rest := sql[locs[len(locs)-1][1]:]
		depth := 1
		for i, r := range rest {
			if r == '(' {
				depth++
			} else if r == ')' {
				depth--
				if depth == 0 {
					body, from = rest[:i], n
					break
				}
			}
		}
	}
	if body == "" {
		t.Fatal("no migration adds copyright_title_reviews_decision_shape")
	}
	return body, from
}

func TestCopyrightDecisionShapeRefusesANullReason(t *testing.T) {
	body, from := lastDecisionShape(t)
	if m := reNullableReasonEq.FindString(body); m != "" {
		t.Fatalf("%s: the decision-shape CHECK compares reason with %q, which is NULL for a NULL reason and so passes; use IS NOT DISTINCT FROM", from, m)
	}
	norm := strings.Join(strings.Fields(strings.ToLower(body)), " ")
	if !strings.Contains(norm, "reason is not distinct from 'owner_registered'") {
		t.Fatalf("%s: the system-block branch no longer reads reason IS NOT DISTINCT FROM 'owner_registered': %s", from, norm)
	}
	if !strings.Contains(norm, "decided_by is not null or reason is not distinct from 'owner_registered'") {
		t.Fatalf("%s: a block must need a reviewer or the system's owner_registered: %s", from, norm)
	}
}
