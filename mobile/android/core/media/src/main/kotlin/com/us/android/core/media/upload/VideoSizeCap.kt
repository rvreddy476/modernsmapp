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
): AppResult<T> {
    var details: Map<String, String> = emptyMap()
    val result = apiCall(errorMapper) {
        try {
            block()
        } catch (e: HttpException) {
            details = e.payloadTooLargeDetails()
            throw e
        }
    }
    val error = (result as? AppResult.Failure)?.error
    if (error !is AppError.Unknown || details.isEmpty()) return result
    return AppResult.Failure(error.copy(details = details))
}

/** A 413's `error.details` primitives, read without consuming the body. Empty for anything else. */
internal fun HttpException.payloadTooLargeDetails(): Map<String, String> {
    if (code() != HTTP_PAYLOAD_TOO_LARGE) return emptyMap()
    val raw = runCatching { response()?.errorBody()?.source()?.peek()?.readUtf8() }.getOrNull() ?: return emptyMap()
    return errorDetails(raw)
}

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
