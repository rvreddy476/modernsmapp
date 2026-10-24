package chatdb

import (
	"context"
	"strings"
	"testing"
)

// The hidden-chat check reads the callee's own ACTIVE membership lock, on the
// call's source chat or the pair's DM.
func TestCallHiddenSQLReadsTheCalleesLock(t *testing.T) {
	for _, want := range []string{"cm.user_id = $1", "cm.locked", "cm.left_at IS NULL", "cm.conversation_id = $2::uuid", "chat.direct_conversation_keys"} {
		if !strings.Contains(callHiddenSQL, want) {
			t.Fatalf("callHiddenSQL lost %q", want)
		}
	}
}

func TestHiddenForWithoutAPoolHidesNothing(t *testing.T) {
	var m *MuteReader
	if m.HiddenFor(context.Background(), [16]byte{}, [16]byte{}, nil) {
		t.Fatal("nil reader must answer false")
	}
}
