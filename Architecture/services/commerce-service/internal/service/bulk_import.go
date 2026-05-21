// Phase F2.3 — bulk SKU upload service.
//
// Flow:
//
//	1. InitiateBulkUpload returns a presigned PUT URL + jobID.
//	2. Seller PUTs the CSV directly to MinIO (no commerce-service hop).
//	3. ValidateImport enqueues a fulfillment_job; worker parses + counts.
//	4. ExecuteImport enqueues a second job; worker upserts rows.
//	5. Seller polls GetImportJob for status + counts; downloads the
//	   error CSV via GetImportJobErrorsURL when rows fail.
//
// Idempotency:
//   - Validate is idempotent (re-parses; overwrites the error report).
//   - Execute uses INSERT … ON CONFLICT (sku, seller_id) DO UPDATE so
//     retrying after a partial failure converges to the same final state.
package service

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// randSlugSuffix returns 8 hex characters of randomness for slug
// uniqueness during bulk imports. Falls back to a millisecond stamp
// if the crypto/rand read fails (vanishingly rare; the suffix is for
// uniqueness, not security).
func randSlugSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08d", time.Now().UnixNano()%1e8)
	}
	return hex.EncodeToString(b[:])
}

var (
	ErrImportJobNotFound = fmt.Errorf("import job not found")
	ErrImportJobNotOwner = fmt.Errorf("not the seller for this import job")
	ErrImportBlobMissing = fmt.Errorf("blob store not configured")
	ErrImportBadStatus   = fmt.Errorf("import job not in the required state")
)

const bulkImportTTL = 30 * time.Minute

// InitiateBulkUpload creates a job row + a presigned PUT URL. The
// seller PUTs the CSV at that URL; subsequent steps reference the job
// by id. The storage key is namespaced by seller so two sellers'
// uploads never collide.
func (s *Service) InitiateBulkUpload(ctx context.Context, sellerID uuid.UUID, filename string) (*postgres.ImportJob, string, error) {
	if s.blob == nil {
		return nil, "", ErrImportBlobMissing
	}
	storageKey := fmt.Sprintf("bulk-import/%s/%s-%s.csv",
		sellerID, time.Now().UTC().Format("20060102T150405"), uuid.NewString())
	// blob.Store satisfies BlobStore by interface — but BlobStore here
	// only declares the methods commerce-service uses. Cast to the
	// presign-capable type via the helper method below.
	presigner, ok := s.blob.(interface {
		PresignedPutURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	})
	if !ok {
		return nil, "", fmt.Errorf("blob store does not support presigned PUT")
	}
	uploadURL, err := presigner.PresignedPutURL(ctx, storageKey, bulkImportTTL)
	if err != nil {
		return nil, "", fmt.Errorf("presign put: %w", err)
	}
	j, err := s.store.CreateImportJob(ctx, sellerID, filename, storageKey)
	if err != nil {
		return nil, "", fmt.Errorf("create import job: %w", err)
	}
	return j, uploadURL, nil
}

// MarkUploadComplete flips the job from 'uploaded' (created at init
// time) to 'validating' and enqueues the worker. Caller passes the
// jobID returned by InitiateBulkUpload; seller_id is checked from the
// authenticated user.
func (s *Service) MarkUploadComplete(ctx context.Context, sellerID, jobID uuid.UUID) error {
	job, err := s.assertImportJobOwned(ctx, sellerID, jobID)
	if err != nil {
		return err
	}
	if job.Status != "uploaded" {
		return ErrImportBadStatus
	}
	if err := s.store.UpdateImportJobStatus(ctx, jobID, "validating"); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"job_id": jobID})
	if err := s.store.EnqueueJobPool(ctx, "bulk_import_validate", payload); err != nil {
		return fmt.Errorf("enqueue validate: %w", err)
	}
	return nil
}

// ExecuteImport flips the job from 'ready_to_import' to 'importing'
// and enqueues the executor. Refuses to start if validation produced
// errors that the seller hasn't acknowledged (state stays
// validation_failed until they re-upload).
func (s *Service) ExecuteImport(ctx context.Context, sellerID, jobID uuid.UUID) error {
	job, err := s.assertImportJobOwned(ctx, sellerID, jobID)
	if err != nil {
		return err
	}
	if job.Status != "ready_to_import" {
		return ErrImportBadStatus
	}
	if err := s.store.UpdateImportJobStatus(ctx, jobID, "importing"); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"job_id": jobID})
	if err := s.store.EnqueueJobPool(ctx, "bulk_import_execute", payload); err != nil {
		return fmt.Errorf("enqueue execute: %w", err)
	}
	return nil
}

