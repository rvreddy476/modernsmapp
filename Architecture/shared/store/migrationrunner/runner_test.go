package migrationrunner

import (
	"reflect"
	"testing"
	"testing/fstest"
)

// Run() needs a live pgxpool.Pool, so the round-trip story belongs in an
// integration test (testcontainers — H6 follow-up). Here we pin the file-
// traversal contract so a refactor can't silently change which migrations
// are picked up or in what order.

func TestListMigrationFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/003_late.sql":   {Data: []byte("-- 003")},
		"migrations/001_first.sql":  {Data: []byte("-- 001")},
		"migrations/002_second.sql": {Data: []byte("-- 002")},
		"migrations/.gitkeep":       {Data: []byte("")},
		"migrations/README.md":      {Data: []byte("docs")},
		"migrations/subdir/x.sql":   {Data: []byte("nested")},
	}
	got, err := listMigrationFiles(fsys, "migrations")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"001_first.sql",
		"002_second.sql",
		"003_late.sql",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestListMigrationFilesMissingDirIsFine(t *testing.T) {
	fsys := fstest.MapFS{
		"setup.sql": {Data: []byte("-- setup")},
	}
	got, err := listMigrationFiles(fsys, "migrations")
	if err != nil {
		t.Fatalf("missing migrations/ should not error, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestListMigrationFilesEmptyDir(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/.gitkeep": {Data: []byte("")},
	}
	got, err := listMigrationFiles(fsys, "migrations")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty migrations dir should yield no files, got %v", got)
	}
}
