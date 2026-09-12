package com.us.android.feature.tube.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.Path

/**
 * The series a post belongs to: post-service's `/v1/posts/:postId/series`
 * (Tube auto-advance, 2026-09-12). A 404 means the post is in no series
 * the caller can see, which is the common case and never an error the
 * viewer hears about.
 */
interface VideoSeriesApi {

    @GET("v1/posts/{postId}/series")
    suspend fun forPost(@Path("postId") postId: String): ApiEnvelope<PostSeriesDto>
}

/**
 * The series, every episode in it, and where this post sits. `next` and
 * `prev` are the server's answer; the client keeps its own rule over
 * [episodes] ([com.us.android.feature.tube.ui.watch.nextEpisode]) so the
 * list and the arrow can never disagree on screen.
 */
@Serializable
data class PostSeriesDto(
    val series: SeriesSummaryDto,
    val episodes: List<SeriesEpisodeDto> = emptyList(),
    val current: SeriesPositionDto? = null,
    val next: SeriesEpisodeDto? = null,
    val prev: SeriesEpisodeDto? = null,
)

@Serializable
data class SeriesSummaryDto(
    val id: String,
    val title: String = "",
)

@Serializable
data class SeriesEpisodeDto(
    @SerialName("post_id") val postId: String,
    @SerialName("episode_num") val episodeNum: Int,
    val title: String = "",
)

@Serializable
data class SeriesPositionDto(
    @SerialName("episode_num") val episodeNum: Int,
)
