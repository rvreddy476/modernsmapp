//go:build integration

package postgres

// The inheritance walk, against a real recursive CTE and a real taxonomy.
//
// This is the half of the attribute schema that cannot be proved in memory.
// "Nearest ancestor wins" is one DISTINCT ON with one ORDER BY, and every way
// of getting it wrong — taking the deepest instead of the nearest, unioning
// the ancestors' opinions, letting an exclusion be overruled by the row it was
// written to overrule — produces a query that still returns rows.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -run Attribute -v

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// attrFixture is a three-level chain — Root › Mid › Leaf — and four
// definitions, one per rule under test.
type attrFixture struct {
	store *Store
	root  uuid.UUID
	mid   uuid.UUID
	leaf  uuid.UUID
	defs  map[string]uuid.UUID
	codes map[string]string
}

func newAttrFixture(t *testing.T) *attrFixture {
	t.Helper()
	f := &attrFixture{
		store: New(testPool),
		root:  uuid.New(), mid: uuid.New(), leaf: uuid.New(),
		defs:  map[string]uuid.UUID{},
		codes: map[string]string{},
	}
	tag := f.root.String()[:8]

	mustExec(t, `INSERT INTO product_categories (id,parent_id,name,slug,display_order,is_active)
	             VALUES ($1,NULL,'Attr Root',$2,1,TRUE)`, f.root, "attr-root-"+tag)
	mustExec(t, `INSERT INTO product_categories (id,parent_id,name,slug,display_order,is_active)
	             VALUES ($1,$2,'Attr Mid',$3,1,TRUE)`, f.mid, f.root, "attr-mid-"+tag)
	mustExec(t, `INSERT INTO product_categories (id,parent_id,name,slug,display_order,is_active)
	             VALUES ($1,$2,'Attr Leaf',$3,1,TRUE)`, f.leaf, f.mid, "attr-leaf-"+tag)

	for _, spec := range []struct {
		key, dataType, group string
		active               bool
	}{
		{"overridden", "text", "Product Details", true},
		{"excluded", "text", "Product Details", true},
		{"retired", "text", "Product Details", true},
		{"inherited", "integer", "Logistics", true},
	} {
		id := uuid.New()
		f.defs[spec.key] = id
		f.codes[spec.key] = "t_" + spec.key + "_" + tag
		mustExec(t, `INSERT INTO attribute_definitions
		               (id,code,label,data_type,display_group,applies_to,is_active)
		             VALUES ($1,$2,$3,$4,$5,'item',$6)`,
			id, "t_"+spec.key+"_"+tag, spec.key, spec.dataType, spec.group, spec.active)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, id := range f.defs {
			_, _ = testPool.Exec(ctx, `DELETE FROM attribute_definitions WHERE id=$1`, id)
		}
		_, _ = testPool.Exec(ctx, `DELETE FROM product_categories WHERE id IN ($1,$2,$3)`,
			f.leaf, f.mid, f.root)
	})
	return f
}

func (f *attrFixture) bind(t *testing.T, category uuid.UUID, def string, required, excluded bool) {
	t.Helper()
	mustExec(t, `INSERT INTO category_attributes
	               (category_id,definition_id,is_required,is_excluded,sort_order)
	             VALUES ($1,$2,$3,$4,10)`, category, f.defs[def], required, excluded)
}

func effectiveByCode(rows []*EffectiveAttribute) map[string]*EffectiveAttribute {
	out := map[string]*EffectiveAttribute{}
	for _, r := range rows {
		out[r.Definition.Code] = r
	}
	return out
}

