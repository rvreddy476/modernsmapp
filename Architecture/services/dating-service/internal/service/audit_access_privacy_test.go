// Audit 2026-10-30, access & privacy lane — guards that need no database.
// The database halves are in audit_access_privacy_it_test.go and the store
// and http packages.
package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func strp(s string) *string { return &s }

// L4: a note the recipient's filter hides is never sent — the list carries
// the reason, not the text. Kind notes keep their text.
func TestApplyNoteFilterDropsHiddenText(t *testing.T) {
	rude := &store.Spark{ID: uuid.New(), Note: strp("you look stupid")}
	word := &store.Spark{ID: uuid.New(), Note: strp("Do you like CRICKET?")}
	nice := &store.Spark{ID: uuid.New(), Note: strp("Great answer about books")}
	none := &store.Spark{ID: uuid.New()}
	applyNoteFilter(store.CommentFilter{FilterUnkind: true, Words: []string{"cricket"}}, []*store.Spark{rude, word, nice, none, nil})

	if rude.NoteHidden != NoteHiddenUnkind || rude.Note != nil {
		t.Fatalf("unkind: hidden=%q note=%v, want unkind and no text", rude.NoteHidden, rude.Note)
	}
	if word.NoteHidden != NoteHiddenYourWords || word.Note != nil {
		t.Fatalf("your words: hidden=%q note=%v, want your_words and no text", word.NoteHidden, word.Note)
	}
	if nice.NoteHidden != "" || nice.Note == nil || *nice.Note != "Great answer about books" {
		t.Fatalf("kind note changed: hidden=%q note=%v", nice.NoteHidden, nice.Note)
	}
	raw, _ := json.Marshal([]*store.Spark{rude, word})
	if strings.Contains(string(raw), "stupid") || strings.Contains(strings.ToLower(string(raw)), "cricket") {
		t.Fatalf("serialised list carries hidden note text: %s", raw)
	}
	// Filter off: nothing hidden.
	off := &store.Spark{Note: strp("you look stupid")}
	applyNoteFilter(store.CommentFilter{}, []*store.Spark{off})
	if off.NoteHidden != "" || off.Note == nil {
		t.Fatalf("filter off hid a note")
	}
}

// L4: with an identity reader configured, a client birth date never gates,
// and a birth date on file counts only when identity supplied it.
func TestEffectiveBirthDate_RequireIdentity(t *testing.T) {
	client := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	idDOB := time.Date(1992, 2, 2, 0, 0, 0, 0, time.UTC)
	onFile := time.Date(1993, 3, 3, 0, 0, 0, 0, time.UTC)
	identity := &IdentityBasics{Found: true, BirthDate: &idDOB, DOBSource: IdentityDOBSourceRegistration}
	noDOB := &IdentityBasics{Found: true, DOBSource: IdentityDOBSourceNone}
	clientPrior := &store.Profile{BirthDate: &onFile, DOBSource: strp(store.BasicsSourceClient)}
	identityPrior := &store.Profile{BirthDate: &onFile, DOBSource: strp(store.BasicsSourceIdentityProfile)}

	if d, src := effectiveBirthDate(nil, &client, identity, true); d == nil || !d.Equal(idDOB) || src != store.BasicsSourceIdentityRegistration {
		t.Fatalf("identity answer: %v %q", d, src)
	}
	for name, tc := range map[string]struct {
		prior    *store.Profile
		identity *IdentityBasics
	}{
		"new profile, identity has none":     {nil, noDOB},
		"new profile, identity 404":          {nil, nil},
		"client date on file, identity none": {clientPrior, noDOB},
	} {
		if d, src := effectiveBirthDate(tc.prior, &client, tc.identity, true); d != nil || src != "" {
			t.Fatalf("%s: got %v %q, want nothing (refuse)", name, d, src)
		}
	}
	if d, src := effectiveBirthDate(identityPrior, &client, noDOB, true); d == nil || !d.Equal(onFile) || src != "" {
		t.Fatalf("identity date on file: %v %q, want it kept", d, src)
	}
	// local/dev without identity: the interim client rule is unchanged.
	if d, src := effectiveBirthDate(nil, &client, nil, false); d == nil || !d.Equal(client) || src != store.BasicsSourceClient {
		t.Fatalf("dev interim rule: %v %q", d, src)
	}
}

func TestTrustsBirthDateSource(t *testing.T) {
	dev := &Service{}
	if !dev.trustsBirthDateSource(strp(store.BasicsSourceClient)) || !dev.trustsBirthDateSource(nil) {
		t.Fatalf("without identity every source is trusted (interim rule)")
	}
	prod := &Service{identityClient: &stubIdentityBasics{}}
	for _, src := range []*string{nil, strp(""), strp(store.BasicsSourceClient)} {
		if prod.trustsBirthDateSource(src) {
			t.Fatalf("with identity, source %v must not be trusted", src)
		}
	}
	for _, src := range []string{store.BasicsSourceIdentity, store.BasicsSourceIdentityRegistration, store.BasicsSourceIdentityProfile} {
		if !prod.trustsBirthDateSource(strp(src)) {
			t.Fatalf("identity source %q not trusted", src)
		}
	}
}

func TestErrIdentityDOBRequiredIsUnderage(t *testing.T) {
	if !errors.Is(ErrIdentityDOBRequired, ErrUnderage) {
		t.Fatalf("ErrIdentityDOBRequired must be an ErrUnderage for adult-or-not callers")
	}
}

// M3: someone other than the vouchee sees what the friend said, never who
// the friend is; only accepted vouches.
func TestPublicVouchesHideTheVoucher(t *testing.T) {
	voucher, community := uuid.New(), uuid.New()
	rel := "friend"
	in := []*store.Vouch{
		{ID: uuid.New(), VoucherID: voucher, VoucheeID: uuid.New(), Relationship: &rel, CommunityID: &community, Note: strp("kind and funny"), Status: "accepted"},
		{ID: uuid.New(), VoucherID: uuid.New(), Status: "pending", Note: strp("pending one")},
		nil,
	}
	out := publicVouches(in)
	if len(out) != 1 || out[0].Note == nil || *out[0].Note != "kind and funny" || out[0].Relationship == nil {
		t.Fatalf("public vouches = %+v, want the accepted one with its note", out)
	}
	raw, _ := json.Marshal(out)
	for _, leak := range []string{voucher.String(), community.String(), "voucher_id", "community_id", "pending one"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("public vouch carries %q: %s", leak, raw)
		}
	}
}
