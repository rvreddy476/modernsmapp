package service

// P0-6 correction-pass guard: no product surface may claim end-to-end
// encryption while none exists (directive §3.7). The independent review
// found shipped iOS screens claiming "encrypted on-device with zero server
// logs" and "Verify End-to-End Encryption". This test scans the mobile
// product source trees for AFFIRMATIVE encryption claims and fails the Go
// gate when one appears. Lines that state the ABSENCE of E2EE (the honest
// disclosures this pass added) carry a negation and are exempt.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var forbiddenClaimPhrases = []string{
	"end-to-end encrypt",
	"encrypted end-to-end",
	"encrypted on-device",
	"zero server logs",
	"only you can read",
	"type encrypted",
}

var negationMarkers = []string{
	"not ", "n't ", "no ", "never", "until", "yet", "forbid", "absent", "isn't", "aren't",
}

func lineMakesAffirmativeClaim(line string) (string, bool) {
	// Comments are not product surfaces — the claims that matter are the
	// ones a user can see (string literals, resource text).
	if strings.HasPrefix(strings.TrimSpace(line), "//") ||
		strings.HasPrefix(strings.TrimSpace(line), "*") ||
		strings.HasPrefix(strings.TrimSpace(line), "<!--") {
		return "", false
	}
	lower := strings.ToLower(line)
	for _, phrase := range forbiddenClaimPhrases {
		if !strings.Contains(lower, phrase) {
			continue
		}
		negated := false
		for _, neg := range negationMarkers {
			if strings.Contains(lower, neg) {
				negated = true
				break
			}
		}
		if !negated {
			return phrase, true
		}
	}
	return "", false
}

func TestNoProductSurfaceClaimsE2EE(t *testing.T) {
	root := repoRootFromService(t)
	scanRoots := []string{
		filepath.Join(root, "mobile", "ios"),
		filepath.Join(root, "mobile", "android"),
	}
	skipDirs := map[string]bool{
		"build": true, ".gradle": true, "node_modules": true, ".git": true,
	}
	exts := map[string]bool{".swift": true, ".kt": true, ".xml": true}

	for _, scanRoot := range scanRoots {
		if _, err := os.Stat(scanRoot); err != nil {
			continue
		}
		err := filepath.WalkDir(scanRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !exts[filepath.Ext(path)] {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if phrase, bad := lineMakesAffirmativeClaim(line); bad {
					t.Errorf("%s:%d makes an affirmative E2EE claim (%q) — forbidden until CH-LB-5 passes:\n  %s",
						path, i+1, phrase, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func repoRootFromService(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "mobile")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("repository root with mobile/ not found — guard runs only in the full checkout")
	return ""
}
