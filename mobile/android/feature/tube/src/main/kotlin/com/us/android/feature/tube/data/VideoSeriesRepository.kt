package com.us.android.feature.tube.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/** One episode of a series: the post it is, its number, and its title for the row. */
data class SeriesEpisode(val postId: String, val episodeNum: Int, val title: String)

/** A series and every episode in it, as the server lists them. */
data class SeriesInfo(val id: String, val title: String, val episodes: List<SeriesEpisode>)

/**
 * Reads the series a post belongs to. Best-effort by design, as
 * [WatchProgressRepository] is: a read that fails (a 404 for a post in no
 * series, no network, an endpoint that has not landed) is "no series", and
 * a video with no series ends on its end screen. Nothing about playback
 * waits on this answer.
 */
@Singleton
class VideoSeriesRepository @Inject constructor(
    private val api: VideoSeriesApi,
    private val errorMapper: ErrorMapper,
) {

    /** The post's series, or null when it is in none the viewer can see. */
    suspend fun forPost(postId: String): SeriesInfo? =
        when (val result = apiCall(errorMapper) { api.forPost(postId) }) {
            is AppResult.Success -> result.data.toSeriesInfo()
            is AppResult.Failure -> null
        }

    private fun PostSeriesDto.toSeriesInfo(): SeriesInfo = SeriesInfo(
        id = series.id,
        title = series.title,
        episodes = episodes.map { SeriesEpisode(it.postId, it.episodeNum, it.title) },
    )
}
