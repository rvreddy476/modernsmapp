# Doorstep contracts

Doorstep is home services in the Urban Company style: a fixed-price catalogue booked into a time slot, done by gig professionals. Pilot city: Hyderabad (`HYD`, GST state 36). These files are the binding wire contract for every lane. Names are pinned in the programme's "Doorstep" plan section and its pinned-names note.

| File | What it pins |
|---|---|
| `openapi.yaml` | Every customer route, every `/pro` route, every `/internal/admin` route, and the background-check webhook (125 paths, 148 operations). It also holds the booking state enum and transitions (`x-doorstep-booking-states`), the stable error codes (`x-doorstep-error-codes`), the 18 admin permissions (`x-doorstep-permissions`) and the payments references (`x-doorstep-payments`). Each route carries `x-lane` (the lane that builds it) and, on admin routes, `x-permission`. |
| `asyncapi.yaml` | The Kafka topic `doorstep.events` (31 event types, every payload carries `customer_user_id` and/or `pro_user_id`), the realtime topics `doorstep.booking.<id>`, `doorstep.pro.<user_id>` and `doorstep.admin.live`, and the push-type registry (`x-push-types`): 17 Momentum types (`doorstep.*`) and 17 `doorstep_pro` types (`doorstep.pro.*`), each mapped to its source event. |

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

## Status (A1)

**Built and tested:**

- the catalogue routes, serviceability and quotes
- every admin-internal catalogue and config route: cities, zones, categories, skills, services, options, add-on groups, add-ons, prices, rate cards, slot configs, cancellation rules, commission rules, and the audit log
- migration 001 with the whole `doorstep` schema
- the Hyderabad dev seed

**Contract only, built by later lanes:**

- addresses, slots, bookings, payments and refunds (A3)
- professional onboarding (A2): built; see the A2 fixtures above. A2 added two routes to this contract (`POST /pro/me/skills/{code}/certificate`, `GET /internal/admin/documents/{id}/view`), `Skill.requires_certificate`, `ProDocument.skill_code` and the kinds `trade_certificate` and `selfie`, and four error codes.
- dispatch, offers and realtime (A4)
- the visit, extras, ratings, rework, safety, chat and tickets (A5)
- the remaining admin routes (A2 and A6)
