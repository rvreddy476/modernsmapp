package com.us.android.feature.post.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.Query

/**
 * Hashtag suggestions for the details step's HASHTAGS field (2026-09-05)
 * — post-service `SearchHashtags`, `GET /v1/hashtags/search?q=&limit=`
 * through the gateway. Prefix matches over the tags already used on
 * posts, most used first; the server wants at least two characters and
 * caps the page at twenty.
 *
 * Its own interface so the fakes of [PostApi] stay small.
 */
interface HashtagSearchApi {

    @GET("v1/hashtags/search")
    suspend fun search(
        @Query("q") query: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<HashtagSearchDto>
}

@Serializable
data class HashtagSearchDto(
    val query: String = "",
    val hashtags: List<HashtagSuggestionDto> = emptyList(),
)

@Serializable
data class HashtagSuggestionDto(
    @SerialName("normalized_name") val normalizedName: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("post_count") val postCount: Long = 0L,
)
