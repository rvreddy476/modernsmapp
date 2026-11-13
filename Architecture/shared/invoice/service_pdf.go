package invoice

// A pure-Go PDF of a ServiceDocument (13 Nov 2026): no external binary and
// no CGO, so it renders in the Alpine images that never had wkhtmltopdf.
// The text is set in Noto Sans (SIL OFL 1.1, fonts/OFL.txt), embedded as a
// subset, which carries ₹ and the Latin letters of Indian names.
//
// The output is deterministic: the creation and modification dates are the
// document's own date (midnight IST), resources are written in sorted order
// and nothing random is written, so the same document always yields the same
// bytes and a golden test can pin them.

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"codeberg.org/go-pdf/fpdf"
)

//go:embed fonts/NotoSans-Regular.ttf
var notoSansRegular []byte

//go:embed fonts/NotoSans-Bold.ttf
var notoSansBold []byte

const (
	pdfFamily    = "NotoSans"
	pdfMargin    = 15.0
	pdfPageW     = 210.0
	pdfContentW  = pdfPageW - 2*pdfMargin
	pdfBreakAt   = 297.0 - 18.0
	pdfAmountPt  = 8.5
	pdfBodyPt    = 9.5
	pdfLineH     = 4.8
	pdfCellInset = 1.5
)

// pdfIST is India Standard Time: a document's date is an Indian date.
var pdfIST = time.FixedZone("IST", 5*3600+30*60)

// RenderServicePDF renders an A4 PDF of the document. It refuses a document
// that does not Validate.
func RenderServicePDF(d ServiceDocument) ([]byte, error) {
	return renderServicePDF(d, true)
}

type pdfColumn struct {
	head  string
	w     float64
	align string
	cell  func(i int, l ServiceLine) string
}

