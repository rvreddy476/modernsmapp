package com.us.android.feature.doorstep.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * doorstep-service's customer routes (`/v1/doorstep`, contracts/doorstep/openapi.yaml)
 * on the platform's shared Retrofit client. Every call returns the raw
 * [Response] so [doorstepCall] can read the error envelope of a 4xx instead of
 * throwing. The gateway stamps identity; nothing here sends a user id.
 */
@Suppress("TooManyFunctions")
interface DoorstepApi {

    // ── Catalogue (A1) ──
    @GET("v1/doorstep/catalogue")
    suspend fun catalogue(@Query("city") city: String): Response<ApiEnvelope<CatalogueDto>>

    @GET("v1/doorstep/categories/{slug}")
    suspend fun category(@Path("slug") slug: String, @Query("city") city: String): Response<ApiEnvelope<CategoryPageDto>>

    @GET("v1/doorstep/services/{id}")
    suspend fun service(@Path("id") serviceId: String, @Query("city") city: String): Response<ApiEnvelope<ServicePageDto>>

    @POST("v1/doorstep/serviceability")
    suspend fun serviceability(@Body body: ServiceabilityRequestDto): Response<ApiEnvelope<ServiceabilityDto>>

    @POST("v1/doorstep/quotes")
    suspend fun createQuote(@Body body: QuoteRequestDto): Response<ApiEnvelope<QuoteDto>>

    @GET("v1/doorstep/quotes/{id}")
    suspend fun quote(@Path("id") quoteId: String): Response<ApiEnvelope<QuoteDto>>

    // ── Addresses (A3) ──
    @GET("v1/doorstep/addresses")
    suspend fun addresses(): Response<ApiEnvelope<AddressListDto>>

    @POST("v1/doorstep/addresses")
    suspend fun createAddress(@Body body: AddressInputDto): Response<ApiEnvelope<AddressDto>>

    @PATCH("v1/doorstep/addresses/{id}")
    suspend fun updateAddress(@Path("id") addressId: String, @Body body: AddressInputDto): Response<ApiEnvelope<AddressDto>>

    @DELETE("v1/doorstep/addresses/{id}")
    suspend fun deleteAddress(@Path("id") addressId: String): Response<Unit>

    // ── Slots and bookings (A3) ──
    @GET("v1/doorstep/slots")
    suspend fun slots(
        @Query("quote_id") quoteId: String?,
        @Query("booking_id") bookingId: String?,
        @Query("address_id") addressId: String?,
        @Query("require_female_pro") requireFemalePro: Boolean?,
    ): Response<ApiEnvelope<SlotDaysDto>>

    @POST("v1/doorstep/bookings")
    suspend fun createBooking(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: BookingCreateRequestDto,
    ): Response<ApiEnvelope<BookingCreatedDto>>

    @GET("v1/doorstep/bookings")
    suspend fun bookings(
        @Query("status") status: String,
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int,
    ): Response<ApiEnvelope<BookingPageDto>>

    @GET("v1/doorstep/bookings/{id}")
    suspend fun booking(@Path("id") bookingId: String): Response<ApiEnvelope<BookingDto>>

    @GET("v1/doorstep/bookings/{id}/cancel-preview")
    suspend fun cancelPreview(@Path("id") bookingId: String): Response<ApiEnvelope<CancelPreviewDto>>

    @POST("v1/doorstep/bookings/{id}/cancel")
    suspend fun cancel(@Path("id") bookingId: String, @Body body: CancelRequestDto): Response<ApiEnvelope<BookingDto>>

    @POST("v1/doorstep/bookings/{id}/reschedule")
    suspend fun reschedule(@Path("id") bookingId: String, @Body body: RescheduleRequestDto): Response<ApiEnvelope<BookingDto>>

    /** (Re)opens the intent for a pending booking — idempotent on `doorstep:booking:{id}` server-side. */
    @POST("v1/doorstep/bookings/{id}/payment/intent")
    suspend fun bookingPaymentIntent(@Path("id") bookingId: String): Response<ApiEnvelope<PaymentIntentDto>>

    /** The ONLY source of "paid": the payment rows the signed event settled. */
    @GET("v1/doorstep/bookings/{id}/payment")
    suspend fun bookingPayments(@Path("id") bookingId: String): Response<ApiEnvelope<BookingPaymentsDto>>

    /**
     * DEVELOPMENT STACKS ONLY: asks payments-service's stub gateway to settle
     * the booking's intent. It marks nothing paid — the signed event does, and
     * the client reads that from [bookingPayments]. 404 DOORSTEP_NOT_FOUND on
     * any other deployment, 409 DOORSTEP_STUB_UNAVAILABLE when payments has a
     * real provider.
     */
    @POST("v1/doorstep/bookings/{id}/payment/stub-confirm")
    suspend fun stubConfirmBookingPayment(@Path("id") bookingId: String): Response<ApiEnvelope<BookingPaymentsDto>>

    // ── The customer picks the professional (B1) ──

