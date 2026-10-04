# Doorstep — questions for the tax adviser

**Status: nothing in this document is confirmed.** It lists every tax statement built into `Architecture/shared/gst` for Doorstep (home services) that the engineering team is not certain of. Every seeded rate row carries `NeedsAdviserConfirmation = true`, every computation result exposes that flag, and every invoice produced while it is set must be marked as pending confirmation. Real bookings must not be taken until each point below is answered.

Prepared 4 October 2026 while building Doorstep, an Urban Company–style home-services product. The platform is modelled as an electronic commerce operator (ECO). Gig professionals (cleaners, technicians, electricians, plumbers, carpenters, painters, beauticians) supply the service to the customer through the platform. Customers pay in full at booking; extras approved during the visit are charged at the end. Prices are shown **including GST**. The pilot city is Hyderabad (Telangana, GST state code 36). The Feast questions in `docs/FEAST-TAX-ADVISER-REVIEW.md` on rounding, TCS rate, platform fee, commission and TDS apply here too and are not repeated in full.

## How the engine models it

Each service family has two categories. The booking system picks one from the professional's registration, at completion, using the professional who actually did the job:

- **`<FAMILY>_VIA_ECO`**: the professional has **no GSTIN**. The supply is treated as a housekeeping service notified under CGST Act s.9(5), so the platform is liable as if it were the supplier. The engine **refuses** this category when the professional has a GSTIN; that supply goes on `_REGISTERED`.
- **`<FAMILY>_REGISTERED`**: the professional is registered and liable for their own supply. The engine **refuses** the line if the professional's GSTIN is missing. Through the platform the line carries a marker that the platform collects TCS under s.52. The TCS amount is not computed.

Beauty / salon has **only** `_REGISTERED`, because beauty services are not notified under s.9(5). An unregistered beautician's supply cannot be computed today (question 3).

The families added on 4 October 2026 (car wash, home staffing, packers and movers, photography, yoga, construction) follow the same rule. Only construction (small masonry and tiling repairs at home) is treated as housekeeping and has both categories. The other five have `_REGISTERED` only (questions 23–31).

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
| Car wash at the customer's parking (`CAR_CARE_REGISTERED`), registered only | Service professional | No (TCS) | 18% | Yes | 998714 |
| Home staffing: cook, house help, nanny, driver, hourly or monthly (`HOME_STAFFING_REGISTERED`), registered only | Service professional | No (TCS) | 18% | Yes | 999800 |
| Packers and movers within the city (`RELOCATION_REGISTERED`), registered only | Service professional | No (TCS) | 18% | Yes | 996791 |
| Photography at home: events, portraits (`PHOTOGRAPHY_REGISTERED`), registered only | Service professional | No (TCS) | 18% | Yes | 998383 |
| Yoga trainer at home (`FITNESS_WELLNESS_REGISTERED`), registered only | Service professional | No (TCS) | 5% | No | 999723 |
| Construction and masonry, unregistered (`CONSTRUCTION_VIA_ECO`) | Service professional | Yes | 18% | No | 995457 |
| Construction and masonry, registered (`CONSTRUCTION_REGISTERED`) | Service professional | No (TCS) | 18% | Yes | 995457 |
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

## Families added on 4 October 2026

Professionals now set their own price for each service, and an admin approves it. The catalogue gained the families below. Each row is seeded with the same effective date (22 September 2025) and the same `NeedsAdviserConfirmation` flag as the rows above. Questions 1–22 apply to these families too, especially 2, 3, 7, 11 and 17.

