package invoice

// Service documents (added 13 Nov 2026 for Momentum Premium): a GST tax
// invoice or credit note for a service one supplier sells to an
// unregistered recipient (B2C), in INTEGER PAISE. The issuer stores the
// paise; this file only checks that they add up and formats them as rupees.
//
// It is separate from Invoice (the commerce order model, float rupees,
// commerce branding) so neither changes the other's output.

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// DocumentKind is what a ServiceDocument is.
type DocumentKind string

const (
	// KindTaxInvoice is a tax invoice (CGST Rules, rule 46).
	KindTaxInvoice DocumentKind = "tax_invoice"
	// KindCreditNote is a credit note against an earlier tax invoice
	// (CGST Act s.34, rule 53).
	KindCreditNote DocumentKind = "credit_note"
)

// MaxDocumentNumberLen is the most characters a GST invoice or credit note
// number may have (rule 46(b), rule 53(1)).
const MaxDocumentNumberLen = 16

// ServiceParty is the supplier as printed.
type ServiceParty struct {
	LegalName string
	Address   string
	GSTIN     string
	StateName string
	StateCode string
}

// ServiceLine is one supply. Every amount is paise and non-negative: a
// credit note states the amounts it credits.
type ServiceLine struct {
	Description  string
	SAC          string
	Quantity     int64
	TaxablePaise int64
	RateBP       int
	CGSTPaise    int64
	SGSTPaise    int64
	IGSTPaise    int64
	TotalPaise   int64
}

// ServiceReference names the invoice a credit note is issued against.
type ServiceReference struct {
	Number string
	Date   string // YYYY-MM-DD
}

// ServiceDocument is one tax invoice or credit note.
type ServiceDocument struct {
	Kind              DocumentKind
	Number            string
	Date              string // YYYY-MM-DD, the issue date in India
	Supplier          ServiceParty
	RecipientName     string
	PlaceOfSupplyName string
	PlaceOfSupplyCode string
	Lines             []ServiceLine
	TaxablePaise      int64
	CGSTPaise         int64
	SGSTPaise         int64
	IGSTPaise         int64
	TotalPaise        int64
	// ReverseCharge: whether tax is payable on reverse charge (printed as
	// Yes/No, rule 46(p)).
	ReverseCharge bool
	// Original is required on a credit note and absent on an invoice.
	Original *ServiceReference
	Notes    []string
}

// ErrServiceDocumentInvalid: the document's amounts or fields do not hold.
var ErrServiceDocumentInvalid = errors.New("invoice: service document does not add up")

// Validate checks every identity the document must satisfy.
func (d ServiceDocument) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrServiceDocumentInvalid, fmt.Sprintf(format, a...))
	}
	switch d.Kind {
	case KindTaxInvoice:
		if d.Original != nil {
			return bad("an invoice references no original")
		}
	case KindCreditNote:
		if d.Original == nil || strings.TrimSpace(d.Original.Number) == "" || !validDate(d.Original.Date) {
			return bad("a credit note names the invoice it credits")
		}
	default:
		return bad("kind %q", d.Kind)
	}
	if n := strings.TrimSpace(d.Number); n == "" || len(n) > MaxDocumentNumberLen {
		return bad("number %q", d.Number)
	}
	if !validDate(d.Date) {
		return bad("date %q", d.Date)
	}
	if len(d.Lines) == 0 {
		return bad("no lines")
	}
	var taxable, cgst, sgst, igst, total int64
	for i, l := range d.Lines {
		if l.Quantity <= 0 || l.TaxablePaise < 0 || l.CGSTPaise < 0 || l.SGSTPaise < 0 || l.IGSTPaise < 0 || l.RateBP < 0 || l.RateBP > 10000 {
			return bad("line %d has a negative amount or impossible rate", i)
		}
		if l.IGSTPaise != 0 && (l.CGSTPaise != 0 || l.SGSTPaise != 0) {
			return bad("line %d charges IGST and CGST/SGST", i)
		}
		if l.TaxablePaise+l.CGSTPaise+l.SGSTPaise+l.IGSTPaise != l.TotalPaise {
			return bad("line %d taxable + GST != total", i)
		}
		taxable += l.TaxablePaise
		cgst += l.CGSTPaise
		sgst += l.SGSTPaise
		igst += l.IGSTPaise
		total += l.TotalPaise
	}
	if taxable != d.TaxablePaise || cgst != d.CGSTPaise || sgst != d.SGSTPaise || igst != d.IGSTPaise || total != d.TotalPaise {
		return bad("lines do not sum to the totals")
	}
	return nil
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// ShortFinancialYear is the Indian financial year of t in its own location,
// as "26-27" for April 2026 to March 2027 (FinancialYear's short form, which
// keeps a document number within 16 characters).
func ShortFinancialYear(t time.Time) string {
	y := t.Year()
	if t.Month() < time.April {
		y--
	}
	return fmt.Sprintf("%02d-%02d", y%100, (y+1)%100)
}

