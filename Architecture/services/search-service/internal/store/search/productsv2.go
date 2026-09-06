package search

// products_v2, and the alias that makes it reversible.
//
// ─── WHY A NEW INDEX AND NOT A PUT-MAPPING ──────────────────────────────
//
// Everywhere else in this file's neighbourhood, a new field is added with
// an idempotent put_mapping (putEngagementMapping, putPrivacyMapping,
// putResultRowMapping). That works because those are ADDITIVE: OpenSearch
// will introduce a field it has never seen, and will not change the type of
// one it has.
//
// The product document needs both kinds of change. `price` is a float in
// products_v1 and the money it stands for is an integer count of paise;
// `category` is a single keyword and a listing has to answer for its whole
// ancestor chain; and the attribute values need a `nested` field, which
// cannot be introduced over documents already holding a plain object. None
// of those can be put-mapped onto a live index. They need a new one.
//
// ─── WHY AN ALIAS ───────────────────────────────────────────────────────
//
// Because "the new mapping is wrong" is a thing that happens, and the cost
// of finding out must not be a redeploy.
//
//	products (alias) ──▶ products_v2   ← the new mapping
//	                     products_v1   ← still there, still populated
//
// Every read and write in this service goes through the alias
// (IndexProducts below is the alias name, not an index name), so moving it
// back is one atomic call and nothing else in the system changes — no
// deploy, no config, no restart. products_v1 IS NOT DELETED and this file
// contains no code that could delete it: that index is the rollback, and a
// rollback you have already thrown away is not one.
//
// The alias is also why the switch is atomic. OpenSearch applies an
// _aliases actions list as one operation, so there is no instant in which
// `products` points at nothing and every product search returns empty.
//
// ─── WHY THE ATTRIBUTES ARE `nested` ────────────────────────────────────
//
// Commerce hands over `attributes_doc`, a code→value object: {"author":
// "R. K. Narayan", "page_count": 328, "binding": ["paperback"]}. Mapped as
// a plain object, every code becomes its OWN field, and a catalogue whose
// operators can define new attribute codes at will is then a mapping that
// grows without bound — the "mapping explosion" that eventually refuses
// writes cluster-wide.
//
// So the object is flattened into an ARRAY OF CODE-KEYED PAIRS —
// [{code: "author", value: "R. K. Narayan"}, …] — and mapped `nested` so
// that `code` and `value` stay associated within one pair. Mapped as a
// plain object array they would flatten into two independent lists, and a
// filter for "binding=hardcover" would match a book that is a paperback by
// an author called Hardcover. `nested` is what makes a facet count mean
// what it says.
//
// Two fields, not one, for the value: `value` (keyword) is what a terms
// aggregation buckets on, `value_num` (double) is what a range filter and a
// numeric histogram read. A measure's unit rides alongside as `unit`,
// because 250 g and 250 kg are not the same bucket.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Index / alias names.
//
// IndexProducts is deliberately the ALIAS. Everything that reads or writes
// products in this service names it, so the alias move below is the only
// thing that has to happen for a rollback — there is no second place
// holding a physical index name that would have to be edited and
// redeployed.
const (
	// IndexProductsV1 is the ORIGINAL index. Kept, populated, never
	// deleted: it is what the alias moves back to.
	IndexProductsV1 = "products_v1"
	// IndexProductsV2 carries the mapping this step introduces.
	IndexProductsV2 = "products_v2"
)

