// Package commerceclient reads product documents back from
// commerce-service, which owns the catalogue.
//
// ─── WHY A READ-BACK AND NOT AN EVENT PAYLOAD ───────────────────────────
//
// commerce-service publishes commerce.product.published /
// commerce.product.unpublished carrying ids and nothing else. This package
// is the other half of that decision: when one of those events arrives,
// the consumer asks commerce what the product says NOW and indexes that.
//
// A fat event payload would be a copy of the catalogue frozen at publish
// time, and the index would then be as correct as the message stream is
// orderly — which no message stream is:
//
//	delayed       the price changed while the message sat in a partition.
//	replayed      a DLQ replay a day later re-indexes yesterday's title.
//	out of order  two transitions land backwards and the index settles on
//	              whichever arrived last, not whichever happened last.
//
// With a read-back, all three converge on the same document, because the
// document is not carried by the message — it is fetched. That is why the
// consumer keys its decision on the fetched doc's `visible` field and not
// on which event type woke it: an unpublish that overtakes its publish
// still leaves the index agreeing with the catalogue.
//
// FAILURES ARE ERRORS, NOT DEFAULTS. Unlike mediaclient (a missing
// thumbnail is a blank tile), an unresolved read-back means we do not know
// whether this listing should be in the index. Returning "not visible"
// would delete live listings during a commerce outage; returning an empty
// document would index a blank card. So a transport failure is an error,
// the consumer retries, and the message eventually dead-letters — the
// durable-outcome rule the rest of this consumer already follows.
//
// A 404 is the one case that is NOT an error: the product is gone, and
// "gone" is a fact, not a failure.
package commerceclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrProductGone means commerce answered 404: the product does not exist.
// The consumer treats it as "delete the document", never as a failure.
var ErrProductGone = errors.New("commerceclient: product not found")

// ErrNotConfigured means COMMERCE_SERVICE_URL was never set. Distinct from
// a transport failure so the consumer can log the difference between "the
// operator did not wire this" and "commerce is down".
var ErrNotConfigured = errors.New("commerceclient: COMMERCE_SERVICE_URL not configured")

// Category is one rung of a product's category chain, root-first.
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// SearchDoc mirrors commerce-service's postgres.SearchDoc. Unknown fields
// are ignored by encoding/json, so commerce may add one without a
// coordinated deploy.
type SearchDoc struct {
	ProductID  string `json:"product_id"`
	SellerID   string `json:"seller_id"`
	SellerName string `json:"seller_name"`

	// Visible is the field that decides index-vs-delete. See the package
	// comment.
	Visible        bool   `json:"visible"`
	Status         string `json:"status"`
	ApprovalStatus string `json:"approval_status"`

	Title            string `json:"title"`
	Description      string `json:"description"`
	ShortDescription string `json:"short_description"`
	BrandName        string `json:"brand_name"`
	Condition        string `json:"condition"`
	ProductType      string `json:"product_type"`
	Slug             string `json:"slug"`

	CategoryID   string     `json:"category_id"`
	CategoryName string     `json:"category_name"`
	CategoryPath []Category `json:"category_path"`

	MinPriceMinor int64  `json:"min_price_minor"`
	MaxPriceMinor int64  `json:"max_price_minor"`
	MRPMinor      int64  `json:"mrp_minor"`
	Currency      string `json:"currency"`

	TotalStock int  `json:"total_stock"`
	InStock    bool `json:"in_stock"`

	ImageMediaID string `json:"image_media_id"`
	ImageURL     string `json:"image_url"`

	AvgRating   float64 `json:"avg_rating"`
	ReviewCount int     `json:"review_count"`
	OrderCount  int     `json:"order_count"`
	ViewCount   int     `json:"view_count"`

	// Attributes is commerce's `products.attributes_doc`: definition CODE →
	// value. A measure is {"value":…,"unit":…}; a multi_enum is always an
	// array. Codes, never labels.
	Attributes map[string]any `json:"attributes"`

	SearchKeywords []string   `json:"search_keywords"`
	PublishedAt    *time.Time `json:"published_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// FacetOption is one enum option of a filterable definition.
type FacetOption struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	SwatchHex string `json:"swatch_hex"`
	SortOrder int    `json:"sort_order"`
}

// FacetDefinition is one attribute definition an operator has marked
// filterable, with its options. Codes AND labels: the code is the key an
// aggregation and a stored filter are written in, the label is what a
// buyer reads and may be re-worded at any time without a deploy.
type FacetDefinition struct {
	Code       string        `json:"code"`
	Label      string        `json:"label"`
	DataType   string        `json:"data_type"`
	UnitFamily string        `json:"unit_family"`
	Group      string        `json:"display_group"`
	AppliesTo  string        `json:"applies_to"`
	Options    []FacetOption `json:"options"`
}

// Page is one keyset page of the reindex walk.
type Page struct {
	Items        []SearchDoc
	NextCursor   string
	VisibleTotal int
}

// Client talks to commerce-service's internal read-back routes.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client

	// Facet definitions change when an operator edits the schema, which is
	// rare, and are read on every faceted query, which is not. A short TTL
	// keeps a facet rail from putting one commerce call on every search
	// while still making "tick is_filterable" visible within a minute —
	// the same bargain the category attribute-schema endpoint strikes with
	// its own Cache-Control: max-age=60.
	facetsMu   sync.Mutex
	facets     []FacetDefinition
	facetsAt   time.Time
	facetsTTL  time.Duration
	facetsErr  error
	facetsOnce bool
}

// New builds a client. An empty baseURL yields a client whose every call
// returns ErrNotConfigured — callers check Configured() at startup and warn
// rather than crashing a search engine over an optional integration.
func New(baseURL, internalKey string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		http:        &http.Client{Timeout: 10 * time.Second},
		facetsTTL:   60 * time.Second,
	}
}

// WithHTTPClient swaps the transport (tests, or a tuned pool).
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	if h != nil {
		c.http = h
	}
	return c
}

// Configured reports whether a base URL was supplied.
func (c *Client) Configured() bool { return c != nil && c.baseURL != "" }

// ProductSearchDoc fetches one product's document by id.
//
// Returns ErrProductGone on 404. Every other non-200 is an error, so the
// consumer retries rather than acting on a guess.
func (c *Client) ProductSearchDoc(ctx context.Context, productID string) (*SearchDoc, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	endpoint := fmt.Sprintf("%s/v1/commerce/internal/products/%s/search-doc",
		c.baseURL, url.PathEscape(productID))
	body, status, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrProductGone
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("commerceclient: search-doc for %s returned %d: %s",
			productID, status, truncate(body))
	}
	var parsed struct {
		Data SearchDoc `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("commerceclient: decode search-doc for %s: %w", productID, err)
	}
	if parsed.Data.ProductID == "" {
		return nil, fmt.Errorf("commerceclient: search-doc for %s carried no product_id", productID)
	}
	return &parsed.Data, nil
}

