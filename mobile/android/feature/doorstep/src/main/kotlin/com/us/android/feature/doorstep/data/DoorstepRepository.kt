package com.us.android.feature.doorstep.data

import kotlinx.serialization.json.Json
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/** The customer's Doorstep data seam. Every call answers a typed [DoorstepResult]; nothing throws. */
@Suppress("TooManyFunctions")
interface DoorstepRepository {
    suspend fun catalogue(city: String): DoorstepResult<CatalogueDto>
    suspend fun category(slug: String, city: String): DoorstepResult<CategoryPageDto>
    suspend fun service(serviceId: String, city: String): DoorstepResult<ServicePageDto>
    suspend fun serviceability(lat: Double, lng: Double): DoorstepResult<ServiceabilityDto>
    suspend fun createQuote(request: QuoteRequestDto): DoorstepResult<QuoteDto>
    suspend fun quote(quoteId: String): DoorstepResult<QuoteDto>

    suspend fun addresses(): DoorstepResult<List<AddressDto>>
    suspend fun createAddress(input: AddressInputDto): DoorstepResult<AddressDto>
    suspend fun deleteAddress(addressId: String): DoorstepResult<Unit>

    suspend fun slots(quoteId: String?, bookingId: String?, addressId: String?, requireFemalePro: Boolean): DoorstepResult<SlotDaysDto>

    /** `POST /bookings` under [idempotencyKey]: the SAME key on a resend returns the same booking. */
    suspend fun createBooking(idempotencyKey: String, request: BookingCreateRequestDto): DoorstepResult<BookingCreatedDto>
    suspend fun bookings(filter: String, cursor: String? = null, limit: Int = PAGE_SIZE): DoorstepResult<BookingPageDto>
    suspend fun booking(bookingId: String): DoorstepResult<BookingDto>
    suspend fun cancelPreview(bookingId: String): DoorstepResult<CancelPreviewDto>
    suspend fun cancel(bookingId: String, reason: String): DoorstepResult<BookingDto>
    suspend fun reschedule(bookingId: String, slotStart: String): DoorstepResult<BookingDto>
    suspend fun bookingPaymentIntent(bookingId: String): DoorstepResult<PaymentIntentDto>
    suspend fun bookingPayments(bookingId: String): DoorstepResult<BookingPaymentsDto>

    suspend fun extras(bookingId: String): DoorstepResult<List<ExtraDto>>
    suspend fun approveExtra(bookingId: String, extraId: String): DoorstepResult<ExtraDto>
    suspend fun declineExtra(bookingId: String, extraId: String): DoorstepResult<ExtraDto>
    suspend fun extrasBill(bookingId: String): DoorstepResult<ExtrasBillDto>
    suspend fun extrasPaymentIntent(billId: String): DoorstepResult<PaymentIntentDto>
    suspend fun outstanding(): DoorstepResult<OutstandingDto>

    suspend fun rate(bookingId: String, stars: Int, comment: String?): DoorstepResult<RatingDto>
    suspend fun requestRework(bookingId: String, reason: String): DoorstepResult<ReworkRequestDto>
    suspend fun rework(bookingId: String): DoorstepResult<List<ReworkRequestDto>>

    suspend fun sos(bookingId: String, note: String?): DoorstepResult<IncidentDto>
    suspend fun share(bookingId: String): DoorstepResult<ShareTokenDto>

    suspend fun realtimeToken(bookingId: String): DoorstepResult<RealtimeTokenDto>

    /** A fresh idempotency key (one per booking decision, one per sheet opening). */
    fun newIdempotencyKey(): String = UUID.randomUUID().toString()

    companion object {
        const val PAGE_SIZE = 20
    }
}

/**
 * doorstep-service over the shared client. The platform [Json] ignores
 * unknown keys, so an additive server field never breaks parsing; the strict
 * fixture test is what catches a renamed or dropped key.
 */
