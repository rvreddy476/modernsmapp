// Package gst computes Indian GST on a food order: restaurant supplies, the
// platform's own fees, and delivery. Every amount is integer paise; no float
// touches a money path.
//
// # What this package decides and what it does not
//
// It decides, from an effective-dated rate table:
//
//   - the rate, SAC and input-tax-credit flag for each line;
//   - who is LIABLE to pay the tax: the supplier itself (SUPPLIER), or the
//     electronic commerce operator under CGST Act section 9(5)
//     (ECO_SECTION_9_5) when the supply is made through the platform and the
//     category is notified under that section;
//   - whether the line is interstate (IGST) or intrastate (CGST + SGST), by
//     comparing the liable party's GSTIN state with the place of supply the
//     caller passes (IGST Act section 12; the package does not guess the
//     place of supply);
//   - the exact paise split, so that the line amounts always sum to the
//     order total.
//
// It does NOT decide whether premises are "specified premises" (the caller
// picks the category), does NOT compute TCS under CGST Act section 52 (a
// line only carries the ECOCollectsTCS marker), does NOT issue invoices, and
// does NOT model platform-funded discounts or cashback.
//
// # Uncertainty
//
// Every seeded rate row carries NeedsAdviserConfirmation = true, and
// Result.NeedsAdviserConfirmation is true whenever any line used such a row.
// A caller that shows or files these numbers without a tax adviser having
// confirmed the table is using unconfirmed tax positions. The positions the
// adviser must confirm are listed in rates.go against each row.
//
// # Provenance of the arithmetic
//
// The inclusive extraction, the largest-remainder allocation and the
// CGST/SGST split are ported from
// Architecture/services/commerce-service/internal/tax/gst.go, which carries
// its own golden and 20,000-case property tests; line citations are on each
// function in money.go and compute.go.
package gst
