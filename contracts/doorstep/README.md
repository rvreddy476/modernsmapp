# Doorstep contracts

Doorstep is home services in the Urban Company style: a fixed-price catalogue booked into a time slot, done by gig professionals. Pilot city: Hyderabad (`HYD`, GST state 36). These files are the binding wire contract for every lane. Names are pinned in the programme's "Doorstep" plan section and its pinned-names note.

| File | What it pins |
|---|---|
| `openapi.yaml` | Every customer route, every `/pro` route, every `/internal/admin` route, and the background-check webhook (142 paths, 170 operations). It also holds the booking state enum and transitions (`x-doorstep-booking-states`), the stable error codes (`x-doorstep-error-codes`), the 19 admin permissions (`x-doorstep-permissions`) and the payments references (`x-doorstep-payments`). Each route carries `x-lane` (the lane that builds it) and, on admin routes, `x-permission`. |
| `asyncapi.yaml` | The Kafka topic `doorstep.events` (38 event types, every payload carries `customer_user_id` and/or `pro_user_id`), the realtime topics `doorstep.booking.<id>`, `doorstep.pro.<user_id>` and `doorstep.admin.live`, and the push-type registry (`x-push-types`): 19 Momentum types (`doorstep.*`) and 18 `doorstep_pro` types (`doorstep.pro.*`), each mapped to its source event. |

## Conventions

- **Envelope.** Every response is `{"data": ..., "meta": {"request_id": ...}}` or `{"error": {"code", "message", "details"?}, "meta": ...}`. Schemas describe `data`.
- **Money** is integer paise in fields ending `_paise`. Customer prices include GST. A quote shows the split as `taxable_paise` + `tax_paise` per line and in total.
- **Nulls are explicit.** Optional values are emitted as `null`, never left out, so strict decoders see every key.
- **Error codes** are stable UPPER_SNAKE strings. Clients branch on `error.code`, never on `message`.
- **Identity.** The gateway is the only JWT verifier. User routes trust `X-User-Id` behind `X-Internal-Service-Key`. The `/internal/admin` family accepts only admin-service tokens: `aud doorstep`, `iss admin-service`, `scope` = the one `doorstep:*` permission, `act` = the admin's user id, expiry of 60 s or less. The internal key is neither needed nor accepted as proof there.

## Golden fixtures

doorstep-service produces these through its real handlers, in `Architecture/services/doorstep-service/internal/http/testdata/contracts/`. Web and Android copy them byte for byte. The ids are the dev seed's deterministic ids (`devseed.ID`), so fixture ids match a seeded dev database. The clock is fixed at `2026-10-04T06:30:00Z` and the request id is `fixture`. To regenerate after a deliberate shape change, run `UPDATE_CONTRACT_FIXTURES=1 go test ./internal/http/`, then tell the web and Android lanes.

