package com.us.android.core.food.repository

import com.us.android.core.food.model.FoodCapabilities
import com.us.android.core.food.network.AcceptingDto
import com.us.android.core.food.network.AcceptingRequest
import com.us.android.core.food.network.ComplianceDto
import com.us.android.core.food.network.ComplianceRequest
import com.us.android.core.food.network.FoodApi
import com.us.android.core.food.network.FoodErrorEnvelopeDto
import com.us.android.core.food.network.FssaiDto
import com.us.android.core.food.network.FssaiRequest
import com.us.android.core.food.network.LocationDto
import com.us.android.core.food.network.LocationRequest
import com.us.android.core.food.network.OperatingHoursDto
import com.us.android.core.food.network.OperatingHoursRequest
import com.us.android.core.food.network.PayoutAccountDto
import com.us.android.core.food.network.PayoutAccountRequest
import com.us.android.core.food.network.RealtimeTokenDto
import com.us.android.core.food.network.SubmitDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import retrofit2.Response
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The food onboarding data layer.
 *
 * Every expected 4xx/5xx comes back as [FoodResult.Failure] with a typed
 * [FoodError] read from the ERROR body (Retrofit leaves `body()` null on a
 * non-2xx). Nothing here throws except cancellation.
 *
 * The onboarding responses are returned as their DTOs: they are already typed
 * and masked, and the partner screens that shape them arrive in A3/A4.
 */
@Suppress("TooManyFunctions")
@Singleton
class FoodRepository @Inject constructor(
    private val api: FoodApi,
    private val json: Json,
) {

    suspend fun capabilities(): FoodResult<FoodCapabilities> = call { api.capabilities() }.map {
        FoodCapabilities(
            userId = it.userId,
            isCustomer = it.isCustomer,
            isRestaurantOwner = it.isRestaurantOwner,
            isDeliveryPartner = it.isDeliveryPartner,
            isAdmin = it.isAdmin,
            isModerator = it.isModerator,
        )
    }

    suspend fun realtimeToken(): FoodResult<RealtimeTokenDto> = call { api.realtimeToken() }

    suspend fun putCompliance(restaurantId: String, request: ComplianceRequest): FoodResult<ComplianceDto> =
        call { api.putCompliance(restaurantId, request) }

    suspend fun putLocation(restaurantId: String, request: LocationRequest): FoodResult<LocationDto> =
        call { api.putLocation(restaurantId, request) }

    suspend fun putOperatingHours(
        restaurantId: String,
        request: OperatingHoursRequest,
    ): FoodResult<OperatingHoursDto> = call { api.putOperatingHours(restaurantId, request) }

    suspend fun setAccepting(restaurantId: String, accepting: Boolean): FoodResult<AcceptingDto> =
        call { api.patchAccepting(restaurantId, AcceptingRequest(accepting)) }

    suspend fun putFssai(restaurantId: String, request: FssaiRequest): FoodResult<FssaiDto> =
        call { api.putFssai(restaurantId, request) }

    /** Failure(NotReady) carries `missing[]` and its checklist. */
    suspend fun submit(restaurantId: String): FoodResult<SubmitDto> = call { api.submit(restaurantId) }

    suspend fun putRestaurantPayoutAccount(
        restaurantId: String,
        request: PayoutAccountRequest,
    ): FoodResult<PayoutAccountDto> = call { api.putRestaurantPayoutAccount(restaurantId, request) }

    /** Failure(NotFound) when no account has been added yet. */
    suspend fun getRestaurantPayoutAccount(restaurantId: String): FoodResult<PayoutAccountDto> =
        call { api.getRestaurantPayoutAccount(restaurantId) }

    suspend fun putDeliveryPayoutAccount(request: PayoutAccountRequest): FoodResult<PayoutAccountDto> =
        call { api.putDeliveryPayoutAccount(request) }

    suspend fun getDeliveryPayoutAccount(): FoodResult<PayoutAccountDto> = call { api.getDeliveryPayoutAccount() }

    private suspend fun <T> call(block: suspend () -> Response<ApiEnvelope<T>>): FoodResult<T> = try {
        val response = block()
        val data = response.body()?.data
        when {
            response.isSuccessful && data != null -> FoodResult.Success(data)
            response.isSuccessful -> FoodResult.Failure(
                FoodError.Unexpected(response.code(), null, "2xx response carried no data"),
            )
            else -> {
                val raw = response.errorBody()?.string()
                val body = raw?.let {
                    runCatching { json.decodeFromString(FoodErrorEnvelopeDto.serializer(), it) }.getOrNull()
                }?.error
                FoodResult.Failure(FoodError.from(response.code(), body))
            }
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: IOException) {
        FoodResult.Failure(FoodError.Network(e))
    } catch (e: SerializationException) {
        FoodResult.Failure(FoodError.Unexpected(null, "MALFORMED_RESPONSE", e.message))
    }
}
