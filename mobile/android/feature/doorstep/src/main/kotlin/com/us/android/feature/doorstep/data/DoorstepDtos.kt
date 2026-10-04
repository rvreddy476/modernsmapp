package com.us.android.feature.doorstep.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject

/*
 * doorstep-service's customer wire shapes — contracts/doorstep/openapi.yaml,
 * pinned by the golden fixtures doorstep-service writes from its real handlers
 * (internal/http/testdata/contracts, copied byte for byte into
 * src/test/resources/contracts).
 *
 * THE CONTRACT EMITS EVERY KEY: optional values are an explicit `null`, never
 * left out. So every field the OpenAPI schema lists as `required` has NO
 * default here — a key the server drops fails the strict fixture test (and
 * the decode) rather than quietly becoming an empty string. Defaults exist
 * only for keys the schema does not require. Money is integer paise, always.
 */

// ── Catalogue (A1, golden) ─────────────────────────────────────────────────

@Serializable
data class CityRefDto(
    @SerialName("code") val code: String,
    @SerialName("name") val name: String,
)

/** `GET /v1/doorstep/catalogue?city=` — catalogue_get_200.json. */
@Serializable
data class CatalogueDto(
    @SerialName("city") val city: CityRefDto,
    @SerialName("categories") val categories: List<CategorySummaryDto>,
)

@Serializable
data class CategorySummaryDto(
    @SerialName("id") val id: String,
    @SerialName("slug") val slug: String,
    @SerialName("name") val name: String,
    @SerialName("description") val description: String,
    /** HOME_CLEANING | PEST_CONTROL | APPLIANCE_REPAIR | INSTALLATION_REPAIR | PAINTING | BEAUTY_SALON */
    @SerialName("family") val family: String,
    /** any | female_pros_only | male_pros_only */
    @SerialName("gender_rule") val genderRule: String,
    @SerialName("image_url") val imageUrl: String?,
    @SerialName("sort_order") val sortOrder: Int,
    @SerialName("service_count") val serviceCount: Int,
    @SerialName("starting_price_paise") val startingPricePaise: Long,
)

/** `GET /v1/doorstep/categories/{slug}?city=` — category_get_200.json. */
@Serializable
data class CategoryPageDto(
    @SerialName("city") val city: CityRefDto,
    @SerialName("category") val category: CategorySummaryDto,
    @SerialName("services") val services: List<ServiceSummaryDto>,
)

@Serializable
data class ServiceSummaryDto(
    @SerialName("id") val id: String,
    @SerialName("category_id") val categoryId: String,
    @SerialName("slug") val slug: String,
    @SerialName("name") val name: String,
    @SerialName("description") val description: String,
    @SerialName("duration_minutes") val durationMinutes: Int,
    @SerialName("image_url") val imageUrl: String?,
    @SerialName("starting_price_paise") val startingPricePaise: Long,
    @SerialName("starting_mrp_paise") val startingMrpPaise: Long?,
)

/** `GET /v1/doorstep/services/{id}?city=` — service_get_200.json. */
@Serializable
data class ServicePageDto(
    @SerialName("city") val city: CityRefDto,
    @SerialName("service") val service: ServiceDetailDto,
)

@Serializable
data class ServiceDetailDto(
    @SerialName("id") val id: String,
    @SerialName("category") val category: ServiceCategoryDto,
    @SerialName("slug") val slug: String,
    @SerialName("name") val name: String,
    @SerialName("description") val description: String,
    @SerialName("duration_minutes") val durationMinutes: Int,
    @SerialName("inclusions") val inclusions: List<String>,
    @SerialName("exclusions") val exclusions: List<String>,
    @SerialName("image_url") val imageUrl: String?,
    @SerialName("crew_size") val crewSize: Int,
    @SerialName("rework_days") val reworkDays: Int,
    @SerialName("min_before_photos") val minBeforePhotos: Int,
    @SerialName("min_after_photos") val minAfterPhotos: Int,
    @SerialName("options") val options: List<ServiceOptionDto>,
    @SerialName("addon_groups") val addonGroups: List<AddonGroupDto>,
)