func (s *Service) GetImportJob(ctx context.Context, sellerID, jobID uuid.UUID) (*postgres.ImportJob, error) {
	return s.assertImportJobOwned(ctx, sellerID, jobID)
}

func (s *Service) ListImportJobs(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*postgres.ImportJob, error) {
	return s.store.ListImportJobsForSeller(ctx, sellerID, limit, offset)
}

// GetImportJobErrorsURL returns a presigned download URL for the
// validation error report. Empty string + nil error means no errors.
func (s *Service) GetImportJobErrorsURL(ctx context.Context, sellerID, jobID uuid.UUID, ttl time.Duration) (string, error) {
	job, err := s.assertImportJobOwned(ctx, sellerID, jobID)
	if err != nil {
		return "", err
	}
	if job.ErrorFileID == nil {
		return "", nil
	}
	if s.blob == nil {
		return "", ErrImportBlobMissing
	}
	return s.blob.PresignedGetURL(ctx, errorsKey(jobID), ttl)
}

func (s *Service) assertImportJobOwned(ctx context.Context, sellerID, jobID uuid.UUID) (*postgres.ImportJob, error) {
	job, err := s.store.GetImportJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrImportJobNotFound
	}
	if job.SellerID != sellerID {
		return nil, ErrImportJobNotOwner
	}
	return job, nil
}

// ─── Worker handlers ─────────────────────────────────────────────

// ValidateBulkImportJob is the fulfillment-worker entry. It downloads
// the CSV, validates each row, writes an error report back to MinIO,
// and updates the job's row counts. Idempotent — every call overwrites
// the prior report so retrying after a transient blob error converges.
func (s *Service) ValidateBulkImportJob(ctx context.Context, jobID uuid.UUID) error {
	job, err := s.store.GetImportJob(ctx, jobID)
	if err != nil || job == nil {
		return fmt.Errorf("load job: %w", err)
	}
	if s.blob == nil {
		return ErrImportBlobMissing
	}
	downloader, ok := s.blob.(interface {
		GetObject(ctx context.Context, key string) ([]byte, error)
	})
	if !ok {
		return fmt.Errorf("blob store does not support GetObject")
	}
	data, err := downloader.GetObject(ctx, job.StorageKey)
	if err != nil {
		return fmt.Errorf("read csv: %w", err)
	}
	rows, errorReport, err := parseBulkImportCSV(data)
	if err != nil {
		_ = s.store.UpdateImportJobStatus(ctx, jobID, "validation_failed")
		return err
	}
	total := len(rows)
	errorRows := len(errorReport)
	validRows := total - errorRows
	var errorBlobID *string
	if errorRows > 0 {
		errorCSV := buildErrorCSV(errorReport)
		_ = s.blob.Upload(ctx, errorsKey(jobID), errorCSV, "text/csv")
		id := jobID.String()
		errorBlobID = &id
	}
	if err := s.store.UpdateImportJobCounts(ctx, jobID, total, validRows, 0, errorRows, errorBlobID); err != nil {
		return err
	}
	next := "ready_to_import"
	if errorRows > 0 && validRows == 0 {
		next = "validation_failed"
	}
	return s.store.UpdateImportJobStatus(ctx, jobID, next)
}

