package com.us.android.core.feed.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.Path

/**
 * The owner's edit of a published post (2026-10-21, the web's Edit sheet) —
 * post-service `internal/http/post_edit.go`:
 *
 *     GET   /v1/posts/:id   the post detail, owner fields included
 *     PATCH /v1/posts/:id   owner only; any subset of the fields below → the post detail
 *
 * Absent = leave it (every field of [PostEditPatch] is optional and the
 * app's JSON drops nulls); `recording_date` takes "" to clear. Refusals: 403
 * FORBIDDEN (not the owner), 404 NOT_FOUND, and 422 with the field's own
 * code (TITLE_TOO_LONG, INVALID_VISIBILITY, INVALID_HASHTAG, …) — see
 * [postEditRefusal].
 *
 * Its own interface rather than two more members on [FeedApi]: the feed's
 * fakes would otherwise grow methods no feed screen calls.
 */
interface PostEditApi {

    @GET("v1/posts/{postId}")
    suspend fun detail(@Path("postId") postId: String): ApiEnvelope<PostEditDetailDto>

    @PATCH("v1/posts/{postId}")
    suspend fun update(@Path("postId") postId: String, @Body body: PostEditPatch): ApiEnvelope<PostEditDetailDto>
}

/**
 * The owner-editable fields of `PostDetail`, by the tags post-service writes
 * (`store/postgres/posts.go`). Defaults are what an absent field means there:
 * `omitempty` strings read as the server's own defaults.
 */
@Serializable
data class PostEditDetailDto(
    val id: String = "",
    val title: String = "",
    val text: String = "",
    val visibility: String = "",
    val status: String = "",
    @SerialName("content_type") val contentType: String = "",
    @SerialName("post_type") val postType: String = "",
    val category: String = "",
    @SerialName("category_slug") val categorySlug: String = "",
    val language: String = "",
    val tags: List<String> = emptyList(),
    val hashtags: List<String> = emptyList(),
    @SerialName("allow_download") val allowDownload: Boolean = true,
    @SerialName("no_comments") val noComments: Boolean = false,
    @SerialName("is_made_for_kids") val madeForKids: Boolean = false,
    @SerialName("paid_promotion") val paidPromotion: Boolean = false,
    @SerialName("altered_content") val alteredContent: Boolean = false,
    val license: String = "",
    @SerialName("allow_embedding") val allowEmbedding: Boolean = true,
    /** An RFC 3339 instant on the wire; only its date is edited. */
    @SerialName("recording_date") val recordingDate: String? = null,
    @SerialName("recording_location") val recordingLocation: String = "",
    @SerialName("remix_setting") val remixSetting: String = "",
    @SerialName("comment_moderation") val commentModeration: String = "",
    @SerialName("comment_access") val commentAccess: String = "",
    @SerialName("age_restricted") val ageRestricted: Boolean = false,
    @SerialName("hide_like_count") val hideLikeCount: Boolean = false,
    @SerialName("default_comment_sort") val defaultCommentSort: String = "",
)

/**
 * The PATCH body: ONLY the fields that moved (the web's `detailsPatch`). Null
 * is "leave it" and is not sent — the app's JSON omits nulls.
 */
@Serializable
data class PostEditPatch(
    val title: String? = null,
    val text: String? = null,
    val category: String? = null,
    val tags: List<String>? = null,
    val hashtags: List<String>? = null,
    val language: String? = null,
    val visibility: String? = null,
    @SerialName("allow_download") val allowDownload: Boolean? = null,
    @SerialName("no_comments") val noComments: Boolean? = null,
    @SerialName("made_for_kids") val madeForKids: Boolean? = null,
    @SerialName("paid_promotion") val paidPromotion: Boolean? = null,
    @SerialName("altered_content") val alteredContent: Boolean? = null,
    val license: String? = null,
    @SerialName("allow_embedding") val allowEmbedding: Boolean? = null,
    /** "YYYY-MM-DD", or "" to clear. */
    @SerialName("recording_date") val recordingDate: String? = null,
    @SerialName("recording_location") val recordingLocation: String? = null,
    @SerialName("remix_setting") val remixSetting: String? = null,
    @SerialName("comment_moderation") val commentModeration: String? = null,
    @SerialName("comment_access") val commentAccess: String? = null,
    @SerialName("age_restricted") val ageRestricted: Boolean? = null,
    @SerialName("hide_like_count") val hideLikeCount: Boolean? = null,
    @SerialName("default_comment_sort") val defaultCommentSort: String? = null,
) {
    /** Nothing moved: Save has nothing to send. */
    val isEmpty: Boolean get() = this == PostEditPatch()

    /** Two saves in one session, as one: the newer value of each field wins. */
    @Suppress("CyclomaticComplexMethod") // One elvis per field: a table, not logic.
    fun then(newer: PostEditPatch): PostEditPatch = PostEditPatch(
        title = newer.title ?: title,
        text = newer.text ?: text,
        category = newer.category ?: category,
        tags = newer.tags ?: tags,
        hashtags = newer.hashtags ?: hashtags,
        language = newer.language ?: language,
        visibility = newer.visibility ?: visibility,
        allowDownload = newer.allowDownload ?: allowDownload,
        noComments = newer.noComments ?: noComments,
        madeForKids = newer.madeForKids ?: madeForKids,
        paidPromotion = newer.paidPromotion ?: paidPromotion,
        alteredContent = newer.alteredContent ?: alteredContent,
        license = newer.license ?: license,
        allowEmbedding = newer.allowEmbedding ?: allowEmbedding,
        recordingDate = newer.recordingDate ?: recordingDate,
        recordingLocation = newer.recordingLocation ?: recordingLocation,
        remixSetting = newer.remixSetting ?: remixSetting,
        commentModeration = newer.commentModeration ?: commentModeration,
        commentAccess = newer.commentAccess ?: commentAccess,
        ageRestricted = newer.ageRestricted ?: ageRestricted,
        hideLikeCount = newer.hideLikeCount ?: hideLikeCount,
        defaultCommentSort = newer.defaultCommentSort ?: defaultCommentSort,
    )
}