// productsV2Mapping is the field definitions for products_v2.
//
// The legacy fields (`category`, `price`, `city`, `status`) are kept
// alongside the new ones and are written on every document. They are not
// dead weight: the /v1/search/products endpoint and the ranked-search
// projection read them, and a v2 index that dropped them would change those
// responses — which this step is not allowed to do. They are mirrors of the
// authoritative new fields, written from the same source in the same call,
// so they cannot drift.
func productsV2Mapping() string {
	return `{
		"settings": ` + opensearchSettingsJSON() + `,
		"mappings": {
			"properties": {
				"product_id":        { "type": "keyword" },
				"seller_id":         { "type": "keyword" },
				"seller_name":       { "type": "text", "fields": { "keyword": { "type": "keyword", "ignore_above": 256 } } },

				"title":             { "type": "text", "analyzer": "english", "fields": { "keyword": { "type": "keyword", "ignore_above": 256 } } },
				"description":       { "type": "text" },
				"short_description": { "type": "text" },
				"brand_name":        { "type": "text", "fields": { "keyword": { "type": "keyword", "ignore_above": 256 } } },
				"search_keywords":   { "type": "text" },
				"condition":         { "type": "keyword" },
				"product_type":      { "type": "keyword" },
				"slug":              { "type": "keyword" },

				"category_id":       { "type": "keyword" },
				"category_name":     { "type": "keyword" },
				"category_ids":      { "type": "keyword" },
				"category_names":    { "type": "keyword" },
				"category_slugs":    { "type": "keyword" },
				"category_path":     { "type": "text" },
				"category":          { "type": "keyword" },

				"min_price_minor":   { "type": "long" },
				"max_price_minor":   { "type": "long" },
				"mrp_minor":         { "type": "long" },
				"currency":          { "type": "keyword" },
				"price":             { "type": "float" },

				"total_stock":       { "type": "integer" },
				"in_stock":          { "type": "boolean" },

				"image_media_id":    { "type": "keyword" },
				"image_url":         { "type": "keyword", "index": false },

				"avg_rating":        { "type": "float" },
				"review_count":      { "type": "integer" },
				"order_count":       { "type": "integer" },
				"view_count":        { "type": "integer" },
				"engagement_score":  { "type": "double" },

				"city":              { "type": "keyword" },
				"status":            { "type": "keyword" },
				"approval_status":   { "type": "keyword" },
				"is_hidden":         { "type": "boolean" },

				"attributes": {
					"type": "nested",
					"properties": {
						"code":       { "type": "keyword" },
						"value":      { "type": "keyword", "ignore_above": 256 },
						"value_text": { "type": "text" },
						"value_num":  { "type": "double" },
						"unit":       { "type": "keyword" }
					}
				},

				"published_at":      { "type": "date" },
				"created_at":        { "type": "date" },
				"updated_at":        { "type": "date" }
			}
		}
	}`
}

// ─── The document ───────────────────────────────────────────────────────

// ProductAttributePair is one code-keyed attribute value.
type ProductAttributePair struct {
	Code string `json:"code"`
	// Value is the string form, and it is what a terms facet buckets on.
	// A number is stringified here as well as kept in ValueNum, because a
	// facet over "page_count" is still a facet and a bucket key has to be
	// something a client can echo back as a filter.
	Value     string   `json:"value"`
	ValueText string   `json:"value_text,omitempty"`
	ValueNum  *float64 `json:"value_num,omitempty"`
	Unit      string   `json:"unit,omitempty"`
}

