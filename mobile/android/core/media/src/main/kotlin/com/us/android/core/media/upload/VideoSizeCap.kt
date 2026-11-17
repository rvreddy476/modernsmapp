package com.us.android.core.media.upload

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import retrofit2.HttpException

/**
 * media-service's refusal of a video original over the largest one its
 * transcode worker takes (2026-11-07, 16 GiB by default). Answered at `init`
 * on BOTH upload routes, before a byte is sent, as HTTP 413 with
 * `details.max_bytes`. Not a request to switch routes: the resumable route
 * refuses it the same way.
 */
const val CODE_VIDEO_TOO_LARGE = "VIDEO_TOO_LARGE"

/** The cap, in bytes, a [CODE_VIDEO_TOO_LARGE] refusal carries. */
const val DETAIL_MAX_BYTES = "max_bytes"

/** A video the platform will not take, and the cap the server named (null when it named none). */
data class VideoTooLarge(val maxBytes: Long?) {
    /** What the upload tile says. Never a Retry: the same file is refused again. */
    val message: String
        get() = maxBytes?.let { "This video is too big to upload (max ${formatCap(it)})." }
            ?: "This video is too big to upload."
}

/** The refusal in [this], when it is one. */
fun AppError.videoTooLarge(): VideoTooLarge? =
    if (this is AppError.Unknown && code == CODE_VIDEO_TOO_LARGE) {
        VideoTooLarge(details[DETAIL_MAX_BYTES]?.toLongOrNull()?.takeIf { it > 0L })
    } else {
        null
    }

/**
 * The cap in the unit people read: GB (binary, as the server counts) to a
 * tenth, rounded DOWN so a file of "the max" always fits; MB under 1 GB.
 */
internal fun formatCap(bytes: Long): String {
    if (bytes < BYTES_PER_GB) return "${(bytes / BYTES_PER_MB).coerceAtLeast(1L)} MB"
    val tenths = bytes * TENTHS / BYTES_PER_GB
    val whole = tenths / TENTHS
    val fraction = tenths % TENTHS
    return if (fraction == 0L) "$whole GB" else "$whole.$fraction GB"
}

/**
 * [apiCall] for an upload `init`, keeping a 413's `error.details` on the
 * [AppError.Unknown] it maps to.
 *
 * [ErrorMapper] keeps a 413's code but not its details, and the
 * [CODE_VIDEO_TOO_LARGE] message has to name the cap. The body is PEEKED,
 * not read, so the mapper still sees all of it. Once core:network keeps
 * details on every [AppError.Unknown] this reduces to [apiCall].
 */
suspend fun <T> uploadInitCall(
    errorMapper: ErrorMapper,
    block: suspend () -> ApiEnvelope<T>,
): AppResult<T> = uploadCall(errorMapper, block)

/**
 * [apiCall] for any upload route (2026-11-18): a 413's details as above, and
 * the upload refusals media-service added on 17 Nov kept whole — their code,
 * their details and the `Retry-After` header (see [UploadRefusal]).
 *
 * [ErrorMapper] turns every 429 into [AppError.RateLimited], which the
 * uploaders treat as a blip to retry in seconds. A quota refusal or a "long
 * video already processing" is not a blip: retrying it six times only delays
 * the message. Those 429s, and any 429 asking for more than a minute, become
 * an [AppError.Unknown] carrying their code, so the tile can say what happened
 * and when to try again.
 */
suspend fun <T> uploadCall(
    errorMapper: ErrorMapper,
    block: suspend () -> ApiEnvelope<T>,
): AppResult<T> {
    var refusal: RawUploadRefusal? = null
    val result = apiCall(errorMapper) {
        try {
            block()
        } catch (e: HttpException) {
            refusal = e.rawUploadRefusal()
            throw e
        }
    }
    val error = (result as? AppResult.Failure)?.error ?: return result
    val raw = refusal ?: return result
    return AppResult.Failure(raw.reshape(error))
}

/** A 413's `error.details` primitives, read without consuming the body. Empty for anything else. */
internal fun HttpException.payloadTooLargeDetails(): Map<String, String> {
    if (code() != HTTP_PAYLOAD_TOO_LARGE) return emptyMap()
    val raw = runCatching { response()?.errorBody()?.source()?.peek()?.readUtf8() }.getOrNull() ?: return emptyMap()
    return errorDetails(raw)
}

/** `error.code` of an envelope body, or null. Pure. */
internal fun errorCode(body: String): String? = runCatching {
    val error = Json.parseToJsonElement(body).jsonObject["error"] as? JsonObject
    (error?.get("code") as? JsonPrimitive)?.takeIf { it !is JsonNull }?.content?.takeIf { it.isNotBlank() }
}.getOrNull()

/** `error.message` of an envelope body, or null. Pure. */
internal fun errorMessage(body: String): String? = runCatching {
    val error = Json.parseToJsonElement(body).jsonObject["error"] as? JsonObject
    (error?.get("message") as? JsonPrimitive)?.takeIf { it !is JsonNull }?.content?.takeIf { it.isNotBlank() }
}.getOrNull()

/** An upload refusal as it came off the wire, peeked so the mapper still reads the body. */
private fun HttpException.rawUploadRefusal(): RawUploadRefusal? {
    if (code() !in REFUSAL_STATUSES) return null
    val body = runCatching { response()?.errorBody()?.source()?.peek()?.readUtf8() }.getOrNull().orEmpty()
    return RawUploadRefusal(
        status = code(),
        code = errorCode(body),
        message = errorMessage(body),
        details = errorDetails(body),
        retryAfterSeconds = response()?.headers()?.get("Retry-After")?.trim()?.toLongOrNull()?.takeIf { it >= 0L },
    )
}

private val REFUSAL_STATUSES =
    setOf(HTTP_CONFLICT, HTTP_GONE, HTTP_PAYLOAD_TOO_LARGE, HTTP_TOO_MANY_REQUESTS, HTTP_SERVICE_UNAVAILABLE)

/** `error.details` of an envelope body, primitives only, as their content. Pure, for the table test. */
internal fun errorDetails(body: String): Map<String, String> {
    val error = runCatching { Json.parseToJsonElement(body).jsonObject["error"] as? JsonObject }.getOrNull()
    val details = error?.get("details") as? JsonObject ?: return emptyMap()
    return details
        .mapNotNull { (key, value) ->
            val primitive = (value as? JsonPrimitive)?.takeIf { it !is JsonNull }
            primitive?.let { key to it.content }
        }
        .toMap()
}

private const val HTTP_PAYLOAD_TOO_LARGE = 413
private const val BYTES_PER_MB = 1024L * 1024L
private const val BYTES_PER_GB = 1024L * BYTES_PER_MB
private const val TENTHS = 10L
