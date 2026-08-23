package com.us.android.feature.feed.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Wire types for feed-service, transcribed from the 2026-08-17 captures
 * (prompt/android-api-contracts.md §1 and the evidence-pass section).
 *
 * Every field has a default. That is not habit: `score` is absent on home and
 * present on ranked surfaces, `media` is absent on a text post rather than an
 * empty array, `hls_url` is absent for an image, and `viewer_reaction` is
 * omitted entirely when unauthenticated or unset. A parser that required any
 * of them would fail on half the real fixtures.
 */

/** A hydrated feed item — renders a whole row with no follow-up call. */
@Serializable
data class FeedItemDto(
    val id: String = "",
    @SerialName("author_id") val authorId: String = "",
    val author: FeedAuthorDto = FeedAuthorDto(),
    val text: String = "",
    val visibility: String = "",
    @SerialName("content_type") val contentType: String = "",
    @SerialName("feed_content_type") val feedContentType: String = "",
    @SerialName("post_type") val postType: String = "",
    @SerialName("app_origin") val appOrigin: String = "",
    @SerialName("is_pinned") val isPinned: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    val media: List<FeedMediaDto> = emptyList(),
    val counts: FeedCountsDto = FeedCountsDto(),
    @SerialName("view_count") val viewCount: Int = 0,
    @SerialName("repost_count") val repostCount: Int = 0,
    @SerialName("is_bookmarked") val isBookmarked: Boolean = false,
    @SerialName("has_reacted") val hasReacted: Boolean = false,
    @SerialName("has_reposted") val hasReposted: Boolean = false,
    @SerialName("viewer_reaction") val viewerReaction: String? = null,
    @SerialName("is_repostable") val isRepostable: Boolean = false,
    /** Ranked surfaces only. Home is chronological and omits it. */
    val score: Double? = null,
)

/**
 * Embedded author.
 *
 * Defaults to an empty object rather than being nullable: the server always
 * sends `author`, and substituting a non-enumerating placeholder for a deleted
 * profile is its job, not the client's.
 */
@Serializable
data class FeedAuthorDto(
    val id: String = "",
    @SerialName("display_name") val displayName: String = "",
    val username: String? = null,
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
)

/**
 * A media reference with its authorized delivery fields.
 *
 * `variants` values are absolute pre-signed URLs; `hls_url` is a
 * gateway-relative authorized path. They are NOT interchangeable — see
 * [com.us.android.core.model.FeedMedia].
 */
@Serializable
data class FeedMediaDto(
    @SerialName("media_id") val mediaId: String = "",
    val kind: String = "",
    /**
     * The author's accessibility decision — Slice C, C-CLB-3.
     *
     * Defaulted for the same reason as [com.us.android.feature.post.data.dto.PostMediaDto]:
     * older posts do not carry these and must still decode.
     */
    @SerialName("alt_text") val altText: String = "",
    @SerialName("alt_decorative") val altDecorative: Boolean = false,
    val status: String = "",
    val width: Int = 0,
    val height: Int = 0,
    val blurhash: String = "",
    val variants: Map<String, String> = emptyMap(),
    @SerialName("hls_url") val hlsUrl: String? = null,
    /** Five minutes after issue, per the captured contract. */
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class FeedCountsDto(
    val likes: Int = 0,
    val comments: Int = 0,
)

/**
 * `GET /v1/feed/delta`.
 *
 * Not a list request: `feed_type` and `anchor` are required, and omitting
 * `feed_type` returns `INVALID_REQUEST`. No cursor envelope — `has_more` lives
 * inside `data`, and `newest_anchor` is what the client retains.
 */
@Serializable
data class FeedDeltaDto(
    @SerialName("new_count") val newCount: Int = 0,
    @SerialName("newest_anchor") val newestAnchor: String = "",
    @SerialName("has_more") val hasMore: Boolean = false,
)
