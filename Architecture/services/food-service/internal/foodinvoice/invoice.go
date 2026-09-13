// Package foodinvoice renders a Feast order's tax invoice from the GST
// breakdown stored on the order (Wave 1 B3). It is pure.
//
// One order yields up to two sections, grouped by who is liable for the tax:
//   - PLATFORM: the platform's own fees plus any restaurant supply the
//     platform pays GST on under s.9(5), on the platform invoice series;
//   - RESTAURANT: restaurant supplies the restaurant itself is liable for,
//     showing the restaurant GSTIN, on that restaurant's own series.
//
// Every invoice carries AdviserMarker while any rate is unconfirmed. The
// invoice format (number format, series, s.9(5) wording) is adviser
// question 14 and is not settled.
package foodinvoice

import (
	"bytes"
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/shared/gst"
)

const (
	IssuerPlatform   = "PLATFORM"
	IssuerRestaurant = "RESTAURANT"

	// AdviserMarker is printed on every invoice while a rate is unconfirmed.
	AdviserMarker = pricing.AdviserNotice

	// PlatformName names the electronic commerce operator on its section.
	PlatformName = "Feast (electronic commerce operator)"
)

type Party struct {
	Name        string `json:"name"`
	LegalName   string `json:"legal_name,omitempty"`
	GSTIN       string `json:"gstin,omitempty"`
	AddressLine string `json:"address_line,omitempty"`
	City        string `json:"city,omitempty"`
	State       string `json:"state,omitempty"`
}

// LineDescription names an item or add-on line by its breakdown ref.
type LineDescription struct {
	Description    string
	Quantity       int64
	UnitPricePaise int64
}

// LegacyItem is an order item placed before B3 (no stored breakdown).
type LegacyItem struct {
	Name           string
	HSN            string
	Quantity       int64
	UnitPricePaise int64
	// LineTotalPaise is unit price x quantity, BEFORE tax.
	LineTotalPaise int64
	TaxPaise       int64
	TaxPercent     float64
}

type Data struct {
	OrderID                 string
	OrderNumber             string
	PlacedAt                time.Time
	Restaurant              Party
	Buyer                   Party
	Breakdown               *pricing.Breakdown
	Descriptions            map[string]LineDescription
	PlatformInvoiceNumber   string
	RestaurantInvoiceNumber string
	LegacyInvoiceNumber     string
	FinalAmountPaise        int64
	Legacy                  []LegacyItem
}

type Line struct {
	Ref            string `json:"ref,omitempty"`
	Kind           string `json:"kind"`
	Description    string `json:"description"`
	SAC            string `json:"sac,omitempty"`
	Quantity       int64  `json:"quantity"`
	UnitPricePaise int64  `json:"unit_price_paise"`
	SuppliedBy     string `json:"supplied_by"`
	Liability      string `json:"liability"`
	RateBP         int32  `json:"rate_bp"`
	RatePercent    string `json:"rate_percent"`
	DiscountPaise  int64  `json:"discount_paise"`
	TaxablePaise   int64  `json:"taxable_paise"`
	CGSTPaise      int64  `json:"cgst_paise"`
	SGSTPaise      int64  `json:"sgst_paise"`
	IGSTPaise      int64  `json:"igst_paise"`
	TaxPaise       int64  `json:"tax_paise"`
	TotalPaise     int64  `json:"total_paise"`
}

type Section struct {
	Issuer        string   `json:"issuer"`
	Title         string   `json:"title"`
	InvoiceNumber string   `json:"invoice_number"`
	IssuerName    string   `json:"issuer_name"`
	IssuerGSTIN   string   `json:"issuer_gstin,omitempty"`
	Lines         []Line   `json:"lines"`
	TaxablePaise  int64    `json:"taxable_paise"`
	CGSTPaise     int64    `json:"cgst_paise"`
	SGSTPaise     int64    `json:"sgst_paise"`
	IGSTPaise     int64    `json:"igst_paise"`
	TaxPaise      int64    `json:"tax_paise"`
	TotalPaise    int64    `json:"total_paise"`
	Notes         []string `json:"notes"`
}

