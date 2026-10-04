package com.us.android.feature.doorstep

import android.app.Activity
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentLauncher
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSession
import com.us.android.feature.doorstep.data.AddressDto
import com.us.android.feature.doorstep.data.BookingCreateRequestDto
import com.us.android.feature.doorstep.data.BookingCreatedDto
import com.us.android.feature.doorstep.data.BookingDto
import com.us.android.feature.doorstep.data.BookingPageDto
import com.us.android.feature.doorstep.data.BookingPaymentsDto
import com.us.android.feature.doorstep.data.BookingProfessionalDto
import com.us.android.feature.doorstep.data.CancelPreviewDto
import com.us.android.feature.doorstep.data.CatalogueDto
import com.us.android.feature.doorstep.data.CategoryPageDto
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.ExtraDto
import com.us.android.feature.doorstep.data.ExtrasBillDto
import com.us.android.feature.doorstep.data.IncidentDto
import com.us.android.feature.doorstep.data.OutstandingDto
import com.us.android.feature.doorstep.data.PaymentIntentDto
import com.us.android.feature.doorstep.data.QuoteDto
import com.us.android.feature.doorstep.data.QuoteRequestDto
import com.us.android.feature.doorstep.data.RatingDto
import com.us.android.feature.doorstep.data.RealtimeTokenDto
import com.us.android.feature.doorstep.data.RefundDto
import com.us.android.feature.doorstep.data.ReworkRequestDto
import com.us.android.feature.doorstep.data.ServicePageDto
import com.us.android.feature.doorstep.data.ServiceabilityDto
import com.us.android.feature.doorstep.data.ShareTokenDto
import com.us.android.feature.doorstep.data.SlotDaysDto
import com.us.android.feature.doorstep.payment.DoorstepReference
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.io.File

/*
 * Shared test doubles for the Doorstep feature (the Feast/Mopedu shape).
 */

/** The fixture files, decoded leniently into data for tests that need realistic DTOs. */
object Fixtures {
    private val lenient = Json { ignoreUnknownKeys = true }
    private val dir = File("src/test/resources/contracts")

    fun raw(name: String): String = File(dir, name).readText()

    fun quote(name: String = "quote_post_201.json"): QuoteDto =
        lenient.decodeFromString(com.us.android.core.network.ApiEnvelope.serializer(QuoteDto.serializer()), raw(name)).data!!

    fun salonService(): ServicePageDto =
        lenient.decodeFromString(com.us.android.core.network.ApiEnvelope.serializer(ServicePageDto.serializer()), raw("service_get_200.json")).data!!
}

const val BOOKING_ID = "b0000000-0000-4000-8000-000000000001"
const val ADDRESS_ID = "a0000000-0000-4000-8000-000000000001"
const val PAYMENT_ID = "p0000000-0000-4000-8000-000000000001"

fun address(id: String = ADDRESS_ID) = AddressDto(
    id = id,
    label = "Home",
    line1 = "Flat 402, Lake View",
    line2 = null,
    landmark = null,
    locality = "Gachibowli",
    cityCode = "HYD",
    pincode = "500032",
    lat = 17.44,
    lng = 78.35,
    zoneId = "ab4daa10-2d55-51d8-ac2a-f7ca4179e957",
    isDefault = true,
    createdAt = "2026-10-04T06:00:00Z",
)

fun booking(
    status: String = "pending_payment",
    startOtp: String? = null,
    id: String = BOOKING_ID,
    holdExpiresAt: String? = "2026-10-04T06:40:00Z",
) = BookingDto(
    id = id,
    status = status,
    serviceId = "492b816e-df2d-5545-b150-b9b8889c7dde",
    serviceName = "Kitchen deep cleaning",
    categorySlug = "home-cleaning",
    cityCode = "HYD",
    zoneId = "ab4daa10-2d55-51d8-ac2a-f7ca4179e957",
    slotStart = "2026-10-04T10:00:00Z",
    slotEnd = "2026-10-04T13:30:00Z",
    durationMinutes = 210,
    requireFemalePro = false,
    items = emptyList(),
    totalPaise = 224800,
    taxablePaise = 190508,
    taxPaise = 34292,
    paidPaise = 0,
    refundedPaise = 0,
    cancellationFeePaise = 0,
    extrasTotalPaise = 0,
    outstandingPaise = 0,
    holdExpiresAt = holdExpiresAt,
    address = address(),
    professional = if (status in setOf("pending_payment", "confirmed")) null else BookingProfessionalDto("Lakshmi", null, 4.8, 120),
    parentBookingId = null,
    startOtp = startOtp,
    canCancel = true,
    canReschedule = true,
    createdAt = "2026-10-04T06:30:00Z",
    updatedAt = "2026-10-04T06:30:00Z",
)

