package com.us.android.core.engagement.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * post-service's soft-delete lifecycle — delete, restore, and the viewer's
 * own "Recently deleted" list.
 *
 * Founder decision (2026-09-04): deleting a post is SOFT with a 30-day
 * restore window. The purge after that is server-side and invisible here —
 * the client never sends a hard delete.
 *
 * Lives beside the engagement endpoints rather than in `:feature:post`
 * because the delete row sits in the "more" sheet, which every feed surface
 * hosts, and the restore lives in Settings. Neither may depend on the other,
 * so the seam sits in the core module both can see.
 *
 * The contract, as the server agent is finishing it:
 *
 *  - `DELETE /v1/posts/{id}` → 204 (403 not yours, 404 unknown).
 *  - `POST /v1/posts/{id}/restore` → 200 with the post; 410, or 403 with a
 *    code, once the window has passed.
 *  - `GET /v1/posts/me/deleted?cursor=&limit=` → the platform envelope, a
 *    list under `data` and `meta.next_cursor` — the same page shape every
 *    other cursor-paged post list on this platform uses.
 *
 * Posts carry `deleted_at` and `purge_at` (RFC 3339, `omitempty`).
 */
interface PostLifecycleApi {

    /** 204 with no body, so `Unit` — see `noContentApiCall`. */
    @DELETE("v1/posts/{postId}")
    suspend fun deletePost(@Path("postId") postId: String)

    @POST("v1/posts/{postId}/restore")
    suspend fun restorePost(@Path("postId") postId: String): ApiEnvelope<DeletedPostDto>

    @GET("v1/posts/me/deleted")
    suspend fun listDeleted(
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int,
    ): ApiEnvelope<List<DeletedPostDto>>
}

/**
 * A post as the deleted list and the restore endpoint return it — only the
 * fields a compact row needs. Every field defaults: a partial row must not
 * fail the page.
 */
@Serializable
data class DeletedPostDto(
    val id: String = "",
    val text: String = "",
    @SerialName("post_type") val postType: String = "",
    @SerialName("content_type") val contentType: String = "",
    val media: List<DeletedMediaDto> = emptyList(),
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("deleted_at") val deletedAt: String = "",
    @SerialName("purge_at") val purgeAt: String = "",
)

/** The media reference, with the pre-signed variant URLs a thumbnail is drawn from. */
@Serializable
data class DeletedMediaDto(
    @SerialName("media_id") val mediaId: String = "",
    val kind: String = "",
    val blurhash: String = "",
    val variants: Map<String, String> = emptyMap(),
)
