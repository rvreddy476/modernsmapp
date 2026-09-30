package http

// The golden fixtures and the tests that render them are one set.
//
// Runs without a database, so it is in the ordinary `go test ./...` sweep:
// a fixture file that no test renders is a stale contract a client may copy,
// and a registered fixture with no file is a contract nobody can copy. Both
// fail here, by name.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ctRegisteredRe finds every `name: "area/route_status"` registration in the
// integration-tagged fixture list, which this untagged test cannot import.
var ctRegisteredRe = regexp.MustCompile(`name:\s*"([a-z_]+/[a-z0-9_]+)"`)

func TestContractInventoryMatchesTheFixtureList(t *testing.T) {
	src, err := os.ReadFile("contracts_fixtures_integration_test.go")
	if err != nil {
		t.Fatalf("the fixture list is missing: %v", err)
	}
	registered := map[string]bool{}
	for _, m := range ctRegisteredRe.FindAllStringSubmatch(string(src), -1) {
		if registered[m[1]] {
			t.Errorf("fixture %q is registered twice", m[1])
		}
		registered[m[1]] = true
	}
	if len(registered) == 0 {
		t.Fatal("no fixtures are registered")
	}

	onDisk := map[string]bool{}
	err = filepath.WalkDir(contractsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		rel, _ := filepath.Rel(contractsDir, path)
		onDisk[strings.TrimSuffix(filepath.ToSlash(rel), ".json")] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", contractsDir, err)
	}

	for name := range registered {
		if !onDisk[name] {
			t.Errorf("%s is registered but %s/%s.json does not exist (run the integration test with -update)", name, contractsDir, name)
		}
	}
	for name := range onDisk {
		if !registered[name] {
			t.Errorf("%s/%s.json exists but no fixture renders it; delete the file or register it", contractsDir, name)
		}
	}

	// The file name states the status; the body of an error fixture states
	// its code. Both are what a client keys on, so a fixture whose name says
	// 4xx must carry an error envelope and a 2xx must not.
	for name := range onDisk {
		body, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		status := ctStatusFromName(name)
		if status == 0 {
			t.Errorf("%s: the file name carries no HTTP status", name)
			continue
		}
		if status == 204 {
			if len(body) != 0 {
				t.Errorf("%s: a 204 fixture must be an empty file", name)
			}
			continue
		}
		if len(body) == 0 || body[len(body)-1] != '\n' {
			t.Errorf("%s: fixture must end with a newline", name)
		}
		hasError := strings.Contains(string(body), `"error": {`)
		if status >= 400 && !hasError {
			t.Errorf("%s: a %d fixture must carry an error envelope", name, status)
		}
		if status < 400 && hasError {
			t.Errorf("%s: a %d fixture must not carry an error envelope", name, status)
		}
	}

	names := make([]string, 0, len(registered))
	for n := range registered {
		names = append(names, n)
	}
	sort.Strings(names)
	t.Logf("%d contract fixtures", len(names))
}