// Rupees formats paise as rupees with Indian digit grouping: 14990000 →
// "₹1,49,900.00", -17582 → "-₹175.82".
func Rupees(paise int64) string {
	sign := ""
	u := uint64(paise)
	if paise < 0 {
		sign, u = "-", uint64(-(paise+1))+1
	}
	whole, frac := u/100, u%100
	return fmt.Sprintf("%s₹%s.%02d", sign, groupIndian(whole), frac)
}

func groupIndian(n uint64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	head, tail := s[:len(s)-3], s[len(s)-3:]
	var parts []string
	for len(head) > 2 {
		parts = append([]string{head[len(head)-2:]}, parts...)
		head = head[:len(head)-2]
	}
	if head != "" {
		parts = append([]string{head}, parts...)
	}
	return strings.Join(parts, ",") + "," + tail
}

var (
	wordsOnes = []string{"Zero", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten",
		"Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen", "Seventeen", "Eighteen", "Nineteen"}
	wordsTens = []string{"", "", "Twenty", "Thirty", "Forty", "Fifty", "Sixty", "Seventy", "Eighty", "Ninety"}
)

func wordsBelow100(n uint64) string {
	if n < 20 {
		return wordsOnes[n]
	}
	if n%10 == 0 {
		return wordsTens[n/10]
	}
	return wordsTens[n/10] + "-" + wordsOnes[n%10]
}

func wordsBelow1000(n uint64) string {
	switch {
	case n < 100:
		return wordsBelow100(n)
	case n%100 == 0:
		return wordsOnes[n/100] + " Hundred"
	}
	return wordsOnes[n/100] + " Hundred " + wordsBelow100(n%100)
}

// wordsIndian spells n in the Indian system (thousand, lakh, crore).
func wordsIndian(n uint64) string {
	if n == 0 {
		return "Zero"
	}
	var parts []string
	if c := n / 10_000_000; c > 0 {
		parts = append(parts, wordsIndian(c)+" Crore")
		n %= 10_000_000
	}
	if l := n / 100_000; l > 0 {
		parts = append(parts, wordsBelow100(l)+" Lakh")
		n %= 100_000
	}
	if t := n / 1000; t > 0 {
		parts = append(parts, wordsBelow100(t)+" Thousand")
		n %= 1000
	}
	if n > 0 {
		parts = append(parts, wordsBelow1000(n))
	}
	return strings.Join(parts, " ")
}

// AmountInWords spells a non-negative amount of paise: 17582 → "Indian
// Rupees One Hundred Seventy-Five and Eighty-Two Paise Only". A negative
// amount is spelt by its magnitude.
func AmountInWords(paise int64) string {
	u := uint64(paise)
	if paise < 0 {
		u = uint64(-(paise + 1)) + 1
	}
	s := "Indian Rupees " + wordsIndian(u/100)
	if p := u % 100; p > 0 {
		s += " and " + wordsBelow100(p) + " Paise"
	}
	return s + " Only"
}

// RatePercent prints basis points as a percentage: 1800 → "18", 250 → "2.5".
func RatePercent(bp int) string {
	s := fmt.Sprintf("%d.%02d", bp/100, bp%100)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}

