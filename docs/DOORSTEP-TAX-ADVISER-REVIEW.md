# Doorstep — questions for the tax adviser

**Status: nothing in this document is confirmed.** It lists every tax statement built into `Architecture/shared/gst` for Doorstep (home services) that the engineering team is not certain of. Every seeded rate row carries `NeedsAdviserConfirmation = true`, every computation result exposes that flag, and every invoice produced while it is set must be marked as pending confirmation. Real bookings must not be taken until each point below is answered.

Prepared 4 October 2026 while building Doorstep, an Urban Company–style home-services product. The platform is modelled as an electronic commerce operator (ECO). Gig professionals (cleaners, technicians, electricians, plumbers, carpenters, painters, beauticians) supply the service to the customer through the platform. Customers pay in full at booking; extras approved during the visit are charged at the end. Prices are shown **including GST**. The pilot city is Hyderabad (Telangana, GST state code 36). The Feast questions in `docs/FEAST-TAX-ADVISER-REVIEW.md` on rounding, TCS rate, platform fee, commission and TDS apply here too and are not repeated in full.

## How the engine models it

Each service family has two categories. The booking system picks one from the professional's registration, at completion, using the professional who actually did the job:

- **`<FAMILY>_VIA_ECO`**: the professional has **no GSTIN**. The supply is treated as a housekeeping service notified under CGST Act s.9(5), so the platform is liable as if it were the supplier. The engine **refuses** this category when the professional has a GSTIN; that supply goes on `_REGISTERED`.
- **`<FAMILY>_REGISTERED`**: the professional is registered and liable for their own supply. The engine **refuses** the line if the professional's GSTIN is missing. Through the platform the line carries a marker that the platform collects TCS under s.52. The TCS amount is not computed.

Beauty / salon has **only** `_REGISTERED`, because beauty services are not notified under s.9(5). An unregistered beautician's supply cannot be computed today (question 3).

## Seeded rate table (effective 22 September 2025, IST)

| Category | Supplier | Liable via ECO under s.9(5) | Rate | ITC | SAC |
|---|---|---|---|---|---|
| Home cleaning, unregistered professional | Service professional | Yes | 18% | No | 998533 |
| Home cleaning, registered professional | Service professional | No (platform collects TCS) | 18% | Yes | 998533 |
| Pest control, unregistered | Service professional | Yes | 18% | No | 998531 |
| Pest control, registered | Service professional | No (TCS) | 18% | Yes | 998531 |
| Appliance repair (AC, RO, washing machine), unregistered | Service professional | Yes | 18% | No | 998715 |
| Appliance repair, registered | Service professional | No (TCS) | 18% | Yes | 998715 |
| Electrician / plumber / carpenter, unregistered | Service professional | Yes | 18% | No | 995469 |
| Electrician / plumber / carpenter, registered | Service professional | No (TCS) | 18% | Yes | 995469 |
| Painting, unregistered | Service professional | Yes | 18% | No | 995473 |
| Painting, registered | Service professional | No (TCS) | 18% | Yes | 995473 |
| Salon at home (women and men), registered only | Service professional | No (TCS) | 5% | No | 999722 |
| Platform / convenience fee (shared with Feast) | Platform | n/a (own supply) | 18% | Yes | 998599 |

ITC is shown as "No" on the unregistered rows because the professional cannot claim it and the platform pays a s.9(5) liability in cash.

## Questions

