package com.us.android.feature.mopedu.rider.data

import com.us.android.core.mobility.model.CaptainInfo
import com.us.android.core.mobility.model.CouponValidation
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.OutstandingCharge
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.QuoteBreakdown
import com.us.android.core.mobility.model.QuoteOption
import com.us.android.core.mobility.model.QuoteSnapshot
import com.us.android.core.mobility.model.ReceiptLine
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RidePayment
import com.us.android.core.mobility.model.RidePaymentIntent
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.RideReceipt
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.SurgeReason
import com.us.android.core.mobility.model.VehicleType
import kotlinx.serialization.json.Json
import java.time.Instant
import java.time.OffsetDateTime
import java.time.format.DateTimeParseException
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/** The customer's ride data seam. Every call answers a typed [MopeduResult]; nothing throws. */
@Suppress("TooManyFunctions")
interface MopeduRiderRepository {
    suspend fun estimate(pickup: GeoPoint, drop: GeoPoint, couponCode: String? = null): MopeduResult<QuoteSnapshot>
    suspend fun validateCoupon(code: String, cityId: String? = null, vehicleType: VehicleType? = null): MopeduResult<CouponValidation>
    suspend fun bookRide(
        quote: QuoteSnapshot,
        vehicleType: VehicleType,
        paymentMethod: PaymentMethod,
        idempotencyKey: String,
    ): MopeduResult<RideBooking>
    suspend fun activeRide(): MopeduResult<RideBooking?>
    suspend fun rideHistory(limit: Int = HISTORY_LIMIT): MopeduResult<List<RideBooking>>
    suspend fun cancelRide(rideId: String, reason: String): MopeduResult<Unit>
    suspend fun receipt(rideId: String): MopeduResult<RideReceipt>
    suspend fun triggerSOS(rideId: String, lat: Double, lng: Double): MopeduResult<Unit>
    suspend fun createShareLink(rideId: String): MopeduResult<String>
    suspend fun rateRide(rideId: String, rating: Int, feedback: String): MopeduResult<Unit>
    suspend fun paymentIntent(rideId: String, method: PaymentMethod): MopeduResult<RidePaymentIntent>
    suspend fun ridePayment(rideId: String): MopeduResult<RidePayment>
    suspend fun switchToCash(rideId: String): MopeduResult<RidePayment>
    suspend fun outstanding(): MopeduResult<List<OutstandingCharge>>
    suspend fun outstandingPaymentIntent(chargeId: String, method: PaymentMethod): MopeduResult<RidePaymentIntent>

    /** A fresh idempotency key (one per booking decision, one per sheet opening). */
    fun newIdempotencyKey(): String = UUID.randomUUID().toString()

    companion object {
        const val HISTORY_LIMIT = 20
    }
}

/**
 * rider-service over the shared client. The platform [Json] ignores unknown
 * keys, so an additive server field never breaks parsing; the error mapper
 * reads the same Json.
 */
