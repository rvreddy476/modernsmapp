package service

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Doorstep (home services, 4 Oct 2026): the Android launcher tile and the
// onboarding picker send module id home_services.

func TestModules_HomeServicesIsKnown(t *testing.T) {
	mods, home, err := normalizeModulePreferences([]string{" Home_Services ", "reels"}, "home_services")
	if err != nil {
		t.Fatalf("home_services refused: %v", err)
	}
	if !reflect.DeepEqual(mods, []string{"home_services", "reels"}) || home != "home_services" {
		t.Fatalf("got %v home %q", mods, home)
	}
	// Home must still be one of the CHOSEN modules.
	if _, _, err := normalizeModulePreferences([]string{"reels"}, "home_services"); !errors.Is(err, ErrInvalidHomeModule) {
		t.Fatalf("home_services as home without choosing it: err = %v, want %v", err, ErrInvalidHomeModule)
	}
	// Near misses stay unknown.
	for _, bad := range []string{"home-services", "homeservices", "doorstep", "home_service"} {
		if _, _, err := normalizeModulePreferences([]string{bad}, "feed"); !errors.Is(err, ErrInvalidModule) {
			t.Fatalf("%q: err = %v, want %v", bad, err, ErrInvalidModule)
		}
	}
	// A fresh account has it on, like every other module.
	found := false
	for _, m := range defaultModulePreferences([16]byte{1}).Modules {
		found = found || m == "home_services"
	}
	if !found {
		t.Fatal("home_services is not in the defaults")
	}
}

// The CHECK constraints in database/setup.sql (the CREATE TABLE ones and the
// idempotent drop/re-add) carry exactly knownModules, so the database can
// never refuse a module the service accepts, or store one it does not.
func TestModules_SetupSQLMatchesKnownModules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "database", "setup.sql"))
	if err != nil {
		t.Fatalf("read setup.sql: %v", err)
	}
	sql := string(raw)
	quoted := regexp.MustCompile(`'([a-z_]+)'`)
	list := func(s string) []string {
		var out []string
		for _, m := range quoted.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
		return out
	}
	arrays := regexp.MustCompile(`modules <@ ARRAY\[([^\]]*)\]::TEXT\[\]`).FindAllStringSubmatch(sql, -1)
	homes := regexp.MustCompile(`home_module IN \(([^)]*)\)`).FindAllStringSubmatch(sql, -1)
	if len(arrays) != 2 || len(homes) != 2 {
		t.Fatalf("found %d modules CHECKs and %d home_module CHECKs, want 2 of each (CREATE + re-add)", len(arrays), len(homes))
	}
	wantHome := append([]string{HomeModuleFeed}, knownModules...)
	for i := range arrays {
		if got := list(arrays[i][1]); !reflect.DeepEqual(got, knownModules) {
			t.Errorf("modules CHECK %d = %v, want %v", i, got, knownModules)
		}
		if got := list(homes[i][1]); !reflect.DeepEqual(got, wantHome) {
			t.Errorf("home_module CHECK %d = %v, want %v", i, got, wantHome)
		}
	}
	// The re-add is idempotent: each ADD is preceded by its DROP IF EXISTS.
	for _, c := range []string{"module_preferences_modules_known", "module_preferences_home_module_known"} {
		drop := strings.Index(sql, "DROP CONSTRAINT IF EXISTS "+c)
		add := strings.Index(sql, "ADD CONSTRAINT "+c)
		if drop < 0 || add < 0 || drop > add {
			t.Errorf("%s: DROP IF EXISTS at %d, ADD at %d", c, drop, add)
		}
	}
}
