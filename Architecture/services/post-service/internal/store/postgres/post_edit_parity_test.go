package postgres

import (
	"testing"
)

// The audit diff records the details screen's fields, and a no-op writes
// nothing (2026-10-18).
func TestPostEditChanges_DetailsScreenFields(t *testing.T) {
	link := "https://example.com/a"
	yes, no := true, false
	before := &Post{LinkURL: &link, IsBranded: false, AIGenerated: true}

	if got := postEditChanges(before, PostEditPatch{LinkURL: &link, IsBranded: &no, AIGenerated: &yes}); len(got) != 0 {
		t.Fatalf("unchanged values audited: %v", got)
	}
	other := "https://example.com/b"
	got := postEditChanges(before, PostEditPatch{LinkURL: &other, IsBranded: &yes, AIGenerated: &no})
	if c := got["link_url"]; c.From != link || c.To != other {
		t.Fatalf("link_url change = %+v", c)
	}
	if c := got["is_branded"]; c.From != false || c.To != true {
		t.Fatalf("is_branded change = %+v", c)
	}
	if c := got["ai_generated"]; c.From != true || c.To != false {
		t.Fatalf("ai_generated change = %+v", c)
	}
	cleared := postEditChanges(before, PostEditPatch{ClearLinkURL: true, LinkURL: &other})
	if c := cleared["link_url"]; c.From != link || c.To != "" {
		t.Fatalf("clear wins over a value: %+v", c)
	}
	if got := postEditChanges(&Post{}, PostEditPatch{ClearLinkURL: true}); len(got) != 0 {
		t.Fatalf("clearing an absent link audited: %v", got)
	}
}
