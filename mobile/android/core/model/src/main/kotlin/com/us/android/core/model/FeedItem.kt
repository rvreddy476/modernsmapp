package com.us.android.core.model

/**
 * One entry in a feed.
 *
 * Feed items are HYDRATED posts, not post-id rows. The 2026-08-17 capture
 * settled this: `GET /v1/feed/home` returns text, author, counts, media
 * references and viewer state inline, so a feed can render without a per-item
 * call to post detail. That is the difference between one request per screen
 * and one request per row.
 *
 * It is deliberately NOT [Post]. The two overlap but are not the same shape:
 * a feed item carries `feed_content_type`, `app_origin` and an optional
 * ranking `score`, and it lacks `is_repostable`, `no_comments`, `no_likes` and
 * the distribution metadata that post detail returns. Modelling them as one
 * type would force every absent field to be nullable and lose the ability to
 * say which surface a value came from.
 */
data class FeedItem(
    val id: String,
    val authorId: String,
    val text: String,
    val visibility: String,
    /** `post`, `flick`, `long_video`, … — the feed's own classification. */
    val feedContentType: String,
    /** `text`, `image`, `video`, … — how to render the body. */
    val postType: String,
    val createdAt: String,
    val isPinned: Boolean,
    val media: List<FeedMedia>,
    val counts: FeedCounts,
    val isBookmarked: Boolean,
    /**
     * Ranking score, present only on the ranked surfaces (reels, videos,
     * watch). Home is chronological and omits it. Kept because its presence
     * is itself the signal that a surface is ranked.
     */
    val score: Double? = null,
)

/**
 * A media reference — an id and a kind, NOT a delivery URL.
 *
 * Rendering the bytes needs a second call to `GET /v1/media/:id/url`, which
 * returns signed variants and an HLS master path. That is an N+1 against the
 * feed and is recorded as a contract gap rather than worked around here: the
 * client must not construct a URL from a storage key, and the capture says so
 * explicitly.
 */
data class FeedMedia(
    val mediaId: String,
    /** `image` or `video` in the captured fixtures. */
    val kind: String,
)

data class FeedCounts(
    val likes: Int,
    val comments: Int,
    val views: Int,
)

/**
 * Which feed surface to load.
 *
 * A closed enum rather than a string, because the surfaces differ in ways the
 * caller must respect: only [Home] returns `meta.next_cursor`. The ranked
 * surfaces returned a full page with no cursor at all, and the capture is
 * explicit that the client must not invent one.
 */
enum class FeedSurface(val path: String, val paginated: Boolean) {
    Home("home", paginated = true),
    Reels("reels", paginated = false),
    Videos("videos", paginated = false),
    Watch("watch", paginated = false),
}
