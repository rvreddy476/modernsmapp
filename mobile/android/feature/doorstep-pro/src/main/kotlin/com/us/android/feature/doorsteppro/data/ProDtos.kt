package com.us.android.feature.doorsteppro.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject

/*
 * doorstep-service's PROFESSIONAL wire shapes (`/v1/doorstep/pro/…`) —
 * contracts/doorstep/openapi.yaml, pinned by the golden fixtures
 * doorstep-service writes from its real handlers (internal/http/testdata/
 * contracts/pro_*.json, copied byte for byte into src/test/resources/contracts).
 *
 * THE CONTRACT EMITS EVERY KEY: optional values are an explicit `null`, never
 * left out. So every field the OpenAPI schema lists as `required` has NO
 * default here — a key the server drops fails the strict fixture test (and
 * the decode) rather than quietly becoming an empty string. Defaults exist
 * only for keys the schema does not require.
 *
 * REQUEST bodies go through the platform Json (encodeDefaults off,
 * explicitNulls off): a property left at its default — every optional one is
 * `null` — is OMITTED, so the server's strict decoder never sees a key it did
 * not ask for. Required request fields therefore carry no default.
 *
 * Money is integer paise, always.
 */

// ── Shared shapes ──────────────────────────────────────────────────────────

/** `{items: [...]}` — every list route without a cursor. */
@Serializable
data class ProListDto<T>(
    @SerialName("items") val items: List<T>,
)

/** `{items, next_cursor}` — the jobs list. */
@Serializable
data class ProJobPageDto(
    @SerialName("items") val items: List<ProJobDto>,
    @SerialName("next_cursor") val nextCursor: String?,
)

// ── Profile and onboarding (A2, golden) ─────────────────────────────────────

/** `Professional` — pro_apply_201, pro_me_get_200, pro_me_patch_200. */
@Serializable
data class ProfessionalDto(
    @SerialName("id") val id: String,
    @SerialName("user_id") val userId: String,
    /** draft | pending_verification | approved | suspended | rejected | blocked */
    @SerialName("status") val status: String,
    @SerialName("display_name") val displayName: String,
    @SerialName("city_code") val cityCode: String,
    /** female | male | other — from DigiLocker Aadhaar only, never typed. */
    @SerialName("gender") val gender: String?,
    @SerialName("photo_media_id") val photoMediaId: String?,
    @SerialName("rating_avg") val ratingAvg: Double?,
    @SerialName("rating_count") val ratingCount: Int,
    @SerialName("jobs_completed") val jobsCompleted: Int,
    @SerialName("max_jobs_per_day") val maxJobsPerDay: Int,
    @SerialName("created_at") val createdAt: String,
    @SerialName("updated_at") val updatedAt: String,
)

/** `ProReadiness` — pure MissingSteps on the server. */
@Serializable
data class ProReadinessDto(
    @SerialName("status") val status: String,
    @SerialName("missing_steps") val missingSteps: List<String>,
    @SerialName("completed_steps") val completedSteps: List<String>,
    /** Not in the schema's required list: optional steps (PAN). */
    @SerialName("recommended_steps") val recommendedSteps: List<String> = emptyList(),
    @SerialName("can_go_on_duty") val canGoOnDuty: Boolean,
)

@Serializable
data class DigiLockerStartDto(
    @SerialName("authorization_url") val authorizationUrl: String,
    @SerialName("state") val state: String,
)

/** `KycCheck` — the selfie face match result. */
@Serializable
data class KycCheckDto(
    /** digilocker_aadhaar | selfie_face_match | pan | bank */
    @SerialName("kind") val kind: String,
    /** pending | passed | failed | expired */
    @SerialName("status") val status: String,
    @SerialName("score") val score: Double?,
    @SerialName("verified_at") val verifiedAt: String?,
)

/** `Skill` — the skill catalogue. */
@Serializable
data class SkillDto(
    @SerialName("code") val code: String,
    @SerialName("name") val name: String,
    @SerialName("description") val description: String,
    /** true: verified only through an admin-approved trade certificate. */
    @SerialName("requires_certificate") val requiresCertificate: Boolean,
)

/** `ProSkill` — my declared skills with their verification. */
@Serializable
data class ProSkillDto(
    @SerialName("skill_code") val skillCode: String,
    /** pending | verified | revoked */
    @SerialName("status") val status: String,
    @SerialName("verified_at") val verifiedAt: String?,
)

