// Package productindex turns a commerce search document into the
// OpenSearch document products_v2 holds.
//
// It is its own package for one reason: the Kafka consumer and the reindex
// both need this conversion, and if either owned it the other would grow a
// second copy. A reindex whose documents differ in any field from the ones
// the consumer writes is a reindex that CHANGES the index — so a run
// intended to repair drift would introduce it, and the difference would
// only show up as results that quietly move when a product is touched.
//
// One function, no configuration, no I/O. Everything it decides is
// derivable from the source document, which is what makes it testable
// without an OpenSearch or a commerce-service.
package productindex

import (
	"strings"

	"github.com/atpost/search-service/internal/commerceclient"
	"github.com/atpost/search-service/internal/store/search"
)

// Doc converts one commerce search document into the indexed document.
//
// The ancestor chain is fanned into three parallel keyword lists, and that
// fan-out is the mechanism behind "a Books filter matches a Textbooks
// listing": commerce sends the chain root-first with the leaf last, and
// every rung's id, name and slug goes into the document, so a term query
// for the department matches a product filed under a leaf three levels
// down. Without it a category filter is exact-match on a leaf, which is a
// filter no shopper can use.
func Doc(src commerceclient.SearchDoc) search.ProductV2Doc {
	doc := search.ProductV2Doc{
		ProductID:  src.ProductID,
		SellerID:   src.SellerID,
		SellerName: src.SellerName,

		Title:            src.Title,
		Description:      src.Description,
		ShortDescription: src.ShortDescription,
		BrandName:        src.BrandName,
		SearchKeywords:   src.SearchKeywords,
		Condition:        src.Condition,
		ProductType:      src.ProductType,
		Slug:             src.Slug,

		CategoryID:   src.CategoryID,
		CategoryName: src.CategoryName,
		// The legacy single-value field /v1/search/products has always
		// filtered on. Written from the same source in the same call as
		// category_names, so the two cannot disagree.
		Category: src.CategoryName,

		MinPriceMinor: src.MinPriceMinor,
		MaxPriceMinor: src.MaxPriceMinor,
		MRPMinor:      src.MRPMinor,
		Currency:      src.Currency,
		// Rupees, for the legacy `price` field the existing product search
		// response carries. Derived here and never sourced separately: the
		// paise are the money and this is a rendering of them.
		Price: float64(src.MinPriceMinor) / 100.0,

		TotalStock: src.TotalStock,
		InStock:    src.InStock,

		ImageMediaID: src.ImageMediaID,
		ImageURL:     src.ImageURL,

		AvgRating:   src.AvgRating,
		ReviewCount: src.ReviewCount,
		OrderCount:  src.OrderCount,
		ViewCount:   src.ViewCount,

		Status:         src.Status,
		ApprovalStatus: src.ApprovalStatus,

		Attributes: search.FlattenAttributes(src.Attributes),

		PublishedAt: src.PublishedAt,
		CreatedAt:   src.CreatedAt,
		UpdatedAt:   src.UpdatedAt,
	}

	names := make([]string, 0, len(src.CategoryPath))
	for _, rung := range src.CategoryPath {
		if rung.ID != "" {
			doc.CategoryIDs = append(doc.CategoryIDs, rung.ID)
		}
		if rung.Name != "" {
			doc.CategoryNames = append(doc.CategoryNames, rung.Name)
			names = append(names, rung.Name)
		}
		if rung.Slug != "" {
			doc.CategorySlugs = append(doc.CategorySlugs, rung.Slug)
		}
	}
	// The breadcrumb, for display and for full-text matching on a
	// department name a buyer typed into the query box rather than clicked.
	doc.CategoryPath = strings.Join(names, " > ")

	// A product with a category that resolved to no chain still answers for
	// its own leaf: better a filter that matches only the exact category
	// than one that matches nothing at all.
	if len(doc.CategoryIDs) == 0 && src.CategoryID != "" {
		doc.CategoryIDs = []string{src.CategoryID}
	}
	if len(doc.CategoryNames) == 0 && src.CategoryName != "" {
		doc.CategoryNames = []string{src.CategoryName}
	}
	return doc
}
