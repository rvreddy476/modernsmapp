package com.us.android.feature.doorsteppro.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.intOrNull
import retrofit2.Response
import java.io.IOException

/*
 * doorstep-service calls for the professional. COPIED from :feature:doorstep's
 * data/DoorstepCall.kt (features may not share code) with the professional's
 * codes and wording. Lift both into :core when a third Doorstep client exists.
 */

/** The doorstep-service failures a screen renders distinctly. Branch on [code], never on a message. */
sealed interface ProError {

    /** The server refused with a stable code (DOORSTEP_OTP_INVALID, DOORSTEP_OFFER_TAKEN, …). */
    data class Refused(
        val status: Int,
        val code: String,
        val message: String,
        val details: JsonObject? = null,
    ) : ProError

    data object Unauthorized : ProError

    /**
     * 404 without a Doorstep code: the gateway's dormant-product gate answering
     * a non-pilot account, OR a route doorstep-service has not shipped yet
     * (lanes A4/A5 — see the pending list in the contract test).
     */
    data object NotFound : ProError

    data class Network(val cause: Throwable?) : ProError

    data class Unexpected(val status: Int?, val message: String?) : ProError

    companion object {
        private const val HTTP_UNAUTHORIZED = 401
        private const val HTTP_NOT_FOUND = 404
        private const val MAX_MESSAGE = 200

        /** Maps an HTTP status and the raw error body onto a [ProError]. Pure. */
        fun from(status: Int, rawBody: String?, json: Json): ProError {
            val body = rawBody?.let {
                runCatching { json.decodeFromString(ProErrorEnvelopeDto.serializer(), it) }.getOrNull()
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

/** The server's stable error codes the professional's screens branch on (x-doorstep-error-codes). */
object ProCodes {
    const val INVALID_REQUEST = "DOORSTEP_INVALID_REQUEST"
    const val CONFLICT = "DOORSTEP_CONFLICT"
    const val NOT_FOUND = "DOORSTEP_NOT_FOUND"
    const val INVALID_TRANSITION = "DOORSTEP_INVALID_TRANSITION"
    const val PRO_NOT_FOUND = "DOORSTEP_PRO_NOT_FOUND"
    const val PRO_EXISTS = "DOORSTEP_PRO_EXISTS"
    const val PRO_NOT_APPROVED = "DOORSTEP_PRO_NOT_APPROVED"
    const val PRO_SUSPENDED = "DOORSTEP_PRO_SUSPENDED"
    const val ONBOARDING_INCOMPLETE = "DOORSTEP_ONBOARDING_INCOMPLETE"
    const val BACKGROUND_CHECK_REQUIRED = "DOORSTEP_BACKGROUND_CHECK_REQUIRED"
    const val DIGILOCKER_UNAVAILABLE = "DOORSTEP_DIGILOCKER_UNAVAILABLE"
    const val FACE_MATCH_FAILED = "DOORSTEP_FACE_MATCH_FAILED"
    const val GENDER_RULE = "DOORSTEP_GENDER_RULE"
    const val PII_UNAVAILABLE = "DOORSTEP_PII_UNAVAILABLE"
    const val MEDIA_UNAVAILABLE = "DOORSTEP_MEDIA_UNAVAILABLE"
    const val NOT_ON_DUTY = "DOORSTEP_NOT_ON_DUTY"
    const val OFFER_NOT_FOUND = "DOORSTEP_OFFER_NOT_FOUND"
    const val OFFER_EXPIRED = "DOORSTEP_OFFER_EXPIRED"
    const val OFFER_TAKEN = "DOORSTEP_OFFER_TAKEN"
    const val GEO_CHECK_FAILED = "DOORSTEP_GEO_CHECK_FAILED"
    const val OTP_INVALID = "DOORSTEP_OTP_INVALID"
    const val OTP_LOCKED = "DOORSTEP_OTP_LOCKED"
    const val PHOTOS_REQUIRED = "DOORSTEP_PHOTOS_REQUIRED"
    const val EXTRA_INVALID = "DOORSTEP_EXTRA_INVALID"
    const val EXTRAS_NOT_ALLOWED = "DOORSTEP_EXTRAS_NOT_ALLOWED"
    const val EXTRAS_PENDING = "DOORSTEP_EXTRAS_PENDING"
    const val CANCEL_NOT_ALLOWED = "DOORSTEP_CANCEL_NOT_ALLOWED"
    const val CHAT_CLOSED = "DOORSTEP_CHAT_CLOSED"
    const val RATING_EXISTS = "DOORSTEP_RATING_EXISTS"
    const val RATING_WINDOW_CLOSED = "DOORSTEP_RATING_WINDOW_CLOSED"
}

/** The server's stable error code, when the failure carried one. */
val ProError.code: String?
    get() = (this as? ProError.Refused)?.code

/** `details.field` of a DOORSTEP_INVALID_REQUEST, when the server named the field. */
val ProError.field: String?
    get() = (this as? ProError.Refused)?.details?.get("field")?.let { (it as? JsonPrimitive)?.content }

/** An integer from the refusal's details (attempts_left, distance_m, required, uploaded, …). */
fun ProError.detailInt(key: String): Int? =
    ((this as? ProError.Refused)?.details?.get(key) as? JsonPrimitive)?.intOrNull

/** A string from the refusal's details (locked_until, phase, …). */
fun ProError.detailText(key: String): String? =
    ((this as? ProError.Refused)?.details?.get(key) as? JsonPrimitive)?.takeIf { it.isString }?.content

/**
 * What a professional reads. Server messages are shown for 4xx refusals: they
 * are written for the person in front of the screen. A 5xx message never is.
 */
fun ProError.userMessage(): String = when (this) {
    is ProError.Refused -> when (code) {
        ProCodes.PII_UNAVAILABLE -> "Secure storage for bank and identity details isn't switched on yet. Please try again later."
        ProCodes.MEDIA_UNAVAILABLE -> "Photos can't be checked right now. Please try again in a minute."
        ProCodes.DIGILOCKER_UNAVAILABLE -> "DigiLocker is unavailable right now. Please try again later."
        ProCodes.OFFER_TAKEN -> "Another professional took this job first."
        ProCodes.OFFER_EXPIRED -> "This offer has expired."
        ProCodes.PRO_NOT_APPROVED -> "Your account isn't approved yet. Finish your checklist and wait for review."
        ProCodes.PRO_SUSPENDED -> "Your account is paused. Contact Doorstep support."
        ProCodes.BACKGROUND_CHECK_REQUIRED -> "You need a valid police clearance certificate for this."
        ProCodes.NOT_ON_DUTY -> "You're off duty. Go on duty to share your location."
        ProCodes.CHAT_CLOSED -> "Chat with this customer has closed."
        else -> if (status >= SERVER_ERROR) GENERIC else message.ifBlank { "That didn't work ($code)." }
    }
    ProError.Unauthorized -> "Your session has ended. Please sign in again."
    ProError.NotFound -> "This isn't available on your account yet."
    is ProError.Network -> "You're offline. Check your connection and try again."
    is ProError.Unexpected -> GENERIC
}

private const val SERVER_ERROR = 500
private const val GENERIC = "Something went wrong on our side. Please try again."

/** A result carrying a typed failure rather than a thrown exception. */
sealed interface ProResult<out T> {
    data class Success<T>(val value: T) : ProResult<T>
    data class Failure(val error: ProError) : ProResult<Nothing>
}

inline fun <T, R> ProResult<T>.map(transform: (T) -> R): ProResult<R> = when (this) {
    is ProResult.Success -> ProResult.Success(transform(value))
    is ProResult.Failure -> this
}

fun <T> ProResult<T>.valueOrNull(): T? = (this as? ProResult.Success)?.value

fun <T> ProResult<T>.errorOrNull(): ProError? = (this as? ProResult.Failure)?.error

/**
 * One enveloped doorstep-service call. Every expected 4xx/5xx comes back as a
 * [ProResult.Failure] read from the ERROR body; nothing throws except
 * cancellation.
 */
suspend fun <T> proCall(
    json: Json,
    block: suspend () -> Response<ApiEnvelope<T>>,
): ProResult<T> = guarded {
    val response = block()
    val data = response.body()?.data
    when {
        response.isSuccessful && data != null -> ProResult.Success(data)
        response.isSuccessful -> ProResult.Failure(ProError.Unexpected(response.code(), "2xx response carried no data"))
        else -> ProResult.Failure(ProError.from(response.code(), response.errorBody()?.string(), json))
    }
}

/** A call whose success body is empty (`204`): any 2xx is success. */
suspend fun proUnitCall(json: Json, block: suspend () -> Response<Unit>): ProResult<Unit> = guarded {
    val response = block()
    if (response.isSuccessful) {
        ProResult.Success(Unit)
    } else {
        ProResult.Failure(ProError.from(response.code(), response.errorBody()?.string(), json))
    }
}

private suspend fun <T> guarded(block: suspend () -> ProResult<T>): ProResult<T> = try {
    block()
} catch (e: CancellationException) {
    throw e
} catch (e: IOException) {
    ProResult.Failure(ProError.Network(e))
} catch (e: SerializationException) {
    ProResult.Failure(ProError.Unexpected(null, e.message))
}