func displayDate(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return t.Format("02 Jan 2006")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

var serviceTemplate = template.Must(template.New("service").Funcs(template.FuncMap{
	"rupees": Rupees, "date": displayDate, "dash": orDash, "rate": RatePercent, "inc": func(i int) int { return i + 1 },
	"half": func(bp int) int { return bp / 2 }, "otherHalf": func(bp int) int { return bp - bp/2 },
	"yesno": func(b bool) string {
		if b {
			return "Yes"
		}
		return "No"
	},
}).Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>{{.Title}} {{.Doc.Number}}</title>
<style>
@page{size:A4;margin:16mm}
body{font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#111;font-size:12px;margin:0;padding:24px}
h1{font-size:18px;margin:0 0 2px}
.meta{display:flex;justify-content:space-between;gap:24px;border-bottom:2px solid #111;padding-bottom:12px;margin-bottom:16px}
.meta div{line-height:1.6}
.parties{display:grid;grid-template-columns:1fr 1fr;gap:20px;margin-bottom:16px;line-height:1.6}
.label{color:#555;font-size:10px;text-transform:uppercase;letter-spacing:.06em}
table{width:100%;border-collapse:collapse;font-size:11px}
th,td{padding:7px 6px;border-bottom:1px solid #ddd;text-align:left;vertical-align:top}
th{background:#f3f4f6;font-weight:600}
.num{text-align:right;font-variant-numeric:tabular-nums;white-space:nowrap}
.totals{margin:14px 0 0 auto;width:55%}
.totals td{border:none;padding:4px 6px}
.grand td{border-top:2px solid #111;font-weight:700;font-size:13px;padding-top:8px}
.words{margin-top:10px}
.foot{margin-top:28px;font-size:10px;color:#555;line-height:1.6}
</style></head><body>
<div class="meta">
  <div><h1>{{.Title}}</h1>
    <div>Number: <b>{{.Doc.Number}}</b></div>
    <div>Date: {{date .Doc.Date}}</div>
    {{with .Doc.Original}}<div>Against tax invoice {{.Number}} dated {{date .Date}}</div>{{end}}
  </div>
  <div style="text-align:right">
    <div>Place of supply: {{dash .Doc.PlaceOfSupplyName}}{{if .Doc.PlaceOfSupplyCode}} ({{.Doc.PlaceOfSupplyCode}}){{end}}</div>
    <div>Whether tax is payable on reverse charge basis: {{yesno .Doc.ReverseCharge}}</div>
  </div>
</div>
<div class="parties">
  <div><div class="label">Supplier</div>
    <b>{{dash .Doc.Supplier.LegalName}}</b><br>
    {{dash .Doc.Supplier.Address}}<br>
    State: {{dash .Doc.Supplier.StateName}}{{if .Doc.Supplier.StateCode}} ({{.Doc.Supplier.StateCode}}){{end}}<br>
    GSTIN: {{dash .Doc.Supplier.GSTIN}}
  </div>
  <div><div class="label">Recipient</div>
    <b>{{dash .Doc.RecipientName}}</b><br>Unregistered person
  </div>
</div>
<table><thead><tr>
  <th>#</th><th>Description</th><th>SAC</th><th class="num">Qty</th><th class="num">Taxable value</th>
  {{if .Inter}}<th class="num">IGST</th>{{else}}<th class="num">CGST</th><th class="num">SGST</th>{{end}}
  <th class="num">Total</th>
</tr></thead><tbody>
{{range $i, $l := .Doc.Lines}}<tr>
  <td>{{inc $i}}</td><td>{{$l.Description}}</td><td>{{$l.SAC}}</td><td class="num">{{$l.Quantity}}</td>
  <td class="num">{{rupees $l.TaxablePaise}}</td>
  {{if $.Inter}}<td class="num">{{rate $l.RateBP}}%<br>{{rupees $l.IGSTPaise}}</td>
  {{else}}<td class="num">{{rate (half $l.RateBP)}}%<br>{{rupees $l.CGSTPaise}}</td><td class="num">{{rate (otherHalf $l.RateBP)}}%<br>{{rupees $l.SGSTPaise}}</td>{{end}}
  <td class="num">{{rupees $l.TotalPaise}}</td>
</tr>{{end}}
</tbody></table>
<table class="totals"><tbody>
  <tr><td>Taxable value</td><td class="num">{{rupees .Doc.TaxablePaise}}</td></tr>
  {{if .Inter}}<tr><td>IGST</td><td class="num">{{rupees .Doc.IGSTPaise}}</td></tr>
  {{else}}<tr><td>CGST</td><td class="num">{{rupees .Doc.CGSTPaise}}</td></tr><tr><td>SGST</td><td class="num">{{rupees .Doc.SGSTPaise}}</td></tr>{{end}}
  <tr class="grand"><td>{{.TotalLabel}}</td><td class="num">{{rupees .Doc.TotalPaise}}</td></tr>
</tbody></table>
<div class="words">{{.TotalLabel}} in words: {{.Words}}</div>
<div class="foot">
  {{range .Doc.Notes}}<div>{{.}}</div>{{end}}
  <div>This is a computer-generated document and needs no signature.</div>
</div>
</body></html>`))

type serviceView struct {
	Doc        ServiceDocument
	Title      string
	TotalLabel string
	Inter      bool
	Words      string
}

// RenderServiceHTML renders a printable, self-contained HTML document. It
// refuses a document that does not Validate.
func RenderServiceHTML(d ServiceDocument) ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	v := serviceView{Doc: d, Title: "Tax invoice", TotalLabel: "Invoice total", Inter: d.IGSTPaise > 0, Words: AmountInWords(d.TotalPaise)}
	if d.Kind == KindCreditNote {
		v.Title, v.TotalLabel = "Credit note", "Amount credited"
	}
	var buf bytes.Buffer
	if err := serviceTemplate.Execute(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ServiceFilename is a safe file name for a document: "MP_26-27_000001.html".
func ServiceFilename(number, ext string) string {
	var b strings.Builder
	for _, r := range number {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String() + ext
}
