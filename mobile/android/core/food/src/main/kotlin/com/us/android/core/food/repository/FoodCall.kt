package com.us.android.core.food.repository

import com.us.android.core.food.network.FoodErrorEnvelopeDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import retrofit2.Response
import java.io.IOException

/**
 * Runs one food-service call and returns a typed result.
 *
 * Every expected 4xx/5xx comes back as [FoodResult.Failure] with a [FoodError]
 * read from the ERROR body (Retrofit leaves `body()` null on a non-2xx).
 * Nothing here throws except cancellation.
 *
 * Shared by [FoodRepository] and [KitchenRepository] (lifted out in Feast A3) so
 * the two cannot drift on how an error body is read.
 */
suspend fun <T> foodCall(json: Json, block: suspend () -> Response<ApiEnvelope<T>>): FoodResult<T> = try {
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

/** The server's stable error code, when the failure carried one. Branch on this, never on a message. */
val FoodError.code: String?
    get() = when (this) {
        is FoodError.InvalidField -> code
        is FoodError.Unexpected -> code
        else -> null
    }
