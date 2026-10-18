package com.us.android.feature.post.data

import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * post-service's composer drafts (`/v1/posts/drafts`, Module 1 P0-5), for the
 * photo and text details screen (2026-10-18): save, list, reopen, change,
 * delete and publish.
 *
 * The server decodes [PostDraftPayloadDto] with `DisallowUnknownFields`, so
 * the payload carries ONLY keys post-service declares — every one nullable
 * and left off the wire when unset (the app's `Json` has `explicitNulls`
 * off), which also keeps a draft from an older server readable.
 */
interface PostDraftsApi {

    @POST("v1/posts/drafts")
    suspend fun create(@Body body: PostDraftRequestDto): ApiEnvelope<PostDraftDto>

    @GET("v1/posts/drafts")
    suspend fun list(@Query("limit") limit: Int): ApiEnvelope<List<PostDraftDto>>

    @GET("v1/posts/drafts/{draftId}")
    suspend fun get(@Path("draftId") draftId: String): ApiEnvelope<PostDraftDto>

    @PATCH("v1/posts/drafts/{draftId}")
    suspend fun update(@Path("draftId") draftId: String, @Body body: PostDraftRequestDto): ApiEnvelope<PostDraftDto>

    @DELETE("v1/posts/drafts/{draftId}")
    suspend fun delete(@Path("draftId") draftId: String): ApiEnvelope<Map<String, Boolean>>

    @POST("v1/posts/drafts/{draftId}/publish")
    suspend fun publish(@Path("draftId") draftId: String): ApiEnvelope<PostDraftPublishDto>
}

/** A draft as saved: its kind (`post` for photo and text posts) and the post it will become. */
@Serializable
data class PostDraftRequestDto(
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("post_type") val postType: String = DRAFT_KIND_POST,
    val payload: PostDraftPayloadDto,
)

/**
 * The post a draft will become — post-service's `PostDraftPayload`, the
 * subset the details screen sets. `text` is always on the wire (the server
 * reads a missing one as empty, and a draft needs text or media).
 */
@Serializable
data class PostDraftPayloadDto(
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val text: String = "",
    val visibility: String? = null,
    @SerialName("content_type") val contentType: String? = null,
    @SerialName("media_ids") val mediaIds: List<String>? = null,
    /** media id → alt text, applied through media-service at publish. */
    @SerialName("alt_texts") val altTexts: Map<String, String>? = null,
    @SerialName("no_comments") val noComments: Boolean? = null,
    @SerialName("location_name") val locationName: String? = null,
    val language: String? = null,
    val title: String? = null,
    val distribution: DistributionRequest? = null,
    @SerialName("cover_media_id") val coverMediaId: String? = null,
    @SerialName("paid_promotion") val paidPromotion: Boolean? = null,
    /** The post's own `text` | `image` — not the draft row's kind. */
    @SerialName("post_type") val postType: String? = null,
    val hashtags: List<String>? = null,
    val mentions: List<String>? = null,
    @SerialName("remix_setting") val remixSetting: String? = null,
    @SerialName("comment_access") val commentAccess: String? = null,
    @SerialName("link_url") val linkUrl: String? = null,
    @SerialName("ai_generated") val aiGenerated: Boolean? = null,
    @SerialName("is_branded") val isBranded: Boolean? = null,
    @SerialName("cover_index") val coverIndex: Int? = null,
    @SerialName("location_place_id") val locationPlaceId: String? = null,
)

/** One saved draft, as the list and the read return it. */
@Serializable
data class PostDraftDto(
    val id: String = "",
    @SerialName("post_type") val postType: String = "",
    val payload: PostDraftPayloadDto = PostDraftPayloadDto(),
    /** `draft`, `scheduled`, `publishing`, `published`, `blocked`. */
    val status: String = "",
    @SerialName("schedule_at") val scheduleAt: String? = null,
    @SerialName("published_post_id") val publishedPostId: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

/** What publishing a draft answers: the post it became (`201`, the post itself). Only the id is read. */
@Serializable
data class PostDraftPublishDto(val id: String = "")

/** The draft row's kind for a photo or text post. */
const val DRAFT_KIND_POST = "post"

/** A draft still waiting for its author. */
const val DRAFT_STATUS_DRAFT = "draft"