@Serializable
data class ServiceCategoryDto(
    @SerialName("id") val id: String,
    @SerialName("slug") val slug: String,
    @SerialName("name") val name: String,
    @SerialName("family") val family: String,
    @SerialName("gender_rule") val genderRule: String,
    /** rate_card | catalogue_addons_only (salon) */
    @SerialName("extras_policy") val extrasPolicy: String,
)

@Serializable
data class ServiceOptionDto(
    @SerialName("id") val id: String,
    @SerialName("name") val name: String,
    @SerialName("description") val description: String,
    /** Duration of ONE unit. */
    @SerialName("duration_minutes") val durationMinutes: Int,
    @SerialName("max_quantity") val maxQuantity: Int,
    @SerialName("is_default") val isDefault: Boolean,
    /** GST-inclusive, per unit. */
    @SerialName("price_paise") val pricePaise: Long,
    @SerialName("mrp_paise") val mrpPaise: Long?,
)

@Serializable
data class AddonGroupDto(
    @SerialName("id") val id: String,
    @SerialName("name") val name: String,
    @SerialName("min_select") val minSelect: Int,
    @SerialName("max_select") val maxSelect: Int,
    @SerialName("is_required") val isRequired: Boolean,
    @SerialName("addons") val addons: List<AddonDto>,
)

@Serializable
data class AddonDto(
    @SerialName("id") val id: String,
    @SerialName("name") val name: String,
    @SerialName("description") val description: String,
    @SerialName("extra_duration_minutes") val extraDurationMinutes: Int,
    @SerialName("price_paise") val pricePaise: Long,
)

@Serializable
data class ServiceabilityRequestDto(
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
)

/** `POST /v1/doorstep/serviceability` — always 200; serviceability_in/out_200.json. */
@Serializable
data class ServiceabilityDto(
    @SerialName("serviceable") val serviceable: Boolean,
    @SerialName("city") val city: CityRefDto?,
    @SerialName("zone") val zone: ZoneRefDto?,
    /** OUTSIDE_SERVICE_AREA when not serviceable, else null. */
    @SerialName("reason") val reason: String?,
)

@Serializable
data class ZoneRefDto(
    @SerialName("id") val id: String,
    @SerialName("name") val name: String,
)

// ── Quotes (A1, golden) ────────────────────────────────────────────────────

@Serializable
data class QuoteRequestDto(
    @SerialName("service_id") val serviceId: String,
    @SerialName("option_id") val optionId: String,
    @SerialName("quantity") val quantity: Int,
    @SerialName("addons") val addons: List<QuoteAddonDto>,
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
)

@Serializable
data class QuoteAddonDto(@SerialName("addon_id") val addonId: String)

/** `POST /v1/doorstep/quotes` 201 — quote_post_201.json, quote_post_201_salon.json. */
@Serializable
data class QuoteDto(
    @SerialName("id") val id: String,
    /** open | expired | consumed */
    @SerialName("status") val status: String,
    @SerialName("service_id") val serviceId: String,
    @SerialName("option_id") val optionId: String,
    @SerialName("quantity") val quantity: Int,
    @SerialName("city_code") val cityCode: String,
    @SerialName("zone_id") val zoneId: String,
    @SerialName("lines") val lines: List<QuoteLineDto>,
    /** What the customer pays, GST-inclusive. */
    @SerialName("total_paise") val totalPaise: Long,
    @SerialName("taxable_paise") val taxablePaise: Long,
    @SerialName("tax_paise") val taxPaise: Long,
    @SerialName("prices_include_tax") val pricesIncludeTax: Boolean,
    @SerialName("tax_provisional") val taxProvisional: Boolean,
    @SerialName("tax_note") val taxNote: String,
    @SerialName("duration_minutes") val durationMinutes: Int,
    @SerialName("expires_at") val expiresAt: String,
    @SerialName("created_at") val createdAt: String,
)

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

