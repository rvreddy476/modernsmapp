package com.us.android.feature.mopedu.rider.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.Path

interface MopeduRiderApi {

    @POST("v1/rider/estimate")
    suspend fun getFareEstimate(
        @Body request: EstimateRequestDto,
    ): ApiEnvelope<QuoteSnapshotDto>

    @POST("v1/rider/rides")
    suspend fun createRide(
        @Body request: CreateRideRequestDto,
        @Header("Idempotency-Key") idempotencyKey: String? = null,
    ): ApiEnvelope<RideDto>

    @GET("v1/rider/rides/active")
    suspend fun getActiveRide(): ApiEnvelope<ActiveRideResponseDto>

    @GET("v1/rider/rides/{id}/receipt")
    suspend fun getRideReceipt(
        @Path("id") rideId: String,
    ): ApiEnvelope<RideReceiptDto>

    @POST("v1/rider/rides/{id}/cancel")
    suspend fun cancelRide(
        @Path("id") rideId: String,
        @Body body: Map<String, String>,
    ): ApiEnvelope<Unit>

    @POST("v1/rider/rides/{id}/rate")
    suspend fun rateRide(
        @Path("id") rideId: String,
        @Body request: RateRideRequestDto,
    ): ApiEnvelope<Unit>

    @POST("v1/rider/rides/{id}/sos")
    suspend fun triggerSOS(
        @Path("id") rideId: String,
        @Body request: SOSRequestDto,
    ): ApiEnvelope<Unit>

    @POST("v1/rider/rides/{id}/share")
    suspend fun createShareToken(
        @Path("id") rideId: String,
    ): ApiEnvelope<ShareTokenResponseDto>

    @DELETE("v1/rider/rides/{id}/share")
    suspend fun revokeShareToken(
        @Path("id") rideId: String,
    ): ApiEnvelope<Unit>
}
