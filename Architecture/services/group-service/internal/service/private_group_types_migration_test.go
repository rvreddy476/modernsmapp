package service

import (
	"os"
	"strings"
	"testing"
)

// Migration 017 heals exactly the group types that CreateGroup makes
// private (groupTypeNeedsPrivacy), and sets them the way CreateGroup does.
// The DB-backed run is store/private_group_types_migration_integration_test.go.
func TestMigration017MatchesCreateGroup(t *testing.T) {
	raw, err := os.ReadFile("../../database/migrations/017_private_group_types_are_private.sql")
	if err != nil {
		t.Fatal(err)
	}
	var code []string
	for _, line := range strings.Split(string(raw), "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		code = append(code, line)
	}
	sql := strings.Join(strings.Fields(strings.Join(code, "\n")), " ")

	// Every type the groups_group_type_check admits (migration 004).
	allTypes := []string{"public", "private", "hidden", "local", "study", "marketplace", "brand", "event", "family"}
	var private []string
	for _, ty := range allTypes {
		if groupTypeNeedsPrivacy(ty) {
			private = append(private, "'"+ty+"'")
		}
	}
	privacy, err := privacyForGroupType("hidden", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"SET privacy_level = '" + privacy + "',",
		"visibility = 'private',",
		"join_mode = 'invite_only',",
		"WHERE group_type IN (" + strings.Join(private, ", ") + ")",
		"privacy_level IS DISTINCT FROM 'private'",
		"OR visibility IS DISTINCT FROM 'private'",
		"OR join_mode IS DISTINCT FROM 'invite_only'",
	} {
		if !strings.Contains(sql, must) {
			t.Errorf("migration 017 lost %q:\n%s", must, sql)
		}
	}
	if err := ValidatePrivacyJoinCombo("private", "invite_only"); err != nil {
		t.Fatalf("migration 017 writes a combination CreateGroup refuses: %v", err)
	}
}
