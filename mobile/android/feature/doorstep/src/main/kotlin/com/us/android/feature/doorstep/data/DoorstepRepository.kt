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

    /**
     * Dev stack only: `POST /bookings/{id}/payment/stub-confirm`. The rows it
     * answers are NOT a verdict — callers ignore them and poll [bookingPayments].
     */
    suspend fun stubConfirmBookingPayment(bookingId: String): DoorstepResult<Unit>

    /** B1: `GET /services/{id}/professionals` for [query]. */
    suspend fun serviceProfessionals(query: ProfessionalQuery): DoorstepResult<ProfessionalListDto>

    /** B1: `GET /bookings/{id}/professionals` — alternatives for a pro_unavailable booking. */
    suspend fun bookingProfessionals(bookingId: String, date: String?, asap: Boolean, sort: String): DoorstepResult<ProfessionalListDto>

    /** B1: `POST /bookings/{id}/change-professional` under [idempotencyKey] (the same key on a resend answers the same change). */
    suspend fun changeProfessional(idempotencyKey: String, bookingId: String, request: ProChangeRequestDto): DoorstepResult<ProChangeResultDto>

    suspend fun extras(bookingId: String): DoorstepResult<List<ExtraDto>>
    suspend fun approveExtra(bookingId: String, extraId: String): DoorstepResult<ExtraDto>
    suspend fun declineExtra(bookingId: String, extraId: String): DoorstepResult<ExtraDto>
    suspend fun extrasBill(bookingId: String): DoorstepResult<ExtrasBillDto>
    suspend fun extrasPaymentIntent(billId: String): DoorstepResult<PaymentIntentDto>
    suspend fun outstanding(): DoorstepResult<OutstandingDto>

    suspend fun rate(bookingId: String, stars: Int, comment: String?): DoorstepResult<RatingDto>
    suspend fun requestRework(bookingId: String, reason: String, slotStart: String? = null): DoorstepResult<ReworkRequestDto>
    suspend fun rework(bookingId: String): DoorstepResult<List<ReworkRequestDto>>

    suspend fun sos(bookingId: String, note: String?): DoorstepResult<IncidentDto>
    suspend fun share(bookingId: String): DoorstepResult<ShareTokenDto>
    suspend fun revokeShare(bookingId: String): DoorstepResult<Unit>
    suspend fun messages(bookingId: String, cursor: String? = null): DoorstepResult<MessagePageDto>
    suspend fun sendMessage(bookingId: String, body: String): DoorstepResult<MessageDto>
    suspend fun readMessage(bookingId: String, messageId: String): DoorstepResult<Unit>
    suspend fun trustedContact(): DoorstepResult<TrustedContactDto?>
    suspend fun saveTrustedContact(name: String, phone: String): DoorstepResult<TrustedContactDto>
    suspend fun tickets(): DoorstepResult<List<TicketDto>>
    suspend fun openTicket(body: TicketInputDto): DoorstepResult<TicketDto>

    suspend fun realtimeToken(bookingId: String): DoorstepResult<RealtimeTokenDto>

    /** A fresh idempotency key (one per booking decision, one per sheet opening). */
    fun newIdempotencyKey(): String = UUID.randomUUID().toString()

    companion object {
        const val PAGE_SIZE = 20
    }
}

/**
 * What `GET /services/{id}/professionals` is asked: the customer's selection,
 * the address, and how to list (a [date] or [asap], never both; [sort]).
 */
data class ProfessionalQuery(
    val serviceId: String,
    val optionId: String,
    val quantity: Int,
    val addonIds: Set<String>,
    val addressId: String,
    val date: String?,
    val asap: Boolean,
    val sort: String,
    val requireFemalePro: Boolean,
)

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

    // The answer's rows are dropped on purpose: "paid" is read only from bookingPayments.
    override suspend fun stubConfirmBookingPayment(bookingId: String) =
        doorstepCall(json) { api.stubConfirmBookingPayment(bookingId) }.map { }

    override suspend fun serviceProfessionals(query: ProfessionalQuery) = doorstepCall(json) {
        api.serviceProfessionals(
            serviceId = query.serviceId,
            optionId = query.optionId,
            quantity = query.quantity,
            addonIds = query.addonIds.sorted(),
            addressId = query.addressId,
            date = query.date.takeUnless { query.asap },
            asap = true.takeIf { query.asap },
            sort = query.sort,
            requireFemalePro = true.takeIf { query.requireFemalePro },
        )
    }

    override suspend fun bookingProfessionals(bookingId: String, date: String?, asap: Boolean, sort: String) =
        doorstepCall(json) { api.bookingProfessionals(bookingId, date.takeUnless { asap }, true.takeIf { asap }, sort) }

    override suspend fun changeProfessional(idempotencyKey: String, bookingId: String, request: ProChangeRequestDto) =
        doorstepCall(json) { api.changeProfessional(idempotencyKey, bookingId, request) }

    override suspend fun extras(bookingId: String) = doorstepCall(json) { api.extras(bookingId) }.map { it.items }

    override suspend fun approveExtra(bookingId: String, extraId: String) = doorstepCall(json) { api.approveExtra(bookingId, extraId) }

    override suspend fun declineExtra(bookingId: String, extraId: String) = doorstepCall(json) { api.declineExtra(bookingId, extraId) }

    override suspend fun extrasBill(bookingId: String) = doorstepCall(json) { api.extrasBill(bookingId) }

    override suspend fun extrasPaymentIntent(billId: String) = doorstepCall(json) { api.extrasPaymentIntent(billId) }

    override suspend fun outstanding() = doorstepCall(json) { api.outstanding() }

    override suspend fun rate(bookingId: String, stars: Int, comment: String?) =
        doorstepCall(json) { api.rate(bookingId, RatingInputDto(stars = stars, comment = comment?.takeIf { it.isNotBlank() })) }

    override suspend fun requestRework(bookingId: String, reason: String, slotStart: String?) =
        doorstepCall(json) { api.requestRework(bookingId, ReworkInputDto(reason = reason, slotStart = slotStart)) }

    override suspend fun rework(bookingId: String) = doorstepCall(json) { api.rework(bookingId) }.map { it.items }

    override suspend fun sos(bookingId: String, note: String?) =
        doorstepCall(json) { api.sos(bookingId, SosInputDto(note = note?.takeIf { it.isNotBlank() })) }

    override suspend fun share(bookingId: String) = doorstepCall(json) { api.share(bookingId) }

    override suspend fun revokeShare(bookingId: String) = doorstepUnitCall(json) { api.revokeShare(bookingId) }
    override suspend fun messages(bookingId: String, cursor: String?) = doorstepCall(json) { api.messages(bookingId, cursor) }
    override suspend fun sendMessage(bookingId: String, body: String) = doorstepCall(json) { api.sendMessage(bookingId, MessageInputDto(body)) }
    override suspend fun readMessage(bookingId: String, messageId: String) = doorstepUnitCall(json) { api.readMessage(bookingId, messageId) }
    override suspend fun trustedContact() = doorstepNullableCall(json) { api.trustedContact() }
    override suspend fun saveTrustedContact(name: String, phone: String) = doorstepCall(json) { api.saveTrustedContact(TrustedContactInputDto(name, phone)) }
    override suspend fun tickets() = doorstepCall(json) { api.tickets() }.map { it.items }
    override suspend fun openTicket(body: TicketInputDto) = doorstepCall(json) { api.openTicket(body) }

    override suspend fun realtimeToken(bookingId: String) =
        doorstepCall(json) { api.realtimeToken(RealtimeTokenRequestDto(bookingId)) }
}