@Singleton
@Suppress("TooManyFunctions")
class RealDoorstepRepository @Inject constructor(
    private val api: DoorstepApi,
    private val json: Json,
) : DoorstepRepository {

    override suspend fun catalogue(city: String) = doorstepCall(json) { api.catalogue(city) }

    override suspend fun category(slug: String, city: String) = doorstepCall(json) { api.category(slug, city) }

    override suspend fun service(serviceId: String, city: String) = doorstepCall(json) { api.service(serviceId, city) }

    override suspend fun serviceability(lat: Double, lng: Double) =
        doorstepCall(json) { api.serviceability(ServiceabilityRequestDto(lat, lng)) }

    override suspend fun createQuote(request: QuoteRequestDto) = doorstepCall(json) { api.createQuote(request) }

    override suspend fun quote(quoteId: String) = doorstepCall(json) { api.quote(quoteId) }

    override suspend fun addresses() = doorstepCall(json) { api.addresses() }.map { it.items }

    override suspend fun createAddress(input: AddressInputDto) = doorstepCall(json) { api.createAddress(input) }

    override suspend fun deleteAddress(addressId: String) = doorstepUnitCall(json) { api.deleteAddress(addressId) }

    override suspend fun slots(quoteId: String?, bookingId: String?, addressId: String?, requireFemalePro: Boolean) =
        doorstepCall(json) { api.slots(quoteId, bookingId, addressId, requireFemalePro) }

    override suspend fun createBooking(idempotencyKey: String, request: BookingCreateRequestDto) =
        doorstepCall(json) { api.createBooking(idempotencyKey, request) }

    override suspend fun bookings(filter: String, cursor: String?, limit: Int) =
        doorstepCall(json) { api.bookings(filter, cursor, limit) }

    override suspend fun booking(bookingId: String) = doorstepCall(json) { api.booking(bookingId) }

    override suspend fun cancelPreview(bookingId: String) = doorstepCall(json) { api.cancelPreview(bookingId) }

    override suspend fun cancel(bookingId: String, reason: String) =
        doorstepCall(json) { api.cancel(bookingId, CancelRequestDto(reason)) }

    override suspend fun reschedule(bookingId: String, slotStart: String) =
        doorstepCall(json) { api.reschedule(bookingId, RescheduleRequestDto(slotStart)) }

    override suspend fun bookingPaymentIntent(bookingId: String) = doorstepCall(json) { api.bookingPaymentIntent(bookingId) }

    override suspend fun bookingPayments(bookingId: String) = doorstepCall(json) { api.bookingPayments(bookingId) }

    override suspend fun extras(bookingId: String) = doorstepCall(json) { api.extras(bookingId) }.map { it.items }

    override suspend fun approveExtra(bookingId: String, extraId: String) = doorstepCall(json) { api.approveExtra(bookingId, extraId) }

    override suspend fun declineExtra(bookingId: String, extraId: String) = doorstepCall(json) { api.declineExtra(bookingId, extraId) }

    override suspend fun extrasBill(bookingId: String) = doorstepCall(json) { api.extrasBill(bookingId) }

    override suspend fun extrasPaymentIntent(billId: String) = doorstepCall(json) { api.extrasPaymentIntent(billId) }

    override suspend fun outstanding() = doorstepCall(json) { api.outstanding() }

    override suspend fun rate(bookingId: String, stars: Int, comment: String?) =
        doorstepCall(json) { api.rate(bookingId, RatingInputDto(stars = stars, comment = comment?.takeIf { it.isNotBlank() })) }

    override suspend fun requestRework(bookingId: String, reason: String) =
        doorstepCall(json) { api.requestRework(bookingId, ReworkInputDto(reason = reason)) }

    override suspend fun rework(bookingId: String) = doorstepCall(json) { api.rework(bookingId) }.map { it.items }

    override suspend fun sos(bookingId: String, note: String?) =
        doorstepCall(json) { api.sos(bookingId, SosInputDto(note = note?.takeIf { it.isNotBlank() })) }

    override suspend fun share(bookingId: String) = doorstepCall(json) { api.share(bookingId) }

    override suspend fun realtimeToken(bookingId: String) =
        doorstepCall(json) { api.realtimeToken(RealtimeTokenRequestDto(bookingId)) }
}
