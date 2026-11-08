package com.us.android.core.food.repository

import com.us.android.core.food.network.FoodErrorEnvelopeDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import retrofit2.Response
import java.io.IOException
import java.time.Instant

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

/**
 * Whether a failed request may still have taken effect: the connection dropped,
 * a gateway or the service answered 5xx, a 2xx could not be read, or the server
 * is still running the first request under the same Idempotency-Key. A retry
 * then reuses the SAME key; only a definite 4xx refusal frees it.
 */
val FoodError.outcomeUnknown: Boolean
    get() = when (this) {
        is FoodError.Network -> true
        is FoodError.Unexpected ->
            status == null ||
                status >= HTTP_SERVER_ERROR ||
                status in HTTP_OK_FIRST..HTTP_OK_LAST ||
                code == IDEMPOTENCY_IN_PROGRESS
        else -> false
    }

private const val HTTP_SERVER_ERROR = 500
private const val HTTP_OK_FIRST = 200
private const val HTTP_OK_LAST = 299
private const val IDEMPOTENCY_IN_PROGRESS = "FOOD_IDEMPOTENCY_IN_PROGRESS"

/** A response value with the server's own clock reading when it answered. */
data class ServerTimed<T>(
    val value: T,
    /** The HTTP `Date` header (one-second resolution); null when the response carried none. */
    val serverTime: Instant?,
)

/**
 * [foodCall], keeping the response's `Date` header: a countdown to a server
 * deadline is measured on the server's clock, never the device's (food audit L1).
 */
suspend fun <T> foodCallTimed(json: Json, block: suspend () -> Response<ApiEnvelope<T>>): FoodResult<ServerTimed<T>> {
    var serverTime: Instant? = null
    return foodCall(json) {
        block().also { response -> serverTime = response.headers().getDate("Date")?.toInstant() }
    }.map { ServerTimed(it, serverTime) }
}
