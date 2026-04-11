package service

import "testing"

func TestNormalizeGrantedPermissions_AllowsRequestedSubset(t *testing.T) {
	requested := []string{"clipboard.write", "user.profile.read"}
	granted := []string{"user.profile.read", "clipboard.write", "clipboard.write"}

	normalized, err := normalizeGrantedPermissions(requested, granted)
	if err != nil {
		t.Fatalf("normalizeGrantedPermissions returned error: %v", err)
	}

	if len(normalized) != 2 {
		t.Fatalf("expected 2 permissions, got %d", len(normalized))
	}
	if normalized[0] != "user.profile.read" || normalized[1] != "clipboard.write" {
		t.Fatalf("unexpected normalized permissions: %#v", normalized)
	}
}

func TestNormalizeGrantedPermissions_RejectsUnrequestedPermission(t *testing.T) {
	requested := []string{"clipboard.write"}
	granted := []string{"device.camera"}

	_, err := normalizeGrantedPermissions(requested, granted)
	if err == nil {
		t.Fatal("expected invalid permissions error")
	}
	if err.Error() != "INVALID_PERMISSIONS" {
		t.Fatalf("unexpected error: %v", err)
	}
}
