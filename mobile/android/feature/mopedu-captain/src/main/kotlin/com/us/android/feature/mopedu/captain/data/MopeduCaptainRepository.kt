package com.us.android.feature.mopedu.captain.data

import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.CaptainState
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RidePayment
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import kotlinx.serialization.json.Json
import java.time.Instant
import java.time.OffsetDateTime
import java.time.format.DateTimeParseException
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.math.roundToLong

/**
 * The onboarding verdict (2026-09-18). DigiLocker-verified documents
 * auto-approve; only manually uploaded documents wait for a reviewer.
 */
enum class ReviewState(val code: String) {
    APPROVED("approved"),
    UNDER_REVIEW("under_review"),
    INCOMPLETE("incomplete"),
    ;

    companion object {
        /** An unknown state is INCOMPLETE: the app never assumes approval. */
        fun fromCode(code: String?): ReviewState = entries.firstOrNull { it.code == code } ?: INCOMPLETE
    }
}

/** `review {state, pending}` as the gate reads it. [pending] names what is missing or waiting. */
data class PartnerReview(val state: ReviewState, val pending: List<String> = emptyList()) {
    val isApproved: Boolean get() = state == ReviewState.APPROVED

    companion object {
        val INCOMPLETE = PartnerReview(ReviewState.INCOMPLETE)
    }
}

/** The captain's profile as the gate reads it: whether the server thinks they are online, and the review verdict, ride along. */
data class CaptainProfile(
    val profile: PartnerProfile,
    val isOnline: Boolean,
    val review: PartnerReview = PartnerReview.INCOMPLETE,
)

/** The checkout answer: instantly active (the trial), or a sheet session for the paid plan. */
data class SubscriptionCheckout(
    val subscriptionId: String,
    val intentId: String?,
    val amount: MoneyPaise,
    val currency: String,
    val clientSession: Map<String, String>,
    val status: String,
) {
    /** The trial: granted on the spot, no sheet. */
    val isActive: Boolean get() = status == ACTIVE

    private companion object {
        const val ACTIVE = "active"
    }
}

/** What `GET /subscriptions/me/payment` says. Only [PAID] ever activates a paid plan on this device. */
enum class SubscriptionPaymentStatus(val code: String) {
    PENDING("pending"),
    CONFIRMING("confirming"),
    PAID("paid"),
    FAILED("failed"),

    /** A status this build does not know. Treated as still confirming, never as paid. */
    UNKNOWN(""),
    ;

    companion object {
        fun fromCode(code: String?): SubscriptionPaymentStatus = entries.firstOrNull { it.code == code && it != UNKNOWN } ?: UNKNOWN
    }
}

data class SubscriptionPayment(val status: SubscriptionPaymentStatus, val intentId: String?, val expiresAt: String?)

/** The captain's data seam. Every call answers a typed [CaptainResult]; nothing throws. */
@Suppress("TooManyFunctions")
interface MopeduCaptainRepository {
    suspend fun setOnline(online: Boolean): CaptainResult<Unit>
    suspend fun sendLocation(telemetry: LocationTelemetryDto): CaptainResult<Unit>
    suspend fun incomingOffers(): CaptainResult<List<CaptainOffer>>
    suspend fun acceptOffer(offerId: String): CaptainResult<String>
    suspend fun rejectOffer(offerId: String): CaptainResult<Unit>
    suspend fun markArriving(rideId: String): CaptainResult<Unit>
    suspend fun markArrived(rideId: String): CaptainResult<Unit>
    suspend fun verifyOtpAndStart(rideId: String, otp: String): CaptainResult<Unit>
    suspend fun completeRide(rideId: String, finalDistanceKm: Double, finalDurationMin: Int, idempotencyKey: String): CaptainResult<Unit>
    suspend fun confirmCashPayment(rideId: String): CaptainResult<Unit>
    suspend fun ridePayment(rideId: String): CaptainResult<RidePayment>
    suspend fun earnings(): CaptainResult<CaptainState>

