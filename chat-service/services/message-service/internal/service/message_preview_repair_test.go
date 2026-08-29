package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
)

// MP-LB-1: a successful deletion must never leave deleted plaintext in the
// inbox preview indefinitely. These tests walk every failure boundary of the
// durable-obligation design: obligation-before-delete ordering, each failure
// mode leaving the obligation pending, the worker's recovery, the write-ahead
// live-message case, and the guard against overwriting newer previews.

type previewFixture struct {
	sender  uuid.UUID
	convID  uuid.UUID
	msgID   uuid.UUID
	ts      time.Time
	conv    *previewConvStore
	msgs    *previewMsgStore
	service *Service
}

func newPreviewFixture(t *testing.T) *previewFixture {
	t.Helper()
	fx := &previewFixture{
		sender: uuid.New(),
		convID: uuid.New(),
		msgID:  uuid.New(),
		ts:     time.Now().UTC().Truncate(time.Millisecond),
	}
	f := newGovernanceFake()
	f.conversations[fx.convID] = &postgres.Conversation{ID: fx.convID, Type: "direct"}
	f.roles[fx.convID] = map[uuid.UUID]string{fx.sender: "member"}
	fx.conv = newPreviewConvStore(f)
	fx.conv.currentLastMessageAt = fx.ts
	fx.msgs = &previewMsgStore{
		target: &scylla.Message{
			ConversationID: fx.convID, SenderID: fx.sender,
			Ts: fx.ts, Text: "deleted plaintext",
		},
		surviving: []scylla.Message{{
			ConversationID: fx.convID, SenderID: fx.sender,
			Ts: fx.ts.Add(-time.Minute), Text: "the survivor",
		}},
	}
	fx.service = newPreviewService(fx.conv, fx.msgs)
	return fx
}

func (fx *previewFixture) deleteMessage(t *testing.T) error {
	t.Helper()
	return fx.service.DeleteMessage(
		context.Background(), fx.sender, fx.convID, fx.msgID, "202608", fx.ts,
	)
}

func TestObligationIsDurableBeforeTheDeleteAndCompletedAfterRepair(t *testing.T) {
	fx := newPreviewFixture(t)
	var journal []string
	fx.conv.journal = &journal
	fx.msgs.journal = &journal

	if err := fx.deleteMessage(t); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// WRITE-AHEAD: the durable obligation must exist before Scylla mutates.
	if len(journal) < 2 || journal[0] != "obligation" || journal[1] != "scylla-delete" {
		t.Fatalf("obligation was not durably recorded before the delete: %v", journal)
	}
	if !fx.conv.applied || fx.conv.lastPreview != "the survivor" {
		t.Fatalf("inline repair did not rewrite the preview: applied=%v preview=%q",
			fx.conv.applied, fx.conv.lastPreview)
	}
	if len(fx.conv.obligations) != 0 {
		t.Fatal("resolved obligation was not completed")
	}
}

func TestObligationCreateFailurePreventsDeletion(t *testing.T) {
	fx := newPreviewFixture(t)
	fx.conv.createObligationErr = errors.New("postgres down")

	if err := fx.deleteMessage(t); err == nil {
		t.Fatal("deletion succeeded without a durable repair obligation")
	}
	if fx.msgs.deletes != 0 {
		t.Fatal("the message was deleted even though the obligation could not be recorded")
	}
	if fx.msgs.target.IsDeleted {
		t.Fatal("the message must remain live when the obligation write fails")
	}
}

// The crash boundary: the Scylla delete succeeded, then the process died (here:
// the inline survivor read fails, leaving exactly the state a crash leaves —
// the durable obligation and a deleted message). Restarting the worker must
// finish the repair.
func TestCrashAfterDeleteIsRepairedByTheWorkerAfterRestart(t *testing.T) {
	fx := newPreviewFixture(t)
	fx.msgs.getPageErr = errors.New("process died here")

	if err := fx.deleteMessage(t); err != nil {
		t.Fatalf("delete must report success once the message is deleted: %v", err)
	}
	if fx.conv.applied {
		t.Fatal("fixture error: inline repair should have failed")
	}
	if len(fx.conv.obligations) != 1 {
		t.Fatalf("the durable obligation must survive the crash, found %d", len(fx.conv.obligations))
	}

	// "Restart": the failure clears and a worker pass runs.
	fx.msgs.getPageErr = nil
	if err := fx.service.runPreviewRepairPass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}

	if !fx.conv.applied || fx.conv.lastPreview != "the survivor" {
		t.Fatalf("worker did not repair the preview: applied=%v preview=%q",
			fx.conv.applied, fx.conv.lastPreview)
	}
	if fx.conv.appliedCount != 1 {
		t.Fatalf("repair must land exactly once, landed %d times", fx.conv.appliedCount)
	}
	if len(fx.conv.obligations) != 0 {
		t.Fatal("repaired obligation was not completed")
	}
}