| Fixture | Route |
|---|---|
| `catalogue_get_200.json`, `catalogue_get_404_city.json` | `GET /v1/doorstep/catalogue?city=` |
| `category_get_200.json` | `GET /v1/doorstep/categories/{slug}?city=` |
| `service_get_200.json` | `GET /v1/doorstep/services/{id}?city=` (women's salon facial: a required pick-one mask group plus optional add-ons) |
| `serviceability_in_200.json`, `serviceability_out_200.json` | `POST /v1/doorstep/serviceability` |
| `quote_post_201.json` | `POST /v1/doorstep/quotes`: kitchen deep cleaning, `HOME_CLEANING_VIA_ECO` at 18%, computed by shared/gst |
| `quote_post_201_salon.json` | Same route, salon: `BEAUTY_SALON_REGISTERED` at 5%, estimated |
| `quote_post_422_addon_min.json`, `quote_post_422_addon_max.json`, `quote_post_422_outside_area.json`, `quote_post_422_option_invalid.json`, `quote_post_422_quantity.json` | Quote refusals |
| `admin_cities_list_200.json`, `admin_category_post_201.json`, `admin_zone_post_201.json`, `admin_zone_post_422.json`, `admin_price_post_201.json`, `admin_price_post_409.json`, `admin_token_403_scope.json` | Admin samples |
| `pro_apply_201.json`, `pro_apply_409_exists.json`, `pro_apply_400_gender_field.json`, `pro_me_get_200.json`, `pro_me_get_404.json`, `pro_me_patch_200.json`, `pro_me_patch_409_blocked.json` | `POST /pro/apply`, `GET`/`PATCH /pro/me` (A2; a body naming `gender` is refused: gender comes from DigiLocker only) |
| `pro_readiness_200_draft.json`, `pro_readiness_200_pending_review.json`, `pro_readiness_200_approved.json` | `GET /pro/readiness` |
| `pro_digilocker_start_200.json`, `pro_digilocker_start_503.json`, `pro_digilocker_callback_200.json`, `pro_digilocker_callback_400_state.json` | DigiLocker (PKCE, single-use state bound to the professional) |
| `pro_selfie_200.json`, `pro_selfie_200_pending.json`, `pro_selfie_422_failed.json`, `pro_selfie_422_no_aadhaar.json` | `POST /pro/selfie` |
| `pro_skills_list_200.json`, `pro_skills_put_200.json`, `pro_skills_put_400_unknown.json`, `pro_skills_put_403_gender.json`, `pro_trade_certificate_201.json`, `pro_trade_certificate_400_not_required.json` | skills and trade certificates |
| `pro_area_put_200.json`, `pro_area_put_400_radius.json`, `pro_area_put_400_zone.json`, `pro_hours_get_200.json`, `pro_hours_put_200.json`, `pro_hours_put_400_overlap.json`, `pro_days_off_list_200.json`, `pro_days_off_post_201.json`, `pro_days_off_post_400_past.json`, `pro_days_off_post_409_job.json`, `pro_days_off_delete_404.json` | area, weekly hours, days off |
| `pro_bank_put_200.json`, `pro_bank_put_400_ifsc.json`, `pro_bank_put_503_pii.json`, `pro_police_certificate_201.json`, `pro_police_certificate_400_old.json`, `pro_police_certificate_409_pending.json`, `pro_agreement_200.json`, `pro_agreement_400_version.json`, `pro_pan_put_200.json`, `pro_pan_put_400.json` | bank (masked), police certificate, agreement, PAN |
| `webhook_background_check_404.json` | background-check vendor webhook (no vendor enabled) |
| `admin_professionals_list_200.json`, `admin_professional_get_200.json`, `admin_professional_get_404.json`, `admin_professional_approve_200.json`, `admin_professional_approve_422_incomplete.json`, `admin_professional_approve_403_gender.json`, `admin_professional_reject_200.json`, `admin_professional_reject_400_reason.json`, `admin_professional_suspend_200.json`, `admin_professional_suspend_400_reason.json`, `admin_professional_suspend_409_transition.json`, `admin_professional_reinstate_200.json`, `admin_professional_block_200.json`, `admin_skill_verify_200_revoke.json`, `admin_skill_verify_422_certificate.json` | admin professional review (A2) |
| `admin_documents_list_200.json`, `admin_document_decide_200_police.json`, `admin_document_decide_400_reason.json`, `admin_document_decide_409_decided.json`, `admin_document_view_403_scope.json`, `admin_document_view_404.json`, `admin_document_view_503.json` | document review and the audited image view (the 200 is image bytes, no fixture) |
| `address_post_201.json`, `address_post_422_outside_area.json`, `addresses_get_200.json` | addresses (A3; street lines sealed at rest, serviceability on save) |
| `slots_get_200.json`, `slots_get_409_outstanding.json` | `GET /slots?quote_id=&address_id=` (calendar-derived; Sunday has no hours in the fixture world, Monday shows one professional busy 10:00-13:30) |
| `booking_post_201.json`, `booking_post_400_idempotency_key.json`, `booking_post_409_slot_taken.json`, `booking_post_409_outstanding.json`, `booking_post_410_quote_expired.json`, `booking_post_422_slot_unavailable.json` | `POST /bookings` (Idempotency-Key; the 201 carries the Razorpay-shaped `checkout`) |
| `booking_get_200_pending_payment.json`, `booking_get_200.json`, `booking_get_404.json`, `bookings_get_200.json` | booking detail (with `status_history`, `end_otp` null, `photos` []) and list |
| `booking_payment_intent_post_200.json`, `booking_payment_intent_410_hold_expired.json`, `booking_payment_get_200_pending.json`, `booking_payment_get_200.json`, `booking_payment_get_200_refund.json`, `booking_payment_stub_confirm_404.json` | payments: intent, the paid source, the dev stub confirm refused outside development |
| `cancel_preview_get_200.json`, `booking_cancel_post_200.json`, `booking_reschedule_post_200.json`, `booking_reschedule_post_409.json` | cancel and reschedule |
| `admin_bookings_list_200.json`, `admin_booking_get_200.json`, `admin_booking_cancel_200.json`, `admin_booking_refund_201.json`, `admin_booking_refund_422_exceeds.json`, `admin_stats_200.json` | admin booking pages (A3; never an OTP) |
| `quote_post_400_pro_required.json`, `quote_post_422_price_unavailable.json` | B1: a quote needs `pro_id`; a selection the professional has no approved price for is `DOORSTEP_PRICE_UNAVAILABLE` (`details.missing`) |
| `service_professionals_get_200.json`, `service_professionals_get_200_asap.json`, `service_professionals_get_200_asap_none.json`, `service_professionals_get_400_address.json` | B1: `GET /services/{id}/professionals` (scheduled, ASAP, ASAP with nobody plus `scheduled_alternatives`) |
| `booking_post_201_asap.json` | B1: `POST /bookings` with `pro_id` from the quote and `asap: true` (the block runs from now) |
| `booking_get_200_pro_unavailable.json`, `booking_get_200_pending_change.json`, `cancel_preview_get_200_pro_unavailable.json` | B1: a booking whose professional is gone (`choice_deadline`, `unavailable_cause`), one with a dearer change waiting for payment (`pending_change`), and its full-refund cancel preview |
| `booking_professionals_get_200.json`, `booking_professionals_get_200_asap.json`, `booking_professionals_get_409.json` | B1: `GET /bookings/{id}/professionals` (each card with `difference_paise`) |
| `booking_change_professional_post_200_refund.json`, `booking_change_professional_post_200_charge.json`, `booking_change_professional_422_excluded.json`, `booking_change_professional_409_window.json` | B1: `POST /bookings/{id}/change-professional` (cheaper: applied and refunded; dearer: `pending_payment` with a `doorstep_extras` intent) |
| `pro_prices_get_200.json`, `pro_price_post_201.json`, `pro_price_post_400.json`, `pro_price_post_403_skill.json`, `pro_price_post_409_unchanged.json`, `pro_price_withdraw_200.json`, `pro_price_withdraw_409.json`, `pro_same_day_put_200.json` | B1: the professional's own prices (always pending until an admin approves) and the same-day opt-in |
| `admin_pro_prices_get_200.json`, `admin_pro_price_approve_200.json`, `admin_pro_price_approve_409.json`, `admin_pro_price_reject_200.json`, `admin_pro_price_reject_400_reason.json`, `admin_pro_price_403_scope.json` | B1: the price review queue (`doorstep:prices.review`, audited) |

## Completion update (5 Oct 2026)

A1–A6 and the B1 pick-a-professional model are implemented. The historical
status sections below describe the original lane boundaries, not pending work.
See `docs/handoff/doorstep-completion-report.md` for verification and launch
restrictions and `docs/handoff/doorstep-dev-readiness.md` for founder-run setup.

Additional handler-generated contract families:

| Fixtures | Coverage |
| --- | --- |
| `pro_en_route_200`, `pro_arrived_200`, `pro_photo_201`, `pro_start_200`, `pro_start_422_photos`, `pro_start_400_otp`, `pro_finish_200`, `pro_complete_200`, `pro_complete_409_extras`, `pro_no_show_200` | Geo arrival, private evidence, OTP-gated visit transitions |
| `extras_get_200`, `extra_approve_post_200`, `extra_decline_post_200`, `extras_bill_get_200`, `extras_payment_intent_post_200`, `outstanding_get_200`, `pro_extra_options_get_200`, `pro_extra_post_201`, `pro_extras_get_200` | Catalogue/rate-card extras, decisions, payment and outstanding balances |
| `messages_get_200`, `message_post_201`, `pro_messages_get_200`, `pro_message_post_201` | Participant-only visit conversations |
| `rating_post_201`, `pro_rating_201`, `rework_post_201`, `rework_get_200`, `pro_earnings_get_200` | Ratings, zero-price rework and compute-only earnings |
| `share_post_201`, `sos_post_201`, `pro_sos_201`, `pro_unsafe_exit_201`, `trusted_contact_get_200`, `trusted_contact_get_200_empty`, `trusted_contact_put_200` | Capability links, safety incidents and sealed trusted contacts |
| `ticket_post_201`, `ticket_get_200`, `tickets_get_200` | Customer support with request bodies and status |
| `admin_incidents_get_200`, `admin_incident_ack_200`, `admin_incident_resolve_200`, `admin_incidents_403_scope`, `admin_tickets_get_200`, `admin_ticket_status_200`, `admin_ratings_get_200`, `admin_rating_hide_200`, `admin_settlements_get_200` | Audited aftercare queues and decisions |
| `admin_tax_registration_get_200`, `admin_tax_registration_post_200`, `admin_tax_registration_403_scope`, `admin_tax_registration_400_unverified` | Step-up manual GST registration review; no automatic approval |

Names above omit `.json`. JSON bodies are captured by the actual HTTP handlers,
then copied byte-identical to the relevant client test directories. Binary
photo reads and 204 withdrawal/read/revocation responses are tested without
inventing JSON fixtures.

## Status (A1, historical)

**Built and tested:**

- the catalogue routes, serviceability and quotes
- every admin-internal catalogue and config route: cities, zones, categories, skills, services, options, add-on groups, add-ons, prices, rate cards, slot configs, cancellation rules, commission rules, and the audit log
- migration 001 with the whole `doorstep` schema
- the Hyderabad dev seed

**Contract only, built by later lanes:**

- addresses, slots, bookings, payments and refunds (A3): built; see the A3 fixtures above. A3 added `Booking.end_otp`, `Booking.photos`, `Booking.status_history` (`StatusStep`), `Extra.evidence_media_id`, the development-only `POST /bookings/{id}/payment/stub-confirm`, `AdminCancelInput.fee_paise`, the Idempotency-Key on the admin refund, `AdminBookingDetail.reserved_pro_id/needs_attention/attention_reason`, `AdminStats.bookings_needing_attention`, the error code `DOORSTEP_STUB_UNAVAILABLE` and the events `doorstep.booking.refund_failed` and `doorstep.booking.payment_attention` (ops only). The admin booking list, detail, cancel, refund and stats routes moved from A6 to A3.
- professional onboarding (A2): built; see the A2 fixtures above. A2 added two routes to this contract (`POST /pro/me/skills/{code}/certificate`, `GET /internal/admin/documents/{id}/view`), `Skill.requires_certificate`, `ProDocument.skill_code` and the kinds `trade_certificate` and `selfie`, and four error codes.
- dispatch, offers and realtime (A4)
- the visit, extras, ratings, rework, safety, chat and tickets (A5)
- the remaining admin routes (A2 and A6)

## Status (B1, 4 Oct 2026): professionals set prices, customers pick the professional

**Built and tested** (migration `005_doorstep_pro_pricing.sql`):

- Catalogue expansion: appliance repairs (TV, refrigerator, washing machine, microwave, geyser, chimney and hob, laptop/computer, mobile), car wash, disinfection, home staffing (hourly and monthly), packers and movers, photographer, makeup artist (women's beauty rules), yoga trainer and construction, each with a skill. `ServiceOption.unit` is `per_job`, `per_hour` or `per_month`. The city price is only `suggested_price_paise` now; the catalogue's starting prices are the lowest approved professional prices (`from_price_paise`, null when nobody prices it).
- Families `CAR_CARE`, `HOME_STAFFING`, `RELOCATION`, `PHOTOGRAPHY`, `FITNESS_WELLNESS` and `CONSTRUCTION` are seeded but hidden from customers until shared/gst maps them (`tax_category_pending`).
- Professional prices (`pro_service_prices`, effective-dated): submitted from `/pro/me/prices`, always `pending` until an admin approves under `doorstep:prices.review`, audited in the same transaction. Only an approved, live price is bookable; the database refuses a quote or booking line carrying anything else.
- The customer picks the professional: `GET /services/{id}/professionals`; the quote and booking carry `pro_id`; the hold is on that professional only; `asap` holds from now with a 3-minute offer.
- `pro_unavailable`: a decline, an expired offer, a give-back, a no-show, not on duty or an ops redispatch never reassigns silently. The customer picks another professional (the difference charged through a `doorstep_extras` pro_change bill, or refunded) or cancels for a full refund; no choice within 30 minutes is a full refund.
- Nothing approves itself (founder, 4 Oct): the selfie face match is advisory (the selfie stays pending for an admin), every declared skill is pending until an admin verifies it, a background-check vendor verdict is advisory, and a deferred database trigger refuses any approval or verification without an admin audit row by the same reviewer in the same transaction.

**Fixture changes the clients must copy:** every quote, booking, slots, catalogue and service fixture (units, suggested and from prices, `pro_id`, `asap`, `choice_deadline`, `unavailable_cause`, `pending_change`), `pro_selfie_200.json` (pending, not approved), `pro_skills_put_200.json` (pending), the readiness, DigiLocker, agreement and PAN fixtures, the A4 job fixtures, and the admin booking and document fixtures.