    suspend fun profile(): CaptainResult<CaptainProfile>
    suspend fun createProfile(fullName: String, phone: String, email: String?, cityId: String? = null): CaptainResult<PartnerProfile>
    suspend fun documents(): CaptainResult<List<PartnerDocument>>
    /** A document record for a photo ALREADY confirmed by media-service: [mediaId] is never a placeholder. */
    suspend fun submitDocument(documentType: String, documentNumber: String?, mediaId: String): CaptainResult<PartnerDocument>
    suspend fun startAadhaar(): CaptainResult<AadhaarStartResponseDto>
    suspend fun callbackAadhaar(requestId: String, assertionToken: String): CaptainResult<PartnerProfile>
    suspend fun vehicles(): CaptainResult<List<Vehicle>>
    suspend fun addVehicle(vehicleType: VehicleType, registrationNumber: String, brand: String?, model: String?): CaptainResult<Vehicle>
    suspend fun subscriptionPlans(): CaptainResult<List<SubscriptionPlan>>

    /** Starts [planCode]: the trial instantly, a paid plan through the sheet. */
    suspend fun checkout(planCode: String, method: PaymentMethod): CaptainResult<SubscriptionCheckout>

    /** The plan payment's server state — the only source of "paid". */
    suspend fun subscriptionPayment(): CaptainResult<SubscriptionPayment>
    suspend fun mySubscription(): CaptainResult<PartnerSubscription?>

    fun newIdempotencyKey(): String = UUID.randomUUID().toString()
}