func TestEffectiveCategoryAttributesResolvesInheritance(t *testing.T) {
	f := newAttrFixture(t)
	ctx := context.Background()

	// `overridden` is bound on BOTH Root (required) and Mid (not required).
	// The nearest binding is Mid's, so the leaf must NOT see it as required.
	// A query that took the root's opinion — or unioned the two — would make
	// a field required that the merchandiser deliberately relaxed for this
	// branch, and every seller under it would be blocked.
	f.bind(t, f.root, "overridden", true, false)
	f.bind(t, f.mid, "overridden", false, false)

	// `excluded` is bound on Root and EXCLUDED on Mid. Exclusion is the only
	// way a branch can escape an inherited field — the row belongs to the
	// ancestor and deleting it there would drop the field for every sibling.
	f.bind(t, f.root, "excluded", true, false)
	f.bind(t, f.mid, "excluded", false, true)

	// `retired` is bound on Root and the DEFINITION is deactivated. Products
	// keep their stored values and the console still lists it; a form must
	// not go on asking for it.
	f.bind(t, f.root, "retired", false, false)
	mustExec(t, `UPDATE attribute_definitions SET is_active=FALSE WHERE id=$1`, f.defs["retired"])

	// `inherited` is bound ONLY on Root, two levels above the leaf. This is
	// the case the whole binding table exists for.
	f.bind(t, f.root, "inherited", true, false)

	rows, err := f.store.EffectiveCategoryAttributes(ctx, f.leaf)
	if err != nil {
		t.Fatalf("EffectiveCategoryAttributes: %v", err)
	}
	got := effectiveByCode(rows)
	find := func(key string) *EffectiveAttribute { return got[f.codes[key]] }

	overridden := find("overridden")
	if overridden == nil {
		t.Fatal("the leaf must inherit `overridden` from its nearest binding")
	}
	if overridden.IsRequired {
		t.Fatal("nearest ancestor wins: Mid binds `overridden` as optional, so the leaf must see it " +
			"optional even though the root binds it required")
	}
	if overridden.Depth != 1 {
		t.Fatalf("`overridden` resolved at depth %d, want 1 (Mid) — the depth is the only thing that "+
			"explains WHERE a surprising requirement came from", overridden.Depth)
	}
	if overridden.BoundAt != f.mid {
		t.Fatalf("`overridden` bound_at = %s, want Mid %s", overridden.BoundAt, f.mid)
	}

	if find("excluded") != nil {
		t.Fatal("Mid excludes `excluded`; the leaf must not be asked for it. An exclusion that loses " +
			"to the ancestor row it was written to overrule is not an exclusion")
	}

	if find("retired") != nil {
		t.Fatal("a deactivated definition must not appear in a form, even where it is still bound")
	}

	inherited := find("inherited")
	if inherited == nil {
		t.Fatal("the leaf must inherit a binding two levels up; that is what the binding table is for")
	}
	if !inherited.IsRequired {
		t.Fatal("`inherited` is bound required on the root and nothing overrides it")
	}
	if inherited.Depth != 2 {
		t.Fatalf("`inherited` resolved at depth %d, want 2 (Root, over a three-level chain)", inherited.Depth)
	}

	// The MID's own form differs from the leaf's only where Mid itself is the
	// binding — which is the point of resolving per category rather than once.
	midRows, err := f.store.EffectiveCategoryAttributes(ctx, f.mid)
	if err != nil {
		t.Fatalf("EffectiveCategoryAttributes(mid): %v", err)
	}
	if len(effectiveByCode(midRows)) != len(got) {
		t.Fatalf("mid and leaf resolve to a different number of fields (%d vs %d); nothing between "+
			"them binds anything", len(midRows), len(rows))
	}

	// The ROOT sees its own bindings, and the root's `overridden` IS required
	// — Mid's relaxation applies to Mid and below, never upward.
	rootRows, err := f.store.EffectiveCategoryAttributes(ctx, f.root)
	if err != nil {
		t.Fatalf("EffectiveCategoryAttributes(root): %v", err)
	}
	for _, ea := range rootRows {
		if ea.Depth != 0 {
			t.Fatalf("a root's own bindings must resolve at depth 0, got %d", ea.Depth)
		}
	}
}

