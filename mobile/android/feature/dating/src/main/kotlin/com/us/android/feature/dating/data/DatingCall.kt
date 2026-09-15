package com.us.android.feature.dating.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.dating.network.ConsentRequiredDetailsDto
import com.us.android.feature.dating.network.DatingErrorEnvelopeDto
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import retrofit2.Response
import java.io.IOException

/** The dating failures a screen renders distinctly. Branch on these and on [code], never on a message. */
sealed interface DatingError {

    /**
     * Dating is not open to this account: the gateway's pilot allowlist answers
     * 404 for every path under `/v1/dating`. Its body has NO `meta` (every
     * dating-service error carries one), or no dating envelope at all.
     */
    data object NotAvailable : DatingError

    /** 422 CONSENT_REQUIRED: ask for [consentType] in flow, then retry. */
    data class ConsentRequired(val consentType: String, val policyVersion: String?) : DatingError

    /** Any other refusal the server explained with a stable code. */
    data class Refused(
        val status: Int,
        val code: String,
        val message: String,
        val details: JsonElement?,
    ) : DatingError

    data object Unauthorized : DatingError

    data class Network(val cause: Throwable?) : DatingError

    data class Unexpected(val status: Int?, val message: String?) : DatingError

    companion object {
        private const val HTTP_UNAUTHORIZED = 401
        private const val HTTP_NOT_FOUND = 404

        /** Maps an HTTP status and the raw error body onto a [DatingError]. Pure. */
        fun from(status: Int, rawBody: String?, json: Json): DatingError {
            val envelope = rawBody?.let {
                runCatching { json.decodeFromString(DatingErrorEnvelopeDto.serializer(), it) }.getOrNull()
            }
            val body = envelope?.error?.takeIf { it.code.isNotBlank() }
            return when {
                status == HTTP_UNAUTHORIZED -> Unauthorized
                // The gateway's pilot gate: a 404 that dating-service did not write.
                status == HTTP_NOT_FOUND && (body == null || envelope.meta == null) -> NotAvailable
                body == null -> Unexpected(status, rawBody?.take(MAX_MESSAGE))
                body.code == CODE_CONSENT_REQUIRED -> {
                    val details = body.details?.let {
                        runCatching { json.decodeFromJsonElement(ConsentRequiredDetailsDto.serializer(), it) }.getOrNull()
                    }
                    if (details != null) {
                        ConsentRequired(details.consentType, details.policyVersion)
                    } else {
                        Refused(status, body.code, body.message, body.details)
                    }
                }
                else -> Refused(status, body.code, body.message, body.details)
            }
        }

        const val CODE_CONSENT_REQUIRED = "CONSENT_REQUIRED"
        private const val MAX_MESSAGE = 200
    }
}

/** The server's stable error code, when the failure carried one. */
val DatingError.code: String?
    get() = when (this) {
        is DatingError.Refused -> code
        is DatingError.ConsentRequired -> DatingError.CODE_CONSENT_REQUIRED
        else -> null
    }

/** Reads a refusal's `details` as [serializer], or null. */
fun <T> DatingError.detailsAs(json: Json, serializer: KSerializer<T>): T? {
    val details = (this as? DatingError.Refused)?.details ?: return null
    return runCatching { json.decodeFromJsonElement(serializer, details) }.getOrNull()
}

/** A result carrying a typed failure rather than a thrown exception. */
sealed interface DatingResult<out T> {
    data class Success<T>(val value: T) : DatingResult<T>
    data class Failure(val error: DatingError) : DatingResult<Nothing>
}

inline fun <T, R> DatingResult<T>.map(transform: (T) -> R): DatingResult<R> = when (this) {
    is DatingResult.Success -> DatingResult.Success(transform(value))
    is DatingResult.Failure -> this
}

fun <T> DatingResult<T>.valueOrNull(): T? = (this as? DatingResult.Success)?.value

/**
 * One enveloped dating-service call. Every expected 4xx/5xx comes back as a
 * [DatingResult.Failure] read from the ERROR body; nothing throws except
 * cancellation. [allowNullData] is for the routes that answer `data:null` for
 * an empty list (`GET /prompts`, `GET /photos`).
 */
suspend fun <T> datingCall(
    json: Json,
    allowNullData: Boolean = false,
    empty: T? = null,
    block: suspend () -> Response<ApiEnvelope<T>>,
): DatingResult<T> = guarded {
    val response = block()
    val data = response.body()?.data
    when {
        response.isSuccessful && data != null -> DatingResult.Success(data)
        response.isSuccessful && allowNullData && empty != null -> DatingResult.Success(empty)
        response.isSuccessful -> DatingResult.Failure(DatingError.Unexpected(response.code(), "2xx response carried no data"))
        else -> DatingResult.Failure(DatingError.from(response.code(), response.errorBody()?.string(), json))
    }
}

/** A call whose success body is NOT the envelope (`GET /pulse/today`). */
suspend fun <T> datingRawCall(json: Json, block: suspend () -> Response<T>): DatingResult<T> = guarded {
    val response = block()
    val body = response.body()
    when {
        response.isSuccessful && body != null -> DatingResult.Success(body)
        response.isSuccessful -> DatingResult.Failure(DatingError.Unexpected(response.code(), "2xx response carried no body"))
        else -> DatingResult.Failure(DatingError.from(response.code(), response.errorBody()?.string(), json))
    }
}

private suspend fun <T> guarded(block: suspend () -> DatingResult<T>): DatingResult<T> = try {
    block()
} catch (e: CancellationException) {
    throw e
} catch (e: IOException) {
    DatingResult.Failure(DatingError.Network(e))
} catch (e: SerializationException) {
    DatingResult.Failure(DatingError.Unexpected(null, e.message))
}
