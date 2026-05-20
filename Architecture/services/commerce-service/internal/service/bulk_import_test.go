package service

import "testing"

func TestParseBulkImportCSV_ValidRow(t *testing.T) {
	csv := []byte("sku,title,mrp,selling_price,stock_qty\nSKU1,Hat,200,180,5\n")
	rows, errs, err := parseBulkImportCSV(csv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %+v", errs)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.SKU != "SKU1" || r.Title != "Hat" || r.MRP != 200 || r.SellingPrice != 180 || r.StockQty != 5 {
		t.Errorf("row parsed wrong: %+v", r)
	}
}

func TestParseBulkImportCSV_MissingRequiredColumn(t *testing.T) {
	csv := []byte("sku,title,mrp\nSKU1,Hat,200\n") // selling_price missing
	_, _, err := parseBulkImportCSV(csv)
	if err == nil {
		t.Error("expected error for missing required column")
	}
}

func TestParseBulkImportCSV_InvalidNumeric(t *testing.T) {
	csv := []byte("sku,title,mrp,selling_price,stock_qty\n" +
		"SKU1,Hat,not-a-number,180,5\n")
	rows, errs, err := parseBulkImportCSV(csv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("invalid row should not be included; got %d", len(rows))
	}
	if len(errs) == 0 {
		t.Error("expected per-row error")
	}
}

func TestParseBulkImportCSV_TierColumns(t *testing.T) {
	csv := []byte("sku,title,mrp,selling_price,stock_qty,tier_min_qty_1,tier_price_1\n" +
		"SKU1,Hat,200,180,5,10,160\n")
	rows, _, _ := parseBulkImportCSV(csv)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row")
	}
	if len(rows[0].Tiers) != 1 || rows[0].Tiers[0].MinQty != 10 || rows[0].Tiers[0].Price != 160 {
		t.Errorf("tier not parsed: %+v", rows[0].Tiers)
	}
}

func TestParseBulkImportCSV_PartialTierPairRejected(t *testing.T) {
	// price filled but min missing.
	csv := []byte("sku,title,mrp,selling_price,stock_qty,tier_min_qty_1,tier_price_1\n" +
		"SKU1,Hat,200,180,5,,160\n")
	_, errs, _ := parseBulkImportCSV(csv)
	if len(errs) == 0 {
		t.Error("expected error for partial tier")
	}
}
