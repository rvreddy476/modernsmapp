package postgres

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Unit-level guards for the 2026-11-17 publish fixes; the behaviour itself is
// proved against Postgres in video_gate_integration_test.go (CI only).

// Every field the search document carries re-indexes on edit; fields it does
// not carry do not.
func TestIndexedFieldChangedCoversTheSearchDocument(t *testing.T) {
	for _, k := range []string{"title", "text", "tags", "hashtags", "category", "language", "cover_media_id"} {
		if !indexedFieldChanged(map[string]PostEditChange{k: {Changed: true}}) {
			t.Errorf("an edit to %q does not re-index the post", k)
		}
	}
	for _, k := range []string{"allow_download", "no_comments", "hide_like_count"} {
		if indexedFieldChanged(map[string]PostEditChange{k: {Changed: true}}) {
			t.Errorf("an edit to %q re-indexes the post", k)
		}
	}
	if indexedFieldChanged(map[string]PostEditChange{}) {
		t.Error("an empty edit re-indexes the post")
	}
}

// lv-17: the readiness rule waits for a ready transcode AND a decided review
// gate, against the right row, and both the worker's scan and its guarded
// flip apply it.
func TestScheduledVideoReadyRule(t *testing.T) {
	got := scheduledVideoReadySQL("p")
	for _, want := range []string{"NOT EXISTS", "vm.post_id = p.id", "vm.upload_status <> 'ready'", "p.review_status = 'pending'"} {
		if !strings.Contains(got, want) {
			t.Errorf("readiness rule %q lacks %q", got, want)
		}
	}
	src, err := os.ReadFile("scheduled.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	if !strings.Contains(code, "AND `+scheduledVideoReadySQL(\"p\")+`") {
		t.Error("ListDueScheduledPosts no longer waits for the video")
	}
	if !strings.Contains(code, "` AND publish_at <= $2 AND ` + scheduledVideoReadySQL(\"posts\")") {
		t.Error("the worker's guarded flip no longer waits for the video")
	}
}

// The scheduled publish writes PostSearchEligibilityChanged (the notification
// lane's second trigger and search's re-read) before PostCreated, at the
// revision it bumps once; it is skipped for a held post. (Store.db is a
// concrete pool, so the flip itself is proved in video_gate_integration_test.go.)
func TestScheduledPublishWritesTheEligibilityEvent(t *testing.T) {
	src, err := os.ReadFile("scheduled.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	start := strings.Index(code, "func (s *Store) PublishScheduledPostWithHold(")
	end := strings.Index(code, "func scheduledFlipHeldTx(")
	if start < 0 || end < start {
		t.Fatal("PublishScheduledPostWithHold not found")
	}
	body := code[start:end]
	emit := strings.Index(body, "if rev, err = BumpSearchRevAndEmitTxRev(ctx, tx, postID)")
	if emit < 0 {
		t.Fatal("the scheduled publish no longer writes PostSearchEligibilityChanged")
	}
	if guard := strings.LastIndex(body[:emit], "if !held {"); guard < 0 || emit-guard > 40 {
		t.Fatal("the eligibility event is not limited to a post that is not held")
	}
	if created := strings.Index(body, "eventType, payload, err := event(rev)"); created < emit {
		t.Fatal("PostCreated is built before the eligibility revision")
	}
	if strings.Contains(body, "search_rev = search_rev + 1") {
		t.Fatal("the flip bumps search_rev itself as well: two revisions per publish")
	}
}

// The edit transaction actually consults the indexed-field rule.
func TestEditReindexesOnIndexedFields(t *testing.T) {
	src, err := os.ReadFile("post_edit.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "|| indexedFieldChanged(changes) {") {
		t.Fatal("UpdatePostFields no longer re-indexes on an indexed-field edit")
	}
}

// The eligibility event carries tags, hashtags, category and language when
// the post is eligible, and never for a gated (or otherwise hidden) post.
func TestEligibilityEventCarriesTaxonomyOnlyWhenEligible(t *testing.T) {
	tx := newGateTx()
	if err := BumpSearchRevAndEmitTx(context.Background(), tx, uuid.New()); err != nil {
		t.Fatal(err)
	}
	p := tx.payloads[0]
	if len(p.Tags) != 1 || len(p.Hashtags) != 1 || p.Category != "music" || p.Language != "te" {
		t.Fatalf("eligible payload taxonomy = %+v", p.PostSearchTaxonomy)
	}
	for name, mut := range map[string]func(*gateTx){
		"gated":   func(tx *gateTx) { tier := uuid.New(); tx.row.tier = &tier },
		"pending": func(tx *gateTx) { tx.row.review = "pending" },
		"private": func(tx *gateTx) { tx.row.visibility = "private" },
	} {
		tx := newGateTx()
		mut(tx)
		if err := BumpSearchRevAndEmitTx(context.Background(), tx, uuid.New()); err != nil {
			t.Fatal(err)
		}
		if p := tx.payloads[0]; p.Category != "" || p.Tags != nil || p.Hashtags != nil || p.Language != "" {
			t.Errorf("%s post's event carried taxonomy %+v", name, p.PostSearchTaxonomy)
		}
	}
}

// UpdatePostCategory (the category tool) re-indexes like an edit does.
func TestUpdatePostCategoryReindexes(t *testing.T) {
	src, err := os.ReadFile("post_edit.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	i := strings.Index(code, "func (s *Store) UpdatePostCategory(")
	if i < 0 {
		t.Fatal("UpdatePostCategory not found")
	}
	body := code[i:]
	if end := strings.Index(body, "\n}"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "s.WithSearchEligibilityTx(ctx, postID,") || !strings.Contains(body, "category IS DISTINCT FROM $2") {
		t.Fatal("UpdatePostCategory no longer writes the eligibility event on a change")
	}
}