func TestCategoryAttributeOverridesAxisAndGroup(t *testing.T) {
	f := newAttrFixture(t)
	ctx := context.Background()

	// A category may promote a field to a variant axis and move it into a
	// different fieldset without forcing every other category that uses the
	// same definition to agree. `overridden` is a text definition, which is
	// one of the three types a variant axis may be.
	mustExec(t, `INSERT INTO category_attributes
	               (category_id,definition_id,is_required,is_variant_axis,display_group,sort_order)
	             VALUES ($1,$2,FALSE,TRUE,'Product Identity',5)`, f.root, f.defs["overridden"])

	rows, err := f.store.EffectiveCategoryAttributes(ctx, f.leaf)
	if err != nil {
		t.Fatalf("EffectiveCategoryAttributes: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly the one binding, got %d", len(rows))
	}
	ea := rows[0]
	if ea.Definition.IsVariantAxis {
		t.Fatal("the DEFINITION is not an axis; only this category's binding says it is")
	}
	if !ea.IsVariantAxis {
		t.Fatal("the binding's is_variant_axis override must win over the definition's default")
	}
	if ea.DisplayGroup != "Product Identity" {
		t.Fatalf("the binding's display_group override must win; got %q", ea.DisplayGroup)
	}
	if ea.Definition.DisplayGroup != "Product Details" {
		t.Fatal("the override must not rewrite the definition itself — another category still uses it")
	}
}

func TestCategoryPathIsRootFirst(t *testing.T) {
	f := newAttrFixture(t)
	path, err := f.store.CategoryPath(context.Background(), f.leaf)
	if err != nil {
		t.Fatalf("CategoryPath: %v", err)
	}
	want := []string{"Attr Root", "Attr Mid", "Attr Leaf"}
	if len(path) != len(want) {
		t.Fatalf("path = %v, want %v", path, want)
	}
	for i := range want {
		if path[i] != want[i] {
			t.Fatalf("path = %v, want %v — it is rendered as a breadcrumb and reads root-first", path, want)
		}
	}
	if _, err := f.store.CategoryPath(context.Background(), uuid.New()); err != ErrCategoryNotFound {
		t.Fatalf("an unknown category must be ErrCategoryNotFound, got %v", err)
	}
}

func TestAttributeImpactCountsWhatANarrowingEditWouldBreak(t *testing.T) {
	f := newAttrFixture(t)
	ctx := context.Background()

	// `inherited` is an integer field bound on the root, so it is effective
	// for every category in the chain — including the leaf, where the
	// products live. That downward propagation is the same nearest-ancestor
	// rule read from the other end, and it is what makes the count right.
	f.bind(t, f.root, "inherited", false, false)
	mustExec(t, `UPDATE attribute_definitions SET min_num=1, max_num=100 WHERE id=$1`, f.defs["inherited"])

	var code string
	if err := testPool.QueryRow(ctx, `SELECT code FROM attribute_definitions WHERE id=$1`,
		f.defs["inherited"]).Scan(&code); err != nil {
		t.Fatalf("read code: %v", err)
	}

	sellerID, userID := uuid.New(), uuid.New()
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	             VALUES ($1,$2,'Impact Store',$3,'impact@example.test','KA')`,
		sellerID, userID, "impact-"+sellerID.String()[:8])
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM sellers WHERE id=$1`, sellerID) })

	newProduct := func(status, approval string, value *string) uuid.UUID {
		id := uuid.New()
		mustExec(t, `INSERT INTO products (id,seller_id,category_id,title,slug,status,approval_status,return_policy_type)
		             VALUES ($1,$2,$3,'Impact Product',$4,$5,$6,'7_days')`,
			id, sellerID, f.leaf, "impact-"+id.String()[:8], status, approval)
		if value != nil {
			mustExec(t, `INSERT INTO product_attributes (product_id,name,value) VALUES ($1,$2,$3)`,
				id, code, *value)
		}
		return id
	}

	inRange, outOfRange, notANumber := "42", "5000", ""
	newProduct("active", "approved", &inRange)    // fine
	newProduct("active", "approved", &outOfRange) // above max_num
	newProduct("active", "approved", nil)         // missing
	newProduct("active", "approved", &notANumber) // empty string counts as missing, not as garbage
	// A draft is not live and must not be counted: making a field required
	// does not break a listing nobody can buy.
	newProduct("draft", "draft", nil)

	imp, err := f.store.AttributeImpact(ctx, f.defs["inherited"])
	if err != nil {
		t.Fatalf("AttributeImpact: %v", err)
	}
	if imp.LiveProducts != 4 {
		t.Fatalf("live_products = %d, want 4 — the draft must not be counted", imp.LiveProducts)
	}
	if imp.Missing != 2 {
		t.Fatalf("missing = %d, want 2 (no row, and an empty value)", imp.Missing)
	}
	if imp.OutOfRange != 1 {
		t.Fatalf("out_of_range = %d, want 1 (5000 is above max_num 100)", imp.OutOfRange)
	}
	if imp.Affected != 3 {
		t.Fatalf("affected = %d, want missing+out_of_range = 3; that is the number ack_impact must match",
			imp.Affected)
	}
}

