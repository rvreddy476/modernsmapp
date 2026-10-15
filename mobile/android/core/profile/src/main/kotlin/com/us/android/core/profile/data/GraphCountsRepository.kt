package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.FollowCounts
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.profile.data.dto.GraphCountsDto
import retrofit2.http.GET
import retrofit2.http.Path
import javax.inject.Inject
import javax.inject.Singleton

/**
 * graph-service's count route, on its own interface rather than as one more
 * member of [ProfileApi]: every hand-written `ProfileApi` fake across the
 * feed, post and profile tests would otherwise have to grow a method none of
 * those screens call. Created from the same app-wide Retrofit.
 */
interface GraphCountsApi {

    /**
     * `GET /v1/graph/counts/:userId` — follower, following and friend counts
     * as the graph holds them. Proxied by the gateway under `/v1/graph`.
     */
    @GET("v1/graph/counts/{userId}")
    suspend fun counts(@Path("userId") userId: String): ApiEnvelope<GraphCountsDto>
}

/**
 * The live follow counts for a profile header (T-20, audit R-1).
 *
 * The profile payload and `GET /v1/profiles/:id/stats` both read
 * `profile_stats`, which only the retired profile-service follow route
 * incremented — so on Android a follow never moved the number. The graph is
 * where follows are written now, and this is where they are counted.
 */
@Singleton
class GraphCountsRepository @Inject constructor(
    private val api: GraphCountsApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun counts(userId: String): AppResult<FollowCounts> =
        apiCall(errorMapper) { api.counts(userId) }.map { it.toDomain() }
}

/** int64 on the wire, clamped into the header's `Int` rather than wrapped into a negative. */
internal fun GraphCountsDto.toDomain() = FollowCounts(
    followers = followerCount.toClampedInt(),
    following = followingCount.toClampedInt(),
    friends = friendCount.toClampedInt(),
)

private fun Long.toClampedInt(): Int = coerceIn(0L, Int.MAX_VALUE.toLong()).toInt()
