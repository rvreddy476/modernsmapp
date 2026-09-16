package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type blockListBody struct {
	Data struct {
		Items []struct {
			UserID    string `json:"user_id"`
			FirstName string `json:"first_name"`
			Age       int    `json:"age"`
			BlockedAt string `json:"blocked_at"`
		} `json:"items"`
	} `json:"data"`
}

func readBlocks(t *testing.T, r http.Handler, user uuid.UUID) blockListBody {
	t.Helper()
	rec := contractDo(r, http.MethodGet, "/v1/dating/blocks", ``, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("blocks: status %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "photo") {
		t.Fatalf("the block list carries a photo: %s", rec.Body.String())
	}
	var out blockListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode blocks: %v (%s)", err, rec.Body.String())
	}
	return out
}

// Lane D10 — the block list and unblocking.
func TestBlockListAndUnblock(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()
	user, target := uuid.New(), uuid.New()
	mustSeedActiveProfile(t, st, user)
	mustSeedActiveProfile(t, st, target)

	// A match and sparks exist before the block.
	matchID, _, err := st.CreateOrGetOpenMatch(ctx, user, target, nil)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if err := st.MarkMatchActive(ctx, matchID, uuid.New()); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := st.CreateSpark(ctx, target, user, "photo", "0", ""); err != nil {
		t.Fatalf("seed spark: %v", err)
	}

	if list := readBlocks(t, r, user); len(list.Data.Items) != 0 {
		t.Fatalf("a fresh user has blocks: %+v", list.Data.Items)
	}
	rec := contractDo(r, http.MethodPost, "/v1/dating/safety/block", `{"target_user_id":"`+target.String()+`"}`, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("block: status %d body %s", rec.Code, rec.Body.String())
	}
	list := readBlocks(t, r, user)
	if len(list.Data.Items) != 1 || list.Data.Items[0].UserID != target.String() ||
		list.Data.Items[0].FirstName != "Asha" || list.Data.Items[0].Age < 18 || list.Data.Items[0].BlockedAt == "" {
		t.Fatalf("block list = %+v", list.Data.Items)
	}
	// The other side does not see the block in their own list.
	if other := readBlocks(t, r, target); len(other.Data.Items) != 0 {
		t.Fatalf("the blocked person sees a block: %+v", other.Data.Items)
	}

	// Unblock: the list empties, and nothing the block severed returns.
	rec = contractDo(r, http.MethodDelete, "/v1/dating/blocks/"+target.String(), ``, user)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed":true`) {
		t.Fatalf("unblock: status %d body %s", rec.Code, rec.Body.String())
	}
	if list := readBlocks(t, r, user); len(list.Data.Items) != 0 {
		t.Fatalf("block survived the unblock: %+v", list.Data.Items)
	}
	m, err := st.GetMatch(ctx, matchID)
	if err != nil || m.Status != "closed" {
		t.Fatalf("the closed match came back: %+v (%v)", m, err)
	}
	if sparks, err := st.ListIncomingSparks(ctx, user, 50, 0); err != nil || len(sparks) != 0 {
		t.Fatalf("the deleted sparks came back: %d (%v)", len(sparks), err)
	}
	// Idempotent.
	rec = contractDo(r, http.MethodDelete, "/v1/dating/blocks/"+target.String(), ``, user)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed":false`) {
		t.Fatalf("repeat unblock: status %d body %s", rec.Code, rec.Body.String())
	}
}

// A report auto-blocks the target; the reporter can undo that block.
func TestReporterCanUnblockAReportedPerson(t *testing.T) {
	r, st, cleanup := setupTestRouter(t)
	defer cleanup()
	ctx := context.Background()
	reporter, target := uuid.New(), uuid.New()
	mustSeedActiveProfile(t, st, reporter)
	mustSeedActiveProfile(t, st, target)

	body := `{"target_id":"` + target.String() + `","reason":"harassment","details":"unpleasant messages"}`
	rec := contractDo(r, http.MethodPost, "/v1/dating/safety/report", body, reporter)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report: status %d body %s", rec.Code, rec.Body.String())
	}
	if list := readBlocks(t, r, reporter); len(list.Data.Items) != 1 || list.Data.Items[0].UserID != target.String() {
		t.Fatalf("the report did not block: %+v", list.Data.Items)
	}
	if rec := contractDo(r, http.MethodDelete, "/v1/dating/blocks/"+target.String(), ``, reporter); rec.Code != http.StatusOK {
		t.Fatalf("unblock after a report: status %d body %s", rec.Code, rec.Body.String())
	}
	if list := readBlocks(t, r, reporter); len(list.Data.Items) != 0 {
		t.Fatalf("a report-raised block cannot be undone: %+v", list.Data.Items)
	}
	// The report itself survives the unblock.
	reports, err := st.ListReports(ctx, "", "", 100, 0)
	if err != nil {
		t.Fatalf("list reports: %v", err)
	}
	found := false
	for _, rep := range reports {
		if rep.ReporterID == reporter && rep.TargetID == target {
			found = true
		}
	}
	if !found {
		t.Fatalf("the report was lost with the block")
	}
}