func TestPublishBumpsTheSchemaVersionAndClearsTheDraftFlag(t *testing.T) {
	lockSchemaState(t)
	ctx := context.Background()
	store := New(testPool)

	if err := store.MarkAttributeSchemaDirty(ctx); err != nil {
		t.Fatalf("MarkAttributeSchemaDirty: %v", err)
	}
	dirty, err := store.GetAttributeSchemaState(ctx)
	if err != nil {
		t.Fatalf("GetAttributeSchemaState: %v", err)
	}
	if !dirty.DraftDirty {
		t.Fatal("a definition edit must mark the draft dirty; that is what stops a half-typed field " +
			"going live the moment it is saved")
	}

	published, err := store.PublishAttributeSchema(ctx)
	if err != nil {
		t.Fatalf("PublishAttributeSchema: %v", err)
	}
	if published.PublishedVersion != dirty.PublishedVersion+1 {
		t.Fatalf("published_version went %d → %d, want +1", dirty.PublishedVersion, published.PublishedVersion)
	}
	if published.DraftDirty {
		t.Fatal("publishing must clear the dirty flag")
	}
	if published.PublishedAt == nil {
		t.Fatal("publishing must stamp published_at")
	}
}

func TestCategoryTreeNestsChildrenUnderParents(t *testing.T) {
	f := newAttrFixture(t)
	roots, err := f.store.CategoryTree(context.Background(), false)
	if err != nil {
		t.Fatalf("CategoryTree: %v", err)
	}

	var findNode func([]*CategoryTreeNode, uuid.UUID) *CategoryTreeNode
	findNode = func(nodes []*CategoryTreeNode, id uuid.UUID) *CategoryTreeNode {
		for _, n := range nodes {
			if n.ID == id {
				return n
			}
			if hit := findNode(n.Children, id); hit != nil {
				return hit
			}
		}
		return nil
	}

	root := findNode(roots, f.root)
	if root == nil {
		t.Fatal("the fixture's root category must appear at the top level")
	}
	if root.Depth != 0 {
		t.Fatalf("a root's depth is %d, want 0", root.Depth)
	}
	mid := findNode(root.Children, f.mid)
	if mid == nil {
		t.Fatal("Mid must be nested under Root, not returned flat")
	}
	leaf := findNode(mid.Children, f.leaf)
	if leaf == nil {
		t.Fatal("Leaf must be nested under Mid")
	}
	if leaf.Depth != 2 {
		t.Fatalf("Leaf depth = %d, want 2", leaf.Depth)
	}

	// ?depth=2 trims the third level and leaves an EMPTY array, never a null:
	// a client decoding `children` into an array type must not fail on a
	// trimmed node.
	trimmed := PruneTreeDepth(roots, 2)
	root = findNode(trimmed, f.root)
	mid = findNode(root.Children, f.mid)
	if mid == nil {
		t.Fatal("depth=2 must still include the second level")
	}
	if mid.Children == nil {
		t.Fatal("a trimmed node must carry an empty children array, not null")
	}
	if len(mid.Children) != 0 {
		t.Fatalf("depth=2 must trim the third level, got %d children", len(mid.Children))
	}
}