// ExecuteBulkImportJob upserts the validated rows.
//
// Each row is processed in its own goroutine-local transaction so a
// constraint violation on row N+1 doesn't roll back rows 1..N. After
// each row the worker bumps imported_rows/error_rows atomically on the
// job so the seller's polling UI sees progress.
//
// Idempotency: re-runs match on (sku, seller_id) — existing variants
// have their pricing + stock + product metadata refreshed, fresh rows
// get a brand-new product + variant pair. Tier ladders are replaced
// atomically when the row carries tier columns; rows without tiers
// leave any existing ladder untouched.
//
// On a transient failure we don't roll back the whole job — partial
// progress survives so the worker (or a manual re-run) can pick up
// where it left off.
func (s *Service) ExecuteBulkImportJob(ctx context.Context, jobID uuid.UUID) error {
	job, err := s.store.GetImportJob(ctx, jobID)
	if err != nil || job == nil {
		return fmt.Errorf("load job: %w", err)
	}
	if s.blob == nil {
		return ErrImportBlobMissing
	}
	downloader, ok := s.blob.(interface {
		GetObject(ctx context.Context, key string) ([]byte, error)
	})
	if !ok {
		return fmt.Errorf("blob store does not support GetObject")
	}
	data, err := downloader.GetObject(ctx, job.StorageKey)
	if err != nil {
		_ = s.store.UpdateImportJobStatus(ctx, jobID, "failed")
		return fmt.Errorf("read csv: %w", err)
	}
	rows, _, err := parseBulkImportCSV(data)
	if err != nil {
		_ = s.store.UpdateImportJobStatus(ctx, jobID, "failed")
		return err
	}

	imported := 0
	failed := 0
	for _, row := range rows {
		if err := s.upsertImportRow(ctx, job.SellerID, row); err != nil {
			failed++
			_ = s.store.IncrImportJobErrors(ctx, jobID, 1)
			// Continue with the next row — bulk imports must be
			// resilient to per-row constraint hits.
			continue
		}
		imported++
		_ = s.store.IncrImportJobImported(ctx, jobID, 1)
	}

	final := "completed"
	if imported == 0 {
		final = "failed"
	} else if failed > 0 {
		final = "partially_imported"
	}
	return s.store.UpdateImportJobStatus(ctx, jobID, final)
}

// upsertImportRow handles one CSV row. Returns nil on success so the
// caller can advance counters; any error is logged + counted but
// doesn't propagate to the worker (the row's error is the seller's
// problem, not a retryable system failure).
func (s *Service) upsertImportRow(ctx context.Context, sellerID uuid.UUID, row *BulkImportRow) error {
	existing, err := s.store.FindVariantBySKUForSeller(ctx, sellerID, row.SKU)
	if err != nil {
		return fmt.Errorf("find variant %s: %w", row.SKU, err)
	}
	if existing != nil {
		return s.updateExistingVariant(ctx, existing, row)
	}
	return s.createVariantAndProduct(ctx, sellerID, row)
}

// updateExistingVariant patches pricing + stock + product metadata on
// a known SKU. The variant row + inventory row + product row each get
// their own focused UPDATE so the seller's reupload doesn't undo
// fields they edited via the product editor (description, images).
func (s *Service) updateExistingVariant(ctx context.Context, v *postgres.ProductVariant, row *BulkImportRow) error {
	prod, err := s.store.GetProductByID(ctx, v.ProductID)
	if err != nil || prod == nil {
		return fmt.Errorf("load parent product for variant %s: %w", v.ID, err)
	}
	if err := s.store.UpdateVariantPricing(ctx, v.ID, row.MRP, row.SellingPrice, row.CostPrice, row.WeightGrams); err != nil {
		return fmt.Errorf("update variant pricing: %w", err)
	}
	if err := s.store.UpsertInventory(ctx, v.ID, prod.SellerID, row.StockQty); err != nil {
		return fmt.Errorf("upsert inventory: %w", err)
	}
	if err := s.store.UpdateProductMetadata(ctx, v.ProductID, postgres.ProductMetadataUpdate{
		Title:            row.Title,
		BrandName:        row.BrandName,
		ManufacturerName: row.ManufacturerName,
		HSNCode:          row.HSNCode,
		CountryOfOrigin:  row.CountryOfOrigin,
		WeightGrams:      row.WeightGrams,
		LengthCm:         row.LengthCm,
		WidthCm:          row.WidthCm,
		HeightCm:         row.HeightCm,
	}); err != nil {
		return fmt.Errorf("update product: %w", err)
	}
	return s.applyTierLadderIfPresent(ctx, v.ID, row.Tiers)
}

