//go:build integration

package http

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pgstore "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Chat parity fix pass 2 end to end (lane M): mark unread, Giphy stickers,
// media.sensitive, the requests' sender block, conversation reports. Same
// rig as TestParityEndToEnd (a *_test database and keyspace, httptest
// fakes); writes / asserts the fix-pass-2 golden fixtures.
func TestParityFixPass2(t *testing.T) {
	e := newParityEnv(t)
	ctx := context.Background()

	code, raw := e.do("POST", "/v1/chat/conversations/direct", pA, map[string]string{"other_user_id": pB.String()})
	e.must(code, 200, raw, "create dm")
	var dm struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &dm)
	code, raw = e.do("POST", "/v1/chat/conversations/group", pA, map[string]any{"title": "Trip", "member_ids": []string{pB.String(), pD.String()}})
	e.must(code, 201, raw, "create group")
	var grp struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &grp)
	dmPath := "/v1/chat/conversations/" + dm.ID.String()
	grpPath := "/v1/chat/conversations/" + grp.ID.String()
	send := func(user uuid.UUID, path string, body map[string]any) map[string]any {
		t.Helper()
		code, raw := e.do("POST", path+"/messages", user, body)
		e.must(code, 200, raw, "send")
		var m map[string]any
		dataOf(t, raw, &m)
		return m
	}
	rowFor := func(user, conv uuid.UUID, lane string) map[string]any {
		t.Helper()
		var rows []map[string]any
		dataOf(t, mustGet(e, "/v1/chat/conversations"+lane, user), &rows)
		for _, r := range rows {
			if r["id"] == conv.String() {
				return r
			}
		}
		t.Fatalf("conversation %s not in %s inbox", conv, user)
		return nil
	}

	// --- PUT .../unread ---
	first := send(pA, dmPath, map[string]any{"type": "text", "text": "hi"})
	if code, raw := e.do("POST", dmPath+"/read", pB, map[string]string{"message_id": first["msg_id"].(string)}); code != 200 {
		t.Fatalf("read: %d %s", code, raw)
	}
	if r := rowFor(pB, dm.ID, ""); r["has_unread"] != false || r["unread_count"].(float64) != 0 || r["marked_unread"] != false {
		t.Fatalf("read row = %v", r)
	}
	if code, raw := e.do("PUT", dmPath+"/unread", pB, map[string]any{"unread": true}); code != 204 {
		t.Fatalf("mark unread: %d %s", code, raw)
	}
	if r := rowFor(pB, dm.ID, ""); r["has_unread"] != true || r["unread_count"].(float64) != 1 || r["marked_unread"] != true {
		t.Fatalf("marked row = %v", r)
	}
	code, raw = e.do("GET", dmPath, pB, nil)
	e.must(code, 200, raw, "marked detail")
	if !strings.Contains(string(raw), `"marked_unread":true`) {
		t.Fatalf("detail lacks the mark: %s", raw)
	}
	assertFixture(t, "conversation_marked_unread", raw)
	// The mark is the caller's own: A's row is untouched.
	if r := rowFor(pA, dm.ID, ""); r["marked_unread"] != false {
		t.Fatalf("A sees B's mark: %v", r)
	}
	// Real unread messages keep their count under a mark.
	send(pA, dmPath, map[string]any{"type": "text", "text": "two"})
	last := send(pA, dmPath, map[string]any{"type": "text", "text": "three"})
	if r := rowFor(pB, dm.ID, ""); r["unread_count"].(float64) != 2 || r["marked_unread"] != true {
		t.Fatalf("marked row with 2 new = %v", r)
	}
	// Reading clears the mark.
	if code, raw := e.do("POST", dmPath+"/read", pB, map[string]string{"message_id": last["msg_id"].(string)}); code != 200 {
		t.Fatalf("read: %d %s", code, raw)
	}
	if r := rowFor(pB, dm.ID, ""); r["has_unread"] != false || r["unread_count"].(float64) != 0 || r["marked_unread"] != false {
		t.Fatalf("read must clear the mark: %v", r)
	}
	// unread:false clears it too.
	e.do("PUT", dmPath+"/unread", pB, map[string]any{"unread": true})
	if code, _ := e.do("PUT", dmPath+"/unread", pB, map[string]any{"unread": false}); code != 204 {
		t.Fatal("clear mark")
	}
	if r := rowFor(pB, dm.ID, ""); r["has_unread"] != false || r["marked_unread"] != false {
		t.Fatalf("unread:false must clear the mark: %v", r)
	}
	// Clearing the chat clears the mark with the counter.
	e.do("PUT", dmPath+"/unread", pB, map[string]any{"unread": true})
	if code, _ := e.do("POST", dmPath+"/clear", pB, nil); code != 204 {
		t.Fatal("clear")
	}
	var marked bool
	if err := e.pool.QueryRow(ctx, `SELECT marked_unread FROM chat.conversation_members WHERE conversation_id=$1 AND user_id=$2`, dm.ID, pB).Scan(&marked); err != nil || marked {
		t.Fatalf("clear must drop the mark: %v %v", marked, err)
	}
	if code, _ := e.do("PUT", dmPath+"/unread", pC, map[string]any{"unread": true}); code != 404 {
		t.Fatalf("non-member mark: %d", code)
	}
	if code, _ := e.do("PUT", dmPath+"/unread", pB, map[string]any{}); code != 400 {
		t.Fatalf("mark without the flag: %d", code)
	}

	// --- Giphy sticker (gif + is_sticker) ---
	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "gif", "gif": map[string]any{
		"provider": "giphy", "id": "l0HlNQ03J5JxX6lva", "url": "https://media1.giphy.com/media/l0HlNQ03J5JxX6lva/giphy.gif",
		"width": 200, "height": 200, "is_sticker": true}})
	e.must(code, 200, raw, "giphy sticker")
	if !strings.Contains(string(raw), `"is_sticker":true`) {
		t.Fatalf("is_sticker not returned: %s", raw)
	}
	assertFixture(t, "message_gif_sticker", raw)
	if lm := rowFor(pB, dm.ID, "")["last_message"].(map[string]any); lm["type"] != "gif" || lm["preview"] != "Sticker" {
		t.Fatalf("sticker last_message = %v", lm)
	}
	if !strings.Contains(string(mustGet(e, dmPath+"/messages?limit=5", pB)), `"is_sticker":true`) {
		t.Fatal("history lost is_sticker")
	}
	plain := send(pA, dmPath, map[string]any{"type": "gif", "gif": map[string]any{
		"provider": "giphy", "id": "abc", "url": "https://i.giphy.com/abc.gif", "width": 10, "height": 10}})
	if g := plain["gif"].(map[string]any); g["is_sticker"] != false {
		t.Fatalf("plain gif is_sticker = %v", g["is_sticker"])
	}

	// --- media.sensitive ---
	clean, late, unscanned := uuid.New(), uuid.New(), uuid.New()
	no := false
	e.fakes.mu.Lock()
	for _, id := range []uuid.UUID{clean, late, unscanned} {
		e.fakes.mediaKind[id.String()] = "image"
	}
	e.fakes.mediaSensitive[clean.String()] = &no
	e.fakes.mediaSensitive[unscanned.String()] = nil // explicit null: no real scan
	e.fakes.mu.Unlock()
	code, raw = e.do("POST", dmPath+"/messages", pA, map[string]any{"type": "media", "media_id": clean})
	e.must(code, 200, raw, "clean media")
	if !strings.Contains(string(raw), `"sensitive":false`) {
		t.Fatalf("clean media sensitive: %s", raw)
	}
	assertFixture(t, "message_media_sensitive", raw)
	lateMsg := send(pA, dmPath, map[string]any{"type": "media", "media_id": late})
	if m := lateMsg["media"].(map[string]any); m["sensitive"] != nil {
		t.Fatalf("unscanned at send must be null: %v", m)
	}
	unscannedMsg := send(pA, dmPath, map[string]any{"type": "media", "media_id": unscanned})
	if m := unscannedMsg["media"].(map[string]any); m["sensitive"] != nil {
		t.Fatalf("explicit null must stay null: %v", m)
	}
	// Moderation lands after the send: history completes the verdict.
	yes := true
	e.fakes.mu.Lock()
	e.fakes.mediaSensitive[late.String()] = &yes
	e.fakes.mu.Unlock()
	var hist []map[string]any
	dataOf(t, mustGet(e, dmPath+"/messages?limit=10", pB), &hist)
	verdict := map[string]any{}
	for _, m := range hist {
		if id, _ := m["media_id"].(string); id != "" {
			verdict[id] = m["media"].(map[string]any)["sensitive"]
		}
	}
	if verdict[late.String()] != true || verdict[clean.String()] != false || verdict[unscanned.String()] != nil {
		t.Fatalf("history verdicts = %v", verdict)
	}

	// --- requests: the sender block ---
	code, raw = e.do("POST", "/v1/chat/conversations/direct", pC, map[string]string{"other_user_id": pB.String()})
	e.must(code, 200, raw, "request create")
	var req struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &req)
	reqPath := "/v1/chat/conversations/" + req.ID.String()
	send(pC, reqPath, map[string]any{"type": "text", "text": "hello from chen"})
	type senderBlock struct {
		UserID      *string `json:"user_id"`
		DisplayName string  `json:"display_name"`
		Username    string  `json:"username"`
		AvatarURL   string  `json:"avatar_url"`
		Deleted     bool    `json:"deleted"`
	}
	requestSender := func() senderBlock {
		t.Helper()
		var rows []struct {
			ID     uuid.UUID    `json:"id"`
			Sender *senderBlock `json:"sender"`
		}
		// Chen's account is two days old: safe mode files it under filtered.
		dataOf(t, mustGet(e, "/v1/chat/requests?filter=filtered", pB), &rows)
		if len(rows) != 1 || rows[0].ID != req.ID || rows[0].Sender == nil {
			t.Fatalf("requests = %+v", rows)
		}
		return *rows[0].Sender
	}
	if s := requestSender(); s.Deleted || s.DisplayName != "Chen" || s.Username != "chen" || s.UserID == nil || *s.UserID != pC.String() || s.AvatarURL == "" {
		t.Fatalf("sender = %+v", s)
	}
	assertFixture(t, "requests_list", mustGet(e, "/v1/chat/requests?filter=filtered", pB))
	detail := mustGet(e, reqPath, pB)
	if !strings.Contains(string(detail), `"sender":{`) || !strings.Contains(string(detail), `"display_name":"Chen","username":"chen"`) {
		t.Fatalf("request detail sender: %s", detail)
	}
	assertFixture(t, "request_detail", detail)
	laneRows := mustGet(e, "/v1/chat/conversations?lane=requests", pB)
	if !strings.Contains(string(laneRows), `"deleted":false`) {
		t.Fatalf("requests lane rows lack the sender: %s", laneRows)
	}
	if strings.Contains(string(mustGet(e, dmPath, pB)), `"sender":`) {
		t.Fatal("a normal conversation must not carry a sender block")
	}
	// A deactivated account reads as deleted, with nothing that names it.
	store := pgstore.New(e.pool)
	if err := store.SetUserHidden(ctx, pC, true, "test"); err != nil {
		t.Fatal(err)
	}
	if s := requestSender(); !s.Deleted || s.DisplayName != "" || s.Username != "" || s.AvatarURL != "" {
		t.Fatalf("deactivated sender = %+v", s)
	}
	if err := store.SetUserHidden(ctx, pC, false, "test"); err != nil {
		t.Fatal(err)
	}
	// A purged account (request row and profile gone, directory 404s) too.
	e.fakes.mu.Lock()
	e.fakes.goneUsers[pC] = true
	e.fakes.mu.Unlock()
	for _, q := range []string{`DELETE FROM chat.message_requests WHERE conversation_id = $1`} {
		if _, err := e.pool.Exec(ctx, q, req.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.pool.Exec(ctx, `DELETE FROM chat.user_profiles WHERE user_id = $1`, pC); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID     uuid.UUID    `json:"id"`
		Sender *senderBlock `json:"sender"`
	}
	dataOf(t, mustGet(e, "/v1/chat/requests", pB), &rows)
	var purged *senderBlock
	for _, r := range rows {
		if r.ID == req.ID {
			purged = r.Sender
		}
	}
	if purged == nil || !purged.Deleted || purged.DisplayName != "" || purged.UserID == nil || *purged.UserID != pC.String() {
		t.Fatalf("purged sender = %+v (rows %+v)", purged, rows)
	}
	// The request detail resolves the purged sender from the conversation's
	// creator too.
	var purgedDetail struct {
		Sender *senderBlock `json:"sender"`
	}
	dataOf(t, mustGet(e, reqPath, pB), &purgedDetail)
	if s := purgedDetail.Sender; s == nil || !s.Deleted || s.UserID == nil || *s.UserID != pC.String() {
		t.Fatalf("purged sender on the detail = %+v", s)
	}

	// --- POST .../report ---
	countReports := func() int {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM chat.outbox_events WHERE event_type = 'ConversationReported'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if code, _ := e.do("POST", dmPath+"/report", pB, map[string]any{"reason": "boring"}); code != 400 {
		t.Fatal("bad reason accepted")
	}
	if code, _ := e.do("POST", dmPath+"/report", pC, map[string]any{"reason": "spam"}); code != 404 {
		t.Fatal("non-member report accepted")
	}
	// DM, trust-safety without the conversation route: the user report is
	// the outbox event; the filing falls back to the newest message A sent.
	if code, raw := e.do("POST", dmPath+"/report", pB, map[string]any{"reason": "harassment", "note": "keeps messaging"}); code != 202 {
		t.Fatalf("dm report: %d %s", code, raw)
	}
	if code, _ := e.do("POST", dmPath+"/report", pB, map[string]any{"reason": "harassment"}); code != 202 {
		t.Fatal("dm report retry")
	}
	if n := countReports(); n != 1 {
		t.Fatalf("ConversationReported events = %d, want 1 (a retry dedupes)", n)
	}
	var payloadRaw []byte
	if err := e.pool.QueryRow(ctx, `SELECT payload FROM chat.outbox_events WHERE event_type = 'ConversationReported'`).Scan(&payloadRaw); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		EntityType     string   `json:"entity_type"`
		EntityID       string   `json:"entity_id"`
		ReporterID     string   `json:"reporter_id"`
		ReportedUserID string   `json:"reported_user_id"`
		Reason         string   `json:"reason"`
		MessageIDs     []string `json:"message_ids"`
	}
	_ = json.Unmarshal(payloadRaw, &payload)
	if payload.EntityType != "user" || payload.EntityID != pA.String() || payload.ReporterID != pB.String() ||
		payload.ReportedUserID != pA.String() || payload.Reason != "harassment" || len(payload.MessageIDs) != 5 {
		t.Fatalf("dm report payload = %s", payloadRaw)
	}
	e.fakes.mu.Lock()
	var fallback map[string]string
	for _, r := range e.fakes.reports {
		if strings.HasPrefix(r["idempotency_key"], "chat-conv-report:") && fallback == nil {
			fallback = r // the first filing; the retry reuses its key
		}
	}
	e.fakes.mu.Unlock()
	if fallback == nil || fallback["reporter_id"] != pB.String() || !strings.HasPrefix(fallback["entity_id"], dm.ID.String()+":"+payload.MessageIDs[0]) ||
		!strings.Contains(fallback["note"], "Reported the whole chat") || !strings.Contains(fallback["note"], "keeps messaging") {
		t.Fatalf("message-route fallback = %v", fallback)
	}
	// Group, conversation route deployed: filed there as a conversation report.
	send(pD, grpPath, map[string]any{"type": "text", "text": "buy my course"})
	e.fakes.mu.Lock()
	e.fakes.convRouteOn = true
	e.fakes.mu.Unlock()
	if code, raw := e.do("POST", grpPath+"/report", pB, map[string]any{"reason": "scam"}); code != 202 {
		t.Fatalf("group report: %d %s", code, raw)
	}
	e.fakes.mu.Lock()
	conv := e.fakes.convReports
	e.fakes.mu.Unlock()
	if len(conv) != 1 || conv[0]["entity_type"] != "conversation" || conv[0]["entity_id"] != grp.ID.String() ||
		conv[0]["reporter_id"] != pB.String() || conv[0]["reason"] != "scam" || len(conv[0]["message_ids"].([]any)) != 1 {
		t.Fatalf("conversation route reports = %v", conv)
	}
	if n := countReports(); n != 2 {
		t.Fatalf("ConversationReported events = %d, want 2", n)
	}
}
