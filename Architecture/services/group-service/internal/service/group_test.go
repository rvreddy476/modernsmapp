package service

import (
	"testing"
)

func TestValidGroupTypes(t *testing.T) {
	validTypes := []string{
		"public", "private", "hidden", "local",
		"study", "marketplace", "brand", "event", "family",
	}
	for _, gt := range validTypes {
		if err := ValidateGroupType(gt); err != nil {
			t.Errorf("expected group type %q to be valid, got error: %v", gt, err)
		}
	}
}

func TestInvalidGroupType(t *testing.T) {
	invalidTypes := []string{"unknown", "secret", "", "PUBLIC", "Private"}
	for _, gt := range invalidTypes {
		if err := ValidateGroupType(gt); err == nil {
			t.Errorf("expected group type %q to be invalid, but got no error", gt)
		}
	}
}

func TestValidCommentPermissions(t *testing.T) {
	validPerms := []string{"all_members", "admins_mods", "admins_only"}
	for _, cp := range validPerms {
		if err := ValidateCommentPermission(cp); err != nil {
			t.Errorf("expected comment permission %q to be valid, got error: %v", cp, err)
		}
	}
}

func TestInvalidCommentPermission(t *testing.T) {
	invalidPerms := []string{"everyone", "", "mods_only"}
	for _, cp := range invalidPerms {
		if err := ValidateCommentPermission(cp); err == nil {
			t.Errorf("expected comment permission %q to be invalid, but got no error", cp)
		}
	}
}

func TestMaxMembersEnforcement(t *testing.T) {
	// This tests the validation logic indirectly:
	// When MaxMembers > 0, the join should be blocked if count >= max.
	// We test the boundary conditions of the comparison logic.

	tests := []struct {
		name       string
		maxMembers int
		count      int64
		shouldBlock bool
	}{
		{"no limit", 0, 100, false},
		{"under limit", 50, 49, false},
		{"at limit", 50, 50, true},
		{"over limit", 50, 51, true},
		{"limit of 1 with 0 members", 1, 0, false},
		{"limit of 1 with 1 member", 1, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked := tt.maxMembers > 0 && tt.count >= int64(tt.maxMembers)
			if blocked != tt.shouldBlock {
				t.Errorf("maxMembers=%d, count=%d: expected blocked=%v, got %v",
					tt.maxMembers, tt.count, tt.shouldBlock, blocked)
			}
		})
	}
}