// ProductV2Doc is one product as products_v2 holds it.
type ProductV2Doc struct {
	ProductID  string `json:"product_id"`
	SellerID   string `json:"seller_id"`
	SellerName string `json:"seller_name,omitempty"`

	Title            string   `json:"title"`
	Description      string   `json:"description,omitempty"`
	ShortDescription string   `json:"short_description,omitempty"`
	BrandName        string   `json:"brand_name,omitempty"`
	SearchKeywords   []string `json:"search_keywords,omitempty"`
	Condition        string   `json:"condition,omitempty"`
	ProductType      string   `json:"product_type,omitempty"`
	Slug             string   `json:"slug,omitempty"`

	CategoryID   string `json:"category_id,omitempty"`
	CategoryName string `json:"category_name,omitempty"`
	// CategoryIDs / Names / Slugs are the WHOLE chain, root-first, leaf
	// included. This is what makes a "Books" filter match a listing filed
	// under Books › Textbooks: the filter is a term on category_ids, and
	// the parent's id is in the list.
	CategoryIDs   []string `json:"category_ids,omitempty"`
	CategoryNames []string `json:"category_names,omitempty"`
	CategorySlugs []string `json:"category_slugs,omitempty"`
	CategoryPath  string   `json:"category_path,omitempty"`
	// Category is the legacy single-value field /v1/search/products has
	// always filtered on. Mirror of CategoryName.
	Category string `json:"category,omitempty"`

	MinPriceMinor int64  `json:"min_price_minor"`
	MaxPriceMinor int64  `json:"max_price_minor"`
	MRPMinor      int64  `json:"mrp_minor,omitempty"`
	Currency      string `json:"currency,omitempty"`
	// Price is the legacy rupee mirror. Derived from MinPriceMinor, never
	// carried separately — the minor field is the money.
	Price float64 `json:"price"`

	TotalStock int  `json:"total_stock"`
	InStock    bool `json:"in_stock"`

	ImageMediaID string `json:"image_media_id,omitempty"`
	ImageURL     string `json:"image_url,omitempty"`

	AvgRating       float64 `json:"avg_rating"`
	ReviewCount     int     `json:"review_count"`
	OrderCount      int     `json:"order_count"`
	ViewCount       int     `json:"view_count"`
	EngagementScore float64 `json:"engagement_score"`

	City           string `json:"city,omitempty"`
	Status         string `json:"status,omitempty"`
	ApprovalStatus string `json:"approval_status,omitempty"`

	Attributes []ProductAttributePair `json:"attributes"`

	PublishedAt *time.Time `json:"published_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
}

// FlattenAttributes turns commerce's attributes_doc into nested pairs.
//
// The three shapes rebuildAttributesDocTx can produce, and what each
// becomes:
//
//	{"value": 250, "unit": "g"}   a measure  → one pair, Unit set
//	["en", "hi"]                  a multi_enum → one pair PER member, so a
//	                              filter for "hi" matches without the
//	                              consumer having to know the field repeats
//	328 / "R. K. Narayan" / true  everything else → one pair
//
// Sorted by (code, value) so two products with the same values produce
// byte-identical documents. That is not cosmetic: it makes a re-index a
// no-op at the storage layer and makes a diff of two documents readable
// when one of them is wrong.
func FlattenAttributes(doc map[string]any) []ProductAttributePair {
	pairs := []ProductAttributePair{}
	for code, raw := range doc {
		pairs = append(pairs, attributePairs(code, raw)...)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Code != pairs[j].Code {
			return pairs[i].Code < pairs[j].Code
		}
		return pairs[i].Value < pairs[j].Value
	})
	return pairs
}

func attributePairs(code string, raw any) []ProductAttributePair {
	switch v := raw.(type) {
	case nil:
		return nil
	case []any:
		out := []ProductAttributePair{}
		for _, member := range v {
			out = append(out, attributePairs(code, member)...)
		}
		return out
	case map[string]any:
		// A measure: {"value": …, "unit": …}. Anything else shaped like an
		// object is not something a facet can bucket, and is skipped rather
		// than stringified into a key nobody can filter on.
		val, hasVal := v["value"]
		if !hasVal {
			return nil
		}
		pairs := attributePairs(code, val)
		if unit, ok := v["unit"].(string); ok {
			for i := range pairs {
				pairs[i].Unit = unit
			}
		}
		return pairs
	case float64:
		n := v
		return []ProductAttributePair{{
			Code:      code,
			Value:     strconv.FormatFloat(n, 'f', -1, 64),
			ValueText: strconv.FormatFloat(n, 'f', -1, 64),
			ValueNum:  &n,
		}}
	case bool:
		return []ProductAttributePair{{Code: code, Value: strconv.FormatBool(v), ValueText: strconv.FormatBool(v)}}
	case string:
		p := ProductAttributePair{Code: code, Value: v, ValueText: v}
		// A stored number that arrived as a JSON string still deserves its
		// numeric form: a range filter on a page count must not depend on
		// which of two encodings commerce happened to use.
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			p.ValueNum = &n
		}
		return []ProductAttributePair{p}
	default:
		return nil
	}
}

// ─── Index / alias lifecycle ────────────────────────────────────────────

// ensureProductsIndex creates products_v2 and points the `products` alias
// at it the FIRST time, then never touches the alias again.
//
// "Then never again" is the important half. A boot that re-pointed the
// alias would undo an operator's rollback on the next restart — the
// rollback would appear to work, and the next deploy would silently move
// production back onto the mapping they rejected. So: if the alias already
// exists, wherever it points, this leaves it alone.
func (s *Store) ensureProductsIndex(ctx context.Context) {
	s.createIndexIfNotExists(ctx, IndexProductsV2, productsV2Mapping())

	target, err := s.ProductsAliasTarget(ctx)
	if err == nil && target != "" {
		slog.Info("search: products alias already set", "alias", IndexProducts, "index", target)
		return
	}
	if err := s.MoveProductsAlias(ctx, IndexProductsV2); err != nil {
		slog.Error("search: could not point the products alias at products_v2", "err", err)
		return
	}
	slog.Info("search: products alias created", "alias", IndexProducts, "index", IndexProductsV2)
}

// ProductsAliasTarget reports which physical index `products` resolves to,
// or "" when the alias does not exist.
func (s *Store) ProductsAliasTarget(ctx context.Context) (string, error) {
	req := opensearchapi.IndicesGetAliasRequest{Name: []string{IndexProducts}}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return "", nil
	}
	if res.IsError() {
		return "", fmt.Errorf("get alias %s: %s", IndexProducts, res.String())
	}
	var parsed map[string]any
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return "", err
	}
	// One alias may name several indices. Sorted so the answer is stable
	// and a two-index alias is visible rather than arbitrary.
	names := make([]string, 0, len(parsed))
	for name := range parsed {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", nil
	}
	if len(names) > 1 {
		return "", fmt.Errorf("alias %s points at %d indices (%v); a write alias must name exactly one",
			IndexProducts, len(names), names)
	}
	return names[0], nil
}

// MoveProductsAlias points `products` at exactly one index, atomically.
//
// One _aliases call carrying both the removal and the addition. Two calls
// would leave a window in which the alias names nothing and every product
// search returns an index_not_found error — during a rollback, which is
// the moment least able to afford a second outage.
//
// `remove` names the alias with a wildcard index so it succeeds whether or
// not the alias currently exists, and whichever index it currently points
// at. `must_exist:false` is what makes the first call — when there is no
// alias yet — not an error.
func (s *Store) MoveProductsAlias(ctx context.Context, index string) error {
	body := fmt.Sprintf(`{"actions":[
		{"remove":{"index":"*","alias":%q,"must_exist":false}},
		{"add":{"index":%q,"alias":%q,"is_write_index":true}}
	]}`, IndexProducts, index, IndexProducts)

	req := opensearchapi.IndicesUpdateAliasesRequest{Body: bytes.NewReader([]byte(body))}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("move alias %s -> %s: %s", IndexProducts, index, res.String())
	}
	return nil
}

// IndexProductV2Doc upserts one product document through the alias.
func (s *Store) IndexProductV2Doc(ctx context.Context, doc ProductV2Doc) error {
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityProducts,
			engagementCounters{Views: doc.ViewCount, Purchases: doc.OrderCount})
	}
	if doc.Attributes == nil {
		doc.Attributes = []ProductAttributePair{}
	}
	return s.indexDoc(ctx, IndexProducts, doc.ProductID, doc)
}

// BulkIndexProductV2 writes a page of documents in one _bulk call and
// returns how many were accepted.
//
// A reindex of a real catalogue is thousands of documents; one HTTP round
// trip each is the difference between a reindex that takes a minute and
// one nobody runs.
func (s *Store) BulkIndexProductV2(ctx context.Context, docs []ProductV2Doc) (int, error) {
	if len(docs) == 0 {
		return 0, nil
	}
	var buf bytes.Buffer
	for i := range docs {
		if docs[i].ProductID == "" {
			continue
		}
		if docs[i].Attributes == nil {
			docs[i].Attributes = []ProductAttributePair{}
		}
		if docs[i].EngagementScore == 0 {
			docs[i].EngagementScore = computeEngagementScore(EntityProducts,
				engagementCounters{Views: docs[i].ViewCount, Purchases: docs[i].OrderCount})
		}
		meta, _ := json.Marshal(map[string]any{
			"index": map[string]any{"_index": IndexProducts, "_id": docs[i].ProductID},
		})
		line, err := json.Marshal(docs[i])
		if err != nil {
			return 0, err
		}
		buf.Write(meta)
		buf.WriteByte('\n')
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if buf.Len() == 0 {
		return 0, nil
	}

	req := opensearchapi.BulkRequest{Body: bytes.NewReader(buf.Bytes())}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return 0, fmt.Errorf("bulk index %s: %s", IndexProducts, res.String())
	}
	var parsed struct {
		Items []map[string]struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return 0, err
	}
	// Counted per item, not from `errors:false`. A bulk response reports a
	// per-document status, and a run that says "indexed 4000" while 12 were
	// rejected for a mapping conflict is the reindex report lying about the
	// one thing it exists to tell you.
	ok := 0
	for _, item := range parsed.Items {
		for _, r := range item {
			if r.Status >= 200 && r.Status < 300 {
				ok++
			}
		}
	}
	return ok, nil
}

// CountProducts returns how many documents the alias currently resolves to.
func (s *Store) CountProducts(ctx context.Context) (int64, error) {
	return s.countIndex(ctx, IndexProducts)
}

// CountIndexDocs counts one named index — used to report the old index's
// contents next to the new one's without moving the alias.
func (s *Store) CountIndexDocs(ctx context.Context, index string) (int64, error) {
	return s.countIndex(ctx, index)
}

func (s *Store) countIndex(ctx context.Context, index string) (int64, error) {
	req := opensearchapi.CountRequest{Index: []string{index}}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return 0, nil
	}
	if res.IsError() {
		return 0, fmt.Errorf("count %s: %s", index, res.String())
	}
	var parsed struct {
		Count int64 `json:"count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return 0, err
	}
	return parsed.Count, nil
}