    /** The professionals for a selection at an address: scheduled (next free starts, or a [date]) or [asap] with an ETA. */
    @Suppress("LongParameterList")
    @GET("v1/doorstep/services/{id}/professionals")
    suspend fun serviceProfessionals(
        @Path("id") serviceId: String,
        @Query("option_id") optionId: String,
        @Query("quantity") quantity: Int,
        @Query("addon_id") addonIds: List<String>,
        @Query("address_id") addressId: String,
        @Query("date") date: String?,
        @Query("asap") asap: Boolean?,
        @Query("sort") sort: String,
        @Query("require_female_pro") requireFemalePro: Boolean?,
    ): Response<ApiEnvelope<ProfessionalListDto>>

    /** Alternatives for a pro_unavailable booking; each card carries difference_paise. 409 in any other status. */
    @GET("v1/doorstep/bookings/{id}/professionals")
    suspend fun bookingProfessionals(
        @Path("id") bookingId: String,
        @Query("date") date: String?,
        @Query("asap") asap: Boolean?,
        @Query("sort") sort: String,
    ): Response<ApiEnvelope<ProfessionalListDto>>

    /** Pick another professional for a pro_unavailable booking (no new payment; dearer: a pro_change bill). */
    @POST("v1/doorstep/bookings/{id}/change-professional")
    suspend fun changeProfessional(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Path("id") bookingId: String,
        @Body body: ProChangeRequestDto,
    ): Response<ApiEnvelope<ProChangeResultDto>>

    // ── The visit (A5) ──
    @GET("v1/doorstep/bookings/{id}/extras")
    suspend fun extras(@Path("id") bookingId: String): Response<ApiEnvelope<ExtraListDto>>

    @POST("v1/doorstep/bookings/{id}/extras/{extraId}/approve")
    suspend fun approveExtra(@Path("id") bookingId: String, @Path("extraId") extraId: String): Response<ApiEnvelope<ExtraDto>>

    @POST("v1/doorstep/bookings/{id}/extras/{extraId}/decline")
    suspend fun declineExtra(@Path("id") bookingId: String, @Path("extraId") extraId: String): Response<ApiEnvelope<ExtraDto>>

    @GET("v1/doorstep/bookings/{id}/extras-bill")
    suspend fun extrasBill(@Path("id") bookingId: String): Response<ApiEnvelope<ExtrasBillDto>>

    @POST("v1/doorstep/extras-bills/{id}/payment/intent")
    suspend fun extrasPaymentIntent(@Path("id") billId: String): Response<ApiEnvelope<PaymentIntentDto>>

    @GET("v1/doorstep/me/outstanding")
    suspend fun outstanding(): Response<ApiEnvelope<OutstandingDto>>

    @POST("v1/doorstep/bookings/{id}/rating")
    suspend fun rate(@Path("id") bookingId: String, @Body body: RatingInputDto): Response<ApiEnvelope<RatingDto>>

    @POST("v1/doorstep/bookings/{id}/rework")
    suspend fun requestRework(@Path("id") bookingId: String, @Body body: ReworkInputDto): Response<ApiEnvelope<ReworkRequestDto>>

    @GET("v1/doorstep/bookings/{id}/rework")
    suspend fun rework(@Path("id") bookingId: String): Response<ApiEnvelope<ReworkListDto>>

    // ── Safety (A5) ──
    @POST("v1/doorstep/bookings/{id}/sos")
    suspend fun sos(@Path("id") bookingId: String, @Body body: SosInputDto): Response<ApiEnvelope<IncidentDto>>

    @POST("v1/doorstep/bookings/{id}/share")
    suspend fun share(@Path("id") bookingId: String): Response<ApiEnvelope<ShareTokenDto>>

    @DELETE("v1/doorstep/bookings/{id}/share")
    suspend fun revokeShare(@Path("id") bookingId: String): Response<Unit>

    @GET("v1/doorstep/bookings/{id}/messages")
    suspend fun messages(@Path("id") bookingId: String, @Query("cursor") cursor: String?): Response<ApiEnvelope<MessagePageDto>>

    @POST("v1/doorstep/bookings/{id}/messages")
    suspend fun sendMessage(@Path("id") bookingId: String, @Body body: MessageInputDto): Response<ApiEnvelope<MessageDto>>

    @POST("v1/doorstep/bookings/{id}/messages/{messageId}/read")
    suspend fun readMessage(@Path("id") bookingId: String, @Path("messageId") messageId: String): Response<Unit>

    @GET("v1/doorstep/trusted-contact")
    suspend fun trustedContact(): Response<ApiEnvelope<TrustedContactDto>>

    @PUT("v1/doorstep/trusted-contact")
    suspend fun saveTrustedContact(@Body body: TrustedContactInputDto): Response<ApiEnvelope<TrustedContactDto>>

    // ── Realtime (A4) ──
    @GET("v1/doorstep/tickets")
    suspend fun tickets(): Response<ApiEnvelope<TicketListDto>>

    @POST("v1/doorstep/tickets")
    suspend fun openTicket(@Body body: TicketInputDto): Response<ApiEnvelope<TicketDto>>

    @POST("v1/doorstep/realtime/token")
    suspend fun realtimeToken(@Body body: RealtimeTokenRequestDto): Response<ApiEnvelope<RealtimeTokenDto>>
}
