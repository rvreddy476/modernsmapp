package com.us.android.core.network

import com.us.android.core.common.error.AppError
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonPrimitive
import retrofit2.HttpException
import java.io.IOException
import java.net.SocketTimeoutException
import java.net.UnknownHostException
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Maps HTTP failures onto the closed [AppError] hierarchy.
 *
 * The mapping keys off `error.code` from the response body, falling back to
 * the HTTP status only when the body carries no code. Message text is never
 * inspected — see [ApiErrorBody.code].
 */
@Singleton
class ErrorMapper @Inject constructor(
    private val json: Json,
) {

    fun map(throwable: Throwable): AppError = when (throwable) {
        is HttpException -> mapHttp(throwable)
        is SocketTimeoutException -> AppError.Timeout()
        is UnknownHostException -> AppError.NoNetwork()
        // Covers okio.IOException too — since Okio 3 it is a typealias for
        // java.io.IOException, so a separate branch would be dead code.
        is IOException -> AppError.NoNetwork()
        else -> AppError.Unknown(code = null, statusCode = null)
    }

    private fun mapHttp(e: HttpException): AppError {
        val raw = runCatching { e.response()?.errorBody()?.string() }.getOrNull()
        val envelope = raw?.let { body ->
            runCatching { json.decodeFromString<ApiEnvelope<JsonObject>>(body) }.getOrNull()
        }
        val errorBody = envelope?.error
        val code = errorBody?.code?.takeIf { it.isNotBlank() }
        val message = errorBody?.message.orEmpty()
        val requestId = envelope?.meta?.requestId

        return when {
            code == CODE_INVALID_REQUEST -> AppError.InvalidRequest(
                message = message,
                fieldErrors = extractFieldErrors(errorBody),
                requestId = requestId,
            )

            code == CODE_AUTH_FAILED -> AppError.AuthFailed(requestId)

            // EMAIL_NOT_VERIFIED is deliberately NOT flattened into Forbidden.
            // core:auth needs to pull `verification_token` out of details, so
            // the code must survive the mapping intact.
            e.code() == HTTP_FORBIDDEN && code != null -> AppError.Forbidden(
                code = code,
                details = extractFieldErrors(errorBody),
                requestId = requestId,
            )

            e.code() == HTTP_UNAUTHORIZED -> AppError.AuthFailed(requestId)
            e.code() == HTTP_NOT_FOUND -> AppError.NotFound(requestId)

            e.code() == HTTP_TOO_MANY_REQUESTS -> AppError.RateLimited(
                retryAfterSeconds = e.response()
                    ?.headers()
                    ?.get("Retry-After")
                    ?.toLongOrNull(),
                requestId = requestId,
            )

            e.code() >= HTTP_SERVER_ERROR -> AppError.Server(
                statusCode = e.code(),
                code = code,
                requestId = requestId,
            )

            else -> AppError.Unknown(
                code = code,
                statusCode = e.code(),
                requestId = requestId,
            )
        }
    }

    private fun extractFieldErrors(error: ApiErrorBody?): Map<String, String> {
        val details = error?.details as? JsonObject ?: return emptyMap()
        return details.mapNotNull { (key, value) ->
            runCatching { key to value.jsonPrimitive.content }.getOrNull()
        }.toMap()
    }

    companion object {
        const val CODE_INVALID_REQUEST = "INVALID_REQUEST"
        const val CODE_AUTH_FAILED = "AUTH_FAILED"
        const val CODE_EMAIL_NOT_VERIFIED = "EMAIL_NOT_VERIFIED"
        const val CODE_STEP_UP_UNAVAILABLE = "STEP_UP_UNAVAILABLE"

        private const val HTTP_UNAUTHORIZED = 401
        private const val HTTP_FORBIDDEN = 403
        private const val HTTP_NOT_FOUND = 404
        private const val HTTP_TOO_MANY_REQUESTS = 429
        private const val HTTP_SERVER_ERROR = 500
    }
}