@Singleton
@Suppress("TooManyFunctions")
class RealMopeduRiderRepository @Inject constructor(
    private val api: MopeduRiderApi,
    private val json: Json,
) : MopeduRiderRepository {

    override suspend fun estimate(pickup: GeoPoint, drop: GeoPoint, couponCode: String?): MopeduResult<QuoteSnapshot> =
        mopeduCall(json) {
            api.estimate(
                EstimateRequestDto(
                    pickupLat = pickup.lat,
                    pickupLng = pickup.lng,
                    pickupLabel = pickup.address,
                    dropLat = drop.lat,
                    dropLng = drop.lng,
                    dropLabel = drop.address,
                    couponCode = couponCode?.takeIf { it.isNotBlank() },
                ),
            )
        }.map { it.toSnapshot(pickup, drop) }

    override suspend fun validateCoupon(code: String, cityId: String?, vehicleType: VehicleType?): MopeduResult<CouponValidation> =
        mopeduCall(json) { api.validateCoupon(code.trim(), cityId, vehicleType?.code) }.map { dto ->
            CouponValidation(
                code = dto.code.ifBlank { code.trim() },
                description = dto.description,
                discountType = dto.discountType,
                discountValue = dto.discountValuePaise?.let(::MoneyPaise),
                percentBasisPoints = dto.percentBps,
                maxDiscount = dto.maxDiscountPaise?.let(::MoneyPaise),
            )
        }

    override suspend fun bookRide(
        quote: QuoteSnapshot,
        vehicleType: VehicleType,
        paymentMethod: PaymentMethod,
        idempotencyKey: String,
    ): MopeduResult<RideBooking> = mopeduCall(json) {
        api.createRide(
            CreateRideRequestDto(
                quoteId = quote.quoteId,
                pickup = LocationDto(quote.pickup.address, quote.pickup.lat, quote.pickup.lng),
                drop = LocationDto(quote.drop.address, quote.drop.lat, quote.drop.lng),
                vehicleType = vehicleType.code,
                paymentMethod = paymentMethod.code,
                idempotencyKey = idempotencyKey,
                couponCode = quote.couponCode,
            ),
            idempotencyKey,
        )
    }.map { it.toBooking() }

    override suspend fun activeRide(): MopeduResult<RideBooking?> =
        mopeduCall(json, empty = ActiveRideResponseDto()) { api.activeRide() }.map { it.ride?.toBooking() }

    override suspend fun rideHistory(limit: Int): MopeduResult<List<RideBooking>> =
        mopeduCall(json, empty = emptyList()) { api.rides(limit) }.map { rides -> rides.map { it.toBooking() } }

    override suspend fun cancelRide(rideId: String, reason: String): MopeduResult<Unit> =
        mopeduUnitCall(json) { api.cancelRide(rideId, CancelRideRequestDto(reason)) }

    override suspend fun receipt(rideId: String): MopeduResult<RideReceipt> =
        mopeduCall(json) { api.receipt(rideId) }.map { it.toReceipt() }

    override suspend fun triggerSOS(rideId: String, lat: Double, lng: Double): MopeduResult<Unit> =
        mopeduUnitCall(json) { api.triggerSOS(rideId, SOSRequestDto(lat = lat, lng = lng)) }

    override suspend fun createShareLink(rideId: String): MopeduResult<String> =
        mopeduCall(json) { api.createShareToken(rideId) }.map { it.url }

    override suspend fun rateRide(rideId: String, rating: Int, feedback: String): MopeduResult<Unit> =
        mopeduUnitCall(json) { api.rateRide(rideId, RateRideRequestDto(rating = rating, feedback = feedback)) }

    override suspend fun paymentIntent(rideId: String, method: PaymentMethod): MopeduResult<RidePaymentIntent> =
        mopeduCall(json) { api.paymentIntent(rideId, PaymentIntentRequestDto(method.code)) }.map { it.toIntent() }

    override suspend fun ridePayment(rideId: String): MopeduResult<RidePayment> =
        mopeduCall(json) { api.ridePayment(rideId) }.map { it.toPayment() }

    override suspend fun switchToCash(rideId: String): MopeduResult<RidePayment> =
        mopeduCall(json) { api.switchToCash(rideId) }.map { it.toPayment() }

    override suspend fun outstanding(): MopeduResult<List<OutstandingCharge>> =
        mopeduCall(json, empty = emptyList()) { api.outstanding() }.map { list -> list.map { it.toCharge() } }

    override suspend fun outstandingPaymentIntent(chargeId: String, method: PaymentMethod): MopeduResult<RidePaymentIntent> =
        mopeduCall(json) { api.outstandingPaymentIntent(chargeId, PaymentIntentRequestDto(method.code)) }.map { it.toIntent() }
}

// ── DTO → domain ────────────────────────────────────────────────────────

internal fun QuoteSnapshotDto.toSnapshot(pickup: GeoPoint, drop: GeoPoint, nowEpochMs: Long = System.currentTimeMillis()) =
    QuoteSnapshot(
        quoteId = quoteId,
        pickup = pickup,
        drop = drop,
        distanceMeters = (estimatedDistanceKm * METERS_PER_KM).toInt(),
        durationSeconds = (estimatedDurationMin * SECONDS_PER_MINUTE).toInt(),
        options = options.map { it.toOption() },
        expiresAtEpochMs = expiresAt.toEpochMs() ?: (nowEpochMs + QUOTE_TTL_MILLIS),
        couponCode = couponCode ?: options.firstNotNullOfOrNull { it.couponCode },
    )

