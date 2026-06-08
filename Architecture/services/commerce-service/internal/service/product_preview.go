package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ProductPreview is the compact projection of a product for the
// in-video tag composer. Pulls (id, title, slug, primary image,
// lowest variant price, status, visibility) without the full
// product/variants response shape that GetProduct returns.
//
// Visibility is exposed so the composer can grey-out unpublished
// products. Status carries the moderation state (approved /
// pending_review / rejected); composer should only allow tagging
// approved + live products, but the gate is enforced server-side
// in the post-service product-tag validator path.
type ProductPreview struct {
	ID                  uuid.UUID
	Title               string
	Slug                string
	PrimaryImageMediaID *uuid.UUID
	Price               float64
	Currency            string
	Status              string
	Visibility          string
}

// GetProductPreview returns nil + nil when the product doesn't exist
// (handler maps to 404). Returns an error only on transport failures.
//
// Pricing rule
//
//	Use the lowest selling_price across all variants. A product with no
//	variants returns Price=0; the composer should show "Price on
//	request" in that case rather than $0.00.
func (s *Service) GetProductPreview(
	ctx context.Context,
	productID uuid.UUID,
) (*ProductPreview, error) {
	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("preview: load product: %w", err)
	}
	if product == nil {
		return nil, nil
	}

	preview := &ProductPreview{
		ID:                  product.ID,
		Title:               product.Title,
		Slug:                product.Slug,
		PrimaryImageMediaID: product.PrimaryImageMediaID,
		Status:              product.Status,
		Visibility:          product.Visibility,
	}

	variants, err := s.store.GetVariantsByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("preview: load variants: %w", err)
	}
	if len(variants) > 0 {
		minPrice := variants[0].SellingPrice
		currency := variants[0].CurrencyCode
		for _, v := range variants[1:] {
			if v.SellingPrice < minPrice {
				minPrice = v.SellingPrice
				if v.CurrencyCode != "" {
					currency = v.CurrencyCode
				}
			}
		}
		preview.Price = minPrice
		preview.Currency = currency
	}

	return preview, nil
}