type Document struct {
	OrderID                      string    `json:"order_id,omitempty"`
	OrderNumber                  string    `json:"order_number"`
	InvoiceDate                  string    `json:"invoice_date"`
	PlaceOfSupplyState           string    `json:"place_of_supply_state,omitempty"`
	Buyer                        Party     `json:"buyer"`
	Sections                     []Section `json:"sections"`
	GrandTotalPaise              int64     `json:"grand_total_paise"`
	Currency                     string    `json:"currency"`
	MenuPricesTreatedAsExclusive bool      `json:"menu_prices_treated_as_exclusive"`
	NeedsAdviserConfirmation     bool      `json:"needs_adviser_confirmation"`
	AdviserMarker                string    `json:"adviser_marker,omitempty"`
	Legacy                       bool      `json:"legacy"`
	Notes                        []string  `json:"notes"`
}

func ratePercent(bp int32) string { return fmt.Sprintf("%d.%02d", bp/100, bp%100) }

func (s *Section) add(l Line) {
	s.Lines = append(s.Lines, l)
	s.TaxablePaise += l.TaxablePaise
	s.CGSTPaise += l.CGSTPaise
	s.SGSTPaise += l.SGSTPaise
	s.IGSTPaise += l.IGSTPaise
	s.TaxPaise += l.TaxPaise
	s.TotalPaise += l.TotalPaise
}

// LegacyLine converts a pre-B3 item. line_total is already pre-tax, so the
// taxable value IS the line total; subtracting the tax from it understated
// every legacy invoice.
func LegacyLine(it LegacyItem) Line {
	bp := int32(math.Round(it.TaxPercent * 100))
	cgst := it.TaxPaise / 2
	return Line{
		Kind: pricing.KindItem, Description: it.Name, SAC: it.HSN, Quantity: it.Quantity, UnitPricePaise: it.UnitPricePaise,
		Liability: string(gst.LiabilitySupplier), RateBP: bp, RatePercent: ratePercent(bp),
		TaxablePaise: it.LineTotalPaise, CGSTPaise: cgst, SGSTPaise: it.TaxPaise - cgst, TaxPaise: it.TaxPaise,
		TotalPaise: it.LineTotalPaise + it.TaxPaise,
	}
}

func restaurantIssuerName(p Party) string {
	if strings.TrimSpace(p.LegalName) != "" {
		return p.LegalName
	}
	return p.Name
}