1. **What s.9(5) covers.** Housekeeping services ("such as plumbing, carpentering etc.") are believed to be notified under s.9(5) (Notification 17/2017-Central Tax (Rate), as amended). Does this cover **each** of these: home and sofa/carpet cleaning, pest control, AC servicing and appliance/RO repair, electrical work, plumbing, carpentry, and painting? For any family that is not covered, an unregistered professional's supply is outside s.9(5), and the `_VIA_ECO` row for that family is wrong.
2. **"Liable for registration" or "has a GSTIN".** The s.9(5) shift is believed not to apply where the supplier is liable for registration under s.22(1). The engine uses a simpler test: a GSTIN means `_REGISTERED`, no GSTIN means `_VIA_ECO`. Is that right for a professional who registered **voluntarily** while below the threshold? And for a professional who is over the threshold but has not registered, who is liable, and what must the platform do?
3. **Salon by an unregistered beautician.** Beauty is not under s.9(5). An unregistered beautician below the threshold, supplying through an ECO, is believed to be exempt from compulsory registration (Notification 65/2017-Central Tax), so that supply carries no GST. Is that right? Most salon professionals will be unregistered. If so: (a) may the platform show the same tax-inclusive price, with no GST in it, and what document does the customer receive; (b) must salon at home be limited to registered professionals instead; (c) does the platform collect TCS on such a supply?
4. **Salon rate.** Is beauty and physical well-being (salons, barbers) 5% without ITC from 22 September 2025? Please cite the notification.
5. **SAC codes.** Confirm 998533 (general cleaning), 998531 (disinfecting and exterminating), 998715 (repair of electrical household appliances; does it cover split and window ACs and RO purifiers?), 995469 (repair services related to installations), 995473 (painting), 999722 (cosmetic treatment, manicure and pedicure). Should invoices use trade-level codes instead, such as 995461 (electrical), 995462 (plumbing), 995476 (carpentry), or 999721 (haircuts) for men's salon?
6. **Electrician, plumber, carpenter: 9954 or 9987.** We chose 9954 (installation and building-completion services) over 9987 (repair of goods) because these jobs work on fixtures of the building: wiring, pipes, fittings, woodwork. Is that right? Does 9954 bring in any construction or works-contract treatment for small home repairs?
7. **Parts and materials sold as extras.** During a visit the professional may fit parts at rate-card prices: a capacitor, a tap, a switch, paint. Is the visit plus the parts a composite supply taxed at the service rate, or a separate supply of goods at the goods' own rate? If separate, who supplies the goods (the professional or the platform), and is a goods supply by an unregistered professional through the platform allowed at all? Parts are not modelled; extras are taxed today at the family's service category.
8. **Painting with paint supplied.** When the painter brings the paint, is this a works contract (s.2(119)), and does that change the rate, the SAC or the s.9(5) position?
9. **Place of supply.** Services performed at the customer's home, especially painting, plumbing and electrical work on the building, may be "directly in relation to immovable property" (IGST Act s.12(3)), with the place of supply where the property is. Is that right for each family? The engine takes the place of supply from the caller and uses IGST when it differs from the liable party's state. In the pilot, everything is in Telangana.
10. **Platform registration in Telangana.** Under s.9(5) the platform is liable as the supplier for home services performed in Telangana. Must the platform hold a Telangana GSTIN, or a registration in every state it operates in? Today the engine charges IGST if the platform's state differs from the place of supply.
11. **Inclusive pricing before the professional is known.** Prices are quoted GST-inclusive at booking, before a professional is assigned. The tax split is recomputed at completion with the actual professional. For the 18% families both categories give the same tax. For salon, question 3 may make the same price carry no GST at all. Is it acceptable for the customer's price to stay fixed while the liable party (platform or professional) changes after payment? When is the time of supply for an amount paid in full in advance?
12. **Two payments per booking.** The booking amount is paid at checkout. Extras are paid separately during or after the visit, and an unpaid amount below ₹3,000 becomes "outstanding". Is that one supply invoiced once at completion, or two supplies? When does tax become due on each?
13. **Cancellation and no-show fees.** Customers pay ₹75–₹200 for late cancellation or no-show; these are placeholders. Is a cancellation fee consideration for the main supply, taxed at the family's rate and under s.9(5) where applicable, or a separate platform supply? Who is the supplier when the fee goes to the professional?
14. **Rework.** Free rework within 7 days (30 for repairs) is a child booking at zero price. Confirm it is not a new taxable supply.
15. **Refunds.** A cancelled or failed booking is refunded in full or in part. What credit note does the platform issue under s.9(5)? What does the registered professional issue for a `_REGISTERED` line?
16. **Commission.** The platform keeps a commission from each professional's earnings. Is GST at 18% charged on it? For an unregistered professional, is that a B2C supply by the platform? How does this interact with the platform already paying the s.9(5) tax on the gross?
17. **TCS under s.52 and TDS under s.194-O.** Confirm TCS (believed 0.5%) applies to `_REGISTERED` lines and not to s.9(5) lines. Does TDS under s.194-O apply to payments to professionals?
18. **Invoices.** Under s.9(5), what must the platform's invoice to the customer show, and which number series? For `_REGISTERED` lines, may the platform issue the invoice on the professional's behalf? Not modelled yet. Feast uses `FP/` and `FR/` series; Doorstep has no series yet.

## Engineering assumptions that need confirming

19. **A `_VIA_ECO` line with a professional GSTIN is refused** (`GST_UNSUPPORTED_SUPPLY`), so the platform never pays s.9(5) tax on a registered professional's supply. This depends on question 2.
20. **A `_REGISTERED` line outside the platform** (not through the ECO) leaves the professional liable, with no TCS marker. Doorstep always sells through the platform, so this path is unused.
21. **One rounding group per liable party, rate, SAC and place of supply**, as for Feast: several services from one professional in one booking are rounded together, then shared back to lines.
22. **Effective date.** All Doorstep rows start on 22 September 2025, like Feast's. An invoice dated earlier is refused rather than given a guessed rate.

## Non-tax items raised alongside (founder, not the adviser)

- Gig-worker welfare contribution under the labour codes.
- Insurance for damage in customers' homes.
