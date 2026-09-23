package service

import (
	"os"
	"strings"
	"testing"
)

/*
Adding people to a group must actually add them.

graph-service answers "can this person be put in a group" with TWO
permissions — directAdd and invite — and the group paths computed both, then
used them only as `!directAdd && !invite` to decide whether to refuse.
Everybody who got through was sent an invitation. So a person whose privacy
says people may add them straight in still had to accept one, the setting had
no effect, and a creator who picked three people watched their new group sit
at one member.

The batch also returned a bare error, so "invited everyone" and "skipped
everyone" were the same answer — nil — and a client could truthfully report
inviting people it had not invited.

These guards are structural: this package's tests have no database and no
graph-service, and what is worth protecting is that the two permissions lead
to two different actions and that the count comes back.
*/

func TestDirectAddPermissionActuallyAddsTheMember(t *testing.T) {
	body := funcSource(t, "group.go", "addOrInvite")

	if !strings.Contains(body, "canAddToGroup") {
		t.Fatal("addOrInvite no longer asks graph-service — a block would stop being honoured")
	}
	add := strings.Index(body, "AddMemberWithInviter")
	if add < 0 {
		t.Fatal("addOrInvite never adds a member: directAdd is being computed and thrown away, which is the bug where a group stays at one member however many people you pick")
	}
	inv := strings.Index(body, "CreateInvite")
	if inv < 0 {
		t.Fatal("addOrInvite never creates an invite, so anyone who only permits invitations cannot be reached at all")
	}
	if !strings.Contains(body, "case directAdd:") {
		t.Fatal("the direct-add branch is not keyed on directAdd — the two permissions must lead to two different actions")
	}
	// Order matters: directAdd is the stronger answer, so it must be tested
	// first. Checking invite first would send an invitation to someone who
	// permitted a direct add, which is the behaviour being fixed.
	if add > inv {
		t.Fatal("the invite branch precedes the direct-add branch, so someone who allows a direct add gets an invitation instead")
	}
}

// A blocked or refusing target must be skipped silently. Naming them would
// tell the inviter who blocked them, which is the thing a block hides.
func TestRefusedTargetIsSkippedWithoutSayingWhy(t *testing.T) {
	body := funcSource(t, "group.go", "addOrInvite")

	tail := body[strings.Index(body, "default:"):]
	if !strings.Contains(tail, "outcomeSkipped") {
		t.Fatal("a refused target must fall through to outcomeSkipped, not an error naming them")
	}

	batch := funcSource(t, "group.go", "InviteUsersBatch")
	if strings.Contains(batch, "ErrInviteNotPermitted") {
		t.Fatal("the batch returns a not-permitted error — one refusal in fifty would tell the inviter exactly who blocked them")
	}
	if !strings.Contains(batch, "result.Skipped++") {
		t.Fatal("the batch must count skips; returning nil whether it invited everyone or nobody lets a client report invites it never sent")
	}
}

// The wire carries totals, never names: added and invited as id lists would
// leak the skipped ones by subtraction.
func TestAddPeopleResultCarriesCountsNotNames(t *testing.T) {
	src := readSource(t, "group.go")
	decl := src[strings.Index(src, "type AddPeopleResult struct {"):]
	decl = decl[:strings.Index(decl, "}")]

	for _, field := range []string{"Added int", "Invited int", "Skipped int"} {
		if !strings.Contains(decl, field) {
			t.Fatalf("AddPeopleResult.%s must be a count — an id list tells the inviter which people were refused", field)
		}
	}
	if strings.Contains(decl, "uuid.UUID") {
		t.Fatal("AddPeopleResult carries user ids; pick three and get two back and you know exactly who the third was")
	}
}

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

