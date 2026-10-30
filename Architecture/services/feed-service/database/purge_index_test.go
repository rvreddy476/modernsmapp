package database

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Audit L11: the purge's `OR author_id = $1` halves have an index to use.
func TestPurgeTablesHaveAuthorIndexes(t *testing.T) {
	var all strings.Builder
	all.WriteString(SetupSQL)
	err := fs.WalkDir(Migrations, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(Migrations, path)
		all.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(all.String())
	for _, table := range []string{"user_interactions", "feed_feedback", "feed_author_feedback"} {
		re := regexp.MustCompile(`create index if not exists \w+ on ` + table + ` \(author_id\)`)
		if !re.MatchString(sql) {
			t.Errorf("%s has no index leading with author_id: every purge scans the table", table)
		}
	}
}