fun checkout(orderId: String = "order_RZP1") = JsonObject(
    mapOf(
        "provider" to JsonPrimitive("razorpay"),
        "order_id" to JsonPrimitive(orderId),
        "key_id" to JsonPrimitive("rzp_test_publishable"),
        "merchant_display_name" to JsonPrimitive("Doorstep"),
    ),
)

fun intent(
    referenceId: String = BOOKING_ID,
    referenceType: String = DoorstepReference.BOOKING,
    status: String = "created",
    paymentId: String = PAYMENT_ID,
    checkout: JsonObject = checkout(),
) = PaymentIntentDto(
    paymentId = paymentId,
    referenceType = referenceType,
    referenceId = referenceId,
    amountPaise = 224800,
    status = status,
    checkout = checkout,
)

fun payments(vararg rows: PaymentIntentDto, refunds: List<RefundDto> = emptyList()) = BookingPaymentsDto(rows.toList(), refunds)

fun extra(id: String, status: String, totalPaise: Long, quantity: Int = 1) = ExtraDto(
    id = id,
    bookingId = BOOKING_ID,
    kind = "rate_card",
    rateCardId = "rc-$id",
    addonId = null,
    name = "Extra $id",
    quantity = quantity,
    unitPricePaise = totalPaise / quantity,
    totalPaise = totalPaise,
    status = status,
    createdAt = "2026-10-04T11:00:00Z",
)

fun bill(status: String, amountPaise: Long = 50000, id: String = "bill-1") = ExtrasBillDto(
    id = id,
    bookingId = BOOKING_ID,
    amountPaise = amountPaise,
    taxablePaise = amountPaise * 100 / 118,
    taxPaise = amountPaise - amountPaise * 100 / 118,
    status = status,
    dueAt = null,
    paidAt = null,
)

/** A configurable [DoorstepRepository] that records what the screens asked. */
@Suppress("TooManyFunctions")
class FakeDoorstepRepository : DoorstepRepository {

    var quoteResult: DoorstepResult<QuoteDto> = DoorstepResult.Success(Fixtures.quote())
    var addressesResult: DoorstepResult<List<AddressDto>> = DoorstepResult.Success(listOf(address()))
    var createBookingResult: (key: String, request: BookingCreateRequestDto) -> DoorstepResult<BookingCreatedDto> =
        { _, _ -> DoorstepResult.Success(BookingCreatedDto(booking(), intent())) }
    var paymentsResults: ArrayDeque<DoorstepResult<BookingPaymentsDto>> = ArrayDeque()
    var paymentsFallback: DoorstepResult<BookingPaymentsDto> = DoorstepResult.Success(payments(intent(status = "pending")))
    var paymentIntentResult: DoorstepResult<PaymentIntentDto> = DoorstepResult.Success(intent(status = "pending"))

    val bookingKeys = mutableListOf<String>()
    val bookingRequests = mutableListOf<BookingCreateRequestDto>()
    var paymentReads = 0
    var paymentIntentCalls = 0
    private var keySeq = 0

    override fun newIdempotencyKey(): String = "key-${++keySeq}"

    override suspend fun createBooking(idempotencyKey: String, request: BookingCreateRequestDto): DoorstepResult<BookingCreatedDto> {
        bookingKeys += idempotencyKey
        bookingRequests += request
        return createBookingResult(idempotencyKey, request)
    }

