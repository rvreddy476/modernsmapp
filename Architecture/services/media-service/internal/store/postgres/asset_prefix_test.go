package postgres

import (
	"testing"

	"github.com/google/uuid"
)

// Reels audit 2026-10-30, finding 8: a prefix reclaim deletes everything
// under it, so only one asset's exact prefix ever qualifies.
func TestAssetPrefixIsExactlyOneAsset(t *testing.T) {
	u, m := uuid.NewString(), uuid.NewString()
	prefix, ok := AssetPrefixForKey("user/" + u + "/" + m + "/original")
	if !ok || prefix != "user/"+u+"/"+m+"/" {
		t.Fatalf("prefix = %q, %v", prefix, ok)
	}
	for _, key := range []string{
		"user/test/asset/original", "user/" + u + "/original", "user/" + u + "/" + m + "/original.mp4",
		"public/" + u + "/" + m + "/original", "user/" + u + "/" + m + "/x/original",
	} {
		if p, ok := AssetPrefixForKey(key); ok {
			t.Fatalf("%q gave prefix %q", key, p)
		}
	}
	for _, broad := range []string{"", "/", "user/", "user/" + u + "/", "user/" + u + "/" + m,
		"user/" + u + "/" + m + "/hls/", "user/x/y/", "user/{" + u[:34] + "}/" + m + "/"} {
		if IsAssetPrefix(broad) {
			t.Fatalf("%q accepted as one asset's prefix", broad)
		}
	}
}
