package com.us.android.core.food.network

import com.us.android.core.network.ApiEnvelope
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path

/**
 * food-service's delivery-partner routes for Feast Rider (handler.go,
 * handler_rider_kyc.go, handler_delivery_offers.go, handler_delivery_proof.go).
 *
 * Every route acts on the CALLER's own partner profile; there is no partner id
 * in any path. The payout account stays on [FoodApi] beside the restaurant one.
 *
 * Assignment steps REQUIRE an `Idempotency-Key` (400 IDEMPOTENCY_KEY_REQUIRED
 * without one). PICKED_UP and DELIVERED are not steps a rider can post: pickup
 * is the kitchen verifying the rider's code, delivery is [verifyDelivery].
 */
@Suppress("TooManyFunctions")
interface RiderApi {

    // Profile and vehicle

    /** 404 FOOD_DELIVERY_PROFILE_NOT_FOUND until the rider has one. */
    @GET("v1/food/delivery/profile")
    suspend fun profile(): Response<ApiEnvelope<DeliveryPartnerDto>>

    /** Creates the profile (status PENDING_REVIEW) — the "become a rider" start. */
    @POST("v1/food/delivery/profile")
    suspend fun createProfile(@Body body: DeliveryPartnerRequest): Response<ApiEnvelope<DeliveryPartnerDto>>

    /** The same upsert as create server-side: every field is replaced. */
    @PATCH("v1/food/delivery/profile")
    suspend fun updateProfile(@Body body: DeliveryPartnerRequest): Response<ApiEnvelope<DeliveryPartnerDto>>

    // Verification (B4)

    @POST("v1/food/delivery/kyc/digilocker/start")
    suspend fun startDigiLocker(): Response<ApiEnvelope<DigiLockerStartDto>>

    @POST("v1/food/delivery/kyc/digilocker/callback")
    suspend fun completeDigiLocker(@Body body: DigiLockerCallbackRequest): Response<ApiEnvelope<DeliveryKycDto>>

    @GET("v1/food/delivery/kyc/status")
    suspend fun kycStatus(): Response<ApiEnvelope<DeliveryKycDto>>

    /** DRIVING_LICENCE, VEHICLE_RC or SELFIE. Aadhaar is refused here (DigiLocker only). */
    @POST("v1/food/delivery/documents")
    suspend fun addDocument(@Body body: DeliveryDocumentRequest): Response<ApiEnvelope<DeliveryDocumentDto>>

    // Duty

    @POST("v1/food/delivery/availability")
    suspend fun setAvailability(@Body body: DeliveryAvailabilityRequest): Response<ApiEnvelope<DeliveryPartnerDto>>

    @POST("v1/food/delivery/location")
    suspend fun postLocation(@Body body: DeliveryLocationRequest): Response<ApiEnvelope<DeliveryLocationDto>>

    // Offers

    @GET("v1/food/delivery/offers/me")
    suspend fun offers(): Response<ApiEnvelope<DeliveryOffersDto>>

    @POST("v1/food/delivery/offers/{offerId}/accept")
    suspend fun acceptOffer(@Path("offerId") offerId: String): Response<ApiEnvelope<OfferResponseDto>>

    @POST("v1/food/delivery/offers/{offerId}/reject")
    suspend fun rejectOffer(
        @Path("offerId") offerId: String,
        @Body body: RejectOfferRequest,
    ): Response<ApiEnvelope<OfferResponseDto>>

    // The job

    /** 404 FOOD_DELIVERY_CURRENT_NOT_FOUND when the rider holds no job. */
    @GET("v1/food/delivery/assignments/current")
    suspend fun currentAssignment(): Response<ApiEnvelope<DeliveryAssignmentDto>>

    /** ASSIGNED → ACCEPTED. The pickup code becomes visible after this. */
    @POST("v1/food/delivery/assignments/{assignmentId}/accept")
    suspend fun acceptAssignment(
        @Path("assignmentId") assignmentId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
    ): Response<ApiEnvelope<DeliveryAssignmentDto>>

    /** ASSIGNED / ACCEPTED / ARRIVED_AT_RESTAURANT → released back to dispatch. */
    @POST("v1/food/delivery/assignments/{assignmentId}/reject")
    suspend fun rejectAssignment(
        @Path("assignmentId") assignmentId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
    ): Response<ApiEnvelope<DeliveryAssignmentDto>>

    /** ACCEPTED → ARRIVED_AT_RESTAURANT. */
    @POST("v1/food/delivery/assignments/{assignmentId}/arrived-restaurant")
    suspend fun arrivedAtRestaurant(
        @Path("assignmentId") assignmentId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
    ): Response<ApiEnvelope<DeliveryAssignmentDto>>

    /** PICKED_UP → ARRIVED_AT_CUSTOMER. */
    @POST("v1/food/delivery/assignments/{assignmentId}/arrived-customer")
    suspend fun arrivedAtCustomer(
        @Path("assignmentId") assignmentId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
    ): Response<ApiEnvelope<DeliveryAssignmentDto>>

    /**
     * The code the CUSTOMER shows, entered by the rider (B5c). 422 wrong code,
     * 429 locked after five, 409 not picked up yet.
     */
    @POST("v1/food/delivery/assignments/{assignmentId}/verify-delivery")
    suspend fun verifyDelivery(
        @Path("assignmentId") assignmentId: String,
        @Body body: DeliveryCodeRequest,
    ): Response<ApiEnvelope<VerifyDeliveryDto>>

    // Earnings

    @GET("v1/food/delivery/earnings")
    suspend fun earnings(): Response<ApiEnvelope<DeliveryEarningsDto>>

    @GET("v1/food/delivery/history")
    suspend fun history(): Response<ApiEnvelope<ItemsDto<DeliveryAssignmentDto>>>
}