    override suspend fun bookingPayments(bookingId: String): DoorstepResult<BookingPaymentsDto> {
        paymentReads++
        return paymentsResults.removeFirstOrNull() ?: paymentsFallback
    }

    override suspend fun bookingPaymentIntent(bookingId: String): DoorstepResult<PaymentIntentDto> {
        paymentIntentCalls++
        return paymentIntentResult
    }

    override suspend fun quote(quoteId: String) = quoteResult

    override suspend fun addresses() = addressesResult

    override suspend fun catalogue(city: String): DoorstepResult<CatalogueDto> = unused()
    override suspend fun category(slug: String, city: String): DoorstepResult<CategoryPageDto> = unused()
    override suspend fun service(serviceId: String, city: String): DoorstepResult<ServicePageDto> = unused()
    override suspend fun serviceability(lat: Double, lng: Double): DoorstepResult<ServiceabilityDto> = unused()
    override suspend fun createQuote(request: QuoteRequestDto): DoorstepResult<QuoteDto> = quoteResult
    override suspend fun createAddress(input: com.us.android.feature.doorstep.data.AddressInputDto): DoorstepResult<AddressDto> = unused()
    override suspend fun deleteAddress(addressId: String): DoorstepResult<Unit> = unused()
    override suspend fun slots(quoteId: String?, bookingId: String?, addressId: String?, requireFemalePro: Boolean): DoorstepResult<SlotDaysDto> =
        unused()
    override suspend fun bookings(filter: String, cursor: String?, limit: Int): DoorstepResult<BookingPageDto> = unused()
    override suspend fun booking(bookingId: String): DoorstepResult<BookingDto> = unused()
    override suspend fun cancelPreview(bookingId: String): DoorstepResult<CancelPreviewDto> = unused()
    override suspend fun cancel(bookingId: String, reason: String): DoorstepResult<BookingDto> = unused()
    override suspend fun reschedule(bookingId: String, slotStart: String): DoorstepResult<BookingDto> = unused()
    override suspend fun extras(bookingId: String): DoorstepResult<List<ExtraDto>> = unused()
    override suspend fun approveExtra(bookingId: String, extraId: String): DoorstepResult<ExtraDto> = unused()
    override suspend fun declineExtra(bookingId: String, extraId: String): DoorstepResult<ExtraDto> = unused()
    override suspend fun extrasBill(bookingId: String): DoorstepResult<ExtrasBillDto> = unused()
    var extrasIntentResult: DoorstepResult<PaymentIntentDto> =
        DoorstepResult.Success(intent(referenceId = "bill-1", referenceType = DoorstepReference.EXTRAS, paymentId = "p-extras"))

    override suspend fun extrasPaymentIntent(billId: String): DoorstepResult<PaymentIntentDto> = extrasIntentResult
    override suspend fun outstanding(): DoorstepResult<OutstandingDto> = unused()
    override suspend fun rate(bookingId: String, stars: Int, comment: String?): DoorstepResult<RatingDto> = unused()
    override suspend fun requestRework(bookingId: String, reason: String): DoorstepResult<ReworkRequestDto> = unused()
    override suspend fun rework(bookingId: String): DoorstepResult<List<ReworkRequestDto>> = unused()
    override suspend fun sos(bookingId: String, note: String?): DoorstepResult<IncidentDto> = unused()
    override suspend fun share(bookingId: String): DoorstepResult<ShareTokenDto> = unused()
    override suspend fun realtimeToken(bookingId: String): DoorstepResult<RealtimeTokenDto> = unused()

    private fun <T> unused(): DoorstepResult<T> = DoorstepResult.Failure(DoorstepError.Unexpected(null, "not faked"))
}

/** A coordinator whose launcher refuses to open: a ViewModel must never open a sheet itself. */
fun confirmingOnlyCoordinator() = PaymentCoordinator(
    object : PaymentLauncher {
        override fun open(activity: Activity, attempt: PaymentAttempt, session: PaymentSession, onOutcome: (PaymentOutcome) -> Unit) =
            error("the checkout ViewModel must never open a sheet itself")

        override fun abandon(attempt: PaymentAttempt) = Unit
    },
)
