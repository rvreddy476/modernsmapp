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
