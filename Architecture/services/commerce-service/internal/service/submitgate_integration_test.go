//go:build integration

package service

// The submit gate as a seller and a reviewer actually meet it: a listing that
// saves happily while unfinished, refuses to be submitted with every gap named
// at once, sails through once they are filled, and — after somebody tightens a
// rule underneath it — is flagged without ever coming off sale.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/service/... \
//	  -run 'SubmitGate|Submission|Sweep' -v -count=1

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Fixture ────────────────────────────────────────────────────────────

// gateFixture is a category asking three questions — two REQUIRED, one not —
// and a seller who can list under it.
//
// The optional one is not decoration. "Required and unanswered" must be a gap
// and "optional and unanswered" must not, and a fixture with only required
// fields cannot tell a working gate from one that refuses everything blank.
type gateFixture struct {
	t     *testing.T
	svc   *Service
	store *postgres.Store

	sellerID   uuid.UUID
	userID     uuid.UUID
	categoryID uuid.UUID
	taxClassID uuid.UUID

	authorCode, pagesCode, isbnCode string
	authorDef, pagesDef, isbnDef    uuid.UUID
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	st := postgres.New(svcTestPool)

	f := &gateFixture{
		t: t, svc: &Service{store: st}, store: st,
		userID:     uuid.New(),
		sellerID:   uuid.New(),
		categoryID: uuid.New(),
		authorCode: "sg_author_" + suffix,
		pagesCode:  "sg_pages_" + suffix,
		isbnCode:   "sg_isbn_" + suffix,
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := svcTestPool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture %.40s…: %v", sql, err)
		}
	}

	// The seller's own user id is seeded rather than random, because the
	// submit path resolves the shop FROM the user — an ownership gate this
	// suite has to exercise, not bypass.
	exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
	      VALUES ($1,$2,$3,$4,$5,'KA','approved')`,
		f.sellerID, f.userID, "Gate Store "+suffix, "gate-"+suffix, "gate-"+suffix+"@example.test")

	exec(`INSERT INTO product_categories (id,name,slug,is_active,is_listable)
	      VALUES ($1,'Submit Gate Books',$2,TRUE,TRUE)`, f.categoryID, "sg-cat-"+suffix)

	for _, d := range []struct {
		code, label string
		required    bool
		target      *uuid.UUID
	}{
		{f.authorCode, "Author " + suffix, true, &f.authorDef},
		{f.pagesCode, "Pages " + suffix, true, &f.pagesDef},
		{f.isbnCode, "ISBN " + suffix, false, &f.isbnDef},
	} {
		id := uuid.New()
		*d.target = id
		dtype := "text"
		if d.code == f.pagesCode {
			dtype = "integer"
		}
		exec(`INSERT INTO attribute_definitions
		      (id, code, label, data_type, display_group, applies_to, is_active)
		      VALUES ($1,$2,$3,$4,'Product Details','item',TRUE)`, id, d.code, d.label, dtype)
		exec(`INSERT INTO category_attributes (category_id, definition_id, is_required, sort_order)
		      VALUES ($1,$2,$3,10)`, f.categoryID, id, d.required)
	}

	if err := svcTestPool.QueryRow(ctx,
		`SELECT id FROM tax_classes ORDER BY created_at LIMIT 1`).Scan(&f.taxClassID); err != nil {
		t.Skipf("no tax class configured in this database, so the gate cannot pass: %v", err)
	}
	return f
}

// listing is how a fixture product is asked for: everything on by default, and
// each flag turned off to break exactly one built-in.
type listing struct {
	noTaxClass bool
	noImage    bool
	noPrice    bool
	noStock    bool
	answers    []AttributeValueInput
}

func (f *gateFixture) create(l listing) *postgres.Product {
	f.t.Helper()
	ctx := context.Background()
	cat := f.categoryID

	in := CreateProductInput{
		SellerID:    f.sellerID,
		ActorUserID: f.userID,
		CategoryID:  &cat,
		Title:       "Gate Fixture Book " + uuid.NewString()[:6],
		Attributes:  l.answers,
		Variants: []CreateVariantInput{{
			SKU:               "SG-" + uuid.NewString()[:10],
			MRPMinor:          50000,
			SellingPriceMinor: 45000,
			StockQty:          6,
		}},
	}
	if !l.noTaxClass {
		in.TaxClassID = &f.taxClassID
	}
	if l.noPrice {
		in.Variants[0].SellingPriceMinor = 0
	}
	if l.noStock {
		in.Variants[0].StockQty = 0
	}

	p, err := f.svc.CreateProduct(ctx, in)
	if err != nil {
		f.t.Fatalf("create: %v", err)
	}
	if !l.noImage {
		// Straight into product_media rather than through the media route:
		// the gate asks whether the listing HAS an image, and media-service
		// ownership verification is a different test's subject.
		if _, err := svcTestPool.Exec(ctx,
			`INSERT INTO product_media (product_id, media_id, media_type, sort_order)
			 VALUES ($1,$2,'image',0)`, p.ID, uuid.New()); err != nil {
			f.t.Fatalf("attach image: %v", err)
		}
	}
	return p
}

// complete is the listing that has nothing wrong with it.
func (f *gateFixture) complete() *postgres.Product {
	return f.create(listing{answers: []AttributeValueInput{
		{Code: f.authorCode, Value: "R K Narayan"},
		{Code: f.pagesCode, Value: 328},
	}})
}

func (f *gateFixture) submit(p *postgres.Product) error {
	return f.svc.SubmitProduct(context.Background(), p.ID, f.userID)
}

// gaps unwraps the refusal into the codes it named, failing on any other error.
func gaps(t *testing.T, err error) map[string]string {
	t.Helper()
	var bad *ProductIncompleteError
	if !errors.As(err, &bad) {
		t.Fatalf("expected a ProductIncompleteError, got %v", err)
	}
	out := map[string]string{}
	for _, fld := range bad.Fields {
		if fld.Label == "" {
			t.Errorf("gap on %q carries no label; a missing field has nothing on screen, so the "+
				"message is the only thing naming it", fld.Code)
		}
		out[fld.Code] = fld.Reason
	}
	return out
}

func codesOf(m map[string]string) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func (f *gateFixture) approvalStatus(id uuid.UUID) string {
	f.t.Helper()
	var s string
	if err := svcTestPool.QueryRow(context.Background(),
		`SELECT approval_status FROM products WHERE id=$1`, id).Scan(&s); err != nil {
		f.t.Fatalf("read approval_status: %v", err)
	}
	return s
}

// ─── 1. Every gap, in one refusal ───────────────────────────────────────

// A seller with six gaps must not need six round trips.
//
// This is the whole reason the gate reports a set rather than the first
// problem it finds: a form with six red controls is one afternoon's work, and
// six sequential rejections is the afternoon somebody starts typing "n/a".
func TestSubmitGateReportsEveryGapAtOnce(t *testing.T) {
	f := newGateFixture(t)

	// Nothing: no tax class, no image, an unpriced variant with no stock, and
	// neither required attribute answered.
	p := f.create(listing{noTaxClass: true, noImage: true, noPrice: true, noStock: true})

	err := f.submit(p)
	got := gaps(t, err)

	want := []string{
		builtinPrice, builtinStock, builtinImage, builtinTaxClass,
		f.authorCode, f.pagesCode,
	}
	for _, code := range want {
		if _, ok := got[code]; !ok {
			t.Errorf("gap %q was not reported; got: %s", code, codesOf(got))
		}
	}
	if len(got) != len(want) {
		t.Errorf("want exactly %d gaps, got %d (%s)", len(want), len(got), codesOf(got))
	}

	// The optional attribute must NOT be a gap. A gate that refuses every
	// blank field is not a completeness gate, it is a form that cannot be
	// left unfinished — which is the thing this whole design exists to avoid.
	if _, ok := got[f.isbnCode]; ok {
		t.Error("an OPTIONAL attribute was reported as a gap")
	}

	// And the refusal changed nothing: a listing that failed the gate has not
	// been submitted, so it must not have passed through 'submitted' on its
	// way back to draft. A seller watching their dashboard would see a
	// listing that went to review and returned in the same second.
	if s := f.approvalStatus(p.ID); s != "draft" {
		t.Errorf("a refused submit moved the listing to %q", s)
	}
	if subs, _ := f.store.ProductSubmissions(context.Background(), p.ID, 10); len(subs) != 0 {
		t.Errorf("a refused submit recorded %d submission(s)", len(subs))
	}
}

// ─── 2. Each built-in, on its own ───────────────────────────────────────

// The unit test proves the RULES are independent. This proves each one is
// wired to the fact in the database it claims to read — a check that tested
// the wrong column would pass the unit test and let an unsellable listing
// through.
func TestSubmitGateEachBuiltinRefusalReadsTheRightFact(t *testing.T) {
	answers := func(f *gateFixture) []AttributeValueInput {
		return []AttributeValueInput{
			{Code: f.authorCode, Value: "R K Narayan"},
			{Code: f.pagesCode, Value: 328},
		}
	}

	cases := []struct {
		name string
		code string
		make func(*gateFixture) listing
	}{
		{"no GST class", builtinTaxClass, func(f *gateFixture) listing {
			return listing{noTaxClass: true, answers: answers(f)}
		}},
		{"no photograph", builtinImage, func(f *gateFixture) listing {
			return listing{noImage: true, answers: answers(f)}
		}},
		{"an unpriced variant", builtinPrice, func(f *gateFixture) listing {
			return listing{noPrice: true, answers: answers(f)}
		}},
		{"nothing in stock", builtinStock, func(f *gateFixture) listing {
			return listing{noStock: true, answers: answers(f)}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGateFixture(t)
			p := f.create(tc.make(f))
			got := gaps(t, f.submit(p))
			if len(got) != 1 {
				t.Fatalf("breaking one thing produced %d gaps (%s)", len(got), codesOf(got))
			}
			if _, ok := got[tc.code]; !ok {
				t.Fatalf("want the %q gap, got %s", tc.code, codesOf(got))
			}
			// The sentence has to say why it cannot sell, not merely that a
			// field is empty. A seller told "tax class is required" fills one
			// in at random; told the checkout refuses a line whose tax it
			// cannot compute, they pick the right one.
			if len(got[tc.code]) < 30 {
				t.Errorf("refusal for %q is too terse to act on: %q", tc.code, got[tc.code])
			}
		})
	}
}

// A required attribute the seller has not answered is a gap, and the message
// says WHERE the requirement comes from — a seller looking at a listing filed
// three categories deep needs to know which one asks.
func TestSubmitGateNamesTheRequiredAttributesAndWhereTheyComeFrom(t *testing.T) {
	f := newGateFixture(t)
	p := f.create(listing{answers: []AttributeValueInput{
		{Code: f.authorCode, Value: "R K Narayan"},
	}})

	got := gaps(t, f.submit(p))
	if len(got) != 1 {
		t.Fatalf("only `pages` is unanswered; got %s", codesOf(got))
	}
	if !strings.Contains(got[f.pagesCode], "category") {
		t.Errorf("the refusal does not say where the requirement comes from: %q", got[f.pagesCode])
	}
}

// ─── 3. The happy path ──────────────────────────────────────────────────

func TestSubmitGatePassesAFinishedListingAndRecordsTheSubmission(t *testing.T) {
	ctx := context.Background()
	f := newGateFixture(t)
	p := f.complete()

	if err := f.submit(p); err != nil {
		t.Fatalf("a complete listing was refused: %v", err)
	}
	if s := f.approvalStatus(p.ID); s != "submitted" {
		t.Fatalf("approval_status is %q, want submitted", s)
	}

	subs, err := f.store.ProductSubmissions(ctx, p.ID, 10)
	if err != nil {
		t.Fatalf("read submissions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("want one submission, got %d", len(subs))
	}
	s := subs[0]
	if s.Attempt != 1 {
		t.Errorf("first submission is attempt %d", s.Attempt)
	}
	if s.SubmittedBy == nil || *s.SubmittedBy != f.userID {
		t.Errorf("the submission does not record who made it: %v", s.SubmittedBy)
	}
	if s.SellerID != f.sellerID {
		t.Errorf("the submission is attributed to the wrong shop")
	}

	// The snapshot carries the VALUES, not a pass/fail. A diff built from
	// booleans could only ever say "Stock: true → true".
	byCode := map[string]postgres.SubmissionValue{}
	for _, v := range s.Snapshot {
		byCode[v.Code] = v
	}
	if got := byCode[f.authorCode].Value; got != "R K Narayan" {
		t.Errorf("the snapshot did not record the author: %#v", got)
	}
	if byCode[f.authorCode].Label == "" {
		t.Error("the snapshot froze no label; renaming the attribute would rewrite history")
	}
	if got := byCode[builtinStock].Value; got == nil {
		t.Error("the snapshot recorded no stock figure")
	}
	// Every effective field is in the snapshot, answered or not — a diff has
	// to be able to show a field going from nothing to something.
	if _, ok := byCode[f.isbnCode]; !ok {
		t.Error("an unanswered optional field is missing from the snapshot, so the diff could " +
			"never show the seller filling it in")
	}
}

// ─── 4. A draft may be incomplete ───────────────────────────────────────

// The other half of the principle, asserted from the service side: the create
// and the patch do not ask whether the form is finished, and only the submit
// does.
//
// The HTTP-level twin of this is TestProductWriteADraftSavesHappilyWhileIncomplete.
func TestSubmitGateADraftStillSavesAndPatchesWhileIncomplete(t *testing.T) {
	ctx := context.Background()
	f := newGateFixture(t)

	// A create with neither required field.
	p := f.create(listing{noTaxClass: true, noImage: true})
	if p.ApprovalStatus != "draft" {
		t.Fatalf("a create must land in draft, got %q", p.ApprovalStatus)
	}

	// And a patch that still leaves it incomplete is accepted.
	if _, err := f.svc.UpdateProduct(ctx, UpdateProductInput{
		ActorUserID: f.userID, ProductID: p.ID,
		Attributes: []AttributeValueInput{{Code: f.authorCode, Value: "R K Narayan"}},
	}); err != nil {
		t.Fatalf("a patch that leaves the listing incomplete was refused: %v", err)
	}

	// A WRONG value is still refused, though — incomplete is fine, wrong is
	// not, and that distinction is the whole design.
	_, err := f.svc.UpdateProduct(ctx, UpdateProductInput{
		ActorUserID: f.userID, ProductID: p.ID,
		Attributes: []AttributeValueInput{{Code: f.pagesCode, Value: "many"}},
	})
	var invalid *AttributeValuesInvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("a wrong value on a draft was accepted or misreported: %v", err)
	}

	// Only the submit asks.
	if got := gaps(t, f.submit(p)); len(got) == 0 {
		t.Fatal("the submit gate found nothing wrong with a listing missing three things")
	}

	// And the readiness read answers the same question without submitting,
	// so a seller sees the checklist instead of pressing the button to find
	// out what it says.
	missing, err := f.svc.ProductReadiness(ctx, p.ID, f.userID)
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if len(missing) == 0 {
		t.Fatal("readiness reported a listing as ready that the gate refuses")
	}
	if f.approvalStatus(p.ID) != "draft" {
		t.Fatal("asking whether a listing is ready submitted it")
	}
}

// ─── 5. The reviewer diff ───────────────────────────────────────────────

// Rejected, edited, resubmitted — and the reviewer sees the three things that
// changed rather than the whole listing again.
//
// The re-submission itself is the part that did not work before this step:
// SubmitProductForReview accepted only `approval_status='draft'`, so a
// rejected listing could be edited and never resubmitted, and
// "request changes" was a dead end whose own doc comment promised otherwise.
func TestSubmissionDiffOverARejectedThenResubmittedProduct(t *testing.T) {
	ctx := context.Background()
	f := newGateFixture(t)
	p := f.complete()

	if err := f.submit(p); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if err := f.store.RejectProductByAdmin(ctx, p.ID, uuid.New(), "the cover photo is the wrong book"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if s := f.approvalStatus(p.ID); s != "rejected" {
		t.Fatalf("approval_status is %q after a rejection", s)
	}

	// The seller fixes it: corrects the page count and fills in the ISBN they
	// had left blank.
	if _, err := f.svc.UpdateProduct(ctx, UpdateProductInput{
		ActorUserID: f.userID, ProductID: p.ID,
		Attributes: []AttributeValueInput{
			{Code: f.pagesCode, Value: 412},
			{Code: f.isbnCode, Value: "9780143039655"},
		},
	}); err != nil {
		t.Fatalf("patch after rejection: %v", err)
	}

	if err := f.submit(p); err != nil {
		t.Fatalf("a rejected listing could not be resubmitted — which makes 'request changes' a "+
			"dead end: %v", err)
	}

	subs, diff, err := f.svc.ProductSubmissionHistory(ctx, p.ID, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("want two attempts, got %d", len(subs))
	}
	if subs[0].Attempt != 2 || subs[1].Attempt != 1 {
		t.Fatalf("attempts are %d and %d, want 2 then 1 (newest first)", subs[0].Attempt, subs[1].Attempt)
	}
	if diff.FirstSubmission {
		t.Fatal("a re-submission is not a first submission")
	}
	if diff.Previous == nil || diff.Previous.Attempt != 1 {
		t.Fatal("the diff does not name the attempt it compared against")
	}

	byCode := map[string]SubmissionFieldChange{}
	for _, c := range diff.Changes {
		byCode[c.Code] = c
	}
	if len(diff.Changes) != 2 {
		t.Fatalf("want exactly the two fields the seller touched, got %+v", diff.Changes)
	}
	if c := byCode[f.isbnCode]; c.Change != "added" || c.After != "9780143039655" {
		t.Errorf("filling in the ISBN should read as added: %+v", c)
	}
	if c := byCode[f.pagesCode]; c.Change != "changed" {
		t.Errorf("correcting the page count should read as changed: %+v", c)
	}
	// Labels, beside the codes, because the reviewer is a person.
	for _, c := range diff.Changes {
		if c.Label == "" {
			t.Errorf("change on %q has no label", c.Code)
		}
	}
	// And the field the seller did NOT touch is absent — which is the entire
	// point of a diff over handing the reviewer the listing again.
	if _, ok := byCode[f.authorCode]; ok {
		t.Error("an untouched field appeared in the diff")
	}
}

// ─── 6. The sweeper, and the listing that keeps selling ─────────────────

// approve puts a listing live the way the moderation queue does.
func (f *gateFixture) approve(p *postgres.Product) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.svc.SubmitProduct(ctx, p.ID, f.userID); err != nil {
		f.t.Fatalf("submit before approve: %v", err)
	}
	if err := f.store.ApproveProductByAdmin(ctx, p.ID, uuid.New(), "ok"); err != nil {
		f.t.Fatalf("approve: %v", err)
	}
}

func (f *gateFixture) variantID(productID uuid.UUID) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	if err := svcTestPool.QueryRow(context.Background(),
		`SELECT id FROM product_variants WHERE product_id=$1 LIMIT 1`, productID).Scan(&id); err != nil {
		f.t.Fatalf("read variant: %v", err)
	}
	return id
}

// Decision 8, end to end: an operator makes a field required AFTER a listing
// went live, the sweep flags it, the seller is told — and the listing is still
// on sale the whole time.
//
// The last assertion is the one that matters. Every other design for "this
// listing is no longer compliant" delists it, and delisting a shop's
// catalogue for a rule its seller was never shown is not a policy, it is an
// outage they experience as the platform breaking their business.
func TestSweeperFlagsANewlyRequiredFieldAndTheListingKeepsSelling(t *testing.T) {
	ctx := context.Background()
	f := newGateFixture(t)

	// A listing that is complete under TODAY's rules: `isbn` is optional.
	p := f.complete()
	f.approve(p)

	variant := f.variantID(p.ID)
	sellable := func() bool {
		f.t.Helper()
		_, ok, err := f.store.ProductSaleEligibility(ctx, variant)
		if err != nil {
			t.Fatalf("sale eligibility: %v", err)
		}
		return ok
	}
	if !sellable() {
		t.Fatal("the fixture listing is not sellable before the sweep, so the test proves nothing")
	}

	// The founder tightens the rule: ISBN becomes required on this category.
	if _, err := svcTestPool.Exec(ctx,
		`UPDATE category_attributes SET is_required = TRUE
		  WHERE category_id=$1 AND definition_id=$2`, f.categoryID, f.isbnDef); err != nil {
		t.Fatalf("tighten the rule: %v", err)
	}

	res, err := f.svc.SweepComplianceGaps(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.DefinitionsChecked == 0 {
		t.Fatal("the sweep checked no definitions at all")
	}

	open, err := f.store.OpenGapsForProduct(ctx, p.ID)
	if err != nil {
		t.Fatalf("read gaps: %v", err)
	}
	if len(open) != 1 || open[0].Code != f.isbnCode {
		t.Fatalf("want one gap on %q, got %+v", f.isbnCode, open)
	}
	if open[0].Reason != "missing" {
		t.Errorf("a newly-required unanswered field is 'missing', got %q", open[0].Reason)
	}

	// ── AND IT IS STILL PURCHASABLE ──────────────────────────
	if !sellable() {
		t.Fatal("the sweep took a live listing off sale. Decision 8: making a field required " +
			"later must NEVER delist a listing that was compliant when it was approved")
	}
	var status, approval string
	var published *string
	if err := svcTestPool.QueryRow(ctx,
		`SELECT status, approval_status, published_at::text FROM products WHERE id=$1`, p.ID,
	).Scan(&status, &approval, &published); err != nil {
		t.Fatalf("read lifecycle: %v", err)
	}
	if status != "active" || approval != "approved" || published == nil {
		t.Fatalf("the sweep moved the listing's lifecycle: status=%q approval=%q published=%v",
			status, approval, published)
	}

	// ── The seller's signal ──────────────────────────────────
	needed, err := f.svc.SellerActionNeeded(ctx, f.userID, 50)
	if err != nil {
		t.Fatalf("action needed: %v", err)
	}
	found := false
	for _, item := range needed {
		if item.ProductID != p.ID {
			continue
		}
		found = true
		if !item.StillSelling {
			t.Error("the seller's signal does not say the listing is still selling, which is the " +
				"first thing they will want to know")
		}
		if len(item.Fields) != 1 || item.Fields[0].Code != f.isbnCode {
			t.Errorf("the signal does not name the field to fix: %+v", item.Fields)
		}
		if item.Fields[0].Label == "" {
			t.Error("the signal names a code with no label")
		}
		if !strings.Contains(item.Fields[0].Reason, "still on sale") {
			t.Errorf("the reason does not reassure the seller: %q", item.Fields[0].Reason)
		}
	}
	if !found {
		t.Fatalf("the flagged listing is not in the seller's action-needed list (%d items)", len(needed))
	}

	// ── The founder's queue ──────────────────────────────────
	queue, total, err := f.svc.AdminComplianceGaps(ctx, &f.isbnDef, 50, 0)
	if err != nil {
		t.Fatalf("admin gaps: %v", err)
	}
	if total == 0 || len(queue) == 0 {
		t.Fatal("the founder's queue is empty after a sweep that found a gap")
	}

	// ── The seller fixes it on their next edit ───────────────
	if _, err := f.svc.UpdateProduct(ctx, UpdateProductInput{
		ActorUserID: f.userID, ProductID: p.ID,
		Attributes:      []AttributeValueInput{{Code: f.isbnCode, Value: "9780143039655"}},
		AckRevalidation: true,
	}); err != nil {
		t.Fatalf("fixing the gap: %v", err)
	}
	still, err := f.store.OpenGapsForProduct(ctx, p.ID)
	if err != nil {
		t.Fatalf("re-read gaps: %v", err)
	}
	if len(still) != 0 {
		t.Fatalf("the gap survived the edit that closed it: %+v", still)
	}
}

// A sweep run twice must reach the same state, or the founder's queue grows a
// duplicate row per cycle and the count on it becomes meaningless.
func TestSweeperIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newGateFixture(t)
	p := f.complete()
	f.approve(p)

	if _, err := svcTestPool.Exec(ctx,
		`UPDATE category_attributes SET is_required = TRUE
		  WHERE category_id=$1 AND definition_id=$2`, f.categoryID, f.isbnDef); err != nil {
		t.Fatalf("tighten: %v", err)
	}

	for i := 0; i < 3; i++ {
		res, err := f.svc.SweepComplianceGaps(ctx)
		if err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
		// The SECOND and third sweeps must open nothing. A count that
		// includes the upsert's refresh arm climbs forever, and the number
		// the founder reads after a rule change stops meaning anything.
		if i > 0 && res.GapsOpened != 0 {
			t.Errorf("sweep %d reported %d newly-opened gaps over an unchanged catalogue",
				i+1, res.GapsOpened)
		}
	}
	open, err := f.store.OpenGapsForProduct(ctx, p.ID)
	if err != nil {
		t.Fatalf("read gaps: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("three sweeps produced %d open gaps on one field: %s", len(open), gapCodes(open))
	}
}

func gapCodes(gs []*postgres.ComplianceGap) string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = fmt.Sprintf("%s/%s", g.Code, g.Reason)
	}
	return strings.Join(out, ", ")
}

// A gap and the impact COUNT must agree about what is in violation. They are
// built from the same SQL (attributeViolationCTE) precisely so they cannot
// drift, and this is the test that notices if somebody forks it.
func TestSweeperAgreesWithTheImpactCountTheOperatorWasShown(t *testing.T) {
	ctx := context.Background()
	f := newGateFixture(t)
	p := f.complete()
	f.approve(p)

	// What the admin console would tell the founder BEFORE they tick the box.
	before, err := f.store.AttributeImpact(ctx, f.isbnDef)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if before.Missing == 0 {
		t.Fatalf("the impact count sees no listing missing %q, so the comparison is vacuous", f.isbnCode)
	}

	if _, err := svcTestPool.Exec(ctx,
		`UPDATE category_attributes SET is_required = TRUE
		  WHERE category_id=$1 AND definition_id=$2`, f.categoryID, f.isbnDef); err != nil {
		t.Fatalf("tighten: %v", err)
	}
	if _, err := f.svc.SweepComplianceGaps(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	after, err := f.store.AttributeImpact(ctx, f.isbnDef)
	if err != nil {
		t.Fatalf("impact after: %v", err)
	}
	_, total, err := f.svc.AdminComplianceGaps(ctx, &f.isbnDef, 500, 0)
	if err != nil {
		t.Fatalf("gaps: %v", err)
	}
	if total != after.Affected {
		t.Fatalf("the operator was warned about %d affected listings and the sweep flagged %d; "+
			"the count and the sweep have forked their definition of 'in violation'",
			after.Affected, total)
	}
}