// Build assembles the invoice document.
func Build(d Data) (*Document, error) {
	doc := &Document{
		OrderID: d.OrderID, OrderNumber: d.OrderNumber, InvoiceDate: d.PlacedAt.UTC().Format("2006-01-02"),
		Buyer: d.Buyer, Currency: "INR", Sections: []Section{}, Notes: []string{},
	}
	if d.Breakdown == nil {
		doc.Legacy = true
		doc.NeedsAdviserConfirmation = true
		s := Section{Issuer: IssuerRestaurant, Title: "Tax invoice", InvoiceNumber: d.LegacyInvoiceNumber,
			IssuerName: restaurantIssuerName(d.Restaurant), IssuerGSTIN: d.Restaurant.GSTIN, Lines: []Line{},
			Notes: []string{"Order placed before per-party GST invoicing; figures are as recorded at the time."}}
		for _, it := range d.Legacy {
			l := LegacyLine(it)
			l.SuppliedBy = d.Restaurant.Name
			s.add(l)
		}
		doc.Sections = append(doc.Sections, s)
		doc.GrandTotalPaise = s.TotalPaise
		if d.FinalAmountPaise > 0 {
			doc.GrandTotalPaise = d.FinalAmountPaise
		}
		doc.AdviserMarker = AdviserMarker
		return doc, nil
	}

	b := d.Breakdown
	doc.PlaceOfSupplyState = b.PlaceOfSupplyState
	doc.MenuPricesTreatedAsExclusive = b.MenuPricesTreatedAsExclusive
	doc.NeedsAdviserConfirmation = b.NeedsAdviserConfirmation
	restaurant := Section{Issuer: IssuerRestaurant, Title: "Tax invoice issued by the restaurant",
		InvoiceNumber: d.RestaurantInvoiceNumber, IssuerName: restaurantIssuerName(d.Restaurant), IssuerGSTIN: d.Restaurant.GSTIN,
		Lines: []Line{}, Notes: []string{"The platform collects TCS under section 52 on this supply; the amount is not shown here."}}
	platform := Section{Issuer: IssuerPlatform, Title: "Tax invoice issued by the platform",
		InvoiceNumber: d.PlatformInvoiceNumber, IssuerName: PlatformName, Lines: []Line{}, Notes: []string{}}
	anySection95 := false
	for _, bl := range b.Lines {
		l := Line{
			Ref: bl.Ref, Kind: bl.Kind, SAC: bl.SAC, Liability: bl.Liability, RateBP: bl.RateBP, RatePercent: ratePercent(bl.RateBP),
			Quantity: 1, UnitPricePaise: bl.AmountPaise, DiscountPaise: bl.AllocatedDiscountPaise,
			TaxablePaise: bl.TaxablePaise, CGSTPaise: bl.CGSTPaise, SGSTPaise: bl.SGSTPaise, IGSTPaise: bl.IGSTPaise,
			TaxPaise: bl.TaxPaise, TotalPaise: bl.GrossPaise,
		}
		switch bl.Kind {
		case pricing.KindItem, pricing.KindAddon:
			desc, ok := d.Descriptions[bl.Ref]
			if !ok {
				return nil, fmt.Errorf("foodinvoice: no description for line %s", bl.Ref)
			}
			l.Description, l.Quantity, l.UnitPricePaise = desc.Description, desc.Quantity, desc.UnitPricePaise
		case pricing.KindPackaging:
			l.Description = "Packaging charges"
		case pricing.KindPlatformFee:
			l.Description = "Platform fee"
		case pricing.KindDeliveryFee:
			l.Description = "Delivery fee"
		}
		if bl.Supplier == string(gst.SupplierRestaurant) {
			l.SuppliedBy = d.Restaurant.Name
		} else {
			l.SuppliedBy = PlatformName
		}
		if bl.LiableParty == IssuerRestaurant {
			if bl.LiablePartyGSTIN != "" {
				restaurant.IssuerGSTIN = bl.LiablePartyGSTIN
			}
			restaurant.add(l)
			continue
		}
		if platform.IssuerGSTIN == "" {
			platform.IssuerGSTIN = bl.LiablePartyGSTIN
		}
		if bl.Liability == string(gst.LiabilityECOSection95) {
			anySection95 = true
		}
		platform.add(l)
	}
	if anySection95 {
		platform.Notes = append(platform.Notes, "Restaurant service supplied through the platform: the platform pays the GST on it as the electronic commerce operator under section 9(5).")
	}
	if len(restaurant.Lines) > 0 {
		doc.Sections = append(doc.Sections, restaurant)
	}
	if len(platform.Lines) > 0 {
		doc.Sections = append(doc.Sections, platform)
	}
	var sum int64
	for _, s := range doc.Sections {
		sum += s.TotalPaise
	}
	if sum != b.TotalPaise {
		return nil, fmt.Errorf("foodinvoice: sections total %d != breakdown total %d", sum, b.TotalPaise)
	}
	doc.GrandTotalPaise = b.TotalPaise
	if doc.MenuPricesTreatedAsExclusive {
		doc.Notes = append(doc.Notes, "Menu prices are treated as exclusive of GST; tax is added at checkout.")
	}
	if doc.NeedsAdviserConfirmation {
		doc.AdviserMarker = AdviserMarker
	}
	return doc, nil
}

