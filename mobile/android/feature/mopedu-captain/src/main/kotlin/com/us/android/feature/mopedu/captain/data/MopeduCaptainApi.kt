package com.us.android.feature.mopedu.captain.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path

interface MopeduCaptainApi {

    @POST("v1/rider/partners/me/online")
    suspend fun goOnline(): ApiEnvelope<Unit>

    @POST("v1/rider/partners/me/offline")
    suspend fun goOffline(): ApiEnvelope<Unit>

    @POST("v1/rider/partners/me/location")
    suspend fun updateLocation(
        @Body location: LocationTelemetryDto,
    ): ApiEnvelope<Unit>

    @GET("v1/rider/offers/incoming")
    suspend fun getIncomingOffers(): ApiEnvelope<List<CaptainOfferDto>>

    @POST("v1/rider/offers/{id}/accept")
    suspend fun acceptOffer(
        @Path("id") offerId: String,
    ): ApiEnvelope<AcceptOfferResponseDto>

    @POST("v1/rider/offers/{id}/reject")
    suspend fun rejectOffer(
        @Path("id") offerId: String,
    ): ApiEnvelope<Unit>

    @POST("v1/rider/rides/{id}/arriving")
    suspend fun markArriving(
        @Path("id") rideId: String,
    ): ApiEnvelope<Unit>

    @POST("v1/rider/rides/{id}/arrived")
    suspend fun markArrived(
        @Path("id") rideId: String,
    ): ApiEnvelope<Unit>

    @POST("v1/rider/rides/{id}/start")
    suspend fun startRide(
        @Path("id") rideId: String,
        @Body request: StartRideRequestDto,
    ): ApiEnvelope<Unit>

    @POST("v1/rider/rides/{id}/complete")
    suspend fun completeRide(
        @Path("id") rideId: String,
        @Body request: CompleteRideRequestDto,
    ): ApiEnvelope<Unit>

    @POST("v1/rider/rides/{id}/payment/cash-confirm")
    suspend fun confirmCashPayment(
        @Path("id") rideId: String,
    ): ApiEnvelope<Unit>

    @GET("v1/rider/partners/me/earnings")
    suspend fun getEarnings(): ApiEnvelope<CaptainEarningsDto>

    // --- Onboarding & Profile ---

    @GET("v1/rider/partners/me")
    suspend fun getProfile(): ApiEnvelope<PartnerProfileDto>

    @POST("v1/rider/partners")
    suspend fun createProfile(
        @Body request: CreatePartnerRequestDto,
    ): ApiEnvelope<PartnerProfileDto>

    @retrofit2.http.PATCH("v1/rider/partners/me")
    suspend fun updateProfile(
        @Body request: UpdatePartnerRequestDto,
    ): ApiEnvelope<PartnerProfileDto>

    // --- Documents & Aadhaar KYC ---

    @GET("v1/rider/partners/me/documents")
    suspend fun getDocuments(): ApiEnvelope<List<PartnerDocumentDto>>

    @POST("v1/rider/partners/me/documents")
    suspend fun submitDocument(
        @Body request: SubmitDocumentRequestDto,
    ): ApiEnvelope<PartnerDocumentDto>

    @POST("v1/rider/partners/me/aadhaar/start")
    suspend fun startAadhaar(): ApiEnvelope<AadhaarStartResponseDto>

    @POST("v1/rider/partners/me/aadhaar/callback")
    suspend fun callbackAadhaar(
        @Body request: AadhaarCallbackRequestDto,
    ): ApiEnvelope<PartnerProfileDto>

    // --- Vehicles ---

    @GET("v1/rider/partners/me/vehicles")
    suspend fun getVehicles(): ApiEnvelope<List<VehicleDto>>

    @POST("v1/rider/partners/me/vehicles")
    suspend fun addVehicle(
        @Body request: CreateVehicleRequestDto,
    ): ApiEnvelope<VehicleDto>

    @POST("v1/rider/vehicles/{id}/documents")
    suspend fun submitVehicleDocument(
        @Path("id") vehicleId: String,
        @Body request: SubmitDocumentRequestDto,
    ): ApiEnvelope<PartnerDocumentDto>

    // --- Subscriptions ---

    @GET("v1/rider/subscriptions/plans")
    suspend fun getSubscriptionPlans(): ApiEnvelope<List<SubscriptionPlanDto>>

    @POST("v1/rider/subscriptions/subscribe")
    suspend fun subscribe(
        @Body request: SubscribeRequestDto,
    ): ApiEnvelope<SubscribeResponseDto>

    @POST("v1/rider/subscriptions/payment-proof")
    suspend fun submitPaymentProof(
        @Body request: PaymentProofRequestDto,
    ): ApiEnvelope<Unit>

    @GET("v1/rider/subscriptions/me")
    suspend fun getMySubscription(): ApiEnvelope<PartnerSubscriptionDto>
}

