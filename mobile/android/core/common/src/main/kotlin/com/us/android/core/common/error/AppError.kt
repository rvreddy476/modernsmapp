package com.us.android.core.common.error

/**
 * Closed error hierarchy for the whole app.
 *
 * Two rules, both enforced by review and by the detekt config:
 *
 *  1. Branch on the server's `error.code`, NEVER on `error.message`.
 *     Messages are human-facing and will be reworded; codes are contract.
 *  2. Every case carries [requestId] where the server supplied one
 *     (`meta.request_id`), so a user-reported failure is traceable in
 *     backend logs without guesswork.
 *
 * Codes verified against identity-platform/shared/api/response.go and the
 * auth-service handlers. See PHASE_0_1_PLAN.md §D.1 / §D.2.
 *
 * Phase 1 wires the mapping from HTTP responses to these cases; Phase 0
 * fixes the shape so :core:common consumers can be written now.
 */
sealed interface AppError {
    val requestId: String?

    // ── Transport ──────────────────────────────────────────────────────
    data class NoNetwork(override val requestId: String? = null) : AppError
    data class Timeout(override val requestId: String? = null) : AppError

    /** Response arrived but could not be parsed into the expected envelope. */
    data class Malformed(
        val detail: String,
        override val requestId: String? = null,
    ) : AppError

    // ── Server, typed by contract code ─────────────────────────────────
    /** 400 INVALID_REQUEST. [fieldErrors] comes from `error.details` when present. */
    data class InvalidRequest(
        val message: String,
        val fieldErrors: Map<String, String> = emptyMap(),
        override val requestId: String? = null,
    ) : AppError

    /** 401 AUTH_FAILED — bad credentials, or a refresh that could not be recovered. */
    data class AuthFailed(override val requestId: String? = null) : AppError

    /**
     * 403. [code] is the discriminator.
     *
     * [details] is carried through rather than discarded because at least one
     * 403 is not terminal: `EMAIL_NOT_VERIFIED` ships a live
     * `verification_token` here, and it is the only way a user who closed the
     * app mid-signup can finish signing up.
     */
    data class Forbidden(
        val code: String,
        val details: Map<String, String> = emptyMap(),
        override val requestId: String? = null,
    ) : AppError

    data class NotFound(override val requestId: String? = null) : AppError

    data class RateLimited(
        val retryAfterSeconds: Long?,
        override val requestId: String? = null,
    ) : AppError

    data class Server(
        val statusCode: Int,
        val code: String?,
        override val requestId: String? = null,
    ) : AppError

    /**
     * Any contract code not yet modelled. Carries the raw code so it can be
     * logged and promoted to a real case later, rather than being flattened
     * into a generic failure.
     */
    data class Unknown(
        val code: String?,
        val statusCode: Int?,
        override val requestId: String? = null,
    ) : AppError
}
