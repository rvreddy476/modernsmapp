package com.us.android.feature.feed.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Wire types for feed-service, transcribed from the 2026-08-17 repair capture
 * (prompt/android-api-contracts.md §1).
 *
 * The 2026-08-16 pass only ever saw `{"data":[]}`, so this shape could not be
 * written then without inventing it. It is written now because a seeded stack
 * returned real items.
 */

/**
 * A hydrated feed item.
 *
 * Every field has a default: `score` is absent on home and present on the
 * ranked surfaces, and `media` is absent on a text post rather than an empty
 * array. A parser that required either would fail on half the fixtures.
 */
@Serializable
data class FeedItemDto(
    val id: String = "",
    @SerialName("author_id") val authorId: String = "",
    val text: String = "",
    val visibility: String = "",
    @SerialName("content_type") val contentType: String = "",
    @SerialName("feed_content_type") val feedContentType: String = "",
    @SerialName("post_type") val postType: String = "",
    @SerialName("app_origin") val appOrigin: String = "",
    @SerialName("share_to_postbook") val shareToPostbook: Boolean = false,
    @SerialName("is_pinned") val isPinned: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    val media: List<FeedMediaDto> = emptyList(),
    val counts: FeedCountsDto = FeedCountsDto(),
    @SerialName("view_count") val viewCount: Int = 0,
    @SerialName("is_bookmarked") val isBookmarked: Boolean = false,
    /** Ranked surfaces only. Home is chronological and omits it. */
    val score: Double? = null,
)

/** An id and a kind. No URL — see [com.us.android.core.model.FeedMedia]. */
@Serializable
data class FeedMediaDto(
    @SerialName("media_id") val mediaId: String = "",
    val kind: String = "",
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
 * `feed_type` returns `INVALID_REQUEST`. The response has no cursor envelope —
 * `has_more` lives inside `data`, and `newest_anchor` is the value the client
 * retains for the next call.
 */
@Serializable
data class FeedDeltaDto(
    @SerialName("new_count") val newCount: Int = 0,
    @SerialName("newest_anchor") val newestAnchor: String = "",
    @SerialName("has_more") val hasMore: Boolean = false,
)
