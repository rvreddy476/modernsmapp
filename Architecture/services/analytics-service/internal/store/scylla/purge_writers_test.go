package scylla

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reels-8: account erasure deletes watch_sessions (by the keys PostgreSQL
// holds) and viewer_history (by partition). reel_views and
// content_engagement hold a viewer id but cannot be found by it without a
// full scan, so the erasure relies on nothing writing them. This keeps it
// so: a new caller of either writer fails here until it comes with an
// erase in purge.go.
func TestUnerasableScyllaTablesHaveNoWriter(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		for _, call := range []string{".RecordReelView(", ".RecordEngagement("} {
			if strings.Contains(src, call) {
				offenders = append(offenders, path+": "+call)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("reel_views/content_engagement gained a writer with no erase path: %v", offenders)
	}
}
