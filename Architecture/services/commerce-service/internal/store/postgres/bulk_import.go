// Phase F2.3 — bulk SKU import job data access.
// Reuses the existing product_import_jobs table (status, totals, etc.)
// added in earlier commerce work.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ImportJob struct {
	ID            uuid.UUID  `db:"id" json:"id"`
	SellerID      uuid.UUID  `db:"seller_id" json:"seller_id"`
	Filename      string     `db:"filename" json:"filename"`
	FileMediaID   *uuid.UUID `db:"file_media_id" json:"file_media_id,omitempty"`
	Status        string     `db:"status" json:"status"`
	TotalRows     int        `db:"total_rows" json:"total_rows"`
	ValidRows     int        `db:"valid_rows" json:"valid_rows"`
	ImportedRows  int        `db:"imported_rows" json:"imported_rows"`
	ErrorRows     int        `db:"error_rows" json:"error_rows"`
	ErrorFileID   *uuid.UUID `db:"error_file_id" json:"error_file_id,omitempty"`
	DryRun        bool       `db:"dry_run" json:"dry_run"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
	CompletedAt   *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	// Storage key for the seller's uploaded CSV in MinIO.
	StorageKey    string     `json:"storage_key,omitempty"`
}

// CreateImportJob inserts a freshly-uploaded job. storageKey is the
// MinIO object key the seller PUT to; we stash it as the filename so
// the worker can fetch it later (file_media_id isn't used for CSV-only).
func (s *Store) CreateImportJob(ctx context.Context, sellerID uuid.UUID, filename, storageKey string) (*ImportJob, error) {
	j := &ImportJob{
		SellerID:   sellerID,
		Filename:   filename,
		Status:     "uploaded",
		StorageKey: storageKey,
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO product_import_jobs (seller_id, filename, status)
		VALUES ($1, $2, 'uploaded')
		RETURNING id, created_at`,
		sellerID, storageKey, // store key in the filename column so worker can fetch
	).Scan(&j.ID, &j.CreatedAt)
	return j, err
}

func (s *Store) GetImportJob(ctx context.Context, id uuid.UUID) (*ImportJob, error) {
	j := &ImportJob{}
	err := s.db.QueryRow(ctx, `
		SELECT id, seller_id, filename, file_media_id, status,
		       total_rows, valid_rows, imported_rows, error_rows,
		       error_file_id, dry_run, created_at, completed_at
		FROM product_import_jobs WHERE id = $1`, id).Scan(
		&j.ID, &j.SellerID, &j.Filename, &j.FileMediaID, &j.Status,
		&j.TotalRows, &j.ValidRows, &j.ImportedRows, &j.ErrorRows,
		&j.ErrorFileID, &j.DryRun, &j.CreatedAt, &j.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	// Filename column doubles as storage key for now.
	j.StorageKey = j.Filename
	return j, nil
}

// UpdateImportJobStatus is the small state-machine setter. Valid
// transitions are enforced in the service layer.
func (s *Store) UpdateImportJobStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE product_import_jobs SET status = $2,
		  completed_at = CASE WHEN $2 IN ('completed','failed','partially_imported') THEN NOW() ELSE completed_at END
		 WHERE id = $1`, id, status)
	return err
}

// UpdateImportJobCounts updates validation / import row totals. Use
// during the worker's validate and execute phases.
func (s *Store) UpdateImportJobCounts(ctx context.Context, id uuid.UUID, total, valid, imported, errorRows int, errorFileID *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE product_import_jobs SET
			total_rows = $2,
			valid_rows = $3,
			imported_rows = $4,
			error_rows = $5,
			error_file_id = NULLIF($6, '')::uuid
		WHERE id = $1`, id, total, valid, imported, errorRows, derefOrEmpty(errorFileID))
	return err
}

func (s *Store) ListImportJobsForSeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*ImportJob, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, seller_id, filename, file_media_id, status,
		       total_rows, valid_rows, imported_rows, error_rows,
		       error_file_id, dry_run, created_at, completed_at
		FROM product_import_jobs WHERE seller_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ImportJob
	for rows.Next() {
		j := &ImportJob{}
		if err := rows.Scan(&j.ID, &j.SellerID, &j.Filename, &j.FileMediaID, &j.Status,
			&j.TotalRows, &j.ValidRows, &j.ImportedRows, &j.ErrorRows,
			&j.ErrorFileID, &j.DryRun, &j.CreatedAt, &j.CompletedAt); err != nil {
			return nil, err
		}
		j.StorageKey = j.Filename
		out = append(out, j)
	}
	return out, rows.Err()
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ─── Helpers used by the execute worker ──────────────────────────

