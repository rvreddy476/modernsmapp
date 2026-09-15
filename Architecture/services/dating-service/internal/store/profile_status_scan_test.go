package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// profileStatusWriterAllowlist names the only non-test Go file allowed to
// write the lifecycle columns of dating_profiles (profile_status,
// prior_status, paused). Everything else must call TransitionProfileStatus,
// which validates the edge and the actor, checks onboarding evidence, and
// writes guarded on the state it locked.
//
// Keys are paths relative to the service root (internal/..., cmd/...).
var profileStatusWriterAllowlist = map[string]string{
	"internal/store/profile_status.go": "TransitionProfileStatus is the guarded writer",
}

var (
	updateProfilesRe = regexp.MustCompile(`(?is)\bUPDATE\s+dating_profiles\b(?:\s+(?:AS\s+)?[a-z_]+)?\s+SET\b(.*)`)
	insertProfilesRe = regexp.MustCompile(`(?is)\bINSERT\s+INTO\s+dating_profiles\b(.*)`)
	profileWhereRe   = regexp.MustCompile(`(?is)\bWHERE\b`)
	// profile_status = ..., prior_status = ..., paused = ...; p.paused = counts.
	lifecycleAssignRe = regexp.MustCompile(`(?i)(?:^|[\s,(.])(?:profile_status|prior_status|paused)\s*=`)
	lifecycleColumnRe = regexp.MustCompile(`(?i)\b(?:profile_status|prior_status|paused)\b`)
)

// TestNoRawProfileStatusWrites fails when any non-test Go file under
// internal/ or cmd/ holds a SQL literal that writes a lifecycle column of
// dating_profiles outside the allowlist, or names profile_status /
// prior_status as a bare string (the dynamic column list in UpsertProfile).
func TestNoRawProfileStatusWrites(t *testing.T) {
	serviceRoot := filepath.Join("..", "..")
	fset := token.NewFileSet()
	allowedHits := 0
	var offenders []string

	for _, dir := range []string{"internal", "cmd"} {
		root := filepath.Join(serviceRoot, dir)
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(serviceRoot, path)
			rel = filepath.ToSlash(rel)
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				lit, ok := node.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				sql, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if !writesProfileLifecycle(sql) {
					return true
				}
				if _, ok := profileStatusWriterAllowlist[rel]; ok {
					allowedHits++
					return true
				}
				offenders = append(offenders, fset.Position(lit.Pos()).String())
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if allowedHits == 0 {
		t.Fatalf("scanner matched no allowlisted writer at all; the pattern is broken")
	}
	if len(offenders) > 0 {
		t.Fatalf("raw dating_profiles lifecycle writes outside TransitionProfileStatus:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// TestProfileStatusWriterAllowlistIsOnlyTheGuard pins the allowlist itself.
func TestProfileStatusWriterAllowlistIsOnlyTheGuard(t *testing.T) {
	if len(profileStatusWriterAllowlist) != 1 {
		t.Fatalf("allowlist = %v, want only internal/store/profile_status.go", profileStatusWriterAllowlist)
	}
	if _, ok := profileStatusWriterAllowlist["internal/store/profile_status.go"]; !ok {
		t.Fatalf("allowlist = %v, want only internal/store/profile_status.go", profileStatusWriterAllowlist)
	}
}

func writesProfileLifecycle(sql string) bool {
	switch strings.ToLower(strings.TrimSpace(sql)) {
	case "profile_status", "prior_status":
		return true
	}
	if m := updateProfilesRe.FindStringSubmatch(sql); m != nil {
		set := m[1]
		if loc := profileWhereRe.FindStringIndex(set); loc != nil {
			set = set[:loc[0]]
		}
		if lifecycleAssignRe.MatchString(set) {
			return true
		}
	}
	if m := insertProfilesRe.FindStringSubmatch(sql); m != nil && lifecycleColumnRe.MatchString(m[1]) {
		return true
	}
	return false
}

func TestWritesProfileLifecyclePattern(t *testing.T) {
	cases := map[string]bool{
		`UPDATE dating_profiles SET profile_status = 'active' WHERE user_id = $1`:                   true,
		"UPDATE dating_profiles\n\t\tSET paused = $2, profile_status = $3 WHERE user_id = $1":       true,
		`UPDATE dating_profiles SET prior_status = NULL`:                                            true,
		`UPDATE dating_profiles p SET p.paused = true`:                                              true,
		`UPDATE dating_profiles SET updated_at = now(), paused = true WHERE user_id = $1`:           true,
		`INSERT INTO dating_profiles (user_id, profile_status) VALUES ($1, 'active')`:              true,
		`INSERT INTO dating_profiles (user_id) VALUES ($1) ON CONFLICT (user_id) DO UPDATE SET paused = true`: true,
		`profile_status`: true,
		`UPDATE dating_profiles SET trust_tier = $2 WHERE user_id = $1 AND profile_status = 'active'`: false,
		`UPDATE dating_profiles SET blur_mode = $2 WHERE paused = false`:                              false,
		`INSERT INTO dating_profiles (user_id, intent, cohort_salt) VALUES ($1, $2, $3)`:             false,
		`SELECT profile_status, paused FROM dating_profiles WHERE user_id = $1`:                      false,
		`UPDATE dating_matches SET status = 'closed'`:                                                 false,
		`paused`: false,
	}
	for sql, want := range cases {
		if got := writesProfileLifecycle(sql); got != want {
			t.Errorf("writesProfileLifecycle(%q) = %v, want %v", sql, got, want)
		}
	}
}