// ── Addresses (A3) ─────────────────────────────────────────────────────────

@Serializable
data class AddressDto(
    @SerialName("id") val id: String,
    @SerialName("label") val label: String,
    @SerialName("line1") val line1: String,
    @SerialName("line2") val line2: String?,
    @SerialName("landmark") val landmark: String?,
    /** The only part a professional sees before accepting. */
    @SerialName("locality") val locality: String,
    @SerialName("city_code") val cityCode: String,
    @SerialName("pincode") val pincode: String,
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
    @SerialName("zone_id") val zoneId: String,
    @SerialName("is_default") val isDefault: Boolean,
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class AddressListDto(@SerialName("items") val items: List<AddressDto>)

@Serializable
data class AddressInputDto(
    @SerialName("label") val label: String,
    @SerialName("line1") val line1: String,
    @SerialName("line2") val line2: String? = null,
    @SerialName("landmark") val landmark: String? = null,
    @SerialName("locality") val locality: String,
    @SerialName("pincode") val pincode: String,
    @SerialName("lat") val lat: Double,
    @SerialName("lng") val lng: Double,
    @SerialName("is_default") val isDefault: Boolean? = null,
)

// ── Slots and bookings (A3) ────────────────────────────────────────────────

@Serializable
data class SlotDaysDto(
    @SerialName("timezone") val timezone: String,
    @SerialName("days") val days: List<SlotDayDto>,
)

@Serializable
data class SlotDayDto(
    /** YYYY-MM-DD in the city's zone. */
    @SerialName("date") val date: String,
    @SerialName("slots") val slots: List<SlotDto>,
)

@Serializable
data class SlotDto(
    @SerialName("start") val start: String,
    @SerialName("end") val end: String,
    @SerialName("available") val available: Boolean,
)

@Serializable
data class BookingCreateRequestDto(
    @SerialName("quote_id") val quoteId: String,
    @SerialName("address_id") val addressId: String,
    @SerialName("slot_start") val slotStart: String,
    @SerialName("require_female_pro") val requireFemalePro: Boolean,
    @SerialName("notes") val notes: String? = null,
)

@Serializable
data class BookingCreatedDto(
    @SerialName("booking") val booking: BookingDto,
    @SerialName("payment_intent") val paymentIntent: PaymentIntentDto,
)

@Serializable
data class BookingSummaryDto(
    @SerialName("id") val id: String,
    @SerialName("status") val status: String,
    @SerialName("service_name") val serviceName: String,
    @SerialName("category_slug") val categorySlug: String,
    @SerialName("slot_start") val slotStart: String,
    @SerialName("slot_end") val slotEnd: String,
    @SerialName("total_paise") val totalPaise: Long,
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class BookingPageDto(
    @SerialName("items") val items: List<BookingSummaryDto>,
    @SerialName("next_cursor") val nextCursor: String?,
)

@Serializable
data class BookingDto(
    @SerialName("id") val id: String,
    @SerialName("status") val status: String,
    @SerialName("service_id") val serviceId: String,
    @SerialName("service_name") val serviceName: String,
    @SerialName("category_slug") val categorySlug: String,
    @SerialName("city_code") val cityCode: String,
    @SerialName("zone_id") val zoneId: String,
    @SerialName("slot_start") val slotStart: String,
    @SerialName("slot_end") val slotEnd: String,
    @SerialName("duration_minutes") val durationMinutes: Int,
    @SerialName("require_female_pro") val requireFemalePro: Boolean,
    @SerialName("items") val items: List<QuoteLineDto>,
    @SerialName("total_paise") val totalPaise: Long,
    @SerialName("taxable_paise") val taxablePaise: Long,
    @SerialName("tax_paise") val taxPaise: Long,
    @SerialName("paid_paise") val paidPaise: Long,
    @SerialName("refunded_paise") val refundedPaise: Long,
    @SerialName("cancellation_fee_paise") val cancellationFeePaise: Long,
    @SerialName("extras_total_paise") val extrasTotalPaise: Long,
    @SerialName("outstanding_paise") val outstandingPaise: Long,
    @SerialName("hold_expires_at") val holdExpiresAt: String?,
    @SerialName("address") val address: AddressDto,
    @SerialName("professional") val professional: BookingProfessionalDto?,
    @SerialName("parent_booking_id") val parentBookingId: String?,
    /** The server sends it from assigned until in_progress; the client shows it only then too. */
    @SerialName("start_otp") val startOtp: String?,
    /**
     * The finish code: the server sets it while the job is in_progress, once
     * the after photos are in (A5); null before. The client shows it ONLY while
     * in_progress (`BookingRules.visibleEndOtp`).
     */
    @SerialName("end_otp") val endOtp: String?,
    /** Before/after visit photos (A5); [] until then. Customers are sent only those two phases. */
    @SerialName("photos") val photos: List<BookingPhotoDto>,
    /** The booking's timeline, oldest first: what the tracking timeline is drawn from. */
    @SerialName("status_history") val statusHistory: List<StatusStepDto>,
    @SerialName("can_cancel") val canCancel: Boolean,
    @SerialName("can_reschedule") val canReschedule: Boolean,
    @SerialName("created_at") val createdAt: String,
    @SerialName("updated_at") val updatedAt: String,
)

/** One step of the customer's booking timeline (`StatusStep`). Actor and reason are admin-only. */
@Serializable
data class StatusStepDto(
    /** null on the first step (the booking was created). */
    @SerialName("from_status") val fromStatus: String?,
    @SerialName("to_status") val toStatus: String,
    @SerialName("created_at") val createdAt: String,
)

/** A visit photo (`Photo`): the professional's before/after evidence. */
@Serializable
data class BookingPhotoDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String,
    /** before | after for the customer (kit_seal and extra_evidence are not sent to them). */
    @SerialName("phase") val phase: String,
    @SerialName("media_id") val mediaId: String,
    @SerialName("created_at") val createdAt: String,
)

/** First name, photo, rating — never a phone number. */
@Serializable
data class BookingProfessionalDto(
    @SerialName("first_name") val firstName: String,
    @SerialName("photo_media_id") val photoMediaId: String?,
    @SerialName("rating_avg") val ratingAvg: Double?,
    @SerialName("jobs_completed") val jobsCompleted: Int,
)

@Serializable
data class CancelPreviewDto(
    @SerialName("allowed") val allowed: Boolean,
    @SerialName("fee_paise") val feePaise: Long,
    @SerialName("refund_paise") val refundPaise: Long,
    /** free_before_assignment | lt_3h | lt_1h_or_en_route | arrived | … */
    @SerialName("rule") val rule: String,
)

@Serializable
data class CancelRequestDto(@SerialName("reason") val reason: String)

@Serializable
data class RescheduleRequestDto(@SerialName("slot_start") val slotStart: String)

// ── Payments (A3/A5) ───────────────────────────────────────────────────────

@Serializable
data class PaymentIntentDto(
    @SerialName("payment_id") val paymentId: String,
    /** doorstep_booking | doorstep_extras */
    @SerialName("reference_type") val referenceType: String,
    @SerialName("reference_id") val referenceId: String,
    @SerialName("amount_paise") val amountPaise: Long,
    /** created | pending | succeeded | failed | refunded | partially_refunded */
    @SerialName("status") val status: String,
    /** payments-service's client session, relayed unchanged; `{}` when payments attached none. */
    @SerialName("checkout") val checkout: CheckoutSessionDto,
)

/**
 * payments-service's client session (`paymentsclient.ClientSession`) as
 * doorstep-service relays it in `PaymentIntent.checkout`: public fields only,
 * never a secret. No key is required — the server sends `{}` when payments
 * attached no session — so each defaults to null.
 */
@Serializable
data class CheckoutSessionDto(
    /** `razorpay`, or `stub` on a development stack (settled via the dev stub-confirm route, never a sheet). */
    @SerialName("provider") val provider: String? = null,
    @SerialName("order_id") val orderId: String? = null,
    /** The PUBLISHABLE key id. */
    @SerialName("key_id") val keyId: String? = null,
    @SerialName("merchant_display_name") val merchantDisplayName: String? = null,
)

@Serializable
data class BookingPaymentsDto(
    @SerialName("payments") val payments: List<PaymentIntentDto>,
    @SerialName("refunds") val refunds: List<RefundDto>,
)

@Serializable
data class RefundDto(
    @SerialName("id") val id: String,
    @SerialName("payment_id") val paymentId: String,
    @SerialName("cause") val cause: String,
    @SerialName("amount_paise") val amountPaise: Long,
    /** requested | pending | succeeded | failed */
    @SerialName("status") val status: String,
    @SerialName("created_at") val createdAt: String,
)

// ── The visit: extras, outstanding, rating, rework (A5) ────────────────────

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
    /** The photo the professional attached to justify the extra; null when none. */
    @SerialName("evidence_media_id") val evidenceMediaId: String?,
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class ExtraListDto(@SerialName("items") val items: List<ExtraDto>)

@Serializable
data class ExtrasBillDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String,
    @SerialName("amount_paise") val amountPaise: Long,
    @SerialName("taxable_paise") val taxablePaise: Long,
    @SerialName("tax_paise") val taxPaise: Long,
    /** open | payment_pending | paid | outstanding | waived | refunded */
    @SerialName("status") val status: String,
    @SerialName("due_at") val dueAt: String?,
    @SerialName("paid_at") val paidAt: String?,
)

