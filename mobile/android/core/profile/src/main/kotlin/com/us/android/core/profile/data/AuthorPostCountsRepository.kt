package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import retrofit2.http.GET
import retrofit2.http.Path
import javax.inject.Inject
import javax.inject.Singleton

/**
 * post-service's per-author counts, on its own interface for the reason
 * [GraphCountsApi] is: the hand-written `ProfileApi` fakes stay as they are.
 */
interface AuthorPostCountsApi {

    /**
     * `GET /v1/posts/by-author/:authorId/counts` — post-service
     * `GetAuthorCounts`: `{"data":{"post":5,"long_video":2,"total":7}}`, one
     * key per content type the author has plus `total`, deleted posts left
     * out. A map, because the keys are whatever content types exist.
     */
    @GET("v1/posts/by-author/{authorId}/counts")
    suspend fun counts(@Path("authorId") authorId: String): ApiEnvelope<Map<String, Long>>
}

/**
 * The profile header's post number (2026-10-17). The profile payload's
 * `post_count` is never updated by anything, so every header read 0; the
 * web profile reads this route for the same reason.
 */
@Singleton
class AuthorPostCountsRepository @Inject constructor(
    private val api: AuthorPostCountsApi,
    private val errorMapper: ErrorMapper,
) {

    /** The author's live post total; a failure is the caller's cue to keep the number it had. */
    suspend fun total(authorId: String): AppResult<Int> =
        apiCall(errorMapper) { api.counts(authorId) }.map { authorPostTotal(it) }
}

/**
 * `total` from the counts map, clamped into the header's `Int`. An answer
 * without `total` (a server that predates it) is the sum of the per-type
 * counts, which is what the server's `total` is.
 */
internal fun authorPostTotal(counts: Map<String, Long>): Int {
    val total = counts[TOTAL_KEY] ?: counts.filterKeys { it != TOTAL_KEY }.values.sum()
    return total.coerceIn(0L, Int.MAX_VALUE.toLong()).toInt()
}

private const val TOTAL_KEY = "total"