internal fun QuoteOptionDto.toOption() = QuoteOption(
    vehicleType = VehicleType.fromCode(vehicleType),
    available = available,
    pickupETASeconds = pickupEtaSeconds,
    distanceMeters = distanceMeters,
    durationSeconds = durationSeconds,
    totalFare = MoneyPaise(totalPaise),
    breakdown = breakdown?.let {
        QuoteBreakdown(
            basePaise = it.basePaise,
            distancePaise = it.distancePaise,
            timePaise = it.timePaise,
            platformFeePaise = it.platformFeePaise,
            taxPaise = it.taxPaise,
            surgePaise = it.surgePaise,
            waitingPaise = it.waitingPaise,
            discountPaise = it.discountPaise,
            outstandingPaise = it.outstandingPaise,
        )
    } ?: QuoteBreakdown(),
    surgeBasisPoints = surgeBasisPoints,
    surgeReason = SurgeReason.fromCode(surgeReason),
    windowName = windowName,
    couponCode = couponCode,
    discount = MoneyPaise(if (discountPaise > 0) discountPaise else breakdown?.discountPaise ?: 0),
    taxNote = taxNote,
)

internal fun RideDto.toBooking() = RideBooking(
    id = id,
    customerUserId = customerUserId.orEmpty(),
    partnerId = partnerId,
    vehicleId = vehicleId,
    quoteId = quoteId,
    revision = revision,
    vehicleType = VehicleType.fromCode(vehicleType),
    status = RideStatus.fromCode(status),
    pickup = GeoPoint(pickupLat, pickupLng, pickupAddress),
    drop = GeoPoint(dropLat, dropLng, dropAddress),
    estimatedFare = MoneyPaise(estimatedFarePaise ?: estimatedFare?.let { (it * PAISE_PER_RUPEE).toLong() } ?: 0L),
    finalFare = finalFarePaise?.let(::MoneyPaise),
    paymentMethod = PaymentMethod.fromCode(paymentMethod),
    otp = otp,
    captain = captain?.takeIf { it.id.isNotBlank() || it.name.isNotBlank() }?.let {
        CaptainInfo(it.id, it.name, it.phone, it.rating, it.vehicleModel, it.vehicleNumber, it.vehicleColor, it.profilePhotoUrl)
    },
    requestedAtEpochMs = (requestedAt ?: createdAt).toEpochMs() ?: 0L,
    completedAtEpochMs = completedAt.toEpochMs(),
    cancellationFee = cancellationFeePaise?.let(::MoneyPaise),
    cancelFreeUntilEpochMs = cancelFreeUntil.toEpochMs(),
)

internal fun RideReceiptDto.toReceipt() = RideReceipt(
    rideId = rideId,
    customerUserId = customerUserId.orEmpty(),
    partnerId = partnerId,
    vehicleType = VehicleType.fromCode(vehicleType),
    status = status,
    pickupAddress = pickupAddress,
    dropAddress = dropAddress,
    distanceMeters = distanceMeters,
    durationSeconds = durationSeconds,
    totalFare = MoneyPaise(totalPaise),
    paymentMethod = PaymentMethod.fromCode(paymentMethod),
    paymentStatus = RidePaymentStatus.fromCode(paymentStatus),
    completedAtEpochMs = completedAt.toEpochMs(),
    lines = lines.map { ReceiptLine(it.label, MoneyPaise(it.amountPaise)) },
    taxNote = taxNote,
)

internal fun PaymentIntentDto.toIntent() = RidePaymentIntent(
    intentId = intentId,
    amount = MoneyPaise(amountPaise),
    currency = currency,
    clientSession = clientSession,
    status = status,
)

internal fun RidePaymentDto.toPayment() = RidePayment(
    method = PaymentMethod.fromCode(method),
    status = RidePaymentStatus.fromCode(status),
    amount = MoneyPaise(amountPaise),
    refunded = MoneyPaise(refundedPaise),
)

internal fun OutstandingChargeDto.toCharge() = OutstandingCharge(
    id = id,
    rideId = rideId,
    reason = reason.ifBlank { "Cancellation fee" },
    amount = MoneyPaise(amountPaise),
    createdAtEpochMs = createdAt.toEpochMs(),
    status = status.lowercase(),
)

/** An ISO-8601 instant (`2026-09-18T10:00:00Z` or with an offset) as epoch millis; null when absent or unparseable. */
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

private const val METERS_PER_KM = 1000.0
private const val SECONDS_PER_MINUTE = 60.0
private const val PAISE_PER_RUPEE = 100.0
private const val QUOTE_TTL_MILLIS = 300_000L