func TestPostgresWriteFailureKeepsTheObligationPendingUntilRepairedOnce(t *testing.T) {
	fx := newPreviewFixture(t)
	fx.conv.replaceErr = errors.New("postgres write timeout")

	if err := fx.deleteMessage(t); err != nil {
		t.Fatalf("delete must report success once the message is deleted: %v", err)
	}
	if len(fx.conv.obligations) != 1 {
		t.Fatal("obligation must stay pending after the projection write failed")
	}

	// A worker pass while PostgreSQL still fails: pending, with backoff.
	if err := fx.service.runPreviewRepairPass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if len(fx.conv.obligations) != 1 {
		t.Fatal("obligation must remain pending while the write keeps failing")
	}
	if fx.conv.deferCalls == 0 {
		t.Fatal("a failed attempt must be deferred with backoff, not dropped")
	}

	// PostgreSQL recovers: the next pass repairs EXACTLY once and completes.
	fx.conv.replaceErr = nil
	if err := fx.service.runPreviewRepairPass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if fx.conv.appliedCount != 1 {
		t.Fatalf("repair must land exactly once, landed %d times", fx.conv.appliedCount)
	}
	if len(fx.conv.obligations) != 0 {
		t.Fatal("repaired obligation was not completed")
	}
}

func TestScyllaSurvivorReadFailureKeepsTheObligationPending(t *testing.T) {
	fx := newPreviewFixture(t)
	// Restart-resumption shape: the obligation and the deleted message exist;
	// no inline attempt ran (the process died before it).
	fx.msgs.target.IsDeleted = true
	fx.conv.obligations[fx.msgID] = postgres.PreviewRepairObligation{
		MessageID: fx.msgID, ConversationID: fx.convID,
		Bucket: "202608", DeletedTs: fx.ts, CreatedAt: time.Now(),
	}
	fx.msgs.getPageErr = errors.New("scylla unavailable")

	if err := fx.service.runPreviewRepairPass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if len(fx.conv.obligations) != 1 {
		t.Fatal("obligation must remain pending while the survivor read fails")
	}
	if fx.conv.applied {
		t.Fatal("no rewrite may happen on a failed survivor read")
	}

	fx.msgs.getPageErr = nil
	if err := fx.service.runPreviewRepairPass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if !fx.conv.applied || len(fx.conv.obligations) != 0 {
		t.Fatalf("recovered pass must repair and complete: applied=%v pending=%d",
			fx.conv.applied, len(fx.conv.obligations))
	}
}

// The write-ahead residue: the obligation committed but the Scylla delete
// never happened. The message is LIVE — the worker must never remove or
// replace its preview, must wait while the deleting request could still be in
// flight, and may retire the obligation only once age proves no deletion
// occurred.
func TestLiveMessageObligationNeverTouchesThePreview(t *testing.T) {
	fx := newPreviewFixture(t)
	fx.conv.obligations[fx.msgID] = postgres.PreviewRepairObligation{
		MessageID: fx.msgID, ConversationID: fx.convID,
		Bucket: "202608", DeletedTs: fx.ts, CreatedAt: time.Now(), // fresh
	}

	// Young obligation: possibly an in-flight delete — defer, touch nothing.
	if err := fx.service.runPreviewRepairPass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if fx.conv.replaceCalls != 0 {
		t.Fatal("a live message's preview was touched")
	}
	if len(fx.conv.obligations) != 1 {
		t.Fatal("a young live-message obligation must stay pending, not be retired")
	}

	// Old obligation: deletion demonstrably never happened — retire quietly.
	o := fx.conv.obligations[fx.msgID]
	o.CreatedAt = time.Now().Add(-time.Hour)
	fx.conv.obligations[fx.msgID] = o
	if err := fx.service.runPreviewRepairPass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if fx.conv.replaceCalls != 0 {
		t.Fatal("retiring a live-message obligation must not rewrite anything")
	}
	if len(fx.conv.obligations) != 0 {
		t.Fatal("a proven-undeleted obligation must be retired")
	}
	if fx.msgs.target.IsDeleted {
		t.Fatal("fixture invariant: the message must still be live")
	}
}

// Concurrent delivery: by the time the repair runs, a NEWER message owns the
// preview. The guard makes the rewrite a no-op and the obligation resolves.
func TestRepairNeverOverwritesANewerPreview(t *testing.T) {
	fx := newPreviewFixture(t)
	fx.msgs.target.IsDeleted = true
	fx.conv.obligations[fx.msgID] = postgres.PreviewRepairObligation{
		MessageID: fx.msgID, ConversationID: fx.convID,
		Bucket: "202608", DeletedTs: fx.ts, CreatedAt: time.Now(),
	}
	// A newer message replaced the preview while the repair was pending.
	fx.conv.currentLastMessageAt = fx.ts.Add(30 * time.Second)

	if err := fx.service.runPreviewRepairPass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if fx.conv.applied {
		t.Fatal("repair overwrote a newer preview")
	}
	if len(fx.conv.obligations) != 0 {
		t.Fatal("a no-op repair still resolves the obligation")
	}
}

func TestReplayedDeletionIsIdempotent(t *testing.T) {
	fx := newPreviewFixture(t)

	if err := fx.deleteMessage(t); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := fx.deleteMessage(t); err == nil {
		t.Fatal("replaying a completed deletion must report message-not-found, not re-delete")
	}
	if fx.msgs.deletes != 1 {
		t.Fatalf("replay re-deleted: %d deletes", fx.msgs.deletes)
	}
	if len(fx.conv.obligations) != 0 {
		t.Fatal("replay left a stray obligation")
	}
	if fx.conv.appliedCount != 1 {
		t.Fatalf("replay repaired again: %d applies", fx.conv.appliedCount)
	}
}
