package com.us.android.core.chat.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Query

/**
 * The suggestion engine's chat surface (contract of 2026-09-05): people the
 * viewer may know and communities worth joining, plus the two write-backs
 * that make the ranking learn — an impression per shown batch and an
 * action per tap.
 */
interface SuggestionsApi {

    @GET("v1/suggestions/people")
    suspend fun people(@Query("limit") limit: Int): ApiEnvelope<PeopleSuggestionsDto>

    @GET("v1/suggestions/communities")
    suspend fun communities(@Query("limit") limit: Int): ApiEnvelope<CommunitySuggestionsDto>

    @POST("v1/suggestions/action")
    suspend fun action(@Body body: SuggestionActionRequest)

    @POST("v1/suggestions/impression")
    suspend fun impression(@Body body: SuggestionImpressionRequest)
}

@Serializable
data class PeopleSuggestionsDto(
    val type: String = "",
    val items: List<PersonSuggestionDto> = emptyList(),
)

@Serializable
data class PersonSuggestionDto(
    @SerialName("candidate_user_id") val candidateUserId: String = "",
    /** May be empty — the profile is the fallback. */
    @SerialName("display_name") val displayName: String = "",
    val score: Double = 0.0,
    @SerialName("reason_codes") val reasonCodes: List<String> = emptyList(),
    @SerialName("explain_text") val explainText: String = "",
    @SerialName("mutual_friend_count") val mutualFriendCount: Int = 0,
    @SerialName("mutual_friend_ids") val mutualFriendIds: List<String> = emptyList(),
    @SerialName("source_bucket") val sourceBucket: String = "",
)

@Serializable
data class CommunitySuggestionsDto(
    val type: String = "",
    val items: List<CommunitySuggestionDto> = emptyList(),
)

@Serializable
data class CommunitySuggestionDto(
    @SerialName("community_id") val communityId: String = "",
    @SerialName("owner_id") val ownerId: String = "",
    val handle: String = "",
    val name: String = "",
    val description: String = "",
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
    @SerialName("member_count") val memberCount: Int = 0,
    @SerialName("update_count") val updateCount: Int = 0,
    @SerialName("reason_codes") val reasonCodes: List<String> = emptyList(),
    @SerialName("explain_text") val explainText: String = "",
    @SerialName("join_path") val joinPath: String = "",
)

/** `type` is `friend` or `community`; `action` is dismiss | follow | friend_request | hide | block. */
@Serializable
data class SuggestionActionRequest(
    val type: String,
    val surface: String,
    val action: String,
    @SerialName("candidate_user_id") val candidateUserId: String? = null,
    @SerialName("community_id") val communityId: String? = null,
)

@Serializable
data class SuggestionImpressionRequest(
    val type: String,
    val surface: String,
    val items: List<SuggestionImpressionItem>,
)

@Serializable
data class SuggestionImpressionItem(
    @SerialName("candidate_user_id") val candidateUserId: String? = null,
    @SerialName("community_id") val communityId: String? = null,
    val rank: Int,
    val score: Double = 0.0,
)
