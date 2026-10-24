package peers

import (
	"strings"
	"testing"
)

// Hidden chats: a peer who hid the DM gets no presence pushes about it.
func TestPresencePeersExcludeAPeerWhoHidTheDM(t *testing.T) {
	if !strings.Contains(dmPeersSQL, "AND NOT other.locked") {
		t.Fatal("presence fan-out would reach a peer who hid the DM")
	}
}