@Serializable
data class OutstandingDto(
    @SerialName("total_paise") val totalPaise: Long,
    @SerialName("bills") val bills: List<ExtrasBillDto>,
)

@Serializable
data class RatingInputDto(
    @SerialName("stars") val stars: Int,
    @SerialName("tags") val tags: List<String> = emptyList(),
    @SerialName("comment") val comment: String? = null,
)

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

@Serializable
data class ReworkInputDto(
    @SerialName("reason") val reason: String,
    @SerialName("media_ids") val mediaIds: List<String> = emptyList(),
    @SerialName("slot_start") val slotStart: String? = null,
)

@Serializable
data class ReworkRequestDto(
    @SerialName("id") val id: String,
    @SerialName("booking_id") val bookingId: String,
    @SerialName("child_booking_id") val childBookingId: String?,
    /** requested | approved | rejected | scheduled | completed */
    @SerialName("status") val status: String,
    @SerialName("reason") val reason: String,
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class ReworkListDto(@SerialName("items") val items: List<ReworkRequestDto>)

// ── Safety (A5) and realtime (A4) ──────────────────────────────────────────

@Serializable
data class SosInputDto(
    @SerialName("lat") val lat: Double? = null,
    @SerialName("lng") val lng: Double? = null,
    @SerialName("note") val note: String? = null,
)

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

@Serializable
data class ShareTokenDto(
    @SerialName("token") val token: String,
    @SerialName("url") val url: String,
    @SerialName("expires_at") val expiresAt: String,
)

@Serializable
data class RealtimeTokenRequestDto(@SerialName("booking_id") val bookingId: String)

@Serializable
data class RealtimeTokenDto(
    @SerialName("token") val token: String,
    @SerialName("topics") val topics: List<String>,
    @SerialName("expires_at") val expiresAt: String,
)

// ── Errors ─────────────────────────────────────────────────────────────────

/** The error envelope, read from a 4xx/5xx body. Clients branch on [DoorstepErrorBodyDto.code], never the message. */
@Serializable
data class DoorstepErrorEnvelopeDto(
    @SerialName("error") val error: DoorstepErrorBodyDto,
    @SerialName("meta") val meta: DoorstepMetaDto? = null,
)

@Serializable
data class DoorstepErrorBodyDto(
    @SerialName("code") val code: String,
    @SerialName("message") val message: String,
    @SerialName("details") val details: JsonObject? = null,
)

@Serializable
data class DoorstepMetaDto(@SerialName("request_id") val requestId: String? = null)
