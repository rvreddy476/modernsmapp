package service

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every claimant service declares its claimant and kinds as constants in its
// media client (`const Claimant = "..."` and `Kind... = "..."`). media-service
// refuses a claim it cannot name, and the claimant then refuses the user's
// write with 503, so a kind added there but not here breaks every save that
// carries media. This keeps the two in step across the repository.
var (
	claimantDecl = regexp.MustCompile(`(?m)^const Claimant = "([^"]+)"`)
	kindDecl     = regexp.MustCompile(`(?m)^\s*(?:const\s+)?Kind[A-Za-z0-9]*\s*=\s*"([^"]+)"`)
)

func TestEveryDeclaredClaimKindIsRegistered(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..") // repository root
	declared := map[string][]string{}
	for _, tree := range []string{"Architecture/services", "identity-platform/services", "chat-service/services"} {
		base := filepath.Join(root, tree)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "vendor" || strings.HasPrefix(d.Name(), "_archived") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			m := claimantDecl.FindSubmatch(src)
			if m == nil {
				return nil
			}
			claimant := string(m[1])
			for _, k := range kindDecl.FindAllSubmatch(src, -1) {
				declared[claimant] = append(declared[claimant], string(k[1]))
			}
			if len(declared[claimant]) == 0 {
				t.Errorf("%s declares claimant %q but no Kind constants", path, claimant)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
	if len(declared) < 2 {
		t.Fatalf("found %d claimant declarations; the scan is not reading the repository", len(declared))
	}

	for claimant, kinds := range declared {
		reg, ok := claimKinds[claimant]
		if !ok {
			t.Errorf("claimant %q is declared in its service but not registered in claimKinds", claimant)
			continue
		}
		for _, k := range kinds {
			if _, ok := reg[k]; !ok {
				t.Errorf("%s kind %q is declared in its service but not registered in claimKinds", claimant, k)
			}
		}
	}
	for claimant := range claimKinds {
		if _, ok := declared[claimant]; !ok {
			t.Errorf("claimKinds registers %q but no service declares it (stale entry)", claimant)
		}
	}
}
