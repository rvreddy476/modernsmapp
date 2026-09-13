# Feast — questions for the tax adviser

**Status: nothing in this document is confirmed.** It lists every tax statement built into `Architecture/shared/gst` (and the KYC formats in `Architecture/shared/kyc`) that the engineering team is not certain of. Every seeded rate row carries `NeedsAdviserConfirmation = true`, every computation result exposes that flag, and every invoice produced while it is set is marked as pending confirmation. Real orders must not be taken until each point below is answered.

Prepared 13 September 2026 while building food delivery (Feast). The platform is modelled as an electronic commerce operator (ECO).

## Seeded rate table (effective 22 September 2025, IST)

| Category | Supplier | Liable via ECO under s.9(5) | Rate | ITC | SAC |
|---|---|---|---|---|---|
| Restaurant, standalone | Restaurant | Yes | 5% | No | 996331 |
| Restaurant in specified premises | Restaurant | No — platform marked as collecting TCS | 18% | Yes | 996331 |
| Cloud kitchen / takeaway | Restaurant | Yes | 5% | No | 996331 |
| Outdoor catering | Restaurant | No | 5% | No | 996334 |
| Outdoor catering in specified premises | Restaurant | No | 18% | Yes | 996334 |
| Platform / convenience fee | Platform | n/a (own supply) | 18% | Yes | 998599 |
| Delivery fee supplied by the platform | Platform | n/a (own supply) | 18% | Yes | 996813 |
| Delivery by a partner through the platform | Delivery partner | Yes | 18% | No | 996813 |

## Questions

1. **Rates and ITC.** Is restaurant service still 5% without ITC, and 18% with ITC in specified premises, after the September 2025 rate changes? Please cite the governing notifications; none are cited in the code.
2. **Specified premises.** Is the test still "declared tariff of any unit of accommodation of ₹7,500 or more per unit per day", or did it change from 1 April 2025 to the value of supply in the preceding financial year, with a voluntary declaration option? The code does not decide this; the restaurant's category is chosen at onboarding.
3. **Specified premises and s.9(5).** Is restaurant service in specified premises outside s.9(5), leaving the restaurant liable, with the platform collecting TCS when the order goes through it?
4. **Outdoor catering.** Is it outside "restaurant service" for s.9(5)? Modelled as caterer-liable, 5% without ITC, or 18% with ITC in specified premises.
5. **Cloud kitchens and takeaway.** Are they restaurant service at 5% without ITC and inside s.9(5)?
6. **SAC codes.** Confirm 996331 and 996334. 998599 for the platform fee is a guess. Confirm 996813 for local delivery.
7. **Platform fee.** Is it the platform's own supply at 18%, with place of supply at the recipient's location?
8. **Delivery.** Was local delivery by unregistered delivery persons through an ECO brought under s.9(5) at 18% from 22 September 2025? What applies to registered delivery partners, which are not modelled? Is 18% right for delivery the platform supplies itself?
9. **Place of supply.** For restaurant service, is it where the service is performed (IGST Act s.12)? Under s.9(5) the platform is taxed from its state of registration; where that differs from the place of supply the code charges IGST. Does that instead mean the platform needs a registration in that state?
10. **TCS under s.52.** Confirm it does not apply to s.9(5) supplies and does apply to supplier-liable supplies through the platform, and confirm the rate (believed 0.5% from 10 July 2024). Only a marker is produced today; no TCS amount is computed.
11. **Rounding.** Exclusive tax is rounded half-up to the paise, once per tax group, then shared across lines. A line can differ by 1 paise from per-line rounding, and CGST/SGST can differ by a few paise at invoice level. Is that acceptable?
12. **Discounts.** A restaurant-funded discount is taken to reduce the taxable value. How should platform-funded discounts and cashback be treated? **Coupons are switched off at launch until this is answered.**
13. **Packaging charges.** Are they part of the restaurant's supply, taxed at the restaurant's rate?
14. **Invoices under s.9(5).** What must the platform's own invoice look like, what series, and when do e-invoicing thresholds apply? Not modelled yet.
15. **Menu prices.** Are listed menu prices exclusive of GST, with tax added at checkout?
16. **Commission.** Is GST at 18% chargeable on the commission the platform deducts from restaurant settlements?
17. **TDS under s.194-O.** Does it apply to restaurant and delivery-partner settlements?

## Assumptions added by order totals, invoices and settlement (13 September 2026)

These are encoded in food-service's pricing, invoice and settlement code. Each needs the same confirmation as the questions above.

18. **Place of supply for every line is the restaurant's state**, including the platform fee and the delivery fee. Question 7 suggests the customer's location may be right for the platform fee. The state is taken from the restaurant's GSTIN, then its recorded GSTIN state code, then an exact state-name match; a restaurant whose state cannot be determined cannot take an order.
19. **GST on commission and TCS are each computed once on the settlement-period total**, rounded half up to the paise, not per order. Question 11 covers only per-order rounding.
20. **The TCS base is the taxable value after the restaurant's own discount**, and a refund does not reduce it.
21. **Commission and the GST on commission are not reversed when a delivered order is refunded.**
22. **The restaurant's share of a refund includes the GST it collected**, and a partial refund is split proportionally across the order's lines.
23. **Coupons, once switched on, are treated as restaurant-funded.** This extends question 12; coupons remain off.
24. **Packaging charges count toward commission** as well as being taxed at the food rate. Question 13 covers only the rate.
25. **Invoice numbering:** platform invoices use `FP/<financial year>/<sequence>` (for example `FP/2627/000001`) and restaurant invoices use `FR/<financial year>/<sequence>` on each restaurant's own series, at most 16 characters. This is a concrete answer to question 14 that needs confirming.
26. **Delivery-partner settlement is 80% of the delivery fee with no GST applied.** Question 17 covers only TDS.
27. **Zero-value lines** (a free item, a zero fee) are left out of the tax computation and off the invoice.

Current rates encoded as configuration and pending confirmation: GST on commission 18% (`FOOD_COMMISSION_GST_BP=1800`); TCS 0.5% (`FOOD_TCS_RATE_BP=50`).

## KYC formats (not tax, also unverified)

- GST state codes: whether 25 and 28 are still valid; code 99 is refused.
- PAN holder types: the ten recognised letters. If real PANs use others (for example E or K), they will be refused.
- Aadhaar never starts with 0 or 1.
- Driving licences: only the 15-character Sarathi layout is accepted; older formats are refused.
- Vehicle registrations: state-series and BH patterns, including the TS/TG, OR/OD and UA/UK prefixes.
- FSSAI licence numbers: a 14-digit format check only; no meaning is read into the digits.
