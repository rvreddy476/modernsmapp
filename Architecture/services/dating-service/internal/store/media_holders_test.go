package store

import (
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/mediaclient"
	"github.com/google/uuid"
)

// The claim protocol's per-holder serialisation must WAIT for the other
// writer and must end with the transaction.
func TestMediaHolderLockWaitsAndIsTransactionScoped(t *testing.T) {
	if lockMediaHolderSQL != `SELECT pg_advisory_xact_lock($1)` {
		t.Fatalf("lock statement is %q; want the waiting, exclusive, transaction-scoped advisory lock", lockMediaHolderSQL)
	}
}

func TestMediaHolderLockKeyIsPerHolder(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	if mediaHolderLockKey(mediaclient.KindPhoto, a) != mediaHolderLockKey(mediaclient.KindPhoto, a) {
		t.Fatal("unstable key")
	}
	if mediaHolderLockKey(mediaclient.KindPhoto, a) == mediaHolderLockKey(mediaclient.KindPhoto, b) ||
		mediaHolderLockKey(mediaclient.KindVerificationSelfie, a) == mediaHolderLockKey(mediaclient.KindSelfieAttempt, a) {
		t.Fatal("two holders share a lock key")
	}
}

// Every kind media-service registers for dating-service is walked by the
// reconcile tool, with a page and a re-read query of its own column.
func TestEveryHolderKindIsReconciled(t *testing.T) {
	want := map[string]string{
		mediaclient.KindPhoto:              "dating_photos",
		mediaclient.KindPromptClip:         "clip_media_id",
		mediaclient.KindSelfieAttempt:      "video_media_id",
		mediaclient.KindVerificationSelfie: "selfie_media_id",
	}
	if len(MediaHolderKinds) != len(want) {
		t.Fatalf("MediaHolderKinds = %v", MediaHolderKinds)
	}
	for _, k := range MediaHolderKinds {
		q, ok := holderQueries[k]
		if !ok || !strings.Contains(q.page, want[k]) || !strings.Contains(q.one, want[k]) {
			t.Errorf("%s: page %q / re-read %q do not read %s", k, q.page, q.one, want[k])
		}
	}
}

func TestOptionalID(t *testing.T) {
	if optionalID(uuid.Nil) != nil {
		t.Fatal("uuid.Nil must be NULL so the column default applies")
	}
	id := uuid.New()
	if got := optionalID(id); got == nil || *got != id {
		t.Fatal("a chosen id was not kept")
	}
}
