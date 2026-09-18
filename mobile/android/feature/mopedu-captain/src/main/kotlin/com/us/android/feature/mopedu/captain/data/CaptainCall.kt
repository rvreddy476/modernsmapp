package com.us.android.feature.mopedu.captain.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import retrofit2.Response
import java.io.IOException

/**
 * The rider-service failures a captain screen renders distinctly. The same
 * shape as the rider feature's MopeduError (features may not share code).
 */
sealed interface CaptainError {
    data class Refused(val status: Int, val code: String, val message: String) : CaptainError

    data object Unauthorized : CaptainError

    /** 404: no partner profile yet, no such ride, or Mopedu not open to this account. */
    data object NotFound : CaptainError

    data class Network(val cause: Throwable?) : CaptainError

    data class Unexpected(val status: Int?, val message: String?) : CaptainError

    companion object {
        private const val HTTP_UNAUTHORIZED = 401
        private const val HTTP_NOT_FOUND = 404
        private const val MAX_MESSAGE = 200

        fun from(status: Int, rawBody: String?, json: Json): CaptainError {
            val body = rawBody?.let {
                runCatching { json.decodeFromString(CaptainErrorEnvelopeDto.serializer(), it) }.getOrNull()
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

val CaptainError.code: String?
    get() = (this as? CaptainError.Refused)?.code

fun CaptainError.userMessage(): String = when (this) {
    is CaptainError.Refused -> message.ifBlank { "That didn't work ($code)." }
    CaptainError.Unauthorized -> "Please sign in again."
    CaptainError.NotFound -> "We couldn't find that."
    is CaptainError.Network -> "You're offline. Check your connection and try again."
    is CaptainError.Unexpected -> "Something went wrong. Please try again."
}

sealed interface CaptainResult<out T> {
    data class Success<T>(val value: T) : CaptainResult<T>
    data class Failure(val error: CaptainError) : CaptainResult<Nothing>
}

inline fun <T, R> CaptainResult<T>.map(transform: (T) -> R): CaptainResult<R> = when (this) {
    is CaptainResult.Success -> CaptainResult.Success(transform(value))
    is CaptainResult.Failure -> this
}

fun <T> CaptainResult<T>.valueOrNull(): T? = (this as? CaptainResult.Success)?.value

/** One enveloped call: every expected 4xx/5xx is a typed failure; nothing throws except cancellation. */
suspend fun <T> captainCall(
    json: Json,
    empty: T? = null,
    block: suspend () -> Response<ApiEnvelope<T>>,
): CaptainResult<T> = guarded {
    val response = block()
    val data = response.body()?.data
    when {
        response.isSuccessful && data != null -> CaptainResult.Success(data)
        response.isSuccessful && empty != null -> CaptainResult.Success(empty)
        response.isSuccessful -> CaptainResult.Failure(CaptainError.Unexpected(response.code(), "2xx response carried no data"))
        else -> CaptainResult.Failure(CaptainError.from(response.code(), response.errorBody()?.string(), json))
    }
}

/** A call whose success body is irrelevant: any 2xx is success. */
suspend fun <T> captainUnitCall(json: Json, block: suspend () -> Response<ApiEnvelope<T>>): CaptainResult<Unit> = guarded {
    val response = block()
    if (response.isSuccessful) {
        CaptainResult.Success(Unit)
    } else {
        CaptainResult.Failure(CaptainError.from(response.code(), response.errorBody()?.string(), json))
    }
}

private suspend fun <T> guarded(block: suspend () -> CaptainResult<T>): CaptainResult<T> = try {
    block()
} catch (e: CancellationException) {
    throw e
} catch (e: IOException) {
    CaptainResult.Failure(CaptainError.Network(e))
} catch (e: SerializationException) {
    CaptainResult.Failure(CaptainError.Unexpected(null, e.message))
}
