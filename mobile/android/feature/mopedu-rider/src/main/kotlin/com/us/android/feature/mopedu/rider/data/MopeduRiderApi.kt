package com.us.android.feature.mopedu.rider.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * rider-service's customer routes, on the platform's shared Retrofit client.
 * Every call returns the raw [Response] so [mopeduCall] can read the error
 * envelope of a 4xx instead of throwing.
 */
@Suppress("TooManyFunctions")
interface MopeduRiderApi {

    @POST("v1/rider/estimate")
    suspend fun estimate(@Body request: EstimateRequestDto): Response<ApiEnvelope<QuoteSnapshotDto>>

    /** 200 with the coupon, or a 422 error envelope when it does not apply. */
    @GET("v1/rider/coupons/validate")
    suspend fun validateCoupon(
        @Query("code") code: String,
        @Query("city_id") cityId: String?,
        @Query("vehicle_type") vehicleType: String?,
    ): Response<ApiEnvelope<CouponValidationDto>>

    @POST("v1/rider/rides")
    suspend fun createRide(
        @Body request: CreateRideRequestDto,
        @Header("Idempotency-Key") idempotencyKey: String,
    ): Response<ApiEnvelope<RideDto>>

    @GET("v1/rider/rides/active")
    suspend fun activeRide(): Response<ApiEnvelope<ActiveRideResponseDto>>

    /** The customer's past rides, newest first. */
    @GET("v1/rider/rides/me")
    suspend fun rides(@Query("limit") limit: Int): Response<ApiEnvelope<List<RideDto>>>

    @GET("v1/rider/rides/{id}/receipt")
    suspend fun receipt(@Path("id") rideId: String): Response<ApiEnvelope<RideReceiptDto>>

    @POST("v1/rider/rides/{id}/cancel")
    suspend fun cancelRide(@Path("id") rideId: String, @Body body: CancelRideRequestDto): Response<ApiEnvelope<RideDto>>

    @POST("v1/rider/rides/{id}/rate")
    suspend fun rateRide(@Path("id") rideId: String, @Body request: RateRideRequestDto): Response<ApiEnvelope<Unit>>

    @POST("v1/rider/rides/{id}/sos")
    suspend fun triggerSOS(@Path("id") rideId: String, @Body request: SOSRequestDto): Response<ApiEnvelope<Unit>>

    @POST("v1/rider/rides/{id}/share")
    suspend fun createShareToken(@Path("id") rideId: String): Response<ApiEnvelope<ShareTokenResponseDto>>

    @DELETE("v1/rider/rides/{id}/share")
    suspend fun revokeShareToken(@Path("id") rideId: String): Response<ApiEnvelope<Unit>>

    // Payment (the pricing/payments lane, 2026-09-18)

    /** After `completed`, for upi/card: the server creates the provider order and returns the sheet session. */
    @POST("v1/rider/rides/{id}/payment/intent")
    suspend fun paymentIntent(@Path("id") rideId: String, @Body body: PaymentIntentRequestDto): Response<ApiEnvelope<PaymentIntentDto>>

    /** The ONLY source of "paid": polled by the status source until the server settles it. */
    @GET("v1/rider/rides/{id}/payment")
    suspend fun ridePayment(@Path("id") rideId: String): Response<ApiEnvelope<RidePaymentDto>>

    /** The fallback when the sheet fails: the fare is collected in cash by the captain. */
    @POST("v1/rider/rides/{id}/payment/switch-to-cash")
    suspend fun switchToCash(@Path("id") rideId: String): Response<ApiEnvelope<RidePaymentDto>>

    @GET("v1/rider/me/outstanding")
    suspend fun outstanding(): Response<ApiEnvelope<List<OutstandingChargeDto>>>

    @POST("v1/rider/me/outstanding/{id}/payment/intent")
    suspend fun outstandingPaymentIntent(
        @Path("id") chargeId: String,
        @Body body: PaymentIntentRequestDto,
    ): Response<ApiEnvelope<PaymentIntentDto>>
}
