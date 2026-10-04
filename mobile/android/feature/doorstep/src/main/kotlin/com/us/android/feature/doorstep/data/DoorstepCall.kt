package com.us.android.feature.doorstep.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import retrofit2.Response
import java.io.IOException

/** The doorstep-service failures a screen renders distinctly. Branch on [code], never on a message. */
sealed interface DoorstepError {

    /** The server refused with a stable code (DOORSTEP_SLOT_TAKEN, DOORSTEP_OUTSTANDING_DUE, …). */
    data class Refused(
        val status: Int,
        val code: String,
        val message: String,
        val details: JsonObject? = null,
    ) : DoorstepError

    data object Unauthorized : DoorstepError

    /** 404 without a Doorstep code — the gateway's dormant-product gate answers this to a non-pilot account. */
    data object NotFound : DoorstepError

    data class Network(val cause: Throwable?) : DoorstepError

    data class Unexpected(val status: Int?, val message: String?) : DoorstepError

    companion object {
        private const val HTTP_UNAUTHORIZED = 401
        private const val HTTP_NOT_FOUND = 404
        private const val MAX_MESSAGE = 200

        /** Maps an HTTP status and the raw error body onto a [DoorstepError]. Pure. */
        fun from(status: Int, rawBody: String?, json: Json): DoorstepError {
            val body = rawBody?.let {
                runCatching { json.decodeFromString(DoorstepErrorEnvelopeDto.serializer(), it) }.getOrNull()
            }?.error?.takeIf { it.code.isNotBlank() }
            return when {
                status == HTTP_UNAUTHORIZED -> Unauthorized
                body != null -> Refused(status, body.code, body.message, body.details)
                status == HTTP_NOT_FOUND -> NotFound
                else -> Unexpected(status, rawBody?.take(MAX_MESSAGE))
            }
        }
    }
}

/** The server's stable error codes the screens branch on (x-doorstep-error-codes). */
object DoorstepCodes {
    const val CITY_NOT_FOUND = "DOORSTEP_CITY_NOT_FOUND"
    const val OUTSIDE_SERVICE_AREA = "DOORSTEP_OUTSIDE_SERVICE_AREA"
    const val ADDON_INVALID = "DOORSTEP_ADDON_INVALID"
    const val QUOTE_EXPIRED = "DOORSTEP_QUOTE_EXPIRED"
    const val SLOT_UNAVAILABLE = "DOORSTEP_SLOT_UNAVAILABLE"
    const val SLOT_TAKEN = "DOORSTEP_SLOT_TAKEN"
    const val HOLD_EXPIRED = "DOORSTEP_HOLD_EXPIRED"
    const val OUTSTANDING_DUE = "DOORSTEP_OUTSTANDING_DUE"
    const val PAYMENT_ALREADY_SETTLED = "DOORSTEP_PAYMENT_ALREADY_SETTLED"
    const val PAYMENTS_UNAVAILABLE = "DOORSTEP_PAYMENTS_UNAVAILABLE"
    const val CANCEL_NOT_ALLOWED = "DOORSTEP_CANCEL_NOT_ALLOWED"
    const val RESCHEDULE_NOT_ALLOWED = "DOORSTEP_RESCHEDULE_NOT_ALLOWED"
    const val RATING_EXISTS = "DOORSTEP_RATING_EXISTS"
    const val REWORK_WINDOW_CLOSED = "DOORSTEP_REWORK_WINDOW_CLOSED"
    const val INVALID_REQUEST = "DOORSTEP_INVALID_REQUEST"
    const val BOOKING_NOT_FOUND = "DOORSTEP_BOOKING_NOT_FOUND"
    const val INVALID_TRANSITION = "DOORSTEP_INVALID_TRANSITION"

    /** The dev stub-confirm route outside a development stack. */
    const val NOT_FOUND = "DOORSTEP_NOT_FOUND"

    /** The dev stub-confirm route while payments-service has a real provider: pay through checkout. */
    const val STUB_UNAVAILABLE = "DOORSTEP_STUB_UNAVAILABLE"
}

/** The server's stable error code, when the failure carried one. */
val DoorstepError.code: String?
    get() = (this as? DoorstepError.Refused)?.code

/** What a screen says. Server messages are shown for refusals: they are written for the customer. */
fun DoorstepError.userMessage(): String = when (this) {
    is DoorstepError.Refused -> when (code) {
        DoorstepCodes.OUTSTANDING_DUE -> "Clear your pending dues to book again."
        DoorstepCodes.SLOT_TAKEN -> "That slot was just taken. Please pick another time."
        DoorstepCodes.HOLD_EXPIRED -> "Your 10-minute hold ended. Please pick a slot again."
        DoorstepCodes.QUOTE_EXPIRED -> "Prices were refreshed. Please review and pick a slot again."
        DoorstepCodes.PAYMENTS_UNAVAILABLE -> "Payments are unavailable right now. Please try again shortly."
        else -> message.ifBlank { "That didn't work ($code)." }
    }
    DoorstepError.Unauthorized -> "Please sign in again."
    DoorstepError.NotFound -> "Doorstep isn't available on your account yet."
    is DoorstepError.Network -> "You're offline. Check your connection and try again."
    is DoorstepError.Unexpected -> "Something went wrong. Please try again."
}

/** A result carrying a typed failure rather than a thrown exception. */
sealed interface DoorstepResult<out T> {
    data class Success<T>(val value: T) : DoorstepResult<T>
    data class Failure(val error: DoorstepError) : DoorstepResult<Nothing>
}

inline fun <T, R> DoorstepResult<T>.map(transform: (T) -> R): DoorstepResult<R> = when (this) {
    is DoorstepResult.Success -> DoorstepResult.Success(transform(value))
    is DoorstepResult.Failure -> this
}

fun <T> DoorstepResult<T>.valueOrNull(): T? = (this as? DoorstepResult.Success)?.value

fun <T> DoorstepResult<T>.errorOrNull(): DoorstepError? = (this as? DoorstepResult.Failure)?.error

/**
 * One enveloped doorstep-service call. Every expected 4xx/5xx comes back as a
 * [DoorstepResult.Failure] read from the ERROR body; nothing throws except
 * cancellation.
 */
suspend fun <T> doorstepCall(
    json: Json,
    block: suspend () -> Response<ApiEnvelope<T>>,
): DoorstepResult<T> = guarded {
    val response = block()
    val data = response.body()?.data
    when {
        response.isSuccessful && data != null -> DoorstepResult.Success(data)
        response.isSuccessful -> DoorstepResult.Failure(DoorstepError.Unexpected(response.code(), "2xx response carried no data"))
        else -> DoorstepResult.Failure(DoorstepError.from(response.code(), response.errorBody()?.string(), json))
    }
}

/** A call whose success body is empty (`204`): any 2xx is success. */
suspend fun doorstepUnitCall(json: Json, block: suspend () -> Response<Unit>): DoorstepResult<Unit> = guarded {
    val response = block()
    if (response.isSuccessful) {
        DoorstepResult.Success(Unit)
    } else {
        DoorstepResult.Failure(DoorstepError.from(response.code(), response.errorBody()?.string(), json))
    }
}

private suspend fun <T> guarded(block: suspend () -> DoorstepResult<T>): DoorstepResult<T> = try {
    block()
} catch (e: CancellationException) {
    throw e
} catch (e: IOException) {
    DoorstepResult.Failure(DoorstepError.Network(e))
} catch (e: SerializationException) {
    DoorstepResult.Failure(DoorstepError.Unexpected(null, e.message))
}
