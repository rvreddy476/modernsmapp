package com.us.android.feature.post.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Wire types for post-service, transcribed from a live capture on 2026-08-16
 * (prompt/android-api-contracts.md §2).
 *
 * Every field has a default and every nullable field is genuinely nullable on
 * the wire — `rich_text` came back as `null` on the captured post. A strict
 * parser here blanks the whole screen for a field nothing renders yet.
 */

/**
 * `GET /v1/posts/:postId`.
 *
 * Auth: none for a public approved post. Supplying the JWT changes
 * viewer-specific fields ([isBookmarked]), which is why an anonymous response
 * must not be cached and later shown to a signed-in viewer.
 *
 * The distribution and moderation fields are declared but unused by the UI.
 * They are kept so the shape stays a faithful record of the contract, and so
 * a future surface that needs `license` or `remix_setting` finds it already
 * modelled rather than re-deriving it from a fresh capture.
 */
@Serializable
data class PostDto(
    val id: String = "",
    /**
     * Asset REFERENCES only — `{media_id, kind}`. Unlike the feed, this
     * payload carries no dimensions and no delivery URLs, so the client
     * resolves each asset through media-service. Captured live 2026-08-18.
     */
    val media: List<PostMediaDto> = emptyList(),
    @SerialName("author_id") val authorId: String = "",
    val text: String = "",
    val visibility: String = "",
    @SerialName("content_type") val contentType: String = "",
    @SerialName("post_type") val postType: String = "",
    @SerialName("is_pinned") val isPinned: Boolean = false,
    /** Null on the captured post. Structured body; no renderer yet. */
    @SerialName("rich_text") val richText: kotlinx.serialization.json.JsonElement? = null,
    /** Author-set switches, expressed as negatives on the wire. */
    @SerialName("no_comments") val noComments: Boolean = false,
    @SerialName("no_likes") val noLikes: Boolean = false,
    @SerialName("app_origin") val appOrigin: String = "",
    @SerialName("review_status") val reviewStatus: String = "",
    val language: String = "",
    @SerialName("comment_access") val commentAccess: String = "",
    @SerialName("comment_moderation") val commentModeration: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    val counts: PostCountsDto = PostCountsDto(),
    @SerialName("view_count") val viewCount: Int = 0,
    @SerialName("repost_count") val repostCount: Int = 0,
    @SerialName("is_bookmarked") val isBookmarked: Boolean = false,
    /**
     * Per-viewer engagement, all three captured live on 2026-08-21 from
     * `GET /v1/posts/{id}` with an engaged viewer:
     *
     *   "viewer_reaction":"love","has_reacted":true,"is_bookmarked":true,
     *   "repost_count":1,"has_reposted":true
     *
     * `viewer_reaction` is `omitempty` on the wire — absent, not null, when
     * there is no reaction — which is why it is nullable with a null default.
     * `has_reacted` remains the binary the UI trusts.
     */
    @SerialName("has_reacted") val hasReacted: Boolean = false,
    @SerialName("viewer_reaction") val viewerReaction: String? = null,
    @SerialName("has_reposted") val hasReposted: Boolean = false,
    @SerialName("is_repostable") val isRepostable: Boolean = false,
)

/** Nested object, not flat fields — `counts.likes`, `counts.comments`. */
@Serializable
data class PostCountsDto(
    val likes: Int = 0,
    val comments: Int = 0,
)

/**
 * `POST /v1/posts/:postId/reactions`.
 *
 * Captured validation: an empty object returns `INVALID_REQUEST` with
 * `Key: 'ReactionRequest.Reaction' ... failed on the 'required' tag`.
 */
@Serializable
data class ReactionRequest(
    val reaction: String,
)

/** Captured [status] values: `reacted`, `unreacted`. */
@Serializable
data class ReactionStatusDto(
    val status: String = "",
)

/**
 * `POST /v1/posts/:postId/bookmark`.
 *
 * This endpoint is a TOGGLE, not an idempotent create, and the returned
 * boolean is the ONLY reliable statement of the resulting state. The client
 * must adopt it rather than assume the flip it intended — a repeated call
 * lands in the opposite state.
 */
@Serializable
data class BookmarkStatusDto(
    val bookmarked: Boolean = false,
)

/**
 * `POST /v1/posts/:postId/repost`.
 *
 * Captured validation: an empty object returns `INVALID_REQUEST` with
 * `Key: 'CreateRepostRequest.Type' ... failed on the 'required' tag`.
 *
 * [type] deliberately has NO Kotlin default. The app's `Json` leaves
 * `encodeDefaults` at its default of false, so a property equal to its
 * declared default is OMITTED from the request body — `RepostRequest()` would
 * serialize to `{}` and the server would reject it with exactly the error
 * above. A required wire field must never carry a Kotlin default.
 */
@Serializable
data class RepostRequest(
    val type: String,
)

/** Returned with HTTP 201 on repost. */
@Serializable
data class RepostDto(
    val id: String = "",
    @SerialName("original_post_id") val originalPostId: String = "",
    val type: String = "",
    val status: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

/**
 * A post's attachment as the post payload describes it: an id and a kind.
 *
 * Deliberately NOT the feed's media shape. The feed's is hydrated with
 * variants and dimensions; this one is a pointer. Sharing one type would
 * invite code to read `variants` here and find it always empty.
 */
@Serializable
data class PostMediaDto(
    @SerialName("media_id") val mediaId: String = "",
    val kind: String = "",
    /**
     * The carousel ordinal — Creator Studio P0-A.
     *
     * Defaults to -1, NEVER 0. A payload cached before this field existed has
     * no ordinal, and defaulting to 0 would make every item claim to be first;
     * the contiguity check would then reject the whole post and the reader
     * would lose it entirely. See CarouselOrdinals.
     */
    val position: Int = com.us.android.core.model.CarouselOrdinals.ABSENT,
    /**
     * The author's accessibility decision — Slice C, C-CLB-3.
     *
     * Defaulted, because posts created before this field existed do not carry
     * it and a strict decoder would otherwise fail the whole read. The default
     * is the honest one: no description, not marked decorative — "nobody said".
     */
    @SerialName("alt_text") val altText: String = "",
    @SerialName("alt_decorative") val altDecorative: Boolean = false,
)