func rupees(p int64) string {
	sign := ""
	if p < 0 {
		sign, p = "-", -p
	}
	return fmt.Sprintf("%s₹%d.%02d", sign, p/100, p%100)
}

var htmlTemplate = template.Must(template.New("invoice").Funcs(template.FuncMap{"rupees": rupees}).Parse(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Invoice {{.OrderNumber}}</title>
<style>
body{font-family:-apple-system,Segoe UI,Roboto,sans-serif;color:#111;font-size:12px;margin:0;padding:20px}
.marker{border:2px solid #b45309;background:#fffbeb;color:#92400e;padding:10px;font-weight:700;margin-bottom:16px}
h1{font-size:18px;margin:0 0 4px}h2{font-size:14px;margin:18px 0 6px}
table{width:100%;border-collapse:collapse;font-size:11px}th,td{padding:6px;border-bottom:1px solid #ddd;text-align:left;vertical-align:top}
.num{text-align:right;font-variant-numeric:tabular-nums}.note{color:#555;font-size:10px}.total td{font-weight:700}
</style></head><body>
{{if .AdviserMarker}}<div class="marker">{{.AdviserMarker}}</div>{{end}}
<h1>Order {{.OrderNumber}}</h1>
<div>Invoice date: {{.InvoiceDate}}{{if .PlaceOfSupplyState}} · Place of supply (state code): {{.PlaceOfSupplyState}}{{end}}</div>
<div>Bill to: {{.Buyer.Name}}{{if .Buyer.City}}, {{.Buyer.City}}{{end}}{{if .Buyer.State}}, {{.Buyer.State}}{{end}}</div>
{{range .Sections}}
<h2>{{.Title}}</h2>
<div>Invoice number: <b>{{.InvoiceNumber}}</b> · Issued by: {{.IssuerName}}{{if .IssuerGSTIN}} · GSTIN: {{.IssuerGSTIN}}{{end}}</div>
<table><thead><tr><th>Description</th><th>Supplied by</th><th>SAC</th><th class="num">Qty</th><th class="num">Taxable</th><th class="num">Rate</th><th class="num">CGST</th><th class="num">SGST</th><th class="num">IGST</th><th class="num">Total</th></tr></thead><tbody>
{{range .Lines}}<tr><td>{{.Description}}</td><td>{{.SuppliedBy}}</td><td>{{.SAC}}</td><td class="num">{{.Quantity}}</td><td class="num">{{rupees .TaxablePaise}}</td><td class="num">{{.RatePercent}}%</td><td class="num">{{rupees .CGSTPaise}}</td><td class="num">{{rupees .SGSTPaise}}</td><td class="num">{{rupees .IGSTPaise}}</td><td class="num">{{rupees .TotalPaise}}</td></tr>
{{end}}<tr class="total"><td colspan="4">Section total</td><td class="num">{{rupees .TaxablePaise}}</td><td></td><td class="num">{{rupees .CGSTPaise}}</td><td class="num">{{rupees .SGSTPaise}}</td><td class="num">{{rupees .IGSTPaise}}</td><td class="num">{{rupees .TotalPaise}}</td></tr>
</tbody></table>
{{range .Notes}}<p class="note">{{.}}</p>{{end}}
{{end}}
<h2>Amount paid: {{rupees .GrandTotalPaise}}</h2>
{{range .Notes}}<p class="note">{{.}}</p>{{end}}
{{if .AdviserMarker}}<div class="marker">{{.AdviserMarker}}</div>{{end}}
</body></html>`))

// RenderHTML renders a printable invoice.
func RenderHTML(doc *Document) ([]byte, error) {
	var buf bytes.Buffer
	if err := htmlTemplate.Execute(&buf, doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