@Singleton
@Suppress("TooManyFunctions")
class RealMopeduCaptainRepository @Inject constructor(
    private val api: MopeduCaptainApi,
    private val json: Json,
) : MopeduCaptainRepository {

    override suspend fun setOnline(online: Boolean): CaptainResult<Unit> =
        captainUnitCall(json) { if (online) api.goOnline() else api.goOffline() }

    override suspend fun sendLocation(telemetry: LocationTelemetryDto): CaptainResult<Unit> =
        captainUnitCall(json) { api.updateLocation(telemetry) }

    override suspend fun incomingOffers(): CaptainResult<List<CaptainOffer>> =
        captainCall(json, empty = emptyList()) { api.incomingOffers() }.map { list -> list.map { it.toOffer() } }

    override suspend fun acceptOffer(offerId: String): CaptainResult<String> =
        captainCall(json) { api.acceptOffer(offerId) }.map { it.rideId }

    override suspend fun rejectOffer(offerId: String): CaptainResult<Unit> = captainUnitCall(json) { api.rejectOffer(offerId) }

    override suspend fun markArriving(rideId: String): CaptainResult<Unit> = captainUnitCall(json) { api.markArriving(rideId) }

    override suspend fun markArrived(rideId: String): CaptainResult<Unit> = captainUnitCall(json) { api.markArrived(rideId) }

    override suspend fun verifyOtpAndStart(rideId: String, otp: String): CaptainResult<Unit> =
        captainUnitCall(json) { api.startRide(rideId, StartRideRequestDto(otp)) }

    override suspend fun completeRide(rideId: String, finalDistanceKm: Double, finalDurationMin: Int, idempotencyKey: String): CaptainResult<Unit> =
        captainUnitCall(json) { api.completeRide(rideId, CompleteRideRequestDto(finalDistanceKm, finalDurationMin, idempotencyKey)) }

    override suspend fun confirmCashPayment(rideId: String): CaptainResult<Unit> = captainUnitCall(json) { api.confirmCashPayment(rideId) }

    override suspend fun ridePayment(rideId: String): CaptainResult<RidePayment> =
        captainCall(json) { api.ridePayment(rideId) }.map {
            RidePayment(
                method = PaymentMethod.fromCode(it.method),
                status = RidePaymentStatus.fromCode(it.status),
                amount = MoneyPaise(it.amountPaise),
                refunded = MoneyPaise(it.refundedPaise),
            )
        }

    override suspend fun earnings(): CaptainResult<CaptainState> = captainCall(json) { api.earnings() }.map {
        CaptainState(
            isOnline = false,
            activeRideId = null,
            rating = it.rating,
            totalRidesCompleted = it.totalRidesCount,
            todayEarnings = MoneyPaise(it.todayEarningsPaise),
            todayRides = it.todayRidesCount,
            totalEarnings = MoneyPaise(it.totalEarningsPaise),
        )
    }

    override suspend fun profile(): CaptainResult<CaptainProfile> =
        captainCall(json) { api.profile() }.map { CaptainProfile(it.toDomain(), it.isOnline, it.toReview()) }

    override suspend fun createProfile(fullName: String, phone: String, email: String?, cityId: String?): CaptainResult<PartnerProfile> =
        captainCall(json) { api.createProfile(CreatePartnerRequestDto(fullName = fullName, phone = phone, email = email, cityId = cityId)) }
            .map { it.toDomain() }

    override suspend fun documents(): CaptainResult<List<PartnerDocument>> =
        captainCall(json, empty = emptyList()) { api.documents() }.map { list -> list.map { it.toDomain() } }

    override suspend fun submitDocument(documentType: String, documentNumber: String?, mediaId: String): CaptainResult<PartnerDocument> =
        captainCall(json) { api.submitDocument(SubmitDocumentRequestDto.forMedia(documentType, documentNumber, mediaId)) }.map { it.toDomain() }

    override suspend fun startAadhaar(): CaptainResult<AadhaarStartResponseDto> = captainCall(json) { api.startAadhaar() }

    override suspend fun callbackAadhaar(requestId: String, assertionToken: String): CaptainResult<PartnerProfile> =
        captainCall(json) { api.callbackAadhaar(AadhaarCallbackRequestDto(requestId, assertionToken)) }.map { it.toDomain() }

    override suspend fun vehicles(): CaptainResult<List<Vehicle>> =
        captainCall(json, empty = emptyList()) { api.vehicles() }.map { list -> list.map { it.toDomain() } }

    override suspend fun addVehicle(vehicleType: VehicleType, registrationNumber: String, brand: String?, model: String?): CaptainResult<Vehicle> =
        captainCall(json) {
            api.addVehicle(CreateVehicleRequestDto(vehicleType = vehicleType.code, registrationNumber = registrationNumber, brand = brand, model = model))
        }.map { it.toDomain() }

    override suspend fun subscriptionPlans(): CaptainResult<List<SubscriptionPlan>> =
        captainCall(json, empty = emptyList()) { api.subscriptionPlans() }.map { list -> list.map { it.toDomain() } }

    override suspend fun checkout(planCode: String, method: PaymentMethod): CaptainResult<SubscriptionCheckout> =
        captainCall(json) { api.checkout(SubscriptionCheckoutRequestDto(planCode = planCode, method = method.code)) }.map {
            SubscriptionCheckout(
                subscriptionId = it.subscriptionId,
                intentId = it.intentId,
                amount = MoneyPaise(it.amountPaise),
                currency = it.currency,
                clientSession = it.clientSession.orEmpty(),
                status = it.status,
            )
        }

    override suspend fun subscriptionPayment(): CaptainResult<SubscriptionPayment> =
        captainCall(json) { api.subscriptionPayment() }.map {
            SubscriptionPayment(SubscriptionPaymentStatus.fromCode(it.status), it.intentId, it.expiresAt)
        }

    override suspend fun mySubscription(): CaptainResult<PartnerSubscription?> =
        when (val result = captainCall(json) { api.mySubscription() }) {
            is CaptainResult.Success -> CaptainResult.Success(result.value.toDomain())
            is CaptainResult.Failure -> if (result.error == CaptainError.NotFound) CaptainResult.Success(null) else result
        }
}

// ── DTO → domain ────────────────────────────────────────────────────────

