package com.us.android.feature.tube.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/** Where a viewer left a video: the saved playhead and whether they finished. */
data class WatchProgress(val positionMs: Long, val durationMs: Long, val completed: Boolean)

/**
 * Reads and writes watch progress. Both directions are best-effort by
 * design: a read that fails (a 404 for a never-watched video, an endpoint
 * that has not landed, no network) starts the video from the top, and a
 * write that fails is simply the next write's job — progress is reported
 * every ten seconds while playing, so nothing is lost for long.
 */
@Singleton
class WatchProgressRepository @Inject constructor(
    private val api: WatchProgressApi,
    private val errorMapper: ErrorMapper,
) {

    /** The saved progress, or null when there is none to resume from. */
    suspend fun progress(postId: String): WatchProgress? =
        when (val result = apiCall(errorMapper) { api.get(postId) }) {
            is AppResult.Success -> result.data.let { WatchProgress(it.positionMs, it.durationMs, it.completed) }
            is AppResult.Failure -> null
        }

    /** Records the playhead. The answer is not needed; a failure is retried by the next report. */
    suspend fun save(postId: String, positionMs: Long, durationMs: Long, completed: Boolean) {
        apiCall(errorMapper) { api.save(postId, WatchProgressRequest(positionMs, durationMs, completed)) }
    }
}