func renderServicePDF(d ServiceDocument, compress bool) ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	title, totalLabel := "Tax Invoice", "Invoice total"
	if d.Kind == KindCreditNote {
		title, totalLabel = "Credit Note", "Amount credited"
	}
	inter := d.IGSTPaise > 0
	issued, err := time.ParseInLocation("2006-01-02", d.Date, pdfIST)
	if err != nil { // Validate already refused it; kept for the compiler's sake.
		return nil, fmt.Errorf("%w: date %q", ErrServiceDocumentInvalid, d.Date)
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(compress)
	pdf.SetCatalogSort(true)
	pdf.SetCreationDate(issued)
	pdf.SetModificationDate(issued)
	pdf.SetProducer("shared/invoice", false)
	pdf.SetTitle(title+" "+d.Number, true)
	if s := strings.TrimSpace(d.Supplier.LegalName); s != "" {
		pdf.SetAuthor(s, true)
	}
	pdf.AddUTF8FontFromBytes(pdfFamily, "", notoSansRegular)
	pdf.AddUTF8FontFromBytes(pdfFamily, "B", notoSansBold)
	pdf.SetMargins(pdfMargin, pdfMargin, pdfMargin)
	pdf.SetAutoPageBreak(true, 297.0-pdfBreakAt)
	pdf.SetCellMargin(pdfCellInset)
	pdf.SetTextColor(17, 17, 17)
	pdf.SetDrawColor(17, 17, 17)
	pdf.AddPage()

	// Title, number and date.
	top := pdf.GetY()
	pdf.SetFont(pdfFamily, "B", 18)
	pdf.CellFormat(95, 9, title, "", 0, "L", false, 0, "")
	rightPair(pdf, top+0.5, "Number: ", d.Number)
	rightPair(pdf, top+6, "Date: ", displayDate(d.Date))
	pdf.SetXY(pdfMargin, top+12)
	if d.Original != nil {
		pdf.SetFont(pdfFamily, "", pdfBodyPt)
		pdf.CellFormat(pdfContentW, pdfLineH+0.4,
			fmt.Sprintf("Against tax invoice %s dated %s", d.Original.Number, displayDate(d.Original.Date)), "", 1, "L", false, 0, "")
	}
	pdf.Ln(1.5)
	rule(pdf, 0.5)

	// Supplier and recipient.
	colW := (pdfContentW - 10) / 2
	partiesTop := pdf.GetY() + 3
	pdf.SetXY(pdfMargin, partiesTop)
	label(pdf, colW, "SUPPLIER")
	pdf.SetFont(pdfFamily, "B", 11)
	pdf.MultiCell(colW, 5.4, orDash(d.Supplier.LegalName), "", "L", false)
	pdf.SetFont(pdfFamily, "", pdfBodyPt)
	pdf.MultiCell(colW, pdfLineH, orDash(d.Supplier.Address), "", "L", false)
	pdf.MultiCell(colW, pdfLineH, "State: "+withCode(d.Supplier.StateName, d.Supplier.StateCode), "", "L", false)
	pdf.MultiCell(colW, pdfLineH, "GSTIN: "+orDash(d.Supplier.GSTIN), "", "L", false)
	leftEnd := pdf.GetY()

	rightX := pdfMargin + colW + 10
	pdf.SetXY(rightX, partiesTop)
	label(pdf, colW, "RECIPIENT")
	pdf.SetX(rightX)
	pdf.SetFont(pdfFamily, "B", 11)
	pdf.MultiCell(colW, 5.4, orDash(d.RecipientName), "", "L", false)
	pdf.SetX(rightX)
	pdf.SetFont(pdfFamily, "", pdfBodyPt)
	pdf.MultiCell(colW, pdfLineH, "Unregistered person", "", "L", false)
	rightEnd := pdf.GetY()

	pdf.SetXY(pdfMargin, max(leftEnd, rightEnd)+3)
	pdf.SetFont(pdfFamily, "", pdfBodyPt)
	pdf.MultiCell(pdfContentW, pdfLineH, "Place of supply: "+withCode(d.PlaceOfSupplyName, d.PlaceOfSupplyCode), "", "L", false)
	pdf.MultiCell(pdfContentW, pdfLineH, "Whether tax is payable on reverse charge basis: "+yesNo(d.ReverseCharge), "", "L", false)
	pdf.Ln(4)

	// Lines.
	cols := lineColumns(inter)
	tableHead(pdf, cols)
	for i, l := range d.Lines {
		pdf.SetFont(pdfFamily, "", pdfBodyPt)
		desc := pdf.SplitText(l.Description, cols[1].w)
		if len(desc) == 0 {
			desc = []string{""}
		}
		h := float64(len(desc))*pdfLineH + 2
		if pdf.GetY()+h > pdfBreakAt {
			pdf.AddPage()
			tableHead(pdf, cols)
		}
		y := pdf.GetY()
		x := pdfMargin
		for ci, c := range cols {
			pdf.SetXY(x, y+1)
			if ci == 1 {
				pdf.SetFont(pdfFamily, "", pdfBodyPt)
				for _, s := range desc {
					pdf.SetX(x)
					pdf.CellFormat(c.w, pdfLineH, s, "", 2, "L", false, 0, "")
				}
			} else {
				pdf.SetFont(pdfFamily, "", cellPt(c))
				pdf.CellFormat(c.w, pdfLineH, c.cell(i, l), "", 0, c.align, false, 0, "")
			}
			x += c.w
		}
		pdf.SetDrawColor(210, 210, 210)
		pdf.SetLineWidth(0.2)
		pdf.Line(pdfMargin, y+h, pdfMargin+pdfContentW, y+h)
		pdf.SetDrawColor(17, 17, 17)
		pdf.SetXY(pdfMargin, y+h)
	}

	// Totals.
	type total struct {
		label string
		paise int64
	}
	totals := []total{{"Taxable value", d.TaxablePaise}}
	if inter {
		totals = append(totals, total{"IGST", d.IGSTPaise})
	} else {
		totals = append(totals, total{"CGST", d.CGSTPaise}, total{"SGST", d.SGSTPaise})
	}
	const boxW, valueW = 90.0, 38.0
	boxX := pdfMargin + pdfContentW - boxW
	if pdf.GetY()+float64(len(totals)+1)*6+20 > pdfBreakAt {
		pdf.AddPage()
	}
	pdf.Ln(3)
	for _, t := range totals {
		pdf.SetX(boxX)
		pdf.SetFont(pdfFamily, "", pdfBodyPt)
		pdf.CellFormat(boxW-valueW, 5.5, t.label, "", 0, "L", false, 0, "")
		pdf.CellFormat(valueW, 5.5, Rupees(t.paise), "", 1, "R", false, 0, "")
	}
	y := pdf.GetY() + 1
	pdf.SetLineWidth(0.5)
	pdf.Line(boxX, y, boxX+boxW, y)
	pdf.SetXY(boxX, y+1)
	pdf.SetFont(pdfFamily, "B", 11)
	pdf.CellFormat(boxW-valueW, 7, totalLabel, "", 0, "L", false, 0, "")
	pdf.CellFormat(valueW, 7, Rupees(d.TotalPaise), "", 1, "R", false, 0, "")

	pdf.Ln(4)
	pdf.SetX(pdfMargin)
	pdf.SetFont(pdfFamily, "", pdfBodyPt)
	pdf.MultiCell(pdfContentW, pdfLineH, totalLabel+" in words: "+AmountInWords(d.TotalPaise), "", "L", false)

	pdf.Ln(8)
	pdf.SetFont(pdfFamily, "", 8.5)
	pdf.SetTextColor(85, 85, 85)
	for _, n := range d.Notes {
		pdf.MultiCell(pdfContentW, 4.4, n, "", "L", false)
	}
	pdf.MultiCell(pdfContentW, 4.4, "This is a computer-generated document and needs no signature.", "", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("invoice: render pdf: %w", err)
	}
	return buf.Bytes(), nil
}

