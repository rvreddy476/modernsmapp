package com.us.android.feature.mopedu.rider.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import retrofit2.Response
import java.io.IOException

/** The rider-service failures a screen renders distinctly. Branch on these and on [code], never on a message. */
sealed interface MopeduError {

    /** The server refused with a stable code (a 422 invalid coupon, a 409 ride state, …). */
    data class Refused(val status: Int, val code: String, val message: String) : MopeduError

    data object Unauthorized : MopeduError

    /** 404: no such ride, no active ride, or Mopedu not open to this account. */
    data object NotFound : MopeduError

    data class Network(val cause: Throwable?) : MopeduError

    data class Unexpected(val status: Int?, val message: String?) : MopeduError

    companion object {
        private const val HTTP_UNAUTHORIZED = 401
        private const val HTTP_NOT_FOUND = 404
        private const val MAX_MESSAGE = 200

        /** Maps an HTTP status and the raw error body onto a [MopeduError]. Pure. */
        fun from(status: Int, rawBody: String?, json: Json): MopeduError {
            val body = rawBody?.let {
                runCatching { json.decodeFromString(MopeduErrorEnvelopeDto.serializer(), it) }.getOrNull()
            }?.error?.takeIf { it.code.isNotBlank() }
            return when {
                status == HTTP_UNAUTHORIZED -> Unauthorized
                body != null -> Refused(status, body.code, body.message)
                status == HTTP_NOT_FOUND -> NotFound
                else -> Unexpected(status, rawBody?.take(MAX_MESSAGE))
            }
        }
    }
}

/** The server's stable error code, when the failure carried one. */
val MopeduError.code: String?
    get() = (this as? MopeduError.Refused)?.code

/** What a screen says. Server messages are shown for refusals: they are written for the customer. */
fun MopeduError.userMessage(): String = when (this) {
    is MopeduError.Refused -> message.ifBlank { "That didn't work ($code)." }
    MopeduError.Unauthorized -> "Please sign in again."
    MopeduError.NotFound -> "We couldn't find that ride."
    is MopeduError.Network -> "You're offline. Check your connection and try again."
    is MopeduError.Unexpected -> "Something went wrong. Please try again."
}

/** A result carrying a typed failure rather than a thrown exception. */
sealed interface MopeduResult<out T> {
    data class Success<T>(val value: T) : MopeduResult<T>
    data class Failure(val error: MopeduError) : MopeduResult<Nothing>
}

inline fun <T, R> MopeduResult<T>.map(transform: (T) -> R): MopeduResult<R> = when (this) {
    is MopeduResult.Success -> MopeduResult.Success(transform(value))
    is MopeduResult.Failure -> this
}

fun <T> MopeduResult<T>.valueOrNull(): T? = (this as? MopeduResult.Success)?.value

fun <T> MopeduResult<T>.errorOrNull(): MopeduError? = (this as? MopeduResult.Failure)?.error

/**
 * One enveloped rider-service call. Every expected 4xx/5xx comes back as a
 * [MopeduResult.Failure] read from the ERROR body; nothing throws except
 * cancellation. [empty] stands in for a 2xx whose `data` is null (an empty list).
 */
suspend fun <T> mopeduCall(
    json: Json,
    empty: T? = null,
    block: suspend () -> Response<ApiEnvelope<T>>,
): MopeduResult<T> = guarded {
    val response = block()
    val data = response.body()?.data
    when {
        response.isSuccessful && data != null -> MopeduResult.Success(data)
        response.isSuccessful && empty != null -> MopeduResult.Success(empty)
        response.isSuccessful -> MopeduResult.Failure(MopeduError.Unexpected(response.code(), "2xx response carried no data"))
        else -> MopeduResult.Failure(MopeduError.from(response.code(), response.errorBody()?.string(), json))
    }
}

/** A call whose success body is irrelevant (`cancel`, `rate`, `sos`): any 2xx is success. */
suspend fun <T> mopeduUnitCall(json: Json, block: suspend () -> Response<ApiEnvelope<T>>): MopeduResult<Unit> = guarded {
    val response = block()
    if (response.isSuccessful) {
        MopeduResult.Success(Unit)
    } else {
        MopeduResult.Failure(MopeduError.from(response.code(), response.errorBody()?.string(), json))
    }
}

private suspend fun <T> guarded(block: suspend () -> MopeduResult<T>): MopeduResult<T> = try {
    block()
} catch (e: CancellationException) {
    throw e
} catch (e: IOException) {
    MopeduResult.Failure(MopeduError.Network(e))
} catch (e: SerializationException) {
    MopeduResult.Failure(MopeduError.Unexpected(null, e.message))
}
