package com.us.android.feature.mopedu.captain.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path

/** rider-service's partner routes, on the platform's shared Retrofit client. */
@Suppress("TooManyFunctions")
interface MopeduCaptainApi {

    @POST("v1/rider/partners/me/online")
    suspend fun goOnline(): Response<ApiEnvelope<Unit>>

    @POST("v1/rider/partners/me/offline")
    suspend fun goOffline(): Response<ApiEnvelope<Unit>>

    /** The on-duty ping from CaptainLocationService. */
    @POST("v1/rider/partners/me/location")
    suspend fun updateLocation(@Body location: LocationTelemetryDto): Response<ApiEnvelope<Unit>>

    @GET("v1/rider/offers/incoming")
    suspend fun incomingOffers(): Response<ApiEnvelope<List<CaptainOfferDto>>>

    @POST("v1/rider/offers/{id}/accept")
    suspend fun acceptOffer(@Path("id") offerId: String): Response<ApiEnvelope<AcceptOfferResponseDto>>

    @POST("v1/rider/offers/{id}/reject")
    suspend fun rejectOffer(@Path("id") offerId: String): Response<ApiEnvelope<Unit>>

    @POST("v1/rider/rides/{id}/arriving")
    suspend fun markArriving(@Path("id") rideId: String): Response<ApiEnvelope<Unit>>

    @POST("v1/rider/rides/{id}/arrived")
    suspend fun markArrived(@Path("id") rideId: String): Response<ApiEnvelope<Unit>>

    @POST("v1/rider/rides/{id}/start")
    suspend fun startRide(@Path("id") rideId: String, @Body request: StartRideRequestDto): Response<ApiEnvelope<Unit>>

    @POST("v1/rider/rides/{id}/complete")
    suspend fun completeRide(@Path("id") rideId: String, @Body request: CompleteRideRequestDto): Response<ApiEnvelope<Unit>>

    /** Cash: the captain's word settles the ride. */
    @POST("v1/rider/rides/{id}/payment/cash-confirm")
    suspend fun confirmCashPayment(@Path("id") rideId: String): Response<ApiEnvelope<Unit>>

    /** UPI/card: polled until the SERVER says paid. The captain never marks an online payment. */
    @GET("v1/rider/rides/{id}/payment")
    suspend fun ridePayment(@Path("id") rideId: String): Response<ApiEnvelope<RidePaymentDto>>

    @GET("v1/rider/partners/me/earnings")
    suspend fun earnings(): Response<ApiEnvelope<CaptainEarningsDto>>

    // --- Onboarding & Profile ---

    /** Carries `review {state, pending}`: the onboarding verdict the app polls after DigiLocker and the selfie. */
    @GET("v1/rider/partners/me")
    suspend fun profile(): Response<ApiEnvelope<PartnerProfileDto>>

    @POST("v1/rider/partners")
    suspend fun createProfile(@Body request: CreatePartnerRequestDto): Response<ApiEnvelope<PartnerProfileDto>>

    @PATCH("v1/rider/partners/me")
    suspend fun updateProfile(@Body request: UpdatePartnerRequestDto): Response<ApiEnvelope<PartnerProfileDto>>

    // --- Documents & Aadhaar KYC ---

    @GET("v1/rider/partners/me/documents")
    suspend fun documents(): Response<ApiEnvelope<List<PartnerDocumentDto>>>

    @POST("v1/rider/partners/me/documents")
    suspend fun submitDocument(@Body request: SubmitDocumentRequestDto): Response<ApiEnvelope<PartnerDocumentDto>>

    @POST("v1/rider/partners/me/aadhaar/start")
    suspend fun startAadhaar(): Response<ApiEnvelope<AadhaarStartResponseDto>>

    @POST("v1/rider/partners/me/aadhaar/callback")
    suspend fun callbackAadhaar(@Body request: AadhaarCallbackRequestDto): Response<ApiEnvelope<PartnerProfileDto>>

    // --- Vehicles ---

    @GET("v1/rider/partners/me/vehicles")
    suspend fun vehicles(): Response<ApiEnvelope<List<VehicleDto>>>

    @POST("v1/rider/partners/me/vehicles")
    suspend fun addVehicle(@Body request: CreateVehicleRequestDto): Response<ApiEnvelope<VehicleDto>>

    @POST("v1/rider/vehicles/{id}/documents")
    suspend fun submitVehicleDocument(
        @Path("id") vehicleId: String,
        @Body request: SubmitDocumentRequestDto,
    ): Response<ApiEnvelope<PartnerDocumentDto>>

    // --- Subscriptions (2026-09-18: checkout through payments-service) ---

    @GET("v1/rider/subscriptions/plans")
    suspend fun subscriptionPlans(): Response<ApiEnvelope<List<SubscriptionPlanDto>>>

    /**
     * Starts a plan. The trial answers `active` with no session; a paid plan
     * answers the sheet's session, and the subscription id is the reference
     * the payment is confirmed under. `POST /subscriptions/payment-proof`
     * answers 410 now and has no client here.
     */
    @POST("v1/rider/subscriptions/checkout")
    suspend fun checkout(@Body request: SubscriptionCheckoutRequestDto): Response<ApiEnvelope<SubscriptionCheckoutResponseDto>>

    /** Polled until the SERVER says paid. Nothing on the device marks a plan paid. */
    @GET("v1/rider/subscriptions/me/payment")
    suspend fun subscriptionPayment(): Response<ApiEnvelope<SubscriptionPaymentDto>>

    @GET("v1/rider/subscriptions/me")
    suspend fun mySubscription(): Response<ApiEnvelope<PartnerSubscriptionDto>>
}
