// Package invoice generates tax invoices for commerce orders.
//
// Primary output is self-contained HTML (printable via browser Cmd+P → Save as PDF).
// For true PDF output, wire a Renderer backed by wkhtmltopdf or a cloud service
// (we keep the surface pluggable via the Renderer interface).
package invoice

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// Invoice is the canonical invoice data model (one per order, one seller).
type Invoice struct {
	Number        string    // "PBK/2026-27/000123"
	Date          time.Time
	OrderNumber   string
	OrderDate     time.Time
	Seller        Party
	Buyer         Party
	ShipTo        Address
	Items         []LineItem
	Subtotal      float64
	ShippingCharges float64
	IsInterstate  bool // IGST vs CGST+SGST
	TotalCGST     float64
	TotalSGST     float64
	TotalIGST     float64
	CouponCode    string
	CouponDiscount float64
	GrandTotal    float64
	Currency      string // "INR"
	Notes         string
}

type Party struct {
	Name      string
	GSTIN     string
	PAN       string
	Address   Address
	Email     string
	Phone     string
}

type Address struct {
	Line1    string
	Line2    string
	City     string
	State    string
	Postal   string
	Country  string
}

type LineItem struct {
	Title        string
	HSN          string
	SKU          string
	Quantity     int
	UnitPrice    float64
	Discount     float64
	Taxable      float64
	CGSTPct      float64
	SGSTPct      float64
	IGSTPct      float64
	CGSTAmount   float64
	SGSTAmount   float64
	IGSTAmount   float64
	LineTotal    float64
}

// ── Numbering ──────────────────────────────────────────────────────────────