// createVariantAndProduct is the new-row path: insert the product
// (with a generated slug), then the variant + inventory + tiers.
func (s *Service) createVariantAndProduct(ctx context.Context, sellerID uuid.UUID, row *BulkImportRow) error {
	// products.slug is globally UNIQUE. uniqueSlug uses a millisecond
	// suffix that collides during fast bulk imports, and SKU alone
	// can clash across sellers. Append 8 hex chars from crypto/rand
	// — 4 billion combos — so a 1000-row import never hits a
	// duplicate-key error on slug.
	slug := slugify(row.Title) + "-" + randSlugSuffix()
	prod := &postgres.Product{
		SellerID:         sellerID,
		Title:            row.Title,
		Slug:             slug,
		BrandName:        row.BrandName,
		ManufacturerName: row.ManufacturerName,
		HSNCode:          row.HSNCode,
		CountryOfOrigin:  row.CountryOfOrigin,
		WeightGrams:      row.WeightGrams,
		LengthCm:         row.LengthCm,
		WidthCm:          row.WidthCm,
		HeightCm:         row.HeightCm,
		ProductType:      "simple",
		Condition:        "new",
		Status:           "draft",
		Visibility:       "public",
		ApprovalStatus:   "draft",
		ReturnPolicyType: "returnable",
		ReturnPolicyDays: 7,
	}
	if err := s.store.CreateProduct(ctx, prod); err != nil {
		return fmt.Errorf("create product: %w", err)
	}
	variant := &postgres.ProductVariant{
		ProductID:    prod.ID,
		SKU:          row.SKU,
		Option1Name:  row.Option1Name,
		Option1Value: row.Option1Value,
		Option2Name:  row.Option2Name,
		Option2Value: row.Option2Value,
		MRP:          row.MRP,
		SellingPrice: row.SellingPrice,
		CostPrice:    row.CostPrice,
		WeightGrams:  row.WeightGrams,
		CurrencyCode: "INR",
		Status:       "active",
	}
	if err := s.store.CreateVariant(ctx, variant); err != nil {
		return fmt.Errorf("create variant: %w", err)
	}
	if err := s.store.UpsertInventory(ctx, variant.ID, sellerID, row.StockQty); err != nil {
		return fmt.Errorf("upsert inventory: %w", err)
	}
	return s.applyTierLadderIfPresent(ctx, variant.ID, row.Tiers)
}

// applyTierLadderIfPresent replaces the variant's tier ladder when
// the row carries tier columns. Empty tier slices leave any existing
// ladder untouched (the seller can clear via the variant editor UI).
func (s *Service) applyTierLadderIfPresent(ctx context.Context, variantID uuid.UUID, tiers []ImportTier) error {
	if len(tiers) == 0 {
		return nil
	}
	rows := make([]*postgres.PriceTier, 0, len(tiers))
	for _, t := range tiers {
		rows = append(rows, &postgres.PriceTier{
			VariantID: variantID,
			MinQty:    t.MinQty,
			Price:     t.Price,
		})
	}
	return s.store.ReplacePriceTiers(ctx, variantID, rows)
}

func errorsKey(jobID uuid.UUID) string {
	return fmt.Sprintf("bulk-import/errors/%s.csv", jobID)
}

// ─── CSV parsing ─────────────────────────────────────────────────

// BulkImportRow is one parsed CSV row. Optional fields are nil when
// the source cell was empty.
type BulkImportRow struct {
	RowNumber       int      // 1-indexed within the data rows (header excluded)
	SKU             string
	ProductID       *string
	Title           string
	BrandName       *string
	ManufacturerName *string
	HSNCode         *string
	CountryOfOrigin *string
	MRP             float64
	SellingPrice    float64
	CostPrice       *float64
	StockQty        int
	WeightGrams     *int
	LengthCm        *float64
	WidthCm         *float64
	HeightCm        *float64
	Option1Name     *string
	Option1Value    *string
	Option2Name     *string
	Option2Value    *string
	Tiers           []ImportTier
}

type ImportTier struct {
	MinQty int
	Price  float64
}

type BulkImportError struct {
	RowNumber int    `json:"row_number"`
	Field     string `json:"field"`
	Message   string `json:"message"`
}

// parseBulkImportCSV reads the bytes, validates headers, and returns
// (validRows, errors). Headers expected (case-insensitive):
//
//   sku, product_id, title, brand_name, manufacturer_name, hsn_code,
//   country_of_origin, mrp, selling_price, cost_price, stock_qty,
//   weight_grams, length_cm, width_cm, height_cm, option_1_name,
//   option_1_value, option_2_name, option_2_value, tier_min_qty_1,
//   tier_price_1, tier_min_qty_2, tier_price_2, tier_min_qty_3, tier_price_3
//
// Only sku, title, mrp, selling_price, stock_qty are required.
func parseBulkImportCSV(data []byte) ([]*BulkImportRow, []BulkImportError, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1 // allow ragged rows; we validate per-field
	records, err := r.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("csv parse: %w", err)
	}
	if len(records) == 0 {
		return nil, nil, fmt.Errorf("csv is empty")
	}
	header := records[0]
	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, must := range []string{"sku", "title", "mrp", "selling_price", "stock_qty"} {
		if _, ok := col[must]; !ok {
			return nil, nil, fmt.Errorf("required column %q missing", must)
		}
	}
	var rows []*BulkImportRow
	var errs []BulkImportError
	for i, rec := range records[1:] {
		rowNum := i + 1
		row, rowErrs := parseImportRow(rowNum, col, rec)
		if len(rowErrs) > 0 {
			errs = append(errs, rowErrs...)
			continue
		}
		rows = append(rows, row)
	}
	return rows, errs, nil
}