/** `ProDocument` — a police or trade certificate under review. */
@Serializable
data class ProDocumentDto(
    @SerialName("id") val id: String,
    @SerialName("pro_id") val proId: String,
    /** police_certificate | trade_certificate | selfie | aadhaar | pan | other */
    @SerialName("kind") val kind: String,
    @SerialName("skill_code") val skillCode: String?,
    @SerialName("media_id") val mediaId: String,
    /** pending | approved | rejected */
    @SerialName("status") val status: String,
    @SerialName("issued_on") val issuedOn: String?,
    @SerialName("expires_on") val expiresOn: String?,
    @SerialName("reason") val reason: String?,
    @SerialName("created_at") val createdAt: String,
)

/** `ProArea` — the saved service area. */
@Serializable
data class ProAreaDto(
    @SerialName("zone_ids") val zoneIds: List<String>,
    @SerialName("home_lat") val homeLat: Double,
    @SerialName("home_lng") val homeLng: Double,
    @SerialName("radius_m") val radiusM: Int,
)

/** `WeeklyHours` — read, written and echoed with the same shape. */
@Serializable
data class WeeklyHoursDto(
    @SerialName("items") val items: List<HoursWindowDto>,
)

@Serializable
data class HoursWindowDto(
    /** 0 = Sunday … 6 = Saturday. */
    @SerialName("weekday") val weekday: Int,
    /** `HH:MM`, India time. */
    @SerialName("start") val start: String,
    @SerialName("end") val end: String,
)

/** `DayOff`. The schema requires only the date; the server always emits the reason (null when none). */
@Serializable
data class DayOffDto(
    @SerialName("date") val date: String,
    @SerialName("reason") val reason: String? = null,
)

/** `PayoutAccount` — masked: the number typed is never echoed. */
@Serializable
data class PayoutAccountDto(
    @SerialName("account_holder") val accountHolder: String,
    @SerialName("account_last4") val accountLast4: String,
    @SerialName("ifsc") val ifsc: String,
    /** pending | verified | failed */
    @SerialName("status") val status: String,
)

/** `Serviceability` — reused to find the zone of the home point. */
@Serializable
data class ServiceabilityDto(
    @SerialName("serviceable") val serviceable: Boolean,
    @SerialName("city") val city: CityRefDto?,
    @SerialName("zone") val zone: ZoneRefDto?,
    @SerialName("reason") val reason: String?,
)

@Serializable
data class CityRefDto(
    @SerialName("code") val code: String,
    @SerialName("name") val name: String,
)

@Serializable
data class ZoneRefDto(
    @SerialName("id") val id: String,
    @SerialName("name") val name: String,
)

// ── Duty, offers, jobs (A4) ─────────────────────────────────────────────────

/** `DutyState`. */
@Serializable
data class DutyStateDto(
    @SerialName("on_duty") val onDuty: Boolean,
    @SerialName("since") val since: String?,
)

/** `Offer` — locality only before acceptance, never the address. */
@Serializable
data class OfferDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String,
    @SerialName("service_name") val serviceName: String,
    @SerialName("category_slug") val categorySlug: String,
    @SerialName("locality") val locality: String,
    @SerialName("distance_m") val distanceM: Int,
    @SerialName("slot_start") val slotStart: String,
    @SerialName("slot_end") val slotEnd: String,
    @SerialName("earning_estimate_paise") val earningEstimatePaise: Long,
    @SerialName("expires_at") val expiresAt: String,
)

/** `ProJob` — the address and the customer's first name only from acceptance to completion + 2 h. */
@Serializable
data class ProJobDto(
    @SerialName("booking_id") val bookingId: String,
    @SerialName("status") val status: String,
    @SerialName("service_name") val serviceName: String,
    @SerialName("category_slug") val categorySlug: String,
    @SerialName("slot_start") val slotStart: String,
    @SerialName("slot_end") val slotEnd: String,
    @SerialName("items") val items: List<QuoteLineDto>,
    @SerialName("locality") val locality: String,
    @SerialName("address") val address: AddressDto?,
    @SerialName("customer_first_name") val customerFirstName: String?,
    @SerialName("chat_open") val chatOpen: Boolean,
    @SerialName("photos_required") val photosRequired: PhotosRequiredDto,
    @SerialName("earning_estimate_paise") val earningEstimatePaise: Long,
)

@Serializable
data class PhotosRequiredDto(
    @SerialName("before") val before: Int,
    @SerialName("after") val after: Int,
    @SerialName("kit_seal") val kitSeal: Int,
)

