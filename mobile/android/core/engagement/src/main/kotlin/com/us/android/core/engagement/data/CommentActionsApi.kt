package com.us.android.core.engagement.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * post-service's COMMENT-level routes (2026-10-14, audit T-17): replies,
 * likes, delete, and the post author's pin and heart. Every one already
 * exists server-side — `internal/http/handler.go` registers the
 * `/v1/comments` group, `comment_routes.go` documents replies and
 * reactions, `comment_creator.go` documents heart and pin — and the gateway
 * forwards `/v1/comments` to post-service. Nothing here is a new route.
 *
 * A separate interface from [EngagementApi] on purpose: those are POST
 * routes keyed by a post id, these are keyed by a comment id, and keeping
 * them apart leaves every existing fake of [EngagementApi] untouched.
 *
 * The LIKE is the reaction pair, not `POST /v1/comments/:id/like`. The
 * legacy like is a TOGGLE, so a retried request lands in an unknowable
 * state; `PUT …/reaction {emoji}` and `DELETE …/reaction` are explicit and
 * idempotent, write the same `comment_reactions` table (the toggle writes
 * ❤️ there too), and answer the fresh count, which is what an optimistic
 * heart reconciles against.
 */
interface CommentActionsApi {

    /** `GET /v1/comments/:id/replies` — oldest first, `meta.next_cursor`. */
    @GET("v1/comments/{commentId}/replies")
    suspend fun replies(
        @Path("commentId") commentId: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String?,
    ): ApiEnvelope<List<CommentDto>>

    /**
     * `POST /v1/comments/:id/reply {text}` — open to everyone who may comment
     * on the post. A reply to a REPLY is filed under that reply's parent by
     * the server (one level of threading), so the client always sends the
     * top-level id, as the web does. Wrapped in the same idempotency
     * middleware as the comment create, hence the header.
     */
    @POST("v1/comments/{commentId}/reply")
    suspend fun reply(
        @Path("commentId") commentId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: CreateCommentRequest,
    ): ApiEnvelope<CommentDto>

    /** `PUT /v1/comments/:id/reaction {emoji}` — upserts the viewer's one reaction. */
    @PUT("v1/comments/{commentId}/reaction")
    suspend fun setReaction(
        @Path("commentId") commentId: String,
        @Body body: CommentReactionRequest,
    ): ApiEnvelope<CommentReactionSummaryDto>

    /** `DELETE /v1/comments/:id/reaction` — 200 with the summary, `viewer_reaction` null. */
    @DELETE("v1/comments/{commentId}/reaction")
    suspend fun removeReaction(@Path("commentId") commentId: String): ApiEnvelope<CommentReactionSummaryDto>

    /**
     * `DELETE /v1/comments/:id` — the AUTHOR's own comment only. The store
     * refuses anyone else with `NOT_COMMENT_AUTHOR` (403), the post's owner
     * included: there is no creator delete on post-service today.
     */
    @DELETE("v1/comments/{commentId}")
    suspend fun delete(@Path("commentId") commentId: String): ApiEnvelope<CommentStatusDto>

    /** `POST /v1/comments/:id/heart` — the post author only (403 otherwise). */
    @POST("v1/comments/{commentId}/heart")
    suspend fun heart(@Path("commentId") commentId: String): ApiEnvelope<CommentHeartDto>

    @DELETE("v1/comments/{commentId}/heart")
    suspend fun unheart(@Path("commentId") commentId: String): ApiEnvelope<CommentHeartDto>

    /**
     * `PUT /v1/comments/:id/pin` — the post author only; replaces any other
     * pin on the post; `422 CANNOT_PIN_REPLY` for a reply.
     */
    @PUT("v1/comments/{commentId}/pin")
    suspend fun pin(@Path("commentId") commentId: String): ApiEnvelope<CommentPinDto>

    @DELETE("v1/comments/{commentId}/pin")
    suspend fun unpin(@Path("commentId") commentId: String): ApiEnvelope<CommentPinDto>
}

// ── Requests ────────────────────────────────────────────────────────────

/** The PUT body; post-service's `commentReactionRequest`. No default: see [RepostRequest]. */
@Serializable
data class CommentReactionRequest(val emoji: String)

// ── Responses ───────────────────────────────────────────────────────────

/** `CommentReactionSummary` — the answer to both reaction routes. */
@Serializable
data class CommentReactionSummaryDto(
    val emoji: String? = null,
    @SerialName("reaction_count") val reactionCount: Int = 0,
    @SerialName("viewer_reaction") val viewerReaction: String? = null,
)

/** `{"status":"deleted"}`. */
@Serializable
data class CommentStatusDto(val status: String = "")

/** `CommentHeartResult`. */
@Serializable
data class CommentHeartDto(
    @SerialName("comment_id") val commentId: String = "",
    @SerialName("hearted_by_author") val heartedByAuthor: Boolean = false,
)

/** `CommentPinResult`. */
@Serializable
data class CommentPinDto(
    @SerialName("comment_id") val commentId: String = "",
    @SerialName("post_id") val postId: String = "",
    val pinned: Boolean = false,
)