// FinancialYear returns Indian FY string for a given date, e.g. "2026-27" for dates in Apr'26–Mar'27.
func FinancialYear(t time.Time) string {
	y := t.Year()
	if t.Month() < time.April {
		return fmt.Sprintf("%d-%02d", y-1, y%100)
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}

// NumberFor returns a formatted invoice number "PBK/<FY>/<seq6>".
func NumberFor(t time.Time, sequence int64) string {
	return fmt.Sprintf("PBK/%s/%06d", FinancialYear(t), sequence)
}

// ── Computation ────────────────────────────────────────────────────────────

// ApplyGST fills CGST/SGST/IGST amounts on each line based on state match.
// Call after LineItem.Taxable is populated.
func (inv *Invoice) ApplyGST() {
	inv.IsInterstate = inv.Seller.Address.State != inv.ShipTo.State
	inv.TotalCGST, inv.TotalSGST, inv.TotalIGST = 0, 0, 0
	for i := range inv.Items {
		it := &inv.Items[i]
		if inv.IsInterstate {
			it.CGSTAmount, it.SGSTAmount = 0, 0
			it.IGSTAmount = round2(it.Taxable * it.IGSTPct / 100)
			it.LineTotal = round2(it.Taxable + it.IGSTAmount)
			inv.TotalIGST += it.IGSTAmount
		} else {
			it.IGSTAmount = 0
			it.CGSTAmount = round2(it.Taxable * it.CGSTPct / 100)
			it.SGSTAmount = round2(it.Taxable * it.SGSTPct / 100)
			it.LineTotal = round2(it.Taxable + it.CGSTAmount + it.SGSTAmount)
			inv.TotalCGST += it.CGSTAmount
			inv.TotalSGST += it.SGSTAmount
		}
	}
	inv.TotalCGST = round2(inv.TotalCGST)
	inv.TotalSGST = round2(inv.TotalSGST)
	inv.TotalIGST = round2(inv.TotalIGST)
}

// ComputeTotals fills Subtotal and GrandTotal from line items.
func (inv *Invoice) ComputeTotals() {
	inv.Subtotal = 0
	for _, it := range inv.Items {
		inv.Subtotal += it.Taxable
	}
	inv.Subtotal = round2(inv.Subtotal)
	inv.GrandTotal = round2(inv.Subtotal + inv.ShippingCharges + inv.TotalCGST + inv.TotalSGST + inv.TotalIGST - inv.CouponDiscount)
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// ── Rendering ──────────────────────────────────────────────────────────────

// Renderer produces bytes (HTML or PDF) from an Invoice.
type Renderer interface {
	Render(inv Invoice) ([]byte, string, error) // returns (body, contentType, err)
}

// HTMLRenderer renders a printable HTML invoice.
type HTMLRenderer struct{}

func (HTMLRenderer) Render(inv Invoice) ([]byte, string, error) {
	t, err := template.New("invoice").Funcs(template.FuncMap{
		"money": func(v float64) string { return fmt.Sprintf("%.2f", v) },
		"add1":  func(i int) int { return i + 1 },
	}).Parse(htmlInvoiceTemplate)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, inv); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "text/html; charset=utf-8", nil
}

// AsFilename returns a safe filename stem (no extension).
func (inv Invoice) AsFilename() string {
	s := strings.ReplaceAll(inv.Number, "/", "_")
	return "invoice_" + s
}

const htmlInvoiceTemplate = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Invoice {{.Number}}</title>
<style>
  @page { size: A4; margin: 18mm; }
  body { font-family: -apple-system,Segoe UI,Roboto,sans-serif; color:#111; font-size:12px; margin:0; padding:20px; }
  .hdr { display:flex; justify-content:space-between; align-items:flex-start; border-bottom:2px solid #6366f1; padding-bottom:14px; margin-bottom:18px; }
  .brand { font-size:22px; font-weight:900; color:#6366f1; letter-spacing:-0.5px }
  .meta { text-align:right; font-size:11px; color:#555 }
  .meta b { color:#111; font-size:13px; display:block }
  h2 { font-size:13px; margin:14px 0 6px; color:#6366f1; text-transform:uppercase; letter-spacing:1px }
  .parties { display:grid; grid-template-columns:1fr 1fr; gap:20px; margin-bottom:18px }
  .box { background:#fafafa; padding:12px; border-radius:6px; line-height:1.5; font-size:11px }
  .box b { font-size:12px; display:block; margin-bottom:4px; color:#111 }
  table { width:100%; border-collapse:collapse; font-size:11px }
  th { background:#6366f1; color:#fff; padding:8px 6px; text-align:left; font-weight:600 }
  td { padding:8px 6px; border-bottom:1px solid #eee; vertical-align:top }
  .num { text-align:right; font-variant-numeric:tabular-nums }
  .totals { margin-top:18px; float:right; width:50%; font-size:12px }
  .totals tr td { border:none; padding:5px 8px }
  .totals tr.grand td { border-top:2px solid #111; font-weight:700; font-size:14px; padding-top:10px }
  .footer { clear:both; margin-top:40px; padding-top:20px; border-top:1px solid #ddd; font-size:10px; color:#888; text-align:center }
  .terms { margin-top:30px; font-size:10px; color:#666; line-height:1.6 }
</style></head>
<body>
<div class="hdr">
  <div>
    <div class="brand">Postbook Commerce</div>
    <div style="color:#888;font-size:10px;margin-top:2px">Tax Invoice / Bill of Supply</div>
  </div>
  <div class="meta">
    <b>Invoice #{{.Number}}</b>
    Invoice Date: {{.Date.Format "02 Jan 2006"}}<br/>
    Order #{{.OrderNumber}}<br/>
    Order Date: {{.OrderDate.Format "02 Jan 2006"}}
  </div>
</div>

<div class="parties">
  <div class="box">
    <b>Seller</b>
    {{.Seller.Name}}<br/>
    {{.Seller.Address.Line1}}{{if .Seller.Address.Line2}}, {{.Seller.Address.Line2}}{{end}}<br/>
    {{.Seller.Address.City}}, {{.Seller.Address.State}} {{.Seller.Address.Postal}}<br/>
    {{if .Seller.GSTIN}}GSTIN: <b style="display:inline">{{.Seller.GSTIN}}</b><br/>{{end}}
    {{if .Seller.Email}}✉ {{.Seller.Email}}{{end}}
  </div>
  <div class="box">
    <b>Bill To / Ship To</b>
    {{.Buyer.Name}}<br/>
    {{.ShipTo.Line1}}{{if .ShipTo.Line2}}, {{.ShipTo.Line2}}{{end}}<br/>
    {{.ShipTo.City}}, {{.ShipTo.State}} {{.ShipTo.Postal}}<br/>
    {{if .Buyer.Phone}}☎ {{.Buyer.Phone}}{{end}}
  </div>
</div>

<table>
  <thead><tr>
    <th>#</th><th>Item</th><th>HSN</th><th class="num">Qty</th><th class="num">Rate</th>
    {{if .IsInterstate}}<th class="num">IGST</th>{{else}}<th class="num">CGST</th><th class="num">SGST</th>{{end}}
    <th class="num">Total</th>
  </tr></thead>
  <tbody>
  {{range $i, $it := .Items}}
  <tr>
    <td>{{add1 $i}}</td>
    <td><b>{{.Title}}</b>{{if .SKU}}<br/><span style="color:#888;font-size:10px">SKU: {{.SKU}}</span>{{end}}</td>
    <td>{{.HSN}}</td>
    <td class="num">{{.Quantity}}</td>
    <td class="num">₹{{money .UnitPrice}}</td>
    {{if $.IsInterstate}}
      <td class="num">{{money .IGSTPct}}%<br/>₹{{money .IGSTAmount}}</td>
    {{else}}
      <td class="num">{{money .CGSTPct}}%<br/>₹{{money .CGSTAmount}}</td>
      <td class="num">{{money .SGSTPct}}%<br/>₹{{money .SGSTAmount}}</td>
    {{end}}
    <td class="num">₹{{money .LineTotal}}</td>
  </tr>
  {{end}}
  </tbody>
</table>

<table class="totals">
  <tr><td>Subtotal (Taxable)</td><td class="num">₹{{money .Subtotal}}</td></tr>
  {{if .IsInterstate}}
    <tr><td>IGST</td><td class="num">₹{{money .TotalIGST}}</td></tr>
  {{else}}
    <tr><td>CGST</td><td class="num">₹{{money .TotalCGST}}</td></tr>
    <tr><td>SGST</td><td class="num">₹{{money .TotalSGST}}</td></tr>
  {{end}}
  {{if gt .ShippingCharges 0.0}}<tr><td>Shipping</td><td class="num">₹{{money .ShippingCharges}}</td></tr>{{end}}
  {{if gt .CouponDiscount 0.0}}<tr><td>Coupon ({{.CouponCode}})</td><td class="num">−₹{{money .CouponDiscount}}</td></tr>{{end}}
  <tr class="grand"><td>Grand Total</td><td class="num">₹{{money .GrandTotal}}</td></tr>
</table>

<div class="terms">
  <b>Terms & Notes</b><br/>
  • This is a computer-generated invoice; no signature required.<br/>
  • Returns accepted within policy window. Visit postbook.app/help for details.<br/>
  {{if .Notes}}• {{.Notes}}{{end}}
</div>

<div class="footer">
  Powered by Postbook Commerce · postbook.app · Generated on {{.Date.Format "02 Jan 2006 15:04 MST"}}
</div>
</body></html>`

// add1 is registered in the renderer if needed; for simplicity we hardcode via an inline FuncMap.
// (Note: we add it via a different Parse call if required.)