/** `QuoteLine` — what the customer booked. */
@Serializable
data class QuoteLineDto(
    /** option | addon */
    @SerialName("kind") val kind: String,
    @SerialName("ref_id") val refId: String,
    @SerialName("price_id") val priceId: String,
    @SerialName("name") val name: String,
    @SerialName("quantity") val quantity: Int,
    @SerialName("unit_price_paise") val unitPricePaise: Long,
    @SerialName("line_total_paise") val lineTotalPaise: Long,
    @SerialName("taxable_paise") val taxablePaise: Long,
    @SerialName("tax_paise") val taxPaise: Long,
    @SerialName("tax_rate_bps") val taxRateBps: Int,
    @SerialName("gst_category") val gstCategory: String,
    @SerialName("sac") val sac: String,
)

/** `Address` — the customer's, on an accepted job. */
@Serializable
data class AddressDto(
    @SerialName("id") val id: String,
    @SerialName("label") val label: String,
    @SerialName("line1") val line1: String,
    @SerialName("line2") val line2: String?,
    @SerialName("landmark") val landmark: String?,
    @SerialName("locality") val locality: String,
    @SerialName("city_code") val cityCode: String,
    @SerialName("pincode") val pincode: String,
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
    @SerialName("zone_id") val zoneId: String,
    @SerialName("is_default") val isDefault: Boolean,
    @SerialName("created_at") val createdAt: String,
)

// ── The visit (A5) ──────────────────────────────────────────────────────────

/** `Photo`. */
@Serializable
data class PhotoDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String,
    /** before | after | kit_seal | extra_evidence */
    @SerialName("phase") val phase: String,
    @SerialName("media_id") val mediaId: String,
    @SerialName("created_at") val createdAt: String,
)

/** `Extra`. */
@Serializable
data class ExtraDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String,
    /** rate_card | addon */
    @SerialName("kind") val kind: String,
    @SerialName("rate_card_id") val rateCardId: String?,
    @SerialName("addon_id") val addonId: String?,
    @SerialName("name") val name: String,
    @SerialName("quantity") val quantity: Int,
    @SerialName("unit_price_paise") val unitPricePaise: Long,
    @SerialName("total_paise") val totalPaise: Long,
    /** proposed | approved | declined | withdrawn | billed */
    @SerialName("status") val status: String,
    @SerialName("created_at") val createdAt: String,
)

/**
 * PROPOSED — NOT IN openapi.yaml (2026-10-04). What a professional may propose
 * as an extra on this job: the category's rate card, or the service's
 * catalogue add-ons for salon. `GET /v1/doorstep/pro/jobs/{id}/extras/options`.
 * Without it the app cannot know a rate_card_id to send; see the report.
 */
@Serializable
data class ExtraOptionDto(
    /** rate_card | addon */
    @SerialName("kind") val kind: String,
    @SerialName("rate_card_id") val rateCardId: String?,
    @SerialName("addon_id") val addonId: String?,
    @SerialName("name") val name: String,
    @SerialName("description") val description: String?,
    @SerialName("unit_price_paise") val unitPricePaise: Long,
    @SerialName("max_quantity") val maxQuantity: Int,
)

/** `Incident` — SOS and unsafe exit. */
@Serializable
data class IncidentDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String?,
    @SerialName("raised_by_kind") val raisedByKind: String,
    @SerialName("kind") val kind: String,
    @SerialName("severity") val severity: String,
    @SerialName("status") val status: String,
    @SerialName("description") val description: String?,
    @SerialName("pro_auto_suspended") val proAutoSuspended: Boolean,
    @SerialName("created_at") val createdAt: String,
)

/** `Rating`. */
@Serializable
data class RatingDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String,
    @SerialName("rater_kind") val raterKind: String,
    @SerialName("stars") val stars: Int,
    @SerialName("tags") val tags: List<String>,
    @SerialName("comment") val comment: String?,
    @SerialName("hidden") val hidden: Boolean,
    @SerialName("created_at") val createdAt: String,
)

/** `Message`. */
@Serializable
data class MessageDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String,
    /** customer | pro | system */
    @SerialName("sender_kind") val senderKind: String,
    @SerialName("body") val body: String,
    @SerialName("created_at") val createdAt: String,
    @SerialName("read_at") val readAt: String?,
)

/** `MessagePage`. */
@Serializable
data class MessagePageDto(
    @SerialName("items") val items: List<MessageDto>,
    @SerialName("next_cursor") val nextCursor: String?,
    /** false outside acceptance .. completion + 2 h. */
    @SerialName("open") val open: Boolean,
)

/** `Earnings` — computed; payouts are OFF. */
@Serializable
data class EarningsDto(
    @SerialName("total_paise") val totalPaise: Long,
    @SerialName("lines") val lines: List<EarningLineDto>,
)

