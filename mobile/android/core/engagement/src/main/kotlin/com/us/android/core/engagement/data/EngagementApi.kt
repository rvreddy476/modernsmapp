package com.us.android.core.engagement.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * The engagement endpoints, as they exist in post-service today.
 *
 * CANONICAL SELECTION — REACTIONS
 *
 * post-service exposes three overlapping reaction mechanisms:
 *
 *   POST/DELETE /v1/posts/{id}/reactions  -> ScyllaDB  (handler React/Unreact)
 *   POST        /v1/posts/{id}/like       -> Redis     (handler ToggleLike)
 *   POST        /v1/posts/{id}/react      -> PostgreSQL(handler ToggleReaction)
 *
 * `/reactions` is the canonical one. The reason is RETRY SAFETY, established
 * against the running stack on 2026-08-21:
 *
 *  - `/reactions` is an explicit add/remove pair and is idempotent. POSTing it
 *    three times left `counts.likes` at 1 and `has_reacted` true; DELETE twice
 *    returned 200 both times and left the count at 0. `InteractionStore.React`
 *    reads before writing and returns early when a reaction already exists, so
 *    a retry after a lost response cannot double-count.
 *  - `/like` is a TOGGLE. The same request produced `liked:false` and then
 *    `liked:true` on consecutive calls. A client that retries a request whose
 *    response was lost has no way to know which state it will land in, which
 *    is exactly the situation an optimistic UI has to survive.
 *  - `/react` is currently broken for `like`: it answered
 *    `500 INTERNAL_ERROR` with `reactions_reaction_type_check` violated.
 *
 * An earlier version of this comment claimed `/like` writes somewhere the feed
 * never reads. That was wrong — `Service.ToggleLike` mirrors into Scylla via
 * `scyllaStore.React` after its Redis toggle, so it does move `has_reacted`.
 * It is rejected for the toggle semantics above, not for invisibility.
 *
 * No new endpoint or storage path is introduced here. The other two mechanisms
 * are left untouched for whatever already calls them; consolidating them is a
 * server-side decision with a data migration attached, not a client one.
 */
interface EngagementApi {

    /**
     * Add or change a reaction.
     *
     * Server-side idempotent by construction: `InteractionStore.React` reads
     * before writing and returns early when a reaction already exists, so a
     * duplicate tap cannot double-count.
     */
    @POST("v1/posts/{postId}/reactions")
    suspend fun addReaction(
        @Path("postId") postId: String,
        @Body body: ReactionRequest,
    ): ApiEnvelope<ReactionStatusDto>

    /** Remove the viewer's reaction. Absent reaction is a no-op, not an error. */
    @DELETE("v1/posts/{postId}/reactions")
    suspend fun removeReaction(
        @Path("postId") postId: String,
    ): ApiEnvelope<ReactionStatusDto>

    /** Bookmarks are viewer-private and persist in PostgreSQL. */
    @POST("v1/posts/{postId}/bookmark")
    suspend fun addBookmark(@Path("postId") postId: String): ApiEnvelope<BookmarkStatusDto>

    @DELETE("v1/posts/{postId}/bookmark")
    suspend fun removeBookmark(@Path("postId") postId: String): ApiEnvelope<BookmarkStatusDto>

    /**
     * Repost. The field is `type`, NOT `repost_type`.
     *
     * The product guide specified `repost_type`; the handler binds `type` with
     * values `plain` and `quote`. Sending the guide's name produces a bound
     * struct with an empty type.
     */
    @POST("v1/posts/{postId}/repost")
    suspend fun repost(
        @Path("postId") postId: String,
        @Body body: RepostRequest,
    ): ApiEnvelope<RepostDto>

    /**
     * Undo a repost.
     *
     * Returns 204 with no body, so this is declared as Unit rather than an
     * envelope — Retrofit cannot deserialize an empty body into one.
     */
    @DELETE("v1/posts/{postId}/repost")
    suspend fun removeRepost(@Path("postId") postId: String)

    /**
     * Records an external share.
     *
     * `share_type` is constrained server-side to `repost`, `quote` or
     * `external`. The client sends `external` only: `repost` and `quote` are
     * already produced by the repost endpoint, and counting one user action
     * through both would inflate the number.
     */
    @POST("v1/posts/{postId}/share")
    suspend fun share(
        @Path("postId") postId: String,
        @Body body: ShareRequest,
    ): ApiEnvelope<ShareResultDto>

    /** A page of comments, newest-first, via the platform cursor. */
    @GET("v1/posts/{postId}/comments")
    suspend fun getComments(
        @Path("postId") postId: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String?,
    ): ApiEnvelope<List<CommentDto>>

    /**
     * Create a comment.
     *
     * The request field is `text`; the response object calls the same value
     * `body`. That asymmetry is real — it is why this is two different types
     * rather than one round-tripped shape.
     *
     * The route is wrapped in post-service's `middleware.Idempotency(rdb)`,
     * which replays the first response for a repeated `Idempotency-Key`. That
     * is what makes "retry after a timeout" safe: without the header a retry
     * posts the comment twice, and the user cannot tell which one to delete.
     */
    @POST("v1/posts/{postId}/comments")
    suspend fun addComment(
        @Path("postId") postId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: CreateCommentRequest,
    ): ApiEnvelope<CommentDto>
}

// ── Requests ────────────────────────────────────────────────────────────

@Serializable
data class ReactionRequest(val reaction: String)

/**
 * No default on [type].
 *
 * kotlinx.serialization is configured with `encodeDefaults = false`, so a
 * property equal to its declared default is omitted from the wire. A default
 * here would serialize `{}` and the server would bind an empty type.
 */
@Serializable
data class RepostRequest(val type: String)

@Serializable
data class ShareRequest(@SerialName("share_type") val shareType: String)

@Serializable
data class CreateCommentRequest(val text: String)

// ── Responses ───────────────────────────────────────────────────────────

/** Captured `status` values: `reacted`, `unreacted`. */
@Serializable
data class ReactionStatusDto(val status: String = "")

@Serializable
data class BookmarkStatusDto(val status: String = "")

@Serializable
data class RepostDto(
    val id: String = "",
    @SerialName("original_post_id") val originalPostId: String = "",
)

@Serializable
data class ShareResultDto(
    @SerialName("share_count") val shareCount: Int = 0,
)

/**
 * A comment as the list and create endpoints return it.
 *
 * Carries `author_id` and no author presentation. Resolving a display name per
 * row would be one profile request per comment inside a scrolling list; the UI
 * therefore shows an honest minimal author until a batch hydration contract
 * exists. Every field defaults because a partial row must not fail the page.
 */
@Serializable
data class CommentDto(
    val id: String = "",
    @SerialName("post_id") val postId: String = "",
    @SerialName("author_id") val authorId: String = "",
    val body: String = "",
    @SerialName("like_count") val likeCount: Int = 0,
    @SerialName("reply_count") val replyCount: Int = 0,
    @SerialName("is_reply") val isReply: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
)
