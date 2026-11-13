package invoice

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden PDF hashes in testdata")

func premiumCreditNote() ServiceDocument {
	d := premiumLikeDoc()
	d.Kind, d.Number = KindCreditNote, "MPC/26-27/000001"
	d.Date = "2026-11-20"
	d.Original = &ServiceReference{Number: "MP/26-27/000001", Date: "2026-11-13"}
	d.Notes = []string{"Issued on the full refund of the Momentum Premium pass invoiced above."}
	return d
}

func interStateDoc() ServiceDocument {
	d := premiumLikeDoc()
	d.Number = "MP/26-27/000002"
	d.RecipientName = "Venkata Sai Lakshmi Narasimha Subrahmanyam Chakravarthula"
	d.PlaceOfSupplyName, d.PlaceOfSupplyCode = "Karnataka", "29"
	l := &d.Lines[0]
	l.CGSTPaise, l.SGSTPaise, l.IGSTPaise = 0, 0, 2682
	d.CGSTPaise, d.SGSTPaise, d.IGSTPaise = 0, 0, 2682
	return d
}

// pdfText extracts the text a PDF draws: every content stream is inflated
// and each string shown by Tj/TJ is decoded as UTF-16BE (the embedded fonts
// map code to Unicode one to one, which is what a reader's copy-paste uses).
func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	streamRE := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	var out strings.Builder
	for _, m := range streamRE.FindAllSubmatch(pdf, -1) {
		r, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			continue // a font file, not deflated text
		}
		raw, err := io.ReadAll(r)
		if err != nil || !bytes.Contains(raw, []byte("BT ")) {
			continue
		}
		for _, s := range pdfStrings(raw) {
			if len(s)%2 != 0 {
				t.Fatalf("odd-length text string %q", s)
			}
			u := make([]uint16, len(s)/2)
			for i := range u {
				u[i] = binary.BigEndian.Uint16([]byte(s[2*i:]))
			}
			out.WriteString(string(utf16.Decode(u)))
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// pdfStrings returns the literal strings of a content stream, unescaped.
func pdfStrings(raw []byte) []string {
	var out []string
	for i := 0; i < len(raw); i++ {
		if raw[i] != '(' {
			continue
		}
		var b []byte
		for i++; i < len(raw) && raw[i] != ')'; i++ {
			if raw[i] == '\\' && i+1 < len(raw) {
				i++
				switch raw[i] {
				case 'r':
					b = append(b, '\r')
				case 'n':
					b = append(b, '\n')
				default:
					b = append(b, raw[i])
				}
				continue
			}
			b = append(b, raw[i])
		}
		out = append(out, string(b))
	}
	return out
}

func mustPDF(t *testing.T, d ServiceDocument) []byte {
	t.Helper()
	out, err := RenderServicePDF(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) || !bytes.HasSuffix(bytes.TrimSpace(out), []byte("%%EOF")) {
		t.Fatalf("not a PDF: %q…", out[:min(len(out), 16)])
	}
	return out
}

func wantText(t *testing.T, text string, wants ...string) {
	t.Helper()
	flat := strings.Join(strings.Fields(text), " ")
	for _, w := range wants {
		if !strings.Contains(flat, w) {
			t.Errorf("PDF text lacks %q", w)
		}
	}
	if t.Failed() {
		t.Logf("PDF text:\n%s", text)
	}
}

func TestServicePDFCarriesTheRule46Fields(t *testing.T) {
	text := pdfText(t, mustPDF(t, premiumLikeDoc()))
	wantText(t, text, "Tax Invoice", "MP/26-27/000001", "13 Nov 2026", "Example Pvt Ltd", "1 Road, Hyderabad",
		"State: Telangana (36)", "GSTIN: 36AAAAA0000A1Z5", "Asha <b>", "Unregistered person", "Place of supply: Telangana (36)",
		"Description", "SAC", "Taxable value", "Rate", "CGST", "SGST", "Total",
		"Momentum Premium — 30 days", "998439", "₹149.00", "9% + 9%", "₹13.41", "₹175.82",
		"Invoice total in words: Indian Rupees One Hundred Seventy-Five and Eighty-Two Paise Only",
		"Whether tax is payable on reverse charge basis: No", "This is a computer-generated document")
	if strings.Contains(text, "Credit Note") || strings.Contains(text, "Against tax invoice") || strings.Contains(text, "IGST") {
		t.Error("an intra-state tax invoice prints credit-note or IGST wording")
	}
}

func TestServicePDFCreditNoteNamesTheOriginal(t *testing.T) {
	text := pdfText(t, mustPDF(t, premiumCreditNote()))
	wantText(t, text, "Credit Note", "MPC/26-27/000001", "20 Nov 2026",
		"Against tax invoice MP/26-27/000001 dated 13 Nov 2026", "Amount credited", "₹175.82",
		"Amount credited in words: Indian Rupees", "Issued on the full refund")
	if strings.Contains(text, "Tax Invoice") {
		t.Error("a credit note is titled Tax Invoice")
	}
}

func TestServicePDFInterStateShowsIGSTOnly(t *testing.T) {
	text := pdfText(t, mustPDF(t, interStateDoc()))
	wantText(t, text, "IGST", "18%", "₹26.82", "Karnataka (29)", "Venkata Sai Lakshmi Narasimha Subrahmanyam Chakravarthula")
	if strings.Contains(text, "CGST") || strings.Contains(text, "SGST") {
		t.Error("an inter-state invoice prints CGST/SGST")
	}
}

func TestServicePDFRefusesWhatDoesNotAddUp(t *testing.T) {
	d := premiumLikeDoc()
	d.TotalPaise++
	if _, err := RenderServicePDF(d); !errors.Is(err, ErrServiceDocumentInvalid) {
		t.Fatalf("an invalid document rendered: %v", err)
	}
	c := premiumCreditNote()
	c.Original = nil
	if _, err := RenderServicePDF(c); !errors.Is(err, ErrServiceDocumentInvalid) {
		t.Fatalf("a credit note without its original rendered: %v", err)
	}
}

// The creation date is the document's date and nothing random is written, so
// the bytes are pinned. Rerun with -update after an intended layout change
// (and look at the PDFs it writes next to the hashes).
func TestServicePDFIsDeterministic(t *testing.T) {
	for name, d := range map[string]ServiceDocument{
		"tax_invoice": premiumLikeDoc(), "credit_note": premiumCreditNote(), "inter_state": interStateDoc(),
	} {
		t.Run(name, func(t *testing.T) {
			a, b := mustPDF(t, d), mustPDF(t, d)
			if !bytes.Equal(a, b) {
				t.Fatal("two renders differ")
			}
			if !bytes.Contains(a, []byte("/CreationDate (D:"+strings.ReplaceAll(d.Date, "-", "")+"000000")) {
				t.Error("the creation date is not the document date")
			}
			sum := sha256.Sum256(a)
			got := hex.EncodeToString(sum[:])
			path := filepath.Join("testdata", "service_"+name+".pdf.sha256")
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				look := filepath.Join(os.TempDir(), "service_"+name+".pdf")
				if os.WriteFile(look, a, 0o644) == nil {
					t.Logf("wrote %s to look at", look)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("golden hash: %v (run go test -run TestServicePDFIsDeterministic -update)", err)
			}
			if strings.TrimSpace(string(want)) != got {
				t.Fatalf("PDF bytes changed: sha256 %s, golden %s", got, strings.TrimSpace(string(want)))
			}
		})
	}
}

// The embedded font has a glyph for every character the documents print,
// ₹ first: a missing glyph renders as an empty box, not an error.
func TestEmbeddedFontsCoverTheRupeeSign(t *testing.T) {
	for name, font := range map[string][]byte{"regular": notoSansRegular, "bold": notoSansBold} {
		for _, r := range "₹—–·ÀāēīōūṣṭḍṇṅñçÉé0123456789ABCXYZabcxyz,./()-:%+" {
			if !ttfHasRune(t, font, r) {
				t.Errorf("%s font lacks %q (U+%04X)", name, r, r)
			}
		}
	}
}

// ttfHasRune looks r up in a TrueType font's format-4 (BMP) cmap subtable.
func ttfHasRune(t *testing.T, font []byte, r rune) bool {
	t.Helper()
	u16 := func(o int) int { return int(binary.BigEndian.Uint16(font[o:])) }
	u32 := func(o int) int { return int(binary.BigEndian.Uint32(font[o:])) }
	cmap := -1
	for i, n := 0, u16(4); i < n; i++ {
		rec := 12 + 16*i
		if string(font[rec:rec+4]) == "cmap" {
			cmap = u32(rec + 8)
		}
	}
	if cmap < 0 {
		t.Fatal("font has no cmap")
	}
	for i, n := 0, u16(cmap+2); i < n; i++ {
		rec := cmap + 4 + 8*i
		sub := cmap + u32(rec+4)
		if u16(rec) != 3 || u16(rec+2) != 1 || u16(sub) != 4 {
			continue
		}
		segs := u16(sub+6) / 2
		ends, starts := sub+14, sub+16+2*segs
		deltas, offsets := starts+2*segs, starts+4*segs
		c := int(r)
		for s := 0; s < segs; s++ {
			if c > u16(ends+2*s) {
				continue
			}
			if c < u16(starts+2*s) {
				return false
			}
			ro := u16(offsets + 2*s)
			if ro == 0 {
				return (c+u16(deltas+2*s))&0xFFFF != 0
			}
			g := u16(offsets + 2*s + ro + 2*(c-u16(starts+2*s)))
			return g != 0
		}
		return false
	}
	t.Fatal("font has no Windows BMP cmap")
	return false
}

func TestServicePDFFilename(t *testing.T) {
	for in, want := range map[string]string{
		"MP/26-27/000001":  "MP-26-27-000001.pdf",
		"MPC/26-27/000042": "MPC-26-27-000042.pdf",
		`a"b\c d`:          "a_b_c_d.pdf",
	} {
		if got := ServicePDFFilename(in); got != want {
			t.Errorf("ServicePDFFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServicePDFColumnsFillTheWidth(t *testing.T) {
	for _, inter := range []bool{false, true} {
		var w float64
		for _, c := range lineColumns(inter) {
			w += c.w
		}
		if w != pdfContentW {
			t.Errorf("inter=%v: columns are %.1f mm, the page is %.1f mm", inter, w, pdfContentW)
		}
	}
}
