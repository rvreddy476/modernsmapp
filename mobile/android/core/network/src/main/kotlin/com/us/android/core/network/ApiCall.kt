package com.us.android.core.network

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.CancellationException

/**
 * Wraps a Retrofit suspend call so call sites see [AppResult] and never the
 * envelope, an exception, or an HTTP status.
 *
 * Design note: the Phase 1 plan proposed a Retrofit `CallAdapter` for this.
 * A wrapper function is used instead because the critical path —
 * `EMAIL_NOT_VERIFIED`, a 403 whose body carries a live verification token —
 * needs explicit, readable error-body handling. A CallAdapter would bury that
 * in framework glue. Ergonomics at the call site are identical.
 *
 * [CancellationException] is deliberately rethrown: swallowing it breaks
 * structured concurrency and leaves coroutines that refuse to die.
 */
@Suppress("TooGenericExceptionCaught")
suspend inline fun <T, R> apiCall(
    errorMapper: ErrorMapper,
    crossinline transform: (T) -> R,
    crossinline block: suspend () -> ApiEnvelope<T>,
): AppResult<R> = try {
    val envelope = block()
    when {
        envelope.error != null -> AppResult.Failure(
            AppError.Unknown(
                code = envelope.error.code,
                statusCode = null,
                requestId = envelope.meta?.requestId,
            ),
        )

        envelope.data != null -> AppResult.Success(transform(envelope.data))

        // A 2xx with neither data nor error. Real cases exist (204-style
        // responses), but a caller expecting a payload must not receive a
        // silent null, so this is an explicit failure rather than a crash.
        else -> AppResult.Failure(
            AppError.Malformed(
                detail = "2xx response carried neither `data` nor `error`",
                requestId = envelope.meta?.requestId,
            ),
        )
    }
} catch (e: CancellationException) {
    throw e
} catch (e: Throwable) {
    AppResult.Failure(errorMapper.map(e))
}

/** [apiCall] for endpoints whose payload needs no transformation. */
suspend inline fun <T> apiCall(
    errorMapper: ErrorMapper,
    crossinline block: suspend () -> ApiEnvelope<T>,
): AppResult<T> = apiCall(errorMapper, { it }, block)

/**
 * [apiCall] for endpoints that answer `204 No Content` with an empty body.
 *
 * These genuinely exist on this platform — `DELETE /v1/posts/:id/repost` and
 * both graph mute routes were captured returning `204` with
 * `Content-Length: 0`. They cannot go through [apiCall]: there is no envelope
 * to deserialize, and treating the absent `data` as a malformed response would
 * turn every successful delete into a failure.
 *
 * The endpoint must declare a `Unit` return type so Retrofit skips body
 * conversion. Failures still arrive as `HttpException` and map normally.
 */
@Suppress("TooGenericExceptionCaught")
suspend inline fun noContentApiCall(
    errorMapper: ErrorMapper,
    crossinline block: suspend () -> Unit,
): AppResult<Unit> = try {
    block()
    AppResult.Success(Unit)
} catch (e: CancellationException) {
    throw e
} catch (e: Throwable) {
    AppResult.Failure(errorMapper.map(e))
}

/**
 * [apiCall] for non-paginated LIST endpoints.
 *
 * Go handlers hand the envelope a slice that is `nil` when the user has no
 * rows, which marshals as `"data": null` (captured live from
 * `GET /v1/auth/trusted-devices` on a fresh account). For a list endpoint
 * that is a legitimate empty result, not a malformed response — the same
 * rule [pagedApiCall] already applies. Routing these through plain [apiCall]
 * turned every empty list into a failure and took down any screen that
 * required all its sections to load.
 */
@Suppress("TooGenericExceptionCaught")
suspend inline fun <T> listApiCall(
    errorMapper: ErrorMapper,
    crossinline block: suspend () -> ApiEnvelope<List<T>>,
): AppResult<List<T>> = try {
    val envelope = block()
    when {
        envelope.error != null -> AppResult.Failure(
            AppError.Unknown(
                code = envelope.error.code,
                statusCode = null,
                requestId = envelope.meta?.requestId,
            ),
        )
        else -> AppResult.Success(envelope.data ?: emptyList())
    }
} catch (e: CancellationException) {
    throw e
} catch (e: Throwable) {
    AppResult.Failure(errorMapper.map(e))
}

/**
 * [apiCall] for cursor-paginated list endpoints, folding `meta.next_cursor`
 * into the result so callers never touch the envelope.
 */
@Suppress("TooGenericExceptionCaught")
suspend inline fun <T> pagedApiCall(
    errorMapper: ErrorMapper,
    crossinline block: suspend () -> ApiEnvelope<List<T>>,
): AppResult<Paged<T>> = try {
    val envelope = block()
    when {
        envelope.error != null -> AppResult.Failure(
            AppError.Unknown(
                code = envelope.error.code,
                statusCode = null,
                requestId = envelope.meta?.requestId,
            ),
        )
        // An empty page is a legitimate result, so `data == null` is treated
        // as an empty list rather than an error here.
        else -> AppResult.Success(
            Paged(
                items = envelope.data ?: emptyList(),
                nextCursor = envelope.meta?.nextCursor?.takeIf { it.isNotBlank() },
            ),
        )
    }
} catch (e: CancellationException) {
    throw e
} catch (e: Throwable) {
    AppResult.Failure(errorMapper.map(e))
}
