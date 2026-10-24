package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.noContentApiCall
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import retrofit2.http.Body
import retrofit2.http.HTTP
import retrofit2.http.POST
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Mute (2026-10-24): graph-service's mute pair, the one half of the old
 * connections seam that survives. Connect itself is gone (founder, 24 Oct
 * 2026; graph-service answers 410 CONNECTIONS_RETIRED to a new request or an
 * accept), so the app no longer sends, answers, lists or removes
 * connections: following and messaging are how people reach each other.
 *
 *     POST   /v1/graph/mute   {user_id}   → 204
 *     DELETE /v1/graph/mute   {user_id}   → 204
 */
interface MuteApi {

    /** `POST /v1/graph/mute {user_id}` (handler.go:1007): 204, no body. */
    @POST("v1/graph/mute")
    suspend fun mute(@Body body: GraphUserIdRequest)

    /** `DELETE /v1/graph/mute {user_id}`: 204, no body. */
    @HTTP(method = "DELETE", path = "v1/graph/mute", hasBody = true)
    suspend fun unmute(@Body body: GraphUserIdRequest)
}

/** What the profile's More menu and the post More sheet mute through; a seam so their ViewModels test against a fake. */
interface MuteSource {
    suspend fun mute(userId: String): AppResult<Unit>
    suspend fun unmute(userId: String): AppResult<Unit>
}

@Singleton
class MuteRepository @Inject constructor(
    private val api: MuteApi,
    private val errorMapper: ErrorMapper,
) : MuteSource {

    override suspend fun mute(userId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.mute(GraphUserIdRequest(userId)) }

    override suspend fun unmute(userId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.unmute(GraphUserIdRequest(userId)) }
}
