package com.us.android.feature.post.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.feature.post.data.dto.CreatePostRequest
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Create-post, and the judgement about whether a failure is worth retrying.
 *
 * Thin: the composer's product rules live in the reducer and the ViewModel.
 * What is here is the one thing neither of them should know — how the server's
 * error codes map onto "try again" versus "this cannot work".
 */
@Singleton
open class ComposerRepository @Inject constructor(
    private val api: PostApi,
    private val errorMapper: ErrorMapper,
) {

    /**
     * Publishes, returning the SERVER's post id.
     *
     * The id is never invented locally. A create that fails returns no post, so
     * anything the client made up would open onto a post that does not exist.
     */
    open suspend fun createPost(
        creationKey: String,
        request: CreatePostRequest,
    ): AppResult<String> = when (
        val result = apiCall(errorMapper) { api.createPost(creationKey, request) }
    ) {
        is AppResult.Success -> AppResult.Success(result.data.id)
        is AppResult.Failure -> result
    }

    /**
     * True when retrying cannot possibly succeed.
     *
     * The distinction drives whether the UI offers Retry at all, so getting it
     * wrong in either direction is a real defect: a retryable failure marked
     * terminal loses a post the user could have published, and a terminal one
     * marked retryable gives them a button that fails forever.
     *
     * Matched on the server's error CODE, never its message — messages are
     * written for humans and get reworded.
     */
    fun isTerminal(error: AppError): Boolean = when (error) {
        // Validation and policy. The bytes are wrong, and the same bytes will
        // be wrong next time.
        is AppError.InvalidRequest -> true
        is AppError.Forbidden -> true
        is AppError.NotFound -> true

        is AppError.Server -> error.code in TerminalServerCodes

        // A 400 under a code this client does not model — the server refusing
        // a `publish_at`, say — is still the bytes being wrong (2026-09-05).
        is AppError.Unknown -> error.statusCode == HTTP_BAD_REQUEST

        // Transport, auth refresh, rate limits and 5xx: all worth another go.
        else -> false
    }

    /** A sentence the composer can show. */
    fun message(error: AppError): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Your post is saved."
        is AppError.Timeout -> "That took too long. Your post is saved."
        is AppError.RateLimited -> "You've posted a lot recently. Try again shortly."
        is AppError.InvalidRequest -> error.message
        is AppError.Forbidden -> "That post couldn't be published."
        is AppError.Server -> serverMessage(error.code)
        // The server's own sentence for a refusal this client cannot name.
        is AppError.Unknown -> error.message?.takeIf { it.isNotBlank() } ?: GENERIC_FAILURE
        else -> GENERIC_FAILURE
    }

    /** Split out so neither branch grows past the point of being readable. */
    private fun serverMessage(code: String?): String = when (code) {
        "IDEMPOTENCY_KEY_REUSED" -> "This post was edited after it failed. Post it again."
        "MEDIA_NOT_OWNED", "MEDIA_NOT_FOUND" -> "That image can't be attached."
        "MEDIA_NOT_READY" -> "That image isn't ready yet."
        "TEXT_TOO_LONG" -> "That post is too long."
        "EMPTY_POST" -> "A post needs text or an image."
        "PAYLOAD_TOO_LARGE" -> "That post is too large to send."
        else -> GENERIC_FAILURE
    }

    private companion object {
        const val GENERIC_FAILURE = "We couldn't publish that. Try again."
        const val HTTP_BAD_REQUEST = 400

        /**
         * Server codes that are the client's fault and will not change.
         *
         * `IDEMPOTENCY_KEY_REUSED` is terminal for THIS frozen operation
         * specifically: the reducer drops the frozen payload on a terminal
         * failure, so the next Post mints a new key and can succeed. Treating
         * it as retryable would replay the same rejected key forever.
         */
        val TerminalServerCodes = setOf(
            "IDEMPOTENCY_KEY_REUSED",
            "MISSING_IDEMPOTENCY_KEY",
            "INVALID_IDEMPOTENCY_KEY",
            "MEDIA_NOT_OWNED",
            "MEDIA_NOT_FOUND",
            "MEDIA_NOT_READY",
            "TEXT_TOO_LONG",
            "EMPTY_POST",
            "PAYLOAD_TOO_LARGE",
            "UNSUPPORTED_DISTRIBUTION",
            "INVALID_DISTRIBUTION",
        )
    }
}
