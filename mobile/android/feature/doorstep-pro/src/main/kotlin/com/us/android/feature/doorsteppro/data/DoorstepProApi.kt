package com.us.android.feature.doorsteppro.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * doorstep-service's professional routes (`/v1/doorstep/pro`,
 * contracts/doorstep/openapi.yaml) on the platform's shared Retrofit client.
 * Every call returns the raw [Response] so [proCall] can read the error
 * envelope of a 4xx instead of throwing. The gateway stamps identity; nothing
 * here sends a user id.
 *
 * Lanes: A2 (onboarding) is live on the server. A4 (duty, location, offers,
 * jobs, realtime token) and A5 (the visit, extras, safety, chat, rating,
 * earnings) are contract-only as of 2026-10-04 — built against the schemas,
 * their fixtures listed as pending in the contract test.
 */
@Suppress("TooManyFunctions")
interface DoorstepProApi {
    @GET("v1/doorstep/pro/me/prices")
    suspend fun prices(): Response<ApiEnvelope<ProListDto<ProServicePricingDto>>>

    @POST("v1/doorstep/pro/me/prices")
    suspend fun submitPrice(@Body body: ProPriceRequest): Response<ApiEnvelope<ProPriceDto>>

    @POST("v1/doorstep/pro/me/prices/{id}/withdraw")
    suspend fun withdrawPrice(@Path("id") id: String): Response<ApiEnvelope<ProPriceDto>>

    @PUT("v1/doorstep/pro/me/services/{id}/same-day")
    suspend fun sameDay(@Path("id") id: String, @Body body: SameDayRequest): Response<ApiEnvelope<SameDaySettingDto>>

    // ── Onboarding (A2) ──
    @POST("v1/doorstep/pro/apply")
    suspend fun apply(@Body body: ProApplyRequest): Response<ApiEnvelope<ProfessionalDto>>

    @GET("v1/doorstep/pro/me")
    suspend fun me(): Response<ApiEnvelope<ProfessionalDto>>

    @PATCH("v1/doorstep/pro/me")
    suspend fun patchMe(@Body body: ProPatchRequest): Response<ApiEnvelope<ProfessionalDto>>

    @GET("v1/doorstep/pro/readiness")
    suspend fun readiness(): Response<ApiEnvelope<ProReadinessDto>>

    @POST("v1/doorstep/pro/digilocker/start")
    suspend fun digiLockerStart(): Response<ApiEnvelope<DigiLockerStartDto>>

    @POST("v1/doorstep/pro/digilocker/callback")
    suspend fun digiLockerCallback(@Body body: DigiLockerCallbackRequest): Response<ApiEnvelope<ProReadinessDto>>

    @POST("v1/doorstep/pro/selfie")
    suspend fun selfie(@Body body: MediaRequest): Response<ApiEnvelope<KycCheckDto>>

    @GET("v1/doorstep/pro/skills")
    suspend fun skills(): Response<ApiEnvelope<ProListDto<SkillDto>>>

    @PUT("v1/doorstep/pro/me/skills")
    suspend fun putSkills(@Body body: SkillsRequest): Response<ApiEnvelope<ProListDto<ProSkillDto>>>

    @POST("v1/doorstep/pro/me/skills/{code}/certificate")
    suspend fun tradeCertificate(
        @Path("code") skillCode: String,
        @Body body: CertificateRequest,
    ): Response<ApiEnvelope<ProDocumentDto>>

    @PUT("v1/doorstep/pro/me/area")
    suspend fun putArea(@Body body: ProAreaRequest): Response<ApiEnvelope<ProAreaDto>>

    @GET("v1/doorstep/pro/me/hours")
    suspend fun hours(): Response<ApiEnvelope<WeeklyHoursDto>>

    @PUT("v1/doorstep/pro/me/hours")
    suspend fun putHours(@Body body: WeeklyHoursDto): Response<ApiEnvelope<WeeklyHoursDto>>

    @GET("v1/doorstep/pro/me/days-off")
    suspend fun daysOff(): Response<ApiEnvelope<ProListDto<DayOffDto>>>

    @POST("v1/doorstep/pro/me/days-off")
    suspend fun addDayOff(@Body body: DayOffDto): Response<ApiEnvelope<DayOffDto>>

    @DELETE("v1/doorstep/pro/me/days-off/{date}")
    suspend fun deleteDayOff(@Path("date") date: String): Response<Unit>

    @PUT("v1/doorstep/pro/me/bank")
    suspend fun putBank(@Body body: BankRequest): Response<ApiEnvelope<PayoutAccountDto>>

    @POST("v1/doorstep/pro/me/police-certificate")
    suspend fun policeCertificate(@Body body: CertificateRequest): Response<ApiEnvelope<ProDocumentDto>>

    @POST("v1/doorstep/pro/me/agreement")
    suspend fun acceptAgreement(@Body body: AgreementRequest): Response<ApiEnvelope<ProReadinessDto>>

    @PUT("v1/doorstep/pro/me/pan")
    suspend fun putPan(@Body body: PanRequest): Response<ApiEnvelope<ProReadinessDto>>

    /** The customer route (A1, live): which active zone a point falls in — the service area's zone. */
    @POST("v1/doorstep/serviceability")
    suspend fun serviceability(@Body body: ServiceabilityRequest): Response<ApiEnvelope<ServiceabilityDto>>

    // ── Duty and offers (A4) ──
    @POST("v1/doorstep/pro/duty/on")
    suspend fun dutyOn(@Body body: LocationRequest): Response<ApiEnvelope<DutyStateDto>>

