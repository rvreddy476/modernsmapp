package com.us.android.core.engagement.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.Path
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The viewer's repost of one post, on its own interface rather than one more
 * member of [EngagementApi]: every hand-written `EngagementApi` fake in the
 * feed, post and engagement tests would otherwise grow a method none of them
 * call. Created from the same app-wide Retrofit.
 */
interface RepostStateApi {

    /**
     * post-service `GetRepostState`: `{"data":{"has_reposted":false}}`, or
     * with `repost_id`, `type`, `quote_text` and `created_at` when there is
     * one.
     */
    @GET("v1/posts/{postId}/repost/me")
    suspend fun repostState(@Path("postId") postId: String): ApiEnvelope<RepostStateDto>
}

/** Only `has_reposted` is read; the rest describes the repost itself. */
@Serializable
data class RepostStateDto(
    @SerialName("has_reposted") val hasReposted: Boolean = false,
    @SerialName("repost_id") val repostId: String? = null,
)

/** Whether the viewer has reposted a post. An interface so the share sheet is tested with a fake. */
fun interface RepostReads {
    suspend fun hasReposted(postId: String): AppResult<Boolean>
}

/** [RepostReads] over `GET /v1/posts/:id/repost/me` (2026-10-17, the share sheet's Repost row). */
@Singleton
class RepostStateRepository @Inject constructor(
    private val api: RepostStateApi,
    private val errorMapper: ErrorMapper,
) : RepostReads {

    override suspend fun hasReposted(postId: String): AppResult<Boolean> =
        apiCall(errorMapper) { api.repostState(postId) }.map { it.hasReposted }
}
