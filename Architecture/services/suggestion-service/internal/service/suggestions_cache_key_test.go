package service

import (
	"os"
	"strings"
	"testing"
)

// The read, the write and the clear must use one key. A surface in the read
// key used to make every request that named a surface miss the cache.
func TestSuggestionsCacheKeyIsSharedAndSurfaceFree(t *testing.T) {
	src, err := os.ReadFile("suggestion.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, `"suggestions:%s:%s:%s"`) {
		t.Fatal("a surface-specific suggestions cache key is back; cacheItems never writes it")
	}
	if n := strings.Count(s, "suggestionsCacheKey(viewerID, suggType)"); n < 3 {
		t.Fatalf("read, write and clear must all use suggestionsCacheKey; found %d uses", n)
	}
}