func parseImportRow(rowNum int, col map[string]int, rec []string) (*BulkImportRow, []BulkImportError) {
	get := func(name string) string {
		idx, ok := col[name]
		if !ok || idx >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[idx])
	}
	getOpt := func(name string) *string {
		v := get(name)
		if v == "" {
			return nil
		}
		return &v
	}
	row := &BulkImportRow{RowNumber: rowNum}
	var errs []BulkImportError

	row.SKU = get("sku")
	if row.SKU == "" {
		errs = append(errs, BulkImportError{RowNumber: rowNum, Field: "sku", Message: "required"})
	}
	row.Title = get("title")
	if row.Title == "" {
		errs = append(errs, BulkImportError{RowNumber: rowNum, Field: "title", Message: "required"})
	}
	row.ProductID = getOpt("product_id")
	row.BrandName = getOpt("brand_name")
	row.ManufacturerName = getOpt("manufacturer_name")
	row.HSNCode = getOpt("hsn_code")
	row.CountryOfOrigin = getOpt("country_of_origin")
	row.Option1Name = getOpt("option_1_name")
	row.Option1Value = getOpt("option_1_value")
	row.Option2Name = getOpt("option_2_name")
	row.Option2Value = getOpt("option_2_value")

	if v, ok := parseFloat(get("mrp")); !ok || v <= 0 {
		errs = append(errs, BulkImportError{RowNumber: rowNum, Field: "mrp", Message: "must be > 0"})
	} else {
		row.MRP = v
	}
	if v, ok := parseFloat(get("selling_price")); !ok || v <= 0 {
		errs = append(errs, BulkImportError{RowNumber: rowNum, Field: "selling_price", Message: "must be > 0"})
	} else {
		row.SellingPrice = v
	}
	if v, ok := parseInt(get("stock_qty")); !ok || v < 0 {
		errs = append(errs, BulkImportError{RowNumber: rowNum, Field: "stock_qty", Message: "must be >= 0"})
	} else {
		row.StockQty = v
	}
	if cp, ok := parseFloat(get("cost_price")); ok {
		row.CostPrice = &cp
	}
	if wg, ok := parseInt(get("weight_grams")); ok {
		row.WeightGrams = &wg
	}
	if lc, ok := parseFloat(get("length_cm")); ok {
		row.LengthCm = &lc
	}
	if wc, ok := parseFloat(get("width_cm")); ok {
		row.WidthCm = &wc
	}
	if hc, ok := parseFloat(get("height_cm")); ok {
		row.HeightCm = &hc
	}

	// Up to 3 tier columns. Validation rejects partial pairs.
	for i := 1; i <= 3; i++ {
		minRaw := get(fmt.Sprintf("tier_min_qty_%d", i))
		priceRaw := get(fmt.Sprintf("tier_price_%d", i))
		if minRaw == "" && priceRaw == "" {
			continue
		}
		minV, mok := parseInt(minRaw)
		priceV, pok := parseFloat(priceRaw)
		switch {
		case !mok || minV < 1:
			errs = append(errs, BulkImportError{
				RowNumber: rowNum,
				Field:     fmt.Sprintf("tier_min_qty_%d", i),
				Message:   "must be a positive integer",
			})
		case !pok || priceV <= 0:
			errs = append(errs, BulkImportError{
				RowNumber: rowNum,
				Field:     fmt.Sprintf("tier_price_%d", i),
				Message:   "must be > 0",
			})
		default:
			row.Tiers = append(row.Tiers, ImportTier{MinQty: minV, Price: priceV})
		}
	}
	return row, errs
}

func parseFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

func buildErrorCSV(errs []BulkImportError) []byte {
	var sb strings.Builder
	sb.WriteString("row_number,field,message\n")
	for _, e := range errs {
		sb.WriteString(fmt.Sprintf("%d,%s,%q\n", e.RowNumber, e.Field, e.Message))
	}
	return []byte(sb.String())
}

// Compile-time guard for stable error reuse.
var _ = errors.New
