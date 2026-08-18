package com.us.android.core.model

/**
 * One entry in a feed.
 *
 * Feed items are HYDRATED posts, not post-id rows, and as of the 2026-08-17
 * server-side hydration closure they are hydrated far enough to render a whole
 * row with no follow-up call: author identity, authorized media delivery, and
 * per-viewer action state all arrive inline.
 *
 * That is the difference between one request per screen and one request per
 * row. The client deliberately does NOT resolve any of it per item — the
 * earlier design, where the card fell back to a truncated user id and a
 * placeholder for media, existed only because the server did not yet send
 * these fields.
 *
 * It is still NOT [Post]. The two overlap but differ: a feed item carries
 * `feed_content_type`, `app_origin` and an optional ranking `score`, and lacks
 * the distribution metadata post detail returns. Modelling them as one type
 * would force every absent field to be nullable.
 */
data class FeedItem(
    val id: String,
    val authorId: String,
    val author: FeedAuthor,
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
    val viewer: FeedViewerState,
    val isRepostable: Boolean,
    /**
     * Ranking score, present only on the ranked surfaces (reels, videos,
     * watch). Home is chronological and omits it. Kept because its presence is
     * itself the signal that a surface is ranked.
     */
    val score: Double? = null,
)

/**
 * Minimal author identity, embedded by the server's page-level profile batch.
 *
 * [displayName] can legitimately be blank — `PUT /v1/profiles/me` is a full
 * replacement that clears strings — and the server substitutes a
 * non-enumerating "Deleted account" for a profile that genuinely no longer
 * exists. Neither case should render an empty row, which is what
 * [nameForDisplay] handles.
 */
data class FeedAuthor(
    val id: String,
    val displayName: String,
    val username: String? = null,
    val avatarMediaId: String? = null,
) {
    val nameForDisplay: String get() = displayName.ifBlank { username ?: "Unnamed" }
}

/**
 * A media reference WITH its authorized delivery URLs.
 *
 * Two URL kinds live here and they behave nothing alike:
 *
 *  - [hlsUrl] is **gateway-relative** (`/v1/media/:id/hls/master.m3u8`) and the
 *    route is authorized, so it must be resolved against the API base URL and
 *    fetched with the bearer token.
 *  - [variants] values are **absolute, pre-signed object-store URLs** and must
 *    be used exactly as returned.
 *
 * Resolving them is `core/media`'s `MediaUrlResolver`, never string
 * concatenation here. Storage keys elsewhere in the payload must never be used
 * to build a URL — the contract capture warns against it explicitly.
 */
data class FeedMedia(
    val mediaId: String,
    /** `image` or `video` in the captured fixtures. */
    val kind: String,
    val status: String = "",
    val width: Int = 0,
    val height: Int = 0,
    /** Compact placeholder gradient; render while the real bytes load. */
    val blurhash: String = "",
    val variants: Map<String, String> = emptyMap(),
    val hlsUrl: String? = null,
    /**
     * When the signed URLs above stop working — five minutes after issue, per
     * the captured contract. A cached page that outlives this must refresh via
     * the media batch endpoint rather than retrying a dead URL.
     */
    val expiresAt: String? = null,
) {
    val isReady: Boolean get() = status == "ready"
    val isVertical: Boolean get() = height > width
}

data class FeedCounts(
    val likes: Int,
    val comments: Int,
    val reposts: Int,
    val views: Int,
)

/**
 * Per-viewer state, now server-authoritative.
 *
 * Previously [hasReacted] and [hasReposted] could only be inferred from taps
 * made in the current session, because the payload carried no per-viewer flag —
 * so a cold start showed every post as un-acted regardless of history. The
 * server now sends all three, and [viewerReaction] names the specific reaction
 * when one is set.
 */
data class FeedViewerState(
    val isBookmarked: Boolean,
    val hasReacted: Boolean,
    val hasReposted: Boolean,
    /**
     * One of `like`, `love`, `haha`, `wow`, `sad`, `angry`. Present only when
     * authenticated AND a reaction is set; absent otherwise. [hasReacted] is
     * the authoritative binary state.
     */
    val viewerReaction: String? = null,
)

/**
 * Which feed surface to load.
 *
 * All four now paginate. Home returns an RFC3339 timestamp cursor; the ranked
 * surfaces return an opaque base64 timeuuid token. Both are replayed verbatim
 * and never parsed or constructed.
 */
enum class FeedSurface(val path: String) {
    Home("home"),
    Reels("reels"),
    Videos("videos"),
    Watch("watch"),
}