func lineColumns(inter bool) []pdfColumn {
	no := pdfColumn{"#", 8, "L", func(i int, _ ServiceLine) string { return fmt.Sprintf("%d", i+1) }}
	sac := pdfColumn{"SAC", 15, "L", func(_ int, l ServiceLine) string { return l.SAC }}
	money := func(head string, w float64, f func(ServiceLine) int64) pdfColumn {
		return pdfColumn{head, w, "R", func(_ int, l ServiceLine) string { return Rupees(f(l)) }}
	}
	taxable := func(l ServiceLine) int64 { return l.TaxablePaise }
	totalOf := func(l ServiceLine) int64 { return l.TotalPaise }
	if inter {
		return []pdfColumn{no, {"Description", 66, "L", nil}, sac,
			money("Taxable value", 26, taxable),
			{"Rate", 13, "C", func(_ int, l ServiceLine) string { return RatePercent(l.RateBP) + "%" }},
			money("IGST", 26, func(l ServiceLine) int64 { return l.IGSTPaise }),
			money("Total", 26, totalOf)}
	}
	return []pdfColumn{no, {"Description", 50, "L", nil}, sac,
		money("Taxable value", 24, taxable),
		{"Rate", 17, "C", func(_ int, l ServiceLine) string {
			return RatePercent(l.RateBP/2) + "% + " + RatePercent(l.RateBP-l.RateBP/2) + "%"
		}},
		money("CGST", 22, func(l ServiceLine) int64 { return l.CGSTPaise }),
		money("SGST", 22, func(l ServiceLine) int64 { return l.SGSTPaise }),
		money("Total", 22, totalOf)}
}

// cellPt keeps a money column's figures on one line.
func cellPt(c pdfColumn) float64 {
	if c.align != "L" {
		return pdfAmountPt
	}
	return pdfBodyPt
}

func tableHead(pdf *fpdf.Fpdf, cols []pdfColumn) {
	pdf.SetFont(pdfFamily, "B", 8.5)
	pdf.SetFillColor(243, 244, 246)
	pdf.SetX(pdfMargin)
	for _, c := range cols {
		pdf.CellFormat(c.w, 7, c.head, "", 0, c.align, true, 0, "")
	}
	pdf.Ln(7)
}

// rightPair prints "label value" (value in bold) flush with the right margin.
func rightPair(pdf *fpdf.Fpdf, y float64, labelText, value string) {
	pdf.SetFont(pdfFamily, "", pdfBodyPt)
	wl := pdf.GetStringWidth(labelText)
	pdf.SetFont(pdfFamily, "B", pdfBodyPt)
	wv := pdf.GetStringWidth(value)
	x := pdfMargin + pdfContentW - wl - wv - 2*pdfCellInset
	pdf.SetXY(x, y)
	pdf.SetFont(pdfFamily, "", pdfBodyPt)
	pdf.CellFormat(wl+pdfCellInset, 5, labelText, "", 0, "L", false, 0, "")
	pdf.SetFont(pdfFamily, "B", pdfBodyPt)
	pdf.CellFormat(wv+pdfCellInset, 5, value, "", 0, "L", false, 0, "")
}

func label(pdf *fpdf.Fpdf, w float64, s string) {
	pdf.SetFont(pdfFamily, "B", 7.5)
	pdf.SetTextColor(85, 85, 85)
	pdf.CellFormat(w, 4.5, s, "", 2, "L", false, 0, "")
	pdf.SetTextColor(17, 17, 17)
}

func rule(pdf *fpdf.Fpdf, width float64) {
	y := pdf.GetY()
	pdf.SetLineWidth(width)
	pdf.Line(pdfMargin, y, pdfMargin+pdfContentW, y)
}

func withCode(name, code string) string {
	s := orDash(name)
	if strings.TrimSpace(code) != "" {
		s += " (" + code + ")"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// ServicePDFFilename is the download name of a document's PDF: the number
// with every "/" as "-" and anything else unsafe as "_", so
// "MP/26-27/000001" → "MP-26-27-000001.pdf".
func ServicePDFFilename(number string) string {
	return ServiceFilename(strings.ReplaceAll(number, "/", "-"), ".pdf")
}