    @POST("v1/doorstep/pro/duty/off")
    suspend fun dutyOff(): Response<ApiEnvelope<DutyStateDto>>

    @POST("v1/doorstep/pro/location")
    suspend fun location(@Body body: LocationRequest): Response<Unit>

    @GET("v1/doorstep/pro/offers")
    suspend fun offers(): Response<ApiEnvelope<ProListDto<OfferDto>>>

    @POST("v1/doorstep/pro/offers/{id}/accept")
    suspend fun acceptOffer(@Path("id") offerId: String): Response<ApiEnvelope<ProJobDto>>

    @POST("v1/doorstep/pro/offers/{id}/decline")
    suspend fun declineOffer(@Path("id") offerId: String, @Body body: DeclineRequest): Response<Unit>

    @GET("v1/doorstep/pro/jobs")
    suspend fun jobs(@Query("status") status: String?, @Query("cursor") cursor: String?): Response<ApiEnvelope<ProJobPageDto>>

    @GET("v1/doorstep/pro/jobs/{id}")
    suspend fun job(@Path("id") bookingId: String): Response<ApiEnvelope<ProJobDto>>

    @POST("v1/doorstep/pro/realtime/token")
    suspend fun realtimeToken(): Response<ApiEnvelope<RealtimeTokenDto>>

    // ── The visit (A5) ──
    @POST("v1/doorstep/pro/jobs/{id}/en-route")
    suspend fun enRoute(@Path("id") bookingId: String): Response<ApiEnvelope<ProJobDto>>

    @POST("v1/doorstep/pro/jobs/{id}/arrived")
    suspend fun arrived(@Path("id") bookingId: String, @Body body: LocationRequest): Response<ApiEnvelope<ProJobDto>>

    @POST("v1/doorstep/pro/jobs/{id}/photos")
    suspend fun photo(@Path("id") bookingId: String, @Body body: PhotoRequest): Response<ApiEnvelope<PhotoDto>>

    @POST("v1/doorstep/pro/jobs/{id}/start")
    suspend fun start(@Path("id") bookingId: String, @Body body: OtpRequest): Response<ApiEnvelope<ProJobDto>>

    /** PROPOSED — not in openapi.yaml: the rate card / add-ons this job may take. */
    @GET("v1/doorstep/pro/jobs/{id}/extras/options")
    suspend fun extraOptions(@Path("id") bookingId: String): Response<ApiEnvelope<ProListDto<ExtraOptionDto>>>

    /** PROPOSED — not in openapi.yaml: the extras already proposed on this job, with their decisions. */
    @GET("v1/doorstep/pro/jobs/{id}/extras")
    suspend fun extras(@Path("id") bookingId: String): Response<ApiEnvelope<ProListDto<ExtraDto>>>

    @POST("v1/doorstep/pro/jobs/{id}/extras")
    suspend fun proposeExtra(@Path("id") bookingId: String, @Body body: ExtraRequest): Response<ApiEnvelope<ExtraDto>>

    @DELETE("v1/doorstep/pro/jobs/{id}/extras/{extraId}")
    suspend fun withdrawExtra(@Path("id") bookingId: String, @Path("extraId") extraId: String): Response<Unit>

    @POST("v1/doorstep/pro/jobs/{id}/finish")
    suspend fun finish(@Path("id") bookingId: String): Response<ApiEnvelope<ProJobDto>>

    @POST("v1/doorstep/pro/jobs/{id}/complete")
    suspend fun complete(@Path("id") bookingId: String, @Body body: OtpRequest): Response<ApiEnvelope<ProJobDto>>

    @POST("v1/doorstep/pro/jobs/{id}/no-show")
    suspend fun customerNoShow(@Path("id") bookingId: String): Response<ApiEnvelope<ProJobDto>>

    @POST("v1/doorstep/pro/jobs/{id}/cancel")
    suspend fun cancel(@Path("id") bookingId: String, @Body body: CancelRequest): Response<Unit>

    @POST("v1/doorstep/pro/jobs/{id}/sos")
    suspend fun sos(@Path("id") bookingId: String, @Body body: SosRequest): Response<ApiEnvelope<IncidentDto>>

    @POST("v1/doorstep/pro/jobs/{id}/unsafe-exit")
    suspend fun unsafeExit(@Path("id") bookingId: String, @Body body: SosRequest): Response<ApiEnvelope<IncidentDto>>

    @POST("v1/doorstep/pro/jobs/{id}/rating")
    suspend fun rateCustomer(@Path("id") bookingId: String, @Body body: RatingRequest): Response<ApiEnvelope<RatingDto>>

    @GET("v1/doorstep/pro/jobs/{id}/messages")
    suspend fun messages(@Path("id") bookingId: String, @Query("cursor") cursor: String?): Response<ApiEnvelope<MessagePageDto>>

    @POST("v1/doorstep/pro/jobs/{id}/messages")
    suspend fun sendMessage(@Path("id") bookingId: String, @Body body: MessageRequest): Response<ApiEnvelope<MessageDto>>

    @POST("v1/doorstep/pro/jobs/{id}/messages/{msgId}/read")
    suspend fun readMessage(@Path("id") bookingId: String, @Path("msgId") messageId: String): Response<Unit>

    @GET("v1/doorstep/pro/earnings")
    suspend fun earnings(@Query("from") from: String?, @Query("to") to: String?): Response<ApiEnvelope<EarningsDto>>
}