// ListProductSearchDocs walks the live catalogue for a reindex.
func (c *Client) ListProductSearchDocs(ctx context.Context, cursor string, limit int) (*Page, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	endpoint := fmt.Sprintf("%s/v1/commerce/internal/products/search-docs?limit=%d",
		c.baseURL, limit)
	if cursor != "" {
		endpoint += "&cursor=" + url.QueryEscape(cursor)
	}
	body, status, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("commerceclient: search-docs returned %d: %s", status, truncate(body))
	}
	var parsed struct {
		Data struct {
			Items        []SearchDoc `json:"items"`
			NextCursor   string      `json:"next_cursor"`
			VisibleTotal int         `json:"visible_total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("commerceclient: decode search-docs page: %w", err)
	}
	return &Page{
		Items:        parsed.Data.Items,
		NextCursor:   parsed.Data.NextCursor,
		VisibleTotal: parsed.Data.VisibleTotal,
	}, nil
}

// FacetDefinitions returns the filterable attribute definitions, cached for
// facetsTTL.
//
// A stale-cache failure is NOT swallowed: a facet rail built from an empty
// definition list looks like "this catalogue has no filters", which is
// indistinguishable from the truth and therefore worse than an error.
func (c *Client) FacetDefinitions(ctx context.Context) ([]FacetDefinition, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	c.facetsMu.Lock()
	defer c.facetsMu.Unlock()
	if c.facetsOnce && time.Since(c.facetsAt) < c.facetsTTL {
		return c.facets, c.facetsErr
	}
	defs, err := c.fetchFacetDefinitions(ctx)
	c.facets, c.facetsErr, c.facetsAt, c.facetsOnce = defs, err, time.Now(), true
	return defs, err
}

func (c *Client) fetchFacetDefinitions(ctx context.Context) ([]FacetDefinition, error) {
	body, status, err := c.get(ctx, c.baseURL+"/v1/commerce/internal/search-facets")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("commerceclient: search-facets returned %d: %s", status, truncate(body))
	}
	var parsed struct {
		Data struct {
			Items []FacetDefinition `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("commerceclient: decode search-facets: %w", err)
	}
	return parsed.Data.Items, nil
}

func (c *Client) get(ctx context.Context, endpoint string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("commerceclient: %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("commerceclient: read %s: %w", endpoint, err)
	}
	return body, resp.StatusCode, nil
}

func truncate(b []byte) string {
	const max = 300
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…(" + strconv.Itoa(len(b)) + " bytes)"
}
