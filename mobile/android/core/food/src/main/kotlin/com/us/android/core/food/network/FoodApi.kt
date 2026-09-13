package com.us.android.core.food.network

import com.us.android.core.network.ApiEnvelope
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path

/**
 * food-service endpoints used by the partner apps' onboarding, plus the
 * capabilities gate and the realtime token.
 *
 * Routes from handler.go / handler_onboarding.go. Partner routes act only on a
 * restaurant the caller owns; anything else is a 404 FOOD_NOT_FOUND, by design
 * indistinguishable from a missing restaurant.
 *
 * Every call returns `Response<…>` so [com.us.android.core.food.repository.FoodRepository]
 * reads the typed error from the error body instead of catching HttpException.
 */
@Suppress("TooManyFunctions")
interface FoodApi {

    @GET("v1/food/me/capabilities")
    suspend fun capabilities(): Response<ApiEnvelope<FoodCapabilitiesDto>>

    /** One HMAC topic token for every topic the caller owns (a scoped variant is planned). */
    @POST("v1/food/realtime/token")
    suspend fun realtimeToken(): Response<ApiEnvelope<RealtimeTokenDto>>

    // Restaurant onboarding (B1)

    @PUT("v1/food/partner/restaurants/{restaurantId}/compliance")
    suspend fun putCompliance(
        @Path("restaurantId") restaurantId: String,
        @Body body: ComplianceRequest,
    ): Response<ApiEnvelope<ComplianceDto>>

    @PUT("v1/food/partner/restaurants/{restaurantId}/location")
    suspend fun putLocation(
        @Path("restaurantId") restaurantId: String,
        @Body body: LocationRequest,
    ): Response<ApiEnvelope<LocationDto>>

    /** Replaces the whole week. */
    @PUT("v1/food/partner/restaurants/{restaurantId}/operating-hours")
    suspend fun putOperatingHours(
        @Path("restaurantId") restaurantId: String,
        @Body body: OperatingHoursRequest,
    ): Response<ApiEnvelope<OperatingHoursDto>>

    @PATCH("v1/food/partner/restaurants/{restaurantId}/accepting")
    suspend fun patchAccepting(
        @Path("restaurantId") restaurantId: String,
        @Body body: AcceptingRequest,
    ): Response<ApiEnvelope<AcceptingDto>>

    @PUT("v1/food/partner/restaurants/{restaurantId}/fssai")
    suspend fun putFssai(
        @Path("restaurantId") restaurantId: String,
        @Body body: FssaiRequest,
    ): Response<ApiEnvelope<FssaiDto>>

    /** 422 FOOD_RESTAURANT_NOT_READY with `details.missing` until every step is done. */
    @POST("v1/food/partner/restaurants/{restaurantId}/submit")
    suspend fun submit(
        @Path("restaurantId") restaurantId: String,
    ): Response<ApiEnvelope<SubmitDto>>

    // Payout accounts (B2)

    @PUT("v1/food/partner/restaurants/{restaurantId}/payout-account")
    suspend fun putRestaurantPayoutAccount(
        @Path("restaurantId") restaurantId: String,
        @Body body: PayoutAccountRequest,
    ): Response<ApiEnvelope<PayoutAccountDto>>

    @GET("v1/food/partner/restaurants/{restaurantId}/payout-account")
    suspend fun getRestaurantPayoutAccount(
        @Path("restaurantId") restaurantId: String,
    ): Response<ApiEnvelope<PayoutAccountDto>>

    @PUT("v1/food/delivery/payout-account")
    suspend fun putDeliveryPayoutAccount(
        @Body body: PayoutAccountRequest,
    ): Response<ApiEnvelope<PayoutAccountDto>>

    @GET("v1/food/delivery/payout-account")
    suspend fun getDeliveryPayoutAccount(): Response<ApiEnvelope<PayoutAccountDto>>
}