@Serializable
data class EarningLineDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String?,
    /** job | extras | incentive | penalty | adjustment | commission */
    @SerialName("kind") val kind: String,
    @SerialName("amount_paise") val amountPaise: Long,
    @SerialName("created_at") val createdAt: String,
)

/** `RealtimeToken` — for doorstep.pro.<user_id> and accepted bookings. */
@Serializable
data class RealtimeTokenDto(
    @SerialName("token") val token: String,
    @SerialName("topics") val topics: List<String>,
    @SerialName("expires_at") val expiresAt: String,
)

// ── Request bodies ──────────────────────────────────────────────────────────

/** `ProApplyInput`. No gender: the server refuses the key (pro_apply_400_gender_field). */
@Serializable
data class ProApplyRequest(
    @SerialName("display_name") val displayName: String,
    @SerialName("city_code") val cityCode: String,
    @SerialName("category_ids") val categoryIds: List<String>? = null,
)

@Serializable
data class ProPatchRequest(
    @SerialName("display_name") val displayName: String? = null,
    @SerialName("photo_media_id") val photoMediaId: String? = null,
)

@Serializable
data class DigiLockerCallbackRequest(
    @SerialName("code") val code: String,
    @SerialName("state") val state: String,
)

@Serializable
data class MediaRequest(
    @SerialName("media_id") val mediaId: String,
)

@Serializable
data class SkillsRequest(
    @SerialName("skill_codes") val skillCodes: List<String>,
)

/** A police or trade certificate. */
@Serializable
data class CertificateRequest(
    @SerialName("media_id") val mediaId: String,
    @SerialName("issued_on") val issuedOn: String,
    @SerialName("certificate_number") val certificateNumber: String? = null,
)

@Serializable
data class ProAreaRequest(
    @SerialName("zone_ids") val zoneIds: List<String>,
    @SerialName("home_lat") val homeLat: Double,
    @SerialName("home_lng") val homeLng: Double,
    @SerialName("radius_m") val radiusM: Int,
)

@Serializable
data class BankRequest(
    @SerialName("account_holder") val accountHolder: String,
    @SerialName("account_number") val accountNumber: String,
    @SerialName("ifsc") val ifsc: String,
)

@Serializable
data class AgreementRequest(
    @SerialName("version") val version: String,
)

@Serializable
data class PanRequest(
    @SerialName("pan") val pan: String,
)

/** `LocationInput` — duty on, pings and arrived. */
@Serializable
data class LocationRequest(
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
    @SerialName("accuracy_m") val accuracyM: Double? = null,
)

@Serializable
data class ServiceabilityRequest(
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
)

/** reason: too_far | busy | not_my_skill | other. */
@Serializable
data class DeclineRequest(
    @SerialName("reason") val reason: String? = null,
)

@Serializable
data class PhotoRequest(
    @SerialName("phase") val phase: String,
    @SerialName("media_id") val mediaId: String,
    @SerialName("lat") val lat: Double? = null,
    @SerialName("lng") val lng: Double? = null,
)

@Serializable
data class OtpRequest(
    @SerialName("otp") val otp: String,
)

/** `ExtraInput` — exactly one of [rateCardId] / [addonId]. Never free text. */
@Serializable
data class ExtraRequest(
    @SerialName("quantity") val quantity: Int,
    @SerialName("rate_card_id") val rateCardId: String? = null,
    @SerialName("addon_id") val addonId: String? = null,
    @SerialName("evidence_media_id") val evidenceMediaId: String? = null,
)

@Serializable
data class CancelRequest(
    @SerialName("reason") val reason: String,
)

@Serializable
data class SosRequest(
    @SerialName("lat") val lat: Double? = null,
    @SerialName("lng") val lng: Double? = null,
    @SerialName("note") val note: String? = null,
)

@Serializable
data class RatingRequest(
    @SerialName("stars") val stars: Int,
    @SerialName("tags") val tags: List<String>? = null,
    @SerialName("comment") val comment: String? = null,
)

@Serializable
data class MessageRequest(
    @SerialName("body") val body: String,
)

// ── Errors ─────────────────────────────────────────────────────────────────

/** The error envelope, read from a 4xx/5xx body. Clients branch on [ProErrorBodyDto.code], never the message. */
@Serializable
data class ProErrorEnvelopeDto(
    @SerialName("error") val error: ProErrorBodyDto,
    @SerialName("meta") val meta: ProMetaDto? = null,
)

@Serializable
data class ProErrorBodyDto(
    @SerialName("code") val code: String,
    @SerialName("message") val message: String,
    @SerialName("details") val details: JsonObject? = null,
)

@Serializable
data class ProMetaDto(@SerialName("request_id") val requestId: String? = null)
