package com.us.android.core.network

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/**
 * The standard envelope every endpoint on this platform returns.
 *
 * Verified against identity-platform/shared/api/response.go (byte-identical
 * copies exist in chat-service/shared/api and Architecture/shared/api):
 *
 *   { "data": {...}, "error": {"code","message","details"},
 *     "meta": {"request_id","next_cursor"} }
 *
 * All three keys are `omitempty` server-side — absent, not null, when unused.
 */
@Serializable
data class ApiEnvelope<T>(
    val data: T? = null,
    val error: ApiErrorBody? = null,
    val meta: ApiMeta? = null,
)

@Serializable
data class ApiErrorBody(
    /**
     * The stable contract string — `INVALID_REQUEST`, `AUTH_FAILED`,
     * `EMAIL_NOT_VERIFIED`, `STEP_UP_UNAVAILABLE`, …
     *
     * ALWAYS branch on this. Never on [message], which is human-facing and
     * will be reworded without notice.
     */
    val code: String = "",
    val message: String = "",
    /**
     * Free-form. Load-bearing for at least one flow: a 403
     * `EMAIL_NOT_VERIFIED` carries a live `verification_token` here, and it
     * is the only way a user who closed the app mid-signup can resume.
     * Discarding error bodies loses the account.
     */
    val details: JsonElement? = null,
)

@Serializable
data class ApiMeta(
    @SerialName("request_id") val requestId: String? = null,
    /**
     * Platform-wide pagination cursor. Opaque — never parse it, never
     * construct one. Absent means "no more pages".
     */
    @SerialName("next_cursor") val nextCursor: String? = null,
)

/**
 * A page of results plus the cursor needed to fetch the next one.
 *
 * Phase 3's Paging 3 `RemoteMediator` consumes exactly this, which is why
 * cursor handling lives in the network layer from day one rather than being
 * rediscovered per-feature.
 */
data class Paged<T>(
    val items: List<T>,
    val nextCursor: String?,
) {
    val hasMore: Boolean get() = nextCursor != null
}