// FindVariantBySKUForSeller returns the variant whose seller owns it.
// The unique constraint on (sku, seller_id) isn't enforced by the
// schema directly — we approximate by joining to products and matching
// the seller_id, then returning the first hit. Used by the bulk-import
// executor to decide insert-vs-update.
func (s *Store) FindVariantBySKUForSeller(ctx context.Context, sellerID uuid.UUID, sku string) (*ProductVariant, error) {
	v := &ProductVariant{}
	err := s.db.QueryRow(ctx, `
		SELECT v.id, v.product_id, v.sku, v.barcode, v.option_1_name, v.option_1_value,
		       v.option_2_name, v.option_2_value, v.option_3_name, v.option_3_value,
		       v.mrp, v.selling_price, v.cost_price, v.currency_code, v.status,
		       v.image_media_id, v.weight_grams, v.created_at, v.updated_at
		FROM product_variants v
		JOIN products p ON p.id = v.product_id
		WHERE p.seller_id = $1 AND v.sku = $2
		LIMIT 1`, sellerID, sku).Scan(
		&v.ID, &v.ProductID, &v.SKU, &v.Barcode, &v.Option1Name, &v.Option1Value,
		&v.Option2Name, &v.Option2Value, &v.Option3Name, &v.Option3Value,
		&v.MRP, &v.SellingPrice, &v.CostPrice, &v.CurrencyCode, &v.Status,
		&v.ImageMediaID, &v.WeightGrams, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return v, nil
}

// UpdateVariantPricing updates the price + stock fields a bulk import
// row touches. Leaves option/barcode/image untouched (those are set
// only at create time so a reupload doesn't lose them).
func (s *Store) UpdateVariantPricing(ctx context.Context, variantID uuid.UUID, mrp, sellingPrice float64, costPrice *float64, weightGrams *int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE product_variants
		SET mrp = $2,
		    selling_price = $3,
		    cost_price = COALESCE($4, cost_price),
		    weight_grams = COALESCE($5, weight_grams),
		    updated_at = NOW()
		WHERE id = $1`,
		variantID, mrp, sellingPrice, costPrice, weightGrams)
	return err
}

// UpdateProductMetadata updates the bulk-import-touchable product
// fields. Existing fields the import doesn't supply (description,
// images, etc.) are preserved.
func (s *Store) UpdateProductMetadata(ctx context.Context, productID uuid.UUID, in ProductMetadataUpdate) error {
	_, err := s.db.Exec(ctx, `
		UPDATE products SET
			title = COALESCE(NULLIF($2,''), title),
			brand_name = COALESCE($3, brand_name),
			manufacturer_name = COALESCE($4, manufacturer_name),
			hsn_code = COALESCE($5, hsn_code),
			country_of_origin = COALESCE($6, country_of_origin),
			weight_grams = COALESCE($7, weight_grams),
			length_cm = COALESCE($8, length_cm),
			width_cm = COALESCE($9, width_cm),
			height_cm = COALESCE($10, height_cm),
			updated_at = NOW()
		WHERE id = $1`,
		productID, in.Title, in.BrandName, in.ManufacturerName, in.HSNCode,
		in.CountryOfOrigin, in.WeightGrams, in.LengthCm, in.WidthCm, in.HeightCm)
	return err
}

// ProductMetadataUpdate is the bulk-import-shaped product patch.
// Pointers everywhere so the COALESCE-no-op pattern works cleanly.
type ProductMetadataUpdate struct {
	Title            string
	BrandName        *string
	ManufacturerName *string
	HSNCode          *string
	CountryOfOrigin  *string
	WeightGrams      *int
	LengthCm         *float64
	WidthCm          *float64
	HeightCm         *float64
}

// IncrImportJobImported atomically increments the imported_rows count.
// Used by the execute worker between row-scoped transactions so the
// seller's poll sees progress mid-import.
func (s *Store) IncrImportJobImported(ctx context.Context, jobID uuid.UUID, delta int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE product_import_jobs SET imported_rows = imported_rows + $2 WHERE id = $1`,
		jobID, delta)
	return err
}

// IncrImportJobErrors atomically increments error_rows so partial
// failures during execute show up immediately.
func (s *Store) IncrImportJobErrors(ctx context.Context, jobID uuid.UUID, delta int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE product_import_jobs SET error_rows = error_rows + $2 WHERE id = $1`,
		jobID, delta)
	return err
}
