package com.us.android.core.chat.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Communities — the product name for broadcast channels (founder,
 * 2026-09-05): anyone creates one, people join, only the owner and admins
 * post updates and events, nobody replies. Members can only react.
 *
 * Contract `v1/broadcast-channels`, live on dev. Every write that answers
 * nothing worth reading is declared `Unit` (subscribe, mute, admins,
 * reactions, views, reports, deletes) and goes through `noContentApiCall`,
 * so a `204` and a `{"data":{"status":"ok"}}` are both a success.
 */
interface CommunityApi {

    @POST("v1/broadcast-channels")
    suspend fun create(@Body body: CreateCommunityRequest): ApiEnvelope<CommunityDto>

    @GET("v1/broadcast-channels/{id}")
    suspend fun community(@Path("id") id: String): ApiEnvelope<CommunityDto>

    @PUT("v1/broadcast-channels/{id}")
    suspend fun update(@Path("id") id: String, @Body body: UpdateCommunityRequest): ApiEnvelope<CommunityDto>

    @DELETE("v1/broadcast-channels/{id}")
    suspend fun delete(@Path("id") id: String)

    /** The viewer's own communities — owned, administered and joined. */
    @GET("v1/broadcast-channels/my")
    suspend fun mine(): ApiEnvelope<List<CommunityDto>>

    @GET("v1/broadcast-channels/discover")
    suspend fun discover(
        @Query("q") query: String?,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String?,
    ): ApiEnvelope<List<CommunityDto>>

    @POST("v1/broadcast-channels/{id}/report")
    suspend fun report(@Path("id") id: String, @Body body: ReportRequest)
}

@Serializable
data class CommunityDto(
    val id: String = "",
    @SerialName("owner_id") val ownerId: String = "",
    val handle: String = "",
    val name: String = "",
    val description: String = "",
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
    /** `public` or `private`. */
    val visibility: String = "",
    @SerialName("member_count") val memberCount: Int = 0,
    @SerialName("update_count") val updateCount: Int = 0,
    @SerialName("is_verified") val isVerified: Boolean = false,
    /** `owner`, `admin`, `subscriber`, `banned` or "" for a stranger. */
    @SerialName("viewer_role") val viewerRole: String = "",
    @SerialName("viewer_muted") val viewerMuted: Boolean = false,
    @SerialName("can_post") val canPost: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class CommunityEventDto(
    val title: String = "",
    @SerialName("starts_at") val startsAt: String = "",
    @SerialName("ends_at") val endsAt: String = "",
    val location: String = "",
)

@Serializable
data class ReactionCountDto(
    val emoji: String = "",
    val count: Int = 0,
)

@Serializable
data class CommunityUpdateDto(
    val id: String = "",
    @SerialName("channel_id") val channelId: String = "",
    @SerialName("author_id") val authorId: String = "",
    @SerialName("update_type") val updateType: String = "",
    val title: String? = null,
    val body: String = "",
    @SerialName("media_ids") val mediaIds: List<String> = emptyList(),
    val event: CommunityEventDto? = null,
    @SerialName("is_pinned") val isPinned: Boolean = false,
    @SerialName("published_at") val publishedAt: String = "",
    @SerialName("view_count") val viewCount: Int = 0,
    @SerialName("reaction_count") val reactionCount: Int = 0,
    val reactions: List<ReactionCountDto> = emptyList(),
    @SerialName("viewer_reaction") val viewerReaction: String? = null,
    @SerialName("can_edit") val canEdit: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class CreateCommunityRequest(
    val name: String,
    val handle: String,
    val description: String,
    val visibility: String,
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
)

@Serializable
data class UpdateCommunityRequest(
    val name: String? = null,
    val description: String? = null,
    val visibility: String? = null,
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
)

/** `{}` mutes indefinitely; `muted_until` is an RFC3339 instant. */
@Serializable
data class MuteCommunityRequest(@SerialName("muted_until") val mutedUntil: String? = null)

@Serializable
data class CommunityAdminRequest(@SerialName("user_id") val userId: String)

@Serializable
data class PostUpdateRequest(
    val body: String,
    @SerialName("media_ids") val mediaIds: List<String> = emptyList(),
    val title: String? = null,
    val event: CommunityEventDto? = null,
)

@Serializable
data class ReactRequest(val emoji: String)

@Serializable
data class ReportRequest(val reason: String, val details: String = "")