23. **Unregistered professionals outside s.9(5): the general case.** Car wash, home staffing, packers and movers, photography and yoga are treated as **not** notified under s.9(5), so only a registered professional's supply is computed. Most of these professionals will be unregistered and below the threshold. Is their supply through the platform exempt from compulsory registration under Notification 65/2017-Central Tax, and so carries no GST? If yes, what does the customer receive, and does the platform collect TCS? This is question 3 again, for five more families. Until it is answered, those families can be booked only with registered professionals.
24. **Car wash.** A car is washed at the customer's parking. We have treated this as servicing a vehicle, not housekeeping of the home, so it is outside s.9(5). Is that right? We use SAC 998714 (maintenance and repair of transport machinery and equipment), which we believe covers washing and polishing of motor vehicles, at 18% with ITC. Please confirm the SAC. If car washing is housekeeping, we will add a `CAR_CARE_VIA_ECO` row.
25. **Home staffing: housekeeping, labour supply or employment.** A cook, house help, nanny or driver is booked by the hour or by the month (one visit a day). Please advise on each of these:
    - (a) Is a monthly engagement a supply by the worker, or employment by the household (Schedule III, outside GST)? Does the platform's role change that?
    - (b) Is hourly house help (sweeping, mopping, dishes, laundry) "housekeeping" under s.9(5), even though cooks, nannies and drivers are not? If so, should house help move to its own family with a `_VIA_ECO` row?
    - (c) We use SAC 999800 (domestic services) at the 18% residual rate. Should it be 9985 (employment and labour supply, for example 998519) instead, especially if the platform is seen as the one supplying the worker?
26. **Packers and movers.** These professionals supply packing, loading, transport within the city and unloading. We treat this as a composite supply with transport of goods as the principal supply. Please advise on each of these:
    - (a) When the mover issues a consignment note, they are a goods transport agency (GTA). Is a GTA's supply to an unregistered individual exempt (Notification 12/2017-CT(Rate), the entry for GTA supplies to unregistered persons)? If so, the customer pays no GST and the row must become an explicit 0%.
    - (b) Is transport by a mover who is not a GTA exempt (transport of goods by road other than GTA)?
    - (c) For a taxable supply, we chose 18% with ITC, the GTA forward-charge option from 22 September 2025, under SAC 996791. Should it be 5% without ITC? Should it be 996511 (road transport of goods, including household furniture), or 998540 (packaging) for packing done alone?
    - (d) The survey visit is a separate booking at ₹199. Is it a separate supply?
27. **Photography.** Events and portrait sessions at home. We use SAC 998383 (event photography) at 18%, with portrait sessions strictly 998381. One SAC per family is used today. Must each invoice carry the session's own code? Are edited photos delivered digitally still part of the same service supply?
28. **Yoga trainer: well-being or coaching.** We treat a personal yoga trainer at home as "physical well-being" (SAC 999723), which we believe moved to 5% without ITC on 22 September 2025 with gyms, yoga centres and salons. If it is commercial coaching instead (9992, 18%), the rate is wrong. Please cite the notification entry. Does the exemption for yoga by charitable entities ever apply to an individual trainer? (We assume not.)
29. **Construction and masonry under s.9(5).** Masonry, tiling and small civil repairs at the customer's home are treated as housekeeping, like plumbing and carpentry, so both categories exist. Please advise on each of these:
    - (a) Is that right? Or is any masonry or tiling "construction" outside the housekeeping entry?
    - (b) Cement and tiles are charged from the rate card. Does that make the job a works contract (s.2(119)), and does that change the rate, the SAC or the s.9(5) position (as in question 8 for painting)?
    - (c) We use SAC 995457 (masonry) at family level, with tiling strictly 995474. Is a single family-level code acceptable?
    - (d) Is the inspection visit (₹299) a separate supply?
30. **Appliance families widened.** Laptop and computer repair and mobile phone repair joined `APPLIANCE_REPAIR` (SAC 998715). Their own codes are 998713 (computers and peripherals) and 998716 (telecommunication equipment), and repairing a phone is not obviously "housekeeping". Should these move to their own registered-only family? TV, refrigerator, microwave, geyser, chimney and hob repair stay under 998715. Disinfection joined `PEST_CONTROL` (998531, disinfecting).
31. **Per-hour and per-month prices.** Staffing and yoga are priced per hour or per month, and monthly is a daily visit that we book one at a time. Is a monthly engagement one supply with a single time of supply, or a continuous supply of services (s.31(5))? Which invoice is due when?

## Non-tax items raised alongside (founder, not the adviser)

- Gig-worker welfare contribution under the labour codes.
- Insurance for damage in customers' homes.