internal fun CaptainOfferDto.toOffer(nowEpochMs: Long = System.currentTimeMillis()) = CaptainOffer(
    id = id,
    rideId = rideId,
    pickup = GeoPoint(pickupLat, pickupLng, pickupAddress),
    drop = GeoPoint(dropLat, dropLng, dropAddress),
    distanceKM = distanceKm,
    estimatedEarnings = MoneyPaise(estimatedFarePaise),
    score = score,
    expiresAtEpochMs = expiresAt.toEpochMs() ?: (nowEpochMs + DEFAULT_OFFER_WINDOW_MILLIS),
    vehicleType = VehicleType.fromCode(vehicleType),
    paymentMethod = PaymentMethod.fromCode(paymentMethod),
)

internal fun PartnerProfileDto.toDomain(): PartnerProfile = PartnerProfile(
    id = id,
    partnerType = partnerType,
    fullName = fullName,
    phone = phone,
    email = email,
    status = status,
    kycStatus = kycStatus,
    cityId = cityId,
    profilePhotoUrl = profilePhotoUrl,
    rating = rating,
    ridesCompleted = ridesCompleted,
)

/**
 * The review verdict. When the server sends `review`, it is authoritative.
 * A server that predates it (no `review` at all) is read through the legacy
 * fields: an approved KYC is approved, anything else is INCOMPLETE — never
 * "under review", because nothing on that server puts a captain in a queue.
 */
internal fun PartnerProfileDto.toReview(): PartnerReview {
    val sent = review
    if (sent != null) return PartnerReview(ReviewState.fromCode(sent.state), sent.pending)
    return if (kycStatus == KYC_APPROVED) PartnerReview(ReviewState.APPROVED) else PartnerReview.INCOMPLETE
}

internal fun PartnerDocumentDto.toDomain(): PartnerDocument = PartnerDocument(
    id = id,
    partnerId = partnerId,
    documentType = documentType,
    documentNumber = documentNumber,
    fileUrl = fileUrl,
    status = status,
    rejectionReason = rejectionReason,
    expiresAt = expiresAt,
)

internal fun VehicleDto.toDomain(): Vehicle = Vehicle(
    id = id,
    partnerId = partnerId,
    vehicleType = VehicleType.fromCode(vehicleType),
    registrationNumber = registrationNumber,
    brand = brand,
    model = model,
    color = color,
    manufactureYear = manufactureYear ?: year,
    status = status,
)

/** The price is `price_paise` when a newer server sends it, else `price_amount` rupees (legacy). */
internal fun SubscriptionPlanDto.toDomain(): SubscriptionPlan = SubscriptionPlan(
    id = id.ifBlank { code },
    code = code,
    name = name,
    vehicleType = vehicleType,
    billingCycle = billingCycle.ifBlank { if (billingPeriodDays > 0) "$billingPeriodDays days" else "" },
    price = MoneyPaise(if (pricePaise > 0) pricePaise else (priceAmount * PAISE_PER_RUPEE).roundToLong()),
    dailyLeadCap = dailyLeadCap,
    priorityScore = priorityScore,
    description = description,
    billingPeriodDays = billingPeriodDays,
)

internal fun PartnerSubscriptionDto.toDomain(): PartnerSubscription = PartnerSubscription(
    id = id,
    partnerId = partnerId,
    planId = planId,
    planCode = planCode,
    planName = planName,
    status = status,
    leadsUsed = leadsUsed,
    dailyLeadCap = dailyLeadCap,
    startsAt = startsAt,
    expiresAt = expiresAt,
)

/** An ISO-8601 instant as epoch millis; null when absent or unparseable. */
internal fun String?.toEpochMs(): Long? {
    val text = this?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    return try {
        OffsetDateTime.parse(text).toInstant().toEpochMilli()
    } catch (e: DateTimeParseException) {
        try {
            Instant.parse(text).toEpochMilli()
        } catch (e2: DateTimeParseException) {
            null
        }
    }
}

private const val DEFAULT_OFFER_WINDOW_MILLIS = 15_000L
private const val PAISE_PER_RUPEE = 100.0
private const val KYC_APPROVED = "approved"
